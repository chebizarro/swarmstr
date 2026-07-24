package main

import (
	"context"
	"path/filepath"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func TestSessionCompactionCheckpointQueries(t *testing.T) {
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("session-a", state.SessionEntry{
		SessionID: "session-a",
		CompactionCheckpoints: []state.CompactionCheckpointRef{
			{CheckpointID: "cp-old", SessionKey: "session-a", SessionID: "session-a", CreatedAt: 10, Reason: "manual", PreCompaction: map[string]any{"session_id": "session-a", "entry_id": "e1"}},
			{CheckpointID: "cp-new", SessionKey: "session-a", SessionID: "session-a", CreatedAt: 20, Reason: "auto-threshold", Summary: "summary", PostCompaction: map[string]any{"sessionId": "session-a", "entryId": "e9"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := listSessionCompactionCheckpoints(store, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := listed["checkpoints"].([]map[string]any)
	if len(checkpoints) != 2 || checkpoints[0]["checkpointId"] != "cp-new" {
		t.Fatalf("checkpoints=%#v", checkpoints)
	}
	got, err := getSessionCompactionCheckpoint(store, "session-a", "cp-old")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := got["checkpoint"].(map[string]any)
	pre := checkpoint["preCompaction"].(map[string]any)
	if pre["entryId"] != "e1" || pre["sessionId"] != "session-a" {
		t.Fatalf("checkpoint=%#v", checkpoint)
	}
	if _, err := getSessionCompactionCheckpoint(store, "session-a", "missing"); err == nil {
		t.Fatal("expected missing checkpoint error")
	}
}

func TestSearchSessionTranscriptsDeterministicAndBounded(t *testing.T) {
	ctx := context.Background()
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "history-test")
	transcripts := state.NewTranscriptRepository(backing, "history-test")
	sessions, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session-a", "session-b"} {
		if _, err := docs.PutSession(ctx, key, state.SessionDoc{Version: 1, SessionID: key}); err != nil {
			t.Fatal(err)
		}
		if err := sessions.Put(key, state.SessionEntry{SessionID: key, AgentID: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	entries := []state.TranscriptEntryDoc{
		{Version: 1, SessionID: "session-a", EntryID: "a1", Role: "user", Text: "Needle once", Unix: 10},
		{Version: 1, SessionID: "session-b", EntryID: "b1", Role: "assistant", Text: "needle twice, needle", Unix: 5},
		{Version: 1, SessionID: "session-b", EntryID: "b2", Role: "system", Text: "needle hidden", Unix: 20},
	}
	for _, entry := range entries {
		if _, err := transcripts.PutEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	out, err := searchSessionTranscripts(ctx, docs, transcripts, sessions, methods.SessionsSearchRequest{AgentID: "main", Query: "needle", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	results := out["results"].([]map[string]any)
	if len(results) != 1 || results[0]["messageId"] != "b1" || out["truncated"] != true {
		t.Fatalf("result=%#v", out)
	}
}
