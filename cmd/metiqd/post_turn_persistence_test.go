package main

import (
	"context"
	"testing"

	"metiq/internal/agent"
	ctxengine "metiq/internal/context"
	"metiq/internal/memory"
	"metiq/internal/store/state"
)

// newTestPostTurnServices builds a postTurnPersistenceServices wired to
// in-memory/temp-backed stores suitable for unit tests.
func newTestPostTurnServices(t *testing.T, engine ctxengine.Engine) (postTurnPersistenceServices, *state.TranscriptRepository, *state.SessionStore) {
	t.Helper()
	transcriptRepo := state.NewTranscriptRepository(newTestStore(), "post-turn-test")
	sessionStore := newTestSessionStore(t)
	memoryIndex, err := memory.OpenIndex("")
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	svc := postTurnPersistenceServices{
		transcriptRepo:       transcriptRepo,
		contextEngine:        engine,
		sessionStore:         sessionStore,
		sessionMemoryRuntime: newSessionMemoryRuntime(sessionStore, transcriptRepo),
		docsRepo:             state.NewDocsRepository(newTestStore(), "post-turn-test"),
		memoryRepo:           state.NewMemoryRepository(newTestStore(), "post-turn-test"),
		memoryIndex:          memoryIndex,
		memoryTracker:        newMemoryIndexTracker(state.CheckpointDoc{}),
	}
	return svc, transcriptRepo, sessionStore
}

// TestPersistPostTurn_PersistsHistoryAndTaskState verifies that an auto-join
// channel turn routed through persistPostTurn persists turn history and updates
// task-state — the pipeline that was previously skipped, matching the DM path
// (swarmstr-nibw).
func TestPersistPostTurn_PersistsHistoryAndTaskState(t *testing.T) {
	ctx := context.Background()
	svc, transcriptRepo, sessionStore := newTestPostTurnServices(t, nil)

	const sessionID = "ch:room1:pubkeyA"
	result := agent.TurnResult{
		Text:       "found them",
		ToolTraces: []agent.ToolTrace{{Call: agent.ToolCall{Name: "web_search"}, Result: "ok"}},
		HistoryDelta: []agent.ConversationMessage{
			{Role: "user", Content: "search the docs", ID: "u1"},
			{Role: "assistant", Content: "found them", ID: "a1"},
		},
	}

	persistPostTurn(svc, postTurnPersistenceParams{
		Ctx:       ctx,
		Config:    state.ConfigDoc{},
		Scope:     memory.ScopedContext{},
		Runtime:   nil,
		SessionID: sessionID,
		EventID:   "evt-1",
		AgentID:   "main",
		Result:    result,
	})

	// Turn history (user + assistant) must be persisted to the transcript.
	entries, err := transcriptRepo.ListSessionAll(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListSessionAll: %v", err)
	}
	haveUser, haveAssistant := false, false
	for _, e := range entries {
		switch e.EntryID {
		case "u1":
			haveUser = true
		case "a1":
			haveAssistant = true
		}
	}
	if !haveUser || !haveAssistant {
		t.Fatalf("expected user+assistant history persisted, got %d entries: %+v", len(entries), entries)
	}

	// Task-state must be updated because the turn used a tool (parity with DM).
	entry, ok := sessionStore.Get(sessionID)
	if !ok || entry.TaskState == nil {
		t.Fatal("expected task state to be updated after a tool-using turn")
	}
}

// TestPersistPostTurn_IngestsIntoContextEngine verifies the history delta is
// ingested into the context engine so future turns can see prior messages —
// the ingestion half of persistAndIngestTurnHistory that auto-join dropped.
func TestPersistPostTurn_IngestsIntoContextEngine(t *testing.T) {
	ctx := context.Background()
	eng, err := ctxengine.NewEngine("windowed", "global", map[string]any{"max_messages": float64(50)})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	svc, _, _ := newTestPostTurnServices(t, eng)

	const sessionID = "ch:room2:pubkeyB"
	result := agent.TurnResult{
		Text: "hi there",
		HistoryDelta: []agent.ConversationMessage{
			{Role: "user", Content: "hello", ID: "u1"},
			{Role: "assistant", Content: "hi there", ID: "a1"},
		},
	}

	persistPostTurn(svc, postTurnPersistenceParams{
		Ctx:       ctx,
		Config:    state.ConfigDoc{},
		SessionID: sessionID,
		EventID:   "evt-2",
		AgentID:   "main",
		Result:    result,
	})

	assembled, err := eng.Assemble(ctx, sessionID, 100_000)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(assembled.Messages) == 0 {
		t.Fatal("expected history to be ingested into the context engine")
	}
}
