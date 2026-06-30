package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sessioncheckpoint "metiq/internal/session/checkpoint"
)

func TestOpenAIChatProvider_ResponseFormatJSONSchemaRequest(t *testing.T) {
	var captured map[string]any
	client := &http.Client{Transport: openAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))}, nil
	})}
	provider := &OpenAIChatProviderChat{BaseURL: "https://api.openai.com/v1", APIKey: "test", Model: "gpt-4o", Client: client}
	_, err := provider.Chat(context.Background(), []LLMMessage{{Role: "user", Content: "json"}}, nil, ChatOptions{ResponseFormat: &ResponseFormatConfig{
		Type:   ResponseFormatJSONSchema,
		Name:   "answer",
		Schema: map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}},
		Strict: true,
	}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	format, ok := captured["response_format"].(map[string]any)
	if !ok || format["type"] != "json_schema" {
		t.Fatalf("response_format = %#v", captured["response_format"])
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok || jsonSchema["name"] != "answer" || jsonSchema["strict"] != true {
		t.Fatalf("json_schema = %#v", format["json_schema"])
	}
}

func TestStructuredOutputHelpers_AnthropicAndGemini(t *testing.T) {
	format := &ResponseFormatConfig{Type: ResponseFormatJSONSchema, Schema: map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}}}
	tool := anthropicStructuredOutputTool(format)
	if tool.Name != anthropicStructuredOutputToolName || tool.InputJSONSchema["type"] != "object" {
		t.Fatalf("anthropic structured tool = %#v", tool)
	}
	cfg := geminiResponseFormatGenerationConfig(format)
	if cfg["responseMimeType"] != "application/json" || cfg["responseSchema"] == nil {
		t.Fatalf("gemini generation config = %#v", cfg)
	}
}

func TestToolSchemaNormalization_GeminiAndXAI(t *testing.T) {
	defs := []ToolDefinition{{Name: "search", InputJSONSchema: map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"oneOf":   []any{map[string]any{"type": "object"}},
		"properties": map[string]any{
			"q": map[string]any{"type": "string", "default": "ignored"},
		},
		"required": []any{"q", "missing"},
	}}}
	gemini := NormalizeGeminiToolSchema(defs)[0].InputJSONSchema
	if _, ok := gemini["$schema"]; ok {
		t.Fatal("Gemini schema retained $schema")
	}
	if _, ok := gemini["oneOf"]; ok {
		t.Fatal("Gemini schema retained oneOf")
	}
	q := gemini["properties"].(map[string]any)["q"].(map[string]any)
	if _, ok := q["default"]; ok {
		t.Fatal("Gemini schema retained nested default")
	}
	req := gemini["required"].([]any)
	if len(req) != 1 || req[0] != "q" {
		t.Fatalf("required = %#v", req)
	}
	xai := NormalizeStrictOpenAIToolSchema(defs)[0].InputJSONSchema
	if _, ok := xai["$schema"]; ok {
		t.Fatal("xAI schema retained $schema")
	}
	if _, ok := xai["oneOf"]; !ok {
		t.Fatal("xAI strict normalization should preserve oneOf")
	}
}

type parityFlakyProvider struct {
	calls int
	err   error
}

func (p *parityFlakyProvider) Chat(context.Context, []LLMMessage, []ToolDefinition, ChatOptions) (*LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return nil, p.err
	}
	return &LLMResponse{Content: "primary recovered"}, nil
}

type parityCountingProvider struct{ calls int }

func (p *parityCountingProvider) Chat(context.Context, []LLMMessage, []ToolDefinition, ChatOptions) (*LLMResponse, error) {
	p.calls++
	return &LLMResponse{Content: "backup"}, nil
}

