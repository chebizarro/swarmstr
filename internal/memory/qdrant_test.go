package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/store/state"
)

type ctxKey string

type contextAwareBackendStub struct {
	addCalled           bool
	searchCalled        bool
	searchSessionCalled bool
	ctxValue            string
	searchResult        []IndexedMemory
	sessionSearchResult []IndexedMemory
}

func (b *contextAwareBackendStub) Add(doc state.MemoryDoc) {}
func (b *contextAwareBackendStub) AddWithContext(ctx context.Context, doc state.MemoryDoc) {
	b.addCalled = true
	if v, _ := ctx.Value(ctxKey("marker")).(string); v != "" {
		b.ctxValue = v
	}
}
func (b *contextAwareBackendStub) Search(query string, limit int) []IndexedMemory {
	return b.searchResult
}
func (b *contextAwareBackendStub) SearchWithContext(ctx context.Context, query string, limit int) []IndexedMemory {
	b.searchCalled = true
	if v, _ := ctx.Value(ctxKey("marker")).(string); v != "" {
		b.ctxValue = v
	}
	return b.searchResult
}
func (b *contextAwareBackendStub) SearchSession(sessionID, query string, limit int) []IndexedMemory {
	return b.sessionSearchResult
}
func (b *contextAwareBackendStub) SearchSessionWithContext(ctx context.Context, sessionID, query string, limit int) []IndexedMemory {
	b.searchSessionCalled = true
	if v, _ := ctx.Value(ctxKey("marker")).(string); v != "" {
		b.ctxValue = v
	}
	return b.sessionSearchResult
}
func (b *contextAwareBackendStub) ListSession(sessionID string, limit int) []IndexedMemory {
	return nil
}
func (b *contextAwareBackendStub) BackendStatus() BackendStatus {
	return BackendStatus{Name: "stub", Available: true}
}
func (b *contextAwareBackendStub) Count() int                                          { return 0 }
func (b *contextAwareBackendStub) SessionCount() int                                   { return 0 }
func (b *contextAwareBackendStub) Compact(maxEntries int) int                          { return 0 }
func (b *contextAwareBackendStub) Save() error                                         { return nil }
func (b *contextAwareBackendStub) Store(sessionID, text string, tags []string) string  { return "" }
func (b *contextAwareBackendStub) Delete(id string) bool                               { return false }
func (b *contextAwareBackendStub) ListByTopic(topic string, limit int) []IndexedMemory { return nil }
func (b *contextAwareBackendStub) Close() error                                        { return nil }

func TestContextAwareHelpersUseHybridIndexContextMethods(t *testing.T) {
	idx, err := OpenIndex(filepath.Join(t.TempDir(), "memory.json"))
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	backend := &contextAwareBackendStub{
		searchResult:        []IndexedMemory{{MemoryID: "g1", Text: "global hit"}},
		sessionSearchResult: []IndexedMemory{{MemoryID: "s1", SessionID: "sess", Text: "session hit"}},
	}
	hybrid := NewHybridIndex(idx, backend)
	ctx := context.WithValue(context.Background(), ctxKey("marker"), "present")

	AddDoc(ctx, hybrid, state.MemoryDoc{MemoryID: "m1", SessionID: "sess", Text: "hello", Unix: 1})
	if !backend.addCalled {
		t.Fatal("expected AddDoc to call backend AddWithContext through HybridIndex")
	}
	if got := idx.Count(); got != 1 {
		t.Fatalf("expected base index to be updated, got count=%d", got)
	}

	global := SearchDocs(ctx, hybrid, "hello", 5)
	if !backend.searchCalled {
		t.Fatal("expected SearchDocs to call backend SearchWithContext through HybridIndex")
	}
	if len(global) != 1 || global[0].Text != "global hit" {
		t.Fatalf("unexpected global search result: %#v", global)
	}

	session := SearchSessionDocs(ctx, hybrid, "sess", "hello", 5)
	if !backend.searchSessionCalled {
		t.Fatal("expected SearchSessionDocs to call backend SearchSessionWithContext through HybridIndex")
	}
	if len(session) != 1 || session[0].Text != "session hit" {
		t.Fatalf("unexpected session search result: %#v", session)
	}
	if backend.ctxValue != "present" {
		t.Fatalf("expected context value to propagate, got %q", backend.ctxValue)
	}
}

