package toolbuiltin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"swarmstr/internal/memory"
)

func newTestMemoryIndex(t *testing.T) *memory.Index {
	t.Helper()
	// Use a path that doesn't exist yet — OpenIndex handles os.IsNotExist gracefully.
	dir := t.TempDir()
	name := filepath.Join(dir, "test-memory.json")
	idx, err := memory.OpenIndex(name)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestMemoryStoreTool_Basic(t *testing.T) {
	idx := newTestMemoryIndex(t)
	tool := MemoryStoreTool(idx)

	result, err := tool(context.Background(), map[string]any{
		"text": "remember to buy milk",
		"tags": []interface{}{"shopping", "reminder"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if out["stored"] != true {
		t.Error("expected stored=true")
	}
	id, ok := out["id"].(string)
	if !ok || id == "" {
		t.Error("expected non-empty id")
	}

	// Verify searchability.
	if hits := idx.Search("milk", 5); len(hits) == 0 {
		t.Error("expected stored memory to be searchable")
	}
}

func TestMemoryStoreTool_WithSessionID(t *testing.T) {
	idx := newTestMemoryIndex(t)
	tool := MemoryStoreTool(idx)

	_, err := tool(context.Background(), map[string]any{
		"text":       "session scoped note",
		"session_id": "sess-abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hits := idx.ListSession("sess-abc", 10)
	if len(hits) == 0 {
		t.Error("expected entry scoped to sess-abc")
	}
}

func TestMemoryStoreTool_TagsAsString(t *testing.T) {
	idx := newTestMemoryIndex(t)
	tool := MemoryStoreTool(idx)
	// Tags provided as a single string (not slice) — should not error.
	_, err := tool(context.Background(), map[string]any{
		"text": "tagged entry",
		"tags": "work",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryStoreTool_MissingText(t *testing.T) {
	idx := newTestMemoryIndex(t)
	tool := MemoryStoreTool(idx)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing text")
	}
}

func TestMemoryDeleteTool_Basic(t *testing.T) {
	idx := newTestMemoryIndex(t)
	storeTool := MemoryStoreTool(idx)
	deleteTool := MemoryDeleteTool(idx)

	storeResult, _ := storeTool(context.Background(), map[string]any{"text": "delete me"})
	var stored map[string]any
	json.Unmarshal([]byte(storeResult), &stored)
	id := stored["id"].(string)

	delResult, err := deleteTool(context.Background(), map[string]any{"id": id})
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}
	var delOut map[string]any
	json.Unmarshal([]byte(delResult), &delOut)
	if delOut["deleted"] != true {
		t.Error("expected deleted=true")
	}

	// Verify it's gone.
	if hits := idx.Search("delete me", 5); len(hits) > 0 {
		t.Error("expected memory entry to be deleted")
	}
}

func TestMemoryDeleteTool_NotFound(t *testing.T) {
	idx := newTestMemoryIndex(t)
	tool := MemoryDeleteTool(idx)
	_, err := tool(context.Background(), map[string]any{"id": "nonexistent-id"})
	if err == nil {
		t.Error("expected error for nonexistent id")
	}
}

func TestMemoryDeleteTool_MissingID(t *testing.T) {
	idx := newTestMemoryIndex(t)
	tool := MemoryDeleteTool(idx)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing id")
	}
}
