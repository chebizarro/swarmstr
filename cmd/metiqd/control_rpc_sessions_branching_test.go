package main

import (
	"context"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

func newHistoryFixture(t *testing.T) (*state.DocsRepository, *state.TranscriptRepository, *state.SessionStore) {
	t.Helper()
	backing := newTestStore()
	docs := state.NewDocsRepository(backing, "history-branch-test")
	sessions, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Put("s1", state.SessionEntry{SessionID: "s1", AgentID: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.PutSession(context.Background(), "s1", state.SessionDoc{Version: 1, SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	transcripts := state.NewTranscriptRepository(backing, "history-branch-test").BindSessionStore(sessions)
	for _, entry := range []state.TranscriptEntryDoc{
		{SessionID: "s1", EntryID: "u1", Role: "user", Text: "first prompt", Unix: 1},
		{SessionID: "s1", EntryID: "a1", Role: "assistant", Text: "first answer", Unix: 2},
		{SessionID: "s1", EntryID: "u2", Role: "user", Text: "second prompt", Unix: 3},
		{SessionID: "s1", EntryID: "a2", Role: "assistant", Text: "second answer", Unix: 4},
	} {
		if _, err := transcripts.PutEntry(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	}
	return docs, transcripts, sessions
}

func TestSessionRewindSwitchAndForkUsePersistedDAG(t *testing.T) {
	ctx := context.Background()
	docs, transcripts, sessions := newHistoryFixture(t)
	result, err := rewindSessionAtEntry(ctx, transcripts, sessions, "s1", "main", "u2")
	if err != nil || result["editorText"] != "second prompt" {
		t.Fatalf("rewind=%#v err=%v", result, err)
	}
	active, err := transcripts.ListSessionAll(ctx, "s1")
	if err != nil || len(active) != 2 || active[1].EntryID != "a1" {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	listed, err := listSessionBranches(ctx, transcripts, sessions, "s1", "main")
	if err != nil {
		t.Fatal(err)
	}
	branches := listed["branches"].([]sessionBranchView)
	if len(branches) != 2 || !branches[0].Active {
		t.Fatalf("branches=%+v", branches)
	}
	if _, err := switchSessionBranch(ctx, transcripts, sessions, "s1", "main", "a2"); err != nil {
		t.Fatal(err)
	}
	active, _ = transcripts.ListSessionAll(ctx, "s1")
	if len(active) != 4 || active[3].EntryID != "a2" {
		t.Fatalf("switched active=%+v", active)
	}
	forked, err := forkSessionAtEntry(ctx, docs, transcripts, sessions, "s1", "main", "u2")
	if err != nil || forked["editorText"] != "second prompt" {
		t.Fatalf("fork=%#v err=%v", forked, err)
	}
	forkKey := forked["sessionKey"].(string)
	forkEntry, ok := sessions.Get(forkKey)
	if !ok || forkEntry.SpawnedBy != "s1" || !forkEntry.ForkedFromParent {
		t.Fatalf("fork entry=%+v ok=%v", forkEntry, ok)
	}
	forkPath, err := transcripts.ListSessionPath(ctx, forkEntry.SessionID, forkEntry.ActiveTranscriptLeafID)
	if err != nil || len(forkPath) != 2 {
		t.Fatalf("fork path=%+v err=%v", forkPath, err)
	}
}

func TestCompactionCheckpointBranchAndRestore(t *testing.T) {
	ctx := context.Background()
	docs, transcripts, sessions := newHistoryFixture(t)
	entries, _ := transcripts.ListSessionAll(ctx, "s1")
	if err := transcripts.WriteSnapshot(ctx, "snap-cp1", "s1", entries); err != nil {
		t.Fatal(err)
	}
	graph, _, _ := sessions.TranscriptGraph("s1")
	checkpoint := state.CompactionCheckpointRef{
		CheckpointID: "cp1", SessionKey: "s1", SessionID: "s1", CreatedAt: 10,
		Reason: "manual", SnapshotID: "snap-cp1",
		PreCompaction:  map[string]any{"session_id": "s1", "leaf_id": graph.ActiveLeafID},
		PostCompaction: map[string]any{"session_id": "s1", "leaf_id": graph.ActiveLeafID},
	}
	if _, err := sessions.CommitTranscriptGraph("s1", graph.Revision, state.TranscriptGraphMutation{ActiveLeafID: graph.ActiveLeafID, BranchHeads: graph.BranchHeads, Checkpoint: &checkpoint}); err != nil {
		t.Fatal(err)
	}
	if _, err := transcripts.PutEntry(ctx, state.TranscriptEntryDoc{SessionID: "s1", EntryID: "u3", Role: "user", Text: "third", Unix: 5}); err != nil {
		t.Fatal(err)
	}
	restored, err := restoreSessionCheckpoint(ctx, transcripts, sessions, "s1", "main", "cp1")
	if err != nil || restored["sessionId"] != "s1" {
		t.Fatalf("restore=%#v err=%v", restored, err)
	}
	active, _ := transcripts.ListSessionAll(ctx, "s1")
	if len(active) != 4 || active[3].EntryID != "a2" {
		t.Fatalf("restored active=%+v", active)
	}
	branched, err := branchSessionAtCheckpoint(ctx, docs, transcripts, sessions, "s1", "main", "cp1")
	if err != nil || branched["sourceKey"] != "s1" {
		t.Fatalf("branch=%#v err=%v", branched, err)
	}
	branchKey := branched["key"].(string)
	branchEntry, _ := sessions.Get(branchKey)
	branchPath, err := transcripts.ListSessionPath(ctx, branchEntry.SessionID, branchEntry.ActiveTranscriptLeafID)
	if err != nil || len(branchPath) != 4 {
		t.Fatalf("branch path=%+v err=%v", branchPath, err)
	}

	current, _ := sessions.Get("s1")
	legacy := state.CompactionCheckpointRef{CheckpointID: "legacy", SessionKey: "s1", SessionID: "s1", CreatedAt: 11, Reason: "manual"}
	current.CompactionCheckpoints = append(current.CompactionCheckpoints, legacy)
	if err := sessions.Put("s1", current); err != nil {
		t.Fatal(err)
	}
	if _, err := branchSessionAtCheckpoint(ctx, docs, transcripts, sessions, "s1", "main", "legacy"); err == nil {
		t.Fatal("expected legacy checkpoint rejection")
	}
}
