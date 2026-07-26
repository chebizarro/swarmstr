package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// fakeMemoryEncryptor is a deterministic stand-in for the NIP-44 self codec.
type fakeMemoryEncryptor struct{ calls int }

func (f *fakeMemoryEncryptor) EncryptMemoryPayload(plaintext string) (string, string, error) {
	f.calls++
	return "enc:" + plaintext, "fake", nil
}

func seedPromotableMemory(t *testing.T, b *SQLiteBackend, id, topic, source string, recallUnix int64) {
	t.Helper()
	b.Add(memoryDocFromIndexed(IndexedMemory{
		MemoryID:   id,
		Text:       "memory " + id,
		Topic:      topic,
		Source:     source,
		Unix:       recallUnix,
		Confidence: 0.9,
	}))
	if _, err := b.db.Exec(`
		INSERT INTO recall_tracking (memory_id, recall_count, unique_queries, last_recall_unix, first_recall_unix, avg_score)
		VALUES (?, 5, 3, ?, ?, 0.95)
	`, id, recallUnix, recallUnix); err != nil {
		t.Fatalf("seed recall_tracking: %v", err)
	}
}

func TestDreamDiary_AppendListReset(t *testing.T) {
	b, _ := createTestSQLiteBackend(t)
	defer b.Close()
	ctx := context.Background()

	entry := DreamDiaryEntry{
		Phase:                DreamingPhaseREM,
		Scope:                "agentA",
		CandidatesConsidered: 3,
		CandidateIDs:         []string{"m1", "m2", "m3"},
		PromotedRecordIDs:    []string{"m1"},
		Narrative:            "reviewed 3, promoted 1",
	}
	saved, err := b.AppendDreamDiaryEntry(ctx, entry)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if saved.ID == "" || saved.Date == "" {
		t.Fatalf("expected normalized ID and date, got %+v", saved)
	}
	if saved.Counts["considered"] != 3 || saved.Counts["promoted"] != 1 {
		t.Fatalf("unexpected counts: %v", saved.Counts)
	}

	// Second scope isolates.
	if _, err := b.AppendDreamDiaryEntry(ctx, DreamDiaryEntry{Phase: DreamingPhaseLight, Scope: "agentB", CandidatesConsidered: 1}); err != nil {
		t.Fatalf("append B: %v", err)
	}

	all, err := b.ListDreamDiaryEntries(ctx, DreamDiaryFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	scopedA, err := b.ListDreamDiaryEntries(ctx, DreamDiaryFilter{Scope: "agentA"})
	if err != nil {
		t.Fatalf("list scoped: %v", err)
	}
	if len(scopedA) != 1 || scopedA[0].Scope != "agentA" {
		t.Fatalf("expected 1 agentA entry, got %d", len(scopedA))
	}
	// Round-trip the persisted candidate/promoted IDs.
	if len(scopedA[0].CandidateIDs) != 3 || len(scopedA[0].PromotedRecordIDs) != 1 {
		t.Fatalf("candidate/promoted IDs not persisted: %+v", scopedA[0])
	}

	// Reset scope A only.
	removed, err := b.ResetDreamDiary(ctx, "agentA")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}
	remaining, _ := b.ListDreamDiaryEntries(ctx, DreamDiaryFilter{})
	if len(remaining) != 1 || remaining[0].Scope != "agentB" {
		t.Fatalf("reset should leave only agentB, got %d", len(remaining))
	}
}

