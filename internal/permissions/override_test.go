package permissions

import (
	"context"
	"testing"

	"metiq/internal/store/state"
)

// TestImmutableSafetyDenyBeatsAllowAll proves that a critical safety deny is
// non-overridable: neither a session-scope allow-all nor an agent-scope
// allow-all (both of which outrank global scope) can neutralize it.
func TestImmutableSafetyDenyBeatsAllowAll(t *testing.T) {
	ctx := context.Background()

	// A globally-denied critical operation.
	danger := func(agentID, sessionID string) *ToolRequest {
		return NewToolRequest("bash", CategoryExec).
			WithContent("rm -rf /etc/passwd").
			WithContext("", "", agentID, sessionID)
	}

	t.Run("session allow-all does not override", func(t *testing.T) {
		engine := NewAutonomousEngine(t.TempDir()) // default behavior: allow
		if err := engine.AllowAllForSession(); err != nil {
			t.Fatalf("AllowAllForSession: %v", err)
		}
		d := engine.Evaluate(ctx, danger("", "sess-1"))
		if d.Behavior != BehaviorDeny {
			t.Fatalf("session allow-all overrode critical deny: got %s (reason: %s)", d.Behavior, d.Reason)
		}
	})

	t.Run("agent allow-all does not override", func(t *testing.T) {
		engine := NewAutonomousEngine(t.TempDir())
		if err := engine.AllowAllForAgent("evil"); err != nil {
			t.Fatalf("AllowAllForAgent: %v", err)
		}
		d := engine.Evaluate(ctx, danger("evil", ""))
		if d.Behavior != BehaviorDeny {
			t.Fatalf("agent allow-all overrode critical deny: got %s (reason: %s)", d.Behavior, d.Reason)
		}
	})

	t.Run("both allow-all layers together do not override", func(t *testing.T) {
		engine := NewAutonomousEngine(t.TempDir())
		if err := engine.AllowAllForSession(); err != nil {
			t.Fatalf("AllowAllForSession: %v", err)
		}
		if err := engine.AllowAllForAgent("evil"); err != nil {
			t.Fatalf("AllowAllForAgent: %v", err)
		}
		d := engine.Evaluate(ctx, danger("evil", "sess-1"))
		if d.Behavior != BehaviorDeny {
			t.Fatalf("stacked allow-all overrode critical deny: got %s (reason: %s)", d.Behavior, d.Reason)
		}
	})

	t.Run("benign command still allowed under allow-all", func(t *testing.T) {
		engine := NewAutonomousEngine(t.TempDir())
		if err := engine.AllowAllForSession(); err != nil {
			t.Fatalf("AllowAllForSession: %v", err)
		}
		req := NewToolRequest("bash", CategoryExec).WithContent("ls -la").WithContext("", "", "", "sess-1")
		if d := engine.Evaluate(ctx, req); d.Behavior != BehaviorAllow {
			t.Fatalf("benign command should stay allowed, got %s", d.Behavior)
		}
	})
}

// TestImmutableSafetyRuleCannotBeRemoved proves the non-overridable layer also
// cannot be neutralized by deleting the rule.
func TestImmutableSafetyRuleCannotBeRemoved(t *testing.T) {
	engine := NewAutonomousEngine(t.TempDir())
	if engine.RemoveRule("safety-deny-rm-rf-root") {
		t.Fatal("immutable safety rule was removable")
	}
	d := engine.Evaluate(context.Background(),
		NewToolRequest("bash", CategoryExec).WithContent("rm -rf /etc/passwd"))
	if d.Behavior != BehaviorDeny {
		t.Fatalf("critical deny gone after removal attempt: got %s", d.Behavior)
	}
}

// TestStateConfigDoesNotSilentlyAllowUnmatchedTool proves NewEngineFromStateConfig
// no longer defaults unmatched tools to allow (default-open). With no matching
// rule, the decision must be ask (fail-closed), not allow.
func TestStateConfigDoesNotSilentlyAllowUnmatchedTool(t *testing.T) {
	cfg := state.PermissionsConfig{AuditEnabled: boolPtr(false)}
	engine, err := NewEngineFromStateConfig(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("NewEngineFromStateConfig: %v", err)
	}

	req := NewToolRequest("some_unmatched_tool", CategoryBuiltin)
	d := engine.Evaluate(context.Background(), req)
	if d.Behavior == BehaviorAllow {
		t.Fatalf("unmatched tool was silently allowed (default-open): %s", d.Reason)
	}
	if d.Behavior != BehaviorAsk {
		t.Fatalf("expected fail-closed ask for unmatched tool, got %s", d.Behavior)
	}
}

