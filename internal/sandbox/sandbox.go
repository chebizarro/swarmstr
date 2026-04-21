// Package sandbox provides a pluggable safe code-execution environment.
//
// The SandboxRunner interface abstracts over different isolation backends:
//   - NopSandbox: plain os/exec with configurable timeout (default)
//   - DockerSandbox: ephemeral Docker container with CPU/memory caps
//
// Callers select a backend via the driver config key.  The docker backend
// requires the Docker CLI to be available on the host.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ─── Result ──────────────────────────────────────────────────────────────────

// Result holds the output of a sandbox execution.
type Result struct {
	// Stdout is the standard output of the command.
	Stdout string `json:"stdout"`
	// Stderr is the standard error of the command.
	Stderr string `json:"stderr"`
	// ExitCode is the process exit code (0 = success).
	ExitCode int `json:"exit_code"`
	// TimedOut is true if the process was killed due to a timeout.
	TimedOut bool `json:"timed_out,omitempty"`
	// Driver is the sandbox backend that executed the command.
	Driver string `json:"driver"`
}

// ─── Interface ────────────────────────────────────────────────────────────────

// SandboxRunner executes a command in an isolated environment.
//
// cmd[0] is the executable; cmd[1:] are arguments.
// env is a list of "KEY=VALUE" pairs added to (or replacing in) the process env.
// workdir is the working directory; empty string uses the current directory.
type SandboxRunner interface {
	// Run executes cmd in the sandbox and returns its output.
	Run(ctx context.Context, cmd []string, env []string, workdir string) (Result, error)
	// Driver returns the backend identifier ("nop", "docker", etc.).
	Driver() string
}

// ─── Config ───────────────────────────────────────────────────────────────────

// Config holds sandbox configuration.  Zero values activate sane defaults.
type Config struct {
	// Driver selects the execution backend: "nop" (default) or "docker".
	Driver string
	// TimeoutSeconds is the maximum execution time.  0 means no limit.
	TimeoutSeconds int
	// MemoryLimit is the container memory limit (e.g. "256m").  Docker only.
	MemoryLimit string
	// CPULimit is the container CPU limit (e.g. "0.5" = half a core).  Docker only.
	CPULimit string
	// DockerImage is the Docker image used for the docker backend.
	// Defaults to "alpine:3" if empty.
	DockerImage string
	// NetworkDisabled disables network access inside the container.  Docker only.
	NetworkDisabled bool
	// MaxOutputBytes caps the total stdout+stderr returned.  0 = 1 MiB.
	MaxOutputBytes int64
}

func (c Config) maxOutput() int64 {
	if c.MaxOutputBytes > 0 {
		return c.MaxOutputBytes
	}
	return 1 << 20 // 1 MiB
}

// DefaultNopTimeoutSeconds is the maximum execution time enforced on
// NopSandbox when no explicit timeout is configured.  This prevents
// runaway processes from consuming resources indefinitely.
const DefaultNopTimeoutSeconds = 300 // 5 minutes

func (c Config) timeout() time.Duration {
	if c.TimeoutSeconds > 0 {
		return time.Duration(c.TimeoutSeconds) * time.Second
	}
	return 0
}

// nopTimeout returns the effective timeout for NopSandbox, applying
// DefaultNopTimeoutSeconds as a safety cap when no explicit timeout is set.
func (c Config) nopTimeout() time.Duration {
	if c.TimeoutSeconds > 0 {
		return time.Duration(c.TimeoutSeconds) * time.Second
	}
	return time.Duration(DefaultNopTimeoutSeconds) * time.Second
}

// ─── Factory ──────────────────────────────────────────────────────────────────

// New returns the configured SandboxRunner.
// driver must be "nop" (default) or "docker".
func New(cfg Config) (SandboxRunner, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "nop":
		return &NopSandbox{cfg: cfg}, nil
	case "docker":
		return &DockerSandbox{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unknown sandbox driver %q (valid: nop, docker)", cfg.Driver)
	}
}

// NewFromMap constructs a Config from a map[string]any (config doc sub-tree)
// and returns the configured runner.
func NewFromMap(m map[string]any) (SandboxRunner, error) {
	if m == nil {
		return New(Config{})
	}
	cfg := Config{
		Driver:          getString(m, "driver"),
		MemoryLimit:     getString(m, "memory_limit"),
		CPULimit:        getString(m, "cpu_limit"),
		DockerImage:     getString(m, "docker_image"),
		NetworkDisabled: getBool(m, "network_disabled"),
	}
	if ts, ok := m["timeout_s"].(float64); ok {
		cfg.TimeoutSeconds = int(ts)
	}
	if mo, ok := m["max_output_bytes"].(float64); ok {
		cfg.MaxOutputBytes = int64(mo)
	}
	return New(cfg)
}

