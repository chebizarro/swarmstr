package tasks

import (
	"time"

	"metiq/internal/store/state"
)

func computeTaskStats(tasks map[string]*LedgerEntry, runs map[string]*RunEntry, now time.Time) TaskStats {
	stats := TaskStats{
		ByStatus: make(map[string]int),
		BySource: make(map[string]int),
	}

	todayStart := now.Truncate(24 * time.Hour).Unix()

	for _, entry := range tasks {
		stats.TotalTasks++
		stats.ByStatus[string(entry.Task.Status)]++
		stats.BySource[string(entry.Source)]++
	}

	for _, entry := range runs {
		stats.TotalRuns++
		if entry.Run.Status == state.TaskRunStatusRunning {
			stats.ActiveRuns++
		}
		if entry.Run.EndedAt >= todayStart {
			if entry.Run.Status == state.TaskRunStatusCompleted {
				stats.CompletedToday++
			} else if entry.Run.Status == state.TaskRunStatusFailed {
				stats.FailedToday++
			}
		}
	}

	return stats
}
