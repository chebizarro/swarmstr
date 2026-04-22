// Package toolbuiltin/system_process provides persistent process handles:
//   - process_spawn  → start a background process, get a handle ID
//   - process_read   → read buffered stdout/stderr from a handle
//   - process_send   → write to a process's stdin
//   - process_kill   → terminate a process
//   - process_list   → list active process handles
package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"metiq/internal/agent"
)

const maxActiveProcesses = 5

// ─── ring buffer for process output ───────────────────────────────────────────

// outputRing is a thread-safe ring buffer that stores the last N bytes.
type outputRing struct {
	mu   sync.Mutex
	buf  []byte
	size int
	pos  int // write position (wraps)
	full bool
	// readPos tracks how much the consumer has read (absolute offset).
	totalWritten int64
	lastReadAt   int64
}

func newOutputRing(size int) *outputRing {
	return &outputRing{buf: make([]byte, size), size: size}
}

func (r *outputRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	for _, b := range p {
		r.buf[r.pos] = b
		r.pos = (r.pos + 1) % r.size
		if r.pos == 0 {
			r.full = true
		}
	}
	r.totalWritten += int64(n)
	return n, nil
}

// ReadNew returns bytes written since the last ReadNew call.
// If the buffer has wrapped past the last read position, returns all available data
// with a "[...truncated...]" prefix.
func (r *outputRing) ReadNew() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	available := r.totalWritten - r.lastReadAt
	if available <= 0 {
		return ""
	}

	var result []byte
	truncated := false

	if available > int64(r.size) {
		// Buffer has wrapped past what we last read — return everything we have.
		truncated = true
		available = int64(r.size)
	}

	// Read `available` bytes ending at current write position.
	start := (r.pos - int(available) + r.size) % r.size
	if start < r.pos {
		result = make([]byte, r.pos-start)
		copy(result, r.buf[start:r.pos])
	} else {
		result = make([]byte, 0, int(available))
		result = append(result, r.buf[start:]...)
		result = append(result, r.buf[:r.pos]...)
	}

	r.lastReadAt = r.totalWritten

	if truncated {
		return "[...truncated...]\n" + string(result)
	}
	return string(result)
}

// ReadAll returns all buffered content (up to ring size).
func (r *outputRing) ReadAll() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.totalWritten == 0 {
		return ""
	}

	n := int(r.totalWritten)
	if n > r.size {
		n = r.size
	}

	start := (r.pos - n + r.size) % r.size
	var result []byte
	if start < r.pos {
		result = make([]byte, r.pos-start)
		copy(result, r.buf[start:r.pos])
	} else {
		result = make([]byte, 0, n)
		result = append(result, r.buf[start:]...)
		result = append(result, r.buf[:r.pos]...)
	}
	return string(result)
}

// ─── process entry ────────────────────────────────────────────────────────────

type processEntry struct {
	id        string
	sessionID string
	command   string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *outputRing
	stderr    *outputRing
	startedAt time.Time
	done      chan struct{} // closed when process exits
	exitCode  int
	exitErr   string
	exited    atomic.Bool
	cancel    context.CancelFunc
}

// ─── process registry ─────────────────────────────────────────────────────────

// ProcessRegistry manages background process handles.
type ProcessRegistry struct {
	mu      sync.Mutex
	entries map[string]*processEntry
	nextID  int
	bgCtx   context.Context    // long-lived context for spawned processes
	bgCancel context.CancelFunc
}

func NewProcessRegistry() *ProcessRegistry {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProcessRegistry{
		entries:  make(map[string]*processEntry),
		bgCtx:   ctx,
		bgCancel: cancel,
	}
}

// Shutdown cancels all spawned processes and cleans up.
func (r *ProcessRegistry) Shutdown() {
	r.bgCancel()
}

func (r *ProcessRegistry) spawn(_ context.Context, sessionID, command, dir string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Count active (non-exited) processes.
	active := 0
	for _, e := range r.entries {
		if !e.exited.Load() {
			active++
		}
	}
	if active >= maxActiveProcesses {
		return "", fmt.Errorf("process_spawn: max %d active processes reached; kill one first", maxActiveProcesses)
	}

	r.nextID++
	id := fmt.Sprintf("proc_%d", r.nextID)

	// Use registry-owned context so the process outlives the tool call.
	procCtx, procCancel := context.WithCancel(r.bgCtx)

	cmd := exec.CommandContext(procCtx, "/bin/sh", "-c", command)
	if dir != "" {
		cmd.Dir = dir
	}
	// Start in own process group so we can kill the entire tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutRing := newOutputRing(64 * 1024) // 64KB per stream
	stderrRing := newOutputRing(64 * 1024)
	cmd.Stdout = stdoutRing
	cmd.Stderr = stderrRing

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		procCancel()
		return "", fmt.Errorf("process_spawn: stdin pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		procCancel()
		return "", fmt.Errorf("process_spawn: %v", err)
	}

	entry := &processEntry{
		id:        id,
		sessionID: sessionID,
		command:   command,
		cmd:       cmd,
		stdin:     stdinPipe,
		stdout:    stdoutRing,
		stderr:    stderrRing,
		startedAt: time.Now(),
		done:      make(chan struct{}),
		cancel:    procCancel,
	}

	// Background goroutine to wait for exit.
	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				entry.exitCode = exitErr.ExitCode()
			} else {
				entry.exitCode = -1
				entry.exitErr = waitErr.Error()
			}
		}
		entry.stdin.Close()
		entry.exited.Store(true)
		close(entry.done)
	}()

	r.entries[id] = entry
	return id, nil
}

