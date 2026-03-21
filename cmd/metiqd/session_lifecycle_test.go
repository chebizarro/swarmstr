package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestRotateSessionLifecycle_ArchivesAndCarriesFlags(t *testing.T) {
	t.Setenv("SWARMSTR_SESSION_ARCHIVE_DIR", t.TempDir())

	repo := state.NewTranscriptRepository(newTestStore(), "author")
	sessionID := "npub-test-1"
	now := time.Now().UTC()
	putTranscriptEntry(t, repo, state.TranscriptEntryDoc{
		SessionID: sessionID,
		EntryID:   "u1",
		Role:      "user",
		Text:      "hello",
		Unix:      now.Add(-2 * time.Minute).Unix(),
	})
	putTranscriptEntry(t, repo, state.TranscriptEntryDoc{
		SessionID: sessionID,
		EntryID:   "a1",
		Role:      "assistant",
		Text:      "world",
		Unix:      now.Add(-1 * time.Minute).Unix(),
	})

	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	entry := ss.GetOrNew(sessionID)
	entry.ModelOverride = "claude-3"
	entry.ProviderOverride = "anthropic"
	if err := ss.Put(sessionID, entry); err != nil {
		t.Fatalf("seed session store: %v", err)
	}

	outcome, err := rotateSessionLifecycle(context.Background(), sessionID, "slash:/reset", state.ConfigDoc{}, repo, ss, now)
	if err != nil {
		t.Fatalf("rotate lifecycle: %v", err)
	}
	if strings.TrimSpace(outcome.ArchivePath) == "" {
		t.Fatal("expected non-empty archive path")
	}
	if _, err := os.Stat(outcome.ArchivePath); err != nil {
		t.Fatalf("archive file not written: %v", err)
	}
	raw, err := os.ReadFile(outcome.ArchivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !strings.Contains(string(raw), "\"entry_id\":\"u1\"") || !strings.Contains(string(raw), "\"entry_id\":\"a1\"") {
		t.Fatalf("archive missing expected entries: %s", string(raw))
	}

	remaining, err := repo.ListSession(context.Background(), sessionID, 100)
	if err != nil {
		t.Fatalf("list session after rotate: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected cleared transcript, got %d entries", len(remaining))
	}

	got, ok := ss.Get(sessionID)
	if !ok {
		t.Fatal("session entry missing after rotate")
	}
	if got.ModelOverride != "claude-3" || got.ProviderOverride != "anthropic" {
		t.Fatalf("carry-over mismatch: %+v", got)
	}
	if got.SessionFile == "" {
		t.Fatalf("expected session_file to be set: %+v", got)
	}
	if got.SpawnedBy != "slash:/reset" {
		t.Fatalf("expected spawned_by to capture reason, got %q", got.SpawnedBy)
	}
}

func TestRotateSessionLifecycle_ForkSeedEnabled(t *testing.T) {
	t.Setenv("SWARMSTR_SESSION_ARCHIVE_DIR", t.TempDir())

	repo := state.NewTranscriptRepository(newTestStore(), "author")
	sessionID := "npub-test-fork"
	now := time.Now().UTC()
	putTranscriptEntry(t, repo, state.TranscriptEntryDoc{
		SessionID: sessionID,
		EntryID:   "u1",
		Role:      "user",
		Text:      "first",
		Unix:      now.Add(-3 * time.Minute).Unix(),
	})
	putTranscriptEntry(t, repo, state.TranscriptEntryDoc{
		SessionID: sessionID,
		EntryID:   "a1",
		Role:      "assistant",
		Text:      "second",
		Unix:      now.Add(-2 * time.Minute).Unix(),
	})

	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"session_reset": map[string]any{
				"fork_parent":      true,
				"fork_max_entries": 1.0,
			},
		},
	}
	outcome, err := rotateSessionLifecycle(context.Background(), sessionID, "stale:dm", cfg, repo, ss, now)
	if err != nil {
		t.Fatalf("rotate lifecycle with fork: %v", err)
	}
	if !outcome.Forked {
		t.Fatal("expected forked outcome")
	}

	entries, err := repo.ListSession(context.Background(), sessionID, 100)
	if err != nil {
		t.Fatalf("list session: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 fork seed entry, got %d", len(entries))
	}
	if entries[0].Role != "system" {
		t.Fatalf("expected system fork entry, got role=%q", entries[0].Role)
	}
	if !strings.Contains(entries[0].Text, "Parent context carried over") {
		t.Fatalf("fork seed text mismatch: %q", entries[0].Text)
	}

	got, ok := ss.Get(sessionID)
	if !ok {
		t.Fatal("session entry missing after fork")
	}
	if !got.ForkedFromParent {
		t.Fatalf("expected forked_from_parent=true: %+v", got)
	}
}

func putTranscriptEntry(t *testing.T, repo *state.TranscriptRepository, entry state.TranscriptEntryDoc) {
	t.Helper()
	if _, err := repo.PutEntry(context.Background(), entry); err != nil {
		t.Fatalf("put transcript entry %s: %v", entry.EntryID, err)
	}
}
