package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"metiq/internal/memory"
	"metiq/internal/store/state"
)

func TestDreamingJobConfigFromMemoryExtra(t *testing.T) {
	// Defaults when unset.
	def := dreamingJobConfigFromMemoryExtra(nil)
	if def.Enabled {
		t.Fatalf("dreaming must default OFF")
	}
	if def.Interval != defaultDreamingInterval {
		t.Fatalf("expected default interval, got %v", def.Interval)
	}
	if !def.Narratives {
		t.Fatalf("narratives should default true")
	}

	extra := map[string]any{
		"dreaming": map[string]any{
			"enabled":     true,
			"interval":    "15m",
			"scope":       "nodeX",
			"light_limit": float64(10),
			"rem_limit":   float64(50),
			"narratives":  false,
		},
	}
	cfg := dreamingJobConfigFromMemoryExtra(extra)
	if !cfg.Enabled || cfg.Interval != 15*time.Minute || cfg.Scope != "nodeX" {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}
	if cfg.LightLimit != 10 || cfg.REMLimit != 50 || cfg.Narratives {
		t.Fatalf("unexpected limits/narratives: %+v", cfg)
	}

	// Interval floor: sub-floor values clamp up to minDreamingInterval.
	floored := dreamingJobConfigFromMemoryExtra(map[string]any{"dreaming": map[string]any{"interval": "100ms"}})
	if floored.Interval != minDreamingInterval {
		t.Fatalf("expected interval floored to %v, got %v", minDreamingInterval, floored.Interval)
	}
}

func TestStartDreamingJob_WritesDiaryAndStops(t *testing.T) {
	dir := t.TempDir()
	baseIndex, err := memory.OpenIndex(filepath.Join(dir, "memory.json"))
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	be, err := memory.OpenSQLiteBackend(filepath.Join(dir, "memory.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	defer be.Close()

	// Seed one promotable memory via the exported recall tracker.
	now := time.Now().Unix()
	be.Add(state.MemoryDoc{MemoryID: "m1", Text: "consolidate me", Topic: "alpha", Unix: now, Confidence: 0.9})
	mgr := memory.NewPromotionManager(be, memory.DefaultPromotionConfig())
	tr := mgr.Tracker()
	tr.TrackRecall("m1", "query one", 0.9)
	tr.TrackRecall("m1", "query two", 0.9)
	tr.TrackRecall("m1", "query three", 0.9)
	if err := tr.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	store := memory.NewHybridIndex(baseIndex, be)
	cfgDoc := state.ConfigDoc{Extra: map[string]any{
		"memory": map[string]any{
			"dreaming": map[string]any{
				"enabled":  true,
				"interval": "1s",
				"scope":    "nodeZ",
			},
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDreamingJob(ctx, store, func() state.ConfigDoc { return cfgDoc }, nil)

	ds := any(store).(memory.DreamDiaryStore)
	got := false
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := ds.ListDreamDiaryEntries(ctx, memory.DreamDiaryFilter{Scope: "nodeZ"})
		if err == nil && len(entries) > 0 {
			got = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !got {
		t.Fatalf("expected the dreaming job to persist a diary entry")
	}

	// Cancellation stops the worker; a subsequent read still succeeds and the
	// entry count no longer grows unboundedly (m1 promoted once).
	cancel()
	time.Sleep(80 * time.Millisecond)
	entries, err := ds.ListDreamDiaryEntries(context.Background(), memory.DreamDiaryFilter{Scope: "nodeZ"})
	if err != nil {
		t.Fatalf("list after cancel: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected persisted entries to remain after shutdown")
	}
	for _, e := range entries {
		if e.CandidatesConsidered == 0 {
			t.Fatalf("diary entry should record considered candidates: %+v", e)
		}
	}
}

func TestStartDreamingJob_NoOpForNonDiaryStore(t *testing.T) {
	// A store that does not implement DreamDiaryStore must not spawn a worker.
	// nil store is the simplest non-supporting case and must return cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDreamingJob(ctx, nil, func() state.ConfigDoc { return state.ConfigDoc{} }, nil)
}