func (r *ProcessRegistry) read(id string) (map[string]any, error) {
	r.mu.Lock()
	entry, ok := r.entries[id]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("process_read: unknown handle %q", id)
	}

	result := map[string]any{
		"id":      id,
		"running": !entry.exited.Load(),
		"stdout":  entry.stdout.ReadNew(),
		"stderr":  entry.stderr.ReadNew(),
	}
	if entry.exited.Load() {
		result["exit_code"] = entry.exitCode
		if entry.exitErr != "" {
			result["exit_error"] = entry.exitErr
		}
	}
	return result, nil
}

func (r *ProcessRegistry) send(id, input string) error {
	r.mu.Lock()
	entry, ok := r.entries[id]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("process_send: unknown handle %q", id)
	}
	if entry.exited.Load() {
		return fmt.Errorf("process_send: process %q has exited", id)
	}
	_, err := io.WriteString(entry.stdin, input)
	if err != nil {
		return fmt.Errorf("process_send: %v", err)
	}
	return nil
}

func (r *ProcessRegistry) kill(id string) (map[string]any, error) {
	r.mu.Lock()
	entry, ok := r.entries[id]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("process_kill: unknown handle %q", id)
	}

	if !entry.exited.Load() {
		// Kill the entire process group (not just the shell).
		if entry.cmd.Process != nil {
			_ = syscall.Kill(-entry.cmd.Process.Pid, syscall.SIGKILL)
		}
		entry.stdin.Close()
		entry.cancel()
		// Wait for exit with timeout.
		select {
		case <-entry.done:
		case <-time.After(5 * time.Second):
		}
	}

	// Collect final output.
	result := map[string]any{
		"id":        id,
		"killed":    true,
		"exit_code": entry.exitCode,
		"stdout":    entry.stdout.ReadAll(),
		"stderr":    entry.stderr.ReadAll(),
	}
	if entry.exitErr != "" {
		result["exit_error"] = entry.exitErr
	}

	// Remove from registry.
	r.mu.Lock()
	delete(r.entries, id)
	r.mu.Unlock()

	return result, nil
}

func (r *ProcessRegistry) list() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	var items []map[string]any
	for _, e := range r.entries {
		item := map[string]any{
			"id":         e.id,
			"command":    e.command,
			"session_id": e.sessionID,
			"running":    !e.exited.Load(),
			"started_at": e.startedAt.UTC().Format(time.RFC3339),
			"uptime_sec": int(time.Since(e.startedAt).Seconds()),
		}
		if e.exited.Load() {
			item["exit_code"] = e.exitCode
		}
		items = append(items, item)
	}
	return items
}

// CleanupSession kills all processes belonging to a session.
func (r *ProcessRegistry) CleanupSession(sessionID string) {
	r.mu.Lock()
	var toKill []string
	for id, e := range r.entries {
		if e.sessionID == sessionID {
			toKill = append(toKill, id)
		}
	}
	r.mu.Unlock()

	for _, id := range toKill {
		r.kill(id) //nolint:errcheck
	}
}

// ─── tool functions ───────────────────────────────────────────────────────────

func ProcessSpawnTool(reg *ProcessRegistry) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		command := agent.ArgString(args, "command")
		if strings.TrimSpace(command) == "" {
			return "", fmt.Errorf("process_spawn: 'command' argument is required")
		}
		dir := agent.ArgString(args, "directory")

		// Extract session ID from the tool-call context.
		sessionID := agent.SessionIDFromContext(ctx)

		id, err := reg.spawn(ctx, sessionID, command, dir)
		if err != nil {
			return "", err
		}

		// Give the process a moment to produce initial output.
		time.Sleep(100 * time.Millisecond)

		// Use the locked read method instead of accessing entries directly.
		readResult, err := reg.read(id)
		if err != nil {
			return "", err
		}
		readResult["command"] = command

		raw, _ := json.Marshal(readResult)
		return string(raw), nil
	}
}

func ProcessReadTool(reg *ProcessRegistry) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("process_read: 'id' argument is required")
		}

		waitSec := agent.ArgInt(args, "wait_seconds", 0)
		if waitSec > 0 {
			if waitSec > 30 {
				waitSec = 30
			}
			// Wait for new output or process exit, whichever comes first.
			reg.mu.Lock()
			entry, ok := reg.entries[id]
			reg.mu.Unlock()
			if ok && !entry.exited.Load() {
				timer := time.NewTimer(time.Duration(waitSec) * time.Second)
				select {
				case <-entry.done:
				case <-timer.C:
				}
				timer.Stop()
			}
		}

		result, err := reg.read(id)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}
}

