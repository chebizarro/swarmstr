package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MemoryEmbeddingProviderFactory constructs a named embedding provider.
type MemoryEmbeddingProviderFactory func(opts map[string]any) (MemoryEmbeddingProvider, error)

var memoryEmbeddingProviders = struct {
	sync.RWMutex
	factories map[string]MemoryEmbeddingProviderFactory
	order     []string
}{factories: map[string]MemoryEmbeddingProviderFactory{}}

// RegisterMemoryEmbeddingProvider registers a provider factory by name. Names
// are case-insensitive. Duplicate names panic to catch configuration mistakes at
// startup.
func RegisterMemoryEmbeddingProvider(name string, factory MemoryEmbeddingProviderFactory) {
	name = normalizeEmbeddingProviderName(name)
	if name == "" {
		panic("memory embedding provider name is required")
	}
	if factory == nil {
		panic("memory embedding provider factory is required")
	}
	memoryEmbeddingProviders.Lock()
	defer memoryEmbeddingProviders.Unlock()
	if _, exists := memoryEmbeddingProviders.factories[name]; exists {
		panic(fmt.Sprintf("memory embedding provider %q already registered", name))
	}
	memoryEmbeddingProviders.factories[name] = factory
	memoryEmbeddingProviders.order = append(memoryEmbeddingProviders.order, name)
}

// ListMemoryEmbeddingProviders returns registered provider names in registration order.
func ListMemoryEmbeddingProviders() []string {
	memoryEmbeddingProviders.RLock()
	defer memoryEmbeddingProviders.RUnlock()
	out := make([]string, len(memoryEmbeddingProviders.order))
	copy(out, memoryEmbeddingProviders.order)
	return out
}

// NewMemoryEmbeddingProvider constructs a registered provider by name.
func NewMemoryEmbeddingProvider(name string, opts map[string]any) (MemoryEmbeddingProvider, error) {
	name = normalizeEmbeddingProviderName(name)
	memoryEmbeddingProviders.RLock()
	factory := memoryEmbeddingProviders.factories[name]
	memoryEmbeddingProviders.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("memory embedding provider %q not registered (available: %v)", name, ListMemoryEmbeddingProviders())
	}
	provider, err := factory(opts)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, fmt.Errorf("memory embedding provider %q returned nil", name)
	}
	meta := provider.EmbeddingProvider().Normalized()
	if meta.ID == "" || meta.Model == "" {
		return nil, fmt.Errorf("memory embedding provider %q returned incomplete metadata", name)
	}
	return provider, nil
}

// StaticMemoryEmbeddingProvider is a deterministic provider useful for tests,
// local fixtures, and providers that are wrapped by callers.
type StaticMemoryEmbeddingProvider struct {
	Provider EmbeddingProvider
	Dims     int
	Vector   []float32
}

func (p StaticMemoryEmbeddingProvider) EmbeddingProvider() EmbeddingProvider {
	return p.Provider.Normalized()
}

func (p StaticMemoryEmbeddingProvider) Embed(_ context.Context, text string) ([]float32, error) {
	dims := p.Dims
	if dims <= 0 {
		dims = len(p.Vector)
	}
	if dims <= 0 {
		dims = 3
	}
	out := make([]float32, dims)
	if len(p.Vector) > 0 {
		copy(out, p.Vector)
		return out, nil
	}
	for i, r := range []byte(strings.TrimSpace(text)) {
		out[i%dims] += float32(r%31) / 31.0
	}
	if normalizeVector(out) == nil {
		out[0] = 1
	}
	return out, nil
}

func normalizeEmbeddingProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func registeredEmbeddingProviderNamesSorted() []string {
	names := ListMemoryEmbeddingProviders()
	sort.Strings(names)
	return names
}
