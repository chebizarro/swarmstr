package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompressHistoricalToolResults_NoMessages(t *testing.T) {
	result := CompressHistoricalToolResults(nil, 2)
	if result.Compressed != 0 {
		t.Errorf("expected 0 compressed, got %d", result.Compressed)
	}
}

func TestCompressHistoricalToolResults_AllProtected(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "tc1", Content: strings.Repeat("x", 1000)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "bash"}}},
		{Role: "tool", ToolCallID: "tc2", Content: strings.Repeat("y", 1000)},
	}
	// preserveRecent=4 protects both tool results
	result := CompressHistoricalToolResults(messages, 4)
	if result.Compressed != 0 {
		t.Errorf("expected 0 compressed when all protected, got %d", result.Compressed)
	}
}

func TestCompressHistoricalToolResults_CompressesOlder(t *testing.T) {
	largeContent := strings.Repeat("line of output\n", 100) // ~1500 chars
	messages := []LLMMessage{
		{Role: "user", Content: "do something"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "tc1", Content: largeContent},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "bash"}}},
		{Role: "tool", ToolCallID: "tc2", Content: largeContent},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc3", Name: "file_search"}}},
		{Role: "tool", ToolCallID: "tc3", Content: largeContent},
	}

	result := CompressHistoricalToolResults(messages, 1)
	if result.Compressed != 2 {
		t.Errorf("expected 2 compressed, got %d", result.Compressed)
	}
	if result.CharsAfter >= result.CharsBefore {
		t.Errorf("expected chars to decrease: before=%d after=%d", result.CharsBefore, result.CharsAfter)
	}

	// The last tool result should be unchanged (protected).
	lastToolIdx := 6
	if result.Messages[lastToolIdx].Content != largeContent {
		t.Error("expected last tool result to be preserved")
	}

	// The first tool result should be compressed.
	firstToolIdx := 2
	if !strings.HasPrefix(result.Messages[firstToolIdx].Content, compressedResultPrefix) {
		t.Errorf("expected compressed prefix, got: %s", result.Messages[firstToolIdx].Content[:80])
	}
}

func TestCompressHistoricalToolResults_SkipsSmallResults(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "tc1", Content: "small result"}, // < 500 chars
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "bash"}}},
		{Role: "tool", ToolCallID: "tc2", Content: strings.Repeat("x", 1000)},
	}

	result := CompressHistoricalToolResults(messages, 1)
	// Only tc2 is a candidate (tc1 is too small); tc2 is preserved (only 1 candidate, 1 preserved)
	if result.Compressed != 0 {
		t.Errorf("expected 0 compressed (small result should be skipped), got %d", result.Compressed)
	}
}

func TestCompressHistoricalToolResults_SkipsAlreadyCompressed(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "bash"}}},
		{Role: "tool", ToolCallID: "tc1", Content: compressedResultPrefix + "bash returned 10 lines]"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "bash"}}},
		{Role: "tool", ToolCallID: "tc2", Content: strings.Repeat("x", 1000)},
	}

	result := CompressHistoricalToolResults(messages, 1)
	// tc1 is already compressed (skipped), tc2 is the only candidate and protected
	if result.Compressed != 0 {
		t.Errorf("expected 0 compressed, got %d", result.Compressed)
	}
}

func TestCompressHistoricalToolResults_SkipsMicroCompactMarker(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "web_fetch"}}},
		{Role: "tool", ToolCallID: "tc1", Content: microCompactClearedMarker},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "bash"}}},
		{Role: "tool", ToolCallID: "tc2", Content: strings.Repeat("x", 1000)},
	}

	result := CompressHistoricalToolResults(messages, 1)
	if result.Compressed != 0 {
		t.Errorf("expected 0 compressed, got %d", result.Compressed)
	}
}

func TestCompressHistoricalToolResults_DoesNotMutateOriginal(t *testing.T) {
	original := strings.Repeat("original content\n", 100)
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "tc1", Content: original},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc2", Name: "bash"}}},
		{Role: "tool", ToolCallID: "tc2", Content: strings.Repeat("x", 1000)},
	}

	result := CompressHistoricalToolResults(messages, 1)
	if result.Compressed != 1 {
		t.Fatalf("expected 1 compressed, got %d", result.Compressed)
	}
	// Original messages should be unchanged.
	if messages[1].Content != original {
		t.Error("original message slice was mutated")
	}
}

