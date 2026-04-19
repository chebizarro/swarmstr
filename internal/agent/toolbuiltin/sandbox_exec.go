// Package toolbuiltin/sandbox_exec provides safe code execution:
//   - sandbox_exec → compile & run code with resource limits and isolation
//
// Three sandbox backends (auto-detected, best available):
//   - nsjail  — Linux namespace isolation (network, fs, pid, rlimit)
//   - docker  — Container isolation (network=none, memory cap, CPU cap)
//   - limits  — Process timeout + temp directory isolation (always available)
//
// Supported languages: go, python, javascript, rust, c, cpp, bash.
package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"metiq/internal/agent"
)

const (
	maxSandboxTimeout = 120         // seconds
	defaultSBTimeout  = 30          // seconds
	maxSBOutput       = 64 * 1024   // 64 KiB per stream
)

// ─── Language specifications ─────────────────────────────────────────────────

type sandboxLang struct {
	ext   string // file extension
	file  string // canonical filename
	image string // Docker image
	build string // compile command (empty for interpreted)
	run   string // execution command
}

var sandboxLangs = map[string]sandboxLang{
	"go":         {".go", "main.go", "golang:1-alpine", "", "go run ."},
	"python":     {".py", "script.py", "python:3-slim", "", "python3 script.py"},
	"javascript": {".js", "script.js", "node:20-slim", "", "node script.js"},
	"rust":       {".rs", "main.rs", "rust:slim", "rustc main.rs -o main 2>&1", "./main"},
	"c":          {".c", "main.c", "gcc:latest", "gcc -o main main.c -lm 2>&1", "./main"},
	"cpp":        {".cpp", "main.cpp", "gcc:latest", "g++ -o main main.cpp -lm 2>&1", "./main"},
	"bash":       {".sh", "script.sh", "alpine:latest", "", "bash script.sh"},
}

// ─── Result type ─────────────────────────────────────────────────────────────

type sandboxResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Sandbox    string `json:"sandbox"`
	Language   string `json:"language"`
	Error      string `json:"error,omitempty"`
}

// ─── Backend detection ───────────────────────────────────────────────────────

func detectSandboxBackend() string {
	if _, err := exec.LookPath("nsjail"); err == nil {
		return "nsjail"
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker"
	}
	return "limits"
}

// ─── Tool function ───────────────────────────────────────────────────────────

