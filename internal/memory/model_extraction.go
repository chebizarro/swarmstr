package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// TranscriptCorpus is the bounded, structured input supplied to a model
// extractor. Keeping entries structured preserves provenance and avoids prompt
// parsing conventions in the memory package.
type TranscriptCorpus struct {
	SessionID string                     `json:"session_id"`
	Entries   []state.TranscriptEntryDoc `json:"entries"`
}

// ModelMemoryCandidate is the structured contract returned by a model-assisted
// extractor. Key is an optional stable semantic slot (for example
// "user/editor-preference") used to resolve conflicting candidates.
type ModelMemoryCandidate struct {
	Key        string   `json:"key,omitempty"`
	Text       string   `json:"text"`
	Type       string   `json:"type,omitempty"`
	Topic      string   `json:"topic,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	SourceRefs []string `json:"source_refs,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// ModelMemoryExtractor is implemented by inference adapters outside this
// package. Implementations should return structured candidates, not free text.
type ModelMemoryExtractor interface {
	ExtractMemories(ctx context.Context, corpus TranscriptCorpus) ([]ModelMemoryCandidate, error)
}

// ModelExtractionConfig controls validation and safe fallback behavior.
type ModelExtractionConfig struct {
	MinConfidence    float64 `json:"min_confidence,omitempty"`
	MaxCandidates    int     `json:"max_candidates,omitempty"`
	MaxTextBytes     int     `json:"max_text_bytes,omitempty"`
	MaxCorpusEntries int     `json:"max_corpus_entries,omitempty"`
	MaxCorpusBytes   int     `json:"max_corpus_bytes,omitempty"`
	FallbackOnEmpty  bool    `json:"fallback_on_empty,omitempty"`
}

func DefaultModelExtractionConfig() ModelExtractionConfig {
	return ModelExtractionConfig{MinConfidence: 0.55, MaxCandidates: 24, MaxTextBytes: 2048, MaxCorpusEntries: 200, MaxCorpusBytes: 100_000, FallbackOnEmpty: true}
}

// ModelExtractionResult reports model/fallback behavior and ready-to-store docs.
type ModelExtractionResult struct {
	Documents          []state.MemoryDoc `json:"documents,omitempty"`
	ModelCandidates    int               `json:"model_candidates,omitempty"`
	RejectedCandidates int               `json:"rejected_candidates,omitempty"`
	DuplicatesSkipped  int               `json:"duplicates_skipped,omitempty"`
	UsedFallback       bool              `json:"used_fallback,omitempty"`
	ModelError         string            `json:"model_error,omitempty"`
}

// ModelExtractionPipeline validates and promotes model candidates while
// retaining ExtractFromTurn as a safe compatibility fallback.
type ModelExtractionPipeline struct {
	extractor ModelMemoryExtractor
	store     Store
	cfg       ModelExtractionConfig
}

func NewModelExtractionPipeline(extractor ModelMemoryExtractor, store Store, cfg ModelExtractionConfig) *ModelExtractionPipeline {
	zeroConfig := cfg == (ModelExtractionConfig{})
	defaults := DefaultModelExtractionConfig()
	if cfg.MinConfidence <= 0 || cfg.MinConfidence > 1 {
		cfg.MinConfidence = defaults.MinConfidence
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = defaults.MaxCandidates
	}
	if cfg.MaxTextBytes <= 0 {
		cfg.MaxTextBytes = defaults.MaxTextBytes
	}
	if cfg.MaxCorpusEntries <= 0 {
		cfg.MaxCorpusEntries = defaults.MaxCorpusEntries
	}
	if cfg.MaxCorpusBytes <= 0 {
		cfg.MaxCorpusBytes = defaults.MaxCorpusBytes
	}
	// A zero-value config should be safe by default.
	if zeroConfig {
		cfg.FallbackOnEmpty = true
	}
	return &ModelExtractionPipeline{extractor: extractor, store: store, cfg: cfg}
}

// ExtractTranscript runs one explicit extraction pass. Model errors are
// reported but do not fail the turn; deterministic heuristics are used instead.
func (p *ModelExtractionPipeline) ExtractTranscript(ctx context.Context, corpus TranscriptCorpus) ModelExtractionResult {
	result := ModelExtractionResult{}
	if p == nil {
		return result
	}
	corpus = boundTranscriptCorpus(corpus, p.cfg)
	var candidates []ModelMemoryCandidate
	if p.extractor != nil {
		modelCandidates, err := p.extractor.ExtractMemories(ctx, corpus)
		result.ModelCandidates = len(modelCandidates)
		if err != nil {
			result.ModelError = err.Error()
		} else {
			candidates = modelCandidates
		}
	}
	result.Documents, result.RejectedCandidates = p.validateCandidates(corpus, candidates)
	hadValidModelCandidate := len(result.Documents) > 0
	result.Documents, result.DuplicatesSkipped = p.deduplicate(ctx, result.Documents)
	if result.ModelError != "" || (!hadValidModelCandidate && p.cfg.FallbackOnEmpty) {
		fallback := p.heuristicFallback(corpus)
		fallback, skipped := p.deduplicate(ctx, append(result.Documents, fallback...))
		result.Documents = fallback
		result.DuplicatesSkipped += skipped
		result.UsedFallback = true
	}
	if len(result.Documents) > p.cfg.MaxCandidates {
		result.Documents = result.Documents[:p.cfg.MaxCandidates]
	}
	return result
}

// ExtractAndStoreTranscript atomically describes the extraction pass from the
// caller's perspective; individual backend writes retain their existing
// best-effort semantics.
func (p *ModelExtractionPipeline) ExtractAndStoreTranscript(ctx context.Context, corpus TranscriptCorpus) ModelExtractionResult {
	result := p.ExtractTranscript(ctx, corpus)
	if p == nil || p.store == nil {
		return result
	}
	for _, doc := range result.Documents {
		AddDoc(ctx, p.store, doc)
	}
	return result
}

func boundTranscriptCorpus(corpus TranscriptCorpus, cfg ModelExtractionConfig) TranscriptCorpus {
	entries := corpus.Entries
	if len(entries) > cfg.MaxCorpusEntries {
		entries = entries[len(entries)-cfg.MaxCorpusEntries:]
	}
	used := 0
	start := len(entries)
	for start > 0 {
		next := len(entries[start-1].Text)
		if used > 0 && used+next > cfg.MaxCorpusBytes {
			break
		}
		used += next
		start--
		if used >= cfg.MaxCorpusBytes {
			break
		}
	}
	entries = append([]state.TranscriptEntryDoc(nil), entries[start:]...)
	if len(entries) == 1 && len(entries[0].Text) > cfg.MaxCorpusBytes {
		entries[0].Text = truncateMemoryUTF8(entries[0].Text, cfg.MaxCorpusBytes)
	}
	corpus.Entries = entries
	return corpus
}

func (p *ModelExtractionPipeline) validateCandidates(corpus TranscriptCorpus, candidates []ModelMemoryCandidate) ([]state.MemoryDoc, int) {
	if len(candidates) > p.cfg.MaxCandidates*4 {
		candidates = candidates[:p.cfg.MaxCandidates*4]
	}
	validRefs := map[string]struct{}{}
	var latestUnix int64
	for _, entry := range corpus.Entries {
		if entry.EntryID != "" {
			validRefs[entry.EntryID] = struct{}{}
		}
		if entry.Unix > latestUnix {
			latestUnix = entry.Unix
		}
	}
	if latestUnix <= 0 {
		latestUnix = time.Now().Unix()
	}
	byKey := map[string]state.MemoryDoc{}
	rejected := 0
	for _, candidate := range candidates {
		text := strings.TrimSpace(candidate.Text)
		if len(text) < 8 || candidate.Confidence < p.cfg.MinConfidence || candidate.Confidence > 1 {
			rejected++
			continue
		}
		text = truncateMemoryUTF8(text, p.cfg.MaxTextBytes)
		refs := make([]string, 0, len(candidate.SourceRefs))
		for _, ref := range candidate.SourceRefs {
			if _, ok := validRefs[ref]; ok {
				refs = append(refs, ref)
			}
		}
		if len(validRefs) > 0 && len(refs) == 0 {
			rejected++
			continue
		}
		mtype := NormalizeMemoryRecordType(candidate.Type)
		topic := strings.TrimSpace(candidate.Topic)
		if topic == "" {
			keywords := extractKeywords(text)
			if len(keywords) > 0 {
				topic = keywords[0]
			} else {
				topic = "general"
			}
		}
		key := normalizeCandidateKey(candidate.Key)
		if key == "" {
			key = mtype + "\x00" + strings.ToLower(topic) + "\x00" + normalizeMemoryText(text)
		}
		doc := state.MemoryDoc{
			Version: 1, MemoryID: stableExtractedMemoryID(corpus.SessionID, key, text), Type: mtype,
			SessionID: corpus.SessionID, SourceRef: strings.Join(refs, ","), Text: text,
			Keywords: normalizeCandidateKeywords(candidate.Keywords, text), Topic: topic, Unix: latestUnix,
			Confidence: candidate.Confidence, Source: "model_extraction",
			Meta: map[string]any{"extraction": "model", "candidate_key": key, "reason": strings.TrimSpace(candidate.Reason), "source_refs": refs},
		}
		current, exists := byKey[key]
		if !exists || preferExtractedDoc(doc, current) {
			byKey[key] = doc
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	docs := make([]state.MemoryDoc, 0, len(keys))
	for _, key := range keys {
		docs = append(docs, byKey[key])
	}
	return docs, rejected
}

func (p *ModelExtractionPipeline) deduplicate(ctx context.Context, docs []state.MemoryDoc) ([]state.MemoryDoc, int) {
	seen := map[string]state.MemoryDoc{}
	skipped := 0
	for _, doc := range docs {
		key := normalizeMemoryText(doc.Text)
		if current, ok := seen[key]; ok {
			skipped++
			if preferExtractedDoc(doc, current) {
				seen[key] = doc
			}
			continue
		}
		if p.store != nil {
			duplicate := false
			for _, existing := range SearchDocs(ctx, p.store, doc.Text, 8) {
				if normalizeMemoryText(existing.Text) == key {
					duplicate = true
					break
				}
			}
			if duplicate {
				skipped++
				continue
			}
		}
		seen[key] = doc
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]state.MemoryDoc, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out, skipped
}

func (p *ModelExtractionPipeline) heuristicFallback(corpus TranscriptCorpus) []state.MemoryDoc {
	var out []state.MemoryDoc
	for _, entry := range corpus.Entries {
		if entry.Deleted {
			continue
		}
		out = append(out, ExtractFromTurn(corpus.SessionID, entry.Role, entry.EntryID, entry.Text, entry.Unix)...)
	}
	return out
}

func preferExtractedDoc(candidate, current state.MemoryDoc) bool {
	if candidate.Confidence != current.Confidence {
		return candidate.Confidence > current.Confidence
	}
	return candidate.Text < current.Text
}

func normalizeCandidateKey(key string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(key))), "-")
}

