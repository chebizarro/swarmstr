package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

const EnvMemoryModelExtraction = "METIQ_MEMORY_MODEL_EXTRACTION"

// ProviderModelMemoryExtractor adapts the production agent runtime to the
// structured memory extraction contract. The same configured provider path and
// response-format plumbing used for normal turns is reused here.
type ProviderModelMemoryExtractor struct {
	runtime agent.Runtime
}

func NewProviderModelMemoryExtractor(runtime agent.Runtime) (*ProviderModelMemoryExtractor, error) {
	if runtime == nil {
		return nil, fmt.Errorf("memory model extractor requires a runtime")
	}
	return &ProviderModelMemoryExtractor{runtime: runtime}, nil
}

func NewProviderModelMemoryExtractorFromEnv() (*ProviderModelMemoryExtractor, error) {
	runtime, err := agent.NewRuntimeFromEnv(nil)
	if err != nil {
		return nil, err
	}
	return NewProviderModelMemoryExtractor(runtime)
}

func (e *ProviderModelMemoryExtractor) ExtractMemories(ctx context.Context, corpus TranscriptCorpus) ([]ModelMemoryCandidate, error) {
	if e == nil || e.runtime == nil {
		return nil, fmt.Errorf("memory model extractor is unavailable")
	}
	encoded, err := json.Marshal(corpus)
	if err != nil {
		return nil, fmt.Errorf("encode transcript corpus: %w", err)
	}
	result, err := e.runtime.ProcessTurn(ctx, agent.Turn{
		SessionID:           corpus.SessionID + ":memory-extraction",
		UserText:            modelMemoryExtractionPrompt + "\n\nTranscript corpus:\n" + string(encoded),
		MaxOutputTokens:     2048,
		ContextWindowTokens: 32_000,
		ResponseFormat: &agent.ResponseFormatConfig{
			Type:   agent.ResponseFormatJSONSchema,
			Name:   "memory_candidates",
			Strict: true,
			Schema: modelMemoryCandidateSchema(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("memory model inference: %w", err)
	}
	candidates, err := decodeModelMemoryCandidates(result.Text)
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

const modelMemoryExtractionPrompt = `Extract only durable, reusable memories grounded in the supplied transcript corpus. Return JSON with a candidates array. Each candidate must cite one or more source_refs from transcript entry_id values. Omit transient chatter, secrets, and unsupported inferences. Use confidence from 0 to 1. Use empty strings or arrays for optional metadata.`

func modelMemoryCandidateSchema() map[string]any {
	candidate := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"key":         map[string]any{"type": "string"},
			"text":        map[string]any{"type": "string"},
			"type":        map[string]any{"type": "string"},
			"topic":       map[string]any{"type": "string"},
			"keywords":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"confidence":  map[string]any{"type": "number"},
			"source_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"reason":      map[string]any{"type": "string"},
		},
		"required": []any{"key", "text", "type", "topic", "keywords", "confidence", "source_refs", "reason"},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"candidates": map[string]any{"type": "array", "items": candidate},
		},
		"required": []any{"candidates"},
	}
}

func decodeModelMemoryCandidates(raw string) ([]ModelMemoryCandidate, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "```") {
		if firstLine := strings.IndexByte(trimmed, '\n'); firstLine >= 0 {
			trimmed = trimmed[firstLine+1:]
		}
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "```"))
	}
	var envelope struct {
		Candidates []ModelMemoryCandidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err == nil && envelope.Candidates != nil {
		return envelope.Candidates, nil
	}
	var candidates []ModelMemoryCandidate
	if err := json.Unmarshal([]byte(trimmed), &candidates); err != nil {
		return nil, fmt.Errorf("decode memory model response: %w", err)
	}
	return candidates, nil
}

var productionModelExtraction struct {
	sync.Mutex
	extractor ModelMemoryExtractor
	resolved  bool
}

// ConfigureProductionModelMemoryExtractor overrides automatic provider
// resolution. It is useful for embedding runtimes and deterministic tests.
func ConfigureProductionModelMemoryExtractor(extractor ModelMemoryExtractor) {
	productionModelExtraction.Lock()
	productionModelExtraction.extractor = extractor
	productionModelExtraction.resolved = extractor != nil
	productionModelExtraction.Unlock()
}

func productionModelMemoryExtractor() ModelMemoryExtractor {
	productionModelExtraction.Lock()
	defer productionModelExtraction.Unlock()
	if productionModelExtraction.resolved {
		return productionModelExtraction.extractor
	}
	productionModelExtraction.resolved = true
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv(EnvMemoryModelExtraction)), "true") || strings.TrimSpace(os.Getenv("METIQ_AGENT_PROVIDER")) != ""
	if !enabled {
		return nil
	}
	extractor, err := NewProviderModelMemoryExtractorFromEnv()
	if err == nil {
		productionModelExtraction.extractor = extractor
	}
	return productionModelExtraction.extractor
}

func extractModelMemoriesForStoredTurn(ctx context.Context, doc state.MemoryDoc) []state.MemoryDoc {
	if doc.Source != MemorySourceKindTurn || strings.TrimSpace(doc.Text) == "" || doc.RecalledContent {
		return nil
	}
	extractor := productionModelMemoryExtractor()
	if extractor == nil {
		return nil
	}
	corpus := TranscriptCorpus{SessionID: doc.SessionID, Entries: []state.TranscriptEntryDoc{{
		SessionID: doc.SessionID,
		EntryID:   doc.SourceRef,
		Role:      doc.Role,
		Text:      doc.Text,
		Unix:      doc.Unix,
	}}}
	result := NewModelExtractionPipeline(extractor, nil, DefaultModelExtractionConfig()).ExtractTranscript(ctx, corpus)
	if result.UsedFallback {
		// The original heuristic document has already been persisted by AddDoc.
		return nil
	}
	for i := range result.Documents {
		result.Documents[i].OriginClass = doc.OriginClass
		result.Documents[i].SessionKind = doc.SessionKind
		result.Documents[i].ExternalToolTaint = doc.ExternalToolTaint
		result.Documents[i].NetworkTaint = doc.NetworkTaint
		result.Documents[i].RecalledContent = doc.RecalledContent
	}
	return result.Documents
}