func TestFallbackChain_RetriesProviderBeforeFallback(t *testing.T) {
	primary := &parityFlakyProvider{err: context.DeadlineExceeded}
	backup := &parityCountingProvider{}
	fc := NewFallbackChain([]FallbackCandidate{
		{Name: "primary", Provider: primary, RetryConfig: RetryConfig{MaxRetries: 1, Deadline: time.Second, RetryableErrors: []failoverReason{reasonTimeout}}},
		{Name: "backup", Provider: backup},
	}, nil)
	resp, err := fc.Chat(context.Background(), nil, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "primary recovered" || primary.calls != 2 || backup.calls != 0 {
		t.Fatalf("resp=%q primary=%d backup=%d", resp.Content, primary.calls, backup.calls)
	}
}

func TestProviderRegistry_DynamicModelCatalogAndPricing(t *testing.T) {
	for _, model := range []string{"gpt-4o", "claude-sonnet-4-5", "gemini-2.5-flash"} {
		desc, ok := DefaultProviderRegistry().Match(model)
		if !ok {
			t.Fatalf("no descriptor for %s", model)
		}
		if desc.Capabilities.CostPer1KInput <= 0 || desc.Capabilities.CostPer1KOutput <= 0 || desc.Capabilities.ContextWindowTokens <= 0 {
			t.Fatalf("%s capabilities missing pricing/context: %#v", model, desc.Capabilities)
		}
	}

	calls := 0
	reg := NewProviderRegistry()
	if err := reg.Register(ProviderDescriptor{ID: "dynamic", Name: "Dynamic", Aliases: []string{"dynamic"}, Capabilities: ProviderCapabilities{SupportsTools: true}, Factory: func(string, ProviderOverride) (Provider, error) {
		return providerRegistryTestProvider{}, nil
	}, ListModels: func(context.Context) ([]ModelInfo, error) {
		calls++
		return []ModelInfo{{ID: "dynamic/model"}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	models, err := reg.ListModels(context.Background(), "dynamic")
	if err != nil || len(models) != 1 || models[0].ProviderID != "dynamic" || !models[0].Capabilities.SupportsTools {
		t.Fatalf("models=%#v err=%v", models, err)
	}
	_, _ = reg.ListModels(context.Background(), "dynamic")
	if calls != 1 {
		t.Fatalf("ListModels cache calls=%d, want 1", calls)
	}
}

func TestProviderDescriptorTransportHooks(t *testing.T) {
	base := openAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("X-Test-Hook") != "prepared" {
			return nil, fmt.Errorf("missing prepared header")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`ok`))}, nil
	})
	wrapped := false
	desc := ProviderDescriptor{
		PrepareRequest: func(req *http.Request) error { req.Header.Set("X-Test-Hook", "prepared"); return nil },
		WrapTransport:  func(rt http.RoundTripper) http.RoundTripper { wrapped = true; return rt },
	}
	client := desc.HTTPClient(&http.Client{Transport: base})
	resp, err := client.Get("https://example.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
	if !wrapped {
		t.Fatal("WrapTransport was not invoked")
	}
}

type overflowOnceProvider struct{ calls int }

func (p *overflowOnceProvider) Chat(context.Context, []LLMMessage, []ToolDefinition, ChatOptions) (*LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return nil, fmt.Errorf("context length exceeded")
	}
	return &LLMResponse{Content: "recovered"}, nil
}

func TestRunAgenticLoop_ContextOverflowAutoRecovery(t *testing.T) {
	provider := &overflowOnceProvider{}
	store := sessioncheckpoint.NewStore()
	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{
		Provider:            provider,
		InitialMessages:     []LLMMessage{{Role: "user", Content: strings.Repeat("long ", 2000)}},
		ContextWindowTokens: 256,
		CheckpointStore:     store,
		SessionID:           "sess-overflow",
	})
	if err != nil {
		t.Fatalf("RunAgenticLoop: %v", err)
	}
	if resp.Content != "recovered" || provider.calls != 2 {
		t.Fatalf("resp=%q calls=%d", resp.Content, provider.calls)
	}
	cps := store.List("sess-overflow")
	if len(cps) != 1 || cps[0].Reason != sessioncheckpoint.ReasonOverflowRetry {
		t.Fatalf("checkpoints=%#v", cps)
	}
}

type resumeProvider struct {
	calls    int
	messages [][]LLMMessage
}

