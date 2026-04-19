package admin

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
)

// ── OpenAI-compatible embeddings types ──────────────────────────────────────

// embeddingsRequest is the incoming POST /v1/embeddings body.
type embeddingsRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"`           // string or []string
	EncodingFormat string `json:"encoding_format"` // "float" (default) or "base64"
	Dimensions     int    `json:"dimensions"`      // optional output dimensionality
	User           string `json:"user"`            // optional
}

// embeddingsResponse is the OpenAI embeddings response.
type embeddingsResponse struct {
	Object string             `json:"object"`
	Data   []embeddingObject  `json:"data"`
	Model  string             `json:"model"`
	Usage  embeddingsUsage    `json:"usage"`
}

// embeddingObject is a single embedding result.
type embeddingObject struct {
	Object    string `json:"object"`
	Index     int    `json:"index"`
	Embedding any    `json:"embedding"` // []float64 or base64 string
}

// embeddingsUsage tracks token usage (stub — embeddings don't consume agent tokens).
type embeddingsUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ── Input validation limits ─────────────────────────────────────────────────

const (
	// MaxEmbeddingInputs is the maximum number of input texts per request.
	MaxEmbeddingInputs = 128
	// MaxEmbeddingInputChars is the maximum characters per single input.
	MaxEmbeddingInputChars = 8192
	// MaxEmbeddingTotalChars is the maximum total characters across all inputs.
	MaxEmbeddingTotalChars = 65536
	// MaxEmbeddingsBodyBytes is the max request body size.
	MaxEmbeddingsBodyBytes = 5 * 1024 * 1024
)

// ── EmbedFunc callback type ─────────────────────────────────────────────────

// EmbedFunc generates embeddings for a batch of input texts.
// Returns one []float32 per input text.
type EmbedFunc func(texts []string) ([][]float32, error)

// ── Input resolution ────────────────────────────────────────────────────────

// resolveInputTexts converts the "input" field to a string slice.
// Returns nil if the input is not a string or []string.
func resolveEmbeddingInputTexts(input any) []string {
	switch v := input.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

// validateEmbeddingInputTexts checks input limits.
func validateEmbeddingInputTexts(texts []string) string {
	if len(texts) > MaxEmbeddingInputs {
		return "Too many inputs (max 128)."
	}
	totalChars := 0
	for _, text := range texts {
		if len(text) > MaxEmbeddingInputChars {
			return "Input too long (max 8192 chars)."
		}
		totalChars += len(text)
		if totalChars > MaxEmbeddingTotalChars {
			return "Total input too large (max 65536 chars)."
		}
	}
	return ""
}

// ── Base64 encoding ─────────────────────────────────────────────────────────

// encodeEmbeddingBase64 encodes a float32 slice as a base64 little-endian
// Float32Array, matching OpenAI's base64 embedding format.
func encodeEmbeddingBase64(embedding []float32) string {
	buf := make([]byte, len(embedding)*4)
	for i, v := range embedding {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// float32sToFloat64s converts float32 slice to float64 for JSON marshalling.
func float32sToFloat64s(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

// ── HTTP handler ────────────────────────────────────────────────────────────

// handleOpenAIEmbeddings returns an http.HandlerFunc for POST /v1/embeddings.
func handleOpenAIEmbeddings(opts ServerOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
				"error": map[string]any{
					"message": "Only POST is allowed.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		if opts.EmbedTexts == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{
				"error": map[string]any{
					"message": "Embeddings provider not configured.",
					"type":    "api_error",
				},
			})
			return
		}

		// Parse body.
		r.Body = http.MaxBytesReader(w, r.Body, MaxEmbeddingsBodyBytes)
		var req embeddingsRequest
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"message": "Invalid request body.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// Validate model.
		if req.Model == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"message": "Missing `model`.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// Resolve input texts.
		texts := resolveEmbeddingInputTexts(req.Input)
		if texts == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"message": "`input` must be a string or an array of strings.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// Validate input limits.
		if errMsg := validateEmbeddingInputTexts(texts); errMsg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"message": errMsg,
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// Generate embeddings.
		embeddings, err := opts.EmbedTexts(texts)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]any{
					"message": "internal error",
					"type":    "api_error",
				},
			})
			return
		}

		// Build response.
		useBase64 := req.EncodingFormat == "base64"
		data := make([]embeddingObject, len(embeddings))
		for i, emb := range embeddings {
			var embedding any
			if useBase64 {
				embedding = encodeEmbeddingBase64(emb)
			} else {
				embedding = float32sToFloat64s(emb)
			}
			data[i] = embeddingObject{
				Object:    "embedding",
				Index:     i,
				Embedding: embedding,
			}
		}

		writeJSON(w, http.StatusOK, embeddingsResponse{
			Object: "list",
			Data:   data,
			Model:  req.Model,
			Usage:  embeddingsUsage{PromptTokens: 0, TotalTokens: 0},
		})
	}
}

// mountOpenAIEmbeddings registers the /v1/embeddings endpoint.
func mountOpenAIEmbeddings(mux *http.ServeMux, opts ServerOptions) {
	mux.HandleFunc("/v1/embeddings", withAuth(opts.Token, handleOpenAIEmbeddings(opts)))
}
