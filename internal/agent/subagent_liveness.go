package agent

import (
	"time"
)

// ─── Subagent Run Liveness Detection ──────────────────────────────────────────
//
// Detects stale unended subagent runs (>2 hours old) vs live ones, enabling
// cleanup of abandoned agent sessions and better resource management.

const (
	// StaleUnendedSubagentRunDuration is the threshold after which an unended
	// subagent run is considered stale.
	StaleUnendedSubagentRunDuration = 2 * time.Hour

	// RecentEndedSubagentDuration is the window during which a recently ended
	// subagent run is still considered relevant.
	RecentEndedSubagentDuration = 30 * time.Minute

	// ExplicitTimeoutGrace is additional grace period when an explicit timeout is set.
	ExplicitTimeoutGrace = 1 * time.Minute

	// MinRealisticRunTimestamp is the earliest plausible timestamp for a run start.
	// Used to filter out invalid timestamps.
	MinRealisticRunTimestamp = 1577836800000 // 2020-01-01 UTC in milliseconds
)

// SubagentRunRecord represents the minimal interface for liveness checks.
// This matches the structure used by SubagentRegistry.
type SubagentRunRecord struct {
	// StartedAt is when the run started (Unix milliseconds)
	StartedAt int64

	// EndedAt is when the run ended (Unix milliseconds), 0 if still running
	EndedAt int64

	// UpdatedAt is the last activity timestamp (Unix milliseconds)
	UpdatedAt int64

	// RunTimeoutSeconds is an optional explicit timeout for the run
	RunTimeoutSeconds int64

	// Status is the current status ("running", "done", "error")
	Status string
}

// HasSubagentRunEnded checks if a subagent run has ended
func HasSubagentRunEnded(entry SubagentRunRecord) bool {
	return entry.EndedAt > 0 ||
		entry.Status == "done" ||
		entry.Status == "error"
}

// resolveStaleCutoff calculates the stale threshold considering explicit timeouts
func resolveStaleCutoff(entry SubagentRunRecord) time.Duration {
	if entry.RunTimeoutSeconds > 0 {
		explicitTimeout := time.Duration(entry.RunTimeoutSeconds) * time.Second
		explicitWithGrace := explicitTimeout + ExplicitTimeoutGrace

		if explicitWithGrace > StaleUnendedSubagentRunDuration {
			return explicitWithGrace
		}
	}
	return StaleUnendedSubagentRunDuration
}

// getEffectiveStartedAt returns the most relevant start timestamp
func getEffectiveStartedAt(entry SubagentRunRecord) int64 {
	// Prefer UpdatedAt as it shows recent activity
	if entry.UpdatedAt > 0 && entry.UpdatedAt >= entry.StartedAt {
		// But only if it's more recent than StartedAt
		// For stale detection, we want the earliest timestamp
		return entry.StartedAt
	}
	return entry.StartedAt
}

// IsStaleUnendedSubagentRun checks if an unended subagent run is stale
func IsStaleUnendedSubagentRun(entry SubagentRunRecord, now time.Time) bool {
	if HasSubagentRunEnded(entry) {
		return false
	}

	startedAt := getEffectiveStartedAt(entry)
	if startedAt <= 0 || startedAt < MinRealisticRunTimestamp {
		return false
	}

	startTime := time.UnixMilli(startedAt)
	cutoff := resolveStaleCutoff(entry)

	return now.Sub(startTime) > cutoff
}

// IsStaleUnendedSubagentRunNow is a convenience wrapper using current time
func IsStaleUnendedSubagentRunNow(entry SubagentRunRecord) bool {
	return IsStaleUnendedSubagentRun(entry, time.Now())
}

// IsLiveUnendedSubagentRun checks if a subagent run is still active
func IsLiveUnendedSubagentRun(entry SubagentRunRecord, now time.Time) bool {
	return !HasSubagentRunEnded(entry) && !IsStaleUnendedSubagentRun(entry, now)
}

// IsLiveUnendedSubagentRunNow is a convenience wrapper using current time
func IsLiveUnendedSubagentRunNow(entry SubagentRunRecord) bool {
	return IsLiveUnendedSubagentRun(entry, time.Now())
}

// IsRecentlyEndedSubagentRun checks if a run ended recently
func IsRecentlyEndedSubagentRun(entry SubagentRunRecord, now time.Time, recentWindow time.Duration) bool {
	if !HasSubagentRunEnded(entry) {
		return false
	}

	if entry.EndedAt <= 0 {
		// Ended but no timestamp - use UpdatedAt
		if entry.UpdatedAt <= 0 {
			return false
		}
		endTime := time.UnixMilli(entry.UpdatedAt)
		return now.Sub(endTime) <= recentWindow
	}

	endTime := time.UnixMilli(entry.EndedAt)
	return now.Sub(endTime) <= recentWindow
}

