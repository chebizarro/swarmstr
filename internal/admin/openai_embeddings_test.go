package admin

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── resolveEmbeddingInputTexts ──────────────────────────────────────────────

func TestResolveEmbeddingInputTexts_String(t *testing.T) {
	texts := resolveEmbeddingInputTexts("hello")
	if len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("got %v", texts)
	}
}

func TestResolveEmbeddingInputTexts_StringArray(t *testing.T) {
	texts := resolveEmbeddingInputTexts([]any{"a", "b", "c"})
	if len(texts) != 3 {
		t.Fatalf("got %v", texts)
	}
}

func TestResolveEmbeddingInputTexts_MixedArray(t *testing.T) {
	texts := resolveEmbeddingInputTexts([]any{"a", 42})
	if texts != nil {
		t.Fatalf("mixed array should return nil, got %v", texts)
	}
}

func TestResolveEmbeddingInputTexts_InvalidType(t *testing.T) {
	texts := resolveEmbeddingInputTexts(42)
	if texts != nil {
		t.Fatalf("int should return nil, got %v", texts)
	}
}

func TestResolveEmbeddingInputTexts_Nil(t *testing.T) {
	texts := resolveEmbeddingInputTexts(nil)
	if texts != nil {
		t.Fatalf("nil should return nil, got %v", texts)
	}
}

func TestResolveEmbeddingInputTexts_NativeStringSlice(t *testing.T) {
	texts := resolveEmbeddingInputTexts([]string{"x", "y"})
	if len(texts) != 2 {
		t.Fatalf("got %v", texts)
	}
}

// ── validateEmbeddingInputTexts ─────────────────────────────────────────────

func TestValidateEmbeddingInputTexts_OK(t *testing.T) {
	if errMsg := validateEmbeddingInputTexts([]string{"hello", "world"}); errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
}

func TestValidateEmbeddingInputTexts_TooMany(t *testing.T) {
	texts := make([]string, MaxEmbeddingInputs+1)
	for i := range texts {
		texts[i] = "x"
	}
	errMsg := validateEmbeddingInputTexts(texts)
	if errMsg == "" {
		t.Fatal("expected error for too many inputs")
	}
	if !strings.Contains(errMsg, "128") {
		t.Fatalf("error should mention limit: %s", errMsg)
	}
}

func TestValidateEmbeddingInputTexts_TooLong(t *testing.T) {
	long := strings.Repeat("x", MaxEmbeddingInputChars+1)
	errMsg := validateEmbeddingInputTexts([]string{long})
	if errMsg == "" {
		t.Fatal("expected error for too long input")
	}
}

func TestValidateEmbeddingInputTexts_TotalTooLarge(t *testing.T) {
	// Each input is within limit but total exceeds.
	chunk := strings.Repeat("x", MaxEmbeddingInputChars)
	texts := make([]string, (MaxEmbeddingTotalChars/MaxEmbeddingInputChars)+2)
	for i := range texts {
		texts[i] = chunk
	}
	errMsg := validateEmbeddingInputTexts(texts)
	if errMsg == "" {
		t.Fatal("expected error for total too large")
	}
}

// ── encodeEmbeddingBase64 ───────────────────────────────────────────────────

func TestEncodeEmbeddingBase64(t *testing.T) {
	embedding := []float32{0.1, 0.2, 0.3}
	encoded := encodeEmbeddingBase64(embedding)
	if encoded == "" {
		t.Fatal("expected non-empty base64")
	}

	// Decode and verify round-trip.
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != len(embedding)*4 {
		t.Fatalf("decoded len = %d, want %d", len(raw), len(embedding)*4)
	}
	for i, expected := range embedding {
		bits := binary.LittleEndian.Uint32(raw[i*4:])
		got := math.Float32frombits(bits)
		if got != expected {
			t.Fatalf("index %d: got %f, want %f", i, got, expected)
		}
	}
}

func TestEncodeEmbeddingBase64_Empty(t *testing.T) {
	encoded := encodeEmbeddingBase64([]float32{})
	if encoded != "" {
		t.Fatalf("empty embedding should produce empty base64, got %q", encoded)
	}
}

// ── float32sToFloat64s ──────────────────────────────────────────────────────

func TestFloat32sToFloat64s(t *testing.T) {
	in := []float32{0.1, 0.2, 0.3}
	out := float32sToFloat64s(in)
	if len(out) != 3 {
		t.Fatalf("len = %d", len(out))
	}
	for i, v := range out {
		expected := float64(in[i])
		if v != expected {
			t.Fatalf("index %d: got %f, want %f", i, v, expected)
		}
	}
}

// ── HTTP handler tests ──────────────────────────────────────────────────────

func stubEmbedFunc(texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{float32(i) + 0.1, float32(i) + 0.2}
	}
	return result, nil
}

func failingEmbedFunc(_ []string) ([][]float32, error) {
	return nil, fmt.Errorf("upstream failure")
}

func embeddingsOpts() ServerOptions {
	return ServerOptions{
		EmbedTexts: stubEmbedFunc,
	}
}

func postEmbeddings(handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func TestHandleOpenAIEmbeddings_SingleInput(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": "hello world",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var resp embeddingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Object != "list" {
		t.Fatalf("object = %q", resp.Object)
	}
	if resp.Model != "metiq" {
		t.Fatalf("model = %q", resp.Model)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("data len = %d", len(resp.Data))
	}
	if resp.Data[0].Object != "embedding" {
		t.Fatalf("data[0].object = %q", resp.Data[0].Object)
	}
	if resp.Data[0].Index != 0 {
		t.Fatalf("data[0].index = %d", resp.Data[0].Index)
	}
}

