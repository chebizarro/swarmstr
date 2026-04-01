package agent

import (
	"context"
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
	if got.Outcome != "" || got.StopReason != "" {
		t.Errorf("expected zero classification for tool-only partial extraction, got outcome=%q stop_reason=%q", got.Outcome, got.StopReason)
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

func TestBuildTurnResultMetadata_InfersSuccessfulTurn(t *testing.T) {
	meta, ok := BuildTurnResultMetadata(TurnResult{
		Text:  "Done.",
		Usage: TurnUsage{InputTokens: 5, OutputTokens: 2},
	}, nil)
	if !ok {
		t.Fatal("expected successful turn metadata")
	}
	if meta.Outcome != TurnOutcomeCompleted {
		t.Fatalf("Outcome = %q, want %q", meta.Outcome, TurnOutcomeCompleted)
	}
	if meta.StopReason != TurnStopReasonModelText {
		t.Fatalf("StopReason = %q, want %q", meta.StopReason, TurnStopReasonModelText)
	}
	if meta.Usage.InputTokens != 5 || meta.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", meta.Usage)
	}
}

func TestBuildTurnResultMetadata_UsesPartialTurnClassification(t *testing.T) {
	err := &TurnExecutionError{
		Cause: fmt.Errorf("tool loop blocked"),
		Partial: TurnResult{
			HistoryDelta: []ConversationMessage{{Role: "assistant", Content: "blocked"}},
			Outcome:      TurnOutcomeBlocked,
			StopReason:   TurnStopReasonLoopBlocked,
			Usage:        TurnUsage{InputTokens: 8, OutputTokens: 1},
		},
	}
	meta, ok := BuildTurnResultMetadata(TurnResult{}, err)
	if !ok {
		t.Fatal("expected partial turn metadata")
	}
	if meta.Outcome != TurnOutcomeBlocked {
		t.Fatalf("Outcome = %q, want %q", meta.Outcome, TurnOutcomeBlocked)
	}
	if meta.StopReason != TurnStopReasonLoopBlocked {
		t.Fatalf("StopReason = %q, want %q", meta.StopReason, TurnStopReasonLoopBlocked)
	}
	if meta.Usage.InputTokens != 8 || meta.Usage.OutputTokens != 1 {
		t.Fatalf("unexpected usage: %+v", meta.Usage)
	}
}

func TestBuildTurnResultMetadata_UsesPartialUsageWithoutHistory(t *testing.T) {
	err := &TurnExecutionError{
		Cause: context.DeadlineExceeded,
		Partial: TurnResult{
			Usage: TurnUsage{InputTokens: 13, OutputTokens: 3},
		},
	}
	meta, ok := BuildTurnResultMetadata(TurnResult{}, err)
	if !ok {
		t.Fatal("expected metadata for partial usage without history")
	}
	if meta.Outcome != TurnOutcomeAborted {
		t.Fatalf("Outcome = %q, want %q", meta.Outcome, TurnOutcomeAborted)
	}
	if meta.StopReason != TurnStopReasonCancelled {
		t.Fatalf("StopReason = %q, want %q", meta.StopReason, TurnStopReasonCancelled)
	}
	if meta.Usage.InputTokens != 13 || meta.Usage.OutputTokens != 3 {
		t.Fatalf("unexpected usage: %+v", meta.Usage)
	}
}
