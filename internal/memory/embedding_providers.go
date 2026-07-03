// Package memory — real semantic embedding providers for vector retrieval.
//
// These providers implement MemoryEmbeddingProvider by calling a real
// embedding service (Ollama or OpenAI) so that vector recall is genuinely
// semantic. The deterministic StaticMemoryEmbeddingProvider (see
// embedding_provider.go) produces NON-SEMANTIC byte-bucket vectors and must
// never be the silent production default; it is only selected via explicit
// opt-in (METIQ_MEMORY_EMBEDDINGS=static), which logs a loud startup warning.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Environment variables selecting and configuring the default memory embedding
// provider used by vector backends (e.g. the local-vector JSON store).
const (
	// EnvMemoryEmbeddingProvider selects the embedding provider:
	//   ""       → default: "ollama" (a real, local semantic provider)
	//   "ollama" → Ollama HTTP embeddings
	//   "openai" → OpenAI HTTP embeddings (requires an API key)
	//   "static" → deterministic NON-SEMANTIC vectors (tests/fixtures only)
	EnvMemoryEmbeddingProvider = "METIQ_MEMORY_EMBEDDINGS"
	// EnvMemoryEmbeddingModel overrides the embedding model name.
	EnvMemoryEmbeddingModel = "METIQ_MEMORY_EMBED_MODEL"
	// EnvMemoryEmbeddingURL overrides the embedding service base URL.
	EnvMemoryEmbeddingURL = "METIQ_MEMORY_EMBED_URL"
	// EnvMemoryEmbeddingAPIKey supplies the API key for hosted providers.
	EnvMemoryEmbeddingAPIKey = "METIQ_MEMORY_EMBED_API_KEY"
)

const (
	defaultOllamaEmbedURL   = "http://localhost:11434"
	defaultOllamaEmbedModel = "nomic-embed-text"
	defaultOpenAIEmbedURL   = "https://api.openai.com/v1"
	defaultOpenAIEmbedModel = "text-embedding-3-small"

	staticEmbeddingModel = "static-memory"
)

func init() {
	RegisterMemoryEmbeddingProvider("ollama", NewOllamaEmbeddingProvider)
	RegisterMemoryEmbeddingProvider("openai", NewOpenAIEmbeddingProvider)
	// "static" is registered so it can be selected explicitly, but it is never
	// the default (see ResolveMemoryEmbeddingProvider).
	RegisterMemoryEmbeddingProvider("static", NewStaticMemoryEmbeddingProviderFromOpts)
}

// ResolveMemoryEmbeddingProvider returns the embedding provider selected by the
// given environment lookup (getenv defaults to os.Getenv). The default — when
// METIQ_MEMORY_EMBEDDINGS is unset — is a real, semantic provider (Ollama). The
// deterministic StaticMemoryEmbeddingProvider is NON-SEMANTIC and is only
// returned when explicitly opted in via METIQ_MEMORY_EMBEDDINGS=static, in which
// case a loud startup warning is logged so it can never silently become the
// production default.
func ResolveMemoryEmbeddingProvider(getenv func(string) string) (MemoryEmbeddingProvider, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	mode := strings.ToLower(strings.TrimSpace(getenv(EnvMemoryEmbeddingProvider)))
	opts := map[string]any{}
	if v := strings.TrimSpace(getenv(EnvMemoryEmbeddingModel)); v != "" {
		opts["model"] = v
	}
	if v := strings.TrimSpace(getenv(EnvMemoryEmbeddingURL)); v != "" {
		opts["url"] = v
	}
	switch mode {
	case "", "ollama":
		return NewMemoryEmbeddingProvider("ollama", opts)
	case "openai":
		key := firstNonEmpty(strings.TrimSpace(getenv(EnvMemoryEmbeddingAPIKey)), strings.TrimSpace(getenv("OPENAI_API_KEY")))
		if key == "" {
			return nil, fmt.Errorf("memory embeddings: %q selected but no API key configured (set %s or OPENAI_API_KEY)", mode, EnvMemoryEmbeddingAPIKey)
		}
		opts["api_key"] = key
		return NewMemoryEmbeddingProvider("openai", opts)
	case "static":
		log.Printf("WARNING: memory embeddings are using StaticMemoryEmbeddingProvider (%s=static). "+
			"These are deterministic, NON-SEMANTIC vectors intended for tests/fixtures only; "+
			"semantic recall quality is severely degraded. Configure a real provider "+
			"(unset %s for Ollama, or set it to \"openai\") for production use.",
			EnvMemoryEmbeddingProvider, EnvMemoryEmbeddingProvider)
		return NewMemoryEmbeddingProvider("static", opts)
	default:
		// Allow any other explicitly registered provider name.
		return NewMemoryEmbeddingProvider(mode, opts)
	}
}

