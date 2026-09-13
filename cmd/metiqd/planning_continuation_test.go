package main

import (
	"errors"
	"strings"
	"testing"

	"metiq/internal/agent"
)

func TestRunTurnWithPlanningContinuation_NotEnabled(t *testing.T) {
	var calls int
	result, used, err := runTurnWithPlanningContinuation(
		false,
		agent.Turn{SessionID: "s1"},
		func(turn agent.Turn) (agent.TurnResult, error) {
			calls++
			return agent.TurnResult{Text: "I'll do it!"}, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Error("expected used=false when disabled")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if result.Text != "I'll do it!" {
		t.Errorf("expected original text, got %q", result.Text)
	}
}

func TestRunTurnWithPlanningContinuation_ToolUsingText(t *testing.T) {
	var calls int
	result, used, err := runTurnWithPlanningContinuation(
		true,
		agent.Turn{SessionID: "s2"},
		func(turn agent.Turn) (agent.TurnResult, error) {
			calls++
			return agent.TurnResult{
				Text:        "I've done it.",
				ToolTraces:  []agent.ToolTrace{{Call: agent.ToolCall{Name: "read"}}},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Error("expected used=false when tools were called")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	_ = result
}

func TestRunTurnWithPlanningContinuation_PlanningOnlyRetry(t *testing.T) {
	callCount := 0
	result, used, err := runTurnWithPlanningContinuation(
		true,
		agent.Turn{SessionID: "s3", Context: "some context"},
		func(turn agent.Turn) (agent.TurnResult, error) {
			callCount++
			if callCount == 1 {
				// First call: planning-only response with promise language, no tools
				return agent.TurnResult{
					Text: "I'll review the code and get back to you.",
				}, nil
			}
			// Second call (continuation): actual work
			return agent.TurnResult{
				Text:        "Found the bug on line 42. Fixed it.",
				ToolTraces:  []agent.ToolTrace{{Call: agent.ToolCall{Name: "read"}}},
				HistoryDelta: []agent.ConversationMessage{
					{Role: "assistant", Content: "I'll review the code and get back to you."},
				},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !used {
		t.Error("expected used=true after planning-only retry")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
	if !strings.Contains(result.Text, "Found the bug") {
		t.Errorf("expected continuation result, got %q", result.Text)
	}
}

func TestRunTurnWithPlanningContinuation_SecondCallError(t *testing.T) {
	callCount := 0
	result, used, err := runTurnWithPlanningContinuation(
		true,
		agent.Turn{SessionID: "s4"},
		func(turn agent.Turn) (agent.TurnResult, error) {
			callCount++
			if callCount == 1 {
				return agent.TurnResult{
					Text: "I'll fix the bug.",
				}, nil
			}
			return agent.TurnResult{}, errors.New("rate limited")
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Error("expected used=false when continuation fails")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
	if result.Text != "I'll fix the bug." {
		t.Errorf("expected original fallback text, got %q", result.Text)
	}
}

func TestRunTurnWithPlanningContinuation_RunError(t *testing.T) {
	var calls int
	_, _, err := runTurnWithPlanningContinuation(
		true,
		agent.Turn{SessionID: "s5"},
		func(turn agent.Turn) (agent.TurnResult, error) {
			calls++
			return agent.TurnResult{}, errors.New("upstream failure")
		},
	)
	if err == nil {
		t.Fatal("expected error from run")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}