package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"metiq/internal/gateway/methods"
)

// ── OpenAI-compatible model types ───────────────────────────────────────────

// openAIModelObject is the OpenAI model object format.
// See https://platform.openai.com/docs/api-reference/models/object
type openAIModelObject struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Created    int64  `json:"created"`
	OwnedBy   string `json:"owned_by"`
	Permission []any  `json:"permission"`
}

// openAIModelListResponse is the response for GET /v1/models.
type openAIModelListResponse struct {
	Object string              `json:"object"`
	Data   []openAIModelObject `json:"data"`
}

// DefaultModelOwnedBy is the default owned_by value for models.
const DefaultModelOwnedBy = "metiq"

// DefaultModelID is the canonical model ID.
const DefaultModelID = "metiq"

// DefaultModelAlias is the default model alias.
const DefaultModelAlias = "metiq/default"

// ── Model resolution ────────────────────────────────────────────────────────

// toOpenAIModel converts a model ID to an OpenAI model object.
func toOpenAIModel(id string) openAIModelObject {
	return openAIModelObject{
		ID:         id,
		Object:     "model",
		Created:    0,
		OwnedBy:   DefaultModelOwnedBy,
		Permission: []any{},
	}
}

// resolveModelIDs builds the list of available model IDs.
// It calls opts.ListModels if available, otherwise returns defaults.
func resolveModelIDs(ctx context.Context, opts ServerOptions) []string {
	ids := []string{DefaultModelID, DefaultModelAlias}

	if opts.ListModels != nil {
		result, err := opts.ListModels(ctx, methods.ModelsListRequest{})
		if err == nil {
			if models, ok := result["models"].([]map[string]any); ok {
				for _, m := range models {
					if id, ok := m["id"].(string); ok && id != "" {
						ids = appendUnique(ids, id)
					}
				}
			}
			// Also try the models as a flat string array.
			if models, ok := result["models"].([]any); ok {
				for _, m := range models {
					if mm, ok := m.(map[string]any); ok {
						if id, ok := mm["id"].(string); ok && id != "" {
							ids = appendUnique(ids, id)
						}
					}
					if id, ok := m.(string); ok && id != "" {
						ids = appendUnique(ids, id)
					}
				}
			}
		}
	}

	return ids
}

// appendUnique adds s to the list if not already present.
func appendUnique(list []string, s string) []string {
	for _, existing := range list {
		if existing == s {
			return list
		}
	}
	return append(list, s)
}

// ── HTTP handler ────────────────────────────────────────────────────────────

// handleOpenAIModels returns an http.HandlerFunc that serves both:
//   - GET /v1/models       → list all models
//   - GET /v1/models/{id}  → get a specific model
func handleOpenAIModels(opts ServerOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
				"error": map[string]any{
					"message": "Only GET is allowed.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		path := r.URL.Path

		// GET /v1/models — list all models.
		if path == "/v1/models" || path == "/v1/models/" {
			ids := resolveModelIDs(r.Context(), opts)
			data := make([]openAIModelObject, len(ids))
			for i, id := range ids {
				data[i] = toOpenAIModel(id)
			}
			writeJSON(w, http.StatusOK, openAIModelListResponse{
				Object: "list",
				Data:   data,
			})
			return
		}

		// GET /v1/models/{id} — get a specific model.
		encodedID := strings.TrimPrefix(path, "/v1/models/")
		if encodedID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"message": "Missing model id.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		decodedID, err := url.PathUnescape(encodedID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"message": "Invalid model id encoding.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// Look up the model in the available IDs.
		ids := resolveModelIDs(r.Context(), opts)
		found := false
		for _, id := range ids {
			if id == decodedID {
				found = true
				break
			}
		}

		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]any{
					"message": "Model '" + decodedID + "' not found.",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		writeJSON(w, http.StatusOK, toOpenAIModel(decodedID))
	}
}

// mountOpenAIModels registers the /v1/models endpoint.
func mountOpenAIModels(mux *http.ServeMux, opts ServerOptions) {
	handler := withAuth(opts.Token, handleOpenAIModels(opts))
	mux.HandleFunc("/v1/models", handler)
	mux.HandleFunc("/v1/models/", handler)
}
