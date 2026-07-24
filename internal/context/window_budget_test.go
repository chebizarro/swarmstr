package context

import (
	stdctx "context"
	"strings"
	"testing"
)

type recordingRecall struct {
	maxChars int
	calls    int
	text     string
}

func (r *recordingRecall) AssembleActiveRecall(_ stdctx.Context, _ string, _ Message, _ []Message, maxChars int) (string, error) {
	r.calls++
	r.maxChars = maxChars
	return r.text, nil
}

func TestWindowedAssembleEnforcesBudgetWithoutMutatingHistory(t *testing.T) {
	engine := NewWindowedEngine(20)
	ctx := stdctx.Background()
	for i := 0; i < 6; i++ {
		_, _ = engine.Ingest(ctx, "s", Message{Role: "assistant", Content: strings.Repeat("history ", 20), ID: string(rune('a' + i))})
	}
	_, _ = engine.Ingest(ctx, "s", Message{Role: "user", Content: "current question", ID: "current"})

	limited, err := engine.Assemble(ctx, "s", 12)
	if err != nil {
		t.Fatal(err)
	}
	if limited.EstimatedTokens > 12 {
		t.Fatalf("assembly exceeds budget: %d", limited.EstimatedTokens)
	}
	foundCurrent := false
	for _, msg := range limited.Messages {
		if msg.ID == "current" {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Fatal("latest user message was removed")
	}
	unlimited, err := engine.Assemble(ctx, "s", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unlimited.Messages) != 7 {
		t.Fatalf("budget projection mutated stored history: %d", len(unlimited.Messages))
	}
}

func TestWindowedAssembleTruncatesOversizedLatestUserUTF8(t *testing.T) {
	engine := NewWindowedEngine(5)
	_, _ = engine.Ingest(stdctx.Background(), "s", Message{Role: "user", Content: strings.Repeat("🙂", 50), ID: "u"})
	result, err := engine.Assemble(stdctx.Background(), "s", 3)
	if err != nil {
		t.Fatal(err)
	}
	if result.EstimatedTokens > 3 || len(result.Messages) != 1 || result.Messages[0].ID != "u" {
		t.Fatalf("unexpected projection: %#v", result)
	}
}

func TestWindowedAssembleKeepsToolExchangeAtomic(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: strings.Repeat("old", 20), ToolCalls: []ToolCallRef{{ID: "tc", Name: "search"}}},
		{Role: "tool", Content: strings.Repeat("result", 20), ToolCallID: "tc"},
		{Role: "user", Content: "latest"},
	}
	projection := fitWindowProjection(messages, "", 3)
	if len(projection.Messages) != 1 || projection.Messages[0].Role != "user" {
		t.Fatalf("tool exchange was split: %#v", projection.Messages)
	}
}

func TestWindowedAssembleBoundsActiveRecall(t *testing.T) {
	engine := NewWindowedEngine(5)
	recall := &recordingRecall{text: strings.Repeat("remembered context ", 100)}
	engine.SetActiveRecallProvider(recall)
	_, _ = engine.Ingest(stdctx.Background(), "s", Message{Role: "user", Content: "question"})
	result, err := engine.Assemble(stdctx.Background(), "s", 10)
	if err != nil {
		t.Fatal(err)
	}
	if recall.calls != 1 || recall.maxChars <= 0 {
		t.Fatalf("recall was not bounded: calls=%d max=%d", recall.calls, recall.maxChars)
	}
	if result.EstimatedTokens > 10 {
		t.Fatalf("recall overflowed budget: %d", result.EstimatedTokens)
	}
}

func TestWindowedAssembleSkipsRecallWithoutCapacity(t *testing.T) {
	engine := NewWindowedEngine(5)
	recall := &recordingRecall{text: "unused"}
	engine.SetActiveRecallProvider(recall)
	_, _ = engine.Ingest(stdctx.Background(), "s", Message{Role: "user", Content: "x"})
	result, err := engine.Assemble(stdctx.Background(), "s", 1)
	if err != nil {
		t.Fatal(err)
	}
	if recall.calls != 0 || result.EstimatedTokens != 1 {
		t.Fatalf("expected no recall at capacity: calls=%d result=%#v", recall.calls, result)
	}
}
