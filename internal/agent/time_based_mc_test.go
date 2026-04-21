package agent

import (
	"strings"
	"testing"
	"time"
)

func TestShouldTimeBasedMicrocompact_Disabled(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: false, GapThresholdMinutes: 60}
	if ShouldTimeBasedMicrocompact(cfg, time.Now().Add(-2*time.Hour)) {
		t.Error("expected false when disabled")
	}
}

func TestShouldTimeBasedMicrocompact_ZeroTime(t *testing.T) {
	cfg := DefaultTimeBasedMCConfig
	if ShouldTimeBasedMicrocompact(cfg, time.Time{}) {
		t.Error("expected false for zero time")
	}
}

func TestShouldTimeBasedMicrocompact_RecentMessage(t *testing.T) {
	cfg := DefaultTimeBasedMCConfig
	// Last assistant message was 5 minutes ago — well within the 60m threshold.
	if ShouldTimeBasedMicrocompact(cfg, time.Now().Add(-5*time.Minute)) {
		t.Error("expected false for recent message (5m < 60m threshold)")
	}
}

func TestShouldTimeBasedMicrocompact_ExpiredGap(t *testing.T) {
	cfg := DefaultTimeBasedMCConfig
	// Last assistant message was 90 minutes ago — past the 60m threshold.
	if !ShouldTimeBasedMicrocompact(cfg, time.Now().Add(-90*time.Minute)) {
		t.Error("expected true for expired gap (90m > 60m threshold)")
	}
}

func TestShouldTimeBasedMicrocompact_CustomThreshold(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 30}
	// 45m gap exceeds 30m custom threshold.
	if !ShouldTimeBasedMicrocompact(cfg, time.Now().Add(-45*time.Minute)) {
		t.Error("expected true for 45m gap with 30m threshold")
	}
	// 15m gap within 30m custom threshold.
	if ShouldTimeBasedMicrocompact(cfg, time.Now().Add(-15*time.Minute)) {
		t.Error("expected false for 15m gap with 30m threshold")
	}
}

func TestTimeBasedMicrocompact_ClearsWhenGapExceeded(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "search for something"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_search"}}},
		{Role: "tool", Content: strings.Repeat("x", 5000), ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "web_fetch"}}},
		{Role: "tool", Content: strings.Repeat("x", 5000), ToolCallID: "tc2"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc3", Name: "memory_search"}}},
		{Role: "tool", Content: "memory result", ToolCallID: "tc3"},
		{Role: "assistant", Content: "here's what I found"},
		{Role: "user", Content: "continue"},
	}

	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 2}
	// 90 minute gap — should trigger.
	result := TimeBasedMicrocompact(messages, cfg, time.Now().Add(-90*time.Minute))

	if result.Cleared == 0 {
		t.Fatal("expected at least one tool result to be cleared")
	}
	if result.CharsBefore <= result.CharsAfter {
		t.Errorf("expected chars reduction: before=%d after=%d", result.CharsBefore, result.CharsAfter)
	}

	// Verify the most recent KeepRecent tool results are preserved.
	// With KeepRecent=2, tc2 and tc3 results should be intact.
	cleared := 0
	for _, msg := range result.Messages {
		if msg.Content == microCompactClearedMarker {
			cleared++
		}
	}
	if cleared != 1 {
		t.Errorf("expected 1 cleared result (3 total - 2 kept), got %d", cleared)
	}
}

func TestTimeBasedMicrocompact_NoOpWhenGapBelowThreshold(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_search"}}},
		{Role: "tool", Content: strings.Repeat("x", 5000), ToolCallID: "tc1"},
		{Role: "user", Content: "thanks"},
	}

	cfg := DefaultTimeBasedMCConfig
	// 5 minute gap — should NOT trigger.
	result := TimeBasedMicrocompact(messages, cfg, time.Now().Add(-5*time.Minute))

	if result.Cleared != 0 {
		t.Errorf("expected no clearing for recent gap, got %d cleared", result.Cleared)
	}
}

func TestTimeBasedMicrocompact_DoesNotMutateInput(t *testing.T) {
	original := strings.Repeat("x", 5000)
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_search"}}},
		{Role: "tool", Content: original, ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "web_fetch"}}},
		{Role: "tool", Content: original, ToolCallID: "tc2"},
	}

	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 1}
	result := TimeBasedMicrocompact(messages, cfg, time.Now().Add(-90*time.Minute))

	// Original should not be modified.
	if messages[1].Content != original {
		t.Error("input messages were mutated")
	}
	// Result should be a different slice.
	if result.Cleared > 0 && &result.Messages[0] == &messages[0] {
		t.Error("result should be a copy, not the same slice")
	}
}
