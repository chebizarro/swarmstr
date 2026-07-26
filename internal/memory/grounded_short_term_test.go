package memory

import (
	"context"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func markPromoted(t *testing.T, b *SQLiteBackend, id string, at int64) {
	t.Helper()
	if _, err := b.db.Exec(`UPDATE recall_tracking SET promoted_at = ?, promoted_to = 'consolidated' WHERE memory_id = ?`, at, id); err != nil {
		t.Fatalf("mark promoted: %v", err)
	}
}

func isPromoted(t *testing.T, b *SQLiteBackend, id string) bool {
	t.Helper()
	var at *int64
	if err := b.db.QueryRow(`SELECT promoted_at FROM recall_tracking WHERE memory_id = ?`, id).Scan(&at); err != nil {
		t.Fatalf("query promoted: %v", err)
	}
	return at != nil && *at > 0
}

func chunkExists(t *testing.T, b *SQLiteBackend, id string) bool {
	t.Helper()
	var n int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("query chunk: %v", err)
	}
	return n > 0
}

func TestGroundedShortTerm_SelectionAndReset(t *testing.T) {
	b, _ := createTestSQLiteBackend(t)
	defer b.Close()
	ctx := context.Background()

	now := time.Now()
	recent := now.Unix()
	old := now.Add(-30 * 24 * time.Hour).Unix()

	seedPromotableMemory(t, b, "cited-recent-unpromoted", "alpha", "tool", recent)
	seedPromotableMemory(t, b, "cited-recent-promoted", "alpha", "file", recent)
	markPromoted(t, b, "cited-recent-promoted", recent)
	seedPromotableMemory(t, b, "uncited-recent", "beta", "", recent)
	seedPromotableMemory(t, b, "cited-old", "gamma", "tool", old)

	opts := GroundedShortTermOptions{Now: now}
	items, err := b.GroundedShortTerm(ctx, opts)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.MemoryID] = it.Promoted
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 cited-recent items, got %d (%v)", len(items), got)
	}
	if _, ok := got["cited-recent-unpromoted"]; !ok {
		t.Fatalf("missing cited-recent-unpromoted")
	}
	if promoted, ok := got["cited-recent-promoted"]; !ok || !promoted {
		t.Fatalf("cited-recent-promoted should be present and flagged promoted")
	}
	if _, ok := got["uncited-recent"]; ok {
		t.Fatalf("uncited-recent must be excluded when citation required")
	}
	if _, ok := got["cited-old"]; ok {
		t.Fatalf("cited-old must be excluded by recency window")
	}

	// RequireCitation=false includes the uncited recent memory.
	itemsNoCite, err := b.GroundedShortTerm(ctx, opts.WithExplicitCitation(false))
	if err != nil {
		t.Fatalf("read no-cite: %v", err)
	}
	if len(itemsNoCite) != 3 {
		t.Fatalf("expected 3 items without citation requirement, got %d", len(itemsNoCite))
	}

	// Reset demotes only the promoted-in-window record.
	res, err := b.ResetGroundedShortTerm(ctx, GroundedShortTermOptions{Now: now})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if res.Demoted != 1 || res.Considered != 1 {
		t.Fatalf("expected 1 considered/demoted, got considered=%d demoted=%d", res.Considered, res.Demoted)
	}
	if isPromoted(t, b, "cited-recent-promoted") {
		t.Fatalf("record should be demoted (promotion cleared)")
	}
	// Reversibility: the underlying chunk is never deleted.
	if !chunkExists(t, b, "cited-recent-promoted") {
		t.Fatalf("demote must not delete the memory record")
	}
	// The unpromoted record is untouched.
	if isPromoted(t, b, "cited-recent-unpromoted") {
		t.Fatalf("unpromoted record must remain unpromoted")
	}
}

func TestGroundedShortTerm_ResetIsReversible(t *testing.T) {
	b, _ := createTestSQLiteBackend(t)
	defer b.Close()
	ctx := context.Background()

	now := time.Now()
	seedPromotableMemory(t, b, "r1", "alpha", "tool", now.Unix())
	markPromoted(t, b, "r1", now.Unix())

	if _, err := b.ResetGroundedShortTerm(ctx, GroundedShortTermOptions{Now: now}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if isPromoted(t, b, "r1") {
		t.Fatalf("expected demotion")
	}
	// After demotion the record is once again a promotion candidate.
	cfg := DefaultPromotionConfig()
	cfg.MinScore = 0.1
	manager := NewPromotionManager(b, cfg)
	candidates, err := manager.FindCandidates()
	if err != nil {
		t.Fatalf("find candidates: %v", err)
	}
	found := false
	for _, c := range candidates {
		if c.Memory.MemoryID == "r1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("demoted record should re-enter the promotion candidate pool")
	}
}
