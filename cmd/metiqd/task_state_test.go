package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

func TestUpdateSessionTaskState_SkipsTextOnlyTurns(t *testing.T) {
	store := newTestSessionStore(t)
	updateSessionTaskState(store, "sess-1", nil, nil, false)
	entry, ok := store.Get("sess-1")
	if ok && entry.TaskState != nil {
		t.Fatal("should not create task state for text-only turn")
	}
}

func TestUpdateSessionTaskState_RecordsToolUsage(t *testing.T) {
	store := newTestSessionStore(t)
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "web_search"}, Result: "ok"},
		{Call: agent.ToolCall{Name: "memory_store"}, Result: "ok"},
	}
	delta := []agent.ConversationMessage{
		{Role: "user", Content: "Find deployment docs"},
		{Role: "assistant", Content: "I found the deployment documentation."},
	}
	updateSessionTaskState(store, "sess-1", traces, delta, false)
	entry, ok := store.Get("sess-1")
	if !ok || entry.TaskState == nil {
		t.Fatal("expected task state to be created")
	}
	ts := entry.TaskState
	if !strings.Contains(ts.CurrentStage, "web_search") {
		t.Errorf("expected CurrentStage to mention web_search, got %q", ts.CurrentStage)
	}
	if ts.Brief != "Find deployment docs" {
		t.Errorf("expected Brief from first user message, got %q", ts.Brief)
	}
	if ts.NextAction == "" {
		t.Error("expected NextAction to be set from assistant message")
	}
	if ts.UpdatedAt == 0 {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestUpdateSessionTaskState_RecordsToolErrors(t *testing.T) {
	store := newTestSessionStore(t)
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "file_write"}, Error: "permission denied"},
	}
	updateSessionTaskState(store, "sess-1", traces, nil, false)
	entry, _ := store.Get("sess-1")
	if entry.TaskState == nil {
		t.Fatal("expected task state")
	}
	if len(entry.TaskState.Constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(entry.TaskState.Constraints))
	}
	if !strings.Contains(entry.TaskState.Constraints[0], "permission denied") {
		t.Errorf("constraint should contain error: %q", entry.TaskState.Constraints[0])
	}
}

func TestUpdateSessionTaskState_RecordsArtifactRefs(t *testing.T) {
	store := newTestSessionStore(t)
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "file_write", Args: map[string]any{"path": "/tmp/output.go"}}, Result: "ok"},
	}
	updateSessionTaskState(store, "sess-1", traces, nil, false)
	entry, _ := store.Get("sess-1")
	if entry.TaskState == nil || len(entry.TaskState.ArtifactRefs) != 1 {
		t.Fatal("expected 1 artifact ref")
	}
	if entry.TaskState.ArtifactRefs[0] != "/tmp/output.go" {
		t.Errorf("expected artifact path, got %q", entry.TaskState.ArtifactRefs[0])
	}
}

func TestUpdateSessionTaskState_FailedTurn(t *testing.T) {
	store := newTestSessionStore(t)
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "web_search"}, Result: "partial"},
	}
	updateSessionTaskState(store, "sess-1", traces, nil, true)
	entry, _ := store.Get("sess-1")
	if entry.TaskState == nil {
		t.Fatal("expected task state for failed turn")
	}
	if !strings.Contains(entry.TaskState.CurrentStage, "failed") {
		t.Errorf("expected CurrentStage to mention failure, got %q", entry.TaskState.CurrentStage)
	}
}

func TestUpdateSessionTaskState_BriefNotOverwritten(t *testing.T) {
	store := newTestSessionStore(t)
	// First turn sets Brief.
	traces1 := []agent.ToolTrace{{Call: agent.ToolCall{Name: "web_search"}, Result: "ok"}}
	delta1 := []agent.ConversationMessage{{Role: "user", Content: "Initial task"}}
	updateSessionTaskState(store, "sess-1", traces1, delta1, false)
	// Second turn should not overwrite Brief.
	traces2 := []agent.ToolTrace{{Call: agent.ToolCall{Name: "memory_store"}, Result: "ok"}}
	delta2 := []agent.ConversationMessage{{Role: "user", Content: "Follow up"}}
	updateSessionTaskState(store, "sess-1", traces2, delta2, false)

	entry, _ := store.Get("sess-1")
	if entry.TaskState.Brief != "Initial task" {
		t.Errorf("Brief should not be overwritten, got %q", entry.TaskState.Brief)
	}
}

func TestBuildTaskStateContextBlock_Empty(t *testing.T) {
	store := newTestSessionStore(t)
	block := buildTaskStateContextBlock(store, "nonexistent")
	if block != "" {
		t.Fatal("expected empty string for nonexistent session")
	}
}

func TestBuildTaskStateContextBlock_RendersState(t *testing.T) {
	store := newTestSessionStore(t)
	entry := store.GetOrNew("sess-1")
	entry.TaskState = &state.TaskState{
		Brief:        "Build auth",
		CurrentStage: "testing",
		NextAction:   "Run integration tests",
	}
	_ = store.Put("sess-1", entry)

	block := buildTaskStateContextBlock(store, "sess-1")
	if !strings.Contains(block, "[Task State]") {
		t.Error("expected [Task State] header")
	}
	if !strings.Contains(block, "Build auth") {
		t.Error("expected Brief in block")
	}
}

func TestExtractArtifactRef(t *testing.T) {
	tests := []struct {
		name     string
		trace    agent.ToolTrace
		expected string
	}{
		{"file_write with path", agent.ToolTrace{Call: agent.ToolCall{Name: "file_write", Args: map[string]any{"path": "/tmp/out.go"}}}, "/tmp/out.go"},
		{"file_create with path", agent.ToolTrace{Call: agent.ToolCall{Name: "file_create", Args: map[string]any{"path": "/tmp/new.go"}}}, "/tmp/new.go"},
		{"file_move with destination", agent.ToolTrace{Call: agent.ToolCall{Name: "file_move", Args: map[string]any{"destination": "/tmp/moved.go"}}}, "/tmp/moved.go"},
		{"web_search no artifact", agent.ToolTrace{Call: agent.ToolCall{Name: "web_search", Args: map[string]any{"q": "test"}}}, ""},
		// Note: extractArtifactRef does not check Error — the caller
		// (updateSessionTaskState) skips errored traces before calling it.
		{"file_write with error still returns path", agent.ToolTrace{Call: agent.ToolCall{Name: "file_write", Args: map[string]any{"path": "/tmp/err.go"}}, Error: "fail"}, "/tmp/err.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractArtifactRef(tt.trace)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

// newTestSessionStore creates a temporary session store for testing.
func newTestSessionStore(t *testing.T) *state.SessionStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	// Write an empty JSON file.
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := state.NewSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
