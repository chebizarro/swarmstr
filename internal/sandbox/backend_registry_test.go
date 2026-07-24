package sandbox_test

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/sandbox"
)

type fakeRunner struct{ driver string }

func (r fakeRunner) Driver() string { return r.driver }
func (r fakeRunner) Run(context.Context, []string, []string, string) (sandbox.Result, error) {
	return sandbox.Result{Driver: r.driver}, nil
}

func TestBackendRegistryBuiltInsPresent(t *testing.T) {
	names := strings.Join(sandbox.RegisteredBackends(), ",")
	if !strings.Contains(names, "docker") || !strings.Contains(names, "nop") {
		t.Fatalf("expected docker and nop built-ins, got %q", names)
	}
}

func TestBackendRegistryRegisterResolve(t *testing.T) {
	name := "fake-registry-test"
	err := sandbox.RegisterBackend(sandbox.BackendFunc{BackendName: name, Constructor: func(sandbox.Config) (sandbox.SandboxRunner, error) {
		return fakeRunner{driver: name}, nil
	}})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	runner, err := sandbox.New(sandbox.Config{Driver: name})
	if err != nil {
		t.Fatalf("new custom: %v", err)
	}
	if runner.Driver() != name {
		t.Fatalf("driver = %q, want %q", runner.Driver(), name)
	}
}

func TestBackendRegistryUnknownDriver(t *testing.T) {
	_, err := sandbox.New(sandbox.Config{Driver: "definitely-missing"})
	if err == nil || !strings.Contains(err.Error(), "unknown sandbox driver") {
		t.Fatalf("expected unknown driver error, got %v", err)
	}
}

func TestDefaultDockerUnavailableFailsClosedWithDoctorHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := sandbox.New(sandbox.Config{})
	if err == nil {
		t.Fatal("expected missing Docker to fail closed")
	}
	for _, want := range []string{"Docker CLI not found", "metiq doctor", "allow_unsafe_nop=true"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing actionable hint %q", err, want)
		}
	}
}

func TestSSHBackendSkeleton(t *testing.T) {
	_, err := sandbox.SSHBackend{}.New(sandbox.Config{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected unconfigured ssh error, got %v", err)
	}
}
