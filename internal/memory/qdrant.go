// Package memory - Qdrant backend for semantic memory search.
// Uses Ollama (nomic-embed-text) for embeddings + Qdrant for vector storage.
package memory

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

const (
	qdrantDefaultURL    = "http://localhost:6333"
	ollamaDefaultURL    = "http://localhost:11434"
	defaultCollection   = "metiq"
	embedModel          = "nomic-embed-text"
	vectorDim           = 768
	qdrantRetryBackoff  = 5 * time.Second
	qdrantRetryMaxDelay = 1 * time.Minute
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
	mu                  sync.Mutex
	qdrantURL           string
	ollamaURL           string
	collection          string
	client              *http.Client
	degraded            bool
	lastError           string
	lastFailureDomain   string
	lastFailureAt       time.Time
	nextRetryAt         time.Time
	consecutiveFailures int
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
	req, err := newQdrantJSONRequest(ctx, http.MethodPost, b.ollamaURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, &qdrantHTTPError{Operation: "ollama embed", StatusCode: resp.StatusCode, Body: string(raw)}
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

// -- Qdrant helpers --

func newQdrantRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func newQdrantJSONRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := newQdrantRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (b *QdrantBackend) ensureCollection() error {
	return b.ensureCollectionWithContext(context.Background())
}

func (b *QdrantBackend) ensureCollectionWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	lookupReq, err := newQdrantRequest(ctx, http.MethodGet,
		fmt.Sprintf("%s/collections/%s", b.qdrantURL, b.collection), nil)
	if err != nil {
		return err
	}
	resp, err := b.client.Do(lookupReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		return &qdrantHTTPError{Operation: "ensure collection lookup", StatusCode: resp.StatusCode, Body: string(raw)}
	}
	body, _ := json.Marshal(map[string]any{
		"vectors": map[string]any{
			"size":     vectorDim,
			"distance": "Cosine",
		},
		"on_disk_payload": true,
	})
	createReq, err := newQdrantJSONRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/collections/%s", b.qdrantURL, b.collection),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	cr, err := b.client.Do(createReq)
	if err != nil {
		return err
	}
	defer cr.Body.Close()
	if cr.StatusCode >= 300 {
		raw, _ := io.ReadAll(cr.Body)
		return &qdrantHTTPError{Operation: "create collection", StatusCode: cr.StatusCode, Body: string(raw)}
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
	req, err := newQdrantJSONRequest(ctx, http.MethodPut,
		fmt.Sprintf("%s/collections/%s/points", b.qdrantURL, b.collection),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
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

const (
	qdrantFailureDomainEmbedding = "embedding"
	qdrantFailureDomainStorage   = "storage"
)

type qdrantHTTPError struct {
	Operation  string
	StatusCode int
	Body       string
}

func (e *qdrantHTTPError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("%s status %d", e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("%s status %d: %s", e.Operation, e.StatusCode, e.Body)
}

func isMissingCollectionError(err error) bool {
	var httpErr *qdrantHTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "doesn't exist")
}

func (b *QdrantBackend) BackendStatus() BackendStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	status := BackendStatus{
		Name:                "qdrant",
		Available:           !b.degraded,
		Degraded:            b.degraded,
		LastError:           b.lastError,
		ConsecutiveFailures: b.consecutiveFailures,
	}
	if !b.lastFailureAt.IsZero() {
		status.LastFailureUnix = b.lastFailureAt.Unix()
	}
	if !b.nextRetryAt.IsZero() {
		status.NextRetryUnix = b.nextRetryAt.Unix()
	}
	return status
}

func (b *QdrantBackend) MemoryStatus() StoreStatus {
	primary := b.BackendStatus()
	return StoreStatus{Kind: "backend", Primary: primary}
}

func (b *QdrantBackend) recordSuccess(domain string) {
	b.mu.Lock()
	if b.lastFailureDomain != "" && b.lastFailureDomain != domain {
		b.mu.Unlock()
		return
	}
	recovered := b.degraded
	b.degraded = false
	b.lastError = ""
	b.lastFailureDomain = ""
	b.nextRetryAt = time.Time{}
	b.consecutiveFailures = 0
	b.mu.Unlock()
	if recovered {
		log.Printf("qdrant: backend recovered collection=%s", b.collection)
	}
}

func (b *QdrantBackend) recordFailure(domain, operation string, err error) {
	if err == nil {
		return
	}
	now := time.Now()
	b.mu.Lock()
	b.degraded = true
	b.lastError = fmt.Sprintf("%s: %v", operation, err)
	b.lastFailureDomain = domain
	b.lastFailureAt = now
	b.consecutiveFailures++
	delay := qdrantRetryBackoff
	for attempt := 1; attempt < b.consecutiveFailures && delay < qdrantRetryMaxDelay; attempt++ {
		delay *= 2
		if delay > qdrantRetryMaxDelay {
			delay = qdrantRetryMaxDelay
			break
		}
	}
	b.nextRetryAt = now.Add(delay)
	b.mu.Unlock()
}

func (b *QdrantBackend) ensureReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	degraded := b.degraded
	nextRetryAt := b.nextRetryAt
	b.mu.Unlock()
	if !degraded {
		return nil
	}
	if !nextRetryAt.IsZero() && time.Now().Before(nextRetryAt) {
		return fmt.Errorf("qdrant backend degraded; retry after %s", nextRetryAt.UTC().Format(time.RFC3339))
	}
	if err := b.ensureCollectionWithContext(ctx); err != nil {
		b.recordFailure(qdrantFailureDomainStorage, "probe", err)
		return fmt.Errorf("qdrant backend probe failed: %w", err)
	}
	return nil
}

func (b *QdrantBackend) runOperation(ctx context.Context, operation string, fn func() error) error {
	err := fn()
	if err == nil {
		b.recordSuccess(qdrantFailureDomainStorage)
		return nil
	}
	if isMissingCollectionError(err) {
		if repairErr := b.ensureCollectionWithContext(ctx); repairErr == nil {
			err = fn()
			if err == nil {
				b.recordSuccess(qdrantFailureDomainStorage)
				return nil
			}
		} else {
			err = fmt.Errorf("%v; collection repair failed: %w", err, repairErr)
		}
	}
	b.recordFailure(qdrantFailureDomainStorage, operation, err)
	return err
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
	reqHTTP, err := newQdrantJSONRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s/collections/%s/points/search", b.qdrantURL, b.collection),
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	resp, err := b.client.Do(reqHTTP)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, &qdrantHTTPError{Operation: "vector search", StatusCode: resp.StatusCode, Body: string(raw)}
	}
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
	if v, ok := p["mem_type"].(string); ok {
		m.Type = v
	}
	if v, ok := p["goal_id"].(string); ok {
		m.GoalID = v
	}
	if v, ok := p["task_id"].(string); ok {
		m.TaskID = v
	}
	if v, ok := p["run_id"].(string); ok {
		m.RunID = v
	}
	if v, ok := p["episode_kind"].(string); ok {
		m.EpisodeKind = v
	}
	if v, ok := p["confidence"].(float64); ok {
		m.Confidence = v
	}
	if v, ok := p["source"].(string); ok {
		m.Source = v
	}
	if v, ok := p["reviewed_at"].(float64); ok {
		m.ReviewedAt = int64(v)
	}
	if v, ok := p["reviewed_by"].(string); ok {
		m.ReviewedBy = v
	}
	if v, ok := p["expires_at"].(float64); ok {
		m.ExpiresAt = int64(v)
	}
	if v, ok := p["mem_status"].(string); ok {
		m.MemStatus = v
	}
	if v, ok := p["superseded_by"].(string); ok {
		m.SupersededBy = v
	}
	if v, ok := p["invalidated_at"].(float64); ok {
		m.InvalidatedAt = int64(v)
	}
	if v, ok := p["invalidated_by"].(string); ok {
		m.InvalidatedBy = v
	}
	if v, ok := p["invalidate_reason"].(string); ok {
		m.InvalidateReason = v
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
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: add skipped (fallback active): %v", err)
		return
	}
	vec, err := b.embed(ctx, text)
	if err != nil {
		b.recordFailure(qdrantFailureDomainEmbedding, "embed add", err)
		log.Printf("qdrant: embed failed: %v", err)
		return
	}
	b.recordSuccess(qdrantFailureDomainEmbedding)
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
	if doc.Type != "" {
		payload["mem_type"] = doc.Type
	}
	if doc.GoalID != "" {
		payload["goal_id"] = doc.GoalID
	}
	if doc.TaskID != "" {
		payload["task_id"] = doc.TaskID
	}
	if doc.RunID != "" {
		payload["run_id"] = doc.RunID
	}
	if doc.EpisodeKind != "" {
		payload["episode_kind"] = doc.EpisodeKind
	}
	if doc.Confidence != 0 {
		payload["confidence"] = doc.Confidence
	}
	if doc.Source != "" {
		payload["source"] = doc.Source
	}
	if doc.ReviewedAt != 0 {
		payload["reviewed_at"] = doc.ReviewedAt
	}
	if doc.ReviewedBy != "" {
		payload["reviewed_by"] = doc.ReviewedBy
	}
	if doc.ExpiresAt != 0 {
		payload["expires_at"] = doc.ExpiresAt
	}
	if doc.MemStatus != "" {
		payload["mem_status"] = doc.MemStatus
	}
	if doc.SupersededBy != "" {
		payload["superseded_by"] = doc.SupersededBy
	}
	if doc.InvalidatedAt != 0 {
		payload["invalidated_at"] = doc.InvalidatedAt
	}
	if doc.InvalidatedBy != "" {
		payload["invalidated_by"] = doc.InvalidatedBy
	}
	if doc.InvalidateReason != "" {
		payload["invalidate_reason"] = doc.InvalidateReason
	}
	if err := b.runOperation(ctx, "upsert", func() error { return b.upsert(ctx, id, vec, payload) }); err != nil {
		log.Printf("qdrant: upsert failed: %v", err)
	}
}

func (b *QdrantBackend) Store(sessionID, text string, tags []string) string {
	id := randomID()
	ctx := context.Background()
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: store skipped (fallback active): %v", err)
		return ""
	}
	vec, err := b.embed(ctx, text)
	if err != nil {
		b.recordFailure(qdrantFailureDomainEmbedding, "embed store", err)
		log.Printf("qdrant: embed failed: %v", err)
		return ""
	}
	b.recordSuccess(qdrantFailureDomainEmbedding)
	payload := map[string]any{
		"text":       text,
		"session_id": sessionID,
		"unix":       time.Now().Unix(),
	}
	if len(tags) > 0 {
		payload["keywords"] = append([]string(nil), tags...)
	}
	if err := b.runOperation(ctx, "store upsert", func() error { return b.upsert(ctx, id, vec, payload) }); err != nil {
		log.Printf("qdrant: upsert failed: %v", err)
		return ""
	}
	return id
}

