// Package sandbox provides a pluggable safe code-execution environment.
//
// The SandboxRunner interface abstracts over different isolation backends:
//   - DockerSandbox: ephemeral Docker container with CPU/memory caps (default)
//   - NopSandbox: plain os/exec with configurable timeout (unsafe opt-in)
//
// Callers select a backend via the driver config key.  Empty configuration uses
// Docker. The nop backend must be requested explicitly and executes on the host
// without isolation.
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
	// Unsafe is true when execution used a backend without isolation.
	Unsafe bool `json:"unsafe,omitempty"`
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
	// Driver selects the execution backend: "docker" (default) or "nop".
	// "nop" is unsafe and also requires AllowUnsafeNop.
	Driver string
	// AllowUnsafeNop must be true when Driver is "nop".
	AllowUnsafeNop bool
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
	// Defaults to true for DockerSandbox unless explicitly disabled.
	NetworkDisabled bool
	// AllowNetwork enables Docker container network access when true.
	AllowNetwork bool
	// ReadOnlyRootFS mounts the container root filesystem read-only. Docker only.
	// Defaults to true.
	ReadOnlyRootFS bool
	// WritableRootFS disables the read-only root filesystem default when true.
	WritableRootFS bool
	// CapDrop is the list of Linux capabilities to drop. Docker only.
	// Defaults to ["ALL"].
	CapDrop []string
	// SecurityOpt is passed as Docker --security-opt values. Docker only.
	// Defaults to ["no-new-privileges"].
	SecurityOpt []string
	// PidsLimit limits the number of processes in the container. Docker only.
	// Defaults to 128.
	PidsLimit int
	// User runs the container process as this user/group. Docker only.
	// Defaults to 65532:65532 (non-root nobody-style user).
	User string
	// Tmpfs mounts tmpfs targets inside the container. Docker only.
	Tmpfs []string
	// Ulimits are Docker --ulimit values. Docker only.
	Ulimits []string
	// MaxOutputBytes caps the total stdout+stderr returned.  0 = 1 MiB.
	MaxOutputBytes int64
	// WorkspaceDir is a host directory mounted into Docker containers when set.
	WorkspaceDir string
	// ContainerWorkdir is the absolute container path used for WorkspaceDir.
	// Defaults to /workspace when WorkspaceDir is set.
	ContainerWorkdir string
	// WorkspaceAccess controls the workspace mount mode: "read_only" or "read_write".
	// Empty defaults to "read_write" when WorkspaceDir is set.
	WorkspaceAccess string
}

func (c Config) maxOutput() int64 {
	if c.MaxOutputBytes > 0 {
		return c.MaxOutputBytes
	}
	return 1 << 20 // 1 MiB
}

func (c Config) dockerNetworkDisabled() bool {
	return c.NetworkDisabled || !c.AllowNetwork
}

func (c Config) dockerReadOnlyRootFS() bool {
	return c.ReadOnlyRootFS || !c.WritableRootFS
}

func (c Config) dockerCapDrop() []string {
	if len(c.CapDrop) > 0 {
		return c.CapDrop
	}
	return []string{"ALL"}
}

func (c Config) dockerSecurityOpt() []string {
	if len(c.SecurityOpt) > 0 {
		return c.SecurityOpt
	}
	return []string{"no-new-privileges"}
}

func (c Config) dockerPidsLimit() int {
	if c.PidsLimit > 0 {
		return c.PidsLimit
	}
	return 128
}

