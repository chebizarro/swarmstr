package methods

import "testing"

func TestApplyCompatResponseAliases(t *testing.T) {
	out := ApplyCompatResponseAliases(map[string]any{"run_id": "r1", "display_name": "Main"})
	if out["runId"] != "r1" || out["displayName"] != "Main" {
		t.Fatalf("expected camelCase aliases, got %#v", out)
	}

	out = ApplyCompatResponseAliases(map[string]any{"runId": "r2", "sessionId": "s1"})
	if out["run_id"] != "r2" || out["session_id"] != "s1" {
		t.Fatalf("expected snake_case aliases, got %#v", out)
	}
}