func (b *QdrantBackend) Search(query string, limit int) []IndexedMemory {
	return b.SearchWithContext(context.Background(), query, limit)
}

func (b *QdrantBackend) SearchWithContext(ctx context.Context, query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 20
	}
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: search fallback active: %v", err)
		return nil
	}
	vec, err := b.embed(ctx, query)
	if err != nil {
		b.recordFailure(qdrantFailureDomainEmbedding, "embed search", err)
		log.Printf("qdrant: embed for search failed: %v", err)
		return nil
	}
	b.recordSuccess(qdrantFailureDomainEmbedding)
	var results []IndexedMemory
	if err := b.runOperation(ctx, "search", func() error {
		var searchErr error
		results, searchErr = b.vectorSearch(ctx, vec, limit, nil)
		return searchErr
	}); err != nil {
		log.Printf("qdrant: search failed: %v", err)
		return nil
	}
	return results
}

func (b *QdrantBackend) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	return b.SearchSessionWithContext(context.Background(), sessionID, query, limit)
}

func (b *QdrantBackend) SearchSessionWithContext(ctx context.Context, sessionID, query string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 8
	}
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: session search fallback active: %v", err)
		return nil
	}
	vec, err := b.embed(ctx, query)
	if err != nil {
		b.recordFailure(qdrantFailureDomainEmbedding, "embed session search", err)
		log.Printf("qdrant: embed for session search failed: %v", err)
		return nil
	}
	b.recordSuccess(qdrantFailureDomainEmbedding)
	filter := map[string]any{
		"must": []map[string]any{
			{"key": "session_id", "match": map[string]any{"value": sessionID}},
		},
	}
	var results []IndexedMemory
	if err := b.runOperation(ctx, "session search", func() error {
		var searchErr error
		results, searchErr = b.vectorSearch(ctx, vec, limit, filter)
		return searchErr
	}); err != nil {
		log.Printf("qdrant: session search failed: %v", err)
		return nil
	}
	return results
}

