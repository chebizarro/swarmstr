package permissions

import "testing"

func TestEvaluateExecPolicyModesAndStricterMerge(t *testing.T) {
	base := ExecPolicyRequest{Tool: "bash_exec", Signature: `["exec","ls"]`, ExecutablePath: "/bin/ls", PromptAvailable: true}
	tests := []struct {
		name   string
		caller map[string]any
		host   map[string]any
		want   Behavior
	}{
		{"deny host wins", map[string]any{"mode": "full"}, map[string]any{"mode": "deny"}, BehaviorDeny},
		{"full both allows", map[string]any{"mode": "full"}, map[string]any{"mode": "full"}, BehaviorAllow},
		{"allowlist exact hit", map[string]any{"mode": "allowlist", "allowlist": []any{`["exec","ls"]`}}, map[string]any{"mode": "full"}, BehaviorAllow},
		{"allowlist miss denies", map[string]any{"mode": "allowlist", "allowlist": []any{`["exec","pwd"]`}}, map[string]any{"mode": "full"}, BehaviorDeny},
		{"ask miss prompts", map[string]any{"mode": "ask"}, map[string]any{"mode": "full"}, BehaviorAsk},
		{"host always hardens allow", map[string]any{"mode": "full"}, map[string]any{"mode": "full", "ask": "always"}, BehaviorAsk},
		{"auto miss prompts", map[string]any{"mode": "auto"}, map[string]any{"mode": "full"}, BehaviorAsk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateExecPolicy(tt.caller, tt.host, base)
			if err != nil {
				t.Fatal(err)
			}
			if got.Behavior != tt.want {
				t.Fatalf("behavior=%s reason=%s effective=%+v", got.Behavior, got.Reason, got.Effective)
			}
		})
	}
}

func TestEvaluateExecPolicyLegacyToolsAndSignature(t *testing.T) {
	req := ExecPolicyRequest{Tool: "bash_exec", Signature: `["exec","ls"]`, ExecutablePath: "/bin/ls", AllowAlwaysSafe: true, PromptAvailable: true}
	got, err := EvaluateExecPolicy(
		map[string]any{"tools": []any{"bash_exec"}},
		map[string]any{"allow_always_signatures": []any{`["exec","ls"]`}}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior != BehaviorAllow || !got.LegacySignature {
		t.Fatalf("got=%+v", got)
	}

	req.Tool = "read_file"
	got, err = EvaluateExecPolicy(map[string]any{"tools": []any{"bash_exec"}}, nil, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior != BehaviorAllow {
		t.Fatalf("unscoped tool should be allowed: %+v", got)
	}
}

func TestEvaluateExecPolicyRequiresEveryRestrictiveLayerToMatch(t *testing.T) {
	req := ExecPolicyRequest{Tool: "bash_exec", Signature: `["exec","ls"]`, ExecutablePath: "/bin/ls", PromptAvailable: true}
	got, err := EvaluateExecPolicy(
		map[string]any{"mode": "allowlist", "allowlist": []any{`["exec","ls"]`}},
		map[string]any{"mode": "allowlist", "allowlist": []any{`["exec","pwd"]`}}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior != BehaviorDeny || got.AllowlistMatched {
		t.Fatalf("got=%+v", got)
	}
}

func TestApplyExecAskFallback(t *testing.T) {
	req := ExecPolicyRequest{Tool: "bash_exec", Signature: `["exec","ls"]`, ExecutablePath: "/bin/ls", PromptAvailable: false}
	got, err := EvaluateExecPolicy(map[string]any{"mode": "ask", "askFallback": "full"}, map[string]any{"mode": "full", "askFallback": "full"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior != BehaviorDeny || !got.FallbackApplied || got.Effective.AskFallback != ExecSecurityAllowlist {
		t.Fatalf("fallback must be clamped by effective security: got=%+v", got)
	}

	matchedReq := req
	matchedReq.Signature = `["exec","ls"]`
	got, err = EvaluateExecPolicy(
		map[string]any{"mode": "ask", "askFallback": "full", "allowlist": []any{matchedReq.Signature}},
		map[string]any{"mode": "full", "askFallback": "full"}, matchedReq)
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior != BehaviorAllow || got.FallbackApplied || !got.AllowlistMatched {
		t.Fatalf("matched allowlist should bypass the prompt fallback: got=%+v", got)
	}

	got, err = EvaluateExecPolicy(map[string]any{"mode": "ask", "askFallback": "allowlist"}, map[string]any{"mode": "full", "askFallback": "full"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior != BehaviorDeny {
		t.Fatalf("allowlist miss fallback must deny: %+v", got)
	}
}

func TestEvaluateExecPolicyArgPatternMatchesArgumentsOnly(t *testing.T) {
	policy := map[string]any{
		"mode":      "allowlist",
		"allowlist": []any{map[string]any{"pattern": "git", "argPattern": `^status --short$`}},
	}
	matched, err := EvaluateExecPolicy(policy, nil, ExecPolicyRequest{Tool: "bash_exec", Argv: []string{"git", "status", "--short"}, ExecutablePath: "/usr/bin/git", Signature: `["exec","git","status","--short"]`, PromptAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	if matched.Behavior != BehaviorAllow {
		t.Fatalf("expected argPattern match: %+v", matched)
	}
	missed, err := EvaluateExecPolicy(policy, nil, ExecPolicyRequest{Tool: "bash_exec", Argv: []string{"git", "status", "--porcelain"}, ExecutablePath: "/usr/bin/git", Signature: `["exec","git","status","--porcelain"]`, PromptAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	if missed.Behavior != BehaviorDeny {
		t.Fatalf("argPattern mismatch was allowed: %+v", missed)
	}
}

func TestEvaluateExecPolicyRejectsInvalidArgPattern(t *testing.T) {
	_, err := EvaluateExecPolicy(map[string]any{"mode": "allowlist", "allowlist": []any{map[string]any{"pattern": "git", "argPattern": "["}}}, nil, ExecPolicyRequest{Tool: "bash_exec"})
	if err == nil {
		t.Fatal("expected invalid argPattern error")
	}
}

func TestEvaluateExecPolicyInvalidFailsClosedToCaller(t *testing.T) {
	_, err := EvaluateExecPolicy(map[string]any{"mode": "sometimes"}, nil, ExecPolicyRequest{Tool: "bash_exec"})
	if err == nil {
		t.Fatal("expected invalid policy error")
	}
}

func TestEvaluateExecPolicyAgentOverrideAndTimeout(t *testing.T) {
	host := map[string]any{
		"defaults": map[string]any{"mode": "full", "timeout_ms": 60000},
		"agents":   map[string]any{"main": map[string]any{"mode": "deny", "timeout_ms": 10000}},
	}
	got, err := EvaluateExecPolicy(map[string]any{"mode": "full", "timeout_ms": 30000}, host, ExecPolicyRequest{Tool: "bash_exec", AgentID: "main", PromptAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior != BehaviorDeny || got.Effective.TimeoutMS != 10000 {
		t.Fatalf("got=%+v", got)
	}
	if got.Effective.Fingerprint == "" {
		t.Fatal("missing policy fingerprint")
	}
}
