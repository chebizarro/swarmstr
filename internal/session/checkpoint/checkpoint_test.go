package checkpoint

import (
	"strings"
	"testing"
)

// ─── ResolveReason ──────────────────────────────────────────────────────────

func TestResolveReason_Manual(t *testing.T) {
	if got := ResolveReason("manual", false); got != ReasonManual {
		t.Fatalf("expected %q, got %q", ReasonManual, got)
	}
}

func TestResolveReason_ManualCaseInsensitive(t *testing.T) {
	if got := ResolveReason("Manual", false); got != ReasonManual {
		t.Fatalf("expected %q, got %q", ReasonManual, got)
	}
}

func TestResolveReason_Overflow(t *testing.T) {
	if got := ResolveReason("overflow", false); got != ReasonOverflowRetry {
		t.Fatalf("expected %q, got %q", ReasonOverflowRetry, got)
	}
}

func TestResolveReason_Timeout(t *testing.T) {
	if got := ResolveReason("budget", true); got != ReasonTimeoutRetry {
		t.Fatalf("expected %q, got %q", ReasonTimeoutRetry, got)
	}
}

func TestResolveReason_AutoThreshold(t *testing.T) {
	if got := ResolveReason("", false); got != ReasonAutoThreshold {
		t.Fatalf("expected %q, got %q", ReasonAutoThreshold, got)
	}
}

func TestResolveReason_BudgetNoTimeout(t *testing.T) {
	if got := ResolveReason("budget", false); got != ReasonAutoThreshold {
		t.Fatalf("expected %q, got %q", ReasonAutoThreshold, got)
	}
}

// ─── CaptureSnapshot ────────────────────────────────────────────────────────

func TestCaptureSnapshot_Valid(t *testing.T) {
	snap := CaptureSnapshot("key-1", "sess-1", []string{"e1", "e2", "e3"})
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.SessionKey != "key-1" {
		t.Errorf("SessionKey = %q", snap.SessionKey)
	}
	if snap.SessionID != "sess-1" {
		t.Errorf("SessionID = %q", snap.SessionID)
	}
	if snap.EntryCount != 3 {
		t.Errorf("EntryCount = %d", snap.EntryCount)
	}
	if snap.FirstEntry != "e1" {
		t.Errorf("FirstEntry = %q", snap.FirstEntry)
	}
	if snap.LastEntry != "e3" {
		t.Errorf("LastEntry = %q", snap.LastEntry)
	}
}

func TestCaptureSnapshot_EmptyKey(t *testing.T) {
	if snap := CaptureSnapshot("", "sess-1", []string{"e1"}); snap != nil {
		t.Fatal("expected nil for empty session key")
	}
}

func TestCaptureSnapshot_EmptySessionID(t *testing.T) {
	if snap := CaptureSnapshot("key-1", "", []string{"e1"}); snap != nil {
		t.Fatal("expected nil for empty session ID")
	}
}

func TestCaptureSnapshot_NoEntries(t *testing.T) {
	if snap := CaptureSnapshot("key-1", "sess-1", nil); snap != nil {
		t.Fatal("expected nil for empty entries")
	}
}

func TestCaptureSnapshot_SingleEntry(t *testing.T) {
	snap := CaptureSnapshot("k", "s", []string{"only"})
	if snap == nil {
		t.Fatal("expected non-nil")
	}
	if snap.FirstEntry != "only" || snap.LastEntry != "only" {
		t.Errorf("expected first=last='only', got first=%q last=%q", snap.FirstEntry, snap.LastEntry)
	}
}

// ─── Store: Persist & List ──────────────────────────────────────────────────

