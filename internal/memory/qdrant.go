// Package memory - Qdrant backend for semantic memory search.
// Uses Ollama (nomic-embed-text) for embeddings + Qdrant for vector storage.
package memory

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

const (
	qdrantDefaultURL  = "http://localhost:6333"
	ollamaDefaultURL  = "http://localhost:11434"
	defaultCollection = "metiq"
	embedModel        = "nomic-embed-text"
	vectorDim         = 768
)

func init() {
	RegisterBackend("qdrant", func(path string) (Backend, error) {
		// path encodes "qdrant_url|ollama_url|collection" or just qdrant_url
		qdrantURL := qdrantDefaultURL
		ollamaURL := ollamaDefaultURL
		collection := defaultCollection
		if path != "" {
			parts := strings.SplitN(path, "|", 3)
			if len(parts) >= 1 && parts[0] != "" {
				qdrantURL = parts[0]
			}
			if len(parts) >= 2 && parts[1] != "" {
				ollamaURL = parts[1]
			}
			if len(parts) >= 3 && parts[2] != "" {
				collection = parts[2]
			}
		}
		b := &QdrantBackend{
			qdrantURL:  strings.TrimRight(qdrantURL, "/"),
			ollamaURL:  strings.TrimRight(ollamaURL, "/"),
			collection: collection,
			client:     &http.Client{Timeout: 30 * time.Second},
		}
		if err := b.ensureCollection(); err != nil {
			return nil, fmt.Errorf("qdrant: ensure collection %q: %w", collection, err)
		}
		return b, nil
	})
}

// QdrantBackend implements memory.Backend using Qdrant + Ollama embeddings.
type QdrantBackend struct {
	mu         sync.Mutex
	qdrantURL  string
	ollamaURL  string
	collection string
	client     *http.Client
}

// -- embedding --

func (b *QdrantBackend) embed(ctx context.Context, text string) ([]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body, _ := json.Marshal(map[string]any{
		"model":  embedModel,
		"prompt": text,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, b.ollamaURL+"/api/embeddings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
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

// -- Qdrant helpers --

func (b *QdrantBackend) ensureCollection() error {
	// Check if collection exists
	resp, err := b.client.Get(fmt.Sprintf("%s/collections/%s", b.qdrantURL, b.collection))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil // exists
	}
	// Create it
	body, _ := json.Marshal(map[string]any{
		"vectors": map[string]any{
			"size":     vectorDim,
			"distance": "Cosine",
		},
		"on_disk_payload": true,
	})
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/collections/%s", b.qdrantURL, b.collection),
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	cr, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer cr.Body.Close()
	if cr.StatusCode >= 300 {
		raw, _ := io.ReadAll(cr.Body)
		return fmt.Errorf("create collection status %d: %s", cr.StatusCode, raw)
	}
	return nil
}

func (b *QdrantBackend) upsert(ctx context.Context, id string, vec []float32, payload map[string]any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Qdrant requires UUID or unsigned int point IDs.
	qdrantID := stringToUUID(id)
	body, _ := json.Marshal(map[string]any{
		"points": []map[string]any{
			{"id": qdrantID, "vector": vec, "payload": payload},
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("%s/collections/%s/points", b.qdrantURL, b.collection),
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upsert status %d: %s", resp.StatusCode, raw)
	}
	return nil
}

type qdrantSearchResult struct {
	Result []struct {
		ID      json.RawMessage `json:"id"` // may be string UUID or numeric integer
		Score   float64         `json:"score"`
		Payload map[string]any  `json:"payload"`
	} `json:"result"`
}

// qdrantIDToString coerces a Qdrant point ID (which may be a JSON string or
// a JSON number) to a plain Go string so callers don't need to branch.
func qdrantIDToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// JSON string — strip quotes.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	// JSON number — return the decimal representation as-is.
	return string(raw)
}

func (b *QdrantBackend) vectorSearch(ctx context.Context, vec []float32, limit int, filter map[string]any) ([]IndexedMemory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req := map[string]any{
		"vector":       vec,
		"limit":        limit,
		"with_payload": true,
	}
	if filter != nil {
		req["filter"] = filter
	}
	body, _ := json.Marshal(req)
	reqHTTP, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/collections/%s/points/search", b.qdrantURL, b.collection),
		bytes.NewReader(body))
	reqHTTP.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(reqHTTP)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var sr qdrantSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}
	out := make([]IndexedMemory, 0, len(sr.Result))
	for _, r := range sr.Result {
		m := payloadToIndexedMemory(qdrantIDToString(r.ID), r.Payload)
		out = append(out, m)
	}
	return out, nil
}

func payloadToIndexedMemory(id string, p map[string]any) IndexedMemory {
	m := IndexedMemory{MemoryID: id}
	if v, ok := p["text"].(string); ok {
		m.Text = v
	}
	if v, ok := p["session_id"].(string); ok {
		m.SessionID = v
	}
	if v, ok := p["role"].(string); ok {
		m.Role = v
	}
	if v, ok := p["topic"].(string); ok {
		m.Topic = v
	}
	if v, ok := p["unix"].(float64); ok {
		m.Unix = int64(v)
	}
	if v, ok := p["keywords"].([]any); ok {
		for _, kw := range v {
			if s, ok := kw.(string); ok {
				m.Keywords = append(m.Keywords, s)
			}
		}
	}
	return m
}

// -- Backend interface --

func (b *QdrantBackend) Add(doc state.MemoryDoc) {
	b.AddWithContext(context.Background(), doc)
}

func (b *QdrantBackend) AddWithContext(ctx context.Context, doc state.MemoryDoc) {
	text := strings.TrimSpace(doc.Text)
	if text == "" {
		return
	}
	vec, err := b.embed(ctx, text)
	if err != nil {
		log.Printf("qdrant: embed failed: %v", err)
		return
	}
	id := doc.MemoryID
	if id == "" {
		id = randomID()
	}
	unix := doc.Unix
	if unix == 0 {
		unix = time.Now().Unix()
	}
	payload := map[string]any{
		"text":       text,
		"session_id": doc.SessionID,
		"role":       doc.Role,
		"unix":       unix,
	}
	if topic := strings.TrimSpace(doc.Topic); topic != "" {
		payload["topic"] = topic
	}
	if len(doc.Keywords) > 0 {
		payload["keywords"] = append([]string(nil), doc.Keywords...)
	}
	if err := b.upsert(ctx, id, vec, payload); err != nil {
		log.Printf("qdrant: upsert failed: %v", err)
	}
}

func (b *QdrantBackend) Store(sessionID, text string, tags []string) string {
	id := randomID()
	vec, err := b.embed(context.Background(), text)
	if err != nil {
		log.Printf("qdrant: embed failed: %v", err)
		return id
	}
	payload := map[string]any{
		"text":       text,
		"session_id": sessionID,
		"unix":       time.Now().Unix(),
	}
	if len(tags) > 0 {
		payload["keywords"] = append([]string(nil), tags...)
	}
	if err := b.upsert(context.Background(), id, vec, payload); err != nil {
		log.Printf("qdrant: upsert failed: %v", err)
	}
	return id
}

func (b *QdrantBackend) Search(query string, limit int) []IndexedMemory {
	return b.SearchWithContext(context.Background(), query, limit)
}

func (b *QdrantBackend) SearchWithContext(ctx context.Context, query string, limit int) []IndexedMemory {
	vec, err := b.embed(ctx, query)
	if err != nil {
		log.Printf("qdrant: embed for search failed: %v", err)
		return nil
	}
	results, err := b.vectorSearch(ctx, vec, limit, nil)
	if err != nil {
		log.Printf("qdrant: search failed: %v", err)
		return nil
	}
	return results
}

func (b *QdrantBackend) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	return b.SearchSessionWithContext(context.Background(), sessionID, query, limit)
}

