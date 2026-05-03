package agent

import (
	"testing"
	"time"
)

func TestHasSubagentRunEnded(t *testing.T) {
	tests := []struct {
		name     string
		entry    SubagentRunRecord
		expected bool
	}{
		{
			"running status",
			SubagentRunRecord{Status: "running"},
			false,
		},
		{
			"done status",
			SubagentRunRecord{Status: "done"},
			true,
		},
		{
			"error status",
			SubagentRunRecord{Status: "error"},
			true,
		},
		{
			"has EndedAt",
			SubagentRunRecord{EndedAt: time.Now().UnixMilli()},
			true,
		},
		{
			"empty",
			SubagentRunRecord{},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasSubagentRunEnded(tt.entry)
			if result != tt.expected {
				t.Errorf("HasSubagentRunEnded() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsStaleUnendedSubagentRun(t *testing.T) {
	now := time.Now()
	twoHoursAgo := now.Add(-2*time.Hour - time.Minute)
	oneHourAgo := now.Add(-1 * time.Hour)

	tests := []struct {
		name     string
		entry    SubagentRunRecord
		expected bool
	}{
		{
			"ended run is not stale",
			SubagentRunRecord{
				Status:    "done",
				StartedAt: twoHoursAgo.UnixMilli(),
			},
			false,
		},
		{
			"recent running is not stale",
			SubagentRunRecord{
				Status:    "running",
				StartedAt: oneHourAgo.UnixMilli(),
			},
			false,
		},
		{
			"old running is stale",
			SubagentRunRecord{
				Status:    "running",
				StartedAt: twoHoursAgo.UnixMilli(),
			},
			true,
		},
		{
			"invalid timestamp not stale",
			SubagentRunRecord{
				Status:    "running",
				StartedAt: 0,
			},
			false,
		},
		{
			"ancient timestamp not stale",
			SubagentRunRecord{
				Status:    "running",
				StartedAt: 1000000, // Before 2020
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsStaleUnendedSubagentRun(tt.entry, now)
			if result != tt.expected {
				t.Errorf("IsStaleUnendedSubagentRun() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsStaleUnendedSubagentRun_UpdatedAtTakesPrecedence(t *testing.T) {
	now := time.Now()
	entry := SubagentRunRecord{
		Status:    "running",
		StartedAt: now.Add(-4 * time.Hour).UnixMilli(),
		UpdatedAt: now.Add(-30 * time.Minute).UnixMilli(),
	}

	if IsStaleUnendedSubagentRun(entry, now) {
		t.Fatal("recent UpdatedAt should keep an old started run live")
	}

	entry.UpdatedAt = now.Add(-3 * time.Hour).UnixMilli()
	if !IsStaleUnendedSubagentRun(entry, now) {
		t.Fatal("old UpdatedAt should make an unended run stale")
	}
}

func TestIsStaleUnendedSubagentRun_WithExplicitTimeout(t *testing.T) {
	now := time.Now()

	// Run with 3-hour explicit timeout should not be stale after 2.5 hours
	twoAndHalfHoursAgo := now.Add(-2*time.Hour - 30*time.Minute)
	entry := SubagentRunRecord{
		Status:            "running",
		StartedAt:         twoAndHalfHoursAgo.UnixMilli(),
		RunTimeoutSeconds: 3 * 60 * 60, // 3 hours
	}

	if IsStaleUnendedSubagentRun(entry, now) {
		t.Error("Run with 3h timeout should not be stale after 2.5h")
	}

	// But should be stale after 3 hours + grace
	threeHoursAgo := now.Add(-3*time.Hour - 2*time.Minute)
	entry.StartedAt = threeHoursAgo.UnixMilli()

	if !IsStaleUnendedSubagentRun(entry, now) {
		t.Error("Run with 3h timeout should be stale after 3h + grace")
	}
}

func TestIsLiveUnendedSubagentRun(t *testing.T) {
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	threeHoursAgo := now.Add(-3 * time.Hour)

	tests := []struct {
		name     string
		entry    SubagentRunRecord
		expected bool
	}{
		{
			"recent running is live",
			SubagentRunRecord{
				Status:    "running",
				StartedAt: oneHourAgo.UnixMilli(),
			},
			true,
		},
		{
			"old running is not live",
			SubagentRunRecord{
				Status:    "running",
				StartedAt: threeHoursAgo.UnixMilli(),
			},
			false,
		},
		{
			"ended is not live",
			SubagentRunRecord{
				Status:    "done",
				StartedAt: oneHourAgo.UnixMilli(),
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsLiveUnendedSubagentRun(tt.entry, now)
			if result != tt.expected {
				t.Errorf("IsLiveUnendedSubagentRun() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsRecentlyEndedSubagentRun(t *testing.T) {
	now := time.Now()
	tenMinutesAgo := now.Add(-10 * time.Minute)
	oneHourAgo := now.Add(-1 * time.Hour)

	tests := []struct {
		name     string
		entry    SubagentRunRecord
		expected bool
	}{
		{
			"recently ended",
			SubagentRunRecord{
				Status:  "done",
				EndedAt: tenMinutesAgo.UnixMilli(),
			},
			true,
		},
		{
			"old ended",
			SubagentRunRecord{
				Status:  "done",
				EndedAt: oneHourAgo.UnixMilli(),
			},
			false,
		},
		{
			"not ended",
			SubagentRunRecord{
				Status:    "running",
				StartedAt: tenMinutesAgo.UnixMilli(),
			},
			false,
		},
		{
			"ended with UpdatedAt fallback",
			SubagentRunRecord{
				Status:    "done",
				UpdatedAt: tenMinutesAgo.UnixMilli(),
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRecentlyEndedSubagentRun(tt.entry, now, RecentEndedSubagentDuration)
			if result != tt.expected {
				t.Errorf("IsRecentlyEndedSubagentRun() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestShouldKeepSubagentRunChildLink(t *testing.T) {
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	threeHoursAgo := now.Add(-3 * time.Hour)
	tenMinutesAgo := now.Add(-10 * time.Minute)

	tests := []struct {
		name              string
		entry             SubagentRunRecord
		activeDescendants int
		expected          bool
	}{
		{
			"live run",
			SubagentRunRecord{Status: "running", StartedAt: oneHourAgo.UnixMilli()},
			0,
			true,
		},
		{
			"stale run",
			SubagentRunRecord{Status: "running", StartedAt: threeHoursAgo.UnixMilli()},
			0,
			false,
		},
		{
			"stale run with active descendants",
			SubagentRunRecord{Status: "running", StartedAt: threeHoursAgo.UnixMilli()},
			2,
			true,
		},
		{
			"recently ended",
			SubagentRunRecord{Status: "done", EndedAt: tenMinutesAgo.UnixMilli()},
			0,
			true,
		},
		{
			"old ended",
			SubagentRunRecord{Status: "done", EndedAt: threeHoursAgo.UnixMilli()},
			0,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldKeepSubagentRunChildLink(tt.entry, tt.activeDescendants, now)
			if result != tt.expected {
				t.Errorf("ShouldKeepSubagentRunChildLink() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSubagentLivenessChecker_FindStaleRuns(t *testing.T) {
	now := time.Now()
	checker := NewSubagentLivenessChecker()

	records := []SubagentRunRecord{
		{Status: "running", StartedAt: now.Add(-1 * time.Hour).UnixMilli()}, // live
		{Status: "running", StartedAt: now.Add(-3 * time.Hour).UnixMilli()}, // stale
		{Status: "done", StartedAt: now.Add(-3 * time.Hour).UnixMilli()},    // ended
		{Status: "running", StartedAt: now.Add(-5 * time.Hour).UnixMilli()}, // stale
	}

	staleIndices := checker.FindStaleRuns(records, now)

	if len(staleIndices) != 2 {
		t.Errorf("Expected 2 stale runs, got %d", len(staleIndices))
	}

	expected := map[int]bool{1: true, 3: true}
	for _, idx := range staleIndices {
		if !expected[idx] {
			t.Errorf("Unexpected stale index: %d", idx)
		}
	}
}

func TestSubagentLivenessChecker_HonorsCustomCutoff(t *testing.T) {
	now := time.Now()
	checker := NewSubagentLivenessCheckerWithCutoff(30 * time.Minute)

	records := []SubagentRunRecord{
		{Status: "running", StartedAt: now.Add(-20 * time.Minute).UnixMilli()},
		{Status: "running", StartedAt: now.Add(-45 * time.Minute).UnixMilli()},
		{Status: "running", StartedAt: now.Add(-45 * time.Minute).UnixMilli(), UpdatedAt: now.Add(-10 * time.Minute).UnixMilli()},
	}

	staleIndices := checker.FindStaleRuns(records, now)
	if len(staleIndices) != 1 || staleIndices[0] != 1 {
		t.Fatalf("stale indices = %v, want [1]", staleIndices)
	}

	live, stale := checker.PartitionRuns(records, now)
	if len(live) != 2 || len(stale) != 1 {
		t.Fatalf("partition live=%d stale=%d, want live=2 stale=1", len(live), len(stale))
	}

	stats := checker.GetLivenessStats(records, now)
	if stats.LiveRuns != 2 || stats.StaleRuns != 1 {
		t.Fatalf("stats live=%d stale=%d, want live=2 stale=1", stats.LiveRuns, stats.StaleRuns)
	}
}

func TestSubagentLivenessChecker_PartitionRuns(t *testing.T) {
	now := time.Now()
	checker := NewSubagentLivenessChecker()

	records := []SubagentRunRecord{
		{Status: "running", StartedAt: now.Add(-1 * time.Hour).UnixMilli()}, // live
		{Status: "running", StartedAt: now.Add(-3 * time.Hour).UnixMilli()}, // stale
		{Status: "done", EndedAt: now.Add(-10 * time.Minute).UnixMilli()},   // recently ended - live
		{Status: "done", EndedAt: now.Add(-2 * time.Hour).UnixMilli()},      // old ended - stale
	}

	live, stale := checker.PartitionRuns(records, now)

	if len(live) != 2 {
		t.Errorf("Expected 2 live runs, got %d", len(live))
	}
	if len(stale) != 2 {
		t.Errorf("Expected 2 stale runs, got %d", len(stale))
	}
}

func TestSubagentLivenessChecker_GetLivenessStats(t *testing.T) {
	now := time.Now()
	checker := NewSubagentLivenessChecker()

	records := []SubagentRunRecord{
		{Status: "running", StartedAt: now.Add(-1 * time.Hour).UnixMilli()},
		{Status: "running", StartedAt: now.Add(-3 * time.Hour).UnixMilli()},
		{Status: "done", EndedAt: now.Add(-10 * time.Minute).UnixMilli()},
		{Status: "error", EndedAt: now.Add(-2 * time.Hour).UnixMilli()},
	}

	stats := checker.GetLivenessStats(records, now)

	if stats.TotalRuns != 4 {
		t.Errorf("TotalRuns = %d, want 4", stats.TotalRuns)
	}
	if stats.LiveRuns != 1 {
		t.Errorf("LiveRuns = %d, want 1", stats.LiveRuns)
	}
	if stats.StaleRuns != 1 {
		t.Errorf("StaleRuns = %d, want 1", stats.StaleRuns)
	}
	if stats.EndedRuns != 2 {
		t.Errorf("EndedRuns = %d, want 2", stats.EndedRuns)
	}
	if stats.RecentlyEnded != 1 {
		t.Errorf("RecentlyEnded = %d, want 1", stats.RecentlyEnded)
	}
}

func TestResolveStaleCutoff(t *testing.T) {
	// Default cutoff when no explicit timeout
	entry := SubagentRunRecord{}
	cutoff := resolveStaleCutoff(entry)
	if cutoff != StaleUnendedSubagentRunDuration {
		t.Errorf("Default cutoff should be %v, got %v", StaleUnendedSubagentRunDuration, cutoff)
	}

	// With explicit timeout shorter than default
	entry.RunTimeoutSeconds = 60 // 1 minute
	cutoff = resolveStaleCutoff(entry)
	if cutoff != StaleUnendedSubagentRunDuration {
		t.Errorf("Short timeout should still use default %v, got %v", StaleUnendedSubagentRunDuration, cutoff)
	}

	// With explicit timeout longer than default
	entry.RunTimeoutSeconds = 4 * 60 * 60 // 4 hours
	cutoff = resolveStaleCutoff(entry)
	expected := 4*time.Hour + ExplicitTimeoutGrace
	if cutoff != expected {
		t.Errorf("Long timeout should use %v, got %v", expected, cutoff)
	}
}
