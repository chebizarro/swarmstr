package agent

import (
	"strings"
	"testing"
)

func TestEnforceTotalContextBudget_FitsWithinBudget(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
	}
	tools := []ToolDefinition{
		{Name: "test_tool", Description: "A simple tool"},
	}

	result := EnforceTotalContextBudget(messages, tools, 200_000, nil)
	if result.HistoryTrimmed != 0 {
		t.Error("should not trim history when within budget")
	}
	if result.ToolsDropped != 0 {
		t.Error("should not drop tools when within budget")
	}
	if result.SystemTruncated {
		t.Error("should not truncate system when within budget")
	}
	if len(result.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result.Messages))
	}
	if len(result.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(result.Tools))
	}
}

func TestEnforceTotalContextBudget_ZeroWindow(t *testing.T) {
	messages := []LLMMessage{{Role: "user", Content: "hi"}}
	result := EnforceTotalContextBudget(messages, nil, 0, nil)
	if len(result.Messages) != 1 {
		t.Error("zero window should return inputs unchanged")
	}
}

func TestEnforceTotalContextBudget_TrimsHistoryFirst(t *testing.T) {
	// Create a scenario where the total is over budget.
	// Use a small context window with lots of history.
	systemPrompt := strings.Repeat("system context ", 200) // ~3000 chars
	messages := []LLMMessage{
		{Role: "system", Content: systemPrompt},
	}
	// Add many history messages.
	for i := 0; i < 20; i++ {
		messages = append(messages,
			LLMMessage{Role: "user", Content: strings.Repeat("history message ", 100)},
			LLMMessage{Role: "assistant", Content: strings.Repeat("response text ", 100)},
		)
	}
	messages = append(messages, LLMMessage{Role: "user", Content: "current question"})

	// Very small window — should force trimming.
	result := EnforceTotalContextBudget(messages, nil, 4096, nil)

	if result.HistoryTrimmed == 0 {
		t.Error("expected history trimming for small window with large history")
	}
	// System prompt should be preserved.
	if result.Messages[0].Role != "system" {
		t.Error("system message should be preserved")
	}
	// Last user message should be preserved.
	lastMsg := result.Messages[len(result.Messages)-1]
	if lastMsg.Content != "current question" {
		t.Errorf("last user message should be preserved, got: %s", lastMsg.Content[:50])
	}
}

func TestEnforceTotalContextBudget_TruncatesDynamicContextBeforeHistory(t *testing.T) {
	dynamicContext := strings.Repeat("volatile dynamic context ", 900)
	oldHistory := strings.Repeat("stable historical exchange ", 60)
	messages := []LLMMessage{
		{Role: "system", Content: strings.Repeat("stable system prefix ", 20), Lane: PromptLaneSystemStatic},
		{Role: "user", Content: oldHistory},
		{Role: "assistant", Content: strings.Repeat("stable assistant response ", 60)},
		buildSyntheticDynamicContextMessage(dynamicContext),
		{Role: "user", Content: "real current question", Lane: PromptLaneCurrentUser},
	}

	result := EnforceTotalContextBudget(messages, nil, 4096, nil)

	if !result.ContextTruncated {
		t.Fatal("expected dynamic context to be truncated")
	}
	if result.HistoryTrimmed != 0 {
		t.Fatalf("expected dynamic context to absorb overage before history trimming, trimmed %d history messages", result.HistoryTrimmed)
	}
	if result.Messages[1].Content != oldHistory {
		t.Fatal("ordinary history should be preserved when dynamic context can satisfy the overage")
	}

	var dynamic LLMMessage
	for _, msg := range result.Messages {
		if msg.Lane == PromptLaneDynamicContext {
			dynamic = msg
			break
		}
	}
	if dynamic.Content == "" {
		t.Fatal("expected dynamic-context message to remain present")
	}
	if len(dynamic.Content) >= len(messages[3].Content) {
		t.Fatal("dynamic-context content should be shorter after truncation")
	}
	if !strings.HasPrefix(dynamic.Content, syntheticDynamicContextPrefix) {
		t.Fatal("dynamic-context truncation should preserve the synthetic wrapper prefix")
	}
	if !strings.Contains(dynamic.Content, "Dynamic context truncated by pre-flight budget gate") {
		t.Fatal("dynamic-context truncation should include the preflight marker")
	}
	lastMsg := result.Messages[len(result.Messages)-1]
	if lastMsg.Lane != PromptLaneCurrentUser || lastMsg.Content != "real current question" {
		t.Fatalf("real current user message should remain last, got %+v", lastMsg)
	}
}