func TestSummarizeToolResult_ReadFile(t *testing.T) {
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	summary := summarizeToolResult("read_file", content)
	if !strings.HasPrefix(summary, compressedResultPrefix) {
		t.Errorf("expected compressed prefix, got: %s", summary)
	}
	if !strings.Contains(summary, "read_file") {
		t.Error("expected tool name in summary")
	}
	if !strings.Contains(summary, "lines") {
		t.Error("expected line count in summary")
	}
	if !strings.Contains(summary, "chars") {
		t.Error("expected char count in summary")
	}
}

func TestSummarizeToolResult_Search(t *testing.T) {
	content := "Found 5 matches in 3 files\nfile1.go:10: match\nfile2.go:20: match\n"
	summary := summarizeToolResult("file_search", content)
	if !strings.Contains(summary, "file_search") {
		t.Error("expected tool name in summary")
	}
	if !strings.Contains(summary, "5 matches") {
		t.Errorf("expected match count in summary, got: %s", summary)
	}
}

func TestSummarizeToolResult_Bash(t *testing.T) {
	content := "$ go build ./...\nok\n"
	summary := summarizeToolResult("bash", content)
	if !strings.Contains(summary, "bash") {
		t.Error("expected tool name in summary")
	}
	if !strings.Contains(summary, "output:") {
		t.Errorf("expected 'output:' for command tool, got: %s", summary)
	}
}

func TestSummarizeToolResult_GenericTool(t *testing.T) {
	content := strings.Repeat("data line\n", 50)
	summary := summarizeToolResult("my_custom_tool", content)
	if !strings.Contains(summary, "my_custom_tool") {
		t.Error("expected tool name in summary")
	}
	if !strings.Contains(summary, "returned") {
		t.Error("expected 'returned' in generic summary")
	}
}

func TestExtractPreview(t *testing.T) {
	// Basic case
	preview := extractPreview("first line\nsecond line\n")
	if preview != "first line" {
		t.Errorf("expected 'first line', got %q", preview)
	}

	// Skips empty first lines
	preview = extractPreview("\n\n  \nactual content\n")
	if preview != "actual content" {
		t.Errorf("expected 'actual content', got %q", preview)
	}

	// Long first line gets truncated
	long := strings.Repeat("a", 200)
	preview = extractPreview(long)
	if len(preview) > compressPreviewMaxChars+10 {
		t.Errorf("preview too long: %d chars", len(preview))
	}
	if !strings.HasSuffix(preview, "...") {
		t.Error("expected ellipsis suffix for truncated preview")
	}
}

func TestExtractTrailingNumber(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"Found 5", 5},
		{"Total: 42", 42},
		{"abc", 0},
		{"  123  ", 123},
		{"matches: 7", 7},
	}
	for _, tt := range tests {
		got := extractTrailingNumber(tt.input)
		if got != tt.want {
			t.Errorf("extractTrailingNumber(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestCompressHistoricalToolResults_DefaultPreserveRecent(t *testing.T) {
	// When preserveRecent=0, should default to 4
	var messages []LLMMessage
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("tc%d", i)
		messages = append(messages,
			LLMMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Name: "read_file"}}},
			LLMMessage{Role: "tool", ToolCallID: id, Content: strings.Repeat("x", 1000)},
		)
	}

	result := CompressHistoricalToolResults(messages, 0) // defaults to 4
	if result.Compressed != 2 {
		t.Errorf("expected 2 compressed (6 total - 4 preserved), got %d", result.Compressed)
	}
}

func TestIsReadTool(t *testing.T) {
	for _, name := range []string{"read_file", "Read", "cat", "ReadFile"} {
		if !isReadTool(name) {
			t.Errorf("expected isReadTool(%q) = true", name)
		}
	}
	if isReadTool("bash") {
		t.Error("bash should not be a read tool")
	}
}

func TestIsSearchTool(t *testing.T) {
	for _, name := range []string{"file_search", "search", "grep", "Grep", "memory_search"} {
		if !isSearchTool(name) {
			t.Errorf("expected isSearchTool(%q) = true", name)
		}
	}
	if isSearchTool("read_file") {
		t.Error("read_file should not be a search tool")
	}
}

func TestIsCommandTool(t *testing.T) {
	for _, name := range []string{"bash", "Bash", "execute", "shell"} {
		if !isCommandTool(name) {
			t.Errorf("expected isCommandTool(%q) = true", name)
		}
	}
	if isCommandTool("read_file") {
		t.Error("read_file should not be a command tool")
	}
}
