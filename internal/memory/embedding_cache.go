// Package memory — Embedding cache for reducing API costs.
//
// The embedding cache stores computed embeddings by content hash, avoiding
// redundant API calls to embedding services (OpenAI, Ollama, etc.).
// This can reduce embedding costs by 50-90% for repeated content.
//
// Cache key: (provider, model, provider_key_hash, content_hash)
// This allows multi-tenant deployments where different API keys may produce
// different embeddings (though in practice they're usually identical).
package memory

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// EmbeddingCacheConfig configures the embedding cache.
type EmbeddingCacheConfig struct {
	// Enabled toggles the embedding cache (default: true).
	Enabled bool `json:"enabled"`

	// MaxEntries limits the cache size (0 = unlimited).
	MaxEntries int `json:"max_entries,omitempty"`
}

// DefaultEmbeddingCacheConfig returns sensible defaults.
func DefaultEmbeddingCacheConfig() EmbeddingCacheConfig {
	return EmbeddingCacheConfig{
		Enabled:    true,
		MaxEntries: 100_000,
	}
}

// EmbeddingCacheEntry represents a cached embedding.
type EmbeddingCacheEntry struct {
	Hash      string    `json:"hash"`
	Embedding []float32 `json:"embedding"`
}

// EmbeddingProvider identifies the embedding provider and model.
type EmbeddingProvider struct {
	// ID is the provider identifier (e.g., "openai", "ollama", "voyage").
	ID string `json:"id"`

	// Model is the specific model (e.g., "text-embedding-3-small").
	Model string `json:"model"`
}

// EmbeddingCache provides caching for embeddings in a SQLite database.
type EmbeddingCache struct {
	db          *sql.DB
	cfg         EmbeddingCacheConfig
	providerKey string // Hash of API key for multi-tenant support
}

// NewEmbeddingCache creates an embedding cache using the given database.
// The database should already have the embedding_cache table created
// (this is done automatically by SQLiteBackend.initSchema).
func NewEmbeddingCache(db *sql.DB, cfg EmbeddingCacheConfig) *EmbeddingCache {
	return &EmbeddingCache{
		db:  db,
		cfg: cfg,
	}
}

// SetProviderKey sets the provider key hash for multi-tenant caching.
// Call this with a hash of the API key being used.
func (c *EmbeddingCache) SetProviderKey(keyHash string) {
	c.providerKey = keyHash
}

// HashProviderKey creates a hash of an API key for use as provider_key.
func HashProviderKey(apiKey string) string {
	if apiKey == "" {
		return "default"
	}
	h := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(h[:8]) // Use first 8 bytes
}

// HashContent creates a hash of text content for cache lookup.
func HashContent(text string) string {
	normalized := strings.TrimSpace(text)
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}

// Load retrieves cached embeddings for the given content hashes.
// Returns a map from hash to embedding for found entries.
func (c *EmbeddingCache) Load(provider EmbeddingProvider, hashes []string) map[string][]float32 {
	if !c.cfg.Enabled || c.db == nil || len(hashes) == 0 {
		return nil
	}

	// Deduplicate hashes
	unique := make([]string, 0, len(hashes))
	seen := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		if h != "" && !seen[h] {
			seen[h] = true
			unique = append(unique, h)
		}
	}

	if len(unique) == 0 {
		return nil
	}

	result := make(map[string][]float32)
	providerKey := c.providerKey
	if providerKey == "" {
		providerKey = "default"
	}

	// Query in batches to avoid SQLite variable limits
	const batchSize = 400
	for start := 0; start < len(unique); start += batchSize {
		end := start + batchSize
		if end > len(unique) {
			end = len(unique)
		}
		batch := unique[start:end]

		// Build query with placeholders
		placeholders := make([]string, len(batch))
		args := make([]any, 0, 3+len(batch))
		args = append(args, provider.ID, provider.Model, providerKey)

		for i, h := range batch {
			placeholders[i] = "?"
			args = append(args, h)
		}

		query := `
			SELECT hash, embedding FROM embedding_cache
			WHERE provider = ? AND model = ? AND provider_key = ?
			AND hash IN (` + strings.Join(placeholders, ",") + `)`

		rows, err := c.db.Query(query, args...)
		if err != nil {
			continue
		}

		for rows.Next() {
			var hash, embeddingJSON string
			if err := rows.Scan(&hash, &embeddingJSON); err != nil {
				continue
			}

			var embedding []float32
			if err := json.Unmarshal([]byte(embeddingJSON), &embedding); err != nil {
				continue
			}

			result[hash] = embedding
		}
		rows.Close()
	}

	return result
}

// Store saves embeddings to the cache.
func (c *EmbeddingCache) Store(provider EmbeddingProvider, entries []EmbeddingCacheEntry) error {
	if !c.cfg.Enabled || c.db == nil || len(entries) == 0 {
		return nil
	}

	providerKey := c.providerKey
	if providerKey == "" {
		providerKey = "default"
	}

	now := time.Now().Unix()

	// Use a transaction for batch insert
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO embedding_cache (provider, model, provider_key, hash, embedding, dims, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, model, provider_key, hash) DO UPDATE SET
			embedding = excluded.embedding,
			dims = excluded.dims,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, entry := range entries {
		if entry.Hash == "" || len(entry.Embedding) == 0 {
			continue
		}

		embeddingJSON, err := json.Marshal(entry.Embedding)
		if err != nil {
			continue
		}

		_, err = stmt.Exec(
			provider.ID,
			provider.Model,
			providerKey,
			entry.Hash,
			string(embeddingJSON),
			len(entry.Embedding),
			now,
		)
		if err != nil {
			continue // Skip failed entries
		}
	}

	return tx.Commit()
}

