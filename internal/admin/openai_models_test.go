package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"metiq/internal/gateway/methods"
)

// ── toOpenAIModel ───────────────────────────────────────────────────────────

func TestToOpenAIModel(t *testing.T) {
	m := toOpenAIModel("gpt-4")
	if m.ID != "gpt-4" {
		t.Fatalf("id = %q", m.ID)
	}
	if m.Object != "model" {
		t.Fatalf("object = %q", m.Object)
	}
	if m.OwnedBy != DefaultModelOwnedBy {
		t.Fatalf("owned_by = %q", m.OwnedBy)
	}
	if m.Permission == nil {
		t.Fatal("permission should be non-nil empty slice")
	}
	if len(m.Permission) != 0 {
		t.Fatalf("permission len = %d", len(m.Permission))
	}
}

// ── appendUnique ────────────────────────────────────────────────────────────

func TestAppendUnique(t *testing.T) {
	list := []string{"a", "b"}
	list = appendUnique(list, "c")
	if len(list) != 3 {
		t.Fatalf("expected 3, got %d", len(list))
	}
	list = appendUnique(list, "b")
	if len(list) != 3 {
		t.Fatalf("duplicate should not be added, got %d", len(list))
	}
}

// ── resolveModelIDs ─────────────────────────────────────────────────────────

func TestResolveModelIDs_NoListModels(t *testing.T) {
	opts := ServerOptions{}
	ids := resolveModelIDs(context.Background(), opts)
	if len(ids) < 2 {
		t.Fatalf("expected at least 2 default IDs, got %d", len(ids))
	}
	if ids[0] != DefaultModelID {
		t.Fatalf("first id = %q", ids[0])
	}
	if ids[1] != DefaultModelAlias {
		t.Fatalf("second id = %q", ids[1])
	}
}

func TestResolveModelIDs_WithListModels(t *testing.T) {
	opts := ServerOptions{
		ListModels: func(_ context.Context, _ methods.ModelsListRequest) (map[string]any, error) {
			return map[string]any{
				"models": []map[string]any{
					{"id": "gpt-4"},
					{"id": "gpt-3.5-turbo"},
				},
			}, nil
		},
	}
	ids := resolveModelIDs(context.Background(), opts)
	found4 := false
	found35 := false
	for _, id := range ids {
		if id == "gpt-4" {
			found4 = true
		}
		if id == "gpt-3.5-turbo" {
			found35 = true
		}
	}
	if !found4 {
		t.Fatal("expected gpt-4 in list")
	}
	if !found35 {
		t.Fatal("expected gpt-3.5-turbo in list")
	}
}

func TestResolveModelIDs_AlwaysIncludesDefaults(t *testing.T) {
	opts := ServerOptions{
		ListModels: func(_ context.Context, _ methods.ModelsListRequest) (map[string]any, error) {
			return map[string]any{"models": []map[string]any{{"id": "custom"}}}, nil
		},
	}
	ids := resolveModelIDs(context.Background(), opts)
	hasDefault := false
	hasAlias := false
	for _, id := range ids {
		if id == DefaultModelID {
			hasDefault = true
		}
		if id == DefaultModelAlias {
			hasAlias = true
		}
	}
	if !hasDefault || !hasAlias {
		t.Fatalf("defaults missing: default=%v alias=%v, ids=%v", hasDefault, hasAlias, ids)
	}
}

// ── HTTP handler tests ──────────────────────────────────────────────────────

func modelsOpts() ServerOptions {
	return ServerOptions{
		ListModels: func(_ context.Context, _ methods.ModelsListRequest) (map[string]any, error) {
			return map[string]any{
				"models": []map[string]any{
					{"id": "metiq/main"},
					{"id": "metiq/assistant"},
				},
			}, nil
		},
	}
}

func TestHandleOpenAIModels_ListAll(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var resp openAIModelListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Object != "list" {
		t.Fatalf("object = %q", resp.Object)
	}
	if len(resp.Data) < 2 {
		t.Fatalf("data len = %d, want >= 2", len(resp.Data))
	}
	// Check all entries are valid model objects.
	for _, m := range resp.Data {
		if m.Object != "model" {
			t.Fatalf("entry object = %q", m.Object)
		}
		if m.ID == "" {
			t.Fatal("entry has empty id")
		}
	}
}

