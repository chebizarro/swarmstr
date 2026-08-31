package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func callSessionRPCForTest(t *testing.T, handler controlRPCHandler, method string, params map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	result, handled, err := handler.handleSessionRPC(context.Background(), nostruntime.ControlRPCInbound{Method: method, Params: raw, Internal: true}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled", method)
	}
	if err != nil {
		return nil, err
	}
	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("method %s returned %T", method, result.Result)
	}
	return payload, nil
}

func TestSessionGetAndResolveDispatchUseDurableStore(t *testing.T) {
	docs, transcripts, sessions := newHistoryFixture(t)
	entry, _ := sessions.Get("s1")
	entry.Label = "Deploy monitor"
	if err := sessions.Put("s1", entry); err != nil {
		t.Fatal(err)
	}
	handler := newControlRPCHandler(controlRPCDeps{docsRepo: docs, transcriptRepo: transcripts, sessionStore: sessions})

	got, err := callSessionRPCForTest(t, handler, methods.MethodSessionsGet, map[string]any{"key": "s1", "limit": 2})
	if err != nil {
		t.Fatal(err)
	}
	messages, ok := got["messages"].([]state.TranscriptEntryDoc)
	if !ok || len(messages) != 2 || messages[1].EntryID != "a2" {
		t.Fatalf("messages=%#v", got["messages"])
	}
	if got["key"] != "s1" || got["sessionId"] != "s1" {
		t.Fatalf("get payload=%#v", got)
	}

	resolved, err := callSessionRPCForTest(t, handler, methods.MethodSessionsResolve, map[string]any{"label": "deploy monitor"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["ok"] != true || resolved["key"] != "s1" {
		t.Fatalf("resolve payload=%#v", resolved)
	}
	missing, err := callSessionRPCForTest(t, handler, methods.MethodSessionsResolve, map[string]any{"key": "missing", "allowMissing": true})
	if err != nil || missing["ok"] != false {
		t.Fatalf("missing=%#v err=%v", missing, err)
	}
}

func TestSessionResolveReportsAmbiguousShortReference(t *testing.T) {
	_, _, sessions := newHistoryFixture(t)
	for _, key := range []string{"agent:main:subagent:12345678-a", "agent:main:subagent:12345678-b"} {
		if err := sessions.Put(key, state.SessionEntry{SessionID: key, AgentID: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := resolveStoredSession(sessions, methods.SessionsResolveRequest{ShortID: "12345678", AgentID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	candidates, ok := resolved["candidates"].([]map[string]any)
	if resolved["ok"] != false || !ok || len(candidates) != 2 {
		t.Fatalf("resolved=%#v", resolved)
	}
}

func TestSessionRecoverFallsBackToDurableCheckpoint(t *testing.T) {
	docs, transcripts, sessions := newHistoryFixture(t)
	ctx := context.Background()
	entries, err := transcripts.ListSessionAll(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := transcripts.WriteSnapshot(ctx, "recovery-snapshot", "s1", entries); err != nil {
		t.Fatal(err)
	}
	graph, _, ok := sessions.TranscriptGraph("s1")
	if !ok {
		t.Fatal("missing transcript graph")
	}
	checkpoint := state.CompactionCheckpointRef{CheckpointID: "recovery-checkpoint", SessionKey: "s1", SessionID: "s1", SnapshotID: "recovery-snapshot", CreatedAt: 10}
	if _, err := sessions.CommitTranscriptGraph("s1", graph.Revision, state.TranscriptGraphMutation{ActiveLeafID: "missing-leaf", BranchHeads: []string{"missing-leaf"}, Checkpoint: &checkpoint}); err != nil {
		t.Fatal(err)
	}

	recovered, err := recoverStoredSession(ctx, docs, transcripts, sessions, "s1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if recovered["recoveredFrom"] != "checkpoint:recovery-checkpoint" {
		t.Fatalf("recovered=%#v", recovered)
	}
	successor, _ := sessions.Get(recovered["key"].(string))
	path, err := transcripts.ListSessionPath(ctx, successor.SessionID, successor.ActiveTranscriptLeafID)
	if err != nil || len(path) != 4 {
		t.Fatalf("path=%+v err=%v", path, err)
	}
}

func TestSessionRecoverCreatesSuccessorAndArchivesSource(t *testing.T) {
	docs, transcripts, sessions := newHistoryFixture(t)
	handler := newControlRPCHandler(controlRPCDeps{docsRepo: docs, transcriptRepo: transcripts, sessionStore: sessions})

	recovered, err := callSessionRPCForTest(t, handler, methods.MethodSessionsRecover, map[string]any{"key": "s1", "agent_id": "main"})
	if err != nil {
		t.Fatal(err)
	}
	successorKey, _ := recovered["key"].(string)
	if !strings.HasPrefix(successorKey, "s1:fork:") || recovered["sourceKey"] != "s1" {
		t.Fatalf("recovered=%#v", recovered)
	}
	source, _ := sessions.Get("s1")
	if !source.Archived {
		t.Fatal("recovery source was not archived")
	}
	successor, ok := sessions.Get(successorKey)
	if !ok || successor.Archived || successor.SpawnedBy != "s1" {
		t.Fatalf("successor=%+v ok=%v", successor, ok)
	}
	path, err := transcripts.ListSessionPath(context.Background(), successor.SessionID, successor.ActiveTranscriptLeafID)
	if err != nil || len(path) != 4 || path[3].Text != "second answer" {
		t.Fatalf("path=%+v err=%v", path, err)
	}
}
