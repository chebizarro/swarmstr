package agent

import (
	"strings"
	"testing"
)

func TestGuardToolResultMessages_TruncatesOversizedResult(t *testing.T) {
	messages := []LLMMessage{{Role: "tool", Content: strings.Repeat("x", 6000)}}
	guarded := GuardToolResultMessages(messages, 1000)
	if len(guarded[0].Content) >= len(messages[0].Content) {
		t.Fatalf("expected truncation, got len=%d", len(guarded[0].Content))
	}
	if !strings.Contains(guarded[0].Content, "Content truncated") {
		t.Fatalf("expected truncation notice, got %q", guarded[0].Content)
	}
}

func TestGuardToolResultMessages_CompactsOldestResultsWhenOverBudget(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: strings.Repeat("s", 2000)},
		{Role: "tool", Content: strings.Repeat("a", 1500)},
		{Role: "tool", Content: strings.Repeat("b", 1500)},
	}
	guarded := GuardToolResultMessages(messages, 500)
	if guarded[1].Content != preemptiveToolResultCompactionPlaceholder {
		t.Fatalf("expected oldest tool result to compact, got %q", guarded[1].Content)
	}
}
