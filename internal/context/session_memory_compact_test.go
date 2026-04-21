package context

import (
	stdctx "context"
	"strings"
	"testing"
)

// ─── estimateMessageTokens tests ──────────────────���──────────────────────────

func TestEstimateMessageTokens_PlainText(t *testing.T) {
	msg := Message{Role: "user", Content: "hello world"} // 11 chars → 3 tokens
	got := estimateMessageTokens(msg)
	if got < 1 {
		t.Errorf("expected > 0 tokens, got %d", got)
	}
}

func TestEstimateMessageTokens_ToolCalls(t *testing.T) {
	msg := Message{
		Role: "assistant",
		ToolCalls: []ToolCallRef{
			{ID: "tc1", Name: "web_search", ArgsJSON: `{"q":"test"}`},
		},
	}
	got := estimateMessageTokens(msg)
	if got < 2 {
		t.Errorf("expected > 1 tokens for tool call message, got %d", got)
	}
}

func TestEstimateMessageTokens_EmptyMessage(t *testing.T) {
	msg := Message{Role: "user"}
	got := estimateMessageTokens(msg)
	if got < 1 {
		t.Errorf("expected minimum 1 token, got %d", got)
	}
}

// ─── hasTextContent tests ──────────────────────────��────────────────────────��

func TestHasTextContent_UserText(t *testing.T) {
	if !hasTextContent(Message{Role: "user", Content: "hello"}) {
		t.Error("expected user text to have text content")
	}
}

func TestHasTextContent_AssistantText(t *testing.T) {
	if !hasTextContent(Message{Role: "assistant", Content: "sure"}) {
		t.Error("expected assistant text to have text content")
	}
}

func TestHasTextContent_ToolResult(t *testing.T) {
	if hasTextContent(Message{Role: "tool", Content: "result", ToolCallID: "tc1"}) {
		t.Error("expected tool result to not count as text content")
	}
}

func TestHasTextContent_EmptyUser(t *testing.T) {
	if hasTextContent(Message{Role: "user"}) {
		t.Error("expected empty user message to not count")
	}
}

func TestHasTextContent_SystemMessage(t *testing.T) {
	if hasTextContent(Message{Role: "system", Content: "you are helpful"}) {
		t.Error("expected system message to not count as text content")
	}
}

// ─── adjustIndexToPreserveToolPairs tests ───────────────��─────────────────────

func TestAdjustIndex_NoToolResults(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "do something"},
	}
	got := adjustIndexToPreserveToolPairs(msgs, 2)
	if got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
}

func TestAdjustIndex_ToolPairAlreadyKept(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "tc1", Name: "search"}}},
		{Role: "tool", Content: "result", ToolCallID: "tc1"},
	}
	// Start at index 1 — both tool_use and tool_result are in the kept range.
	got := adjustIndexToPreserveToolPairs(msgs, 1)
	if got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
}

func TestAdjustIndex_PullsBackForOrphanedToolResult(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "tc1", Name: "search"}}},
		{Role: "tool", Content: "result", ToolCallID: "tc1"},
		{Role: "user", Content: "thanks"},
	}
	// Start at index 2 — tool_result references tc1 but assistant at index 1 is excluded.
	got := adjustIndexToPreserveToolPairs(msgs, 2)
	if got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
}

func TestAdjustIndex_MultipleOrphanedPairs(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "tc1", Name: "fetch"}}},
		{Role: "tool", Content: "data1", ToolCallID: "tc1"},
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "tc2", Name: "search"}}},
		{Role: "tool", Content: "data2", ToolCallID: "tc2"},
		{Role: "user", Content: "process"},
	}
	// Start at index 3 — tool_result tc2 needs assistant at index 2.
	got := adjustIndexToPreserveToolPairs(msgs, 3)
	if got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
}

func TestAdjustIndex_ZeroIndex(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}
	got := adjustIndexToPreserveToolPairs(msgs, 0)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestAdjustIndex_BeyondEnd(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}
	got := adjustIndexToPreserveToolPairs(msgs, 5)
	if got != 5 {
		t.Errorf("expected 5 (unchanged), got %d", got)
	}
}

