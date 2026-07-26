package memory

// maintenance_concurrency_test.go — concurrency hardening coverage for the store
// maintenance gate (swarmstr-r34j / swarmstr-s4wl).
//
// These tests assert three things:
//   1. WithMaintenanceLock genuinely serializes (never more than one holder), and
//      the pre-existing mutating ops (compaction) serialize against a promotion
//      sweep on the SAME gate.
//   2. Candidate promotion is revalidated atomically inside the claim, so two
//      managers racing the same candidate yield exactly one promotion (no
//      lost/duplicated records).
//   3. The documented lock order (maintenanceMu → PromotionManager.mu → b.mu) is
//      deadlock-free: a storm of every gated op type completes and leaves the
//      FTS index consistent with memory_records. Run with `go test -race`.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// seedPromotionCandidate inserts a chunk + a recall_tracking row that clears the
// given config's thresholds so the memory is a live, not-yet-promoted candidate.
func seedPromotionCandidate(t *testing.T, backend *SQLiteBackend, id, text, topic string) {
	t.Helper()
	addTestMemory(backend, id, text, topic)
	now := time.Now().Unix()
	if _, err := backend.db.Exec(`INSERT INTO recall_tracking
		(memory_id, recall_count, unique_queries, query_hashes, last_recall_unix, first_recall_unix, avg_score, promoted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
		id, 5, 3, "[]", now, now-100, 0.9); err != nil {
		t.Fatalf("seed recall_tracking %s: %v", id, err)
	}
}

func promotedCount(t *testing.T, backend *SQLiteBackend) int {
	t.Helper()
	var n int
	if err := backend.db.QueryRow(`SELECT COUNT(*) FROM recall_tracking WHERE promoted_at IS NOT NULL`).Scan(&n); err != nil {
		t.Fatalf("count promoted: %v", err)
	}
	return n
}

// TestWithMaintenanceLockSerializes proves the gate primitive lets at most one
// holder run at a time.
func TestWithMaintenanceLockSerializes(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	var inFlight int32
	var maxObserved int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = backend.WithMaintenanceLock(func() error {
				cur := atomic.AddInt32(&inFlight, 1)
				for {
					prev := atomic.LoadInt32(&maxObserved)
					if cur <= prev || atomic.CompareAndSwapInt32(&maxObserved, prev, cur) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&inFlight, -1)
				return nil
			})
		}()
	}
	wg.Wait()
	if maxObserved != 1 {
		t.Fatalf("maintenance gate allowed %d concurrent holders, want 1", maxObserved)
	}
}

// TestMaintenanceGateSerializesRealOps proves that a pre-existing mutating op
// (CompactMemoryRecords) blocks on the same gate a promotion sweep holds — i.e.
// the pre-existing op is now routed through the gate.
func TestMaintenanceGateSerializesRealOps(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5
	cfg.EnableSummary = true
	manager := NewPromotionManager(backend, cfg)

	// Two candidates on one topic so promoteGroup takes the summary path and the
	// summarizer callback runs while the gate is held.
	seedPromotionCandidate(t, backend, "hot-1", "insight one about deployments", "ops")
	seedPromotionCandidate(t, backend, "hot-2", "insight two about deployments", "ops")

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	manager.SetSummarizer(func([]IndexedMemory) (string, error) {
		once.Do(func() { close(entered) })
		<-release
		return "consolidated summary", nil
	})

	promoteDone := make(chan struct{})
	go func() {
		defer close(promoteDone)
		_, _ = manager.Promote()
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("promotion summarizer never ran; cannot exercise gate contention")
	}

	// The promotion sweep now holds maintenanceMu (blocked in the summarizer).
	// A compaction must not be able to proceed until the sweep releases.
	compactDone := make(chan struct{})
	go func() {
		defer close(compactDone)
		_, _ = backend.CompactMemoryRecords(context.Background(), CompactionConfig{Reason: "test", SkipDedupe: true, SkipExpireStale: true})
	}()

	select {
	case <-compactDone:
		close(release)
		t.Fatal("compaction completed while a promotion sweep held the maintenance gate (not serialized)")
	case <-time.After(150 * time.Millisecond):
		// Expected: compaction is blocked on the gate.
	}

	close(release)

	select {
	case <-compactDone:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction did not complete after the promotion sweep released the gate (possible deadlock)")
	}
	<-promoteDone
}

// TestPromoteAtomicClaimSingleWinner proves candidate revalidation inside the
// promotion claim: two managers racing the same candidate promote it exactly
// once — no double-promotion, no lost update.
func TestPromoteAtomicClaimSingleWinner(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5
	seedPromotionCandidate(t, backend, "solo", "the one candidate", "topic")

	// Two managers over the SAME backend — they share maintenanceMu but not
	// PromotionManager.mu, exactly the cross-instance race the gate must cover.
	m1 := NewPromotionManager(backend, cfg)
	m2 := NewPromotionManager(backend, cfg)

	var total int64
	var wg sync.WaitGroup
	for _, m := range []*PromotionManager{m1, m2} {
		m := m
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := m.Promote()
			if err != nil {
				t.Errorf("Promote: %v", err)
				return
			}
			atomic.AddInt64(&total, int64(res.Promoted))
		}()
	}
	wg.Wait()

	if total != 1 {
		t.Fatalf("combined Promoted across racing managers = %d, want exactly 1", total)
	}
	if got := promotedCount(t, backend); got != 1 {
		t.Fatalf("recall_tracking promoted rows = %d, want 1", got)
	}
}

// TestRepairMemoryHealthNoSelfDeadlock proves the gate split: RepairMemoryHealth
// acquires the gate and its inner compaction runs ungated, so the op completes
// (rather than self-deadlocking on the non-reentrant maintenanceMu).
func TestRepairMemoryHealthNoSelfDeadlock(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()
	addTestMemory(backend, "r1", "alpha bravo charlie", "topic-a")
	addTestMemory(backend, "r2", "delta echo foxtrot", "topic-b")

	done := make(chan error, 1)
	go func() {
		_, err := backend.RepairMemoryHealth(context.Background(), MemoryHealthRepairOptions{FixAll: true})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RepairMemoryHealth: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RepairMemoryHealth did not return — self-deadlock on maintenanceMu")
	}
}

// TestMaintenanceConcurrentWritersIntegrity storms every gated op type against a
// single store and asserts the lock order is deadlock-free and leaves the FTS
// index consistent with memory_records (no lost/duplicated records). Intended to
// run under `go test -race`.
func TestMaintenanceConcurrentWritersIntegrity(t *testing.T) {
	backend, _ := createTestSQLiteBackend(t)
	defer backend.Close()

	cfg := DefaultPromotionConfig()
	cfg.MinRecallCount = 2
	cfg.MinUniqueQueries = 1
	cfg.MinScore = 0.5

	const seed = 24
	for i := 0; i < seed; i++ {
		seedPromotionCandidate(t, backend, id(i), text(i), "ops")
	}
	var recordsBefore int
	if err := backend.db.QueryRow(`SELECT COUNT(*) FROM memory_records`).Scan(&recordsBefore); err != nil {
		t.Fatalf("count records before: %v", err)
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	launch := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	for i := 0; i < 4; i++ {
		launch(func() { _, _ = NewPromotionManager(backend, cfg).Promote() })
		launch(func() { _, _ = backend.CompactMemoryRecords(ctx, CompactionConfig{Reason: "storm", SkipDedupe: true, SkipExpireStale: true}) })
		launch(func() { _, _ = backend.RepairMemoryHealth(ctx, MemoryHealthRepairOptions{FixAll: true}) })
		launch(func() { _, _ = backend.MemoryMigrationApply(ctx, MemoryMigrationApplyOptions{RebuildFTS: true}) })
		launch(func() { _, _ = backend.RunREMHarness(ctx, REMHarnessOptions{Apply: true, Limit: 50}) })
		launch(func() { _, _ = RunDreamingPhases(NewPromotionManager(backend, cfg), DreamingConfig{Enabled: true}, nil) })
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent maintenance ops did not all complete — likely a lock-order deadlock")
	}

	// Invariant: no active memory_records were lost (compaction only supersedes
	// exact duplicates, and we seeded unique text).
	var recordsAfter int
	if err := backend.db.QueryRow(`SELECT COUNT(*) FROM memory_records`).Scan(&recordsAfter); err != nil {
		t.Fatalf("count records after: %v", err)
	}
	if recordsAfter != recordsBefore {
		t.Fatalf("record count changed under concurrent maintenance: before=%d after=%d", recordsBefore, recordsAfter)
	}

	// Invariant: FTS is consistent with memory_records (a rebuild/trigger race
	// would leave drift).
	plan, err := backend.MemoryMigrationPlan(ctx)
	if err != nil {
		t.Fatalf("MemoryMigrationPlan: %v", err)
	}
	if plan.FTSDrift != 0 {
		t.Fatalf("FTS drift after concurrent maintenance = %d, want 0 (FTS/record inconsistency)", plan.FTSDrift)
	}
}

func id(i int) string {
	return "rec-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

func text(i int) string {
	return "unique candidate text number " + id(i)
}
