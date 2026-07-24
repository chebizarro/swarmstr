package policy

import (
	"context"
	"testing"

	"metiq/internal/permissions"
)

func TestToolPolicyEvaluatePriorityAndGroups(t *testing.T) {
	p := ToolPolicy{Rules: []ToolPolicyRule{
		{ID: "allow-runtime", Group: "group:runtime", Action: ToolPolicyAllow},
		{ID: "ask-agent", ToolName: "bash", AgentID: "agent-a", Action: ToolPolicyAsk},
		{ID: "deny-bash", ToolName: "bash", Action: ToolPolicyDeny},
	}}
	dec := p.Evaluate(ToolPolicyRequest{ToolName: "bash", AgentID: "agent-a"})
	if dec.Action != ToolPolicyDeny {
		t.Fatalf("action = %s, want deny", dec.Action)
	}
	if len(dec.MatchedRules) != 3 {
		t.Fatalf("matched rules = %d, want 3", len(dec.MatchedRules))
	}
}

func TestToolPolicyAndPermissionsEngineDecisionParity(t *testing.T) {
	tests := []struct {
		name    string
		actions []ToolPolicyAction
		want    ToolPolicyAction
	}{
		{name: "allow", actions: []ToolPolicyAction{ToolPolicyAllow}, want: ToolPolicyAllow},
		{name: "ask beats allow", actions: []ToolPolicyAction{ToolPolicyAllow, ToolPolicyAsk}, want: ToolPolicyAsk},
		{name: "deny beats ask and allow", actions: []ToolPolicyAction{ToolPolicyAllow, ToolPolicyAsk, ToolPolicyDeny}, want: ToolPolicyDeny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolRules := make([]ToolPolicyRule, 0, len(tt.actions))
			cfg := permissions.DefaultEngineConfig()
			cfg.AuditEnabled = false
			cfg.AutoClassify = false
			cfg.CacheEnabled = false
			cfg.DefaultBehavior = permissions.BehaviorAllow
			engine := permissions.NewEngine(t.TempDir(), cfg)
			for i, action := range tt.actions {
				id := string(rune('a' + i))
				toolRules = append(toolRules, ToolPolicyRule{ID: id, ToolName: "bash", Action: action})
				if err := engine.AddRule(permissions.NewRule(id, permissions.ScopeGlobal, permissions.Behavior(action), "bash")); err != nil {
					t.Fatalf("AddRule: %v", err)
				}
			}

			toolDecision := (ToolPolicy{Rules: toolRules}).Evaluate(ToolPolicyRequest{ToolName: "bash"})
			permissionDecision := engine.Evaluate(context.Background(), permissions.NewToolRequest("bash", permissions.CategoryExec))
			if toolDecision.Action != tt.want || ToolPolicyAction(permissionDecision.Behavior) != tt.want {
				t.Fatalf("tool=%s permissions=%s, want %s", toolDecision.Action, permissionDecision.Behavior, tt.want)
			}
		})
	}
}

func TestToolPolicyCustomGroupAndDefault(t *testing.T) {
	p := ToolPolicy{DefaultAction: ToolPolicyAsk, GroupDefinitions: map[string][]string{"danger": {"custom.*"}}, Rules: []ToolPolicyRule{{ID: "deny-custom", Group: "danger", Action: ToolPolicyDeny}}}
	if got := p.Evaluate(ToolPolicyRequest{ToolName: "custom.tool"}).Action; got != ToolPolicyDeny {
		t.Fatalf("custom group action = %s, want deny", got)
	}
	if got := p.Evaluate(ToolPolicyRequest{ToolName: "other"}).Action; got != ToolPolicyAsk {
		t.Fatalf("default action = %s, want ask", got)
	}
}

func TestPreToolGatePolicyAndHooks(t *testing.T) {
	gate := PreToolGate{
		Policy: ToolPolicy{Rules: []ToolPolicyRule{{ID: "ask-web", Group: "web", Action: ToolPolicyAsk}}},
		Hooks: []PreToolHook{PreToolHookFunc{HookID: "block-fetch", Fn: func(ctx context.Context, p PreToolContext) (PreToolResult, error) {
			if p.ToolName == "fetch" {
				return PreToolResult{Action: PreToolBlock, Message: "fetch disabled"}, nil
			}
			return PreToolResult{Action: PreToolAllow}, nil
		}}},
	}
	res, err := gate.Evaluate(context.Background(), PreToolContext{ToolPolicyRequest: ToolPolicyRequest{ToolName: "fetch"}})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Action != PreToolBlock || res.Message != "fetch disabled" {
		t.Fatalf("result = %+v, want block", res)
	}
}
