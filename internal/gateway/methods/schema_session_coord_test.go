package methods

import (
	"encoding/json"
	"testing"
)

func TestSessionCoordParamsAreStrictAndNormalized(t *testing.T) {
	dispatch, err := DecodeSessionsDispatchParams(json.RawMessage(`{"key":" s1 ","agentId":" worker ","profileId":" remote "}`))
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err = dispatch.Normalize()
	if err != nil || dispatch.Key != "s1" || dispatch.AgentID != "worker" || dispatch.Backend != "remote" {
		t.Fatalf("unexpected dispatch: %+v err=%v", dispatch, err)
	}
	if _, err := DecodeSessionsDispatchParams(json.RawMessage(`{"key":"s1","profileId":"remote","extra":true}`)); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if _, err := (SessionsReclaimRequest{}).Normalize(); err == nil {
		t.Fatal("expected missing key rejection")
	}
	if _, err := DecodeSessionsGroupsListParams(json.RawMessage(`{"extra":true}`)); err == nil {
		t.Fatal("expected closed list params")
	}
}
