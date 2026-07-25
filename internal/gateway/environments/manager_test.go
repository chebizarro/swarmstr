package environments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/sandbox"
)

type stubRunner struct{ driver string }

func (r stubRunner) Driver() string { return r.driver }
func (r stubRunner) Run(context.Context, []string, []string, string) (sandbox.Result, error) {
	return sandbox.Result{}, nil
}

func newTestManager(opts Options) *Manager {
	if opts.Now == nil {
		base := time.UnixMilli(1_700_000_000_000)
		opts.Now = func() time.Time { return base }
	}
	if opts.CheckDocker == nil {
		opts.CheckDocker = func(context.Context) error { return nil }
	}
	if opts.NewRunner == nil {
		opts.NewRunner = func(sandbox.Config) (sandbox.SandboxRunner, error) {
			return stubRunner{driver: "docker"}, nil
		}
	}
	return NewManager(opts)
}

func dockerConfig() sandbox.Config {
	return sandbox.Config{Driver: "docker", DockerImage: "alpine:3"}
}

func TestCreateLifecycleAndStatusProjection(t *testing.T) {
	m := newTestManager(Options{})
	ctx := context.Background()

	summary, err := m.Create(ctx, CreateRequest{ProfileID: "default", IdempotencyKey: "key-1", Config: dockerConfig()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if summary.Status != "available" || summary.Type != "worker" || summary.Label != "default" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.Worker == nil || summary.Worker.State != StateReady || summary.Worker.ProviderID != "sandbox:docker" {
		t.Fatalf("unexpected worker metadata: %+v", summary.Worker)
	}

	got, ok := m.Status(summary.ID)
	if !ok || got.ID != summary.ID || got.Status != "available" {
		t.Fatalf("status: ok=%v %+v", ok, got)
	}
	if _, ok := m.Runner(summary.ID); !ok {
		t.Fatal("expected runner for ready environment")
	}

	destroyed, err := m.Destroy(ctx, summary.ID, false)
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if destroyed.Status != "unavailable" || destroyed.Worker.State != StateDestroyed {
		t.Fatalf("unexpected destroy summary: %+v", destroyed)
	}
	if _, ok := m.Runner(summary.ID); ok {
		t.Fatal("destroyed environment must not expose a runner")
	}
	// Destroy is idempotent.
	again, err := m.Destroy(ctx, summary.ID, true)
	if err != nil || again.Worker.State != StateDestroyed {
		t.Fatalf("re-destroy: %v %+v", err, again)
	}
	if _, err := m.Destroy(ctx, "env-unknown", false); err == nil {
		t.Fatal("expected error for unknown environment")
	}
}

func TestCreateIsIdempotentPerKey(t *testing.T) {
	m := newTestManager(Options{})
	ctx := context.Background()
	first, err := m.Create(ctx, CreateRequest{ProfileID: "default", IdempotencyKey: "same", Config: dockerConfig()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	replay, err := m.Create(ctx, CreateRequest{ProfileID: "default", IdempotencyKey: "same", Config: dockerConfig()})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.ID != first.ID {
		t.Fatalf("idempotency violated: %q != %q", replay.ID, first.ID)
	}
	other, err := m.Create(ctx, CreateRequest{ProfileID: "default", IdempotencyKey: "different", Config: dockerConfig()})
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if other.ID == first.ID {
		t.Fatal("distinct keys must create distinct environments")
	}
	if got := len(m.List()); got != 2 {
		t.Fatalf("expected 2 environments, got %d", got)
	}
}

func TestCreateFailsClosedWithoutDocker(t *testing.T) {
	m := newTestManager(Options{
		CheckDocker: func(context.Context) error { return errors.New("docker daemon unreachable") },
	})
	_, err := m.Create(context.Background(), CreateRequest{ProfileID: "default", IdempotencyKey: "k", Config: dockerConfig()})
	if err == nil || !strings.Contains(err.Error(), "docker required but unavailable") {
		t.Fatalf("expected fail-closed docker error, got %v", err)
	}
	// No partial record survives, and the key is reusable after the failure.
	if got := len(m.List()); got != 0 {
		t.Fatalf("expected empty registry after failed create, got %d", got)
	}
	m2 := newTestManager(Options{})
	if _, err := m2.Create(context.Background(), CreateRequest{ProfileID: "default", IdempotencyKey: "k", Config: dockerConfig()}); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
}

func TestCreateRefusesNonDockerDrivers(t *testing.T) {
	m := newTestManager(Options{})
	ctx := context.Background()

	cfg := dockerConfig()
	cfg.Driver = "nop"
	if _, err := m.Create(ctx, CreateRequest{ProfileID: "p", IdempotencyKey: "k1", Config: cfg}); err == nil {
		t.Fatal("expected refusal for nop driver")
	}

	cfg = dockerConfig()
	cfg.DockerImage = ""
	if _, err := m.Create(ctx, CreateRequest{ProfileID: "p", IdempotencyKey: "k2", Config: cfg}); err == nil {
		t.Fatal("expected refusal without docker_image")
	}

	// A runner that reports a non-docker driver is refused even if construction succeeds.
	m2 := newTestManager(Options{
		NewRunner: func(sandbox.Config) (sandbox.SandboxRunner, error) {
			return stubRunner{driver: "nop"}, nil
		},
	})
	if _, err := m2.Create(ctx, CreateRequest{ProfileID: "p", IdempotencyKey: "k3", Config: dockerConfig()}); err == nil {
		t.Fatal("expected refusal for non-docker runner")
	}

	// Empty driver defaults to docker and succeeds.
	cfg = dockerConfig()
	cfg.Driver = ""
	if _, err := m.Create(ctx, CreateRequest{ProfileID: "p", IdempotencyKey: "k4", Config: cfg}); err != nil {
		t.Fatalf("default driver create: %v", err)
	}
}

// TestConcurrentLifecycle exercises create/status/destroy/list under -race.
func TestConcurrentLifecycle(t *testing.T) {
	m := newTestManager(Options{})
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n%4) // contended idempotency keys
			summary, err := m.Create(ctx, CreateRequest{ProfileID: "default", IdempotencyKey: key, Config: dockerConfig()})
			if err != nil {
				t.Errorf("create %d: %v", n, err)
				return
			}
			m.Status(summary.ID)
			m.List()
			if n%2 == 0 {
				if _, err := m.Destroy(ctx, summary.ID, false); err != nil {
					t.Errorf("destroy %d: %v", n, err)
				}
			}
		}(i)
	}
	wg.Wait()
	if got := len(m.List()); got > 4 {
		t.Fatalf("idempotency keys must bound environment count: got %d", got)
	}
}