func (b *QdrantBackend) ListSession(sessionID string, limit int) []IndexedMemory {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	ctx := context.Background()
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: list session fallback active: %v", err)
		return nil
	}
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
	var out []IndexedMemory
	if err := b.runOperation(ctx, "list session", func() error {
		req, err := newQdrantJSONRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/collections/%s/points/scroll", b.qdrantURL, b.collection),
			bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp, err := b.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			return &qdrantHTTPError{Operation: "list session", StatusCode: resp.StatusCode, Body: string(raw)}
		}
		var sr struct {
			Result struct {
				Points []struct {
					ID      json.RawMessage `json:"id"`
					Payload map[string]any  `json:"payload"`
				} `json:"points"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			return err
		}
		out = make([]IndexedMemory, 0, len(sr.Result.Points))
		for _, p := range sr.Result.Points {
			out = append(out, payloadToIndexedMemory(qdrantIDToString(p.ID), p.Payload))
		}
		return nil
	}); err != nil {
		log.Printf("qdrant: list session failed: %v", err)
		return nil
	}
	return out
}

func (b *QdrantBackend) Count() int {
	ctx := context.Background()
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: count fallback active: %v", err)
		return 0
	}
	count := 0
	if err := b.runOperation(ctx, "count", func() error {
		req, err := newQdrantRequest(ctx, http.MethodGet,
			fmt.Sprintf("%s/collections/%s", b.qdrantURL, b.collection), nil)
		if err != nil {
			return err
		}
		resp, err := b.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			return &qdrantHTTPError{Operation: "count", StatusCode: resp.StatusCode, Body: string(raw)}
		}
		var info struct {
			Result struct {
				PointsCount int `json:"points_count"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return err
		}
		count = info.Result.PointsCount
		return nil
	}); err != nil {
		log.Printf("qdrant: count failed: %v", err)
		return 0
	}
	return count
}

