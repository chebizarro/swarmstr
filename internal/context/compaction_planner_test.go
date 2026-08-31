package context

import (
	stdctx "context"
	"strings"
	"testing"
)

func TestSanitizeCompactionMessagesAndToolPairChunks(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "secret runtime context"},
		{Role: "assistant", ID: "a", ToolCalls: []ToolCallRef{{ID: "tc", Name: "read", ArgsJSON: `{"token":"secret"}`}}},
		{Role: "tool", ID: "r", ToolCallID: "tc", Content: "result"},
		{Role: "user", ID: "u", Content: strings.Repeat("x", 200)},
	}
	safe := SanitizeCompactionMessages(messages)
	if len(safe) != 3 || safe[0].ToolCalls[0].ArgsJSON != "" {
		t.Fatalf("sanitized = %+v", safe)
	}
	chunks := BuildSummaryChunks(messages, 10, SessionMemoryCompactConfig{ContextWindowTokens: 1000})
	if len(chunks) < 2 || len(chunks[0]) != 2 || chunks[0][0].ID != "a" || chunks[0][1].ID != "r" {
		t.Fatalf("tool pair split: %+v", chunks)
	}
}

func TestAdaptivePlannerOversizedFallbackAndMultiStage(t *testing.T) {
	sawOversizedNote := false
	messages := []Message{
		{Role: "user", ID: "huge", Content: strings.Repeat("z", 3000)},
		{Role: "user", ID: "one", Content: strings.Repeat("a", 500)},
		{Role: "assistant", ID: "two", Content: strings.Repeat("b", 500)},
		{Role: "user", ID: "three", Content: strings.Repeat("c", 500)},
	}
	calls := 0
	config := SessionMemoryCompactConfig{
		ContextWindowTokens: 1000, OutputReserveTokens: 100, SummarizationOverheadTokens: 100,
		BaseChunkRatio: 0.4, MinChunkRatio: 0.15, SafetyMargin: 1.2, MaxSummaryStages: 3,
		SummaryGenerator: SummaryGeneratorFunc(func(_ stdctx.Context, messages []Message, previous string) (string, error) {
			calls++
			ids := make([]string, 0, len(messages))
			for _, msg := range messages {
				ids = append(ids, msg.ID)
				if msg.ID == "compaction-oversized-notes" {
					sawOversizedNote = true
				}
			}
			return strings.TrimSpace(previous + " " + strings.Join(ids, ",")), nil
		}),
	}
	result, err := GenerateCompactionSummary(stdctx.Background(), messages, "prior", config)
	if err != nil {
		t.Fatal(err)
	}
	if result.OversizedMessages != 1 || result.Chunks < 2 || result.Stages < 2 || calls < 3 {
		t.Fatalf("planning = %+v calls=%d", result, calls)
	}
	if !sawOversizedNote {
		t.Fatalf("oversized placeholder was not supplied to generator: %+v", result)
	}
}

func TestAdaptivePlannerBoundsOverflowRetries(t *testing.T) {
	calls := 0
	config := SessionMemoryCompactConfig{
		ContextWindowTokens: 2000, OutputReserveTokens: 100, SummarizationOverheadTokens: 100,
		BaseChunkRatio: 0.9, MinChunkRatio: 0.15, MaxOverflowRetries: 1, MaxSummaryStages: 2,
		SummaryGenerator: SummaryGeneratorFunc(func(_ stdctx.Context, messages []Message, _ string) (string, error) {
			calls++
			if len(messages) > 1 {
				return "", ErrCompactionOverflow
			}
			return messages[0].ID, nil
		}),
	}
	messages := []Message{{Role: "user", ID: "a", Content: strings.Repeat("a", 1200)}, {Role: "user", ID: "b", Content: strings.Repeat("b", 1200)}}
	result, err := GenerateCompactionSummary(stdctx.Background(), messages, "", config)
	if err != nil {
		t.Fatal(err)
	}
	if calls > 5 || result.OverflowRetries == 0 || !strings.Contains(result.Summary, "a") || !strings.Contains(result.Summary, "b") {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}
