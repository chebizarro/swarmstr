package agent

import "testing"

func TestGenericChatCompletionUsesProviderUsage(t *testing.T) {
	response, err := parseGenericChatCompletion([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":123,"completion_tokens":45,"prompt_tokens_details":{"cached_tokens":67}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage.InputTokens != 123 || response.Usage.OutputTokens != 45 || response.Usage.CacheReadTokens != 67 {
		t.Fatalf("provider usage not parsed: %+v", response.Usage)
	}
}

func TestCatalogAliasResolution(t *testing.T) {
	row, ok := resolveCatalogModelRef("moonshot/kimi-k2")
	if !ok || row.ProviderID != "moonshot" || row.ID != "kimi-k2-0711-preview" {
		t.Fatalf("bad row: %+v ok=%v", row, ok)
	}
}

func TestDefaultRegistryNewProviders(t *testing.T) {
	reg := NewProviderRegistry()
	for _, d := range builtinProviderDescriptors() {
		if err := reg.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"mistral", "moonshot", "openai-responses", "azure-responses", "vertex", "minimax", "minimax-cn", "deepseek", "zai", "qwen", "cerebras", "cohere", "vercel-ai-gateway"} {
		if _, ok := reg.Descriptor(id); !ok {
			t.Fatalf("missing descriptor %s", id)
		}
	}
	if desc, ok := reg.Match("responses/gpt-4.1"); !ok || desc.ID != "openai-responses" {
		t.Fatalf("bad responses match: %+v ok=%v", desc, ok)
	}
}

func TestPerModelCapabilitiesAreAuthoritative(t *testing.T) {
	reg := NewProviderRegistry()
	for _, descriptor := range builtinProviderDescriptors() {
		if err := reg.Register(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		ref, provider, transport string
		window                   int
		vision, thinking         bool
	}{
		{"responses/gpt-4.1", "openai-responses", "responses-auto", 1047576, true, true},
		{"deepseek/deepseek-chat", "deepseek", "openai-chat-sse", 128000, false, false},
		{"qwen/qwen3.7-plus", "qwen", "openai-chat-sse", 262144, true, true},
		{"vercel-ai-gateway/anthropic/claude-opus-4.6", "vercel-ai-gateway", "openai-chat-sse", 200000, true, true},
	}
	azure, azureOK := reg.ModelInfo("azure/gpt-4.1")
	if !azureOK || azure.Capabilities.SupportsWebSocket || !azure.Capabilities.SupportsContinuation || azure.Capabilities.Transport != "responses-sse" {
		t.Fatalf("Azure Responses capabilities must remain SSE continuation only: %+v ok=%v", azure.Capabilities, azureOK)
	}
	for _, tc := range cases {
		info, ok := reg.ModelInfo(tc.ref)
		if tc.provider == "openai-responses" && (!info.Capabilities.SupportsWebSocket || !info.Capabilities.SupportsContinuation) {
			t.Errorf("%s: Responses transport capabilities=%+v", tc.ref, info.Capabilities)
		}
		if !ok || info.ProviderID != tc.provider || info.Capabilities.ContextWindowTokens != tc.window || info.Capabilities.Transport != tc.transport || info.Capabilities.SupportsVision != tc.vision || info.Capabilities.SupportsThinking != tc.thinking {
			t.Errorf("%s: info=%+v ok=%v", tc.ref, info, ok)
		}
	}
}

func TestNewProviderDescriptorRoutes(t *testing.T) {
	reg := newDefaultProviderRegistry()
	cases := map[string]string{
		"deepseek/deepseek-v4-flash":                  "deepseek",
		"zai/glm-5.2":                                 "zai",
		"qwen/qwen3.7-plus":                           "qwen",
		"cerebras/gpt-oss-120b":                       "cerebras",
		"cohere/command-a-plus-05-2026":               "cohere",
		"vercel-ai-gateway/anthropic/claude-opus-4.6": "vercel-ai-gateway",
	}
	for ref, want := range cases {
		desc, ok := reg.Match(ref)
		if !ok || desc.ID != want {
			t.Errorf("%s matched %+v ok=%v", ref, desc, ok)
			continue
		}
		if got := normalizeProviderModelID(ref, desc); got == ref || got == "" {
			t.Errorf("%s was not normalized: %q", ref, got)
		}
	}
}