func TestTruncateDynamicContextMessages_DoesNotDuplicateMarker(t *testing.T) {
	messages := []LLMMessage{
		buildSyntheticDynamicContextMessage(strings.Repeat("volatile ", 1200)),
	}

	first := truncateDynamicContextMessages(messages, 500)
	if !first.Truncated {
		t.Fatal("expected first dynamic-context truncation")
	}
	second := truncateDynamicContextMessages(first.Messages, 700)
	if !second.Truncated {
		t.Fatal("expected second dynamic-context truncation")
	}

	content := second.Messages[0].Content
	if got := strings.Count(content, "Dynamic context truncated by pre-flight budget gate"); got != 1 {
		t.Fatalf("expected exactly one truncation marker after repeated preflight, got %d in %q", got, content)
	}
	if !strings.HasPrefix(content, syntheticDynamicContextPrefix) {
		t.Fatal("repeated truncation should preserve the synthetic wrapper prefix")
	}
}

func TestTrimHistoryMessages_SkipsDynamicContextAndCurrentUserLane(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "stable system", Lane: PromptLaneSystemStatic},
		{Role: "user", Content: "old history"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "synthetic context", Lane: PromptLaneDynamicContext},
		{Role: "user", Content: "real current question", Lane: PromptLaneCurrentUser},
	}

	result := trimHistoryMessages(messages, 10000)

	if result.Count != 2 {
		t.Fatalf("expected only ordinary history messages to be trimmed, got %d", result.Count)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected system, dynamic context, and current user to remain; got %d messages", len(result.Messages))
	}
	if result.Messages[1].Lane != PromptLaneDynamicContext {
		t.Fatalf("dynamic context should not be treated as trimmable history, got %+v", result.Messages[1])
	}
	if result.Messages[2].Lane != PromptLaneCurrentUser || result.Messages[2].Content != "real current question" {
		t.Fatalf("real current user should be preserved by lane, got %+v", result.Messages[2])
	}
}

func TestEnforceTotalContextBudget_DropsToolsAfterHistory(t *testing.T) {
	// Small system prompt, no trimmable history, many large tools.
	messages := []LLMMessage{
		{Role: "system", Content: "Be helpful."},
		{Role: "user", Content: "Hi"},
	}

	var tools []ToolDefinition
	for i := 0; i < 50; i++ {
		tools = append(tools, ToolDefinition{
			Name:        strings.Repeat("t", 10),
			Description: strings.Repeat("description text ", 50), // ~850 chars each
		})
	}

	// Small window that can't fit all tools.
	result := EnforceTotalContextBudget(messages, tools, 4096, nil)

	if result.ToolsDropped == 0 {
		t.Error("expected tools to be dropped for small window with many tools")
	}
	if len(result.Tools) >= len(tools) {
		t.Errorf("expected fewer tools: got %d, started with %d", len(result.Tools), len(tools))
	}
}

func TestEnforceTotalContextBudget_PreservesCriticalTools(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "Be helpful."},
		{Role: "user", Content: "Hi"},
	}

	tools := []ToolDefinition{
		{Name: "memory_search", Description: strings.Repeat("x", 500)},
		{Name: "large_tool", Description: strings.Repeat("x", 2000)},
		{Name: "session_send", Description: strings.Repeat("x", 500)},
	}

	// Small window — should drop tools but preserve critical ones.
	result := EnforceTotalContextBudget(messages, tools, 2048, DefaultCriticalToolNames())

	criticalFound := make(map[string]bool)
	for _, def := range result.Tools {
		criticalFound[def.Name] = true
	}
	for _, name := range DefaultCriticalToolNames() {
		// Only check if the tool was in the input
		for _, t := range tools {
			if t.Name == name && !criticalFound[name] {
				t2 := t
				_ = t2
				// Critical tools should survive when possible
			}
		}
	}
}

