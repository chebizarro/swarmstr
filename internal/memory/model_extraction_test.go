package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

type fakeModelExtractor struct {
	candidates []ModelMemoryCandidate
	err        error
	seen       TranscriptCorpus
}

func (f *fakeModelExtractor) ExtractMemories(_ context.Context, corpus TranscriptCorpus) ([]ModelMemoryCandidate, error) {
	f.seen = corpus
	return f.candidates, f.err
}

type fakeExtractionRuntime struct {
	result agent.TurnResult
	err    error
	seen   agent.Turn
}

func (f *fakeExtractionRuntime) ProcessTurn(_ context.Context, turn agent.Turn) (agent.TurnResult, error) {
	f.seen = turn
	return f.result, f.err
}

func useProductionExtractor(t *testing.T, extractor ModelMemoryExtractor) {
	t.Helper()
	productionModelExtraction.Lock()
	oldExtractor := productionModelExtraction.extractor
	oldResolved := productionModelExtraction.resolved
	productionModelExtraction.extractor = extractor
	productionModelExtraction.resolved = true
	productionModelExtraction.Unlock()
	t.Cleanup(func() {
		productionModelExtraction.Lock()
		productionModelExtraction.extractor = oldExtractor
		productionModelExtraction.resolved = oldResolved
		productionModelExtraction.Unlock()
	})
}

type fakeConsolidator struct {
	calls  int
	phases []DreamingPhase
}

func (f *fakeConsolidator) ConsolidateMemories(_ context.Context, phase DreamingPhase, _ []IndexedMemory) (string, error) {
	f.calls++
	f.phases = append(f.phases, phase)
	return "model narrative for " + string(phase), nil
}

func transcriptCorpus() TranscriptCorpus {
	return TranscriptCorpus{SessionID: "session-1", Entries: []state.TranscriptEntryDoc{
		{SessionID: "session-1", EntryID: "u1", Role: "user", Text: "Remember: I strongly prefer concise technical answers.", Unix: 100},
		{SessionID: "session-1", EntryID: "a1", Role: "assistant", Text: "Understood; concise technical answers are preferred.", Unix: 101},
	}}
}

func TestProviderModelMemoryExtractorUsesStructuredRuntimeTurn(t *testing.T) {
	runtime := &fakeExtractionRuntime{result: agent.TurnResult{Text: `{"candidates":[{"key":"user/style","text":"User prefers concise technical answers.","type":"preference","confidence":0.95,"source_refs":["u1"]}]}`}}
	extractor, err := NewProviderModelMemoryExtractor(runtime)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := extractor.ExtractMemories(context.Background(), transcriptCorpus())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].SourceRefs[0] != "u1" {
		t.Fatalf("unexpected provider candidates: %#v", candidates)
	}
	if runtime.seen.ResponseFormat == nil || runtime.seen.ResponseFormat.Type != agent.ResponseFormatJSONSchema || !runtime.seen.ResponseFormat.Strict {
		t.Fatalf("provider turn did not request strict structured output: %#v", runtime.seen.ResponseFormat)
	}
	if runtime.seen.MaxOutputTokens != 2048 || !strings.Contains(runtime.seen.UserText, `"entry_id":"u1"`) {
		t.Fatalf("provider turn did not carry extraction budget/corpus: %#v", runtime.seen)
	}
}

func TestAddDocRunsProductionModelExtractionWithHeuristicFallback(t *testing.T) {
	idx, err := OpenIndex(t.TempDir() + "/memory.json")
	if err != nil {
		t.Fatal(err)
	}
	backend := &IndexBackend{idx: idx}
	extractor := &fakeModelExtractor{candidates: []ModelMemoryCandidate{{
		Key: "user/style", Text: "User prefers concise technical answers.", Type: "preference", Confidence: .95, SourceRefs: []string{"turn-1"},
	}}}
	useProductionExtractor(t, extractor)
	AddDoc(context.Background(), backend, state.MemoryDoc{
		MemoryID: "turn-1", SessionID: "session-1", SourceRef: "turn-1", Role: "user",
		Text: "Remember: I strongly prefer concise technical answers.", Source: MemorySourceKindTurn,
	})
	if backend.Count() != 2 {
		t.Fatalf("expected heuristic and model memories, got %d", backend.Count())
	}
	modelHits := backend.Search("concise technical", 10)
	foundModel := false
	for _, hit := range modelHits {
		if hit.Source == "model_extraction" {
			foundModel = true
		}
	}
	if !foundModel {
		t.Fatalf("model-backed memory was not persisted: %#v", modelHits)
	}
}

func TestAddDocKeepsOriginalWhenProductionModelFails(t *testing.T) {
	idx, err := OpenIndex(t.TempDir() + "/memory.json")
	if err != nil {
		t.Fatal(err)
	}
	backend := &IndexBackend{idx: idx}
	useProductionExtractor(t, &fakeModelExtractor{err: errors.New("provider unavailable")})
	AddDoc(context.Background(), backend, state.MemoryDoc{
		MemoryID: "turn-1", SessionID: "session-1", SourceRef: "turn-1", Role: "user",
		Text: "Remember this preference.", Source: MemorySourceKindTurn,
	})
	if backend.Count() != 1 {
		t.Fatalf("provider failure must preserve exactly the original memory, got %d", backend.Count())
	}
}

