package memory

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func newInvalidationTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := OpenIndex(filepath.Join(t.TempDir(), "test.json"))
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestInvalidation_MemStatusConstants(t *testing.T) {
	for _, s := range []string{
		state.MemStatusActive,
		state.MemStatusStale,
		state.MemStatusSuperseded,
		state.MemStatusContradicted,
	} {
		if s == "" {
			t.Error("status constant must not be empty")
		}
		if !state.IsMemStatusValid(s) {
			t.Errorf("IsMemStatusValid(%q) = false, want true", s)
		}
	}
	// Empty is valid (means active).
	if !state.IsMemStatusValid("") {
		t.Error("empty status should be valid")
	}
	// Unknown is invalid.
	if state.IsMemStatusValid("bogus") {
		t.Error("bogus status should be invalid")
	}
}

func TestInvalidation_IsMemoryActive(t *testing.T) {
	tests := []struct {
		status string
		active bool
	}{
		{"", true},
		{state.MemStatusActive, true},
		{state.MemStatusStale, false},
		{state.MemStatusSuperseded, false},
		{state.MemStatusContradicted, false},
	}
	for _, tt := range tests {
		doc := state.MemoryDoc{MemStatus: tt.status}
		if got := doc.IsMemoryActive(); got != tt.active {
			t.Errorf("IsMemoryActive(%q) = %v, want %v", tt.status, got, tt.active)
		}
	}
}

func TestInvalidation_IndexStoresStatus(t *testing.T) {
	idx := newInvalidationTestIndex(t)
	now := time.Now().Unix()
	idx.Add(state.MemoryDoc{
		MemoryID:         "m1",
		Type:             state.MemoryTypeFact,
		Text:             "old fact now stale",
		MemStatus:        state.MemStatusStale,
		InvalidatedAt:    now,
		InvalidatedBy:    "agent-xyz",
		InvalidateReason: "newer data available",
		Unix:             now - 86400,
	})
	results := idx.Search("old fact stale", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	r := results[0]
	if r.MemStatus != state.MemStatusStale {
		t.Errorf("mem_status = %q, want stale", r.MemStatus)
	}
	if r.InvalidatedAt != now {
		t.Errorf("invalidated_at = %d, want %d", r.InvalidatedAt, now)
	}
	if r.InvalidatedBy != "agent-xyz" {
		t.Errorf("invalidated_by = %q, want agent-xyz", r.InvalidatedBy)
	}
	if r.InvalidateReason != "newer data available" {
		t.Errorf("invalidate_reason = %q, want 'newer data available'", r.InvalidateReason)
	}
}

func TestInvalidation_SupersededBy(t *testing.T) {
	idx := newInvalidationTestIndex(t)
	now := time.Now().Unix()
	idx.Add(state.MemoryDoc{
		MemoryID:     "m-old",
		Type:         state.MemoryTypeFact,
		Text:         "original fact",
		MemStatus:    state.MemStatusSuperseded,
		SupersededBy: "m-new",
		Unix:         now - 86400,
	})
	idx.Add(state.MemoryDoc{
		MemoryID: "m-new",
		Type:     state.MemoryTypeFact,
		Text:     "corrected fact",
		Unix:     now,
	})

	all := idx.Search("fact", 10)
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}

	// FilterActiveMemories should only return the new one.
	active := FilterActiveMemories(all, now)
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}
	if active[0].MemoryID != "m-new" {
		t.Errorf("expected m-new, got %q", active[0].MemoryID)
	}

	// Verify superseded_by is preserved.
	for _, r := range all {
		if r.MemoryID == "m-old" && r.SupersededBy != "m-new" {
			t.Errorf("superseded_by = %q, want m-new", r.SupersededBy)
		}
	}
}

func TestInvalidation_ContradictedMemory(t *testing.T) {
	idx := newInvalidationTestIndex(t)
	now := time.Now().Unix()
	idx.Add(state.MemoryDoc{
		MemoryID:         "m-bad",
		Type:             state.MemoryTypeFact,
		Text:             "earth is flat",
		MemStatus:        state.MemStatusContradicted,
		InvalidatedAt:    now,
		InvalidatedBy:    "verifier-task-1",
		InvalidateReason: "contradicted by verified evidence",
		Unix:             now - 86400,
	})

	results := idx.Search("earth flat", 10)
	active := FilterActiveMemories(results, now)
	if len(active) != 0 {
		t.Fatalf("expected 0 active (contradicted), got %d", len(active))
	}
}

func TestInvalidation_JSONShape(t *testing.T) {
	doc := state.MemoryDoc{
		Version:          1,
		MemoryID:         "m-inv",
		Type:             state.MemoryTypeFact,
		Text:             "invalidated doc",
		MemStatus:        state.MemStatusStale,
		SupersededBy:     "m-replacement",
		InvalidatedAt:    1700000000,
		InvalidatedBy:    "reviewer-1",
		InvalidateReason: "outdated info",
		Unix:             1000,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mem_status", "superseded_by", "invalidated_at", "invalidated_by", "invalidate_reason"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in JSON", key)
		}
	}
	if m["mem_status"] != "stale" {
		t.Errorf("mem_status = %v, want stale", m["mem_status"])
	}
}

