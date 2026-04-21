package main

import (
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

func TestDistillTurnState_SkipsTextOnlyTurn(t *testing.T) {
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: "Hello, how can I help?"},
	}
	docs := distillTurnState("sess-1", "evt-1", nil, delta, false)
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs for text-only turn, got %d", len(docs))
	}
}

func TestDistillTurnState_SuccessfulToolTurn(t *testing.T) {
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "web_search", Args: map[string]any{"q": "go generics"}}, Result: "found 3 results"},
		{Call: agent.ToolCall{Name: "memory_store", Args: map[string]any{"text": "saved"}}, Result: "ok"},
	}
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: "Let me search for that."},
		{Role: "tool", Content: "found 3 results"},
		{Role: "assistant", Content: "I found some information about Go generics."},
	}
	docs := distillTurnState("sess-1", "evt-1", traces, delta, false)
	if len(docs) != 1 {
		t.Fatalf("expected 1 outcome doc, got %d", len(docs))
	}
	doc := docs[0]
	if doc.Type != state.MemoryTypeEpisodic {
		t.Errorf("expected type=%s, got %s", state.MemoryTypeEpisodic, doc.Type)
	}
	if doc.EpisodeKind != state.EpisodeKindOutcome {
		t.Errorf("expected kind=%s, got %s", state.EpisodeKindOutcome, doc.EpisodeKind)
	}
	if !strings.Contains(doc.Text, "web_search") {
		t.Errorf("expected text to mention web_search, got %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "memory_store") {
		t.Errorf("expected text to mention memory_store, got %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "2 calls") {
		t.Errorf("expected text to mention call count, got %q", doc.Text)
	}
	if doc.Source != state.MemorySourceSystem {
		t.Errorf("expected source=%s, got %s", state.MemorySourceSystem, doc.Source)
	}
	if doc.SessionID != "sess-1" {
		t.Errorf("expected session_id=sess-1, got %s", doc.SessionID)
	}
}

func TestDistillTurnState_ToolErrors(t *testing.T) {
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "file_read"}, Result: "contents of file"},
		{Call: agent.ToolCall{Name: "file_write"}, Error: "permission denied"},
		{Call: agent.ToolCall{Name: "web_search"}, Error: "timeout after 30s"},
	}
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: "Encountered some issues."},
	}
	docs := distillTurnState("sess-1", "evt-1", traces, delta, false)
	// 1 outcome doc + 2 error docs
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs (1 outcome + 2 errors), got %d", len(docs))
	}

	// Outcome doc should mention errors.
	outcome := docs[0]
	if !strings.Contains(outcome.Text, "2 errors") {
		t.Errorf("outcome should mention 2 errors, got %q", outcome.Text)
	}

	// Error docs should each reference the tool name.
	for _, doc := range docs[1:] {
		if doc.EpisodeKind != state.EpisodeKindError {
			t.Errorf("expected error kind, got %s", doc.EpisodeKind)
		}
		if doc.Topic != "tool-error" {
			t.Errorf("expected topic=tool-error, got %s", doc.Topic)
		}
	}
	if !strings.Contains(docs[1].Text, "file_write") {
		t.Errorf("first error doc should mention file_write: %q", docs[1].Text)
	}
	if !strings.Contains(docs[2].Text, "web_search") {
		t.Errorf("second error doc should mention web_search: %q", docs[2].Text)
	}
}

func TestDistillTurnState_FailedTurn(t *testing.T) {
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "web_search"}, Result: "partial results"},
	}
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: "Searching..."},
	}
	docs := distillTurnState("sess-1", "evt-1", traces, delta, true)
	if len(docs) < 1 {
		t.Fatal("expected at least 1 doc for failed turn")
	}
	doc := docs[0]
	if doc.EpisodeKind != state.EpisodeKindError {
		t.Errorf("expected error kind for failed turn, got %s", doc.EpisodeKind)
	}
	if !strings.Contains(doc.Text, "Turn failed") {
		t.Errorf("expected text to start with 'Turn failed', got %q", doc.Text)
	}
	if doc.Topic != "turn-error" {
		t.Errorf("expected topic=turn-error, got %s", doc.Topic)
	}
}

func TestDistillTurnState_FailedTurnNoTools(t *testing.T) {
	docs := distillTurnState("sess-1", "evt-1", nil, nil, true)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc for failed turn with no tools, got %d", len(docs))
	}
	if docs[0].EpisodeKind != state.EpisodeKindError {
		t.Errorf("expected error kind, got %s", docs[0].EpisodeKind)
	}
}

func TestDistillTurnState_DeduplicatesToolNames(t *testing.T) {
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "web_search"}, Result: "r1"},
		{Call: agent.ToolCall{Name: "web_search"}, Result: "r2"},
		{Call: agent.ToolCall{Name: "web_search"}, Result: "r3"},
	}
	docs := distillTurnState("sess-1", "evt-1", traces, nil, false)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	// Should say "3 calls" but only list web_search once.
	if !strings.Contains(docs[0].Text, "3 calls") {
		t.Errorf("expected '3 calls' in text, got %q", docs[0].Text)
	}
	// Count occurrences of "web_search" in keyword list — should be exactly 1.
	count := 0
	for _, kw := range docs[0].Keywords {
		if kw == "web_search" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected web_search keyword once, got %d", count)
	}
}

func TestDistillAssistantSnippet_TruncatesLongText(t *testing.T) {
	long := strings.Repeat("x", 300)
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: long},
	}
	snippet := distillAssistantSnippet(delta, 120)
	if len(snippet) > 125 { // 120 + "…"
		t.Errorf("snippet too long: %d chars", len(snippet))
	}
	if !strings.HasSuffix(snippet, "…") {
		t.Errorf("expected truncated snippet to end with …")
	}
}

func TestDistillAssistantSnippet_SkipsToolCallMessages(t *testing.T) {
	delta := []agent.ConversationMessage{
		{Role: "assistant", Content: "I'll search", ToolCalls: []agent.ToolCallRef{{Name: "web_search"}}},
		{Role: "tool", Content: "results"},
		{Role: "assistant", Content: "Here are the results."},
	}
	snippet := distillAssistantSnippet(delta, 200)
	if snippet != "Here are the results." {
		t.Errorf("expected final assistant text, got %q", snippet)
	}
}

func TestDistillMemoryID_Deterministic(t *testing.T) {
	id1 := distillMemoryID("outcome", "sess-1", "evt-1", 1000)
	id2 := distillMemoryID("outcome", "sess-1", "evt-1", 1000)
	if id1 != id2 {
		t.Errorf("expected deterministic IDs, got %q and %q", id1, id2)
	}
	id3 := distillMemoryID("err-0", "sess-1", "evt-1", 1000)
	if id1 == id3 {
		t.Error("different kinds should produce different IDs")
	}
}

func TestBuildToolErrorDocs_TruncatesLongErrors(t *testing.T) {
	longErr := strings.Repeat("e", 1000)
	traces := []agent.ToolTrace{
		{Call: agent.ToolCall{Name: "tool_a"}, Error: longErr},
	}
	docs := buildToolErrorDocs("sess-1", "evt-1", traces, 1000)
	if len(docs) != 1 {
		t.Fatalf("expected 1 error doc, got %d", len(docs))
	}
	// Error text in the doc should be capped.
	if len(docs[0].Text) > 600 { // "Tool \"tool_a\" error: " + 512
		t.Errorf("error doc text too long: %d chars", len(docs[0].Text))
	}
}