func TestDreamDiary_EncryptedOutboxPublish(t *testing.T) {
	b, _ := createTestSQLiteBackend(t)
	defer b.Close()
	ctx := context.Background()

	enc := &fakeMemoryEncryptor{}
	b.SetMemoryPayloadEncryptor(enc)

	entry, err := b.AppendDreamDiaryEntry(ctx, DreamDiaryEntry{Phase: DreamingPhaseREM, Scope: "s1", CandidatesConsidered: 2, Narrative: "n"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if enc.calls != 1 {
		t.Fatalf("expected encryptor to be called once, got %d", enc.calls)
	}

	events, err := b.DueMemoryOutboxEvents(ctx, time.Now().UTC(), 10, true)
	if err != nil {
		t.Fatalf("due events: %v", err)
	}
	var diaryEvents []MemoryOutboxEvent
	for _, ev := range events {
		if ev.EventKind == MemoryOutboxKindDreamDiary {
			diaryEvents = append(diaryEvents, ev)
		}
	}
	if len(diaryEvents) != 1 {
		t.Fatalf("expected 1 dream_diary outbox event, got %d", len(diaryEvents))
	}
	var payload DreamDiaryOutboxPayload
	if err := json.Unmarshal([]byte(diaryEvents[0].Payload), &payload); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if !payload.Encrypted || payload.Algo != "fake" {
		t.Fatalf("expected encrypted fake payload, got %+v", payload)
	}
	if !strings.HasPrefix(payload.Content, "enc:") {
		t.Fatalf("expected ciphertext, got %q", payload.Content)
	}
	if payload.EntryID != entry.ID {
		t.Fatalf("payload entry id mismatch: %s vs %s", payload.EntryID, entry.ID)
	}
	// The decrypted content is the full diary entry JSON.
	plain := strings.TrimPrefix(payload.Content, "enc:")
	var roundtrip DreamDiaryEntry
	if err := json.Unmarshal([]byte(plain), &roundtrip); err != nil {
		t.Fatalf("decrypt roundtrip: %v", err)
	}
	if roundtrip.ID != entry.ID || roundtrip.Phase != DreamingPhaseREM {
		t.Fatalf("roundtrip mismatch: %+v", roundtrip)
	}
}

func TestDreamDiary_CleartextOutboxWhenNoEncryptor(t *testing.T) {
	b, _ := createTestSQLiteBackend(t)
	defer b.Close()
	ctx := context.Background()

	if _, err := b.AppendDreamDiaryEntry(ctx, DreamDiaryEntry{Phase: DreamingPhaseREM, CandidatesConsidered: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, _ := b.DueMemoryOutboxEvents(ctx, time.Now().UTC(), 10, true)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}
	var payload DreamDiaryOutboxPayload
	if err := json.Unmarshal([]byte(events[0].Payload), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Encrypted {
		t.Fatalf("expected cleartext payload when no encryptor configured")
	}
}

func TestRunDreamingCycleWithDiary_PersistsEntries(t *testing.T) {
	b, _ := createTestSQLiteBackend(t)
	defer b.Close()
	ctx := context.Background()

	now := time.Now().Unix()
	seedPromotableMemory(t, b, "c1", "alpha", "tool", now)
	seedPromotableMemory(t, b, "c2", "alpha", "tool", now)
	seedPromotableMemory(t, b, "c3", "beta", "file", now)

	cfg := DefaultPromotionConfig()
	cfg.MinScore = 0.1
	manager := NewPromotionManager(b, cfg)

	result, entries, err := RunDreamingCycleWithDiary(ctx, manager, b, DreamingConfig{Enabled: true}, DreamDiaryWriteOptions{Scope: "nodeX"})
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if result == nil || result.Promoted == 0 {
		t.Fatalf("expected promotions, got %+v", result)
	}
	if len(entries) == 0 {
		t.Fatalf("expected diary entries persisted")
	}
	var promotedTotal int
	for _, e := range entries {
		if e.Scope != "nodeX" {
			t.Fatalf("entry scope mismatch: %s", e.Scope)
		}
		if e.Narrative == "" {
			t.Fatalf("expected persisted narrative for phase %s", e.Phase)
		}
		promotedTotal += len(e.PromotedRecordIDs)
	}
	if promotedTotal == 0 {
		t.Fatalf("expected promoted record IDs captured in diary entries")
	}

	// Entries are durably listable.
	listed, _ := b.ListDreamDiaryEntries(ctx, DreamDiaryFilter{Scope: "nodeX"})
	if len(listed) != len(entries) {
		t.Fatalf("expected %d listed entries, got %d", len(entries), len(listed))
	}
}
