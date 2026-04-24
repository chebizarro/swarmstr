package memory

import (
	"path/filepath"
	"testing"
)

func newTestEmbeddingCache(t *testing.T) (*EmbeddingCache, *SQLiteBackend) {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-cache.sqlite")
	backend, err := OpenSQLiteBackend(path)
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	t.Cleanup(func() { backend.Close() })

	cache := NewEmbeddingCache(backend.db, DefaultEmbeddingCacheConfig())
	return cache, backend
}

func TestEmbeddingCache_StoreAndLoad(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	provider := EmbeddingProvider{ID: "openai", Model: "text-embedding-3-small"}

	// Store some embeddings
	entries := []EmbeddingCacheEntry{
		{Hash: "hash1", Embedding: []float32{0.1, 0.2, 0.3}},
		{Hash: "hash2", Embedding: []float32{0.4, 0.5, 0.6}},
	}

	err := cache.Store(provider, entries)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Load them back
	loaded := cache.Load(provider, []string{"hash1", "hash2", "hash3"})

	if len(loaded) != 2 {
		t.Fatalf("Load: got %d entries, want 2", len(loaded))
	}

	if emb, ok := loaded["hash1"]; !ok {
		t.Error("Load: missing hash1")
	} else if len(emb) != 3 || emb[0] != 0.1 {
		t.Errorf("Load hash1: got %v, want [0.1 0.2 0.3]", emb)
	}

	if emb, ok := loaded["hash2"]; !ok {
		t.Error("Load: missing hash2")
	} else if len(emb) != 3 || emb[0] != 0.4 {
		t.Errorf("Load hash2: got %v, want [0.4 0.5 0.6]", emb)
	}

	// hash3 should not be found
	if _, ok := loaded["hash3"]; ok {
		t.Error("Load: hash3 should not be found")
	}
}

func TestEmbeddingCache_ProviderIsolation(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	provider1 := EmbeddingProvider{ID: "openai", Model: "text-embedding-3-small"}
	provider2 := EmbeddingProvider{ID: "voyage", Model: "voyage-2"}

	// Store with provider1
	cache.Store(provider1, []EmbeddingCacheEntry{
		{Hash: "shared_hash", Embedding: []float32{1.0, 2.0}},
	})

	// Store with provider2 (same hash, different embedding)
	cache.Store(provider2, []EmbeddingCacheEntry{
		{Hash: "shared_hash", Embedding: []float32{3.0, 4.0}},
	})

	// Load with provider1
	loaded1 := cache.Load(provider1, []string{"shared_hash"})
	if emb, ok := loaded1["shared_hash"]; !ok {
		t.Error("Load provider1: missing shared_hash")
	} else if emb[0] != 1.0 {
		t.Errorf("Load provider1: got %v, want [1.0 2.0]", emb)
	}

	// Load with provider2
	loaded2 := cache.Load(provider2, []string{"shared_hash"})
	if emb, ok := loaded2["shared_hash"]; !ok {
		t.Error("Load provider2: missing shared_hash")
	} else if emb[0] != 3.0 {
		t.Errorf("Load provider2: got %v, want [3.0 4.0]", emb)
	}
}

func TestEmbeddingCache_ProviderKeyIsolation(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	provider := EmbeddingProvider{ID: "openai", Model: "text-embedding-3-small"}

	// Store with key1
	cache.SetProviderKey(HashProviderKey("api-key-1"))
	cache.Store(provider, []EmbeddingCacheEntry{
		{Hash: "test_hash", Embedding: []float32{1.0}},
	})

	// Store with key2
	cache.SetProviderKey(HashProviderKey("api-key-2"))
	cache.Store(provider, []EmbeddingCacheEntry{
		{Hash: "test_hash", Embedding: []float32{2.0}},
	})

	// Load with key1
	cache.SetProviderKey(HashProviderKey("api-key-1"))
	loaded1 := cache.Load(provider, []string{"test_hash"})
	if emb, ok := loaded1["test_hash"]; !ok {
		t.Error("Load key1: missing test_hash")
	} else if emb[0] != 1.0 {
		t.Errorf("Load key1: got %v, want [1.0]", emb)
	}

	// Load with key2
	cache.SetProviderKey(HashProviderKey("api-key-2"))
	loaded2 := cache.Load(provider, []string{"test_hash"})
	if emb, ok := loaded2["test_hash"]; !ok {
		t.Error("Load key2: missing test_hash")
	} else if emb[0] != 2.0 {
		t.Errorf("Load key2: got %v, want [2.0]", emb)
	}
}

