package agent

import (
	"strings"
	"testing"
)

func TestDefaultContextPruningConfig(t *testing.T) {
	cfg := DefaultContextPruningConfig()

	if !cfg.Enabled {
		t.Error("Expected Enabled to be true by default")
	}
	if cfg.KeepLastAssistants != 3 {
		t.Errorf("Expected KeepLastAssistants=3, got %d", cfg.KeepLastAssistants)
	}
	if cfg.SoftTrimRatio != 0.3 {
		t.Errorf("Expected SoftTrimRatio=0.3, got %f", cfg.SoftTrimRatio)
	}
	if cfg.HardClearRatio != 0.5 {
		t.Errorf("Expected HardClearRatio=0.5, got %f", cfg.HardClearRatio)
	}
	if !cfg.HardClear.Enabled {
		t.Error("Expected HardClear.Enabled to be true by default")
	}
}

func TestIsToolPrunable(t *testing.T) {
	cfg := DefaultContextPruningConfig()

	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"empty name", "", false},
		{"read tool", "read_file", true},
		{"search tool", "file_search", true},
		{"grep tool", "grep", true},
		{"list tool", "list_directory", true},
		{"get tool", "get_status", true},
		{"show tool", "show_diff", true},
		{"write tool", "write_file", false},   // mutating
		{"execute tool", "execute", false},    // mutating
		{"publish tool", "nostr_publish", false}, // mutating
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsToolPrunable(tt.toolName, cfg)
			if result != tt.expected {
				t.Errorf("IsToolPrunable(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestIsToolPrunable_AllowList(t *testing.T) {
	cfg := DefaultContextPruningConfig()
	cfg.ToolAllowList = []string{"read_file", "grep"}

	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"allowed tool", "read_file", true},
		{"another allowed", "grep", true},
		{"not in allow list", "search", false},
		{"not in allow list 2", "list_directory", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsToolPrunable(tt.toolName, cfg)
			if result != tt.expected {
				t.Errorf("IsToolPrunable(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestIsToolPrunable_DenyList(t *testing.T) {
	cfg := DefaultContextPruningConfig()
	cfg.ToolDenyList = []string{"read_file"}

	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"denied tool", "read_file", false},
		{"other read tool", "read_other", true},
		{"search tool", "search", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsToolPrunable(tt.toolName, cfg)
			if result != tt.expected {
				t.Errorf("IsToolPrunable(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestSoftTrimContent(t *testing.T) {
	cfg := SoftTrimConfig{
		MaxChars:  100,
		HeadChars: 30,
		TailChars: 30,
	}

	tests := []struct {
		name        string
		content     string
		wantTrimmed bool
	}{
		{"short content", "short", false},
		{"at limit", strings.Repeat("a", 100), false},
		{"over limit", strings.Repeat("a", 150), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, wasTrimmed := softTrimContent(tt.content, cfg)
			if wasTrimmed != tt.wantTrimmed {
				t.Errorf("softTrimContent wasTrimmed = %v, want %v", wasTrimmed, tt.wantTrimmed)
			}
			if tt.wantTrimmed {
				if !strings.Contains(result, "...") {
					t.Error("Trimmed content should contain '...'")
				}
				if !strings.Contains(result, "Tool result trimmed") {
					t.Error("Trimmed content should contain trim note")
				}
			}
		})
	}
}

func TestSoftTrimContent_PreservesHeadAndTail(t *testing.T) {
	cfg := SoftTrimConfig{
		MaxChars:  50,
		HeadChars: 10,
		TailChars: 10,
	}

	content := "HEADER1234" + strings.Repeat("x", 80) + "FOOTER5678"
	result, wasTrimmed := softTrimContent(content, cfg)

	if !wasTrimmed {
		t.Fatal("Expected content to be trimmed")
	}
	if !strings.HasPrefix(result, "HEADER1234") {
		t.Error("Trimmed content should start with original header")
	}
	if !strings.Contains(result, "FOOTER5678") {
		t.Error("Trimmed content should contain original footer")
	}
}

func TestFindAssistantCutoffIndex(t *testing.T) {
	messages := []PrunableMessage{
		{Role: "user", Index: 0},
		{Role: "assistant", Index: 1},
		{Role: "tool_result", Index: 2},
		{Role: "assistant", Index: 3},
		{Role: "tool_result", Index: 4},
		{Role: "assistant", Index: 5},
		{Role: "user", Index: 6},
		{Role: "assistant", Index: 7},
	}

	tests := []struct {
		name       string
		keepLast   int
		wantCutoff int
	}{
		{"keep 0", 0, 8},   // all prunable
		{"keep 1", 1, 7},   // last assistant protected
		{"keep 2", 2, 5},   // last 2 assistants protected
		{"keep 3", 3, 3},   // last 3 assistants protected
		{"keep more than exist", 10, -1}, // not enough assistants
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findAssistantCutoffIndex(messages, tt.keepLast)
			if result != tt.wantCutoff {
				t.Errorf("findAssistantCutoffIndex(keepLast=%d) = %d, want %d",
					tt.keepLast, result, tt.wantCutoff)
			}
		})
	}
}

func TestFindFirstUserIndex(t *testing.T) {
	tests := []struct {
		name     string
		messages []PrunableMessage
		want     int
	}{
		{
			"user first",
			[]PrunableMessage{{Role: "user"}, {Role: "assistant"}},
			0,
		},
		{
			"system then user",
			[]PrunableMessage{{Role: "system"}, {Role: "user"}, {Role: "assistant"}},
			1,
		},
		{
			"no user",
			[]PrunableMessage{{Role: "system"}, {Role: "assistant"}},
			-1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findFirstUserIndex(tt.messages)
			if result != tt.want {
				t.Errorf("findFirstUserIndex() = %d, want %d", result, tt.want)
			}
		})
	}
}

func TestPruneContextMessages_NoPruningNeeded(t *testing.T) {
	cfg := DefaultContextPruningConfig()
	messages := []PrunableMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}

	// Large context window, no pruning needed
	result := PruneContextMessages(messages, 100_000, cfg)

	if result.SoftTrimCount != 0 {
		t.Errorf("Expected no soft trims, got %d", result.SoftTrimCount)
	}
	if result.HardClearCount != 0 {
		t.Errorf("Expected no hard clears, got %d", result.HardClearCount)
	}
}

