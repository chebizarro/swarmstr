package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── stubRuntime ──────────────────────────────────────────────────────────────

type stubRuntime struct{ id string }

var providerCredentialEnvKeys = []string{
	"METIQ_AGENT_PROVIDER",
	"METIQ_AGENT_MODEL",
	"METIQ_AGENT_ALLOW_ECHO",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_OAUTH_TOKEN",
	"OPENAI_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"GOOGLE_GENERATIVE_AI_API_KEY",
	"COHERE_API_KEY",
	"XAI_API_KEY",
	"GROQ_API_KEY",
	"MISTRAL_API_KEY",
	"TOGETHER_API_KEY",
	"OPENROUTER_API_KEY",
	"FIREWORKS_API_KEY",
	"DEEPINFRA_API_KEY",
	"PERPLEXITY_API_KEY",
	"METIQ_AGENT_HTTP_URL",
	"METIQ_AGENT_HTTP_API_KEY",
	"OLLAMA_API_KEY",
	"OLLAMA_BASE_URL",
	"LMSTUDIO_BASE_URL",
}

func clearProviderCredentialEnv(t *testing.T) {
	t.Helper()
	for _, key := range providerCredentialEnvKeys {
		t.Setenv(key, "")
	}
}

type providerRegistryTestProvider struct{}

func (providerRegistryTestProvider) Generate(context.Context, Turn) (ProviderResult, error) {
	return ProviderResult{Text: "ok"}, nil
}

func (s *stubRuntime) ProcessTurn(_ context.Context, turn Turn) (TurnResult, error) {
	return TurnResult{Text: s.id + ":" + turn.UserText}, nil
}

// ─── ProviderRegistry ────────────────────────────────────────────────────────