func TestHandleOpenAIModels_GetByID(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	req := httptest.NewRequest(http.MethodGet, "/v1/models/"+DefaultModelID, nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var m openAIModelObject
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != DefaultModelID {
		t.Fatalf("id = %q", m.ID)
	}
	if m.Object != "model" {
		t.Fatalf("object = %q", m.Object)
	}
}

func TestHandleOpenAIModels_GetByID_URLEncoded(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	// metiq/main → metiq%2Fmain
	req := httptest.NewRequest(http.MethodGet, "/v1/models/metiq%2Fmain", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var m openAIModelObject
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "metiq/main" {
		t.Fatalf("id = %q", m.ID)
	}
}

func TestHandleOpenAIModels_GetByID_NotFound(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	req := httptest.NewRequest(http.MethodGet, "/v1/models/nonexistent-model", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object")
	}
	msg, _ := errObj["message"].(string)
	if msg == "" {
		t.Fatal("expected error message")
	}
}

func TestHandleOpenAIModels_MethodNotAllowed(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	req := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if rr.Header().Get("Allow") != "GET" {
		t.Fatalf("Allow header = %q", rr.Header().Get("Allow"))
	}
}

func TestHandleOpenAIModels_EmptyID(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	// Trailing slash with no ID should list models (not 400).
	req := httptest.NewRequest(http.MethodGet, "/v1/models/", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (list)", rr.Code)
	}
}

func TestHandleOpenAIModels_NoListModelsCallback(t *testing.T) {
	handler := handleOpenAIModels(ServerOptions{})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even without ListModels", rr.Code)
	}

	var resp openAIModelListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// Should still have the default models.
	if len(resp.Data) < 2 {
		t.Fatalf("data len = %d, want >= 2 defaults", len(resp.Data))
	}
}

// ── Mount integration ───────────────────────────────────────────────────────

func TestMountOpenAIModels(t *testing.T) {
	mux := http.NewServeMux()
	opts := modelsOpts()
	mountOpenAIModels(mux, opts)

	// Test list endpoint.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d", rr.Code)
	}

	// Test get-by-id endpoint.
	req = httptest.NewRequest(http.MethodGet, "/v1/models/"+DefaultModelID, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get-by-id status = %d", rr.Code)
	}
}

func TestMountOpenAIModels_WithAuth(t *testing.T) {
	mux := http.NewServeMux()
	opts := modelsOpts()
	opts.Token = "secret-models-token"
	mountOpenAIModels(mux, opts)

	// No auth → 401.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", rr.Code)
	}

	// With auth → 200.
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer secret-models-token")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("with-auth status = %d, want 200", rr.Code)
	}
}

// ── Response format validation ──────────────────────────────────────────────

func TestOpenAIModels_ResponseFormat(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}

	// Ensure the response is valid JSON with the expected shape.
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["object"] != "list" {
		t.Fatalf("object = %v", raw["object"])
	}
	data, ok := raw["data"].([]any)
	if !ok {
		t.Fatal("data should be an array")
	}
	for _, entry := range data {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Fatal("each entry should be an object")
		}
		if m["object"] != "model" {
			t.Fatalf("entry object = %v", m["object"])
		}
		if _, ok := m["id"].(string); !ok {
			t.Fatal("entry id should be string")
		}
		if _, ok := m["owned_by"].(string); !ok {
			t.Fatal("entry owned_by should be string")
		}
		if _, ok := m["permission"].([]any); !ok {
			t.Fatal("entry permission should be array")
		}
	}
}

func TestOpenAIModels_GetByID_ResponseFormat(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	req := httptest.NewRequest(http.MethodGet, "/v1/models/"+DefaultModelID, nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["object"] != "model" {
		t.Fatalf("object = %v", raw["object"])
	}
	if raw["id"] != DefaultModelID {
		t.Fatalf("id = %v", raw["id"])
	}
}

func TestHandleOpenAIModels_ListIncludesAgentModels(t *testing.T) {
	handler := handleOpenAIModels(modelsOpts())
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	var resp openAIModelListResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	ids := make(map[string]bool)
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	// Expect agent-specific models from our stub.
	if !ids["metiq/main"] {
		t.Fatal("expected metiq/main in list")
	}
	if !ids["metiq/assistant"] {
		t.Fatal("expected metiq/assistant in list")
	}
	// Plus defaults.
	if !ids[DefaultModelID] {
		t.Fatal("expected default model in list")
	}
}
