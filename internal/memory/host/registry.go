package host

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"metiq/internal/memory"
)

// BackendConfig is storage-neutral backend configuration. Path retains the
// established backend-specific connection/path encoding; Options is available
// to third-party host factories without exposing it to callers of Manager.
type BackendConfig struct {
	Name    string
	Path    string
	Options map[string]any
}

// EmbeddingConfig selects one registered memory embedding provider.
type EmbeddingConfig struct {
	Name    string
	Options map[string]any
}

// EngineConfig constructs one complete host from storage and embedding seams.
type EngineConfig struct {
	Backend   BackendConfig
	Embedding EmbeddingConfig
	Debug     DebugHook
}

// BackendFactory opens a storage backend. Vector factories receive the same
// embedding provider exposed by the host, preventing backend-specific provider
// wiring.
type BackendFactory func(ctx context.Context, cfg BackendConfig, provider EmbeddingProvider) (Backend, error)

// EmbeddingFactory constructs a host embedding provider.
type EmbeddingFactory func(ctx context.Context, cfg EmbeddingConfig) (EmbeddingProvider, error)

// Host is the complete runtime contract used by callers. Backends remain hidden
// behind Manager and are closed through this common lifecycle seam.
type Host interface {
	Manager
	Close() error
}

// Registry owns backend and embedding factories for a host runtime.
type Registry struct {
	mu         sync.RWMutex
	backends   map[string]BackendFactory
	embeddings map[string]EmbeddingFactory
}

// NewRegistry creates an empty host registry.
func NewRegistry() *Registry {
	return &Registry{backends: map[string]BackendFactory{}, embeddings: map[string]EmbeddingFactory{}}
}

// NewDefaultRegistry registers swarmstr's SQLite, local-vector/LanceDB alias,
// Qdrant, in-memory storage, and all memory embedding provider factories.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	plain := func(_ context.Context, cfg BackendConfig, _ EmbeddingProvider) (Backend, error) {
		return memory.OpenBackend(cfg.Name, cfg.Path)
	}
	for _, name := range []string{"memory", "json-fts", "sqlite"} {
		r.MustRegisterBackend(name, plain)
	}
	vector := func(_ context.Context, cfg BackendConfig, provider EmbeddingProvider) (Backend, error) {
		if provider == nil {
			return nil, fmt.Errorf("memory host: backend %q requires an embedding provider", cfg.Name)
		}
		return memory.OpenLanceDBBackendWithProvider(cfg.Path, provider)
	}
	r.MustRegisterBackend("local-vector", vector)
	r.MustRegisterBackend("lancedb", vector)
	r.MustRegisterBackend("qdrant", func(_ context.Context, cfg BackendConfig, provider EmbeddingProvider) (Backend, error) {
		if provider == nil {
			return nil, errors.New("memory host: qdrant requires an embedding provider")
		}
		return memory.OpenQdrantBackendWithProvider(cfg.Path, provider)
	})
	for _, name := range memory.ListMemoryEmbeddingProviders() {
		providerName := name
		r.MustRegisterEmbedding(providerName, func(_ context.Context, cfg EmbeddingConfig) (EmbeddingProvider, error) {
			return memory.NewMemoryEmbeddingProvider(providerName, cfg.Options)
		})
	}
	return r
}

// RegisterBackend registers a host backend factory by a case-insensitive name.
func (r *Registry) RegisterBackend(name string, factory BackendFactory) error {
	name = normalizeName(name)
	if name == "" || factory == nil {
		return errors.New("memory host: backend name and factory are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.backends[name]; exists {
		return fmt.Errorf("memory host: backend %q already registered", name)
	}
	r.backends[name] = factory
	return nil
}

// MustRegisterBackend is RegisterBackend with startup-time panic semantics.
func (r *Registry) MustRegisterBackend(name string, factory BackendFactory) {
	if err := r.RegisterBackend(name, factory); err != nil {
		panic(err)
	}
}

// RegisterEmbedding registers an embedding factory by a case-insensitive name.
func (r *Registry) RegisterEmbedding(name string, factory EmbeddingFactory) error {
	name = normalizeName(name)
	if name == "" || factory == nil {
		return errors.New("memory host: embedding name and factory are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.embeddings[name]; exists {
		return fmt.Errorf("memory host: embedding provider %q already registered", name)
	}
	r.embeddings[name] = factory
	return nil
}

// MustRegisterEmbedding is RegisterEmbedding with startup-time panic semantics.
func (r *Registry) MustRegisterEmbedding(name string, factory EmbeddingFactory) {
	if err := r.RegisterEmbedding(name, factory); err != nil {
		panic(err)
	}
}

// Backends returns registered backend names in deterministic order.
func (r *Registry) Backends() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedKeys(r.backends)
}

// Embeddings returns registered embedding provider names in deterministic order.
func (r *Registry) Embeddings() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedKeys(r.embeddings)
}

// Open constructs a complete host and injects one embedding provider into both
// the manager and vector backend factory.
func (r *Registry) Open(ctx context.Context, cfg EngineConfig) (Host, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backendName := normalizeName(cfg.Backend.Name)
	if backendName == "" {
		backendName = "memory"
	}
	cfg.Backend.Name = backendName

	var provider EmbeddingProvider
	embeddingName := normalizeName(cfg.Embedding.Name)
	if embeddingName == "" && (backendName == "local-vector" || backendName == "lancedb" || backendName == "qdrant") {
		embeddingName = "ollama"
	}
	if embeddingName != "" {
		r.mu.RLock()
		factory := r.embeddings[embeddingName]
		r.mu.RUnlock()
		if factory == nil {
			return nil, fmt.Errorf("memory host: unknown embedding provider %q (registered: %v)", embeddingName, r.Embeddings())
		}
		cfg.Embedding.Name = embeddingName
		var err error
		provider, err = factory(ctx, cfg.Embedding)
		if err != nil {
			return nil, fmt.Errorf("memory host: open embedding provider %q: %w", embeddingName, err)
		}
		if provider == nil {
			return nil, fmt.Errorf("memory host: embedding provider %q returned nil", embeddingName)
		}
	}

	r.mu.RLock()
	backendFactory := r.backends[backendName]
	r.mu.RUnlock()
	if backendFactory == nil {
		return nil, fmt.Errorf("memory host: unknown backend %q (registered: %v)", backendName, r.Backends())
	}
	backend, err := backendFactory(ctx, cfg.Backend, provider)
	if err != nil {
		return nil, fmt.Errorf("memory host: open backend %q: %w", backendName, err)
	}
	if backend == nil {
		return nil, fmt.Errorf("memory host: backend %q returned nil", backendName)
	}
	manager, err := NewManager(Options{Backend: backend, EmbeddingProvider: provider, Debug: cfg.Debug})
	if err != nil {
		if closer, ok := backend.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return nil, err
	}
	return manager, nil
}

func normalizeName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func sortedKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
