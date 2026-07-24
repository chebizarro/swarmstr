package host

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"metiq/internal/memory"
)

var (
	_ Backend = (*memory.IndexBackend)(nil)
	_ Backend = (*memory.SQLiteBackend)(nil)
	_ Backend = (*memory.LanceDBBackend)(nil)
	_ Backend = (*memory.QdrantBackend)(nil)
)

type registryProvider struct {
	metadata memory.EmbeddingProvider
}

func (p *registryProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []float32{1, 0, 0}, nil
}

func (p *registryProvider) EmbeddingProvider() memory.EmbeddingProvider { return p.metadata }

func TestRegistryInjectsOneProviderIntoBackendAndManager(t *testing.T) {
	registry := NewRegistry()
	provider := &registryProvider{metadata: memory.EmbeddingProvider{ID: "contract", Model: "v1"}}
	var received EmbeddingProvider
	backend := &fakeBackend{hits: []memory.IndexedMemory{{MemoryID: "m1", Text: "shared contract"}}}
	registry.MustRegisterEmbedding("contract", func(ctx context.Context, cfg EmbeddingConfig) (EmbeddingProvider, error) {
		return provider, nil
	})
	registry.MustRegisterBackend("contract-store", func(ctx context.Context, cfg BackendConfig, injected EmbeddingProvider) (Backend, error) {
		received = injected
		return backend, nil
	})

	host, err := registry.Open(context.Background(), EngineConfig{
		Backend:   BackendConfig{Name: "CONTRACT-STORE"},
		Embedding: EmbeddingConfig{Name: "CONTRACT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if received != provider {
		t.Fatalf("backend did not receive configured provider: %#v", received)
	}
	probe, err := host.ProbeEmbeddingAvailability(context.Background())
	if err != nil || !probe.Available || probe.Provider.ID != "contract" || probe.Dims != 3 {
		t.Fatalf("manager did not use configured provider: probe=%#v err=%v", probe, err)
	}
	results, err := host.Search(context.Background(), "contract", SearchOptions{})
	if err != nil || len(results) != 1 || results[0].Ref != "m1" {
		t.Fatalf("backend was not used through host: results=%#v err=%v", results, err)
	}
}

func TestDefaultRegistryExposesPortableBackendsAndProviders(t *testing.T) {
	registry := NewDefaultRegistry()
	for _, backend := range []string{"memory", "sqlite", "lancedb", "local-vector", "qdrant"} {
		if !containsName(registry.Backends(), backend) {
			t.Fatalf("backend %q missing from %v", backend, registry.Backends())
		}
	}
	for _, provider := range []string{"ollama", "openai", "static"} {
		if !containsName(registry.Embeddings(), provider) {
			t.Fatalf("provider %q missing from %v", provider, registry.Embeddings())
		}
	}
}

func TestRegistryRejectsDuplicatesUnknownsAndCancellation(t *testing.T) {
	registry := NewRegistry()
	factory := func(context.Context, BackendConfig, EmbeddingProvider) (Backend, error) {
		return &fakeBackend{}, nil
	}
	if err := registry.RegisterBackend("store", factory); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterBackend("STORE", factory); err == nil {
		t.Fatal("expected duplicate backend rejection")
	}
	if _, err := registry.Open(context.Background(), EngineConfig{Backend: BackendConfig{Name: "missing"}}); err == nil {
		t.Fatal("expected unknown backend rejection")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.Open(ctx, EngineConfig{Backend: BackendConfig{Name: "store"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if got, want := registry.Backends(), []string{"store"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected deterministic names: got %v want %v", got, want)
	}
}

func containsName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