func (p *resumeProvider) Chat(_ context.Context, messages []LLMMessage, _ []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	p.calls++
	cp := append([]LLMMessage(nil), messages...)
	p.messages = append(p.messages, cp)
	return &LLMResponse{Content: "done after resume"}, nil
}

type resumeExecutor struct{ calls int }

func (e *resumeExecutor) Execute(context.Context, ToolCall) (string, error) {
	e.calls++
	return "tool result", nil
}

func (e *resumeExecutor) EffectiveTraits(ToolCall) (ToolTraits, bool) {
	return ToolTraits{ReadOnly: true}, true
}

func TestRunAgenticLoop_ResumeCheckpointExecutesPendingToolsWithoutReplay(t *testing.T) {
	messages := []LLMMessage{{Role: "user", Content: "hi"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "lookup", Args: map[string]any{"q": "x"}}}}}
	history := []ConversationMessage{{Role: "assistant", ToolCalls: []ToolCallRef{{ID: "tc1", Name: "lookup", ArgsJSON: `{"q":"x"}`}}}}
	pending := []ToolCall{{ID: "tc1", Name: "lookup", Args: map[string]any{"q": "x"}}}
	usage := ProviderUsage{InputTokens: 3, OutputTokens: 4}
	messagesJSON, _ := json.Marshal(messages)
	historyJSON, _ := json.Marshal(history)
	pendingJSON, _ := json.Marshal(pending)
	usageJSON, _ := json.Marshal(usage)
	cp := &sessioncheckpoint.TurnCheckpoint{SessionID: "sess", TurnID: "turn", Status: "before_tool_execution", Iteration: 1, MessagesJSON: messagesJSON, HistoryDeltaJSON: historyJSON, PendingToolCallsJSON: pendingJSON, UsageJSON: usageJSON}

	provider := &resumeProvider{}
	exec := &resumeExecutor{}
	resp, err := RunAgenticLoop(context.Background(), AgenticLoopConfig{Provider: provider, InitialMessages: []LLMMessage{{Role: "user", Content: "should not replay"}}, Executor: exec, ResumeCheckpoint: cp, ResumeCheckpointSafe: true, SessionID: "sess", TurnID: "turn", MaxIterations: 3})
	if err != nil {
		t.Fatalf("RunAgenticLoop: %v", err)
	}
	if resp.Content != "done after resume" || exec.calls != 1 || provider.calls != 1 {
		t.Fatalf("resp=%q exec=%d provider=%d", resp.Content, exec.calls, provider.calls)
	}
	seenToolResult := false
	for _, msg := range provider.messages[0] {
		if msg.Role == "tool" && msg.ToolCallID == "tc1" && msg.Content == "tool result" {
			seenToolResult = true
		}
		if strings.Contains(msg.Content, "should not replay") {
			t.Fatal("initial messages were replayed instead of resume checkpoint")
		}
	}
	if !seenToolResult {
		t.Fatalf("provider messages missing resumed tool result: %#v", provider.messages[0])
	}
}

func TestAnthropicStreamEmitsThinkingDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	provider := NewAnthropicChatProvider("test-key", WithBaseURL(srv.URL), WithModel("claude-sonnet-4-5"))
	var events []RuntimeEvent
	var chunks []string
	res, err := provider.StreamMessages(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}}, nil, ChatOptions{ThinkingBudget: 100, MaxTokens: 200}, "sess", "turn", func(evt RuntimeEvent) {
		events = append(events, evt)
	}, func(text string) { chunks = append(chunks, text) })
	if err != nil {
		t.Fatalf("StreamMessages: %v", err)
	}
	if res.Text != "hi" || len(chunks) != 1 || chunks[0] != "hi" {
		t.Fatalf("res=%q chunks=%#v", res.Text, chunks)
	}
	if len(events) != 2 || events[0].Type != RuntimeEventThinkingDelta || events[0].Delta != "plan" || events[1].Type != RuntimeEventUsage || events[1].Usage.OutputTokens != 1 {
		t.Fatalf("thinking events=%#v", events)
	}
}
