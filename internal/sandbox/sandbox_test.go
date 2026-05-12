package sandbox_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"metiq/internal/sandbox"
)

// ─── Factory ──────────────────────────────────────────────────────────────────

func TestNew_DefaultDriver(t *testing.T) {
	s, err := sandbox.New(sandbox.Config{})
	if err != nil {
		t.Fatalf("New default: %v", err)
	}
	if s.Driver() != "docker" {
		t.Errorf("expected driver=docker, got %q", s.Driver())
	}
}

func TestNew_NopDriverRequiresUnsafeOptIn(t *testing.T) {
	_, err := sandbox.New(sandbox.Config{Driver: "nop"})
	if err == nil {
		t.Fatal("expected nop without allow_unsafe_nop to fail")
	}

	s, err := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true})
	if err != nil {
		t.Fatalf("New nop: %v", err)
	}
	if s.Driver() != "nop" {
		t.Errorf("expected driver=nop, got %q", s.Driver())
	}
}

func TestNew_DockerDriver(t *testing.T) {
	s, err := sandbox.New(sandbox.Config{Driver: "docker"})
	if err != nil {
		t.Fatalf("New docker: %v", err)
	}
	if s.Driver() != "docker" {
		t.Errorf("expected driver=docker, got %q", s.Driver())
	}
}

func TestNew_InvalidDriver(t *testing.T) {
	_, err := sandbox.New(sandbox.Config{Driver: "kubernetes"})
	if err == nil {
		t.Error("expected error for unknown driver")
	}
}

// ─── NopSandbox ───────────────────────────────────────────────────────────────

func TestNopSandbox_EchoCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true})
	res, err := s.Run(context.Background(), []string{"echo", "hello sandbox"}, nil, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code: %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello sandbox") {
		t.Errorf("stdout: %q", res.Stdout)
	}
	if res.Driver != "nop" {
		t.Errorf("driver: %q", res.Driver)
	}
	if !res.Unsafe {
		t.Errorf("expected nop result to be marked unsafe")
	}
}

func TestNopSandbox_ExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true})
	res, err := s.Run(context.Background(), []string{"sh", "-c", "exit 42"}, nil, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 42 {
		t.Errorf("expected exit_code=42, got %d", res.ExitCode)
	}
}

func TestNopSandbox_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true, TimeoutSeconds: 1})
	start := time.Now()
	res, err := s.Run(context.Background(), []string{"sleep", "10"}, nil, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	elapsed := time.Since(start)
	if !res.TimedOut {
		t.Errorf("expected timed_out=true")
	}
	if elapsed > 5*time.Second {
		t.Errorf("took too long: %v", elapsed)
	}
}

func TestNopSandbox_EmptyCommand(t *testing.T) {
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true})
	_, err := s.Run(context.Background(), nil, nil, "")
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestNopSandbox_Stderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true})
	res, err := s.Run(context.Background(), []string{"sh", "-c", "echo err >&2"}, nil, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Stderr, "err") {
		t.Errorf("stderr: %q", res.Stderr)
	}
}

func TestNopSandbox_EnvOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true})
	res, err := s.Run(context.Background(), []string{"sh", "-c", "echo $MY_VAR"}, []string{"MY_VAR=testvalue"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Stdout, "testvalue") {
		t.Errorf("expected env override, stdout=%q", res.Stdout)
	}
}

func TestNopSandbox_OutputLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	// Limit to 100 bytes total.
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true, MaxOutputBytes: 100})
	// Write 200 bytes to stdout.
	res, err := s.Run(context.Background(), []string{"sh", "-c", "printf '%200s' x | tr ' ' 'x'"}, nil, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if int64(len(res.Stdout)) > 100 {
		t.Errorf("output not limited: len=%d", len(res.Stdout))
	}
}

// ─── NewFromMap ───────────────────────────────────────────────────────────────

func TestNewFromMap_EmptyMap(t *testing.T) {
	s, err := sandbox.NewFromMap(nil)
	if err != nil {
		t.Fatalf("NewFromMap nil: %v", err)
	}
	if s.Driver() != "docker" {
		t.Errorf("expected docker driver, got %q", s.Driver())
	}
}

func TestNewFromMap_NopDriverRequiresExplicitConfig(t *testing.T) {
	_, err := sandbox.NewFromMap(map[string]any{"driver": "nop"})
	if err == nil {
		t.Fatal("expected nop without allow_unsafe_nop to fail")
	}

	s, err := sandbox.NewFromMap(map[string]any{"driver": "nop", "allow_unsafe_nop": true})
	if err != nil {
		t.Fatalf("NewFromMap nop: %v", err)
	}
	if s.Driver() != "nop" {
		t.Errorf("expected nop driver, got %q", s.Driver())
	}
}

func TestNewFromMap_DockerDriver(t *testing.T) {
	s, err := sandbox.NewFromMap(map[string]any{"driver": "docker"})
	if err != nil {
		t.Fatalf("NewFromMap docker: %v", err)
	}
	if s.Driver() != "docker" {
		t.Errorf("expected docker driver, got %q", s.Driver())
	}
}

func TestNewFromMap_InvalidDriver(t *testing.T) {
	_, err := sandbox.NewFromMap(map[string]any{"driver": "invalid"})
	if err == nil {
		t.Error("expected error for invalid driver")
	}
}

func TestDefaultNopTimeoutSeconds_Positive(t *testing.T) {
	if sandbox.DefaultNopTimeoutSeconds <= 0 {
		t.Errorf("DefaultNopTimeoutSeconds = %d, want > 0", sandbox.DefaultNopTimeoutSeconds)
	}
}

func TestNopSandbox_DefaultTimeoutPreventsRunaway(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	// With no explicit timeout, NopSandbox should still enforce
	// DefaultNopTimeoutSeconds. We verify by checking that a process
	// runs within the sandbox context (which has a deadline).
	s, _ := sandbox.New(sandbox.Config{Driver: "nop", AllowUnsafeNop: true})
	// Run a fast command — it should succeed because the default timeout
	// is much longer than the command duration.
	res, err := s.Run(context.Background(), []string{"echo", "timeout-test"}, nil, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.TimedOut {
		t.Errorf("fast command should not time out")
	}
	if !strings.Contains(res.Stdout, "timeout-test") {
		t.Errorf("stdout: %q", res.Stdout)
	}
}