func TestProviderRegistry_RegisterMatchBuild(t *testing.T) {
	reg := NewProviderRegistry()
	err := reg.Register(ProviderDescriptor{
		ID:       "custom",
		Name:     "Custom Provider",
		Aliases:  []string{"custom"},
		Prefixes: []string{"custom/"},
		AuthMethods: []AuthMethod{
			AuthMethodAPIKey,
		},
		Capabilities: ProviderCapabilities{SupportsTools: true, SupportsStreaming: true},
		Factory: func(model string, override ProviderOverride) (Provider, error) {
			if model != "custom/model" {
				t.Fatalf("factory got model %q", model)
			}
			if override.APIKey != "secret" {
				t.Fatalf("factory got api key %q", override.APIKey)
			}
			return providerRegistryTestProvider{}, nil
		},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	desc, ok := reg.Match("custom/model")
	if !ok {
		t.Fatal("expected prefix match")
	}
	if desc.ID != "custom" || !desc.Capabilities.SupportsTools || !desc.Capabilities.SupportsStreaming {
		t.Fatalf("unexpected descriptor: %#v", desc)
	}
	provider, matched, err := reg.Build("custom/model", ProviderOverride{APIKey: "secret"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !matched {
		t.Fatal("expected registry build to match")
	}
	if _, ok := provider.(providerRegistryTestProvider); !ok {
		t.Fatalf("expected test provider, got %T", provider)
	}
}

func TestDefaultProviderRegistry_OpenAICompatibleDescriptor(t *testing.T) {
	desc, ok := DefaultProviderRegistry().Match("groq/llama-3.1-70b-versatile")
	if !ok {
		t.Fatal("expected groq descriptor match")
	}
	if desc.ID != "groq" {
		t.Fatalf("expected groq descriptor, got %q", desc.ID)
	}
	if desc.APIKeyEnv != "GROQ_API_KEY" {
		t.Fatalf("expected GROQ_API_KEY, got %q", desc.APIKeyEnv)
	}
	if !desc.Capabilities.SupportsTools || !desc.Capabilities.SupportsStreaming || !desc.Capabilities.SupportsPromptCaching {
		t.Fatalf("expected OpenAI-compatible capabilities, got %#v", desc.Capabilities)
	}
}

func TestDefaultProviderRegistry_BaseURLEnvOverride(t *testing.T) {
	t.Setenv("OLLAMA_BASE_URL", "http://127.0.0.1:11435/v1/")
	baseURL, envKey := resolveOpenAICompat("ollama/llama3")
	if baseURL != "http://127.0.0.1:11435/v1" {
		t.Fatalf("expected env base URL override, got %q", baseURL)
	}
	if envKey != "OLLAMA_API_KEY" {
		t.Fatalf("expected OLLAMA_API_KEY, got %q", envKey)
	}
}

// ─── AgentRuntimeRegistry ────────────────────────────────────────────────────

func TestAgentRuntimeRegistry_defaultFallback(t *testing.T) {
	def := &stubRuntime{id: "default"}
	reg := NewAgentRuntimeRegistry(def)

	got := reg.Get("")
	if got != def {
		t.Error("empty agentID should return default runtime")
	}
	got = reg.Get("main")
	if got != def {
		t.Error("\"main\" should return default runtime")
	}
	got = reg.Get("unknown")
	if got != def {
		t.Error("unregistered ID should fall back to default")
	}
}

func TestAgentRuntimeRegistry_setAndGet(t *testing.T) {
	def := &stubRuntime{id: "default"}
	sub := &stubRuntime{id: "sub"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("alpha", sub)
	if reg.Get("alpha") != sub {
		t.Error("expected sub runtime for alpha")
	}
	if reg.Get("main") != def {
		t.Error("main should still return default")
	}
}

func TestAgentRuntimeRegistry_remove(t *testing.T) {
	def := &stubRuntime{id: "default"}
	sub := &stubRuntime{id: "sub"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("alpha", sub)
	reg.Remove("alpha")
	if reg.Get("alpha") != def {
		t.Error("after remove, alpha should fall back to default")
	}
}

func TestAgentRuntimeRegistry_nilRemovesEntry(t *testing.T) {
	def := &stubRuntime{id: "default"}
	sub := &stubRuntime{id: "sub"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("alpha", sub)
	reg.Set("alpha", nil) // nil removes
	if reg.Get("alpha") != def {
		t.Error("setting nil should remove entry")
	}
}

func TestAgentRuntimeRegistry_registered(t *testing.T) {
	def := &stubRuntime{id: "default"}
	reg := NewAgentRuntimeRegistry(def)

	reg.Set("a", &stubRuntime{})
	reg.Set("b", &stubRuntime{})
	reg.Set("a", nil) // remove a

	ids := reg.Registered()
	if len(ids) != 1 || ids[0] != "b" {
		t.Errorf("expected [b], got %v", ids)
	}
}

// ─── AgentSessionRouter ───────────────────────────────────────────────────────

func TestAgentSessionRouter_getEmpty(t *testing.T) {
	r := NewAgentSessionRouter()
	if r.Get("unknown") != "" {
		t.Error("Get on unknown session should return empty string")
	}
}

func TestAgentSessionRouter_assignAndGet(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("sess1", "agent-a")
	if r.Get("sess1") != "agent-a" {
		t.Error("expected agent-a for sess1")
	}
}

func TestAgentSessionRouter_unassign(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("sess1", "agent-a")
	r.Unassign("sess1")
	if r.Get("sess1") != "" {
		t.Error("after unassign, Get should return empty string")
	}
}

func TestAgentSessionRouter_list(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("s1", "a1")
	r.Assign("s2", "a2")
	r.Unassign("s1")

	m := r.List()
	if len(m) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m))
	}
	if m["s2"] != "a2" {
		t.Error("expected s2→a2")
	}
}

func TestAgentSessionRouter_listIsCopy(t *testing.T) {
	r := NewAgentSessionRouter()
	r.Assign("s1", "a1")
	m := r.List()
	m["s1"] = "mutated" // mutate copy
	if r.Get("s1") != "a1" {
		t.Error("list should return a copy; mutation should not affect router")
	}
}

// ─── BuildRuntimeForModel ────────────────────────────────────────────────────

func TestBuildRuntimeForModel_echo(t *testing.T) {
	rt, err := BuildRuntimeForModel("echo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
	res, err := rt.ProcessTurn(context.Background(), Turn{SessionID: "s", UserText: "hello"})
	if err != nil {
		t.Fatalf("ProcessTurn: %v", err)
	}
	if res.Text == "" {
		t.Error("expected non-empty response from echo runtime")
	}
}

func TestBuildRuntimeForModel_empty_errors(t *testing.T) {
	_, err := BuildRuntimeForModel("", nil)
	if err == nil {
		t.Fatal("expected error for empty model")
	}
	if !strings.Contains(err.Error(), "refusing to default to EchoProvider") {
		t.Fatalf("expected implicit echo refusal, got: %v", err)
	}
}

func TestNewProviderFromEnv_missingConfigFallsBackToEcho(t *testing.T) {
	clearProviderCredentialEnv(t)
	p, err := NewProviderFromEnv()
	if err != nil {
		t.Fatalf("expected fallback to EchoProvider, got error: %v", err)
	}
	if _, ok := p.(EchoProvider); !ok {
		t.Fatalf("expected EchoProvider when no credentials present, got %T", p)
	}
}

func TestNewProviderFromEnv_explicitEchoWorks(t *testing.T) {
	clearProviderCredentialEnv(t)
	t.Setenv("METIQ_AGENT_PROVIDER", "echo")
	p, err := NewProviderFromEnv()
	if err != nil {
		t.Fatalf("expected echo to work without opt-in: %v", err)
	}
	if _, ok := p.(EchoProvider); !ok {
		t.Fatalf("expected EchoProvider, got %T", p)
	}
}

func TestNewProviderFromEnv_explicitAnthropicRequiresCredential(t *testing.T) {
	clearProviderCredentialEnv(t)
	t.Setenv("METIQ_AGENT_PROVIDER", "anthropic")
	_, err := NewProviderFromEnv()
	if err == nil {
		t.Fatal("expected anthropic without credentials to fail")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("expected Anthropic credential error, got: %v", err)
	}
}

func TestNewProviderFromEnv_httpLocalAllowsMissingAPIKey(t *testing.T) {
	clearProviderCredentialEnv(t)
	t.Setenv("METIQ_AGENT_PROVIDER", "http")
	t.Setenv("METIQ_AGENT_HTTP_URL", "http://127.0.0.1:8080/v1")
	p, err := NewProviderFromEnv()
	if err != nil {
		t.Fatalf("expected local HTTP provider without API key to work: %v", err)
	}
	if _, ok := p.(*HTTPProvider); !ok {
		t.Fatalf("expected *HTTPProvider, got %T", p)
	}
}

func TestNewProviderFromEnv_httpRemoteRequiresAPIKey(t *testing.T) {
	clearProviderCredentialEnv(t)
	t.Setenv("METIQ_AGENT_PROVIDER", "http")
	t.Setenv("METIQ_AGENT_HTTP_URL", "https://provider.example/v1")
	_, err := NewProviderFromEnv()
	if err == nil {
		t.Fatal("expected remote HTTP provider without API key to fail")
	}
	if !strings.Contains(err.Error(), "METIQ_AGENT_HTTP_API_KEY") {
		t.Fatalf("expected HTTP API key error, got: %v", err)
	}
}

func TestNewProviderFromEnv_httpPrivateNetworkAllowsMissingAPIKey(t *testing.T) {
	clearProviderCredentialEnv(t)
	t.Setenv("METIQ_AGENT_PROVIDER", "http")
	t.Setenv("METIQ_AGENT_HTTP_URL", "http://192.168.1.100:8080/v1")
	p, err := NewProviderFromEnv()
	if err != nil {
		t.Fatalf("expected private network HTTP provider without API key to work: %v", err)
	}
	if _, ok := p.(*HTTPProvider); !ok {
		t.Fatalf("expected *HTTPProvider, got %T", p)
	}
}

func TestBuildRuntimeForModel_unknown(t *testing.T) {
	_, err := BuildRuntimeForModel("totally-unknown-xyz-model", nil)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestNewProviderForModel_hostedCredentialsRequired(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		wantKey string
	}{
		{name: "anthropic", model: "claude-3-5-sonnet-20241022", wantKey: "ANTHROPIC_API_KEY"},
		{name: "openai", model: "gpt-4o", wantKey: "OPENAI_API_KEY"},
		{name: "gemini", model: "gemini-2.0-flash", wantKey: "GEMINI_API_KEY"},
		{name: "cohere", model: "command-r-plus", wantKey: "COHERE_API_KEY"},
		{name: "groq", model: "groq", wantKey: "GROQ_API_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderCredentialEnv(t)
			_, err := NewProviderForModel(tc.model)
			if err == nil {
				t.Fatalf("expected missing credential error for %s", tc.model)
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("expected error to mention %s, got: %v", tc.wantKey, err)
			}
		})
	}
}

func TestNewProviderForModel_localCompatAllowsMissingCredential(t *testing.T) {
	clearProviderCredentialEnv(t)
	p, err := NewProviderForModel("ollama/llama3")
	if err != nil {
		t.Fatalf("expected local Ollama without API key to work: %v", err)
	}
	oai, ok := p.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected OpenAIChatProvider, got %T", p)
	}
	if !isLocalBaseURL(oai.BaseURL) {
		t.Fatalf("expected local base URL, got %q", oai.BaseURL)
	}
}

func TestNewProviderForModel_ggufHint(t *testing.T) {
	_, err := NewProviderForModel("google_gemma-4-26B-A4B-it-Q4_K_M.gguf")
	if err == nil {
		t.Fatal("expected error for .gguf model")
	}
	if !strings.Contains(err.Error(), "local model files require a provider config") {
		t.Errorf("expected provider config hint for .gguf model, got: %v", err)
	}
}

func TestNewProviderForModel_binHint(t *testing.T) {
	_, err := NewProviderForModel("llama-3.bin")
	if err == nil {
		t.Fatal("expected error for .bin model")
	}
	if !strings.Contains(err.Error(), "local model files require a provider config") {
		t.Errorf("expected provider config hint for .bin model, got: %v", err)
	}
}

// ─── BuildRuntimeWithOverride ─────────────────────────────────────────────────

func TestBuildRuntimeWithOverride_emptyOverrideDelegatesToModel(t *testing.T) {
	// Empty override → falls through to BuildRuntimeForModel("echo").
	rt, err := BuildRuntimeWithOverride("echo", ProviderOverride{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
}

func TestBuildRuntimeWithOverride_withBaseURLBuildsHTTP(t *testing.T) {
	rt, err := BuildRuntimeWithOverride("custom-model", ProviderOverride{
		BaseURL: "http://localhost:11434/v1",
		APIKey:  "test-key",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
}

func TestBuildRuntimeWithOverride_missingBaseURLErrors(t *testing.T) {
	// APIKey without BaseURL and no env → error.
	t.Setenv("METIQ_AGENT_HTTP_URL", "")
	_, err := BuildRuntimeWithOverride("custom-model", ProviderOverride{APIKey: "key-only"}, nil)
	if err == nil {
		t.Error("expected error when base_url is empty and env is unset")
	}
}

func TestBuildProviderWithOverride_ModelArgumentWins(t *testing.T) {
	provider, err := BuildProviderWithOverride("gpt-4o-mini", ProviderOverride{BaseURL: "https://api.openai.com/v1", APIKey: "test-key", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op, ok := provider.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected *OpenAIChatProvider, got %T", provider)
	}
	if op.Model != "gpt-4o-mini" {
		t.Fatalf("expected explicit model argument to win, got %q", op.Model)
	}
}

func TestBuildProviderWithOverride_HostedOverrideRequiresCredential(t *testing.T) {
	clearProviderCredentialEnv(t)
	_, err := BuildProviderWithOverride("gpt-4o", ProviderOverride{BaseURL: "https://api.openai.com/v1"})
	if err == nil {
		t.Fatal("expected hosted OpenAI override without credential to fail")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("expected OPENAI_API_KEY error, got: %v", err)
	}
}

func TestBuildProviderWithOverride_LocalOverrideAllowsMissingCredential(t *testing.T) {
	clearProviderCredentialEnv(t)
	provider, err := BuildProviderWithOverride("gpt-4o", ProviderOverride{BaseURL: "http://localhost:11434/v1"})
	if err != nil {
		t.Fatalf("expected local override without credential to work: %v", err)
	}
	if _, ok := provider.(*OpenAIChatProvider); !ok {
		t.Fatalf("expected *OpenAIChatProvider, got %T", provider)
	}
}

func TestBuildProviderWithOverride_RemoteCustomBaseURLRequiresCredential(t *testing.T) {
	clearProviderCredentialEnv(t)
	_, err := BuildProviderWithOverride("custom-model", ProviderOverride{BaseURL: "https://llm.example/v1"})
	if err == nil {
		t.Fatal("expected remote custom override without credential to fail")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("expected api_key error, got: %v", err)
	}
}

// ─── AnthropicProvider ────────────────────────────────────────────────────────

func TestAnthropicProvider_generate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		resp := anthropicFullResponse("Hello from Anthropic mock")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		APIKey: "test-key",
		Model:  "claude-3-5-sonnet-20241022",
		Client: newRewriteClient(srv.Client(), "https://api.anthropic.com", srv.URL),
	}

	result, err := p.Generate(context.Background(), Turn{UserText: "ping", Context: "you are a test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Text == "" {
		t.Error("expected non-empty response text")
	}
}

// anthropicFullResponse returns a complete Anthropic Messages API response.
func anthropicFullResponse(text string) map[string]any {
	return map[string]any{
		"id":          "msg_test123",
		"type":        "message",
		"role":        "assistant",
		"model":       "claude-3-5-sonnet-20241022",
		"stop_reason": "end_turn",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"usage": map[string]any{
			"input_tokens":  10,
			"output_tokens": 5,
		},
	}
}

func TestAnthropicProvider_missingAPIKeyErrors(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	p := &AnthropicProvider{APIKey: "", Model: "claude-3-5-sonnet-20241022"}
	_, err := p.Generate(context.Background(), Turn{UserText: "hi"})
	if err == nil {
		t.Error("expected error when API key is not set")
	}
}

func TestAnthropicProvider_serverErrorPropagated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "rate_limit_error", "message": "rate limited"},
		})
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		APIKey: "key",
		Model:  "claude-3-5-sonnet-20241022",
		Client: newRewriteClient(srv.Client(), "https://api.anthropic.com", srv.URL),
	}
	_, err := p.Generate(context.Background(), Turn{UserText: "hi"})
	if err == nil {
		t.Error("expected error from server error response")
	}
}

// ─── OpenAIChatProvider ───────────────────────────────────────────────────────

func TestOpenAIChatProvider_generate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "Hello from OpenAI mock"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &OpenAIChatProvider{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "gpt-4o",
		Client:  srv.Client(),
	}
	result, err := p.Generate(context.Background(), Turn{UserText: "ping", Context: "system prompt"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Text == "" {
		t.Error("expected non-empty response text")
	}
}

func TestOpenAIChatProvider_serverErrorPropagated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"type": "invalid_request_error", "message": "invalid key"},
		})
	}))
	defer srv.Close()

	p := &OpenAIChatProvider{BaseURL: srv.URL, APIKey: "bad", Model: "gpt-4o", Client: srv.Client()}
	_, err := p.Generate(context.Background(), Turn{UserText: "hi"})
	if err == nil {
		t.Error("expected error from 401 response")
	}
}

