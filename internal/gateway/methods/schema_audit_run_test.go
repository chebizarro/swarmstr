package methods

import "testing"

func TestAuditRunInspectSelectorAndBounds(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"runId":"run","executionId":"execution"}`,
		`{"executionId":"execution","executionLimit":2}`,
		`{"runId":"run","decisionLimit":101}`,
		`{"runId":"run","extra":true}`,
	} {
		req, err := DecodeAuditRunInspectParams([]byte(raw))
		if err == nil {
			_, err = req.Normalize()
		}
		if err == nil {
			t.Fatalf("expected rejection for %s", raw)
		}
	}
	req, err := DecodeAuditRunInspectParams([]byte(`{"runId":"run","executionLimit":50,"decisionLimit":100}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := req.Normalize(); err != nil {
		t.Fatal(err)
	}
}
