package methods

import "testing"

func TestDecodeHooksStatusParamsAgentAlias(t *testing.T) {
	req, err := DecodeHooksStatusParams([]byte(`{"agentId":" research "}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if req.AgentID != "research" {
		t.Fatalf("agent id=%q", req.AgentID)
	}
	if _, err := DecodeHooksStatusParams([]byte(`{"reload":true}`)); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}
