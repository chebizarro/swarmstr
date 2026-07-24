package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderSpecificOpenAICompatibilityAdapters(t *testing.T) {
	t.Setenv("OPENROUTER_HTTP_REFERER", "https://example.test/app")
	t.Setenv("OPENROUTER_APP_TITLE", "test-app")
	req := httptest.NewRequest(http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", nil)
	if err := openRouterPrepareRequest(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("HTTP-Referer") != "https://example.test/app" || req.Header.Get("X-Title") != "test-app" {
		t.Fatalf("headers = %#v", req.Header)
	}

	desc, ok := DefaultProviderRegistry().Descriptor("groq")
	if !ok {
		t.Fatal("groq descriptor missing")
	}
	provider, err := buildOpenAICompatibleProvider("groq/llama-3.3-70b-versatile", ProviderOverride{APIKey: "key"}, desc)
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.(*OpenAIChatProvider).Model; got != "llama-3.3-70b-versatile" {
		t.Fatalf("normalized model = %q", got)
	}

	if got := openAICompatibleThinkingDelta(`{"reasoning_content":"chain"}`); got != "chain" {
		t.Fatalf("thinking = %q", got)
	}
	usage := normalizeOpenAICompatibleUsage(`{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}`, ProviderUsage{})
	if usage.InputTokens != 11 || usage.OutputTokens != 7 || usage.CacheReadTokens != 3 || usage.CacheCreationTokens != 2 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestAuthenticatedProviderModelDiscovery(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"provider"}]}`))
	}))
	defer server.Close()
	t.Setenv("TEST_PROVIDER_KEY", "secret")
	desc := ProviderDescriptor{ID: "test-provider", Name: "Test Provider", BaseURL: server.URL, APIKeyEnv: "TEST_PROVIDER_KEY", Capabilities: ProviderCapabilities{SupportsStreaming: true}}
	models, err := listAuthenticatedOpenAICompatibleModels(context.Background(), desc)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" || len(models) != 1 || models[0].ID != "model-a" {
		t.Fatalf("auth=%q models=%#v", gotAuth, models)
	}
}

func TestBedrockAndAnthropicVertexDescriptorsAreHonest(t *testing.T) {
	for _, id := range []string{"bedrock", "anthropic-vertex"} {
		desc, ok := DefaultProviderRegistry().Descriptor(id)
		if !ok || !desc.Capabilities.SupportsStreaming {
			t.Fatalf("descriptor %s = %#v ok=%v", id, desc, ok)
		}
	}
	bedrock, matched, err := DefaultProviderRegistry().Build("bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0", ProviderOverride{})
	if err != nil || !matched {
		t.Fatalf("bedrock build matched=%v err=%v", matched, err)
	}
	if _, ok := bedrock.(*BedrockProvider); !ok {
		t.Fatalf("bedrock provider = %T", bedrock)
	}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "project")
	vertex, matched, err := DefaultProviderRegistry().Build("anthropic-vertex/claude-sonnet-4@20250514", ProviderOverride{APIKey: "token"})
	if err != nil || !matched {
		t.Fatalf("vertex build matched=%v err=%v", matched, err)
	}
	if _, ok := vertex.(*AnthropicVertexProvider); !ok {
		t.Fatalf("vertex provider = %T", vertex)
	}
}