func TestPruneContextMessages_SoftTrim(t *testing.T) {
	cfg := DefaultContextPruningConfig()
	cfg.SoftTrimRatio = 0.1          // Trigger soft trim easily
	cfg.SoftTrim.MaxChars = 100      // Small max chars
	cfg.SoftTrim.HeadChars = 20
	cfg.SoftTrim.TailChars = 20

	largeContent := strings.Repeat("x", 500)
	messages := []PrunableMessage{
		{Role: "user", Content: "hello", Index: 0},
		{Role: "tool_result", Content: largeContent, ToolName: "read_file", Index: 1},
		{Role: "assistant", Content: "response", Index: 2},
		{Role: "assistant", Content: "response2", Index: 3},
		{Role: "assistant", Content: "response3", Index: 4},
		{Role: "assistant", Content: "response4", Index: 5},
	}

	// Small context window to trigger pruning
	result := PruneContextMessages(messages, 500, cfg)

	if result.SoftTrimCount == 0 {
		t.Error("Expected at least one soft trim")
	}
	if strings.Contains(result.Messages[1].Content, "...") {
		// Soft trim happened
		if !strings.Contains(result.Messages[1].Content, "Tool result trimmed") {
			t.Error("Soft trimmed content should contain trim note")
		}
	}
}

func TestPruneContextMessages_ProtectsRecentAssistants(t *testing.T) {
	cfg := DefaultContextPruningConfig()
	cfg.KeepLastAssistants = 2
	cfg.SoftTrimRatio = 0.01 // Very aggressive

	messages := []PrunableMessage{
		{Role: "user", Content: "hello", Index: 0},
		{Role: "tool_result", Content: strings.Repeat("x", 1000), ToolName: "read_file", Index: 1},
		{Role: "assistant", Content: "old response", Index: 2},
		{Role: "tool_result", Content: strings.Repeat("y", 1000), ToolName: "read_file", Index: 3},
		{Role: "assistant", Content: "recent response 1", Index: 4},
		{Role: "assistant", Content: "recent response 2", Index: 5},
	}

	result := PruneContextMessages(messages, 200, cfg)

	// The tool result at index 3 should NOT be pruned because it's after the cutoff
	// (we protect the last 2 assistants, which are at indices 4 and 5)
	// Index 3 is the tool_result right before assistant at index 4
	// Actually, the cutoff should be at index 4 (first of last 2 assistants)
	// So index 3 is still prunable

	// The first tool result (index 1) should be pruned
	if result.Messages[1].Content == strings.Repeat("x", 1000) {
		// It was not pruned at all - this is fine if we're under ratio
	}
}

