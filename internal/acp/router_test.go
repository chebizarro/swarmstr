package acp

import "testing"

func TestRouterAliasResolution(t *testing.T) {
	r := NewRouter()

	decision := r.Route(RouteRequest{Harness: "Claude Code"})
	if decision.AgentID != "claude" {
		t.Fatalf("AgentID = %q, want claude", decision.AgentID)
	}
	if decision.TargetHarness != "claude" {
		t.Fatalf("TargetHarness = %q, want claude", decision.TargetHarness)
	}
}

func TestRouterRuntimeVsDirectDecisions(t *testing.T) {
	r := NewRouter()

	runtimeDecision := r.Route(RouteRequest{Harness: "cursor", Runtime: true})
	if runtimeDecision.Mode != RouteModeRuntime {
		t.Fatalf("runtime mode = %q, want %q", runtimeDecision.Mode, RouteModeRuntime)
	}

	directDecision := r.Route(RouteRequest{Harness: "cursor", Direct: true})
	if directDecision.Mode != RouteModeDirect {
		t.Fatalf("direct mode = %q, want %q", directDecision.Mode, RouteModeDirect)
	}

	relayDecision := r.Route(RouteRequest{Harness: "cursor", Intent: RouteIntentRelay})
	if relayDecision.Mode != RouteModeDirect {
		t.Fatalf("relay mode = %q, want %q", relayDecision.Mode, RouteModeDirect)
	}
}

func TestRouterDefaultRouting(t *testing.T) {
	r := NewRouter()

	decision := r.Route(RouteRequest{})
	if decision.AgentID != "openclaw" {
		t.Fatalf("AgentID = %q, want openclaw", decision.AgentID)
	}
	if decision.Mode != RouteModeRuntime {
		t.Fatalf("Mode = %q, want %q", decision.Mode, RouteModeRuntime)
	}
	if decision.SessionMode != SessionModePersistent {
		t.Fatalf("SessionMode = %q, want %q", decision.SessionMode, SessionModePersistent)
	}
}

func TestRouterThreadSpawnPolicy(t *testing.T) {
	r := NewRouter()

	decision := r.Route(RouteRequest{Harness: "qwen code", Intent: RouteIntentThreadSpawn})
	if decision.AgentID != "qwen" {
		t.Fatalf("AgentID = %q, want qwen", decision.AgentID)
	}
	if decision.Mode != RouteModeRuntime {
		t.Fatalf("Mode = %q, want %q", decision.Mode, RouteModeRuntime)
	}
	if !decision.Thread {
		t.Fatalf("Thread = false, want true")
	}
	if !decision.Retry.RepairBeforeRetry {
		t.Fatalf("RepairBeforeRetry = false, want true")
	}
	if !decision.Retry.RestartGatewayAfterRepair {
		t.Fatalf("RestartGatewayAfterRepair = false, want true")
	}
	if decision.Retry.MaxRetries != 1 {
		t.Fatalf("MaxRetries = %d, want 1", decision.Retry.MaxRetries)
	}
	if got, want := decision.Retry.FallbackModes, []RouteMode{RouteModeRuntime, RouteModeDirect}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("FallbackModes = %#v, want %#v", got, want)
	}
}

func TestRouterRuntimeUnavailableRetryPolicy(t *testing.T) {
	r := NewRouter()

	decision := r.Route(RouteRequest{Harness: "gemini", RuntimeUnavailable: true})
	if decision.Mode != RouteModeDirect {
		t.Fatalf("Mode = %q, want %q", decision.Mode, RouteModeDirect)
	}
	if !decision.Retry.RepairBeforeRetry {
		t.Fatalf("RepairBeforeRetry = false, want true")
	}
	if decision.Retry.MaxRetries != 1 {
		t.Fatalf("MaxRetries = %d, want 1", decision.Retry.MaxRetries)
	}
}
