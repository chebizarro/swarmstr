package controlreplay

import "testing"

func TestMethodPolicy_KnownEventAndRequest(t *testing.T) {
	// A representative sample of the read-only methods that should replay both
	// the event and the request/response.
	methods := []string{
		"health",
		"status.get",
		"config.get",
		"sessions.list",
		"tasks.list",
		"mcp.list",
		"acp.peers",
	}
	for _, m := range methods {
		if got := MethodPolicy(m); got != EventAndRequest {
			t.Errorf("MethodPolicy(%q) = %v, want EventAndRequest", m, got)
		}
	}
}

func TestMethodPolicy_SoulfactoryPrefix(t *testing.T) {
	methods := []string{
		"soulfactory.",
		"soulfactory.run",
		"soulfactory.jobs.list",
	}
	for _, m := range methods {
		if got := MethodPolicy(m); got != EventAndRequest {
			t.Errorf("MethodPolicy(%q) = %v, want EventAndRequest (prefix rule)", m, got)
		}
	}
}

func TestMethodPolicy_SecretsResolveIsNone(t *testing.T) {
	// secrets.resolve must never be replayed at all — it is the only None.
	if got := MethodPolicy("secrets.resolve"); got != None {
		t.Errorf("MethodPolicy(%q) = %v, want None", "secrets.resolve", got)
	}
}

func TestMethodPolicy_UnknownDefaultsToEventOnly(t *testing.T) {
	methods := []string{
		"agent.run",
		"chat.send",
		"unknown.method",
		"config.set",
		"",
	}
	for _, m := range methods {
		if got := MethodPolicy(m); got != EventOnly {
			t.Errorf("MethodPolicy(%q) = %v, want EventOnly (default)", m, got)
		}
	}
}

func TestMethodPolicy_TrimsWhitespace(t *testing.T) {
	cases := map[string]Policy{
		"  health  ":         EventAndRequest,
		"\tsecrets.resolve\n": None,
		"  soulfactory.x ":   EventAndRequest,
		"  agent.run ":       EventOnly,
	}
	for method, want := range cases {
		if got := MethodPolicy(method); got != want {
			t.Errorf("MethodPolicy(%q) = %v, want %v", method, got, want)
		}
	}
}

// TestMethodPolicy_PrefixBeatsExactSecrets guards the ordering of the checks:
// the soulfactory prefix is evaluated before the switch, so a (hypothetical)
// soulfactory.secrets.resolve is EventAndRequest, not None.
func TestMethodPolicy_PrefixEvaluatedBeforeSwitch(t *testing.T) {
	if got := MethodPolicy("soulfactory.secrets.resolve"); got != EventAndRequest {
		t.Errorf("MethodPolicy(prefixed secrets.resolve) = %v, want EventAndRequest", got)
	}
}

func TestPolicyConstants_AreDistinct(t *testing.T) {
	if None == EventOnly || None == EventAndRequest || EventOnly == EventAndRequest {
		t.Fatalf("policy constants must be distinct: None=%d EventOnly=%d EventAndRequest=%d",
			None, EventOnly, EventAndRequest)
	}
}
