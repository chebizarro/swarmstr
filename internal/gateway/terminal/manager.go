// Package terminal implements WS-native PTY terminal sessions for the
// gateway. A session is owned by exactly one WebSocket connection: output is
// streamed only to that connection as terminal.data events and lifecycle end
// is reported as a terminal.exit event. All terminal methods are
// operator.admin (enforced by the method descriptor table); this package adds
// the per-connection ownership and lifecycle bookkeeping on top.
package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/creack/pty"
)

const (
	// EventData carries one raw output chunk from a PTY session.
	EventData = "terminal.data"
	// EventExit reports session end-of-life; the session id is invalid after.
	EventExit = "terminal.exit"

	// DefaultMaxSessions bounds concurrently open PTY sessions.
	DefaultMaxSessions = 12
	// DefaultBufferLimit bounds the retained replay ring buffer per session.
	DefaultBufferLimit = 256 * 1024
	// readChunkSize is the PTY read buffer size.
	readChunkSize = 16 * 1024

	minDimension = 1
	maxDimension = 2000
)

// DataEvent is the payload for terminal.data. Seq is the cumulative UTF-16
// code-unit end offset of Data within the session output stream, mirroring
// the OpenClaw wire contract so clients can reconcile replay buffers.
type DataEvent struct {
	SessionID string `json:"sessionId"`
	Seq       int64  `json:"seq"`
	Data      string `json:"data"`
}