// ─── NopSandbox ───────────────────────────────────────────────────────────────

// NopSandbox runs commands via os/exec with an optional timeout.
// It provides no isolation beyond what the host OS offers.
//
// WARNING: NopSandbox executes commands directly on the host with
// the daemon's own privileges. For production deployments that run
// untrusted code (agent tools, sandbox.run), use the "docker" driver.
type NopSandbox struct {
	cfg     Config
	warnLog sync.Once
}

func (s *NopSandbox) Driver() string { return "nop" }

func (s *NopSandbox) Run(ctx context.Context, cmd []string, env []string, workdir string) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("sandbox: empty command")
	}

	s.warnLog.Do(func() {
		log.Printf("WARNING: sandbox running with \"nop\" driver (no isolation). " +
			"Set sandbox.driver=\"docker\" in config for production deployments.")
	})

	// Apply timeout — NopSandbox always enforces a safety cap.
	if d := s.cfg.nopTimeout(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Env = buildEnv(env)
	if workdir != "" {
		c.Dir = workdir
	}

	maxOut := s.cfg.maxOutput()
	var stdout, stderr limitedBuffer
	stdout.limit = maxOut / 2
	stderr.limit = maxOut / 2
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	res := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Driver:   "nop",
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
			res.ExitCode = -1
			return res, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("sandbox run: %w", err)
	}
	return res, nil
}

// ─── DockerSandbox ────────────────────────────────────────────────────────────

// DockerSandbox runs commands inside an ephemeral Docker container.
// The container is removed after execution (--rm).
type DockerSandbox struct {
	cfg Config
}

func (s *DockerSandbox) Driver() string { return "docker" }

func (s *DockerSandbox) Run(ctx context.Context, cmd []string, env []string, workdir string) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("sandbox: empty command")
	}

	image := s.cfg.DockerImage
	if image == "" {
		image = "alpine:3"
	}

	// Apply timeout.
	if d := s.cfg.timeout(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	// Build docker run args.
	dockerArgs := []string{"run", "--rm", "--interactive=false"}

	if s.cfg.NetworkDisabled {
		dockerArgs = append(dockerArgs, "--network=none")
	}
	if s.cfg.MemoryLimit != "" {
		dockerArgs = append(dockerArgs, "--memory="+s.cfg.MemoryLimit)
	}
	if s.cfg.CPULimit != "" {
		dockerArgs = append(dockerArgs, "--cpus="+s.cfg.CPULimit)
	}
	for _, e := range env {
		dockerArgs = append(dockerArgs, "--env="+e)
	}
	if workdir != "" {
		dockerArgs = append(dockerArgs, "--workdir="+workdir)
	}
	dockerArgs = append(dockerArgs, image)
	dockerArgs = append(dockerArgs, cmd...)

	c := exec.CommandContext(ctx, "docker", dockerArgs...)
	c.Env = os.Environ() // docker CLI needs host env (PATH, etc.)

	maxOut := s.cfg.maxOutput()
	var stdout, stderr limitedBuffer
	stdout.limit = maxOut / 2
	stderr.limit = maxOut / 2
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	res := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Driver:   "docker",
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
			res.ExitCode = -1
			return res, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("docker sandbox run: %w", err)
	}
	return res, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// buildEnv merges the provided KEY=VALUE pairs on top of the current process
// environment.
func buildEnv(extra []string) []string {
	base := os.Environ()
	if len(extra) == 0 {
		return base
	}
	// Override or append.
	env := make(map[string]string, len(base)+len(extra))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	for _, kv := range extra {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// limitedBuffer is an io.Writer that caps total bytes written.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int64
	n     int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	total := len(p)
	remaining := b.limit - b.n
	if remaining > 0 {
		write := p
		if int64(len(write)) > remaining {
			write = write[:remaining]
		}
		n, err := b.buf.Write(write)
		b.n += int64(n)
		if err != nil {
			return n, err
		}
	}
	// Report full len(p) consumed to avoid "short write" from the caller.
	return total, nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
