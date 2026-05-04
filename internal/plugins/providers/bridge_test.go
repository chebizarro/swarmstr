package providers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/plugins/registry"
)

type fakeHost struct {
	calls   []hostCall
	results map[string]any
	errs    map[string]error
}

type hostCall struct {
	ProviderID string
	Method     string
	Params     any
}

func (f *fakeHost) InvokeProvider(_ context.Context, providerID, method string, params any) (any, error) {
	f.calls = append(f.calls, hostCall{providerID, method, params})
	if err := f.errs[method]; err != nil {
		return nil, err
	}
	return f.results[method], nil
}

func TestPluginProviderBridgeChatTranslatesRequestAndResponse(t *testing.T) {
	h := &fakeHost{results: map[string]any{"chat": map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "thinking "},
			map[string]any{"type": "tool_use", "id": "tc1", "name": "lookup", "input": map[string]any{"q": "nostr"}},
		},
		"usage":       map[string]any{"input_tokens": float64(12), "output_tokens": float64(5), "cache_read_input_tokens": float64(3)},
		"stop_reason": "tool_use",
	}}}
	b := NewPluginProviderBridge("openai", "plugin-openai", h, nil, WithModel("openai/gpt-4o"), WithAPIKeys(map[string]string{"openai": "sk-test"}), WithEnv(map[string]string{}))
	resp, err := b.Chat(context.Background(), []agent.LLMMessage{
		{Role: "user", Content: "hi", Images: []agent.ImageRef{{URL: "https://example.com/cat.png"}}},
	}, []agent.ToolDefinition{{Name: "lookup", Description: "search", Parameters: agent.ToolParameters{Type: "object"}}}, agent.ChatOptions{MaxTokens: 100, CacheSystem: true})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "thinking" || !resp.NeedsToolResults || len(resp.ToolCalls) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.ToolCalls[0].Name != "lookup" || resp.ToolCalls[0].Args["q"] != "nostr" {
		t.Fatalf("bad tool call: %+v", resp.ToolCalls[0])
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 5 || resp.Usage.CacheReadTokens != 3 {
		t.Fatalf("bad usage: %+v", resp.Usage)
	}
	params := h.calls[0].Params.(map[string]any)
	if params["model"] != "openai/gpt-4o" {
		t.Fatalf("model not passed: %#v", params["model"])
	}
	if len(params["messages"].([]map[string]any)) != 1 || len(params["tools"].([]map[string]any)) != 1 {
		t.Fatalf("messages/tools not translated: %#v", params)
	}
}