// SessionCount returns -1 for Qdrant because an efficient distinct-session
// count is not supported without a full aggregation scan. Callers should
// treat negative values as "unsupported".
func (b *QdrantBackend) SessionCount() int { return -1 }

func (b *QdrantBackend) pointExists(ctx context.Context, id string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := newQdrantRequest(ctx, http.MethodGet,
		fmt.Sprintf("%s/collections/%s/points/%s", b.qdrantURL, b.collection, stringToUUID(id)), nil)
	if err != nil {
		return false, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		if err := b.ensureCollectionWithContext(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return false, &qdrantHTTPError{Operation: "point exists", StatusCode: resp.StatusCode, Body: string(raw)}
	}
	return true, nil
}

func (b *QdrantBackend) ListByTopic(topic string, limit int) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}
	ctx := context.Background()
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: topic list fallback active: %v", err)
		return nil
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
	var out []IndexedMemory
	if err := b.runOperation(ctx, "list topic", func() error {
		req, err := newQdrantJSONRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/collections/%s/points/scroll", b.qdrantURL, b.collection),
			bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp, err := b.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			return &qdrantHTTPError{Operation: "list topic", StatusCode: resp.StatusCode, Body: string(raw)}
		}
		var sr struct {
			Result struct {
				Points []struct {
					ID      json.RawMessage `json:"id"`
					Payload map[string]any  `json:"payload"`
				} `json:"points"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			return err
		}
		out = make([]IndexedMemory, 0, len(sr.Result.Points))
		for _, p := range sr.Result.Points {
			out = append(out, payloadToIndexedMemory(qdrantIDToString(p.ID), p.Payload))
		}
		return nil
	}); err != nil {
		log.Printf("qdrant: topic list failed: %v", err)
		return nil
	}
	return out
}

func (b *QdrantBackend) ListByType(memType string, limit int) []IndexedMemory {
	return b.scrollByField("mem_type", memType, limit, "list type")
}

func (b *QdrantBackend) ListByTaskID(taskID string, limit int) []IndexedMemory {
	return b.scrollByField("task_id", taskID, limit, "list task_id")
}

// scrollByField is a generic scroll-by-exact-match helper used by ListByTopic,
// ListByType, and ListByTaskID.
func (b *QdrantBackend) scrollByField(field, value string, limit int, opName string) []IndexedMemory {
	if limit <= 0 {
		limit = 100
	}
	ctx := context.Background()
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: %s fallback active: %v", opName, err)
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": field, "match": map[string]any{"value": value}},
			},
		},
		"limit":        limit,
		"with_payload": true,
		"order_by":     map[string]any{"key": "unix", "direction": "desc"},
	})
	var out []IndexedMemory
	if err := b.runOperation(ctx, opName, func() error {
		req, err := newQdrantJSONRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/collections/%s/points/scroll", b.qdrantURL, b.collection),
			bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp, err := b.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			return &qdrantHTTPError{Operation: opName, StatusCode: resp.StatusCode, Body: string(raw)}
		}
		var sr struct {
			Result struct {
				Points []struct {
					ID      json.RawMessage `json:"id"`
					Payload map[string]any  `json:"payload"`
				} `json:"points"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			return err
		}
		out = make([]IndexedMemory, 0, len(sr.Result.Points))
		for _, p := range sr.Result.Points {
			out = append(out, payloadToIndexedMemory(qdrantIDToString(p.ID), p.Payload))
		}
		return nil
	}); err != nil {
		log.Printf("qdrant: %s failed: %v", opName, err)
		return nil
	}
	return out
}