// IsRecentlyEndedSubagentRunNow uses default recent window
func IsRecentlyEndedSubagentRunNow(entry SubagentRunRecord) bool {
	return IsRecentlyEndedSubagentRun(entry, time.Now(), RecentEndedSubagentDuration)
}

// ShouldKeepSubagentRunChildLink determines if a run's child link should be kept
// for session management purposes
func ShouldKeepSubagentRunChildLink(entry SubagentRunRecord, activeDescendants int, now time.Time) bool {
	// Keep if still live
	if IsLiveUnendedSubagentRun(entry, now) {
		return true
	}

	// Keep if has active descendants
	if activeDescendants > 0 {
		return true
	}

	// Keep if recently ended
	return IsRecentlyEndedSubagentRun(entry, now, RecentEndedSubagentDuration)
}

// ShouldKeepSubagentRunChildLinkNow is a convenience wrapper using current time
func ShouldKeepSubagentRunChildLinkNow(entry SubagentRunRecord, activeDescendants int) bool {
	return ShouldKeepSubagentRunChildLink(entry, activeDescendants, time.Now())
}

// ─── Liveness Cleanup ─────────────────────────────────────────────────────────

// SubagentLivenessChecker provides utilities for checking and cleaning up
// stale subagent runs.
type SubagentLivenessChecker struct {
	staleCutoff time.Duration
}

// NewSubagentLivenessChecker creates a new checker with default settings
func NewSubagentLivenessChecker() *SubagentLivenessChecker {
	return &SubagentLivenessChecker{
		staleCutoff: StaleUnendedSubagentRunDuration,
	}
}

// NewSubagentLivenessCheckerWithCutoff creates a checker with custom cutoff
func NewSubagentLivenessCheckerWithCutoff(cutoff time.Duration) *SubagentLivenessChecker {
	return &SubagentLivenessChecker{
		staleCutoff: cutoff,
	}
}

// FindStaleRuns returns records that are stale and should be cleaned up
func (c *SubagentLivenessChecker) FindStaleRuns(records []SubagentRunRecord, now time.Time) []int {
	var staleIndices []int
	for i, record := range records {
		if IsStaleUnendedSubagentRun(record, now) {
			staleIndices = append(staleIndices, i)
		}
	}
	return staleIndices
}

// PartitionRuns separates records into live and stale categories
func (c *SubagentLivenessChecker) PartitionRuns(records []SubagentRunRecord, now time.Time) (live, stale []SubagentRunRecord) {
	for _, record := range records {
		if HasSubagentRunEnded(record) {
			// Ended runs go to stale unless recently ended
			if IsRecentlyEndedSubagentRun(record, now, RecentEndedSubagentDuration) {
				live = append(live, record)
			} else {
				stale = append(stale, record)
			}
		} else if IsStaleUnendedSubagentRun(record, now) {
			stale = append(stale, record)
		} else {
			live = append(live, record)
		}
	}
	return
}

// LivenessStats provides statistics about subagent run liveness
type LivenessStats struct {
	TotalRuns       int
	LiveRuns        int
	EndedRuns       int
	StaleRuns       int
	RecentlyEnded   int
	OldestLiveStart time.Time
	OldestStaleAge  time.Duration
}

// GetLivenessStats calculates statistics for a set of records
func (c *SubagentLivenessChecker) GetLivenessStats(records []SubagentRunRecord, now time.Time) LivenessStats {
	stats := LivenessStats{TotalRuns: len(records)}

	for _, record := range records {
		if HasSubagentRunEnded(record) {
			stats.EndedRuns++
			if IsRecentlyEndedSubagentRun(record, now, RecentEndedSubagentDuration) {
				stats.RecentlyEnded++
			}
		} else if IsStaleUnendedSubagentRun(record, now) {
			stats.StaleRuns++
			startTime := time.UnixMilli(getEffectiveStartedAt(record))
			age := now.Sub(startTime)
			if age > stats.OldestStaleAge {
				stats.OldestStaleAge = age
			}
		} else {
			stats.LiveRuns++
			startTime := time.UnixMilli(getEffectiveStartedAt(record))
			if stats.OldestLiveStart.IsZero() || startTime.Before(stats.OldestLiveStart) {
				stats.OldestLiveStart = startTime
			}
		}
	}

	return stats
}
