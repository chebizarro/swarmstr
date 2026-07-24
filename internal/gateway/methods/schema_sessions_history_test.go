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

func TestDecodeSessionsHistoryMutationParams(t *testing.T) {
	if req, err := DecodeSessionsCompactionBranchParams(json.RawMessage(`{"key":" source ","checkpointId":" cp-1 ","agentId":"main"}`)); err != nil || req.Key != "source" || req.CheckpointID != "cp-1" {
		t.Fatalf("branch=%+v err=%v", req, err)
	}
	if _, err := DecodeSessionsCompactionRestoreParams(json.RawMessage(`{"key":"source","checkpointId":"cp-1","extra":true}`)); err == nil {
		t.Fatal("expected closed compaction restore params")
	}
	if req, err := DecodeSessionsBranchesListParams(json.RawMessage(`{"sessionKey":" source ","agentId":"main"}`)); err != nil || req.SessionKey != "source" {
		t.Fatalf("branches.list=%+v err=%v", req, err)
	}
	if req, err := DecodeSessionsBranchesSwitchParams(json.RawMessage(`{"sessionKey":"source","leafEntryId":" leaf "}`)); err != nil || req.LeafEntryID != "leaf" {
		t.Fatalf("branches.switch=%+v err=%v", req, err)
	}
	if req, err := DecodeSessionsRewindParams(json.RawMessage(`{"sessionKey":"source","entryId":" user-1 "}`)); err != nil || req.EntryID != "user-1" {
		t.Fatalf("rewind=%+v err=%v", req, err)
	}
	if req, err := DecodeSessionsForkParams(json.RawMessage(`{"sessionKey":"source","entryId":"user-1"}`)); err != nil || req.EntryID != "user-1" {
		t.Fatalf("fork=%+v err=%v", req, err)
	}
	for _, raw := range []string{
		`{"key":"source"}`,
		`{"sessionKey":"source","entryId":"wrong"}`,
		`{"sessionKey":"source","entryId":"user-1","extra":true}`,
	} {
		if _, err := DecodeSessionsBranchesSwitchParams(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected switch validation error for %s", raw)
		}
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