// ─── NewProviderForModel with real model names ─────────────────────────────────

func TestNewProviderForModel_anthropicPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := NewProviderForModel("claude-3-5-sonnet-20241022")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*AnthropicProvider); !ok {
		t.Errorf("expected *AnthropicProvider, got %T", p)
	}
}

func TestNewProviderForModel_openaiPrefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := NewProviderForModel("gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*OpenAIChatProvider); !ok {
		t.Errorf("expected *OpenAIChatProvider, got %T", p)
	}
}

func TestNewProviderForModel_o1Prefix(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := NewProviderForModel("o1-preview")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*OpenAIChatProvider); !ok {
		t.Errorf("expected *OpenAIChatProvider, got %T", p)
	}
}

func TestNewProviderForModel_anthropicAlias(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	p, err := NewProviderForModel("anthropic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*AnthropicProvider); !ok {
		t.Errorf("expected *AnthropicProvider, got %T", p)
	}
}

func TestNewProviderForModel_openaiAlias(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	p, err := NewProviderForModel("openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*OpenAIChatProvider); !ok {
		t.Errorf("expected *OpenAIChatProvider, got %T", p)
	}
}

// ─── GoogleGeminiProvider tests ───────────────────────────────────────────────

func TestGoogleGeminiProvider_generate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Paris"}]}}]}`))
	}))
	defer srv.Close()

	p := &GoogleGeminiProvider{
		Model:  "gemini-2.0-flash",
		APIKey: "test-key",
		Client: newRewriteClient(&http.Client{}, "https://generativelanguage.googleapis.com", srv.URL),
	}
	res, err := p.Generate(context.Background(), Turn{UserText: "Capital of France?"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text != "Paris" {
		t.Errorf("text: %q", res.Text)
	}
}

func TestGoogleGeminiProvider_missingAPIKey(t *testing.T) {
	p := &GoogleGeminiProvider{Model: "gemini-2.0-flash"}
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_GENERATIVE_AI_API_KEY", "")
	_, err := p.Generate(context.Background(), Turn{UserText: "hi"})
	if err == nil || !containsStr(err.Error(), "API key not configured") {
		t.Errorf("expected API key error, got: %v", err)
	}
}

func TestGoogleGeminiProvider_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":400,"message":"bad request"}}`))
	}))
	defer srv.Close()

	p := &GoogleGeminiProvider{
		Model:  "gemini-2.0-flash",
		APIKey: "test-key",
		Client: newRewriteClient(&http.Client{}, "https://generativelanguage.googleapis.com", srv.URL),
	}
	_, err := p.Generate(context.Background(), Turn{UserText: "hi"})
	if err == nil || !containsStr(err.Error(), "bad request") {
		t.Errorf("expected server error, got: %v", err)
	}
}

