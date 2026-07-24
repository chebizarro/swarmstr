package memory

import (
	"context"
	"errors"
	"testing"

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