func TestModelExtractionValidatesAndResolvesConflicts(t *testing.T) {
	extractor := &fakeModelExtractor{candidates: []ModelMemoryCandidate{
		{Key: "user/answer-style", Text: "User prefers verbose answers.", Type: "preference", Topic: "style", Confidence: .7, SourceRefs: []string{"u1"}},
		{Key: "user/answer-style", Text: "User prefers concise technical answers.", Type: "preference", Topic: "style", Confidence: .95, SourceRefs: []string{"u1"}},
		{Text: "bad", Confidence: .99, SourceRefs: []string{"u1"}},
		{Text: "Plausible but ungrounded memory.", Confidence: .9, SourceRefs: []string{"missing"}},
	}}
	pipeline := NewModelExtractionPipeline(extractor, nil, DefaultModelExtractionConfig())
	result := pipeline.ExtractTranscript(context.Background(), transcriptCorpus())
	if result.UsedFallback || len(result.Documents) != 1 {
		t.Fatalf("unexpected extraction result: %#v", result)
	}
	doc := result.Documents[0]
	if doc.Text != "User prefers concise technical answers." || doc.Type != MemoryRecordTypePreference || doc.Source != "model_extraction" {
		t.Fatalf("wrong conflict winner: %#v", doc)
	}
	if result.RejectedCandidates != 2 || extractor.seen.SessionID != "session-1" || len(extractor.seen.Entries) != 2 {
		t.Fatalf("validation/corpus mismatch: %#v", result)
	}
}

func TestModelExtractionFallsBackOnModelFailure(t *testing.T) {
	extractor := &fakeModelExtractor{err: errors.New("model unavailable")}
	pipeline := NewModelExtractionPipeline(extractor, nil, ModelExtractionConfig{})
	result := pipeline.ExtractTranscript(context.Background(), transcriptCorpus())
	if !result.UsedFallback || result.ModelError == "" || len(result.Documents) == 0 {
		t.Fatalf("expected heuristic fallback: %#v", result)
	}
	if result.Documents[0].Source != MemorySourceKindTurn {
		t.Fatalf("fallback did not preserve heuristic source: %#v", result.Documents[0])
	}
}

func TestModelExtractionSkipsExistingExactMemory(t *testing.T) {
	idx, err := OpenIndex(t.TempDir() + "/memory.json")
	if err != nil {
		t.Fatal(err)
	}
	backend := &IndexBackend{idx: idx}
	backend.Add(state.MemoryDoc{MemoryID: "existing", Text: "User prefers concise technical answers.", Topic: "style"})
	extractor := &fakeModelExtractor{candidates: []ModelMemoryCandidate{
		{Key: "user/style", Text: "User prefers concise technical answers.", Type: "preference", Confidence: .9, SourceRefs: []string{"u1"}},
	}}
	pipeline := NewModelExtractionPipeline(extractor, backend, DefaultModelExtractionConfig())
	result := pipeline.ExtractTranscript(context.Background(), transcriptCorpus())
	if len(result.Documents) != 0 || result.DuplicatesSkipped == 0 {
		t.Fatalf("existing duplicate was not suppressed: %#v", result)
	}
}

func TestModelExtractionStoresDeterministicDocuments(t *testing.T) {
	idx, err := OpenIndex(t.TempDir() + "/memory.json")
	if err != nil {
		t.Fatal(err)
	}
	backend := &IndexBackend{idx: idx}
	extractor := &fakeModelExtractor{candidates: []ModelMemoryCandidate{
		{Key: "project/database", Text: "The project uses SQLite for durable memory.", Type: "fact", Confidence: .9, SourceRefs: []string{"a1"}},
	}}
	pipeline := NewModelExtractionPipeline(extractor, backend, DefaultModelExtractionConfig())
	first := pipeline.ExtractAndStoreTranscript(context.Background(), transcriptCorpus())
	second := pipeline.ExtractAndStoreTranscript(context.Background(), transcriptCorpus())
	if len(first.Documents) != 1 || len(second.Documents) != 0 || backend.Count() != 1 {
		t.Fatalf("deterministic store/dedup failed: first=%#v second=%#v count=%d", first, second, backend.Count())
	}
}

func TestModelAssistedDreamingUsesExistingPhases(t *testing.T) {
	backend := newTestSQLiteBackend(t)
	manager := NewPromotionManager(backend, DefaultPromotionConfig())
	model := &fakeConsolidator{}
	result, err := RunModelAssistedDreaming(context.Background(), manager, DreamingConfig{Enabled: true, Narratives: true}, model)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || len(result.Phases) != 2 || result.Narrative == "" {
		t.Fatalf("model dreaming adapter not invoked: calls=%d result=%#v", model.calls, result)
	}
	ConfigureModelAssistedPromotion(manager, model)
	if manager.summarizer == nil {
		t.Fatal("promotion summarizer was not configured")
	}
}