// ─── NewProviderForModel: new provider prefixes ───────────────────────────────

func TestNewProviderForModel_geminiPrefix(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := NewProviderForModel("gemini-2.0-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*GoogleGeminiProvider); !ok {
		t.Errorf("expected GoogleGeminiProvider, got %T", p)
	}
}

func TestNewProviderForModel_geminiAlias(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := NewProviderForModel("gemini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*GoogleGeminiProvider); !ok {
		t.Errorf("expected GoogleGeminiProvider, got %T", p)
	}
}

func TestNewProviderForModel_grokPrefix(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-key")
	p, err := NewProviderForModel("grok-3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oai, ok := p.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected OpenAIChatProvider, got %T", p)
	}
	if !containsStr(oai.BaseURL, "x.ai") {
		t.Errorf("expected x.ai base URL, got %q", oai.BaseURL)
	}
}

func TestNewProviderForModel_groqAlias(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "test-key")
	p, err := NewProviderForModel("groq")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oai, ok := p.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected OpenAIChatProvider, got %T", p)
	}
	if !containsStr(oai.BaseURL, "groq.com") {
		t.Errorf("expected groq.com base URL, got %q", oai.BaseURL)
	}
}

func TestNewProviderForModel_mistralPrefix(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "test-key")
	p, err := NewProviderForModel("mistral-large-latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oai, ok := p.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected OpenAIChatProvider, got %T", p)
	}
	if !containsStr(oai.BaseURL, "mistral.ai") {
		t.Errorf("expected mistral.ai base URL, got %q", oai.BaseURL)
	}
}

