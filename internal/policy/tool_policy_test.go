package policy

import (
	"context"
	"testing"
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