func TestInvalidation_OmitEmptyWhenActive(t *testing.T) {
	doc := state.MemoryDoc{
		Version:  1,
		MemoryID: "m-active",
		Type:     state.MemoryTypeFact,
		Text:     "active doc",
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
	for _, key := range []string{"mem_status", "superseded_by", "invalidated_at", "invalidated_by", "invalidate_reason"} {
		if _, ok := m[key]; ok {
			t.Errorf("key %q should be omitted for active doc", key)
		}
	}
}

func TestInvalidation_JSONRoundTrip(t *testing.T) {
	original := state.MemoryDoc{
		Version:          1,
		MemoryID:         "m-rt",
		Type:             state.MemoryTypeFact,
		Text:             "round trip",
		MemStatus:        state.MemStatusSuperseded,
		SupersededBy:     "m-new",
		InvalidatedAt:    1700000000,
		InvalidatedBy:    "agent-1",
		InvalidateReason: "replaced",
		Unix:             5000,
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded state.MemoryDoc
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.MemStatus != original.MemStatus {
		t.Errorf("mem_status = %q, want %q", decoded.MemStatus, original.MemStatus)
	}
	if decoded.SupersededBy != original.SupersededBy {
		t.Errorf("superseded_by = %q, want %q", decoded.SupersededBy, original.SupersededBy)
	}
	if decoded.InvalidatedAt != original.InvalidatedAt {
		t.Errorf("invalidated_at = %d, want %d", decoded.InvalidatedAt, original.InvalidatedAt)
	}
	if decoded.InvalidatedBy != original.InvalidatedBy {
		t.Errorf("invalidated_by = %q, want %q", decoded.InvalidatedBy, original.InvalidatedBy)
	}
	if decoded.InvalidateReason != original.InvalidateReason {
		t.Errorf("invalidate_reason = %q, want %q", decoded.InvalidateReason, original.InvalidateReason)
	}
}

func TestInvalidation_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inv-persist.json")

	idx1, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	idx1.Add(state.MemoryDoc{
		MemoryID:         "m-persist",
		Type:             state.MemoryTypeFact,
		Text:             "persisted invalidated",
		MemStatus:        state.MemStatusContradicted,
		InvalidatedAt:    1700000000,
		InvalidatedBy:    "persist-agent",
		InvalidateReason: "test persistence",
		SupersededBy:     "m-repl",
		Unix:             2000,
	})
	if err := idx1.Save(); err != nil {
		t.Fatal(err)
	}

	idx2, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	results := idx2.Search("persisted invalidated", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 after reload, got %d", len(results))
	}
	r := results[0]
	if r.MemStatus != state.MemStatusContradicted {
		t.Errorf("mem_status = %q, want contradicted", r.MemStatus)
	}
	if r.SupersededBy != "m-repl" {
		t.Errorf("superseded_by = %q, want m-repl", r.SupersededBy)
	}
	if r.InvalidatedBy != "persist-agent" {
		t.Errorf("invalidated_by = %q, want persist-agent", r.InvalidatedBy)
	}
}

func TestInvalidation_RankingExcludesInvalid(t *testing.T) {
	now := time.Now().Unix()
	items := []IndexedMemory{
		{MemoryID: "active", Text: "good", Confidence: 0.5, Unix: now},
		{MemoryID: "stale", Text: "stale", MemStatus: state.MemStatusStale, Confidence: 0.9, Unix: now},
		{MemoryID: "superseded", Text: "old", MemStatus: state.MemStatusSuperseded, Confidence: 0.95, Unix: now},
	}
	results := RankRecallResults(items, DefaultRecallPolicy(), 10, now)
	if len(results) != 1 {
		t.Fatalf("expected 1 (only active), got %d", len(results))
	}
	if results[0].Memory.MemoryID != "active" {
		t.Errorf("expected active, got %q", results[0].Memory.MemoryID)
	}
}

func TestInvalidation_IndexedMemoryJSONShape(t *testing.T) {
	im := IndexedMemory{
		MemoryID:         "im-inv",
		Text:             "test",
		MemStatus:        state.MemStatusStale,
		SupersededBy:     "im-new",
		InvalidatedAt:    1700000000,
		InvalidatedBy:    "agent",
		InvalidateReason: "reason",
		Unix:             1000,
	}
	raw, err := json.Marshal(im)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mem_status", "superseded_by", "invalidated_at", "invalidated_by", "invalidate_reason"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in IndexedMemory JSON", key)
		}
	}
}

func TestInvalidation_CombinedWithMetadata(t *testing.T) {
	idx := newInvalidationTestIndex(t)
	now := time.Now().Unix()
	idx.Add(state.MemoryDoc{
		MemoryID:         "m-combo",
		Type:             state.MemoryTypeEpisodic,
		Text:             "combined episodic invalidated",
		GoalID:           "g1",
		TaskID:           "t1",
		Confidence:       0.8,
		Source:           state.MemorySourceAgent,
		ReviewedAt:       now - 100,
		MemStatus:        state.MemStatusStale,
		InvalidatedAt:    now,
		InvalidateReason: "stale after re-run",
		Unix:             now - 86400,
	})

	results := idx.Search("combined episodic", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	r := results[0]
	// All fields should coexist.
	if r.Type != state.MemoryTypeEpisodic {
		t.Errorf("type = %q, want episodic", r.Type)
	}
	if r.GoalID != "g1" {
		t.Errorf("goal_id = %q, want g1", r.GoalID)
	}
	if r.Confidence != 0.8 {
		t.Errorf("confidence = %v, want 0.8", r.Confidence)
	}
	if r.MemStatus != state.MemStatusStale {
		t.Errorf("mem_status = %q, want stale", r.MemStatus)
	}

	// FilterActiveMemories should exclude it.
	active := FilterActiveMemories(results, now)
	if len(active) != 0 {
		t.Errorf("expected 0 active, got %d", len(active))
	}
}
