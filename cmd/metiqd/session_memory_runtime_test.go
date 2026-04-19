package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

type stubSessionMemoryGenerator struct {
	text string
}

func (s stubSessionMemoryGenerator) Generate(context.Context, agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{Text: s.text}, nil
}

func TestSessionMemoryExtractOnce_PreservesCheckpointWhenNoNewTranscriptEntries(t *testing.T) {
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	transcriptRepo := state.NewTranscriptRepository(newTestStore(), "author")
	if _, err := transcriptRepo.PutEntry(context.Background(), state.TranscriptEntryDoc{
		Version:   1,
		SessionID: "sess-a",
		EntryID:   "e1",
		Role:      "user",
		Text:      "hello",
		Unix:      time.Now().Unix(),
	}); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	if err := sessionStore.Put("sess-a", state.SessionEntry{
		SessionID:                "sess-a",
		SessionMemoryInitialized: true,
		SessionMemoryLastEntryID: "e1",
	}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}
	runtime := newSessionMemoryRuntime(sessionStore, transcriptRepo)
	_, err = runtime.extractOnce(context.Background(), "sess-a", t.TempDir(), sessionMemoryConfigFromDoc(state.ConfigDoc{}), stubSessionMemoryGenerator{text: testSessionMemoryDocument("No new transcript should preserve the prior checkpoint.")}, 45*time.Second)
	if err != nil {
		t.Fatalf("extractOnce: %v", err)
	}
	entry, ok := sessionStore.Get("sess-a")
	if !ok {
		t.Fatal("session entry missing after extractOnce")
	}
	if entry.SessionMemoryLastEntryID != "e1" {
		t.Fatalf("expected previous checkpoint preserved, got %+v", entry)
	}
}

func TestSessionMemoryBuildTranscriptExcerpt_ContinuesBeyondFirstLineWhenPageHasMore(t *testing.T) {
	transcriptRepo := state.NewTranscriptRepository(newTestStore(), "author")
	now := time.Now().Unix()
	for i := 0; i < sessionMemoryTranscriptScanLimit+1; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := transcriptRepo.PutEntry(context.Background(), state.TranscriptEntryDoc{
			Version:   1,
			SessionID: "sess-a",
			EntryID:   fmt.Sprintf("e%04d", i),
			Role:      role,
			Text:      fmt.Sprintf("message-%04d", i),
			Unix:      now + int64(i),
		}); err != nil {
			t.Fatalf("seed transcript %d: %v", i, err)
		}
	}
	runtime := newSessionMemoryRuntime(nil, transcriptRepo)
	excerpt, lastEntryID, hasMore, err := runtime.buildTranscriptExcerpt(context.Background(), "sess-a", "", 120)
	if err != nil {
		t.Fatalf("buildTranscriptExcerpt: %v", err)
	}
	if !hasMore {
		t.Fatalf("expected hasMore with %d+ transcript entries", sessionMemoryTranscriptScanLimit)
	}
	if !strings.Contains(excerpt, "message-0000") || !strings.Contains(excerpt, "message-0001") {
		t.Fatalf("expected excerpt to include multiple entries, got: %s", excerpt)
	}
	if strings.TrimSpace(lastEntryID) == "" {
		t.Fatalf("expected lastEntryID to advance, got %q", lastEntryID)
	}
}
