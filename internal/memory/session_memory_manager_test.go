package memory

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/store/state"
)

type fakeSessionMemoryStore struct {
	mu      sync.Mutex
	entries map[string]state.SessionEntry
}

func newFakeSessionMemoryStore() *fakeSessionMemoryStore {
	return &fakeSessionMemoryStore{entries: map[string]state.SessionEntry{}}
}

func (s *fakeSessionMemoryStore) Get(key string) (state.SessionEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	return entry, ok
}

func (s *fakeSessionMemoryStore) GetOrNew(key string) state.SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		entry = state.SessionEntry{SessionID: key, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		s.entries[key] = entry
	}
	return entry
}

func (s *fakeSessionMemoryStore) Put(key string, entry state.SessionEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.SessionID = key
	s.entries[key] = entry
	return nil
}

type fakeTranscriptReader struct {
	page state.TranscriptPage
	err  error
}

func (r fakeTranscriptReader) ListSessionPage(_ context.Context, _ string, _ string, _ int) (state.TranscriptPage, error) {
	return r.page, r.err
}

type fakeSessionMemoryExtractor struct {
	request SessionMemoryExtractionRequest
	calls   int
	doc     string
}

func (e *fakeSessionMemoryExtractor) ExtractSessionMemory(_ context.Context, req SessionMemoryExtractionRequest) (SessionMemoryExtractionResponse, error) {
	e.calls++
	e.request = req
	return SessionMemoryExtractionResponse{Document: e.doc}, nil
}

func sessionMemoryDocumentWithCurrentState(body string) string {
	desc := sessionMemorySections[1].Description
	return strings.Replace(strings.TrimSpace(DefaultSessionMemoryTemplate), desc, desc+"\n"+body, 1)
}

func TestSessionMemoryManager_ObserveTurnTriggersForkedExtractionAtTokenThreshold(t *testing.T) {
	store := newFakeSessionMemoryStore()
	extractor := &fakeSessionMemoryExtractor{doc: sessionMemoryDocumentWithCurrentState("Implemented token-threshold extraction.")}
	manager := NewSessionMemoryManager(SessionMemoryManagerOptions{
		SessionStore: store,
		TranscriptReader: fakeTranscriptReader{page: state.TranscriptPage{Entries: []state.TranscriptEntryDoc{
			{EntryID: "e1", Role: "user", Text: "please implement session memory"},
			{EntryID: "e2", Role: "assistant", Text: "done"},
		}}},
		Extractor:   extractor,
		Config:      SessionMemoryConfig{Enabled: true, InitTokens: 2, UpdateTokens: 2, ToolCallsBetweenUpdates: 2, MaxExcerptChars: 1000, MaxOutputBytes: MaxSessionMemoryBytes},
		Synchronous: true,
		Now:         func() time.Time { return time.Unix(123, 0) },
	})

	result, err := manager.ObserveTurn(context.Background(), SessionMemoryTurnObservation{
		SessionID:    "sess-a",
		WorkspaceDir: t.TempDir(),
		Observation:  SessionMemoryObservation{DeltaTokens: 2},
	})
	if err != nil {
		t.Fatalf("ObserveTurn: %v", err)
	}
	if !result.Observed || !result.Triggered {
		t.Fatalf("expected observed triggered result, got %+v", result)
	}
	if extractor.calls != 1 {
		t.Fatalf("expected one forked extraction, got %d", extractor.calls)
	}
	if extractor.request.ForkedSessionID != "sess-a:session-memory" {
		t.Fatalf("expected forked session id, got %q", extractor.request.ForkedSessionID)
	}
	if !strings.Contains(extractor.request.UserPrompt, "Recent transcript excerpt") || !strings.Contains(extractor.request.TranscriptExcerpt, "assistant: done") {
		t.Fatalf("expected transcript excerpt in extraction request: %+v", extractor.request)
	}
	entry, ok := store.Get("sess-a")
	if !ok || !entry.SessionMemoryInitialized || entry.SessionMemoryLastEntryID != "e2" || entry.SessionMemoryUpdatedAt != 123 {
		t.Fatalf("unexpected persisted session state ok=%v entry=%+v", ok, entry)
	}
}

func TestSessionMemoryManager_BuildRecallContextInjectsSummary(t *testing.T) {
	store := newFakeSessionMemoryStore()
	workspaceDir := t.TempDir()
	path, err := WriteSessionMemoryFile(workspaceDir, "sess-recall", sessionMemoryDocumentWithCurrentState("Continue with session memory manager tests."))
	if err != nil {
		t.Fatalf("WriteSessionMemoryFile: %v", err)
	}
	if err := store.Put("sess-recall", state.SessionEntry{
		SessionID:                  "sess-recall",
		SessionMemoryFile:          path,
		SessionMemoryInitialized:   true,
		SessionMemoryUpdatedAt:     456,
		SessionMemoryObservedChars: 200,
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionMemoryManager(SessionMemoryManagerOptions{SessionStore: store})
	recall, err := manager.BuildRecallContext(context.Background(), "sess-recall", workspaceDir, 10_000)
	if err != nil {
		t.Fatalf("BuildRecallContext: %v", err)
	}
	if !recall.Injected || !strings.Contains(recall.Prompt, "## Session Memory Recall") || !strings.Contains(recall.Prompt, "Continue with session memory manager tests.") {
		t.Fatalf("unexpected recall context: %+v", recall)
	}
}

func TestSessionMemoryObservationFromMessagesCountsToolNaturalBreaks(t *testing.T) {
	obs := SessionMemoryObservationFromMessages([]SessionMemoryConversationMessage{
		{Role: "assistant", Content: "I will inspect files", ToolCalls: 2},
		{Role: "tool", Content: "file contents", ToolResult: true},
	})
	if obs.ToolCalls != 2 || !obs.LastTurnHadToolCalls {
		t.Fatalf("unexpected observation: %+v", obs)
	}
	if obs.DeltaChars == 0 {
		t.Fatal("expected content characters to be counted")
	}
}