// ─── calculateMessagesToKeepIndex tests ────────────────────────���─────────────

func TestCalcKeepIndex_EmptyMessages(t *testing.T) {
	got := calculateMessagesToKeepIndex(nil, -1, DefaultSessionMemoryCompactConfig)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestCalcKeepIndex_ExpandsToMeetMinTokens(t *testing.T) {
	// Create messages that are ~1000 tokens each (4000 chars).
	bigContent := strings.Repeat("x", 4000)
	msgs := make([]Message, 20)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: bigContent}
	}

	cfg := SessionMemoryCompactConfig{
		MinTokens:            5000,
		MinTextBlockMessages: 2,
		MaxTokens:            40000,
	}

	got := calculateMessagesToKeepIndex(msgs, -1, cfg)
	// With -1 lastSummarizedIndex, starts at end and expands back.
	// Each msg ~1000 tokens. Need 5000 tokens → 5 messages → start at index 15.
	// Also needs 2 text block messages (all are user text, so met immediately).
	if got > 16 || got < 14 {
		t.Errorf("expected start index around 15, got %d", got)
	}
}

func TestCalcKeepIndex_StopsAtMaxTokens(t *testing.T) {
	bigContent := strings.Repeat("x", 4000) // ~1000 tokens each
	msgs := make([]Message, 100)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: bigContent}
	}

	cfg := SessionMemoryCompactConfig{
		MinTokens:            50000, // higher than maxTokens → force max cap to trigger
		MinTextBlockMessages: 100,   // also higher → force max cap
		MaxTokens:            8000,  // hard cap at ~8K tokens → ~8 messages
	}

	got := calculateMessagesToKeepIndex(msgs, -1, cfg)
	// Should keep ~8 messages (8000 tokens) before hitting the max cap.
	kept := 100 - got
	if kept > 10 || kept < 6 {
		t.Errorf("expected ~8 messages kept (max cap), got %d (startIndex=%d)", kept, got)
	}
}

func TestCalcKeepIndex_WithLastSummarizedIndex(t *testing.T) {
	bigContent := strings.Repeat("x", 4000) // ~1000 tokens each
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: bigContent}
	}

	cfg := SessionMemoryCompactConfig{
		MinTokens:            2000,
		MinTextBlockMessages: 1,
		MaxTokens:            40000,
	}

	// Last summarized at index 5 → keep from index 6 onwards (4 messages, ~4000 tokens).
	// Both mins are met, so no expansion needed.
	got := calculateMessagesToKeepIndex(msgs, 5, cfg)
	if got != 6 {
		t.Errorf("expected 6, got %d", got)
	}
}

func TestCalcKeepIndex_ExpandsFromSummarizedIndex(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: strings.Repeat("x", 400)} // ~100 tokens each
	}

	cfg := SessionMemoryCompactConfig{
		MinTokens:            500, // need 5 messages
		MinTextBlockMessages: 3,   // need 3 text blocks
		MaxTokens:            40000,
	}

	// Last summarized at index 8 → initially keep only index 9 (1 msg, ~100 tokens).
	// Not enough → expand back to meet mins.
	got := calculateMessagesToKeepIndex(msgs, 8, cfg)
	// Needs 500 tokens (5 msgs) and 3 text blocks. Expanding from 8 back to ~4.
	if got > 6 {
		t.Errorf("expected start index <= 6 to meet mins, got %d", got)
	}
}

func TestCalcKeepIndex_PreservesToolPairs(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: strings.Repeat("x", 40000)},           // 0: big msg
		{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "tc1", Name: "search"}}}, // 1
		{Role: "tool", Content: "result data", ToolCallID: "tc1"},                  // 2
		{Role: "user", Content: strings.Repeat("x", 40000)},           // 3: big msg
		{Role: "assistant", Content: strings.Repeat("x", 40000)},      // 4: big msg
	}

	cfg := SessionMemoryCompactConfig{
		MinTokens:            5000,
		MinTextBlockMessages: 2,
		MaxTokens:            30000,
	}

	got := calculateMessagesToKeepIndex(msgs, -1, cfg)
	// Should include the tool pair at indexes 1-2 if the tool result is in the kept range.
	// Since we expand from the end: index 4 (~10000 tokens), 3 (~10000), 2 (~3), 1 (~3).
	// If the cut lands at 2, adjustIndex should pull back to 1.
	if got == 2 {
		t.Error("should not split tool pair — expected index 1 or lower, not 2")
	}
}