// CollectCached separates items into cached (with embeddings) and missing.
// Returns embeddings slice aligned with input, and indices of missing items.
type CacheableItem interface {
	// CacheHash returns the content hash for this item.
	CacheHash() string
}

// CollectCachedEmbeddings checks which items have cached embeddings.
// Returns:
//   - embeddings: slice aligned with input (nil for missing items)
//   - missing: indices of items that need embedding
func CollectCachedEmbeddings[T CacheableItem](
	items []T,
	cached map[string][]float32,
) (embeddings [][]float32, missing []int) {
	embeddings = make([][]float32, len(items))
	missing = make([]int, 0)

	for i, item := range items {
		hash := item.CacheHash()
		if emb, ok := cached[hash]; ok && len(emb) > 0 {
			embeddings[i] = emb
		} else {
			missing = append(missing, i)
		}
	}

	return embeddings, missing
}

// Count returns the number of cached embeddings.
func (c *EmbeddingCache) Count() int {
	if c.db == nil {
		return 0
	}

	var count int
	err := c.db.QueryRow(`SELECT COUNT(*) FROM embedding_cache`).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// CountByProvider returns the number of cached embeddings for a provider.
func (c *EmbeddingCache) CountByProvider(provider EmbeddingProvider) int {
	if c.db == nil {
		return 0
	}

	providerKey := c.providerKey
	if providerKey == "" {
		providerKey = "default"
	}

	var count int
	err := c.db.QueryRow(
		`SELECT COUNT(*) FROM embedding_cache WHERE provider = ? AND model = ? AND provider_key = ?`,
		provider.ID, provider.Model, providerKey,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// Prune removes old entries to keep the cache under MaxEntries.
// Returns the number of entries removed.
func (c *EmbeddingCache) Prune() int {
	if c.db == nil || c.cfg.MaxEntries <= 0 {
		return 0
	}

	count := c.Count()
	if count <= c.cfg.MaxEntries {
		return 0
	}

	toRemove := count - c.cfg.MaxEntries

	result, err := c.db.Exec(`
		DELETE FROM embedding_cache WHERE rowid IN (
			SELECT rowid FROM embedding_cache ORDER BY updated_at ASC LIMIT ?
		)
	`, toRemove)
	if err != nil {
		return 0
	}

	affected, _ := result.RowsAffected()
	return int(affected)
}

// Clear removes all cached embeddings.
func (c *EmbeddingCache) Clear() error {
	if c.db == nil {
		return nil
	}

	_, err := c.db.Exec(`DELETE FROM embedding_cache`)
	return err
}

// ClearByProvider removes cached embeddings for a specific provider.
func (c *EmbeddingCache) ClearByProvider(provider EmbeddingProvider) error {
	if c.db == nil {
		return nil
	}

	providerKey := c.providerKey
	if providerKey == "" {
		providerKey = "default"
	}

	_, err := c.db.Exec(
		`DELETE FROM embedding_cache WHERE provider = ? AND model = ? AND provider_key = ?`,
		provider.ID, provider.Model, providerKey,
	)
	return err
}

// Stats returns cache statistics.
func (c *EmbeddingCache) Stats() map[string]any {
	stats := map[string]any{
		"enabled":     c.cfg.Enabled,
		"max_entries": c.cfg.MaxEntries,
	}

	if c.db != nil {
		stats["total_entries"] = c.Count()

		// Get unique provider/model combinations
		rows, err := c.db.Query(`
			SELECT provider, model, COUNT(*) as count
			FROM embedding_cache
			GROUP BY provider, model
		`)
		if err == nil {
			providers := make([]map[string]any, 0)
			for rows.Next() {
				var provider, model string
				var count int
				if rows.Scan(&provider, &model, &count) == nil {
					providers = append(providers, map[string]any{
						"provider": provider,
						"model":    model,
						"count":    count,
					})
				}
			}
			rows.Close()
			stats["providers"] = providers
		}
	}

	return stats
}

// ── Integration helpers ─────────────────────────────────────────────────────

// MemoryChunkCacheable wraps a memory chunk to implement CacheableItem.
type MemoryChunkCacheable struct {
	Text string
	hash string // Cached hash
}

func (m *MemoryChunkCacheable) CacheHash() string {
	if m.hash == "" {
		m.hash = HashContent(m.Text)
	}
	return m.hash
}

// NewMemoryChunkCacheable creates a cacheable wrapper for memory text.
func NewMemoryChunkCacheable(text string) *MemoryChunkCacheable {
	return &MemoryChunkCacheable{Text: text}
}

// BatchHashContents computes content hashes for multiple texts.
func BatchHashContents(texts []string) []string {
	hashes := make([]string, len(texts))
	for i, text := range texts {
		hashes[i] = HashContent(text)
	}
	return hashes
}