func (b *QdrantBackend) Compact(maxEntries int) int { return 0 } // Qdrant manages its own storage

func (b *QdrantBackend) Save() error { return nil } // Qdrant persists automatically

func (b *QdrantBackend) Delete(id string) bool {
	ctx := context.Background()
	if err := b.ensureReady(ctx); err != nil {
		log.Printf("qdrant: delete skipped (fallback active): %v", err)
		return false
	}
	exists := false
	if err := b.runOperation(ctx, "delete preflight", func() error {
		var checkErr error
		exists, checkErr = b.pointExists(ctx, id)
		return checkErr
	}); err != nil {
		log.Printf("qdrant: delete preflight failed: %v", err)
		return false
	}
	if !exists {
		return false
	}
	body, _ := json.Marshal(map[string]any{
		"points": []string{stringToUUID(id)},
	})
	if err := b.runOperation(ctx, "delete", func() error {
		req, err := newQdrantJSONRequest(ctx, http.MethodPost,
			fmt.Sprintf("%s/collections/%s/points/delete", b.qdrantURL, b.collection),
			bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp, err := b.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			return &qdrantHTTPError{Operation: "delete", StatusCode: resp.StatusCode, Body: string(raw)}
		}
		return nil
	}); err != nil {
		log.Printf("qdrant: delete failed: %v", err)
		return false
	}
	return true
}

