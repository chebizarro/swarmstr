package agent

import (
	"strings"
	"testing"
)

func TestBuildPlanningOnlyContinuation(t *testing.T) {
	baseHistory := []ConversationMessage{
		{Role: "user", Content: "Please update the config file."},
	}
	base := Turn{
		History: baseHistory,
		Context: "some dynamic context",
		TurnID:  "turn-1",
		UserText: "Please update the config file.",
	}

	planningText := "I'll update the config file right away."
	cont := BuildPlanningOnlyContinuation(base, planningText)

	// Must not mutate original
	if len(base.History) != 1 {
		t.Fatalf("original History mutated: len=%d, want 1", len(base.History))
	}
	if base.History[0].Content != "Please update the config file." {
		t.Fatal("original History[0] content changed")
	}
	if base.Context != "some dynamic context" {
		t.Fatal("original Context changed")
	}

	// History must have original messages plus the prior assistant message
	if len(cont.History) != 2 {
		t.Fatalf("continuation History len=%d, want 2", len(cont.History))
	}
	if cont.History[0].Role != "user" || cont.History[0].Content != "Please update the config file." {
		t.Errorf("continuation History[0] mismatch: got role=%q content=%q", cont.History[0].Role, cont.History[0].Content)
	}
	if cont.History[1].Role != "assistant" || cont.History[1].Content != planningText {
		t.Errorf("continuation History[1] mismatch: got role=%q content=%q, want role=assistant content=%q",
			cont.History[1].Role, cont.History[1].Content, planningText)
	}

	// Context must include both original context and PlanningOnlyRetryInstruction
	if !strings.Contains(cont.Context, "some dynamic context") {
		t.Error("continuation Context missing original context")
	}
	if !strings.Contains(cont.Context, PlanningOnlyRetryInstruction) {
		t.Error("continuation Context missing PlanningOnlyRetryInstruction")
	}
	if !strings.Contains(cont.Context, "\n\n") {
		t.Error("continuation Context should join with newlines")
	}

	// Other fields preserved
	if cont.TurnID != "turn-1" {
		t.Errorf("TurnID not preserved: got %q", cont.TurnID)
	}
	if cont.UserText != "Please update the config file." {
		t.Errorf("UserText not preserved: got %q", cont.UserText)
	}
}

func TestBuildPlanningOnlyContinuationEmptyContext(t *testing.T) {
	base := Turn{
		History: []ConversationMessage{
			{Role: "user", Content: "Fix the bug."},
		},
		Context: "",
	}

	cont := BuildPlanningOnlyContinuation(base, "I'll fix the bug.")

	if cont.Context != PlanningOnlyRetryInstruction {
		t.Errorf("empty context: got %q, want %q", cont.Context, PlanningOnlyRetryInstruction)
	}
	if len(cont.History) != 2 {
		t.Errorf("History len=%d, want 2", len(cont.History))
	}
}

func TestBuildPlanningOnlyContinuationEmptyHistory(t *testing.T) {
	base := Turn{
		History: nil,
		Context: "dynamic",
	}

	cont := BuildPlanningOnlyContinuation(base, "I'll do it.")

	if len(cont.History) != 1 {
		t.Fatalf("History len=%d, want 1", len(cont.History))
	}
	if cont.History[0].Role != "assistant" || cont.History[0].Content != "I'll do it." {
		t.Errorf("History[0] mismatch: got role=%q content=%q", cont.History[0].Role, cont.History[0].Content)
	}
	if !strings.Contains(cont.Context, "dynamic") {
		t.Error("Context missing original")
	}
	if !strings.Contains(cont.Context, PlanningOnlyRetryInstruction) {
		t.Error("Context missing retry instruction")
	}
}