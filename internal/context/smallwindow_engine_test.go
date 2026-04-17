package context

import (
	stdctx "context"
	"strings"
	"testing"
)

func TestSmallWindowBudgetForTokens_ProportionalScaling(t *testing.T) {
	sizes := []int{2048, 4096, 8192, 16384, 32000, 64000, 128000, 200000}
	var prev SmallWindowBudget
	for _, tokens := range sizes {
		b := SmallWindowBudgetForTokens(tokens)
		if b.HistoryMaxChars < prev.HistoryMaxChars {
			t.Errorf("HistoryMaxChars decreased at %d tokens: %d < %d", tokens, b.HistoryMaxChars, prev.HistoryMaxChars)
		}
		if b.KeepRecent < prev.KeepRecent {
			t.Errorf("KeepRecent decreased at %d tokens: %d < %d", tokens, b.KeepRecent, prev.KeepRecent)
		}
		if b.MaxMessages < prev.MaxMessages {
			t.Errorf("MaxMessages decreased at %d tokens: %d < %d", tokens, b.MaxMessages, prev.MaxMessages)
		}
		prev = b
	}
}

func TestSmallWindowBudgetForTokens_Boundaries(t *testing.T) {
	// Tiny model
	tiny := SmallWindowBudgetForTokens(2048)
	if tiny.HistoryMaxChars < 2000 {
		t.Errorf("tiny HistoryMaxChars = %d, want >= 2000 (floor)", tiny.HistoryMaxChars)
	}
	if tiny.KeepRecent != 1 {
		t.Errorf("tiny KeepRecent = %d, want 1 (floor)", tiny.KeepRecent)
	}
	if tiny.MaxMessages < 6 {
		t.Errorf("tiny MaxMessages = %d, want >= 6 (floor)", tiny.MaxMessages)
	}

	// Large model
	large := SmallWindowBudgetForTokens(200_000)
	if large.HistoryMaxChars > 200_000 {
		t.Errorf("large HistoryMaxChars = %d, want <= 200000 (cap)", large.HistoryMaxChars)
	}
	if large.KeepRecent > 8 {
		t.Errorf("large KeepRecent = %d, want <= 8 (cap)", large.KeepRecent)
	}
	if large.MaxMessages > 80 {
		t.Errorf("large MaxMessages = %d, want <= 80 (cap)", large.MaxMessages)
	}

	// Zero defaults to 200K
	zero := SmallWindowBudgetForTokens(0)
	if zero.HistoryMaxChars != large.HistoryMaxChars {
		t.Errorf("zero tokens should produce same budget as 200K")
	}
}

