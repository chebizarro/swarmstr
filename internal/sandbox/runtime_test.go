package sandbox_test

import (
	"testing"
	"time"

	"metiq/internal/sandbox"
)

func TestRuntimeRegistryScopeKeying(t *testing.T) {
	reg := sandbox.NewRuntimeRegistry()
	base := sandbox.Config{Driver: "fake", PersistentRuntime: true, RuntimeKey: "same"}
	_, err := reg.Manage(sandbox.RuntimeSpec{Config: withScope(base, "session"), Backend: fakeRunner{driver: "fake"}})
	if err != nil {
		t.Fatalf("manage session: %v", err)
	}
	_, err = reg.Manage(sandbox.RuntimeSpec{Config: withScope(base, "agent"), Backend: fakeRunner{driver: "fake"}})
	if err != nil {
		t.Fatalf("manage agent: %v", err)
	}

	if got := len(reg.List("")); got != 2 {
		t.Fatalf("list all = %d, want 2", got)
	}
	if got := len(reg.List(sandbox.RuntimeScopeSession)); got != 1 {
		t.Fatalf("list session = %d, want 1", got)
	}
}

func TestRuntimeRegistryConfigHashReuseVsRecreate(t *testing.T) {
	reg := sandbox.NewRuntimeRegistry()
	now := time.Unix(100, 0)
	cfg := sandbox.Config{Driver: "fake", PersistentRuntime: true, RuntimeScope: "session", RuntimeKey: "abc", DockerImage: "one"}
	_, err := reg.Manage(sandbox.RuntimeSpec{Config: cfg, Backend: fakeRunner{driver: "fake"}, Now: now})
	if err != nil {
		t.Fatalf("manage first: %v", err)
	}
	first := reg.List("")[0]

	_, err = reg.Manage(sandbox.RuntimeSpec{Config: cfg, Backend: fakeRunner{driver: "fake"}, Now: now.Add(time.Second)})
	if err != nil {
		t.Fatalf("manage reuse: %v", err)
	}
	reused := reg.List("")[0]
	if reused.ID != first.ID {
		t.Fatalf("expected reuse id %q, got %q", first.ID, reused.ID)
	}

	cfg.DockerImage = "two"
	_, err = reg.Manage(sandbox.RuntimeSpec{Config: cfg, Backend: fakeRunner{driver: "fake"}, Now: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatalf("manage recreate: %v", err)
	}
	recreated := reg.List("")[0]
	if recreated.ID == first.ID || recreated.ConfigHash == first.ConfigHash {
		t.Fatalf("expected recreate with new hash, first=%+v recreated=%+v", first, recreated)
	}
}

func TestRuntimeRegistryListStatusPrune(t *testing.T) {
	reg := sandbox.NewRuntimeRegistry()
	now := time.Unix(1000, 0)
	cfg := sandbox.Config{Driver: "fake", PersistentRuntime: true, RuntimeScope: "shared", RuntimeKey: "hot"}
	runner, err := reg.Manage(sandbox.RuntimeSpec{Config: cfg, Backend: fakeRunner{driver: "fake"}, Now: now})
	if err != nil {
		t.Fatalf("manage: %v", err)
	}
	managed, ok := runner.(*sandbox.ManagedRunner)
	if !ok || managed.Driver() != "fake" {
		t.Fatalf("expected managed fake runner, got %T", runner)
	}

	items := reg.List(sandbox.RuntimeScopeShared)
	if len(items) != 1 || items[0].Status != sandbox.RuntimeStatusRunning {
		t.Fatalf("unexpected list: %+v", items)
	}
	pruned := reg.Prune(sandbox.RuntimeScopeShared, time.Minute, now.Add(2*time.Minute))
	if len(pruned) != 1 || pruned[0].Status != sandbox.RuntimeStatusPruned {
		t.Fatalf("unexpected pruned: %+v", pruned)
	}
	if got := len(reg.List("")); got != 0 {
		t.Fatalf("list after prune = %d, want 0", got)
	}
}

func withScope(cfg sandbox.Config, scope string) sandbox.Config {
	cfg.RuntimeScope = scope
	return cfg
}