func TestNewProviderForModel_openrouterPath(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	p, err := NewProviderForModel("openrouter/meta-llama/llama-3.1-8b-instruct")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oai, ok := p.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected OpenAIChatProvider, got %T", p)
	}
	if !containsStr(oai.BaseURL, "openrouter.ai") {
		t.Errorf("expected openrouter.ai base URL, got %q", oai.BaseURL)
	}
}

func TestNewProviderForModel_togetherPath(t *testing.T) {
	t.Setenv("TOGETHER_API_KEY", "test-key")
	p, err := NewProviderForModel("together/Qwen/Qwen3-235B-A22B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oai, ok := p.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected OpenAIChatProvider, got %T", p)
	}
	if !containsStr(oai.BaseURL, "together.xyz") {
		t.Errorf("expected together.xyz base URL, got %q", oai.BaseURL)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// newRewriteClient returns an *http.Client that replaces oldBase with newBase in request URLs.
func newRewriteClient(base *http.Client, oldBase, newBase string) *http.Client {
	return &http.Client{
		Transport: &rewriteTransport{base: base.Transport, old: oldBase, new_: newBase},
	}
}

type rewriteTransport struct {
	base http.RoundTripper
	old  string
	new_ string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if t.base == nil {
		t.base = http.DefaultTransport
	}
	rawURL := cloned.URL.String()
	newURL := rawURL
	if len(t.old) > 0 && len(rawURL) >= len(t.old) && rawURL[:len(t.old)] == t.old {
		newURL = t.new_ + rawURL[len(t.old):]
	}
	parsed, err := cloned.URL.Parse(newURL)
	if err != nil {
		return nil, err
	}
	cloned.URL = parsed
	cloned.Host = parsed.Host
	return t.base.RoundTrip(cloned)
}

// ─── Vision multi-modal tests ─────────────────────────────────────────────────

func TestAnthropicProvider_Vision_MultiModal(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicFullResponse("I see an image"))
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		APIKey: "test-key",
		Model:  "claude-3-5-sonnet-20241022",
		Client: newRewriteClient(srv.Client(), "https://api.anthropic.com", srv.URL),
	}
	turn := Turn{
		UserText: "What is in this image?",
		Images: []ImageRef{
			{Base64: "aGVsbG8=", MimeType: "image/png"},
		},
	}
	result, err := p.Generate(context.Background(), turn)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Text == "" {
		t.Error("expected non-empty response")
	}
	// Verify the request body contains a content array (multi-modal format).
	msgs, ok := capturedBody["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatal("expected messages in request body")
	}
	userMsg, ok := msgs[0].(map[string]any)
	if !ok {
		t.Fatal("expected first message to be a map")
	}
	// Content should be a []any (multi-modal blocks), not a plain string.
	if _, isSlice := userMsg["content"].([]any); !isSlice {
		t.Errorf("expected content to be array for multi-modal, got %T", userMsg["content"])
	}
}

