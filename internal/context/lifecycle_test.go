package context

import (
	stdctx "context"
	"strings"
	"testing"
)

func TestWindowedEngineAfterTurnAndPromptCacheTelemetry(t *testing.T) {
	engine := NewWindowedEngine(2)
	ctx := stdctx.Background()
	messages := []Message{
		{ID: "m1", Role: "user", Content: "one"},
		{ID: "m2", Role: "assistant", Content: "two"},
		{ID: "m3", Role: "user", Content: "three"},
	}
	if err := RunAfterTurn(ctx, engine, "s1", AfterTurnParams{Messages: messages, TokenBudget: 1, CurrentTokens: 2}); err != nil {
		t.Fatalf("after turn: %v", err)
	}
	assembled, err := engine.Assemble(ctx, "s1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(assembled.Messages) != 3 || assembled.Messages[0].ID != "m1" {
		t.Fatalf("expected after-turn lifecycle to retain unsummarized history, got %#v", assembled.Messages)
	}
	if assembled.PromptCache == nil || assembled.ContextProjection == nil {
		t.Fatalf("expected prompt cache and projection metadata")
	}
}

func TestWindowedEngineSubagentForkRollbackAndMaintainRewrite(t *testing.T) {
	engine := NewWindowedEngine(10)
	ctx := stdctx.Background()
	_, _ = engine.Ingest(ctx, "parent", Message{ID: "p1", Role: "user", Content: "parent context"})
	prep, err := PrepareSubagentSpawn(ctx, engine, SubagentSpawnParams{ParentSessionID: "parent", ChildSessionID: "child", ContextMode: "fork"})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prep.InheritedMessages != 1 {
		t.Fatalf("expected inherited message, got %d", prep.InheritedMessages)
	}
	maint, err := RunMaintain(ctx, engine, "child", MaintainParams{RewriteTranscriptEntries: []TranscriptRewrite{{EntryID: "p1", Role: "user", Text: "redacted"}}})
	if err != nil {
		t.Fatalf("maintain: %v", err)
	}
	if !maint.Changed || maint.RewrittenEntries != 1 {
		t.Fatalf("expected rewrite result, got %#v", maint)
	}
	assembled, _ := engine.Assemble(ctx, "child", 100)
	if got := assembled.Messages[0].Content; got != "redacted" {
		t.Fatalf("expected rewritten child content, got %q", got)
	}
	if err := prep.Rollback(); err != nil {
		t.Fatal(err)
	}
	assembled, _ = engine.Assemble(ctx, "child", 100)
	if len(assembled.Messages) != 0 {
		t.Fatalf("expected rollback to remove child state")
	}
}

func TestSmallWindowMaintainCleansToolResults(t *testing.T) {
	engine := NewSmallWindowEngine(TierSmallSW, SmallWindowBudget{HistoryMaxChars: 1000, KeepRecent: 0, MaxMessages: 10})
	ctx := stdctx.Background()
	_, _ = engine.Ingest(ctx, "s1", Message{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "tc1", Name: "web_search"}}})
	_, _ = engine.Ingest(ctx, "s1", Message{Role: "tool", ToolCallID: "tc1", Content: strings.Repeat("x", 200)})
	result, err := RunMaintain(ctx, engine, "s1", MaintainParams{CleanupToolResults: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.CleanedMessages != 1 {
		t.Fatalf("expected one cleaned tool result, got %#v", result)
	}
}
