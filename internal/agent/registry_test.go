package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ─── stubRuntime ──────────────────────────────────────────────────────────────

type stubRuntime struct{ id string }

func (s *stubRuntime) ProcessTurn(_ context.Context, turn Turn) (TurnResult, error) {
	return TurnResult{Text: s.id + ":" + turn.UserText}, nil
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

func TestBuildRuntimeForModel_empty_usesEcho(t *testing.T) {
	rt, err := BuildRuntimeForModel("", nil)
	if err != nil {
		t.Fatalf("unexpected error for empty model: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
}

func TestBuildRuntimeForModel_unknown(t *testing.T) {
	_, err := BuildRuntimeForModel("totally-unknown-xyz-model", nil)
	if err == nil {
		t.Error("expected error for unknown model")
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
	t.Setenv("SWARMSTR_AGENT_HTTP_URL", "")
	_, err := BuildRuntimeWithOverride("custom-model", ProviderOverride{APIKey: "key-only"}, nil)
	if err == nil {
		t.Error("expected error when base_url is empty and env is unset")
	}
}

// ─── AnthropicProvider ────────────────────────────────────────────────────────

func TestAnthropicProvider_generate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("x-api-key") == "" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Anthropic mock"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		APIKey: "test-key",
		Model:  "claude-3-5-sonnet-20241022",
		Client: srv.Client(),
	}
	// Override the endpoint via a custom http.Client transport that redirects to the test server.
	// We patch the URL by constructing a custom Transport instead.
	// Simpler: just use the OpenAI-style approach with a custom RoundTripper.
	// For Anthropic, we need to rewrite the URL. Use a helper.
	p.Client = newRewriteClient(srv.Client(), "https://api.anthropic.com", srv.URL)

	result, err := p.Generate(context.Background(), Turn{UserText: "ping", Context: "you are a test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Text == "" {
		t.Error("expected non-empty response text")
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
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
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
		if r.URL.Path != "/v1/chat/completions" {
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
	p, err := NewProviderForModel("claude-3-5-sonnet-20241022")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*AnthropicProvider); !ok {
		t.Errorf("expected *AnthropicProvider, got %T", p)
	}
}

func TestNewProviderForModel_openaiPrefix(t *testing.T) {
	p, err := NewProviderForModel("gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*OpenAIChatProvider); !ok {
		t.Errorf("expected *OpenAIChatProvider, got %T", p)
	}
}

func TestNewProviderForModel_o1Prefix(t *testing.T) {
	p, err := NewProviderForModel("o1-preview")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*OpenAIChatProvider); !ok {
		t.Errorf("expected *OpenAIChatProvider, got %T", p)
	}
}

func TestNewProviderForModel_anthropicAlias(t *testing.T) {
	p, err := NewProviderForModel("anthropic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*AnthropicProvider); !ok {
		t.Errorf("expected *AnthropicProvider, got %T", p)
	}
}

func TestNewProviderForModel_openaiAlias(t *testing.T) {
	p, err := NewProviderForModel("openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*OpenAIChatProvider); !ok {
		t.Errorf("expected *OpenAIChatProvider, got %T", p)
	}
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