func TestHandleOpenAIEmbeddings_BatchInput(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": []string{"a", "b", "c"},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var resp embeddingsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Data) != 3 {
		t.Fatalf("data len = %d, want 3", len(resp.Data))
	}
	for i, d := range resp.Data {
		if d.Index != i {
			t.Fatalf("data[%d].index = %d", i, d.Index)
		}
	}
}

func TestHandleOpenAIEmbeddings_Base64Encoding(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"model":           "metiq",
		"input":           "hello",
		"encoding_format": "base64",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var resp embeddingsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("data len = %d", len(resp.Data))
	}

	// Embedding should be a base64 string.
	embStr, ok := resp.Data[0].Embedding.(string)
	if !ok {
		t.Fatalf("expected string embedding for base64, got %T", resp.Data[0].Embedding)
	}
	if embStr == "" {
		t.Fatal("expected non-empty base64 string")
	}

	// Verify it decodes.
	_, err := base64.StdEncoding.DecodeString(embStr)
	if err != nil {
		t.Fatalf("invalid base64: %v", err)
	}
}

func TestHandleOpenAIEmbeddings_FloatEncoding(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": "hello",
	})

	var resp embeddingsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	// Float encoding should be an array of numbers.
	arr, ok := resp.Data[0].Embedding.([]any)
	if !ok {
		t.Fatalf("expected array for float encoding, got %T", resp.Data[0].Embedding)
	}
	if len(arr) != 2 {
		t.Fatalf("embedding len = %d, want 2", len(arr))
	}
}

func TestHandleOpenAIEmbeddings_MissingModel(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"input": "hello",
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleOpenAIEmbeddings_MissingInput(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleOpenAIEmbeddings_InvalidInputType(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": 42,
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleOpenAIEmbeddings_TooManyInputs(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	inputs := make([]string, MaxEmbeddingInputs+1)
	for i := range inputs {
		inputs[i] = "x"
	}
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": inputs,
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleOpenAIEmbeddings_MethodNotAllowed(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	req := httptest.NewRequest(http.MethodGet, "/v1/embeddings", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if rr.Header().Get("Allow") != "POST" {
		t.Fatalf("Allow = %q", rr.Header().Get("Allow"))
	}
}

func TestHandleOpenAIEmbeddings_NoEmbedCallback(t *testing.T) {
	handler := handleOpenAIEmbeddings(ServerOptions{})
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": "hello",
	})

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rr.Code)
	}
}

func TestHandleOpenAIEmbeddings_ProviderError(t *testing.T) {
	opts := ServerOptions{EmbedTexts: failingEmbedFunc}
	handler := handleOpenAIEmbeddings(opts)
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": "hello",
	})

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}

	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != "api_error" {
		t.Fatalf("error type = %v", errObj["type"])
	}
	if errObj["message"] != "internal error" {
		t.Fatalf("error should sanitize message, got %v", errObj["message"])
	}
}

func TestHandleOpenAIEmbeddings_InvalidJSON(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// ── Mount integration ───────────────────────────────────────────────────────

func TestMountOpenAIEmbeddings(t *testing.T) {
	mux := http.NewServeMux()
	opts := embeddingsOpts()
	mountOpenAIEmbeddings(mux, opts)

	raw, _ := json.Marshal(map[string]any{
		"model": "metiq",
		"input": "test",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestMountOpenAIEmbeddings_WithAuth(t *testing.T) {
	mux := http.NewServeMux()
	opts := embeddingsOpts()
	opts.Token = "embed-secret"
	mountOpenAIEmbeddings(mux, opts)

	raw, _ := json.Marshal(map[string]any{
		"model": "metiq",
		"input": "test",
	})

	// No auth → 401.
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", rr.Code)
	}

	// With auth → 200.
	req = httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer embed-secret")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("with-auth status = %d, want 200", rr.Code)
	}
}

// ── Response format validation ──────────────────────────────────────────────

func TestOpenAIEmbeddings_ResponseFormat(t *testing.T) {
	handler := handleOpenAIEmbeddings(embeddingsOpts())
	rr := postEmbeddings(handler, map[string]any{
		"model": "metiq",
		"input": []string{"a", "b"},
	})

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}

	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["object"] != "list" {
		t.Fatalf("object = %v", raw["object"])
	}
	if raw["model"] != "metiq" {
		t.Fatalf("model = %v", raw["model"])
	}

	data, ok := raw["data"].([]any)
	if !ok {
		t.Fatal("data should be array")
	}
	if len(data) != 2 {
		t.Fatalf("data len = %d", len(data))
	}
	for _, d := range data {
		entry, _ := d.(map[string]any)
		if entry["object"] != "embedding" {
			t.Fatalf("entry object = %v", entry["object"])
		}
		if _, ok := entry["embedding"].([]any); !ok {
			t.Fatal("embedding should be array for float format")
		}
	}

	usage, ok := raw["usage"].(map[string]any)
	if !ok {
		t.Fatal("usage should be object")
	}
	if usage["prompt_tokens"] != float64(0) {
		t.Fatalf("prompt_tokens = %v", usage["prompt_tokens"])
	}
}