func (b *QdrantBackend) SearchSessionWithContext(ctx context.Context, sessionID, query string, limit int) []IndexedMemory {
	vec, err := b.embed(ctx, query)
	if err != nil {
		return nil
	}
	filter := map[string]any{
		"must": []map[string]any{
			{"key": "session_id", "match": map[string]any{"value": sessionID}},
		},
	}
	results, err := b.vectorSearch(ctx, vec, limit, filter)
	if err != nil {
		log.Printf("qdrant: session search failed: %v", err)
		return nil
	}
	return results
}

func (b *QdrantBackend) ListSession(sessionID string, limit int) []IndexedMemory {
	// Use a neutral embedding to get recent entries, filtered by session
	// Fallback: scroll API
	body, _ := json.Marshal(map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": "session_id", "match": map[string]any{"value": sessionID}},
			},
		},
		"limit":        limit,
		"with_payload": true,
		"order_by":     map[string]any{"key": "unix", "direction": "desc"},
	})
	resp, err := b.client.Post(
		fmt.Sprintf("%s/collections/%s/points/scroll", b.qdrantURL, b.collection),
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var sr struct {
		Result struct {
			Points []struct {
				ID      json.RawMessage `json:"id"`
				Payload map[string]any  `json:"payload"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil
	}
	out := make([]IndexedMemory, 0)
	for _, p := range sr.Result.Points {
		out = append(out, payloadToIndexedMemory(qdrantIDToString(p.ID), p.Payload))
	}
	return out
}

func (b *QdrantBackend) Count() int {
	resp, err := b.client.Get(fmt.Sprintf("%s/collections/%s", b.qdrantURL, b.collection))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var info struct {
		Result struct {
			PointsCount int `json:"points_count"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0
	}
	return info.Result.PointsCount
}

func (b *QdrantBackend) SessionCount() int { return 0 } // not efficient in Qdrant without aggregation

func (b *QdrantBackend) ListByTopic(topic string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}
	body, _ := json.Marshal(map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": "topic", "match": map[string]any{"value": topic}},
			},
		},
		"limit":        limit,
		"with_payload": true,
		"order_by":     map[string]any{"key": "unix", "direction": "desc"},
	})
	resp, err := b.client.Post(
		fmt.Sprintf("%s/collections/%s/points/scroll", b.qdrantURL, b.collection),
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var sr struct {
		Result struct {
			Points []struct {
				ID      json.RawMessage `json:"id"`
				Payload map[string]any  `json:"payload"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil
	}
	out := make([]IndexedMemory, 0)
	for _, p := range sr.Result.Points {
		out = append(out, payloadToIndexedMemory(qdrantIDToString(p.ID), p.Payload))
	}
	return out
}

func (b *QdrantBackend) Compact(maxEntries int) int { return 0 } // Qdrant manages its own storage

func (b *QdrantBackend) Save() error { return nil } // Qdrant persists automatically

func (b *QdrantBackend) Delete(id string) bool {
	body, _ := json.Marshal(map[string]any{
		"points": []string{stringToUUID(id)},
	})
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/collections/%s/points/delete", b.qdrantURL, b.collection),
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 300
}

func (b *QdrantBackend) Close() error { return nil }

func randomID() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return toUUID(buf)
}

// toUUID formats 16 bytes as a RFC-4122 UUID string.
func toUUID(b []byte) string {
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// stringToUUID converts any string to a deterministic UUID v5-like identifier.
func stringToUUID(s string) string {
	h := sha256.Sum256([]byte(s))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// HybridIndex wraps the existing JSON-FTS Index and mirrors writes to a Backend.
// Reads prefer the Backend (semantic), falling back to the JSON index.
type HybridIndex struct {
	*Index
	backend Backend
}

func (h *HybridIndex) AddWithContext(ctx context.Context, doc state.MemoryDoc) {
	h.Index.Add(doc)
	if ctxBackend, ok := h.backend.(interface {
		AddWithContext(context.Context, state.MemoryDoc)
	}); ok {
		ctxBackend.AddWithContext(ctx, doc)
		return
	}
	h.persistToBackend(doc)
}

func (h *HybridIndex) SearchWithContext(ctx context.Context, query string, limit int) []IndexedMemory {
	if ctxBackend, ok := h.backend.(interface {
		SearchWithContext(context.Context, string, int) []IndexedMemory
	}); ok {
		results := ctxBackend.SearchWithContext(ctx, query, limit)
		if len(results) > 0 {
			return results
		}
	} else {
		results := h.backend.Search(query, limit)
		if len(results) > 0 {
			return results
		}
	}
	return h.Index.Search(query, limit)
}

func (h *HybridIndex) SearchSessionWithContext(ctx context.Context, sessionID, query string, limit int) []IndexedMemory {
	if ctxBackend, ok := h.backend.(interface {
		SearchSessionWithContext(context.Context, string, string, int) []IndexedMemory
	}); ok {
		results := ctxBackend.SearchSessionWithContext(ctx, sessionID, query, limit)
		if len(results) > 0 {
			return results
		}
	} else {
		results := h.backend.SearchSession(sessionID, query, limit)
		if len(results) > 0 {
			return results
		}
	}
	return h.Index.SearchSession(sessionID, query, limit)
}

// NewHybridIndex creates a HybridIndex that writes to both stores.
func NewHybridIndex(idx *Index, backend Backend) *HybridIndex {
	return &HybridIndex{Index: idx, backend: backend}
}

func (h *HybridIndex) Search(query string, limit int) []IndexedMemory {
	return h.SearchWithContext(context.Background(), query, limit)
}

func (h *HybridIndex) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	return h.SearchSessionWithContext(context.Background(), sessionID, query, limit)
}

// persistToBackend asynchronously mirrors an Add to the vector backend.
func (h *HybridIndex) persistToBackend(doc state.MemoryDoc) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = ctx
		h.backend.Add(doc)
	}()
}
func (h *HybridIndex) Add(doc state.MemoryDoc) {
	h.AddWithContext(context.Background(), doc)
}

// Store is the interface satisfied by both *Index and *HybridIndex.
type Store interface {
	Add(doc state.MemoryDoc)
	Search(query string, limit int) []IndexedMemory
	SearchSession(sessionID, query string, limit int) []IndexedMemory
	ListSession(sessionID string, limit int) []IndexedMemory
	// ListByTopic returns all entries with the given topic, newest-first.
	// Used to surface pinned agent knowledge into the system prompt.
	ListByTopic(topic string, limit int) []IndexedMemory
	Count() int
	SessionCount() int
	// Compact removes the oldest entries to keep total count below maxEntries.
	// Returns the number of removed entries.
	Compact(maxEntries int) int
	Save() error
	Store(sessionID, text string, tags []string) string
	Delete(id string) bool
}