func (b *QdrantBackend) Close() error { return nil }

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Fallback: use current time + zero padding — extremely unlikely path.
		now := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			buf[i] = byte(now >> (i * 8))
		}
	}
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
	semOnce sync.Once
	semCh   chan struct{}
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
// maxPersistConcurrency limits the number of outstanding async backend writes.
const maxPersistConcurrency = 8

func NewHybridIndex(idx *Index, backend Backend) *HybridIndex {
	return &HybridIndex{Index: idx, backend: backend}
}

// persistSem returns a lazily-initialised semaphore channel for bounding
// concurrent async backend writes.
func (h *HybridIndex) persistSem() chan struct{} {
	h.semOnce.Do(func() {
		h.semCh = make(chan struct{}, maxPersistConcurrency)
	})
	return h.semCh
}

func (h *HybridIndex) MemoryStatus() StoreStatus {
	primary := BackendStatus{Name: fmt.Sprintf("%T", h.backend), Available: h.backend != nil}
	if reporter, ok := h.backend.(interface{ BackendStatus() BackendStatus }); ok {
		primary = reporter.BackendStatus()
	}
	fallback := &BackendStatus{Name: "json-fts", Available: h.Index != nil}
	if h.Index != nil {
		fallback.Available = true
	}
	return StoreStatus{
		Kind:           "hybrid",
		FallbackActive: primary.Degraded || !primary.Available,
		Primary:        primary,
		Fallback:       fallback,
	}
}

func (h *HybridIndex) Search(query string, limit int) []IndexedMemory {
	return h.SearchWithContext(context.Background(), query, limit)
}

func (h *HybridIndex) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	return h.SearchSessionWithContext(context.Background(), sessionID, query, limit)
}

func (h *HybridIndex) Store(sessionID, text string, tags []string) string {
	id := GenerateMemoryID()
	h.AddWithContext(context.Background(), state.MemoryDoc{
		MemoryID:  id,
		SessionID: sessionID,
		Text:      text,
		Keywords:  append([]string(nil), tags...),
		Unix:      time.Now().Unix(),
	})
	return id
}

func (h *HybridIndex) Delete(id string) bool {
	indexDeleted := h.Index.Delete(id)
	backendDeleted := false
	if h.backend != nil {
		backendDeleted = h.backend.Delete(id)
		if indexDeleted && !backendDeleted {
			log.Printf("memory hybrid: backend delete missed for %q after JSON index delete", id)
		}
	}
	return indexDeleted || backendDeleted
}

func (h *HybridIndex) ListSession(sessionID string, limit int) []IndexedMemory {
	effectiveLimit := hybridListLimit(limit, 20)
	local := h.Index.ListSession(sessionID, effectiveLimit)
	if h.backend == nil {
		return local
	}
	backend := h.backend.ListSession(sessionID, effectiveLimit)
	return mergeHybridExactList(backend, local, effectiveLimit)
}

func (h *HybridIndex) ListByTopic(topic string, limit int) []IndexedMemory {
	effectiveLimit := hybridListLimit(limit, 100)
	local := h.Index.ListByTopic(topic, effectiveLimit)
	if h.backend == nil {
		return local
	}
	backend := h.backend.ListByTopic(topic, effectiveLimit)
	return mergeHybridExactList(backend, local, effectiveLimit)
}

func (h *HybridIndex) Compact(maxEntries int) int {
	if maxEntries < 0 {
		maxEntries = 0
	}
	h.Index.mu.Lock()
	if len(h.Index.docs) <= maxEntries {
		h.Index.mu.Unlock()
		return 0
	}
	entries := make([]IndexedMemory, 0, len(h.Index.docs))
	for _, d := range h.Index.docs {
		entries = append(entries, d)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Unix < entries[b].Unix })
	toRemove := len(entries) - maxEntries
	removedIDs := make([]string, 0, toRemove)
	for idx := 0; idx < toRemove; idx++ {
		removedIDs = append(removedIDs, entries[idx].MemoryID)
		delete(h.Index.docs, entries[idx].MemoryID)
	}
	h.Index.rebuildTokenMapLocked()
	h.Index.clearCacheLocked()
	h.Index.mu.Unlock()
	if h.backend != nil {
		for _, id := range removedIDs {
			if !h.backend.Delete(id) {
				log.Printf("memory hybrid: backend delete missed for compacted memory %q", id)
			}
		}
	}
	return toRemove
}