// TestStateConfigDefaultBehaviorStillHonored ensures an explicit default_behavior
// in the config is still respected after the fail-closed default change.
func TestStateConfigDefaultBehaviorStillHonored(t *testing.T) {
	cfg := state.PermissionsConfig{DefaultBehavior: "allow", AuditEnabled: boolPtr(false)}
	engine, err := NewEngineFromStateConfig(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("NewEngineFromStateConfig: %v", err)
	}
	req := NewToolRequest("some_unmatched_tool", CategoryBuiltin)
	if d := engine.Evaluate(context.Background(), req); d.Behavior != BehaviorAllow {
		t.Fatalf("explicit default_behavior=allow not honored, got %s", d.Behavior)
	}
}

// TestStateConfigAllowedToolsEnforced proves a restrictive AllowedTools list is
// enforced as an exclusive allowlist: only listed tools run, and this holds even
// against an autonomous (allow-all) behavior.
func TestStateConfigAllowedToolsEnforced(t *testing.T) {
	cfg := state.PermissionsConfig{
		AuditEnabled: boolPtr(false),
		Agents: map[string]state.AgentPermissions{
			"scoped": {
				Behavior:     "autonomous", // allow-all, yet still constrained by allowlist
				AllowedTools: []string{"read_file", "list_dir"},
			},
		},
	}
	engine, err := NewEngineFromStateConfig(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("NewEngineFromStateConfig: %v", err)
	}
	ctx := context.Background()

	// Listed tool: admitted by allowlist, then allowed by autonomous allow-all.
	allowed := NewToolRequest("read_file", CategoryFilesystem).WithContext("", "", "scoped", "")
	if d := engine.Evaluate(ctx, allowed); d.Behavior != BehaviorAllow {
		t.Fatalf("allowlisted tool should be allowed, got %s (reason: %s)", d.Behavior, d.Reason)
	}

	// Unlisted tool: denied by the allowlist gate despite autonomous allow-all.
	denied := NewToolRequest("write_file", CategoryFilesystem).WithContext("", "", "scoped", "")
	if d := engine.Evaluate(ctx, denied); d.Behavior != BehaviorDeny {
		t.Fatalf("non-allowlisted tool should be denied, got %s (reason: %s)", d.Behavior, d.Reason)
	}

	// A different agent has no allowlist and is unaffected by it.
	other := NewToolRequest("write_file", CategoryFilesystem).WithContext("", "", "unconfigured", "")
	if d := engine.Evaluate(ctx, other); d.Behavior == BehaviorDeny {
		t.Fatalf("unconfigured agent should not be gated by another agent's allowlist, got deny: %s", d.Reason)
	}
}

// TestSetAgentAllowlistClearing verifies that clearing an allowlist removes the
// restriction for the agent.
func TestSetAgentAllowlistClearing(t *testing.T) {
	engine := NewStandardEngine(t.TempDir())
	if err := engine.SetAgentAllowlist("a1", []string{"read_file"}, nil); err != nil {
		t.Fatalf("SetAgentAllowlist: %v", err)
	}
	ctx := context.Background()
	req := NewToolRequest("write_file", CategoryFilesystem).WithContext("", "", "a1", "")
	if d := engine.Evaluate(ctx, req); d.Behavior != BehaviorDeny {
		t.Fatalf("expected deny while allowlist active, got %s", d.Behavior)
	}
	if err := engine.SetAgentAllowlist("a1", nil, nil); err != nil {
		t.Fatalf("clear SetAgentAllowlist: %v", err)
	}
	if d := engine.Evaluate(ctx, req); d.Behavior == BehaviorDeny {
		t.Fatalf("allowlist not cleared; still denying %s", d.Reason)
	}
}
