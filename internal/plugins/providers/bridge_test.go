package providers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"metiq/internal/agent"
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