func TestStore_PersistAndList(t *testing.T) {
	s := NewStore()
	snap := CaptureSnapshot("k1", "s1", []string{"e1", "e2", "e3"})

	cp := s.Persist(PersistParams{
		SessionKey:     "k1",
		SessionID:      "s1",
		Reason:         ReasonManual,
		Snapshot:       snap,
		Summary:        "test summary",
		FirstKeptEntry: "e2",
		DroppedEntries: 1,
		KeptEntries:    2,
		TokensBefore:   5000,
		TokensAfter:    2000,
		PostEntryCount: 2,
		PostFirstEntry: "e2",
		PostLastEntry:  "e3",
		CreatedAt:      1000,
	})

	if cp.CheckpointID == "" {
		t.Fatal("expected non-empty checkpoint ID")
	}
	if cp.SessionKey != "k1" {
		t.Errorf("SessionKey = %q", cp.SessionKey)
	}
	if cp.Reason != ReasonManual {
		t.Errorf("Reason = %q", cp.Reason)
	}
	if cp.TokensBefore != 5000 || cp.TokensAfter != 2000 {
		t.Errorf("tokens: before=%d after=%d", cp.TokensBefore, cp.TokensAfter)
	}
	if cp.Summary != "test summary" {
		t.Errorf("Summary = %q", cp.Summary)
	}
	if cp.PreCompaction.EntryCount != 3 {
		t.Errorf("PreCompaction.EntryCount = %d", cp.PreCompaction.EntryCount)
	}
	if cp.PostCompaction.EntryCount != 2 {
		t.Errorf("PostCompaction.EntryCount = %d", cp.PostCompaction.EntryCount)
	}

	list := s.List("k1")
	if len(list) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(list))
	}
	if list[0].CheckpointID != cp.CheckpointID {
		t.Errorf("listed checkpoint ID mismatch")
	}
}

func TestStore_ListSortedNewestFirst(t *testing.T) {
	s := NewStore()
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonAutoThreshold, CreatedAt: 100})
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 300})
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonOverflowRetry, CreatedAt: 200})

	list := s.List("k1")
	if len(list) != 3 {
		t.Fatalf("expected 3, got %d", len(list))
	}
	if list[0].CreatedAt != 300 || list[1].CreatedAt != 200 || list[2].CreatedAt != 100 {
		t.Errorf("not sorted newest-first: %v %v %v", list[0].CreatedAt, list[1].CreatedAt, list[2].CreatedAt)
	}
}

func TestStore_ListEmptySession(t *testing.T) {
	s := NewStore()
	if list := s.List("nonexistent"); list != nil {
		t.Errorf("expected nil, got %v", list)
	}
}

// ─── Store: Get ─────────────────────────────────────────────────────────────

func TestStore_Get(t *testing.T) {
	s := NewStore()
	cp := s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 1000})

	got := s.Get("k1", cp.CheckpointID)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.CheckpointID != cp.CheckpointID {
		t.Errorf("ID mismatch")
	}
}

func TestStore_GetNotFound(t *testing.T) {
	s := NewStore()
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 1000})

	if got := s.Get("k1", "nonexistent"); got != nil {
		t.Errorf("expected nil for nonexistent ID")
	}
}

func TestStore_GetEmptyID(t *testing.T) {
	s := NewStore()
	if got := s.Get("k1", "  "); got != nil {
		t.Errorf("expected nil for blank ID")
	}
}

func TestStore_GetWrongSession(t *testing.T) {
	s := NewStore()
	cp := s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 1000})

	if got := s.Get("k2", cp.CheckpointID); got != nil {
		t.Errorf("expected nil for wrong session key")
	}
}

// ─── Store: Len / SessionCount ──────────────────────────────────────────────

func TestStore_LenAndSessionCount(t *testing.T) {
	s := NewStore()
	if s.Len() != 0 || s.SessionCount() != 0 {
		t.Fatal("expected 0/0")
	}
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 1})
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 2})
	s.Persist(PersistParams{SessionKey: "k2", SessionID: "s2", Reason: ReasonManual, CreatedAt: 3})

	if s.Len() != 3 {
		t.Errorf("Len = %d, want 3", s.Len())
	}
	if s.SessionCount() != 2 {
		t.Errorf("SessionCount = %d, want 2", s.SessionCount())
	}
}

// ─── Store: Load / Export ───────────────────────────────────────────────────