func normalizeMemoryText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

func normalizeCandidateKeywords(keywords []string, text string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, keyword := range append(append([]string(nil), keywords...), extractKeywords(text)...) {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if len(keyword) < 2 {
			continue
		}
		if _, ok := seen[keyword]; ok {
			continue
		}
		seen[keyword] = struct{}{}
		out = append(out, keyword)
		if len(out) == 8 {
			break
		}
	}
	return out
}

func stableExtractedMemoryID(sessionID, key, text string) string {
	h := sha256.Sum256([]byte(sessionID + "\x00" + key + "\x00" + normalizeMemoryText(text)))
	return "model:" + hex.EncodeToString(h[:12])
}

func truncateMemoryUTF8(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && (text[cut]&0xc0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(text[:cut])
}

// ModelMemoryConsolidator can drive both promotion summaries and explicit
// dreaming narratives through the existing promotion infrastructure.
type ModelMemoryConsolidator interface {
	ConsolidateMemories(ctx context.Context, phase DreamingPhase, memories []IndexedMemory) (string, error)
}

// ConfigureModelAssistedPromotion installs a model summarizer on the existing
// promotion manager. Promotion thresholds and persistence remain unchanged.
func ConfigureModelAssistedPromotion(manager *PromotionManager, consolidator ModelMemoryConsolidator) {
	if manager == nil || consolidator == nil {
		return
	}
	manager.SetSummarizer(func(memories []IndexedMemory) (string, error) {
		return consolidator.ConsolidateMemories(context.Background(), DreamingPhaseLight, memories)
	})
}

// RunModelAssistedDreaming explicitly invokes the existing phased scheduler
// with a model narrative builder. No timers or polling are introduced.
func RunModelAssistedDreaming(ctx context.Context, manager *PromotionManager, cfg DreamingConfig, consolidator ModelMemoryConsolidator) (*DreamingResult, error) {
	if consolidator == nil {
		return RunDreamingPhases(manager, cfg, nil)
	}
	builder := func(phase DreamingPhase, candidates []PromotionCandidate, promoted int) (string, error) {
		memories := make([]IndexedMemory, 0, len(candidates))
		for _, candidate := range candidates {
			memories = append(memories, candidate.Memory)
		}
		text, err := consolidator.ConsolidateMemories(ctx, phase, memories)
		if err != nil {
			return "", fmt.Errorf("model-assisted dreaming %s: %w", phase, err)
		}
		return text, nil
	}
	return RunDreamingPhases(manager, cfg, builder)
}