func TestEmbeddingCache_Count(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	if count := cache.Count(); count != 0 {
		t.Errorf("Count empty: got %d, want 0", count)
	}

	provider := EmbeddingProvider{ID: "test", Model: "test"}
	cache.Store(provider, []EmbeddingCacheEntry{
		{Hash: "h1", Embedding: []float32{1.0}},
		{Hash: "h2", Embedding: []float32{2.0}},
	})

	if count := cache.Count(); count != 2 {
		t.Errorf("Count after store: got %d, want 2", count)
	}
}

func TestEmbeddingCache_CountByProvider(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	provider1 := EmbeddingProvider{ID: "p1", Model: "m1"}
	provider2 := EmbeddingProvider{ID: "p2", Model: "m2"}

	cache.Store(provider1, []EmbeddingCacheEntry{
		{Hash: "h1", Embedding: []float32{1.0}},
		{Hash: "h2", Embedding: []float32{2.0}},
	})
	cache.Store(provider2, []EmbeddingCacheEntry{
		{Hash: "h3", Embedding: []float32{3.0}},
	})

	if count := cache.CountByProvider(provider1); count != 2 {
		t.Errorf("CountByProvider p1: got %d, want 2", count)
	}
	if count := cache.CountByProvider(provider2); count != 1 {
		t.Errorf("CountByProvider p2: got %d, want 1", count)
	}
}

func TestEmbeddingCache_Prune(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)
	cache.cfg.MaxEntries = 2

	provider := EmbeddingProvider{ID: "test", Model: "test"}

	// Store 5 entries
	for i := 0; i < 5; i++ {
		cache.Store(provider, []EmbeddingCacheEntry{
			{Hash: string(rune('a' + i)), Embedding: []float32{float32(i)}},
		})
	}

	if count := cache.Count(); count != 5 {
		t.Fatalf("Count before prune: got %d, want 5", count)
	}

	removed := cache.Prune()
	if removed != 3 {
		t.Errorf("Prune: removed %d, want 3", removed)
	}

	if count := cache.Count(); count != 2 {
		t.Errorf("Count after prune: got %d, want 2", count)
	}
}

func TestEmbeddingCache_Clear(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	provider := EmbeddingProvider{ID: "test", Model: "test"}
	cache.Store(provider, []EmbeddingCacheEntry{
		{Hash: "h1", Embedding: []float32{1.0}},
	})

	if err := cache.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if count := cache.Count(); count != 0 {
		t.Errorf("Count after clear: got %d, want 0", count)
	}
}

func TestEmbeddingCache_ClearByProvider(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	provider1 := EmbeddingProvider{ID: "p1", Model: "m1"}
	provider2 := EmbeddingProvider{ID: "p2", Model: "m2"}

	cache.Store(provider1, []EmbeddingCacheEntry{{Hash: "h1", Embedding: []float32{1.0}}})
	cache.Store(provider2, []EmbeddingCacheEntry{{Hash: "h2", Embedding: []float32{2.0}}})

	if err := cache.ClearByProvider(provider1); err != nil {
		t.Fatalf("ClearByProvider: %v", err)
	}

	if count := cache.CountByProvider(provider1); count != 0 {
		t.Errorf("CountByProvider p1 after clear: got %d, want 0", count)
	}
	if count := cache.CountByProvider(provider2); count != 1 {
		t.Errorf("CountByProvider p2 after clear: got %d, want 1", count)
	}
}

func TestEmbeddingCache_Stats(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)

	provider := EmbeddingProvider{ID: "openai", Model: "text-embedding-3-small"}
	cache.Store(provider, []EmbeddingCacheEntry{
		{Hash: "h1", Embedding: []float32{1.0, 2.0, 3.0}},
	})

	stats := cache.Stats()

	if stats["enabled"] != true {
		t.Error("Stats enabled: want true")
	}
	if stats["total_entries"].(int) != 1 {
		t.Errorf("Stats total_entries: got %v, want 1", stats["total_entries"])
	}

	providers, ok := stats["providers"].([]map[string]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("Stats providers: got %v", stats["providers"])
	}
	if providers[0]["provider"] != "openai" {
		t.Errorf("Stats provider: got %v, want openai", providers[0]["provider"])
	}
}

