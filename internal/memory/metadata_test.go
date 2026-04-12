package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func newMetadataTestIndex(t *testing.T) *Index {
	t.Helper()
	dir := t.TempDir()
	idx, err := OpenIndex(filepath.Join(dir, "test-index.json"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	return idx
}

func TestMetadata_ConfidenceRoundTrip(t *testing.T) {
	idx := newMetadataTestIndex(t)
	idx.Add(state.MemoryDoc{
		MemoryID:   "m1",
		Type:       state.MemoryTypeFact,
		Text:       "high confidence fact",
		Confidence: 0.95,
		Source:     state.MemorySourceUser,
		Unix:       1000,
	})
	results := idx.Search("confidence fact", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	if results[0].Confidence != 0.95 {
		t.Errorf("confidence = %v, want 0.95", results[0].Confidence)
	}
	if results[0].Source != state.MemorySourceUser {
		t.Errorf("source = %q, want %q", results[0].Source, state.MemorySourceUser)
	}
}

func TestMetadata_ReviewFields(t *testing.T) {
	idx := newMetadataTestIndex(t)
	now := time.Now().Unix()
	idx.Add(state.MemoryDoc{
		MemoryID:   "m-reviewed",
		Type:       state.MemoryTypeFact,
		Text:       "reviewed memory entry",
		Confidence: 0.9,
		Source:     state.MemorySourceAgent,
		ReviewedAt: now,
		ReviewedBy: "reviewer-pubkey-abc",
		Unix:       1000,
	})
	results := idx.Search("reviewed memory", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	r := results[0]
	if r.ReviewedAt != now {
		t.Errorf("reviewed_at = %d, want %d", r.ReviewedAt, now)
	}
	if r.ReviewedBy != "reviewer-pubkey-abc" {
		t.Errorf("reviewed_by = %q, want %q", r.ReviewedBy, "reviewer-pubkey-abc")
	}
}

func TestMetadata_ExpiresAt(t *testing.T) {
	idx := newMetadataTestIndex(t)
	futureExpiry := time.Now().Add(24 * time.Hour).Unix()
	idx.Add(state.MemoryDoc{
		MemoryID:  "m-expiring",
		Type:      state.MemoryTypeEpisodic,
		Text:      "temporary episodic memory",
		ExpiresAt: futureExpiry,
		Unix:      1000,
	})
	results := idx.Search("temporary episodic", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	if results[0].ExpiresAt != futureExpiry {
		t.Errorf("expires_at = %d, want %d", results[0].ExpiresAt, futureExpiry)
	}
}

func TestMetadata_DefaultConfidenceIsZero(t *testing.T) {
	// Zero confidence in the struct means "unset" — callers should treat it
	// as DefaultConfidence (0.5). Verify the zero value is preserved.
	doc := state.MemoryDoc{
		MemoryID: "m-noconf",
		Type:     state.MemoryTypeFact,
		Text:     "no confidence set",
		Unix:     1000,
	}
	if doc.Confidence != 0 {
		t.Errorf("expected zero value for unset confidence, got %v", doc.Confidence)
	}
	// EffectiveConfidence helper check.
	if state.DefaultConfidence != 0.5 {
		t.Errorf("DefaultConfidence = %v, want 0.5", state.DefaultConfidence)
	}
}

func TestMetadata_SourceConstants(t *testing.T) {
	for _, src := range []string{
		state.MemorySourceAgent,
		state.MemorySourceUser,
		state.MemorySourceSystem,
		state.MemorySourceImport,
	} {
		if src == "" {
			t.Error("source constant must not be empty")
		}
	}
}

func TestMetadata_JSONShape(t *testing.T) {
	doc := state.MemoryDoc{
		Version:    1,
		MemoryID:   "m-json",
		Type:       state.MemoryTypeFact,
		Text:       "json shape test",
		Confidence: 0.8,
		Source:     state.MemorySourceSystem,
		ReviewedAt: 1700000000,
		ReviewedBy: "reviewer-1",
		ExpiresAt:  1800000000,
		Unix:       1000,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"confidence", "source", "reviewed_at", "reviewed_by", "expires_at"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in JSON", key)
		}
	}
	if m["confidence"].(float64) != 0.8 {
		t.Errorf("confidence = %v, want 0.8", m["confidence"])
	}
	if m["source"] != "system" {
		t.Errorf("source = %v, want system", m["source"])
	}
}

func TestMetadata_OmitEmptyWhenUnset(t *testing.T) {
	doc := state.MemoryDoc{
		Version:  1,
		MemoryID: "m-minimal",
		Type:     state.MemoryTypeFact,
		Text:     "minimal",
		Unix:     100,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"confidence", "source", "reviewed_at", "reviewed_by", "expires_at"} {
		if _, ok := m[key]; ok {
			t.Errorf("key %q should be omitted when unset", key)
		}
	}
}

func TestMetadata_JSONRoundTrip(t *testing.T) {
	original := state.MemoryDoc{
		Version:    1,
		MemoryID:   "m-rt",
		Type:       state.MemoryTypeEpisodic,
		Text:       "round trip test",
		Confidence: 0.72,
		Source:     state.MemorySourceAgent,
		ReviewedAt: 1700000000,
		ReviewedBy: "reviewer-x",
		ExpiresAt:  1800000000,
		GoalID:     "g1",
		TaskID:     "t1",
		RunID:      "r1",
		Unix:       5000,
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded state.MemoryDoc
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Confidence != original.Confidence {
		t.Errorf("confidence = %v, want %v", decoded.Confidence, original.Confidence)
	}
	if decoded.Source != original.Source {
		t.Errorf("source = %q, want %q", decoded.Source, original.Source)
	}
	if decoded.ReviewedAt != original.ReviewedAt {
		t.Errorf("reviewed_at = %d, want %d", decoded.ReviewedAt, original.ReviewedAt)
	}
	if decoded.ReviewedBy != original.ReviewedBy {
		t.Errorf("reviewed_by = %q, want %q", decoded.ReviewedBy, original.ReviewedBy)
	}
	if decoded.ExpiresAt != original.ExpiresAt {
		t.Errorf("expires_at = %d, want %d", decoded.ExpiresAt, original.ExpiresAt)
	}
}

func TestMetadata_IndexedMemoryJSONShape(t *testing.T) {
	im := IndexedMemory{
		MemoryID:   "im-meta",
		Text:       "indexed with metadata",
		Confidence: 0.6,
		Source:     state.MemorySourceImport,
		ReviewedAt: 1700000000,
		ReviewedBy: "rev-1",
		ExpiresAt:  1800000000,
		Unix:       1000,
	}
	raw, err := json.Marshal(im)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"confidence", "source", "reviewed_at", "reviewed_by", "expires_at"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in IndexedMemory JSON", key)
		}
	}
}

func TestMetadata_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta-persist.json")

	idx1, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	idx1.Add(state.MemoryDoc{
		MemoryID:   "m-persist",
		Type:       state.MemoryTypeFact,
		Text:       "persisted with metadata",
		Confidence: 0.85,
		Source:     state.MemorySourceUser,
		ReviewedAt: 1700000000,
		ReviewedBy: "rev-persist",
		ExpiresAt:  1800000000,
		Unix:       2000,
	})
	if err := idx1.Save(); err != nil {
		t.Fatal(err)
	}

	idx2, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	results := idx2.Search("persisted metadata", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 after reload, got %d", len(results))
	}
	r := results[0]
	if r.Confidence != 0.85 {
		t.Errorf("confidence = %v, want 0.85", r.Confidence)
	}
	if r.Source != state.MemorySourceUser {
		t.Errorf("source = %q, want %q", r.Source, state.MemorySourceUser)
	}
	if r.ReviewedAt != 1700000000 {
		t.Errorf("reviewed_at = %d, want 1700000000", r.ReviewedAt)
	}
	if r.ReviewedBy != "rev-persist" {
		t.Errorf("reviewed_by = %q, want rev-persist", r.ReviewedBy)
	}
	if r.ExpiresAt != 1800000000 {
		t.Errorf("expires_at = %d, want 1800000000", r.ExpiresAt)
	}
}

func TestMetadata_DiskFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk-meta.json")

	idx, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	idx.Add(state.MemoryDoc{
		MemoryID:   "m-disk",
		Type:       state.MemoryTypeFact,
		Text:       "disk format check",
		Confidence: 0.77,
		Source:     state.MemorySourceAgent,
		ReviewedAt: 1700000000,
		ReviewedBy: "disk-rev",
		ExpiresAt:  1800000000,
		Unix:       3000,
	})
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk struct {
		Docs []json.RawMessage `json:"docs"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if len(disk.Docs) != 1 {
		t.Fatalf("expected 1 doc on disk, got %d", len(disk.Docs))
	}
	var m map[string]any
	if err := json.Unmarshal(disk.Docs[0], &m); err != nil {
		t.Fatal(err)
	}
	if m["confidence"].(float64) != 0.77 {
		t.Errorf("disk confidence = %v, want 0.77", m["confidence"])
	}
	if m["source"] != "agent" {
		t.Errorf("disk source = %v, want agent", m["source"])
	}
	if m["reviewed_by"] != "disk-rev" {
		t.Errorf("disk reviewed_by = %v, want disk-rev", m["reviewed_by"])
	}
}

func TestMetadata_CombinedWithEpisodicFields(t *testing.T) {
	idx := newMetadataTestIndex(t)
	idx.Add(state.MemoryDoc{
		MemoryID:    "ep-meta",
		Type:        state.MemoryTypeEpisodic,
		Text:        "episodic with full metadata",
		GoalID:      "g-combo",
		TaskID:      "t-combo",
		RunID:       "r-combo",
		EpisodeKind: state.EpisodeKindOutcome,
		Confidence:  0.92,
		Source:      state.MemorySourceAgent,
		ReviewedAt:  1700000000,
		ReviewedBy:  "combo-reviewer",
		ExpiresAt:   1800000000,
		Unix:        4000,
	})

	results := idx.ListByType(state.MemoryTypeEpisodic, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	r := results[0]
	// Episodic fields.
	if r.GoalID != "g-combo" || r.TaskID != "t-combo" || r.RunID != "r-combo" {
		t.Error("episodic correlation fields lost")
	}
	// Metadata fields.
	if r.Confidence != 0.92 {
		t.Errorf("confidence = %v, want 0.92", r.Confidence)
	}
	if r.Source != state.MemorySourceAgent {
		t.Errorf("source = %q, want %q", r.Source, state.MemorySourceAgent)
	}
	if r.ReviewedAt != 1700000000 || r.ReviewedBy != "combo-reviewer" {
		t.Error("review fields lost")
	}
	if r.ExpiresAt != 1800000000 {
		t.Errorf("expires_at = %d, want 1800000000", r.ExpiresAt)
	}
}

func TestMetadata_AllSourceValues(t *testing.T) {
	idx := newMetadataTestIndex(t)
	sources := []string{
		state.MemorySourceAgent,
		state.MemorySourceUser,
		state.MemorySourceSystem,
		state.MemorySourceImport,
	}
	for i, src := range sources {
		idx.Add(state.MemoryDoc{
			MemoryID: GenerateMemoryID(),
			Type:     state.MemoryTypeFact,
			Text:     "source test " + src,
			Source:   src,
			Unix:     int64(i),
		})
	}
	all := idx.Search("source test", 10)
	if len(all) != 4 {
		t.Fatalf("expected 4, got %d", len(all))
	}
	seenSources := map[string]bool{}
	for _, r := range all {
		seenSources[r.Source] = true
	}
	for _, src := range sources {
		if !seenSources[src] {
			t.Errorf("missing source %q in results", src)
		}
	}
}
