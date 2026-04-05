package agent

import (
	"testing"
)

func TestSanitize_EmptyInput(t *testing.T) {
	out, stats := SanitizeConversationHistory(nil)
	if len(out) != 0 {
		t.Errorf("expected empty output, got %d", len(out))
	}
	if stats != (HistorySanitizeStats{}) {
		t.Errorf("expected zero stats, got %+v", stats)
	}
}

func TestSanitize_CleanHistory(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	out, stats := SanitizeConversationHistory(in)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if stats != (HistorySanitizeStats{}) {
		t.Errorf("expected zero stats for clean history, got %+v", stats)
	}
}

func TestSanitize_OrphanToolResults(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "do something"},
		// Orphan tool result — no preceding assistant with matching tool call
		{Role: "tool", ToolCallID: "unknown_id", Content: "stale result"},
		{Role: "assistant", Content: "ok"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.OrphanToolResultsDropped != 1 {
		t.Errorf("expected 1 orphan dropped, got %d", stats.OrphanToolResultsDropped)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0].Role != "user" || out[1].Role != "assistant" {
		t.Errorf("unexpected output: %+v", out)
	}
}

func TestSanitize_OrphanToolResult_EmptyID(t *testing.T) {
	in := []ConversationMessage{
		{Role: "tool", Content: "no id"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.OrphanToolResultsDropped != 1 {
		t.Errorf("expected 1 orphan dropped, got %d", stats.OrphanToolResultsDropped)
	}
	if len(out) != 0 {
		t.Fatalf("expected 0, got %d", len(out))
	}
}

func TestSanitize_ValidToolChain(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "fetch data"},
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "c1", Name: "nostr_fetch"}}},
		{Role: "tool", ToolCallID: "c1", Content: "event data"},
		{Role: "assistant", Content: "here is the data"},
	}
	out, stats := SanitizeConversationHistory(in)
	if len(out) != 4 {
		t.Fatalf("expected 4, got %d", len(out))
	}
	if stats.OrphanToolResultsDropped != 0 {
		t.Error("should not drop valid tool results")
	}
}

func TestSanitize_EmptyMessagesDropped(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: ""}, // empty, should be dropped
		{Role: "user", Content: ""},      // empty, should be dropped
		{Role: "assistant", Content: "hi"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.EmptyMessagesDropped != 2 {
		t.Errorf("expected 2 empty dropped, got %d", stats.EmptyMessagesDropped)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d: %+v", len(out), out)
	}
}

func TestSanitize_EmptyAssistantWithToolCalls_Preserved(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "run tool"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "c1", Name: "bash_exec"}}},
		{Role: "tool", ToolCallID: "c1", Content: "done"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.EmptyMessagesDropped != 0 {
		t.Error("should not drop assistant with tool calls even if content is empty")
	}
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d", len(out))
	}
}

func TestSanitize_ConsecutiveUserMerge(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "reply"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.ConsecutiveMerged != 1 {
		t.Errorf("expected 1 merge, got %d", stats.ConsecutiveMerged)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0].Content != "first\n\nsecond" {
		t.Errorf("merged content = %q", out[0].Content)
	}
}

func TestSanitize_ConsecutiveAssistantMerge(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "part 1"},
		{Role: "assistant", Content: "part 2"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.ConsecutiveMerged != 1 {
		t.Errorf("expected 1 merge, got %d", stats.ConsecutiveMerged)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[1].Content != "part 1\n\npart 2" {
		t.Errorf("merged content = %q", out[1].Content)
	}
}

func TestSanitize_NoMergeToolCallAssistants(t *testing.T) {
	in := []ConversationMessage{
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "c1", Name: "tool"}}},
		{Role: "tool", ToolCallID: "c1", Content: "done"},
		{Role: "assistant", Content: "after tool"},
		{Role: "assistant", Content: "more text"},
	}
	out, stats := SanitizeConversationHistory(in)
	// The two plain assistant messages at the end should merge,
	// but assistant-with-toolcalls should NOT merge with anything.
	if stats.ConsecutiveMerged != 1 {
		t.Errorf("expected 1 merge, got %d", stats.ConsecutiveMerged)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 (toolcall-assistant + tool + merged-assistant), got %d: %+v", len(out), out)
	}
	if len(out[0].ToolCalls) != 1 {
		t.Error("first message should keep tool calls")
	}
}