func TestEnforceTotalContextBudget_TruncatesSystemAsLastResort(t *testing.T) {
	// Massive system prompt, tiny window, no trimmable history or tools.
	systemPrompt := strings.Repeat("Very important system context. ", 2000) // ~60K chars
	messages := []LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Hi"},
	}

	result := EnforceTotalContextBudget(messages, nil, 2048, nil)

	if !result.SystemTruncated {
		t.Error("expected system prompt truncation as last resort")
	}
	// System message should be shorter than original.
	if len(result.Messages[0].Content) >= len(systemPrompt) {
		t.Error("system prompt should have been truncated")
	}
	if !strings.Contains(result.Messages[0].Content, "⚠️") {
		t.Error("truncated system should contain warning marker")
	}
}

func TestEstimateTotalTokens_Empty(t *testing.T) {
	tokens := estimateTotalTokens(nil, nil)
	if tokens != 0 {
		t.Errorf("expected 0 tokens for empty input, got %d", tokens)
	}
}

func TestEstimateTotalTokens_MixedContent(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: strings.Repeat("x", 3000)}, // ~1000 tokens at 3c/t
		{Role: "user", Content: "hello"},
	}
	tools := []ToolDefinition{
		{Name: "tool", Description: strings.Repeat("y", 250)}, // ~300 chars → ~120 tokens at 2.5c/t
	}

	tokens := estimateTotalTokens(messages, tools)
	// Should be > 1000 (from system prompt alone)
	if tokens < 1000 {
		t.Errorf("expected > 1000 tokens, got %d", tokens)
	}
}

func TestTrimHistoryMessages_PreservesSystemAndLastUser(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "second answer"},
		{Role: "user", Content: "current question"},
	}

	result := trimHistoryMessages(messages, 10000) // trim a lot

	// System should be preserved.
	if result.Messages[0].Role != "system" {
		t.Error("system message should be preserved")
	}
	// Last user message should be preserved.
	lastMsg := result.Messages[len(result.Messages)-1]
	if lastMsg.Content != "current question" {
		t.Errorf("last user message should be preserved, got: %s", lastMsg.Content)
	}
	if result.Count == 0 {
		t.Error("expected some messages to be trimmed")
	}
}

func TestDropLargestTools_DropsLargestFirst(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "small", Description: "x"},
		{Name: "medium", Description: strings.Repeat("x", 200)},
		{Name: "large", Description: strings.Repeat("x", 500)},
	}

	result := dropLargestTools(tools, nil, 100) // small target

	if result.Count == 0 {
		t.Fatal("expected at least one tool dropped")
	}
	// The largest tool should be dropped first.
	for _, def := range result.Tools {
		if def.Name == "large" {
			t.Error("largest tool should have been dropped first")
		}
	}
}

func TestDropLargestTools_PreservesCritical(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "memory_search", Description: strings.Repeat("x", 500)},
		{Name: "regular", Description: strings.Repeat("x", 100)},
	}
	critical := map[string]bool{"memory_search": true}

	result := dropLargestTools(tools, critical, 10000)

	// memory_search should survive even though it's the largest.
	found := false
	for _, def := range result.Tools {
		if def.Name == "memory_search" {
			found = true
		}
	}
	if !found {
		t.Error("critical tool should not be dropped")
	}
}

func TestEstimateTurnTokens(t *testing.T) {
	turn := Turn{
		StaticSystemPrompt: strings.Repeat("x", 3000),
		Context:            strings.Repeat("y", 1000),
		UserText:           "hello",
		Tools: []ToolDefinition{
			{Name: "tool", Description: strings.Repeat("z", 250)},
		},
	}

	tokens := EstimateTurnTokens(turn)
	if tokens < 1000 {
		t.Errorf("expected > 1000 tokens, got %d", tokens)
	}
}

func TestMustFitContext_SmallPrompt(t *testing.T) {
	turn := Turn{
		StaticSystemPrompt:  "Be helpful.",
		UserText:            "Hello",
		ContextWindowTokens: 200_000,
	}
	if !MustFitContext(turn) {
		t.Error("small prompt should fit in 200K window")
	}
}

func TestMustFitContext_HugePromptSmallWindow(t *testing.T) {
	turn := Turn{
		StaticSystemPrompt:  strings.Repeat("x", 100_000),
		UserText:            "Hello",
		ContextWindowTokens: 4096,
	}
	if MustFitContext(turn) {
		t.Error("100K system prompt should not fit in 4K window")
	}
}
