package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestSessionMaintenanceWarnSelectsOldestAndProtectsActive(t *testing.T) {
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0)
	entries := map[string]state.SessionEntry{
		"old-1":  {SessionID: "old-1", LastChannel: "nostr", LastTo: "npub-requester", CreatedAt: now.Add(-4 * time.Hour), UpdatedAt: now.Add(-4 * time.Hour)},
		"old-2":  {SessionID: "old-2", CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour)},
		"new":    {SessionID: "new", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		"active": {SessionID: "active", ActiveRunID: "run", CreatedAt: now.Add(-10 * time.Hour), UpdatedAt: now.Add(-10 * time.Hour)},
	}
	for key, entry := range entries {
		if err := store.Put(key, entry); err != nil {
			t.Fatal(err)
		}
	}
	maxEntries := 2
	zero := int64(0)
	service := newSessionMaintenanceService(nil, nil, store, func() state.SessionConfig {
		return state.SessionConfig{Maintenance: &state.SessionMaintenanceConfig{Mode: "warn", MaxEntries: &maxEntries, PruneAfterSeconds: &zero, MaxDiskBytes: &zero}}
	})
	service.now = func() time.Time { return now }
	report, err := service.RunOnce(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Protected != 1 || len(report.Candidates) != 2 || len(report.Deleted) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if _, ok := store.Get("active"); !ok {
		t.Fatal("active session was removed in warn mode")
	}
}

func TestShortModelRunRetentionPrecedesGeneralAge(t *testing.T) {
	now := time.Unix(20_000, 0)
	entry := state.SessionEntry{SpawnedBy: "model-run", UpdatedAt: now.Add(-2 * time.Hour)}
	if !isShortModelRunSession("probe", entry) {
		t.Fatal("model run was not classified")
	}
}