func TestQdrantAddWithContextPersistsTopicKeywordsAndUnix(t *testing.T) {
	var upsert struct {
		Points []struct {
			Payload map[string]any `json:"payload"`
		} `json:"points"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embeddings":
			_ = json.NewDecoder(r.Body).Decode(&map[string]any{})
			_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1, 0.2, 0.3}})
		case "/collections/test/points":
			if err := json.NewDecoder(r.Body).Decode(&upsert); err != nil {
				t.Fatalf("decode upsert body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	backend := &QdrantBackend{
		qdrantURL:  server.URL,
		ollamaURL:  server.URL,
		collection: "test",
		client:     server.Client(),
	}
	backend.AddWithContext(context.Background(), state.MemoryDoc{
		MemoryID:  "mem-1",
		SessionID: "session-a",
		Text:      "remember this",
		Topic:     "project",
		Keywords:  []string{"project", "deadline"},
		Unix:      1712345678,
	})
	if len(upsert.Points) != 1 {
		t.Fatalf("expected one point payload, got %#v", upsert.Points)
	}
	payload := upsert.Points[0].Payload
	if got, _ := payload["topic"].(string); got != "project" {
		t.Fatalf("expected topic to persist, got %#v", payload["topic"])
	}
	keywords, _ := payload["keywords"].([]any)
	if len(keywords) != 2 || keywords[0] != "project" || keywords[1] != "deadline" {
		t.Fatalf("expected keywords to persist, got %#v", payload["keywords"])
	}
	if got, _ := payload["unix"].(float64); int64(got) != 1712345678 {
		t.Fatalf("expected unix to persist, got %#v", payload["unix"])
	}
}

func TestQdrantDeleteNormalizesIDs(t *testing.T) {
	var got struct {
		Points []string `json:"points"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/collections/test":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points_count": 0}})
		case "/collections/test/points/" + stringToUUID("plain-id"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": stringToUUID("plain-id")}})
		case "/collections/test/points/delete":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode delete body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	backend := &QdrantBackend{
		qdrantURL:  server.URL,
		collection: "test",
		client:     server.Client(),
	}
	if ok := backend.Delete("plain-id"); !ok {
		t.Fatal("expected delete to succeed")
	}
	if len(got.Points) != 1 {
		t.Fatalf("expected one point id, got %#v", got.Points)
	}
	if got.Points[0] != stringToUUID("plain-id") {
		t.Fatalf("expected normalized UUID %q, got %q", stringToUUID("plain-id"), got.Points[0])
	}
}

func TestQdrantSearchRepairsMissingCollectionAndClearsDegradedState(t *testing.T) {
	collectionExists := false
	collectionCreates := 0
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embeddings":
			_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1, 0.2, 0.3}})
		case "/collections/test":
			if r.Method == http.MethodGet {
				if collectionExists {
					_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points_count": 1}})
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`missing collection`))
				return
			}
			if r.Method == http.MethodPut {
				collectionCreates++
				collectionExists = true
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
				return
			}
			t.Fatalf("unexpected method: %s", r.Method)
		case "/collections/test/points/search":
			searchCalls++
			if !collectionExists {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`missing collection`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"id":      "mem-1",
					"score":   0.99,
					"payload": map[string]any{"text": "remembered", "session_id": "sess"},
				}},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	backend := &QdrantBackend{
		qdrantURL:  server.URL,
		ollamaURL:  server.URL,
		collection: "test",
		client:     server.Client(),
	}
	results := backend.SearchWithContext(context.Background(), "remember", 5)
	if len(results) != 1 || results[0].Text != "remembered" {
		t.Fatalf("expected repaired search result, got %#v", results)
	}
	if collectionCreates != 1 {
		t.Fatalf("expected one collection repair, got %d", collectionCreates)
	}
	if searchCalls != 2 {
		t.Fatalf("expected search retry after repair, got %d calls", searchCalls)
	}
	status := backend.BackendStatus()
	if status.Degraded {
		t.Fatalf("expected backend to recover, got %#v", status)
	}
}

func TestQdrantSearchBackoffSuppressesImmediateRetryUntilCooldownExpires(t *testing.T) {
	embedCalls := 0
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embeddings":
			embedCalls++
			if embedCalls == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`boom`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1, 0.2, 0.3}})
		case "/collections/test/points/search":
			searchCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{
					"id":      "mem-1",
					"score":   0.99,
					"payload": map[string]any{"text": "remembered"},
				}},
			})
		case "/collections/test":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points_count": 1}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	backend := &QdrantBackend{
		qdrantURL:  server.URL,
		ollamaURL:  server.URL,
		collection: "test",
		client:     server.Client(),
	}
	if got := backend.SearchWithContext(context.Background(), "remember", 5); got != nil {
		t.Fatalf("expected first search failure, got %#v", got)
	}
	status := backend.BackendStatus()
	if !status.Degraded || status.ConsecutiveFailures != 1 || status.NextRetryUnix == 0 {
		t.Fatalf("expected degraded status after failure, got %#v", status)
	}
	if !strings.Contains(status.LastError, "embed search") {
		t.Fatalf("expected embed failure in status, got %#v", status)
	}
	if got := backend.SearchWithContext(context.Background(), "remember", 5); got != nil {
		t.Fatalf("expected cooldown to suppress immediate retry, got %#v", got)
	}
	if embedCalls != 1 {
		t.Fatalf("expected cooldown to skip re-embedding, got %d calls", embedCalls)
	}
	backend.mu.Lock()
	backend.nextRetryAt = time.Now().Add(-time.Second)
	backend.mu.Unlock()
	results := backend.SearchWithContext(context.Background(), "remember", 5)
	if len(results) != 1 || results[0].Text != "remembered" {
		t.Fatalf("expected successful retry after cooldown, got %#v", results)
	}
	if searchCalls != 1 {
		t.Fatalf("expected one successful search after cooldown, got %d", searchCalls)
	}
	if status := backend.BackendStatus(); status.Degraded {
		t.Fatalf("expected recovered backend after successful retry, got %#v", status)
	}
}

func TestHybridIndexMemoryStatusReflectsBackendFallback(t *testing.T) {
	idx, err := OpenIndex(filepath.Join(t.TempDir(), "memory.json"))
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	backend := &contextAwareBackendStub{}
	hybrid := NewHybridIndex(idx, backend)
	status := hybrid.MemoryStatus()
	if status.Kind != "hybrid" {
		t.Fatalf("expected hybrid store status, got %#v", status)
	}
	if status.FallbackActive {
		t.Fatalf("did not expect fallback active for healthy backend, got %#v", status)
	}
	if status.Primary.Name != "stub" || status.Fallback == nil || status.Fallback.Name != "json-fts" {
		t.Fatalf("unexpected hybrid status: %#v", status)
	}
}