// NewStaticMemoryEmbeddingProviderFromOpts constructs a StaticMemoryEmbeddingProvider
// from an opts map. It exists so the deterministic provider can be selected
// through the registry (opt-in only).
func NewStaticMemoryEmbeddingProviderFromOpts(opts map[string]any) (MemoryEmbeddingProvider, error) {
	dims := embedOptInt(opts, "dims", 1536)
	if dims <= 0 {
		dims = 1536
	}
	return StaticMemoryEmbeddingProvider{
		Provider: EmbeddingProvider{ID: "static", Model: staticEmbeddingModel, Version: "v1"},
		Dims:     dims,
	}, nil
}

// OllamaEmbeddingProvider embeds text via an Ollama server's /api/embeddings
// endpoint. Vectors are genuinely semantic (default model: nomic-embed-text).
type OllamaEmbeddingProvider struct {
	BaseURL string
	Model   string
	client  *http.Client
}

// NewOllamaEmbeddingProvider builds an OllamaEmbeddingProvider from opts.
// Recognized opts: "url" (base URL), "model".
func NewOllamaEmbeddingProvider(opts map[string]any) (MemoryEmbeddingProvider, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(embedOptString(opts, "url", defaultOllamaEmbedURL)), "/")
	model := strings.TrimSpace(embedOptString(opts, "model", defaultOllamaEmbedModel))
	if baseURL == "" {
		baseURL = defaultOllamaEmbedURL
	}
	if model == "" {
		model = defaultOllamaEmbedModel
	}
	return &OllamaEmbeddingProvider{
		BaseURL: baseURL,
		Model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *OllamaEmbeddingProvider) EmbeddingProvider() EmbeddingProvider {
	return EmbeddingProvider{ID: "ollama", Model: p.Model, Version: "v1"}.Normalized()
}

func (p *OllamaEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body, _ := json.Marshal(map[string]any{"model": p.Model, "prompt": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama embed decode: %w", err)
	}
	if len(out.Embedding) == 0 {
		return nil, fmt.Errorf("ollama embed returned empty vector")
	}
	return out.Embedding, nil
}

func (p *OllamaEmbeddingProvider) httpClient() *http.Client {
	if p.client == nil {
		p.client = &http.Client{Timeout: 30 * time.Second}
	}
	return p.client
}

// OpenAIEmbeddingProvider embeds text via an OpenAI-compatible /embeddings
// endpoint (default model: text-embedding-3-small).
type OpenAIEmbeddingProvider struct {
	BaseURL string
	Model   string
	APIKey  string
	client  *http.Client
}

// NewOpenAIEmbeddingProvider builds an OpenAIEmbeddingProvider from opts.
// Recognized opts: "url" (base URL), "model", "api_key".
func NewOpenAIEmbeddingProvider(opts map[string]any) (MemoryEmbeddingProvider, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(embedOptString(opts, "url", defaultOpenAIEmbedURL)), "/")
	model := strings.TrimSpace(embedOptString(opts, "model", defaultOpenAIEmbedModel))
	apiKey := strings.TrimSpace(embedOptString(opts, "api_key", ""))
	if baseURL == "" {
		baseURL = defaultOpenAIEmbedURL
	}
	if model == "" {
		model = defaultOpenAIEmbedModel
	}
	if apiKey == "" {
		return nil, fmt.Errorf("openai embedding provider: api_key is required")
	}
	return &OpenAIEmbeddingProvider{
		BaseURL: baseURL,
		Model:   model,
		APIKey:  apiKey,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *OpenAIEmbeddingProvider) EmbeddingProvider() EmbeddingProvider {
	return EmbeddingProvider{ID: "openai", Model: p.Model, Version: "v1"}.Normalized()
}

func (p *OpenAIEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body, _ := json.Marshal(map[string]any{"model": p.Model, "input": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embed status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai embed decode: %w", err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai embed returned empty vector")
	}
	return out.Data[0].Embedding, nil
}

func (p *OpenAIEmbeddingProvider) httpClient() *http.Client {
	if p.client == nil {
		p.client = &http.Client{Timeout: 30 * time.Second}
	}
	return p.client
}

func embedOptString(opts map[string]any, key, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts[key].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func embedOptInt(opts map[string]any, key string, def int) int {
	if opts == nil {
		return def
	}
	switch v := opts[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return def
	}
}