// ─── SmallWindowEngine CompactWithSessionMemory tests ────────────────���────────

func TestSWE_CompactWithSM_EmptySession(t *testing.T) {
	e := NewSmallWindowEngine(TierStandardSW, DefaultSmallWindowBudget(TierStandardSW))
	cr, err := e.CompactWithSessionMemory(stdctx.Background(), "sess1", "# Summary\nSome work", DefaultSessionMemoryCompactConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Compacted {
		t.Error("expected no compaction on empty session")
	}
}

func TestSWE_CompactWithSM_PrunesOldMessages(t *testing.T) {
	e := NewSmallWindowEngine(TierStandardSW, DefaultSmallWindowBudget(TierStandardSW))
	ctx := stdctx.Background()

	// Ingest 50 large messages.
	for i := 0; i < 50; i++ {
		e.Ingest(ctx, "sess1", Message{
			Role:    "user",
			Content: strings.Repeat("x", 4000), // ~1000 tokens each → 50000 total
		})
	}

	sessionMemory := "# Session Title\nImplementing context reduction\n# Current State\nWorking on compaction"
	cr, err := e.CompactWithSessionMemory(ctx, "sess1", sessionMemory, DefaultSessionMemoryCompactConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !cr.Compacted {
		t.Error("expected compaction")
	}
	if cr.TokensBefore == 0 {
		t.Error("expected non-zero tokens before")
	}
	if cr.TokensAfter >= cr.TokensBefore {
		t.Errorf("expected fewer tokens after: before=%d after=%d", cr.TokensBefore, cr.TokensAfter)
	}

	// Verify the summary is set.
	e.mu.Lock()
	sess := e.sessions["sess1"]
	if sess.summary != sessionMemory {
		t.Error("expected session summary to be set to session memory")
	}
	msgCount := len(sess.messages)
	e.mu.Unlock()

	// Should have kept some messages (between minTextBlockMessages and original count).
	if msgCount >= 50 {
		t.Errorf("expected fewer than 50 messages after compaction, got %d", msgCount)
	}
	if msgCount < DefaultSessionMemoryCompactConfig.MinTextBlockMessages {
		t.Errorf("expected at least %d messages, got %d", DefaultSessionMemoryCompactConfig.MinTextBlockMessages, msgCount)
	}
}

func TestSWE_CompactWithSM_SummaryAppearsInAssemble(t *testing.T) {
	e := NewSmallWindowEngine(TierStandardSW, DefaultSmallWindowBudget(TierStandardSW))
	ctx := stdctx.Background()

	for i := 0; i < 20; i++ {
		e.Ingest(ctx, "sess1", Message{
			Role:    "user",
			Content: strings.Repeat("x", 4000),
		})
	}

	sessionMemory := "# Session Title\nTest session\n# Current State\nDoing stuff"
	_, err := e.CompactWithSessionMemory(ctx, "sess1", sessionMemory, DefaultSessionMemoryCompactConfig)
	if err != nil {
		t.Fatal(err)
	}

	result, err := e.Assemble(ctx, "sess1", 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if result.SystemPromptAddition != sessionMemory {
		t.Errorf("expected session memory in SystemPromptAddition, got %q", result.SystemPromptAddition)
	}
}

func TestSWE_CompactWithSM_PreservesToolPairs(t *testing.T) {
	e := NewSmallWindowEngine(TierStandardSW, DefaultSmallWindowBudget(TierStandardSW))
	ctx := stdctx.Background()

	// Create messages with a tool pair near the end.
	for i := 0; i < 30; i++ {
		e.Ingest(ctx, "sess1", Message{
			Role:    "user",
			Content: strings.Repeat("x", 4000),
		})
	}
	// Tool pair.
	e.Ingest(ctx, "sess1", Message{
		Role:      "assistant",
		ToolCalls: []ToolCallRef{{ID: "tc1", Name: "web_search"}},
	})
	e.Ingest(ctx, "sess1", Message{
		Role:       "tool",
		Content:    "search results",
		ToolCallID: "tc1",
	})
	e.Ingest(ctx, "sess1", Message{
		Role:    "user",
		Content: "thanks",
	})

	cfg := SessionMemoryCompactConfig{
		MinTokens:            500,
		MinTextBlockMessages: 2,
		MaxTokens:            5000,
	}
	_, err := e.CompactWithSessionMemory(ctx, "sess1", "# Summary", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Verify no orphaned tool results.
	e.mu.Lock()
	defer e.mu.Unlock()
	sess := e.sessions["sess1"]
	toolUseIDs := make(map[string]bool)
	toolResultIDs := make(map[string]bool)
	for _, msg := range sess.messages {
		for _, tc := range msg.ToolCalls {
			toolUseIDs[tc.ID] = true
		}
		if msg.ToolCallID != "" {
			toolResultIDs[msg.ToolCallID] = true
		}
	}
	for id := range toolResultIDs {
		if !toolUseIDs[id] {
			t.Errorf("orphaned tool_result for id %q — tool_use not in kept messages", id)
		}
	}
}

// ─── WindowedEngine CompactWithSessionMemory tests ─────────────────��──────────

func TestWE_CompactWithSM_PrunesMessages(t *testing.T) {
	e := NewWindowedEngine(100)
	ctx := stdctx.Background()

	for i := 0; i < 50; i++ {
		e.Ingest(ctx, "sess1", Message{
			Role:    "user",
			Content: strings.Repeat("x", 4000),
		})
	}

	cr, err := e.CompactWithSessionMemory(ctx, "sess1", "# Summary", DefaultSessionMemoryCompactConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !cr.Compacted {
		t.Error("expected compaction")
	}
	if cr.TokensAfter >= cr.TokensBefore {
		t.Errorf("expected fewer tokens: before=%d after=%d", cr.TokensBefore, cr.TokensAfter)
	}
}

func TestWE_CompactWithSM_EmptySession(t *testing.T) {
	e := NewWindowedEngine(100)
	cr, err := e.CompactWithSessionMemory(stdctx.Background(), "sess1", "# Summary", DefaultSessionMemoryCompactConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Compacted {
		t.Error("expected no compaction on empty session")
	}
}

// ─── SessionMemoryCompactState tests ──────────────────��───────────────────────

func TestSMCompactState_SetAndGet(t *testing.T) {
	s := NewSessionMemoryCompactState()
	s.SetLastSummarized("sess1", "msg-42")
	if got := s.GetLastSummarized("sess1"); got != "msg-42" {
		t.Errorf("expected msg-42, got %q", got)
	}
}

func TestSMCompactState_EmptyForUnknownSession(t *testing.T) {
	s := NewSessionMemoryCompactState()
	if got := s.GetLastSummarized("nonexistent"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestSMCompactState_Delete(t *testing.T) {
	s := NewSessionMemoryCompactState()
	s.SetLastSummarized("sess1", "msg-1")
	s.Delete("sess1")
	if got := s.GetLastSummarized("sess1"); got != "" {
		t.Errorf("expected empty after delete, got %q", got)
	}
}

// ─── Interface conformance ────────────────────────────────────────────────────

func TestInterfaceConformance_SmallWindow(t *testing.T) {
	var e Engine = NewSmallWindowEngine(TierStandardSW, DefaultSmallWindowBudget(TierStandardSW))
	if _, ok := e.(SessionMemoryCompacter); !ok {
		t.Error("SmallWindowEngine should implement SessionMemoryCompacter")
	}
}

func TestInterfaceConformance_Windowed(t *testing.T) {
	var e Engine = NewWindowedEngine(50)
	if _, ok := e.(SessionMemoryCompacter); !ok {
		t.Error("WindowedEngine should implement SessionMemoryCompacter")
	}
}