func TestSmallWindowEngine_Ingest(t *testing.T) {
	budget := SmallWindowBudgetForTokens(8192)
	engine := NewSmallWindowEngine(TierSmallSW, budget)
	ctx := stdctx.Background()

	r1, err := engine.Ingest(ctx, "s1", Message{ID: "m1", Role: "user", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Ingested {
		t.Error("first ingest should succeed")
	}

	// Duplicate should be rejected
	r2, err := engine.Ingest(ctx, "s1", Message{ID: "m1", Role: "user", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Ingested {
		t.Error("duplicate should not be ingested")
	}
}

func TestSmallWindowEngine_Assemble_ClearsOldToolResults(t *testing.T) {
	budget := SmallWindowBudget{
		HistoryMaxChars: 10000,
		KeepRecent:      1,
		MaxMessages:     50,
	}
	engine := NewSmallWindowEngine(TierSmallSW, budget)
	ctx := stdctx.Background()

	// Ingest messages with tool results
	engine.Ingest(ctx, "s1", Message{Role: "user", Content: "search for info"})
	engine.Ingest(ctx, "s1", Message{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc1", Name: "web_search"}}})
	engine.Ingest(ctx, "s1", Message{Role: "tool", Content: strings.Repeat("a", 500), ToolCallID: "tc1"})
	engine.Ingest(ctx, "s1", Message{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc2", Name: "web_search"}}})
	engine.Ingest(ctx, "s1", Message{Role: "tool", Content: strings.Repeat("b", 500), ToolCallID: "tc2"})
	engine.Ingest(ctx, "s1", Message{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc3", Name: "web_search"}}})
	engine.Ingest(ctx, "s1", Message{Role: "tool", Content: strings.Repeat("c", 500), ToolCallID: "tc3"})

	result, err := engine.Assemble(ctx, "s1", 8192)
	if err != nil {
		t.Fatal(err)
	}

	// Count cleared messages
	cleared := 0
	for _, msg := range result.Messages {
		if msg.Content == swClearedMarker {
			cleared++
		}
	}
	if cleared < 1 {
		t.Errorf("expected at least 1 cleared tool result, got %d", cleared)
	}
}

func TestSmallWindowEngine_Assemble_TrimsToWindow(t *testing.T) {
	budget := SmallWindowBudget{
		HistoryMaxChars: 10000,
		KeepRecent:      2,
		MaxMessages:     5,
	}
	engine := NewSmallWindowEngine(TierSmallSW, budget)
	ctx := stdctx.Background()

	// Ingest more messages than MaxMessages
	for i := 0; i < 10; i++ {
		engine.Ingest(ctx, "s1", Message{Role: "user", Content: "msg"})
	}

	result, err := engine.Assemble(ctx, "s1", 8192)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) > budget.MaxMessages {
		t.Errorf("messages (%d) exceed MaxMessages (%d)", len(result.Messages), budget.MaxMessages)
	}
}

func TestSmallWindowEngine_Bootstrap(t *testing.T) {
	budget := SmallWindowBudget{
		HistoryMaxChars: 10000,
		KeepRecent:      2,
		MaxMessages:     5,
	}
	engine := NewSmallWindowEngine(TierSmallSW, budget)
	ctx := stdctx.Background()

	// Bootstrap with more messages than MaxMessages
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: "bootstrap msg"}
	}

	result, err := engine.Bootstrap(ctx, "s1", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Bootstrapped {
		t.Error("expected Bootstrapped=true")
	}
	if result.ImportedMessages > budget.MaxMessages {
		t.Errorf("imported %d messages, exceeds MaxMessages %d", result.ImportedMessages, budget.MaxMessages)
	}
}

func TestSmallWindowEngine_EmptySession(t *testing.T) {
	budget := SmallWindowBudgetForTokens(8192)
	engine := NewSmallWindowEngine(TierSmallSW, budget)
	ctx := stdctx.Background()

	result, err := engine.Assemble(ctx, "nonexistent", 8192)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("empty session should return 0 messages, got %d", len(result.Messages))
	}
}

func TestSmallWindowEngine_Factory(t *testing.T) {
	// Test factory with context_window_tokens
	engine, err := NewEngine("small-window", "test-session", map[string]any{
		"context_window_tokens": float64(8192),
	})
	if err != nil {
		t.Fatalf("factory failed: %v", err)
	}
	if engine == nil {
		t.Fatal("factory returned nil engine")
	}

	// Test factory with tier string fallback
	engine2, err := NewEngine("small-window", "test-session", map[string]any{
		"tier": "micro",
	})
	if err != nil {
		t.Fatalf("factory with tier failed: %v", err)
	}
	if engine2 == nil {
		t.Fatal("factory with tier returned nil")
	}
}

func TestDefaultSmallWindowBudget_TierDefaults(t *testing.T) {
	micro := DefaultSmallWindowBudget(TierMicroSW)
	small := DefaultSmallWindowBudget(TierSmallSW)
	standard := DefaultSmallWindowBudget(TierStandardSW)

	if micro.HistoryMaxChars >= small.HistoryMaxChars {
		t.Error("micro should have smaller history than small")
	}
	if small.HistoryMaxChars >= standard.HistoryMaxChars {
		t.Error("small should have smaller history than standard")
	}
	if micro.MaxMessages >= small.MaxMessages {
		t.Error("micro should have fewer max messages than small")
	}
}
