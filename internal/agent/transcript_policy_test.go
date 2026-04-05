package agent

import (
	"strings"
	"testing"
)

func TestResolveOpenAITranscriptPolicy_UsesStrict9ForMistral(t *testing.T) {
	policy := ResolveOpenAITranscriptPolicy("mistral-large-latest", "https://api.mistral.ai/v1")
	if !policy.SanitizeToolCallIDs || policy.ToolCallIDMode != ToolCallIDModeStrict9 {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	if policy.AllowSyntheticResults {
		t.Fatalf("openai-compatible policy should not synthesize tool results")
	}
}

func TestResolveAnthropicTranscriptPolicy_AllowsSyntheticRepair(t *testing.T) {
	policy := ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5")
	if !policy.RepairToolUseResultPair || !policy.AllowSyntheticResults {
		t.Fatalf("unexpected anthropic policy: %+v", policy)
	}
}

func TestPrepareTranscriptMessages_MovesResultsAndSynthesizesForAnthropic(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call:1", Name: "read_file"}, {ID: "call:2", Name: "bash_exec"}}},
		{Role: "user", Content: "interleaved user"},
		{Role: "tool", ToolCallID: "call:1", Content: "first result"},
		{Role: "tool", ToolCallID: "orphan", Content: "drop me"},
	}

	prepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	if len(prepared) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(prepared), prepared)
	}
	if prepared[1].Role != "assistant" || len(prepared[1].ToolCalls) != 2 {
		t.Fatalf("assistant/tool calls lost: %+v", prepared[1])
	}
	if prepared[1].ToolCalls[0].ID != "call1" || prepared[1].ToolCalls[1].ID != "call2" {
		t.Fatalf("expected strict sanitized IDs, got %+v", prepared[1].ToolCalls)
	}
	if prepared[2].Role != "tool" || prepared[2].ToolCallID != "call1" || prepared[2].Content != "first result" {
		t.Fatalf("expected moved matching tool result, got %+v", prepared[2])
	}
	if prepared[3].Role != "tool" || prepared[3].ToolCallID != "call2" || !strings.Contains(prepared[3].Content, "previous turn ended") {
		t.Fatalf("expected synthetic missing tool result, got %+v", prepared[3])
	}
	if prepared[4].Role != "user" || prepared[4].Content != "interleaved user" {
		t.Fatalf("expected remainder after repaired tool span, got %+v", prepared[4])
	}
}

func TestPrepareTranscriptMessages_DoesNotSynthesizeForOpenAI(t *testing.T) {
	messages := []LLMMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call:1", Name: "read_file"}}}}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("gpt-4o", "https://api.openai.com/v1"))
	if len(prepared) != 0 {
		t.Fatalf("expected dangling openai tool call to be dropped, got %+v", prepared)
	}
}

func TestPrepareTranscriptMessages_Strict9SanitizesAndDeduplicates(t *testing.T) {
	messages := []LLMMessage{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "mistral/tool:one", Name: "read_file"},
				{ID: "mistral-tool-one", Name: "bash_exec"},
			},
		},
		{Role: "tool", ToolCallID: "mistral/tool:one", Content: "one"},
		{Role: "tool", ToolCallID: "mistral-tool-one", Content: "two"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("mistral-large-latest", "https://api.mistral.ai/v1"))
	if len(prepared) != 3 {
		t.Fatalf("expected repaired mistral transcript, got %+v", prepared)
	}
	ids := []string{prepared[0].ToolCalls[0].ID, prepared[0].ToolCalls[1].ID}
	for _, id := range ids {
		if len(id) != 9 {
			t.Fatalf("expected strict9 id, got %q", id)
		}
		for _, r := range id {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				t.Fatalf("expected alphanumeric strict9 id, got %q", id)
			}
		}
	}
	if ids[0] == ids[1] {
		t.Fatalf("expected collision-resistant strict9 ids, got %+v", ids)
	}
}

func TestPrepareTranscriptMessages_AllowsSameToolIDAcrossSeparateTurns(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "search", Name: "search"}}},
		{Role: "tool", ToolCallID: "search", Content: "first"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "search", Name: "search"}}},
		{Role: "tool", ToolCallID: "search", Content: "second"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveGeminiTranscriptPolicy("gemini-2.0-flash"))
	if len(prepared) != 4 {
		t.Fatalf("expected both tool spans to survive repeated ids, got %+v", prepared)
	}
	if prepared[1].Content != "first" || prepared[3].Content != "second" {
		t.Fatalf("unexpected repeated-id repair result: %+v", prepared)
	}
}

func TestPrepareTranscriptMessages_PreservesDottedAndSlashedToolNames(t *testing.T) {
	messages := []LLMMessage{{
		Role:      "assistant",
		ToolCalls: []ToolCall{{ID: "call:1", Name: "memory.search"}, {ID: "call:2", Name: "plugin/raw"}},
	}}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("gpt-4o", "https://api.openai.com/v1"))
	if len(prepared) != 0 {
		t.Fatalf("expected openai policy to drop dangling tool span but preserve valid names during normalization, got %+v", prepared)
	}
	anthropicPrepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	if len(anthropicPrepared) != 3 {
		t.Fatalf("expected valid dotted/slashed tools to survive anthropic repair, got %+v", anthropicPrepared)
	}
	if anthropicPrepared[0].ToolCalls[0].Name != "memory.search" || anthropicPrepared[0].ToolCalls[1].Name != "plugin/raw" {
		t.Fatalf("expected dotted/slashed tool names to survive, got %+v", anthropicPrepared[0].ToolCalls)
	}
}

func TestPrepareTranscriptMessages_DropsInvalidToolCalls(t *testing.T) {
	messages := []LLMMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "ok", Name: "bad name"}}}}
	prepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	if len(prepared) != 0 {
		t.Fatalf("expected invalid assistant tool call message to be dropped, got %+v", prepared)
	}
}
