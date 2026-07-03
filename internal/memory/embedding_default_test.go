package memory

import (
	"context"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

// envMap builds a getenv-style lookup from a map for deterministic resolver tests.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestResolveDefaultRequiresRealProvider proves that with no configuration the
// default memory embedding provider is a REAL semantic provider (Ollama), never
// the deterministic non-semantic StaticMemoryEmbeddingProvider.
func TestResolveDefaultRequiresRealProvider(t *testing.T) {
	provider, err := ResolveMemoryEmbeddingProvider(envMap(nil))
	if err != nil {
		t.Fatalf("default resolve failed: %v", err)
	}
	meta := provider.EmbeddingProvider()
	if meta.Model == staticEmbeddingModel {
		t.Fatalf("default provider is the static/non-semantic provider (%q); expected a real provider", meta.Model)
	}
	if meta.ID != "ollama" {
		t.Fatalf("default provider id = %q, want %q (a real semantic provider)", meta.ID, "ollama")
	}
	if _, ok := provider.(StaticMemoryEmbeddingProvider); ok {
		t.Fatal("default provider must not be StaticMemoryEmbeddingProvider")
	}
}

// TestResolveStaticIsOptInOnly proves the static provider is returned ONLY when
// explicitly opted in via METIQ_MEMORY_EMBEDDINGS=static.
func TestResolveStaticIsOptInOnly(t *testing.T) {
	provider, err := ResolveMemoryEmbeddingProvider(envMap(map[string]string{
		EnvMemoryEmbeddingProvider: "static",
	}))
	if err != nil {
		t.Fatalf("static opt-in resolve failed: %v", err)
	}
	if _, ok := provider.(StaticMemoryEmbeddingProvider); !ok {
		t.Fatalf("opt-in did not return StaticMemoryEmbeddingProvider, got %T", provider)
	}
	if meta := provider.EmbeddingProvider(); meta.Model != staticEmbeddingModel {
		t.Fatalf("static provider model = %q, want %q", meta.Model, staticEmbeddingModel)
	}
}

// TestResolveOpenAIRequiresKey proves the OpenAI provider refuses to construct
// without an API key, and constructs cleanly when one is supplied.
func TestResolveOpenAIRequiresKey(t *testing.T) {
	if _, err := ResolveMemoryEmbeddingProvider(envMap(map[string]string{
		EnvMemoryEmbeddingProvider: "openai",
	})); err == nil {
		t.Fatal("expected error when openai is selected without an API key")
	}
	provider, err := ResolveMemoryEmbeddingProvider(envMap(map[string]string{
		EnvMemoryEmbeddingProvider: "openai",
		EnvMemoryEmbeddingAPIKey:   "sk-test",
	}))
	if err != nil {
		t.Fatalf("openai resolve with key failed: %v", err)
	}
	if meta := provider.EmbeddingProvider(); meta.ID != "openai" {
		t.Fatalf("openai provider id = %q, want %q", meta.ID, "openai")
	}
}

// TestOpenLanceDBBackendDefaultProviderIsReal proves the default local-vector
// backend does NOT silently wire the static provider.
func TestOpenLanceDBBackendDefaultProviderIsReal(t *testing.T) {
	t.Setenv(EnvMemoryEmbeddingProvider, "") // neutralize any ambient config
	path := filepath.Join(t.TempDir(), "vectors.json")
	b, err := OpenLanceDBBackend(path)
	if err != nil {
		t.Fatalf("OpenLanceDBBackend: %v", err)
	}
	defer b.Close()
	if meta := b.provider.EmbeddingProvider(); meta.Model == staticEmbeddingModel {
		t.Fatalf("default backend wired the static provider (%q); expected a real provider", meta.Model)
	}
}

// TestOpenLanceDBBackendStaticOptIn proves the static provider is used only when
// explicitly opted in through the environment.
func TestOpenLanceDBBackendStaticOptIn(t *testing.T) {
	t.Setenv(EnvMemoryEmbeddingProvider, "static")
	path := filepath.Join(t.TempDir(), "vectors.json")
	b, err := OpenLanceDBBackend(path)
	if err != nil {
		t.Fatalf("OpenLanceDBBackend: %v", err)
	}
	defer b.Close()
	if meta := b.provider.EmbeddingProvider(); meta.Model != staticEmbeddingModel {
		t.Fatalf("static opt-in backend model = %q, want %q", meta.Model, staticEmbeddingModel)
	}
}

// TestOpenLanceDBBackendWithProviderRejectsNil proves the backend can never run
// with an absent embedding provider.
func TestOpenLanceDBBackendWithProviderRejectsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.json")
	if _, err := OpenLanceDBBackendWithProvider(path, nil); err == nil {
		t.Fatal("expected error when provider is nil")
	}
}

// recordingProvider is a deterministic, semantic-like provider used to prove
// that recall goes through the *configured* provider (not a hardcoded default).
// It maps known texts to orthogonal unit vectors and records every embedded text.
type recordingProvider struct {
	meta    EmbeddingProvider
	vectors map[string][]float32
	calls   *[]string
}

func (p recordingProvider) EmbeddingProvider() EmbeddingProvider { return p.meta.Normalized() }

func (p recordingProvider) Embed(_ context.Context, text string) ([]float32, error) {
	*p.calls = append(*p.calls, text)
	if v, ok := p.vectors[text]; ok {
		return append([]float32(nil), v...), nil
	}
	// Unknown text: a distinct constant vector so it never accidentally matches.
	return []float32{0, 0, 1}, nil
}

// TestLocalVectorRecallUsesConfiguredProvider proves that both indexing and
// query embedding are driven by the injected provider: a query the provider maps
// to the "apple" vector recalls the apple document, and the provider observed the
// query text.
func TestLocalVectorRecallUsesConfiguredProvider(t *testing.T) {
	calls := []string{}
	provider := recordingProvider{
		meta: EmbeddingProvider{ID: "test-embed", Model: "recording", Version: "v1"},
		vectors: map[string][]float32{
			"apple fruit orchard": {1, 0, 0},
			"diesel engine motor": {0, 1, 0},
			"apple-query":         {1, 0, 0}, // semantically aligned with the apple doc
		},
		calls: &calls,
	}

	path := filepath.Join(t.TempDir(), "vectors.json")
	b, err := OpenLanceDBBackendWithProvider(path, provider)
	if err != nil {
		t.Fatalf("OpenLanceDBBackendWithProvider: %v", err)
	}
	defer b.Close()

	b.Add(state.MemoryDoc{MemoryID: "apple", SessionID: "s1", Text: "apple fruit orchard", Unix: 1})
	b.Add(state.MemoryDoc{MemoryID: "diesel", SessionID: "s1", Text: "diesel engine motor", Unix: 2})

	if got := b.Count(); got != 2 {
		t.Fatalf("Count() = %d, want 2", got)
	}

	results := b.Search("apple-query", 2)
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].MemoryID != "apple" {
		t.Fatalf("top recall = %q, want %q (recall did not use the configured provider)", results[0].MemoryID, "apple")
	}

	// The configured provider must have been asked to embed the query text.
	sawQuery := false
	for _, c := range calls {
		if c == "apple-query" {
			sawQuery = true
			break
		}
	}
	if !sawQuery {
		t.Fatalf("configured provider was not used to embed the query; embed calls = %v", calls)
	}
}