func ProcessSendTool(reg *ProcessRegistry) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("process_send: 'id' argument is required")
		}
		input := agent.ArgString(args, "input")
		if input == "" {
			return "", fmt.Errorf("process_send: 'input' argument is required")
		}
		// Append newline if not present (most interactive programs expect it).
		if !strings.HasSuffix(input, "\n") {
			input += "\n"
		}
		if err := reg.send(id, input); err != nil {
			return "", err
		}

		// Brief pause to let the process respond.
		time.Sleep(50 * time.Millisecond)

		result, err := reg.read(id)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}
}

func ProcessKillTool(reg *ProcessRegistry) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("process_kill: 'id' argument is required")
		}
		result, err := reg.kill(id)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}
}

func ProcessListTool(reg *ProcessRegistry) agent.ToolFunc {
	return func(_ context.Context, _ map[string]any) (string, error) {
		items := reg.list()
		if len(items) == 0 {
			return `{"processes":[]}`, nil
		}
		raw, _ := json.Marshal(map[string]any{"processes": items})
		return string(raw), nil
	}
}

// ─── tool definitions ─────────────────────────────────────────────────────────

var ProcessSpawnDef = agent.ToolDefinition{
	Name:        "process_spawn",
	Description: "Start a long-running background process and return a handle ID. Use for dev servers, file watchers, log tailers, or any process you need to interact with over time. The process runs in the background; use process_read to check output, process_send to write to stdin, and process_kill to stop it. Max 5 concurrent processes.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"command": {
				Type:        "string",
				Description: "Shell command to run, e.g. \"npm run dev\" or \"tail -f /var/log/syslog\"",
			},
			"directory": {
				Type:        "string",
				Description: "Working directory for the process. Defaults to current directory.",
			},
		},
		Required: []string{"command"},
	},
}

var ProcessReadDef = agent.ToolDefinition{
	Name:        "process_read",
	Description: "Read new stdout/stderr output from a background process since the last read. Returns only new output (incremental). If the process has exited, includes the exit_code. Use wait_seconds to block briefly for new output instead of polling.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Process handle ID returned by process_spawn.",
			},
			"wait_seconds": {
				Type:        "integer",
				Description: "Wait up to this many seconds for new output before returning (0-30). Default 0 (immediate).",
			},
		},
		Required: []string{"id"},
	},
}

var ProcessSendDef = agent.ToolDefinition{
	Name:        "process_send",
	Description: "Write text to a running process's stdin. A newline is appended automatically if not present. Returns the process's new output after a brief pause. Use for interactive programs, REPLs, or sending commands to running services.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Process handle ID returned by process_spawn.",
			},
			"input": {
				Type:        "string",
				Description: "Text to write to the process's stdin.",
			},
		},
		Required: []string{"id", "input"},
	},
}

var ProcessKillDef = agent.ToolDefinition{
	Name:        "process_kill",
	Description: "Terminate a background process and remove its handle. Returns the final stdout/stderr output and exit code. The handle is freed after this call.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Process handle ID to terminate.",
			},
		},
		Required: []string{"id"},
	},
}

var ProcessListDef = agent.ToolDefinition{
	Name:        "process_list",
	Description: "List all active background process handles with their command, running state, uptime, and session ownership.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// ─── convenience: process_exec (synchronous with structured output) ───────────

// ProcessExecTool is a synchronous variant that waits for the process to complete
// and returns structured output. Like an enhanced bash_exec with separate streams.
// This is intentionally separate from BashExecTool to avoid breaking existing behavior.
func ProcessExecTool(ctx context.Context, args map[string]any) (string, error) {
	command := agent.ArgString(args, "command")
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("process_exec: 'command' argument is required")
	}
	dir := agent.ArgString(args, "directory")

	timeout := 60 * time.Second
	if t := agent.ArgInt(args, "timeout_seconds", 0); t > 0 && t <= 600 {
		timeout = time.Duration(t) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	result := map[string]any{
		"stdout":      strings.TrimRight(stdoutBuf.String(), "\n"),
		"stderr":      strings.TrimRight(stderrBuf.String(), "\n"),
		"exit_code":   0,
		"duration_ms": elapsed.Milliseconds(),
	}

	if err != nil {
		if ctx.Err() != nil {
			result["exit_code"] = -1
			result["timed_out"] = true
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result["exit_code"] = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("process_exec: %v", err)
		}
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

var ProcessExecDef = agent.ToolDefinition{
	Name:        "process_exec",
	Description: "Execute a command synchronously and return structured JSON with separated stdout, stderr, exit_code, and duration_ms. Like bash_exec but with properly separated output streams. Max timeout 600s.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"command": {
				Type:        "string",
				Description: "Shell command to execute.",
			},
			"directory": {
				Type:        "string",
				Description: "Working directory. Defaults to current directory.",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Maximum execution time in seconds (1–600). Defaults to 60.",
			},
		},
		Required: []string{"command"},
	},
	ParamAliases: map[string]string{
		"timeout": "timeout_seconds",
		"cmd":     "command",
		"dir":     "directory",
		"cwd":     "directory",
		"path":    "directory",
	},
}