// ExitEvent is the payload for terminal.exit.
type ExitEvent struct {
	SessionID string `json:"sessionId"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Signal    *int   `json:"signal,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Exit reasons mirror the OpenClaw terminal.exit contract.
const (
	ExitReasonProcessExit  = "process_exit"
	ExitReasonClosed       = "closed"
	ExitReasonDisconnected = "disconnected"
	ExitReasonError        = "error"
)

// Emitter delivers one event to one gateway connection. Implementations must
// be safe for concurrent use and non-blocking (queue or drop).
type Emitter interface {
	EmitTo(connID string, event string, payload any) bool
}

// OpenRequest describes one PTY session to spawn.
type OpenRequest struct {
	ConnID  string
	AgentID string
	Shell   string
	Args    []string
	Cwd     string
	Env     []string
	Cols    int
	Rows    int
}

// OpenResult reports the facts a client renders in its terminal header.
type OpenResult struct {
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`
	Shell     string `json:"shell"`
	Cwd       string `json:"cwd"`
	Confined  bool   `json:"confined"`
}

// SessionInfo is one attachable session as reported by list.
type SessionInfo struct {
	SessionID   string `json:"sessionId"`
	AgentID     string `json:"agentId"`
	Shell       string `json:"shell"`
	Cwd         string `json:"cwd"`
	Confined    bool   `json:"confined"`
	Attached    bool   `json:"attached"`
	CreatedAtMs int64  `json:"createdAtMs"`
}

// Options configures a Manager.
type Options struct {
	Emitter     Emitter
	MaxSessions int
	BufferLimit int
	// NewID overrides session id generation (tests). Nil uses a random id.
	NewID func() string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Manager owns every live PTY session for one gateway process.
type Manager struct {
	mu       sync.Mutex
	opts     Options
	sessions map[string]*session
	closed   bool
}

type session struct {
	id        string
	agentID   string
	shell     string
	cwd       string
	connID    string
	createdAt time.Time

	mu     sync.Mutex
	ptmx   *os.File
	cmd    *exec.Cmd
	buffer []byte
	seq    int64
	done   bool
}

// ErrLimit reports that the session cap is reached; callers map it to an
// invalid-request error rather than an availability failure.
var ErrLimit = errors.New("terminal session limit reached")

// NewManager returns a Manager that emits through opts.Emitter.
func NewManager(opts Options) *Manager {
	if opts.MaxSessions <= 0 {
		opts.MaxSessions = DefaultMaxSessions
	}
	if opts.BufferLimit <= 0 {
		opts.BufferLimit = DefaultBufferLimit
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{opts: opts, sessions: map[string]*session{}}
}

func clampDimension(v int) int {
	if v < minDimension {
		return minDimension
	}
	if v > maxDimension {
		return maxDimension
	}
	return v
}

// Open spawns one PTY-backed shell session bound to req.ConnID.
func (m *Manager) Open(req OpenRequest) (OpenResult, error) {
	if strings.TrimSpace(req.ConnID) == "" {
		return OpenResult{}, errors.New("terminal requires an authenticated connection")
	}
	shell := strings.TrimSpace(req.Shell)
	if shell == "" {
		shell = defaultShell()
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		cwd = os.Getenv("HOME")
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return OpenResult{}, errors.New("terminal manager is shut down")
	}
	if len(m.sessions) >= m.opts.MaxSessions {
		m.mu.Unlock()
		return OpenResult{}, fmt.Errorf("%w (max %d)", ErrLimit, m.opts.MaxSessions)
	}
	id := ""
	if m.opts.NewID != nil {
		id = m.opts.NewID()
	}
	if id == "" {
		id = randomSessionID()
	}
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return OpenResult{}, fmt.Errorf("terminal session id collision: %s", id)
	}
	// Reserve the slot before the (slow) spawn so a burst of opens cannot
	// overshoot MaxSessions.
	s := &session{
		id:        id,
		agentID:   strings.TrimSpace(req.AgentID),
		shell:     shell,
		cwd:       cwd,
		connID:    req.ConnID,
		createdAt: m.opts.Now(),
	}
	m.sessions[id] = s
	m.mu.Unlock()

	cmd := exec.Command(shell, req.Args...)
	cmd.Dir = cwd
	cmd.Env = terminalEnv(req.Env)
	winsize := &pty.Winsize{
		Cols: uint16(clampDimension(req.Cols)),
		Rows: uint16(clampDimension(req.Rows)),
	}
	ptmx, err := pty.StartWithSize(cmd, winsize)
	if err != nil {
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		return OpenResult{}, fmt.Errorf("terminal spawn failed: %w", err)
	}
	s.mu.Lock()
	s.ptmx = ptmx
	s.cmd = cmd
	s.mu.Unlock()

	go m.pump(s)

	return OpenResult{
		SessionID: id,
		AgentID:   s.agentID,
		Shell:     shell,
		Cwd:       cwd,
		Confined:  false,
	}, nil
}

// pump streams PTY output to the owning connection until process exit.
func (m *Manager) pump(s *session) {
	buf := make([]byte, readChunkSize)
	for {
		s.mu.Lock()
		ptmx := s.ptmx
		s.mu.Unlock()
		if ptmx == nil {
			return
		}
		n, err := ptmx.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			s.mu.Lock()
			s.buffer = append(s.buffer, buf[:n]...)
			if over := len(s.buffer) - m.opts.BufferLimit; over > 0 {
				s.buffer = s.buffer[over:]
			}
			s.seq += utf16Len(chunk)
			seq := s.seq
			connID := s.connID
			done := s.done
			s.mu.Unlock()
			if !done && m.opts.Emitter != nil {
				m.opts.Emitter.EmitTo(connID, EventData, DataEvent{SessionID: s.id, Seq: seq, Data: chunk})
			}
		}
		if err != nil {
			// PTY read errors mark process end on both Linux (EIO) and macOS.
			m.finish(s, ExitReasonProcessExit, "")
			return
		}
	}
}

// finish reaps the process, emits terminal.exit once, and forgets the session.
func (m *Manager) finish(s *session, reason, errText string) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	ptmx := s.ptmx
	cmd := s.cmd
	connID := s.connID
	s.mu.Unlock()

	if ptmx != nil {
		_ = ptmx.Close()
	}
	var exitCode, signalNum *int
	if cmd != nil && cmd.Process != nil {
		if reason != ExitReasonProcessExit {
			_ = cmd.Process.Kill()
		}
		err := cmd.Wait()
		if state := cmd.ProcessState; state != nil {
			if code := state.ExitCode(); code >= 0 {
				exitCode = &code
			} else if ws, ok := sysWaitStatus(state); ok && ws.Signaled() {
				sig := int(ws.Signal())
				signalNum = &sig
			}
		}
		_ = err
	}

	m.mu.Lock()
	delete(m.sessions, s.id)
	m.mu.Unlock()

	if m.opts.Emitter != nil {
		payload := ExitEvent{SessionID: s.id, ExitCode: exitCode, Signal: signalNum, Reason: reason}
		if errText != "" {
			payload.Error = errText
		}
		m.opts.Emitter.EmitTo(connID, EventExit, payload)
	}
}

// Write forwards client keystrokes to the session stdin. It reports false for
// unknown sessions or callers that do not own the session.
func (m *Manager) Write(connID, sessionID, data string) bool {
	s := m.owned(connID, sessionID)
	if s == nil {
		return false
	}
	s.mu.Lock()
	ptmx := s.ptmx
	done := s.done
	s.mu.Unlock()
	if done || ptmx == nil {
		return false
	}
	_, err := ptmx.WriteString(data)
	return err == nil
}

// Resize adjusts the PTY grid.
func (m *Manager) Resize(connID, sessionID string, cols, rows int) bool {
	s := m.owned(connID, sessionID)
	if s == nil {
		return false
	}
	s.mu.Lock()
	ptmx := s.ptmx
	done := s.done
	s.mu.Unlock()
	if done || ptmx == nil {
		return false
	}
	err := pty.Setsize(ptmx, &pty.Winsize{
		Cols: uint16(clampDimension(cols)),
		Rows: uint16(clampDimension(rows)),
	})
	return err == nil
}

// Close tears down one owned session and kills its process tree.
func (m *Manager) Close(connID, sessionID string) bool {
	s := m.owned(connID, sessionID)
	if s == nil {
		return false
	}
	m.finish(s, ExitReasonClosed, "")
	return true
}

// DropConnection tears down every session owned by connID (socket closed).
func (m *Manager) DropConnection(connID string) {
	for _, s := range m.ownedAll(connID) {
		m.finish(s, ExitReasonDisconnected, "")
	}
}

// Shutdown closes every session and refuses further opens.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	all := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		m.finish(s, ExitReasonClosed, "")
	}
}

// List reports every live session. The terminal surface is operator.admin, so
// cross-connection visibility adds no privilege.
func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.Lock()
		out = append(out, SessionInfo{
			SessionID:   s.id,
			AgentID:     s.agentID,
			Shell:       s.shell,
			Cwd:         s.cwd,
			Confined:    false,
			Attached:    true,
			CreatedAtMs: s.createdAt.UnixMilli(),
		})
		s.mu.Unlock()
	}
	return out
}

// Count reports the number of live sessions.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) owned(connID, sessionID string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil || s.connID != connID {
		return nil
	}
	return s
}

func (m *Manager) ownedAll(connID string) []*session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*session{}
	for _, s := range m.sessions {
		if s.connID == connID {
			out = append(out, s)
		}
	}
	return out
}

func utf16Len(s string) int64 {
	var n int64
	for _, r := range s {
		n += int64(len(utf16.Encode([]rune{r})))
	}
	return n
}

func terminalEnv(extra []string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	return append(env, extra...)
}

func defaultShell() string {
	if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" {
		return sh
	}
	return "/bin/sh"
}