func TestSanitize_SyntheticToolResults_TrailingUnmatched(t *testing.T) {
	in := []ConversationMessage{
		{Role: "user", Content: "run tools"},
		{Role: "assistant", ToolCalls: []ToolCallRef{
			{ID: "c1", Name: "bash_exec"},
			{ID: "c2", Name: "read_file"},
		}},
		// Only c1 has a result; c2 is orphaned
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.SyntheticToolResults != 1 {
		t.Errorf("expected 1 synthetic result, got %d", stats.SyntheticToolResults)
	}
	// Should be: user + assistant(toolcalls) + tool(c1) + synthetic tool(c2)
	if len(out) != 4 {
		t.Fatalf("expected 4, got %d: %+v", len(out), out)
	}
	last := out[3]
	if last.Role != "tool" || last.ToolCallID != "c2" {
		t.Errorf("expected synthetic tool for c2, got %+v", last)
	}
	if last.Content != "error: previous turn ended before tool completed" {
		t.Errorf("unexpected synthetic content: %q", last.Content)
	}
}

func TestSanitize_NoSyntheticWhenAllAnswered(t *testing.T) {
	in := []ConversationMessage{
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "c1", Name: "tool"}}},
		{Role: "tool", ToolCallID: "c1", Content: "done"},
		{Role: "assistant", Content: "complete"},
	}
	_, stats := SanitizeConversationHistory(in)
	if stats.SyntheticToolResults != 0 {
		t.Errorf("expected 0 synthetic, got %d", stats.SyntheticToolResults)
	}
}

func TestSanitize_ToolResultBeforeToolCall_Dropped(t *testing.T) {
	// A tool result that appears BEFORE its matching assistant tool-call
	// must be dropped — providers require tool results after their tool calls.
	in := []ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "tool", ToolCallID: "c1", Content: "premature result"},
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "c1", Name: "tool_a"}}},
		{Role: "tool", ToolCallID: "c1", Content: "correct result"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.OrphanToolResultsDropped != 1 {
		t.Errorf("expected 1 orphan dropped, got %d", stats.OrphanToolResultsDropped)
	}
	// Should be: user + assistant(toolcall) + tool(c1 correct)
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d: %+v", len(out), out)
	}
	if out[1].Role != "assistant" || len(out[1].ToolCalls) != 1 {
		t.Error("out[1] should be assistant with tool calls")
	}
	if out[2].Role != "tool" || out[2].Content != "correct result" {
		t.Errorf("out[2] should be the correct tool result, got %+v", out[2])
	}
}

func TestSanitize_ToolResultsNotMerged(t *testing.T) {
	in := []ConversationMessage{
		{Role: "assistant", ToolCalls: []ToolCallRef{
			{ID: "c1", Name: "tool_a"},
			{ID: "c2", Name: "tool_b"},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "r1"},
		{Role: "tool", ToolCallID: "c2", Content: "r2"},
	}
	out, stats := SanitizeConversationHistory(in)
	if stats.ConsecutiveMerged != 0 {
		t.Error("tool messages should not be merged")
	}
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d", len(out))
	}
}

func TestSanitize_PreservesDottedAndSlashedToolNames(t *testing.T) {
	in := []ConversationMessage{{
		Role: "assistant",
		ToolCalls: []ToolCallRef{
			{ID: "c1", Name: "memory.search", ArgsJSON: `{}`},
			{ID: "c2", Name: "plugin/raw", ArgsJSON: `{}`},
		},
	}}
	out, stats := SanitizeConversationHistory(in)
	if stats.InvalidToolCallsDropped != 0 {
		t.Fatalf("expected dotted/slashed tool names to survive, got stats %+v", stats)
	}
	if len(out) != 3 || len(out[0].ToolCalls) != 2 {
		t.Fatalf("unexpected sanitized output: %+v", out)
	}
	if out[0].ToolCalls[0].Name != "memory.search" || out[0].ToolCalls[1].Name != "plugin/raw" {
		t.Fatalf("expected dotted/slashed tool names to survive, got %+v", out[0].ToolCalls)
	}
}

func TestSanitize_InvalidToolCallsDropped(t *testing.T) {
	in := []ConversationMessage{{
		Role: "assistant",
		ToolCalls: []ToolCallRef{
			{ID: "c1", Name: "bad name"},
			{ID: "c2", Name: "good_name", ArgsJSON: "{"},
			{ID: "c3", Name: "good_name", ArgsJSON: "{}"},
		},
	}}
	out, stats := SanitizeConversationHistory(in)
	if stats.InvalidToolCallsDropped != 2 {
		t.Fatalf("expected 2 invalid tool calls dropped, got %d", stats.InvalidToolCallsDropped)
	}
	if len(out) != 2 || len(out[0].ToolCalls) != 1 || out[0].ToolCalls[0].ID != "c3" {
		t.Fatalf("unexpected sanitized tool calls: %+v", out)
	}
	if out[1].Role != "tool" || out[1].ToolCallID != "c3" {
		t.Fatalf("expected synthetic tool repair for c3, got %+v", out)
	}
}

func TestSanitizeWithOptions_PrependsSyntheticUserForAssistantFirstHistory(t *testing.T) {
	in := []ConversationMessage{{Role: "assistant", Content: "hello first"}}
	out, stats := SanitizeConversationHistoryWithOptions(in, HistorySanitizeOptions{EnsureLeadingUser: true})
	if stats.SyntheticBootstrapAdded != 1 {
		t.Fatalf("expected synthetic bootstrap, got %+v", stats)
	}
	if len(out) != 2 || out[0].Role != "user" || out[0].Content != syntheticHistoryBootstrapText {
		t.Fatalf("unexpected output: %+v", out)
	}
}