// SandboxExecTool returns a ToolFunc that compiles & runs code in a sandbox.
// The sandbox backend is detected once at creation time.
func SandboxExecTool() agent.ToolFunc {
	detected := detectSandboxBackend()

	return func(ctx context.Context, args map[string]any) (string, error) {
		code := agent.ArgString(args, "code")
		language := agent.ArgString(args, "language")
		stdinStr := agent.ArgString(args, "stdin")
		timeout := agent.ArgInt(args, "timeout", defaultSBTimeout)
		progArgs := agent.ArgString(args, "args")
		backend := agent.ArgString(args, "sandbox")

		if code == "" {
			return "", fmt.Errorf("sandbox_exec: 'code' is required")
		}
		if language == "" {
			return "", fmt.Errorf("sandbox_exec: 'language' is required")
		}

		lang, ok := sandboxLangs[language]
		if !ok {
			return "", fmt.Errorf("sandbox_exec: unsupported language %q; use go, python, javascript, rust, c, cpp, or bash", language)
		}

		if timeout < 1 {
			timeout = defaultSBTimeout
		}
		if timeout > maxSandboxTimeout {
			timeout = maxSandboxTimeout
		}
		if backend == "" {
			backend = detected
		}

		// Create isolated temp directory.
		tmpDir, err := os.MkdirTemp("", "sandbox-*")
		if err != nil {
			return "", fmt.Errorf("sandbox_exec: create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Write code to the canonical filename.
		codePath := filepath.Join(tmpDir, lang.file)
		if err := os.WriteFile(codePath, []byte(code), 0644); err != nil {
			return "", fmt.Errorf("sandbox_exec: write code: %v", err)
		}

		// Go needs a go.mod for `go run .` to work.
		if language == "go" {
			modPath := filepath.Join(tmpDir, "go.mod")
			_ = os.WriteFile(modPath, []byte("module sandbox\n\ngo 1.21\n"), 0644)
		}

		// Build the compile+run shell command.
		cmd := buildSandboxCmd(lang, progArgs)

		// Execute with timeout.
		execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		start := time.Now()
		var stdout, stderr string
		var exitCode int

		switch backend {
		case "nsjail":
			stdout, stderr, exitCode, err = runSandboxNsjail(execCtx, cmd, tmpDir, stdinStr, timeout)
		case "docker":
			stdout, stderr, exitCode, err = runSandboxDocker(execCtx, cmd, tmpDir, stdinStr, lang.image, timeout)
		default:
			backend = "limits"
			stdout, stderr, exitCode, err = runSandboxLimits(execCtx, cmd, tmpDir, stdinStr)
		}

		elapsed := time.Since(start)

		result := sandboxResult{
			Stdout:     truncateSBOutput(stdout),
			Stderr:     truncateSBOutput(stderr),
			ExitCode:   exitCode,
			DurationMS: elapsed.Milliseconds(),
			Sandbox:    backend,
			Language:   language,
		}

		if execCtx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
		}
		if err != nil && !result.TimedOut {
			result.Error = err.Error()
		}

		raw, _ := json.Marshal(result)
		return string(raw), nil
	}
}

// buildSandboxCmd combines compile and run commands with optional program args.
func buildSandboxCmd(lang sandboxLang, args string) string {
	var cmd string
	if lang.build != "" {
		cmd = lang.build + " && " + lang.run
	} else {
		cmd = lang.run
	}
	if args != "" {
		cmd += " " + args
	}
	return cmd
}

// ─── Backend: limits (always available) ──────────────────────────────────────
//
// Runs the command directly with a clean environment and timeout.
// Isolation: temp directory, restricted env vars, context timeout.
// This does NOT provide filesystem or network isolation.

func runSandboxLimits(ctx context.Context, cmd, dir, stdinStr string) (string, string, int, error) {
	var stdoutBuf, stderrBuf bytes.Buffer

	proc := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	proc.Dir = dir
	proc.Stdout = &stdoutBuf
	proc.Stderr = &stderrBuf
	proc.Env = sandboxEnv(dir)
	if stdinStr != "" {
		proc.Stdin = strings.NewReader(stdinStr)
	}
	// Create a new process group so we can kill the entire tree on timeout.
	proc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	proc.Cancel = func() error {
		return syscall.Kill(-proc.Process.Pid, syscall.SIGKILL)
	}
	proc.WaitDelay = 2 * time.Second

	err := proc.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil // non-zero exit is normal, not a tool error
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, err
}

// sandboxEnv builds a minimal, clean environment for sandboxed execution.
func sandboxEnv(dir string) []string {
	return []string{
		"PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"GOPATH=" + filepath.Join(dir, "gopath"),
		"GOCACHE=" + filepath.Join(dir, "gocache"),
		"GOFLAGS=-buildvcs=false",
		"NODE_PATH=" + dir,
	}
}

// ─── Backend: docker ─────────────────────────────────────────────────────────
//
// Runs the command in a Docker container with:
//   - --network none     → no network access
//   - --memory 512m      → 512 MiB memory limit
//   - --cpus 1           → 1 CPU core
//   - --pids-limit 256   → max 256 processes
//   - Volume mount for the code directory

func runSandboxDocker(ctx context.Context, cmd, dir, stdinStr, image string, timeout int) (string, string, int, error) {
	dockerArgs := []string{
		"run", "--rm",
		"--network", "none",
		"--memory", "512m",
		"--cpus", "1",
		"--pids-limit", "256",
		"--stop-timeout", strconv.Itoa(timeout),
		"-v", dir + ":/work",
		"-w", "/work",
		"-e", "GOPATH=/tmp/gopath",
		"-e", "GOCACHE=/tmp/gocache",
		"-e", "GOFLAGS=-buildvcs=false",
	}
	if stdinStr != "" {
		dockerArgs = append(dockerArgs, "-i")
	}
	dockerArgs = append(dockerArgs, image, "/bin/sh", "-c", cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	proc := exec.CommandContext(ctx, "docker", dockerArgs...)
	proc.Stdout = &stdoutBuf
	proc.Stderr = &stderrBuf
	if stdinStr != "" {
		proc.Stdin = strings.NewReader(stdinStr)
	}

	err := proc.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, err
}

// ─── Backend: nsjail ─────────────────────────────────────────────────────────
//
// Runs the command inside nsjail with namespace isolation:
//   - User/PID/mount/network namespaces
//   - rlimit for memory (512 MiB), CPU time, and file size
//   - No /proc, no loopback interface
//   - Bind-mounts the code directory as /work

func runSandboxNsjail(ctx context.Context, cmd, dir, stdinStr string, timeout int) (string, string, int, error) {
	nsjailArgs := []string{
		"-Mo",
		"--chroot", "/",
		"--user", "65534",
		"--group", "65534",
		"--time_limit", strconv.Itoa(timeout),
		"--rlimit_as", "512",
		"--rlimit_cpu", strconv.Itoa(timeout),
		"--rlimit_fsize", "64",
		"--disable_proc",
		"--iface_no_lo",
		"-B", dir + ":/work",
		"--cwd", "/work",
		"-E", "PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"-E", "HOME=/work",
		"-E", "TMPDIR=/work",
		"-E", "GOPATH=/work/gopath",
		"-E", "GOCACHE=/work/gocache",
		"-E", "GOFLAGS=-buildvcs=false",
		"--", "/bin/sh", "-c", cmd,
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	proc := exec.CommandContext(ctx, "nsjail", nsjailArgs...)
	proc.Stdout = &stdoutBuf
	proc.Stderr = &stderrBuf
	if stdinStr != "" {
		proc.Stdin = strings.NewReader(stdinStr)
	}

	err := proc.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}

	// nsjail logs to stderr with prefixes [I], [W], [E], [D].
	// Filter those out to get just the program's stderr.
	programStderr := filterNsjailLogs(stderrBuf.String())

	return stdoutBuf.String(), programStderr, exitCode, err
}

// filterNsjailLogs strips nsjail's own log lines from stderr.
func filterNsjailLogs(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "[I]") ||
			strings.HasPrefix(line, "[W]") ||
			strings.HasPrefix(line, "[E]") ||
			strings.HasPrefix(line, "[D]") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func truncateSBOutput(s string) string {
	if len(s) <= maxSBOutput {
		return s
	}
	return s[:maxSBOutput] + "\n... (output truncated at 64 KiB)"
}

// ─── Tool definition ────────────────────────────────────────────────────────

var SandboxExecDef = agent.ToolDefinition{
	Name: "sandbox_exec",
	Description: `Execute code in a sandboxed environment with resource limits. Provide source code and language; the tool compiles (if needed) and runs it, returning structured output.

Sandbox backends (auto-detected, best available):
  nsjail  — Linux namespace isolation: no network, rlimits, pid/mount namespaces
  docker  — Container isolation: --network none, 512 MiB memory, 1 CPU, 256 pids
  limits  — Process timeout + clean env + temp directory (always available)

Supported languages: go, python, javascript, rust, c, cpp, bash.`,
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"code": {
				Type:        "string",
				Description: "Source code to compile and execute.",
			},
			"language": {
				Type:        "string",
				Description: "Programming language.",
				Enum:        []string{"go", "python", "javascript", "rust", "c", "cpp", "bash"},
			},
			"stdin": {
				Type:        "string",
				Description: "Text to feed to the program's stdin.",
			},
			"timeout": {
				Type:        "integer",
				Description: "Max execution time in seconds (1–120). Default 30.",
			},
			"args": {
				Type:        "string",
				Description: "Command-line arguments to pass to the program.",
			},
			"sandbox": {
				Type:        "string",
				Description: "Override auto-detected sandbox backend.",
				Enum:        []string{"nsjail", "docker", "limits"},
			},
		},
		Required: []string{"code", "language"},
	},
}