func TestStore_LoadAndExport(t *testing.T) {
	s := NewStore()
	imported := []Checkpoint{
		{CheckpointID: "cp-1", SessionKey: "k1", SessionID: "s1", CreatedAt: 100, Reason: ReasonManual},
		{CheckpointID: "cp-2", SessionKey: "k1", SessionID: "s1", CreatedAt: 200, Reason: ReasonAutoThreshold},
	}
	s.Load("k1", imported)

	if s.Len() != 2 {
		t.Fatalf("Len = %d after load", s.Len())
	}

	exported := s.Export("k1")
	if len(exported) != 2 {
		t.Fatalf("exported %d", len(exported))
	}
	if exported[0].CheckpointID != "cp-1" {
		t.Errorf("first exported ID = %q", exported[0].CheckpointID)
	}
}

func TestStore_LoadEmpty(t *testing.T) {
	s := NewStore()
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 1})
	s.Load("k1", nil)

	if s.Len() != 0 {
		t.Errorf("expected 0 after load(nil), got %d", s.Len())
	}
}

func TestStore_ExportEmpty(t *testing.T) {
	s := NewStore()
	if exported := s.Export("nonexistent"); exported != nil {
		t.Errorf("expected nil, got %v", exported)
	}
}

// ─── Store: Delete ──────────────────────────────────────────────────────────

func TestStore_Delete(t *testing.T) {
	s := NewStore()
	s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 1})
	s.Persist(PersistParams{SessionKey: "k2", SessionID: "s2", Reason: ReasonManual, CreatedAt: 2})

	s.Delete("k1")

	if s.Len() != 1 {
		t.Errorf("Len = %d after delete, want 1", s.Len())
	}
	if list := s.List("k1"); list != nil {
		t.Errorf("expected nil after delete")
	}
}

// ─── Trim ───────────────────────────────────────────────────────────────────

func TestStore_TrimToMax(t *testing.T) {
	s := NewStore()
	for i := 0; i < MaxCheckpointsPerSession+10; i++ {
		s.Persist(PersistParams{
			SessionKey: "k1",
			SessionID:  "s1",
			Reason:     ReasonAutoThreshold,
			CreatedAt:  int64(i + 1),
		})
	}

	list := s.List("k1")
	if len(list) != MaxCheckpointsPerSession {
		t.Fatalf("expected %d checkpoints, got %d", MaxCheckpointsPerSession, len(list))
	}
	// Newest should be preserved.
	if list[0].CreatedAt != int64(MaxCheckpointsPerSession+10) {
		t.Errorf("newest checkpoint CreatedAt = %d, want %d", list[0].CreatedAt, MaxCheckpointsPerSession+10)
	}
	// Oldest retained should be index 10 (0-indexed from i=10 → CreatedAt=11).
	if list[len(list)-1].CreatedAt != 11 {
		t.Errorf("oldest retained CreatedAt = %d, want 11", list[len(list)-1].CreatedAt)
	}
}

// ─── Persist without Snapshot ───────────────────────────────────────────────

func TestStore_PersistNilSnapshot(t *testing.T) {
	s := NewStore()
	cp := s.Persist(PersistParams{
		SessionKey: "k1",
		SessionID:  "s1",
		Reason:     ReasonAutoThreshold,
		CreatedAt:  500,
	})
	if cp.PreCompaction.SessionID != "" {
		t.Errorf("expected empty pre-compaction with nil snapshot, got SessionID=%q", cp.PreCompaction.SessionID)
	}
}

// ─── Summary trimming ───────────────────────────────────────────────────────

func TestStore_PersistTrimsSummary(t *testing.T) {
	s := NewStore()
	cp := s.Persist(PersistParams{
		SessionKey: "k1",
		SessionID:  "s1",
		Reason:     ReasonManual,
		Summary:    "  padded summary  ",
		CreatedAt:  1,
	})
	if cp.Summary != "padded summary" {
		t.Errorf("Summary = %q, expected trimmed", cp.Summary)
	}
}

