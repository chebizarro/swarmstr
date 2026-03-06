package agent

import (
	"context"
	"testing"
)

// ─── stubRuntime ──────────────────────────────────────────────────────────────

type stubRuntime struct{ id string }

func (s *stubRuntime) ProcessTurn(_ context.Context, turn Turn) (TurnResult, error) {
	return TurnResult{Text: s.id + ":" + turn.UserText}, nil
}

// ─── AgentRuntimeRegistry ────────────────────────────────────────────────────

func TestAgentRuntimeRegistry_defaultFallback(t *testing.T) {
	def := &stubRuntime{id: "default"}
	reg := NewAgentRuntimeRegistry(def)

	got := reg.Get("")
	if got != def {
		t.Error("empty agentID should return default runtime")
	}
	got = reg.Get("main")
	if got != def {
		t.Error("\"main\" should return default runtime")
	}
	got = reg.Get("unknown")
	if got != def {
		t.Error("unregistered ID should fall back to default")
	}
}

func TestAgentRuntimeRegistry_setAndGet(t *testing.T) {
	def := &stubRuntime{id: "default"}
	sub := &stubRuntime{id: "sub"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("alpha", sub)
	if reg.Get("alpha") != sub {
		t.Error("expected sub runtime for alpha")
	}
	if reg.Get("main") != def {
		t.Error("main should still return default")
	}
}

func TestAgentRuntimeRegistry_remove(t *testing.T) {
	def := &stubRuntime{id: "default"}
	sub := &stubRuntime{id: "sub"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("alpha", sub)
	reg.Remove("alpha")
	if reg.Get("alpha") != def {
		t.Error("after remove, alpha should fall back to default")
	}
}

func TestAgentRuntimeRegistry_nilRemovesEntry(t *testing.T) {
	def := &stubRuntime{id: "default"}
	sub := &stubRuntime{id: "sub"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("alpha", sub)
	reg.Set("alpha", nil) // nil removes
	if reg.Get("alpha") != def {
		t.Error("setting nil should remove entry")
	}
}

func TestAgentRuntimeRegistry_registered(t *testing.T) {
	def := &stubRuntime{id: "default"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("a", &stubRuntime{})
	reg.Set("b", &stubRuntime{})
	reg.Set("a", nil) // remove a

	ids := reg.Registered()
	if len(ids) != 1 || ids[0] != "b" {
		t.Errorf("expected [b], got %v", ids)
	}
}

// ─── AgentSessionRouter ───────────────────────────────────────────────────────

func TestAgentSessionRouter_getEmpty(t *testing.T) {
	r := NewAgentSessionRouter()
	if r.Get("unknown") != "" {
		t.Error("Get on unknown session should return empty string")
	}
}

func TestAgentSessionRouter_assignAndGet(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("sess1", "agent-a")
	if r.Get("sess1") != "agent-a" {
		t.Error("expected agent-a for sess1")
	}
}

func TestAgentSessionRouter_unassign(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("sess1", "agent-a")
	r.Unassign("sess1")
	if r.Get("sess1") != "" {
		t.Error("after unassign, Get should return empty string")
	}
}

func TestAgentSessionRouter_list(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("s1", "a1")
	r.Assign("s2", "a2")
	r.Unassign("s1")

	m := r.List()
	if len(m) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m))
	}
	if m["s2"] != "a2" {
		t.Error("expected s2→a2")
	}
}

func TestAgentSessionRouter_listIsCopy(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("s1", "a1")
	m := r.List()
	m["s1"] = "mutated" // mutate copy
	if r.Get("s1") != "a1" {
		t.Error("list should return a copy; mutation should not affect router")
	}
}

// ─── BuildRuntimeForModel ────────────────────────────────────────────────────

func TestBuildRuntimeForModel_echo(t *testing.T) {
	rt, err := BuildRuntimeForModel("echo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
	res, err := rt.ProcessTurn(context.Background(), Turn{SessionID: "s", UserText: "hello"})
	if err != nil {
		t.Fatalf("ProcessTurn: %v", err)
	}
	if res.Text == "" {
		t.Error("expected non-empty response from echo runtime")
	}
}

func TestBuildRuntimeForModel_empty_usesEcho(t *testing.T) {
	rt, err := BuildRuntimeForModel("", nil)
	if err != nil {
		t.Fatalf("unexpected error for empty model: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
}

func TestBuildRuntimeForModel_unknown(t *testing.T) {
	_, err := BuildRuntimeForModel("gpt-99", nil)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}
