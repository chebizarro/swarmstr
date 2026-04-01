package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
		if r.URL.Path != "/collections/test/points/delete" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode delete body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
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