func TestParseCatalogResultAcceptsDirectModelsAndStrings(t *testing.T) {
	entries, err := ParseCatalogResult("prov", map[string]any{"api": "openai-completions", "models": []any{"m1", map[string]any{"id": "m2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != "m1" || entries[1].ID != "m2" {
		t.Fatalf("bad direct entries: %+v", entries)
	}
}

func TestParseCatalogResultFlattensProviders(t *testing.T) {
	entries, err := ParseCatalogResult("acme", map[string]any{
		"provider": map[string]any{
			"api": "openai-completions", "baseUrl": "https://api.acme.test/v1", "apiKey": "secret",
			"models": []any{map[string]any{"id": "large", "name": "Large", "contextWindow": float64(128000), "input": []any{"text", "image"}}},
		},
		"providers": map[string]any{"other": map[string]any{"api": "anthropic-messages", "models": []any{map[string]any{"id": "claude"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d %#v", len(entries), entries)
	}
	if entries[0].ProviderID != "acme" || entries[0].BaseURL != "https://api.acme.test/v1" || entries[0].ContextWindow != 128000 || entries[0].Raw["apiKey"] != "secret" {
		t.Fatalf("bad first entry: %+v", entries[0])
	}
	if entries[1].ProviderID != "other" || entries[1].API != "anthropic-messages" {
		t.Fatalf("bad second entry: %+v", entries[1])
	}
}

func TestPluginProviderBridgeAuth(t *testing.T) {
	h := &fakeHost{results: map[string]any{"auth": map[string]any{"ok": true, "config": map[string]any{"model": "x"}}}}
	b := NewPluginProviderBridge("acme", "plugin", h, nil, WithEnv(map[string]string{}))
	res, err := b.Auth(context.Background(), "api-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Config["model"] != "x" {
		t.Fatalf("bad auth result: %+v", res)
	}
	params := h.calls[0].Params.(map[string]any)
	if params["auth_id"] != "api-key" {
		t.Fatalf("bad auth params: %#v", params)
	}
}

func TestPluginProviderBridgeEffectiveAPIKeysDoesNotPanicWithEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	b := NewPluginProviderBridge("openai", "plugin", &fakeHost{}, nil)
	keys := b.effectiveAPIKeys()
	if keys["openai"] != "sk-env" {
		t.Fatalf("expected env key, got %#v", keys)
	}
}

func TestPluginProviderBridgeStaticCatalogFallback(t *testing.T) {
	h := &fakeHost{results: map[string]any{"staticCatalog": map[string]any{"models": []any{"m1"}}}, errs: map[string]error{"catalog": errors.New("unknown provider method: catalog")}}
	b := NewPluginProviderBridge("acme", "plugin", h, nil, WithEnv(map[string]string{}), WithAPIKeys(map[string]string{}))
	entries, err := b.RefreshCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "m1" || len(h.calls) != 2 || h.calls[1].Method != "staticCatalog" {
		t.Fatalf("bad static catalog fallback: entries=%+v calls=%+v", entries, h.calls)
	}
}

func TestStreamChatEmitsChunks(t *testing.T) {
	h := &fakeHost{results: map[string]any{"stream": []any{map[string]any{"delta": "hel"}, map[string]any{"delta": "lo"}}}}
	b := NewPluginProviderBridge("acme", "plugin", h, nil, WithEnv(map[string]string{}))
	var chunks []string
	resp, err := b.StreamChat(context.Background(), []agent.LLMMessage{{Role: "user", Content: "hi"}}, nil, agent.ChatOptions{}, func(s string) { chunks = append(chunks, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" || !reflect.DeepEqual(chunks, []string{"hel", "lo"}) {
		t.Fatalf("resp=%+v chunks=%v", resp, chunks)
	}
}

type fallbackProvider struct{}

func (fallbackProvider) Chat(context.Context, []agent.LLMMessage, []agent.ToolDefinition, agent.ChatOptions) (*agent.LLMResponse, error) {
	return &agent.LLMResponse{Content: "fallback"}, nil
}

func TestPluginProviderBridgeFallback(t *testing.T) {
	h := &fakeHost{errs: map[string]error{"chat": errors.New("boom"), "catalog": errors.New("unknown provider method: catalog")}}
	b := NewPluginProviderBridge("acme", "plugin", h, nil, WithFallbacks(fallbackProvider{}), WithMethods("chat"), WithEnv(map[string]string{}))
	resp, err := b.Chat(context.Background(), []agent.LLMMessage{{Role: "user", Content: "hi"}}, nil, agent.ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "fallback" {
		t.Fatalf("expected fallback, got %+v", resp)
	}
}

func TestPluginProviderBridgeMetadataCatalogAndStreamFallback(t *testing.T) {
	h := &fakeHost{results: map[string]any{
		"catalog": map[string]any{"models": []any{map[string]any{"id": "m1", "name": "M1", "metadata": map[string]any{"tier": "test"}}}},
		"chat":    map[string]any{"content": "chat fallback"},
	}, errs: map[string]error{"stream": errors.New("unknown provider method: stream")}}
	b := NewPluginProviderBridge("acme", "plugin", h, nil, WithConfig(map[string]any{"region": "local"}), WithEnv(map[string]string{"ACME_API_KEY": "env-key"}), WithStreamMethods("stream"))
	if b.ProviderID() != "acme" || b.PluginID() != "plugin" {
		t.Fatalf("bad ids")
	}
	if env := b.effectiveEnv(); env["ACME_API_KEY"] != "env-key" {
		t.Fatalf("effective env mismatch: %+v", env)
	}
	entries, err := b.RefreshCatalog(context.Background())
	if err != nil || len(entries) != 1 || entries[0].ID != "m1" {
		t.Fatalf("catalog entries=%+v err=%v", entries, err)
	}
	catalog, err := b.Catalog(context.Background())
	if err != nil || len(catalog) != 1 {
		t.Fatalf("catalog cache empty: %+v err=%v", catalog, err)
	}
	catalog[0].ID = "mutated"
	again, err := b.Catalog(context.Background())
	if err != nil || again[0].ID != "m1" {
		t.Fatalf("catalog cache was mutable: %+v err=%v", again, err)
	}
	var chunks []string
	resp, err := b.StreamChat(context.Background(), []agent.LLMMessage{{Role: "user", Content: "hi"}}, nil, agent.ChatOptions{}, func(s string) { chunks = append(chunks, s) })
	if err != nil || resp.Content != "chat fallback" || !reflect.DeepEqual(chunks, []string{"chat fallback"}) {
		t.Fatalf("stream fallback resp=%+v chunks=%+v err=%v", resp, chunks, err)
	}
}

func TestProviderTranslationVariants(t *testing.T) {
	messages := TranslateMessagesToOpenClaw([]agent.LLMMessage{
		{Role: "assistant", Content: "hi", ToolCalls: []agent.ToolCall{{ID: "tc", Name: "lookup", Args: map[string]any{"q": "x"}}}},
		{Role: "tool", Content: "result", ToolCallID: "tc"},
	})
	if len(messages) != 2 || messages[0]["role"] != "assistant" || messages[1]["tool_call_id"] != "tc" {
		t.Fatalf("messages not translated: %+v", messages)
	}
	tools := TranslateToolsToOpenClaw([]agent.ToolDefinition{{Name: "lookup", Parameters: agent.ToolParameters{Properties: map[string]agent.ToolParamProp{"q": {Type: "string"}}}}})
	if tools[0]["parameters"].(map[string]any)["type"] != "object" {
		t.Fatalf("tool parameters default missing: %+v", tools)
	}
	resp, err := TranslateResponseFromOpenClaw(map[string]any{
		"text":       "hello",
		"tool_calls": []any{map[string]any{"id": "1", "function": map[string]any{"name": "lookup", "arguments": `{"q":"x"}`}}},
		"usage":      map[string]any{"input_tokens": 1, "output_tokens": 2},
	})
	if err != nil || resp.Content != "hello" || len(resp.ToolCalls) != 1 || resp.Usage.InputTokens != 1 {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	streamResp, chunks, err := translateStreamResult([]any{"a", map[string]any{"delta": "b"}})
	if err != nil || streamResp.Content != "ab" || !reflect.DeepEqual(chunks, []string{"a", "b"}) {
		t.Fatalf("streamResp=%+v chunks=%+v err=%v", streamResp, chunks, err)
	}
}

func TestPluginProviderBridgeEnvMetadataAndAuthScalar(t *testing.T) {
	t.Setenv("ACME_TOKEN", "token-one")
	t.Setenv("ACME_TOKEN_TWO", "token-two")
	meta := &registry.RegisteredProvider{ID: "acme", PluginID: "plugin-meta", Raw: map[string]any{
		"envVar":              "ACME_TOKEN",
		"envVars":             []any{"ACME_TOKEN_TWO", ""},
		"providerAuthEnvVars": "ACME_TOKEN_THREE",
		"auth":                []any{map[string]any{"envVar": "ACME_TOKEN_FOUR"}},
	}}
	h := &fakeHost{results: map[string]any{"auth": "ok"}}
	b := NewPluginProviderBridge("", "", h, meta)
	if b.ProviderID() != "acme" || b.PluginID() != "plugin-meta" {
		t.Fatalf("metadata ids not applied")
	}
	if env := b.effectiveEnv(); env["ACME_TOKEN"] != "token-one" {
		t.Fatalf("process env not included: %+v", env)
	}
	if keys := b.effectiveAPIKeys(); keys["acme"] != "token-one" {
		t.Fatalf("metadata API key not resolved: %+v", keys)
	}
	res, err := b.Auth(context.Background(), "scalar", map[string]any{"existing": true})
	if err != nil || !res.OK || res.Raw["value"] != "ok" {
		t.Fatalf("scalar auth result=%+v err=%v", res, err)
	}
	params := h.calls[0].Params.(map[string]any)
	params["env"].(map[string]string)["ACME_TOKEN"] = "mutated"
	if b.effectiveEnv()["ACME_TOKEN"] != "token-one" {
		t.Fatal("effective env returned mutable state")
	}
}

func TestProviderBridgeAdditionalTranslationBranches(t *testing.T) {
	resp, err := TranslateResponseFromOpenClaw(`{"choices":[{"message":{"content":[{"type":"message","content":"hi"},{"kind":"function_call","name":"tool","arguments":{"x":1}}]},"finish_reason":"stop"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4}}`)
	if err != nil || resp.Content != "hi" || !resp.NeedsToolResults || len(resp.ToolCalls) != 1 || resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("translated response=%+v err=%v", resp, err)
	}
	if _, err := TranslateResponseFromOpenClaw(123); err == nil {
		t.Fatal("expected unexpected response type error")
	}
	streamResp, chunks, err := translateStreamResult(map[string]any{"content": []any{map[string]any{"type": "text", "text": "whole"}}})
	if err != nil || streamResp.Content != "whole" || !reflect.DeepEqual(chunks, []string{"whole"}) {
		t.Fatalf("stream map response=%+v chunks=%+v err=%v", streamResp, chunks, err)
	}
	entries, err := ParseCatalogResult("json-provider", `{"models":[{"id":"json-model","max_tokens":2048,"reasoning":"true","cost":{"in":1}}]}`)
	if err != nil || len(entries) != 1 || entries[0].MaxTokens != 2048 || !entries[0].Reasoning || entries[0].Cost["in"] == nil {
		t.Fatalf("json catalog entries=%+v err=%v", entries, err)
	}
	if int64Value(json.Number("42")) != 42 || !boolValue("1") || len(stringSliceFromAny("single")) != 1 || asMap(map[string]string{"a": "b"})["a"] != "b" {
		t.Fatal("conversion branches failed")
	}
}

func TestProviderNativeSelectionAndGenerateErrorPath(t *testing.T) {
	entries := []ModelEntry{{ProviderID: "acme", ID: "m1"}, {ProviderID: "other", ID: "m2"}}
	if entry, ok := selectModelEntry("acme", "acme/m1", entries); !ok || entry.ID != "m1" {
		t.Fatalf("select namespaced model failed: %+v %v", entry, ok)
	}
	if _, ok := selectModelEntry("acme", "missing", entries); ok {
		t.Fatal("unexpected missing model match")
	}
	for _, entry := range []ModelEntry{
		{ProviderID: "openai", ID: "gpt", API: "openai-compatible", Raw: map[string]any{"apiKey": "sk"}},
		{ProviderID: "anthropic", ID: "claude", API: "anthropic-messages"},
		{ProviderID: "google", ID: "gemini", API: "google"},
	} {
		if native, err := nativeProviderForEntry(entry, map[string]string{entry.ProviderID: "env"}); err != nil || native == nil {
			t.Fatalf("native provider for %+v = %T %v", entry, native, err)
		}
	}
	if _, err := nativeProviderForEntry(ModelEntry{ProviderID: "x", ID: "m", API: "unsupported"}, nil); err == nil {
		t.Fatal("expected unsupported native provider error")
	}
	b := NewPluginProviderBridge("acme", "plugin", nil, nil, WithModel("m"))
	if _, err := b.Generate(context.Background(), agent.Turn{UserText: "hi"}); err == nil {
		t.Fatal("expected generate error with nil host")
	}
	if nonEmpty("", "a", " ")[0] != "a" || int64Value("bad") != 0 || len(stringSliceFromAny([]any{"a", 1, "b"})) != 2 {
		t.Fatal("helper branch mismatch")
	}
}