func TestEmbeddingCache_Disabled(t *testing.T) {
	cache, _ := newTestEmbeddingCache(t)
	cache.cfg.Enabled = false

	provider := EmbeddingProvider{ID: "test", Model: "test"}

	// Store should be no-op
	err := cache.Store(provider, []EmbeddingCacheEntry{
		{Hash: "h1", Embedding: []float32{1.0}},
	})
	if err != nil {
		t.Fatalf("Store disabled: %v", err)
	}

	// Load should return nil
	loaded := cache.Load(provider, []string{"h1"})
	if loaded != nil {
		t.Errorf("Load disabled: got %v, want nil", loaded)
	}
}

func TestHashContent(t *testing.T) {
	// Same content should produce same hash
	h1 := HashContent("hello world")
	h2 := HashContent("hello world")
	if h1 != h2 {
		t.Error("HashContent: same content should produce same hash")
	}

	// Different content should produce different hash
	h3 := HashContent("goodbye world")
	if h1 == h3 {
		t.Error("HashContent: different content should produce different hash")
	}

	// Whitespace should be trimmed
	h4 := HashContent("  hello world  ")
	if h1 != h4 {
		t.Error("HashContent: should trim whitespace")
	}
}

func TestHashProviderKey(t *testing.T) {
	// Empty key should return "default"
	if h := HashProviderKey(""); h != "default" {
		t.Errorf("HashProviderKey empty: got %q, want 'default'", h)
	}

	// Same key should produce same hash
	h1 := HashProviderKey("sk-abc123")
	h2 := HashProviderKey("sk-abc123")
	if h1 != h2 {
		t.Error("HashProviderKey: same key should produce same hash")
	}

	// Different keys should produce different hashes
	h3 := HashProviderKey("sk-xyz789")
	if h1 == h3 {
		t.Error("HashProviderKey: different keys should produce different hashes")
	}
}

func TestCollectCachedEmbeddings(t *testing.T) {
	type testItem struct {
		text string
		hash string
	}

	items := []testItem{
		{text: "hello", hash: "h1"},
		{text: "world", hash: "h2"},
		{text: "test", hash: "h3"},
	}

	// Wrap in cacheable interface
	cacheables := make([]*MemoryChunkCacheable, len(items))
	for i, item := range items {
		c := NewMemoryChunkCacheable(item.text)
		c.hash = item.hash // Override hash for testing
		cacheables[i] = c
	}

	// Cached has h1 and h3
	cached := map[string][]float32{
		"h1": {1.0, 2.0},
		"h3": {3.0, 4.0},
	}

	embeddings, missing := CollectCachedEmbeddings(cacheables, cached)

	if len(embeddings) != 3 {
		t.Fatalf("CollectCachedEmbeddings embeddings: got %d, want 3", len(embeddings))
	}

	// h1 should be found
	if embeddings[0] == nil || embeddings[0][0] != 1.0 {
		t.Errorf("embeddings[0]: got %v, want [1.0 2.0]", embeddings[0])
	}

	// h2 should be missing
	if embeddings[1] != nil {
		t.Errorf("embeddings[1]: got %v, want nil", embeddings[1])
	}

	// h3 should be found
	if embeddings[2] == nil || embeddings[2][0] != 3.0 {
		t.Errorf("embeddings[2]: got %v, want [3.0 4.0]", embeddings[2])
	}

	// Missing should be [1] (index of h2)
	if len(missing) != 1 || missing[0] != 1 {
		t.Errorf("missing: got %v, want [1]", missing)
	}
}

func TestBatchHashContents(t *testing.T) {
	texts := []string{"hello", "world", "test"}
	hashes := BatchHashContents(texts)

	if len(hashes) != 3 {
		t.Fatalf("BatchHashContents: got %d hashes, want 3", len(hashes))
	}

	// Each hash should be non-empty
	for i, h := range hashes {
		if h == "" {
			t.Errorf("BatchHashContents[%d]: empty hash", i)
		}
	}

	// Same text should produce same hash
	if hashes[0] != HashContent("hello") {
		t.Error("BatchHashContents: inconsistent with HashContent")
	}
}
