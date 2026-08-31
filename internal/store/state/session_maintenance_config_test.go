package state

import "testing"

func TestResolveSessionMaintenanceConfigDefaultsAndLegacyOverride(t *testing.T) {
	got := ResolveSessionMaintenanceConfig(SessionConfig{})
	if got.Mode != "enforce" || got.IntervalSeconds != 300 || got.MaxEntries != 500 || got.MaxDiskBytes != 10<<30 || got.HighWaterBytes != 8<<30 || got.ModelRunPruneAfterSeconds != 24*60*60 {
		t.Fatalf("defaults = %+v", got)
	}
	got = ResolveSessionMaintenanceConfig(SessionConfig{PruneAfterDays: 7})
	if got.PruneAfterSeconds != 7*24*60*60 {
		t.Fatalf("legacy prune override = %+v", got)
	}
}

func TestResolveSessionMaintenanceConfigClampsHighWater(t *testing.T) {
	maxDisk, highWater, interval, maxEntries := int64(100), int64(200), int64(0), 0
	got := ResolveSessionMaintenanceConfig(SessionConfig{Maintenance: &SessionMaintenanceConfig{Mode: "warn", MaxDiskBytes: &maxDisk, HighWaterBytes: &highWater, IntervalSeconds: &interval, MaxEntries: &maxEntries}})
	if got.Mode != "warn" || got.HighWaterBytes != 100 || got.IntervalSeconds != 0 || got.MaxEntries != 0 {
		t.Fatalf("resolved = %+v", got)
	}
}