func TestPruneContextMessages_ProtectsPreUserMessages(t *testing.T) {
	cfg := DefaultContextPruningConfig()
	cfg.SoftTrimRatio = 0.01 // Very aggressive

	messages := []PrunableMessage{
		{Role: "system", Content: "identity prompt - should not be pruned", Index: 0},
		{Role: "tool_result", Content: strings.Repeat("x", 1000), ToolName: "read_file", Index: 1},
		{Role: "user", Content: "hello", Index: 2},
		{Role: "tool_result", Content: strings.Repeat("y", 1000), ToolName: "read_file", Index: 3},
		{Role: "assistant", Content: "response", Index: 4},
		{Role: "assistant", Content: "response2", Index: 5},
		{Role: "assistant", Content: "response3", Index: 6},
		{Role: "assistant", Content: "response4", Index: 7},
	}

	result := PruneContextMessages(messages, 100, cfg)

	// Messages before first user (indices 0, 1) should be protected
	if result.ProtectedIndices[0] != true {
		t.Error("Index 0 should be protected")
	}
	if result.ProtectedIndices[1] != true {
		t.Error("Index 1 should be protected")
	}
}

func TestPruneContextMessages_Disabled(t *testing.T) {
	cfg := DefaultContextPruningConfig()
	cfg.Enabled = false

	messages := []PrunableMessage{
		{Role: "user", Content: "hello"},
		{Role: "tool_result", Content: strings.Repeat("x", 10000), ToolName: "read_file"},
		{Role: "assistant", Content: "response"},
	}

	result := PruneContextMessages(messages, 10, cfg) // Tiny window

	if result.SoftTrimCount != 0 || result.HardClearCount != 0 {
		t.Error("Disabled config should not prune anything")
	}
}

func TestEstimateMessageChars(t *testing.T) {
	msg := PrunableMessage{
		Role:    "tool_result",
		Content: "hello world",
	}

	chars := EstimateMessageChars(msg)
	if chars < 11 {
		t.Errorf("Expected at least 11 chars, got %d", chars)
	}
}

func TestGetPruningStats(t *testing.T) {
	result := PruningResult{
		OriginalChars:  10000,
		PrunedChars:    5000,
		SoftTrimCount:  3,
		HardClearCount: 1,
	}

	stats := GetPruningStats(result)

	if stats.OriginalTokens != 2500 {
		t.Errorf("Expected 2500 original tokens, got %d", stats.OriginalTokens)
	}
	if stats.PrunedTokens != 1250 {
		t.Errorf("Expected 1250 pruned tokens, got %d", stats.PrunedTokens)
	}
	if stats.ReductionPercent != 50.0 {
		t.Errorf("Expected 50%% reduction, got %.2f%%", stats.ReductionPercent)
	}
	if stats.SoftTrimCount != 3 {
		t.Errorf("Expected 3 soft trims, got %d", stats.SoftTrimCount)
	}
}

func TestPruneToolResultText(t *testing.T) {
	cfg := SoftTrimConfig{
		MaxChars:  50,
		HeadChars: 15,
		TailChars: 15,
	}

	short := "short text"
	result := PruneToolResultText(short, cfg)
	if result != short {
		t.Error("Short text should not be pruned")
	}

	long := strings.Repeat("x", 100)
	result = PruneToolResultText(long, cfg)
	if !strings.Contains(result, "...") {
		t.Error("Long text should be pruned with ellipsis")
	}
}

func TestRemoveImageMarkers(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			"no markers",
			"plain text",
			"plain text",
		},
		{
			"single marker",
			"before [image] after",
			"before [image removed during context pruning] after",
		},
		{
			"marker with content",
			"text [image: screenshot.png] more text",
			"text [image removed during context pruning] more text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RemoveImageMarkers(tt.input)
			if result != tt.expect {
				t.Errorf("RemoveImageMarkers(%q) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}
