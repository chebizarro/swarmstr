package methods

import (
	"context"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func newSessionControlRepos() (*state.DocsRepository, *state.TranscriptRepository) {
	store := newTaskControlTestStore()
	return state.NewDocsRepository(store, "author"), state.NewTranscriptRepository(store, "author")
}

func TestGetSessionWithTranscript(t *testing.T) {
	docsRepo, transcriptRepo := newSessionControlRepos()
	ctx := context.Background()

	if _, err := docsRepo.PutSession(ctx, "sess-1", state.SessionDoc{
		Version: 1, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version: 1, SessionID: "sess-1", EntryID: "e1", Role: "user", Text: "hello", Unix: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := GetSessionWithTranscript(ctx, docsRepo, transcriptRepo, "sess-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.SessionID != "sess-1" {
		t.Fatalf("session_id = %q want sess-1", result.Session.SessionID)
	}
	if len(result.Transcript) != 1 || result.Transcript[0].Text != "hello" {
		t.Fatalf("transcript mismatch: %+v", result.Transcript)
	}
}

func TestGetSessionWithTranscript_NotFound(t *testing.T) {
	docsRepo, transcriptRepo := newSessionControlRepos()
	_, err := GetSessionWithTranscript(context.Background(), docsRepo, transcriptRepo, "nonexistent", 10)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestGetChatHistory(t *testing.T) {
	docsRepo, transcriptRepo := newSessionControlRepos()
	ctx := context.Background()

	if _, err := docsRepo.PutSession(ctx, "sess-1", state.SessionDoc{
		Version: 1, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"first", "second"} {
		if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
			Version: 1, SessionID: "sess-1", EntryID: "e" + string(rune('1'+i)), Role: "user", Text: text, Unix: time.Now().Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := GetChatHistory(ctx, docsRepo, transcriptRepo, "sess-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["session_id"] != "sess-1" {
		t.Fatalf("session_id = %v want sess-1", result["session_id"])
	}
}

func TestGetChatHistory_SessionNotFound(t *testing.T) {
	docsRepo, transcriptRepo := newSessionControlRepos()
	_, err := GetChatHistory(context.Background(), docsRepo, transcriptRepo, "nonexistent", 10)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestPreviewSession(t *testing.T) {
	docsRepo, transcriptRepo := newSessionControlRepos()
	ctx := context.Background()

	if _, err := docsRepo.PutSession(ctx, "sess-1", state.SessionDoc{
		Version: 1, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version: 1, SessionID: "sess-1", EntryID: "e1", Role: "user", Text: "preview me", Unix: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := PreviewSession(ctx, docsRepo, transcriptRepo, "sess-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	session, ok := result["session"].(state.SessionDoc)
	if !ok {
		t.Fatalf("expected SessionDoc, got %T", result["session"])
	}
	if session.SessionID != "sess-1" {
		t.Fatalf("session_id = %q want sess-1", session.SessionID)
	}
	preview, ok := result["preview"].([]state.TranscriptEntryDoc)
	if !ok {
		t.Fatalf("expected []TranscriptEntryDoc, got %T", result["preview"])
	}
	if len(preview) != 1 || preview[0].Text != "preview me" {
		t.Fatalf("preview mismatch: %+v", preview)
	}
}

func TestExportSessionHTML(t *testing.T) {
	docsRepo, transcriptRepo := newSessionControlRepos()
	ctx := context.Background()

	if _, err := docsRepo.PutSession(ctx, "sess-1", state.SessionDoc{
		Version: 1, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version: 1, SessionID: "sess-1", EntryID: "e1", Role: "user", Text: "exported message", Unix: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := ExportSessionHTML(ctx, docsRepo, transcriptRepo, "sess-1", "pubkey-123")
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "html" {
		t.Fatalf("format = %q want html", result.Format)
	}
	if result.HTML == "" {
		t.Fatal("expected non-empty HTML output")
	}
}
