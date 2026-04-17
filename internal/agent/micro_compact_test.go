package agent

import (
	"strings"
	"testing"
)

func TestMicroCompactMessages_ClearsOldestCompactableResults(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "search the web"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_search"}}},
		{Role: "tool", Content: strings.Repeat("a", 500), ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "web_search"}}},
		{Role: "tool", Content: strings.Repeat("b", 500), ToolCallID: "tc2"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc3", Name: "web_search"}}},
		{Role: "tool", Content: strings.Repeat("c", 500), ToolCallID: "tc3"},
	}

	result := MicroCompactMessages(messages, MicroCompactOptions{KeepRecent: 1})

	if result.Cleared != 2 {
		t.Fatalf("expected 2 cleared, got %d", result.Cleared)
	}
	// First two tool results should be cleared
	if result.Messages[2].Content != microCompactClearedMarker {
		t.Errorf("oldest tool result not cleared: %q", result.Messages[2].Content)
	}
	if result.Messages[4].Content != microCompactClearedMarker {
		t.Errorf("second-oldest tool result not cleared: %q", result.Messages[4].Content)
	}
	// Most recent should be preserved
	if result.Messages[6].Content != strings.Repeat("c", 500) {
		t.Errorf("most recent tool result should be preserved")
	}
}

func TestMicroCompactMessages_PreservesNonCompactableTools(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "custom_tool"}}},
		{Role: "tool", Content: "important result", ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "web_fetch"}}},
		{Role: "tool", Content: strings.Repeat("x", 500), ToolCallID: "tc2"},
	}

	result := MicroCompactMessages(messages, MicroCompactOptions{KeepRecent: 0})

	// custom_tool result should be untouched
	if result.Messages[1].Content != "important result" {
		t.Errorf("non-compactable tool result was modified: %q", result.Messages[1].Content)
	}
}

func TestMicroCompactMessages_KeepRecentDefaultsToTwo(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "memory_search"}}},
		{Role: "tool", Content: strings.Repeat("a", 200), ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "memory_search"}}},
		{Role: "tool", Content: strings.Repeat("b", 200), ToolCallID: "tc2"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc3", Name: "memory_search"}}},
		{Role: "tool", Content: strings.Repeat("c", 200), ToolCallID: "tc3"},
	}

	// KeepRecent=0 should default to 2
	result := MicroCompactMessages(messages, MicroCompactOptions{})

	if result.Cleared != 1 {
		t.Fatalf("expected 1 cleared (default KeepRecent=2), got %d", result.Cleared)
	}
}

func TestMicroCompactMessages_TargetCharsStopsEarly(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_fetch"}}},
		{Role: "tool", Content: strings.Repeat("a", 500), ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "web_fetch"}}},
		{Role: "tool", Content: strings.Repeat("b", 500), ToolCallID: "tc2"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc3", Name: "web_fetch"}}},
		{Role: "tool", Content: strings.Repeat("c", 500), ToolCallID: "tc3"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc4", Name: "web_fetch"}}},
		{Role: "tool", Content: strings.Repeat("d", 500), ToolCallID: "tc4"},
	}

	// Set target to something achievable by clearing just 1 result
	totalChars := estimateMessageChars(messages)
	target := totalChars - 400 // clearing one 500-char result saves ~460 chars

	result := MicroCompactMessages(messages, MicroCompactOptions{
		KeepRecent:  1,
		TargetChars: target,
	})

	if result.Cleared < 1 {
		t.Fatalf("expected at least 1 cleared, got %d", result.Cleared)
	}
	if result.CharsAfter > result.CharsBefore {
		t.Fatalf("CharsAfter (%d) > CharsBefore (%d)", result.CharsAfter, result.CharsBefore)
	}
}

func TestMicroCompactMessages_DoesNotMutateInput(t *testing.T) {
	original := "original content"
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_search"}}},
		{Role: "tool", Content: original, ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "web_search"}}},
		{Role: "tool", Content: "newer", ToolCallID: "tc2"},
	}

	MicroCompactMessages(messages, MicroCompactOptions{KeepRecent: 1})

	if messages[1].Content != original {
		t.Fatalf("input slice was mutated: %q", messages[1].Content)
	}
}

func TestMicroCompactMessages_EmptyInput(t *testing.T) {
	result := MicroCompactMessages(nil, MicroCompactOptions{})
	if result.Cleared != 0 {
		t.Errorf("expected 0 cleared for nil input")
	}
	if result.Messages != nil {
		t.Errorf("expected nil messages for nil input")
	}
}

func TestMicroCompactMessages_AlreadyClearedSkipped(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_search"}}},
		{Role: "tool", Content: microCompactClearedMarker, ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "web_search"}}},
		{Role: "tool", Content: "new result", ToolCallID: "tc2"},
	}

	result := MicroCompactMessages(messages, MicroCompactOptions{KeepRecent: 0})

	// Already-cleared message should not count as a candidate
	if result.Cleared != 0 {
		t.Errorf("expected 0 cleared (only 1 candidate, KeepRecent defaults to 2), got %d", result.Cleared)
	}
}

func TestMicroCompactMessages_AdditionalCompactableTools(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "my_custom_tool"}}},
		{Role: "tool", Content: strings.Repeat("x", 500), ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "my_custom_tool"}}},
		{Role: "tool", Content: "newer", ToolCallID: "tc2"},
	}

	result := MicroCompactMessages(messages, MicroCompactOptions{
		KeepRecent:                 1,
		AdditionalCompactableTools: map[string]bool{"my_custom_tool": true},
	})

	if result.Cleared != 1 {
		t.Fatalf("expected 1 cleared with additional compactable tool, got %d", result.Cleared)
	}
}

func TestKeepRecentForTier(t *testing.T) {
	if got := KeepRecentForTier(TierMicro); got != 1 {
		t.Errorf("KeepRecentForTier(Micro) = %d, want 1", got)
	}
	if got := KeepRecentForTier(TierSmall); got != 2 {
		t.Errorf("KeepRecentForTier(Small) = %d, want 2", got)
	}
	if got := KeepRecentForTier(TierStandard); got != 4 {
		t.Errorf("KeepRecentForTier(Standard) = %d, want 4", got)
	}
}

func TestBuildToolNameIndex(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "web_search"},
			{ID: "tc2", Name: "memory_search"},
		}},
		{Role: "tool", Content: "result1", ToolCallID: "tc1"},
		{Role: "tool", Content: "result2", ToolCallID: "tc2"},
	}

	index := buildToolNameIndex(messages)
	if index["tc1"] != "web_search" {
		t.Errorf("expected tc1 → web_search, got %q", index["tc1"])
	}
	if index["tc2"] != "memory_search" {
		t.Errorf("expected tc2 → memory_search, got %q", index["tc2"])
	}
	if _, exists := index["nonexistent"]; exists {
		t.Error("should not have nonexistent key")
	}
}
