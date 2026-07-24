package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestTranscriptGraphPersistsActivePathsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	sessions, err := NewSessionStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Put("s1", SessionEntry{SessionID: "s1", AgentID: "main"}); err != nil {
		t.Fatal(err)
	}
	repo := NewTranscriptRepository(newMemStateStore(), "author").BindSessionStore(sessions)
	for _, entry := range []TranscriptEntryDoc{
		{SessionID: "s1", EntryID: "u1", Role: "user", Text: "first", Unix: 1},
		{SessionID: "s1", EntryID: "a1", Role: "assistant", Text: "answer", Unix: 2},
	} {
		if _, err := repo.PutEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	graph, _, ok := sessions.TranscriptGraph("s1")
	if !ok || graph.Version != 1 || graph.ActiveLeafID != "a1" || graph.Revision != 3 {
		t.Fatalf("graph=%+v ok=%v", graph, ok)
	}
	answer, err := repo.GetEntry(ctx, "s1", "a1")
	if err != nil || answer.ParentEntryID != "u1" {
		t.Fatalf("answer=%+v err=%v", answer, err)
	}
	active, err := repo.ListSessionAll(ctx, "s1")
	if err != nil || len(active) != 2 {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	if err := repo.WriteSnapshot(ctx, "snap-1", "s1", active); err != nil {
		t.Fatal(err)
	}
	restored, err := repo.ReadSnapshot(ctx, "snap-1", "s1")
	if err != nil || len(restored) != 2 || restored[1].ParentEntryID != "u1" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}

	heads := ReplaceTranscriptHead(graph.BranchHeads, graph.ActiveLeafID, "u1", true)
	if _, err := sessions.CommitTranscriptGraph("s1", graph.Revision, TranscriptGraphMutation{ActiveLeafID: "u1", BranchHeads: heads}); err != nil {
		t.Fatal(err)
	}
	active, err = repo.ListSessionAll(ctx, "s1")
	if err != nil || len(active) != 1 || active[0].EntryID != "u1" {
		t.Fatalf("rewound active=%+v err=%v", active, err)
	}
	if _, err := sessions.CommitTranscriptGraph("s1", graph.Revision, TranscriptGraphMutation{ActiveLeafID: "a1", BranchHeads: heads}); !errors.Is(err, ErrTranscriptRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
	reopened, err := NewSessionStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, ok := reopened.TranscriptGraph("s1")
	if !ok || persisted.ActiveLeafID != "u1" || len(persisted.BranchHeads) != 2 {
		t.Fatalf("persisted graph=%+v ok=%v", persisted, ok)
	}
}

func TestTranscriptPathRejectsInvalidAncestry(t *testing.T) {
	ctx := context.Background()
	repo := NewTranscriptRepository(newMemStateStore(), "author")
	if _, err := repo.PutDetachedEntry(ctx, TranscriptEntryDoc{SessionID: "s1", EntryID: "cycle", ParentEntryID: "cycle", Role: "user", Text: "x", Unix: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListSessionPath(ctx, "s1", "cycle"); !errors.Is(err, ErrTranscriptGraphCorrupt) {
		t.Fatalf("expected corrupt graph error, got %v", err)
	}
}
