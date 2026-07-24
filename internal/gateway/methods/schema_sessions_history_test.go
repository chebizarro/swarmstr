package methods

import (
	"encoding/json"
	"testing"
)

func TestDecodeSessionsCompactionQueryParams(t *testing.T) {
	list, err := DecodeSessionsCompactionListParams(json.RawMessage(`{"key":" session-a ","agentId":"main"}`))
	if err != nil || list.Key != "session-a" || list.AgentID != "main" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	get, err := DecodeSessionsCompactionGetParams(json.RawMessage(`{"key":"session-a","checkpointId":"cp-1"}`))
	if err != nil || get.CheckpointID != "cp-1" {
		t.Fatalf("get=%+v err=%v", get, err)
	}
	if _, err := DecodeSessionsCompactionGetParams(json.RawMessage(`{"key":"session-a"}`)); err == nil {
		t.Fatal("expected missing checkpointId error")
	}
	if _, err := DecodeSessionsCompactionListParams(json.RawMessage(`{"key":"session-a","extra":true}`)); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestDecodeSessionsSearchParamsBounds(t *testing.T) {
	req, err := DecodeSessionsSearchParams(json.RawMessage(`{"agentId":"main","sessionKeys":["s2","s1","s2"],"query":" needle ","limit":25}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Query != "needle" || len(req.SessionKeys) != 2 || req.Limit != 25 {
		t.Fatalf("request=%+v", req)
	}
	for _, raw := range []string{
		`{"query":""}`,
		`{"query":"x","sessionKeys":[]}`,
		`{"query":"x","limit":26}`,
		`{"query":"x","unknown":true}`,
	} {
		if _, err := DecodeSessionsSearchParams(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected validation error for %s", raw)
		}
	}
}