func hybridListLimit(limit, defaultLimit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	return limit
}

func mergeHybridExactList(backend, local []IndexedMemory, limit int) []IndexedMemory {
	if len(backend) == 0 {
		return cloneMemories(local)
	}
	if len(local) == 0 {
		return cloneMemories(backend)
	}
	byID := make(map[string]IndexedMemory, len(backend)+len(local))
	anonymous := make([]IndexedMemory, 0)
	for _, mem := range backend {
		if mem.MemoryID == "" {
			anonymous = append(anonymous, mem)
			continue
		}
		byID[mem.MemoryID] = mem
	}
	// The JSON index is the local source of truth for exact enumerations. Let it
	// overwrite matching backend payloads so metadata and deletes repaired in the
	// fallback index are reflected whenever the backend returns overlapping hits.
	for _, mem := range local {
		if mem.MemoryID == "" {
			anonymous = append(anonymous, mem)
			continue
		}
		byID[mem.MemoryID] = mem
	}
	merged := make([]IndexedMemory, 0, len(byID)+len(anonymous))
	for _, mem := range byID {
		merged = append(merged, mem)
	}
	merged = append(merged, anonymous...)
	sort.Slice(merged, func(a, b int) bool {
		if merged[a].Unix == merged[b].Unix {
			return merged[a].MemoryID > merged[b].MemoryID
		}
		return merged[a].Unix > merged[b].Unix
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return cloneMemories(merged)
}

// persistToBackend asynchronously mirrors an Add to the vector backend.
// It enforces a timeout and limits concurrent outstanding writes to avoid
// unbounded goroutine fan-out under high write throughput.
func (h *HybridIndex) persistToBackend(doc state.MemoryDoc) {
	select {
	case h.persistSem() <- struct{}{}:
	default:
		// Concurrency limit reached; keep the JSON-FTS fallback and surface the
		// mirror miss in logs instead of silently losing backend/index parity.
		log.Printf("memory hybrid: backend mirror skipped for %q: concurrency limit reached", doc.MemoryID)
		return
	}
	go func() {
		defer func() { <-h.persistSem() }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if ctxBackend, ok := h.backend.(interface {
			AddWithContext(context.Context, state.MemoryDoc)
		}); ok {
			ctxBackend.AddWithContext(ctx, doc)
		} else {
			h.backend.Add(doc)
		}
	}()
}
func (h *HybridIndex) ListByType(memType string, limit int) []IndexedMemory {
	effectiveLimit := hybridListLimit(limit, 100)
	local := h.Index.ListByType(memType, effectiveLimit)
	if h.backend == nil {
		return local
	}
	backend := h.backend.ListByType(memType, effectiveLimit)
	return mergeHybridExactList(backend, local, effectiveLimit)
}

func (h *HybridIndex) ListByTaskID(taskID string, limit int) []IndexedMemory {
	effectiveLimit := hybridListLimit(limit, 100)
	local := h.Index.ListByTaskID(taskID, effectiveLimit)
	if h.backend == nil {
		return local
	}
	backend := h.backend.ListByTaskID(taskID, effectiveLimit)
	return mergeHybridExactList(backend, local, effectiveLimit)
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
	// ListByType returns all entries with the given memory type, newest-first.
	ListByType(memType string, limit int) []IndexedMemory
	// ListByTaskID returns all entries linked to the given task, newest-first.
	ListByTaskID(taskID string, limit int) []IndexedMemory
	Count() int
	SessionCount() int
	// Compact removes the oldest entries to keep total count below maxEntries.
	// Returns the number of removed entries.
	Compact(maxEntries int) int
	Save() error
	Store(sessionID, text string, tags []string) string
	Delete(id string) bool
}
