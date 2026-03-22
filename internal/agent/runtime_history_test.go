package agent

import (
	"errors"
	"fmt"
	"testing"
)

func TestToolCallToRef(t *testing.T) {
	tc := ToolCall{
		ID:   "call_123",
		Name: "nostr_fetch",
		Args: map[string]any{"kinds": []any{1.0}, "limit": 10.0},
	}
	ref := ToolCallToRef(tc)
	if ref.ID != "call_123" {
		t.Errorf("ID = %q, want %q", ref.ID, "call_123")
	}
	if ref.Name != "nostr_fetch" {
		t.Errorf("Name = %q, want %q", ref.Name, "nostr_fetch")
	}
	if ref.ArgsJSON == "" {
		t.Fatal("ArgsJSON should not be empty")
	}
	// Verify it's valid JSON containing expected keys.
	if got := ref.ArgsJSON; got == "" {
		t.Fatal("expected non-empty ArgsJSON")
	}
}

func TestToolCallToRef_EmptyArgs(t *testing.T) {
	tc := ToolCall{ID: "call_0", Name: "relay_list"}
	ref := ToolCallToRef(tc)
	if ref.ArgsJSON != "" {
		t.Errorf("ArgsJSON = %q, want empty for nil args", ref.ArgsJSON)
	}
}

func TestConversationMessage_ToolCalls(t *testing.T) {
	msg := ConversationMessage{
		Role: "assistant",
		ToolCalls: []ToolCallRef{
			{ID: "c1", Name: "bash_exec", ArgsJSON: `{"command":"ls"}`},
			{ID: "c2", Name: "read_file", ArgsJSON: `{"path":"/tmp/x"}`},
		},
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "bash_exec" {
		t.Errorf("first call = %q, want bash_exec", msg.ToolCalls[0].Name)
	}
}

func TestTurnResult_HistoryDelta(t *testing.T) {
	result := TurnResult{
		Text: "Done.",
		HistoryDelta: []ConversationMessage{
			{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "c1", Name: "read_file"}}},
			{Role: "tool", ToolCallID: "c1", Content: "file contents..."},
			{Role: "assistant", Content: "Done."},
		},
	}
	if len(result.HistoryDelta) != 3 {
		t.Fatalf("expected 3 delta messages, got %d", len(result.HistoryDelta))
	}
	if result.HistoryDelta[0].Role != "assistant" || len(result.HistoryDelta[0].ToolCalls) != 1 {
		t.Error("first delta should be assistant tool-call")
	}
	if result.HistoryDelta[1].Role != "tool" || result.HistoryDelta[1].ToolCallID != "c1" {
		t.Error("second delta should be tool result linked to c1")
	}
}

func TestPartialTurnResult_WithPartial(t *testing.T) {
	cause := fmt.Errorf("context deadline exceeded")
	partial := TurnResult{
		HistoryDelta: []ConversationMessage{
			{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "c1", Name: "bash_exec"}}},
			{Role: "tool", ToolCallID: "c1", Content: "ok"},
		},
		ToolTraces: []ToolTrace{{Call: ToolCall{ID: "c1", Name: "bash_exec"}, Result: "ok"}},
	}
	err := &TurnExecutionError{Cause: cause, Partial: partial}

	got, ok := PartialTurnResult(err)
	if !ok {
		t.Fatal("expected ok=true for error with partial history")
	}
	if len(got.HistoryDelta) != 2 {
		t.Errorf("expected 2 delta messages, got %d", len(got.HistoryDelta))
	}
	if len(got.ToolTraces) != 1 {
		t.Errorf("expected 1 trace, got %d", len(got.ToolTraces))
	}
}

func TestPartialTurnResult_EmptyPartial(t *testing.T) {
	err := &TurnExecutionError{Cause: fmt.Errorf("fail"), Partial: TurnResult{}}
	_, ok := PartialTurnResult(err)
	if ok {
		t.Error("expected ok=false for empty partial result")
	}
}

func TestPartialTurnResult_NonTurnError(t *testing.T) {
	_, ok := PartialTurnResult(fmt.Errorf("regular error"))
	if ok {
		t.Error("expected ok=false for non-TurnExecutionError")
	}
}

func TestTurnExecutionError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("deadline exceeded")
	err := &TurnExecutionError{Cause: inner}
	if !errors.Is(err, inner) {
		t.Error("Unwrap should expose the cause")
	}
	if err.Error() != "deadline exceeded" {
		t.Errorf("Error() = %q, want %q", err.Error(), "deadline exceeded")
	}
}