func TestOpenAIChatProvider_Vision_MultiModal(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "I see a PNG image"}},
			},
		})
	}))
	defer srv.Close()

	p := &OpenAIChatProvider{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "gpt-4o",
		Client:  srv.Client(),
	}
	turn := Turn{
		UserText: "Describe this image.",
		Images: []ImageRef{
			{URL: "https://example.com/photo.jpg"},
		},
	}
	result, err := p.Generate(context.Background(), turn)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Text == "" {
		t.Error("expected non-empty response")
	}
	// Verify multi-modal content array in the request.
	msgs, ok := capturedBody["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatal("expected messages in request body")
	}
	// Find the user message (last message).
	userMsg, ok := msgs[len(msgs)-1].(map[string]any)
	if !ok {
		t.Fatal("expected user message to be a map")
	}
	if _, isSlice := userMsg["content"].([]any); !isSlice {
		t.Errorf("expected content to be array for multi-modal, got %T", userMsg["content"])
	}
}

func TestBuildGeminiParts_TextOnly(t *testing.T) {
	parts := buildGeminiParts("hello", nil)
	if len(parts) != 1 || parts[0].Text != "hello" {
		t.Errorf("expected single text part, got %v", parts)
	}
}

func TestBuildGeminiParts_WithImages(t *testing.T) {
	parts := buildGeminiParts("describe", []ImageRef{
		{Base64: "aGVsbG8=", MimeType: "image/jpeg"},
		{URL: "https://example.com/img.png"},
	})
	// Should have 2 image parts + 1 text part = 3 total.
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	if parts[0].InlineData == nil {
		t.Error("expected first part to be inline_data")
	}
	if parts[1].FileData == nil {
		t.Error("expected second part to be file_data")
	}
	if parts[2].Text != "describe" {
		t.Errorf("expected last part to be text, got %v", parts[2])
	}
}