// ─── Checkpoint ID format ───────────────────────────────────────────────────

func TestCheckpointIDIsUUID(t *testing.T) {
	s := NewStore()
	cp := s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, CreatedAt: 1})

	// UUID v4 format: 8-4-4-4-12 hex digits.
	parts := strings.Split(cp.CheckpointID, "-")
	if len(parts) != 5 {
		t.Errorf("expected UUID format (5 parts), got %q (%d parts)", cp.CheckpointID, len(parts))
	}
}

// ─── FormatCheckpointID ─────────────────────────────────────────────────────

func TestFormatCheckpointID(t *testing.T) {
	if got := FormatCheckpointID("test", 42); got != "test-0042" {
		t.Errorf("FormatCheckpointID = %q", got)
	}
}

// ─── Store: concurrent safety ───────────────────────────────────────────────

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 100; i++ {
			s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonAutoThreshold, CreatedAt: int64(i)})
		}
		close(done)
	}()

	for i := 0; i < 100; i++ {
		s.List("k1")
		s.Len()
		s.SessionCount()
	}
	<-done

	if s.Len() < 1 {
		t.Errorf("expected checkpoints after concurrent access")
	}
}

// ─── ToMap ───────────────────────────────────────────────────────────────────

func TestCheckpoint_ToMap(t *testing.T) {
	cp := Checkpoint{
		CheckpointID:   "cp-1",
		SessionKey:     "k1",
		SessionID:      "s1",
		CreatedAt:      1234,
		Reason:         ReasonManual,
		TokensBefore:   5000,
		TokensAfter:    2000,
		Summary:        "test",
		FirstKeptEntry: "e3",
		DroppedEntries: 2,
		KeptEntries:    3,
		PreCompaction:  TranscriptRef{SessionID: "s1", EntryCount: 5, FirstEntry: "e1", LastEntry: "e5"},
		PostCompaction: TranscriptRef{SessionID: "s1", EntryCount: 3, FirstEntry: "e3", LastEntry: "e5"},
	}
	m := cp.ToMap()

	if m["checkpoint_id"] != "cp-1" {
		t.Errorf("checkpoint_id = %v", m["checkpoint_id"])
	}
	if m["reason"] != "manual" {
		t.Errorf("reason = %v", m["reason"])
	}
	if m["tokens_before"] != 5000 {
		t.Errorf("tokens_before = %v", m["tokens_before"])
	}
	pre := m["pre_compaction"].(map[string]any)
	if pre["entry_count"] != 5 {
		t.Errorf("pre entry_count = %v", pre["entry_count"])
	}
	post := m["post_compaction"].(map[string]any)
	if post["entry_count"] != 3 {
		t.Errorf("post entry_count = %v", post["entry_count"])
	}
}

func TestCheckpoint_ToMapOmitsZeros(t *testing.T) {
	cp := Checkpoint{
		CheckpointID: "cp-2",
		SessionKey:   "k1",
		SessionID:    "s1",
		CreatedAt:    100,
		Reason:       ReasonAutoThreshold,
	}
	m := cp.ToMap()

	for _, key := range []string{"tokens_before", "tokens_after", "summary", "first_kept_entry_id", "dropped_entries", "kept_entries"} {
		if _, ok := m[key]; ok {
			t.Errorf("expected %q to be omitted for zero value", key)
		}
	}
}

// ─── Store: Get returns copy ────────────────────────────────────────────────

func TestStore_GetReturnsCopy(t *testing.T) {
	s := NewStore()
	cp := s.Persist(PersistParams{SessionKey: "k1", SessionID: "s1", Reason: ReasonManual, Summary: "original", CreatedAt: 1})

	got := s.Get("k1", cp.CheckpointID)
	got.Summary = "mutated"

	got2 := s.Get("k1", cp.CheckpointID)
	if got2.Summary != "original" {
		t.Errorf("Get did not return a copy; mutation leaked: Summary = %q", got2.Summary)
	}
}