func (c Config) dockerUser() string {
	if strings.TrimSpace(c.User) != "" {
		return strings.TrimSpace(c.User)
	}
	return "65532:65532"
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
// Empty driver defaults to "docker". The unsafe "nop" driver must be requested
// explicitly via config and requires AllowUnsafeNop.
func New(cfg Config) (SandboxRunner, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "":
		return &DockerSandbox{cfg: cfg}, nil
	case "nop":
		if !cfg.AllowUnsafeNop {
			return nil, fmt.Errorf("sandbox driver \"nop\" requires explicit allow_unsafe_nop=true")
		}
		return &NopSandbox{cfg: cfg}, nil
	case "docker":
		return &DockerSandbox{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unknown sandbox driver %q (valid: docker, nop)", cfg.Driver)
	}
}

// NewFromMap constructs a Config from a map[string]any (config doc sub-tree)
// and returns the configured runner.
func NewFromMap(m map[string]any) (SandboxRunner, error) {
	if m == nil {
		return New(Config{})
	}
	cfg := Config{
		Driver:           getString(m, "driver"),
		AllowUnsafeNop:   getBool(m, "allow_unsafe_nop"),
		MemoryLimit:      getString(m, "memory_limit"),
		CPULimit:         getString(m, "cpu_limit"),
		DockerImage:      getString(m, "docker_image"),
		NetworkDisabled:  getBool(m, "network_disabled"),
		AllowNetwork:     getBool(m, "allow_network"),
		ReadOnlyRootFS:   getBool(m, "read_only_rootfs"),
		WritableRootFS:   getBool(m, "writable_rootfs"),
		CapDrop:          getStringSlice(m, "cap_drop"),
		SecurityOpt:      getStringSlice(m, "security_opt"),
		User:             getString(m, "user"),
		Tmpfs:            getStringSlice(m, "tmpfs"),
		Ulimits:          getStringSlice(m, "ulimits"),
		WorkspaceDir:     getString(m, "workspace_dir"),
		ContainerWorkdir: getString(m, "container_workdir"),
		WorkspaceAccess:  getString(m, "workspace_access"),
	}
	if pids, ok := numberAsInt(m["pids_limit"]); ok {
		cfg.PidsLimit = pids
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
		Unsafe:   true,
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

func (s *DockerSandbox) dockerRunArgs(image string, cmd []string, env []string, workdir string) []string {
	dockerArgs := []string{"run", "--rm", "--interactive=false"}

	if s.cfg.dockerNetworkDisabled() {
		dockerArgs = append(dockerArgs, "--network=none")
	}
	if s.cfg.dockerReadOnlyRootFS() {
		dockerArgs = append(dockerArgs, "--read-only")
	}
	for _, cap := range s.cfg.dockerCapDrop() {
		if strings.TrimSpace(cap) != "" {
			dockerArgs = append(dockerArgs, "--cap-drop="+strings.TrimSpace(cap))
		}
	}
	for _, opt := range s.cfg.dockerSecurityOpt() {
		if strings.TrimSpace(opt) != "" {
			dockerArgs = append(dockerArgs, "--security-opt="+strings.TrimSpace(opt))
		}
	}
	if pids := s.cfg.dockerPidsLimit(); pids > 0 {
		dockerArgs = append(dockerArgs, fmt.Sprintf("--pids-limit=%d", pids))
	}
	if user := s.cfg.dockerUser(); user != "" {
		dockerArgs = append(dockerArgs, "--user="+user)
	}
	if s.cfg.MemoryLimit != "" {
		dockerArgs = append(dockerArgs, "--memory="+s.cfg.MemoryLimit)
	}
	if s.cfg.CPULimit != "" {
		dockerArgs = append(dockerArgs, "--cpus="+s.cfg.CPULimit)
	}
	for _, tmpfs := range s.cfg.Tmpfs {
		if strings.TrimSpace(tmpfs) != "" {
			dockerArgs = append(dockerArgs, "--tmpfs="+strings.TrimSpace(tmpfs))
		}
	}
	for _, ulimit := range s.cfg.Ulimits {
		if strings.TrimSpace(ulimit) != "" {
			dockerArgs = append(dockerArgs, "--ulimit="+strings.TrimSpace(ulimit))
		}
	}
	for _, e := range env {
		dockerArgs = append(dockerArgs, "--env="+e)
	}
	workspace, _ := s.cfg.workspaceMount()
	if workspace.Enabled {
		dockerArgs = append(dockerArgs, workspace.DockerArgs()...)
		if strings.TrimSpace(workdir) == "" {
			workdir = workspace.Target
		}
	}
	if workdir != "" {
		dockerArgs = append(dockerArgs, "--workdir="+workdir)
	}
	dockerArgs = append(dockerArgs, image)
	dockerArgs = append(dockerArgs, cmd...)
	return dockerArgs
}

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

	if _, err := s.cfg.workspaceMount(); err != nil {
		return Result{Driver: "docker"}, err
	}
	dockerArgs := s.dockerRunArgs(image, cmd, env, workdir)

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

func getStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return []string{strings.TrimSpace(value)}
	default:
		return nil
	}
}

func numberAsInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
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
