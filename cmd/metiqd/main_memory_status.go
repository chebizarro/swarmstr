package main

// main_memory_status.go — Memory subsystem status payload builders.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"strings"

	"metiq/internal/memory"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Memory backend / store status
// ---------------------------------------------------------------------------

func memoryBackendStatusPayload(status memory.BackendStatus) map[string]any {
	payload := map[string]any{
		"name":      status.Name,
		"available": status.Available,
	}
	if status.Degraded {
		payload["degraded"] = true
	}
	if status.LastError != "" {
		payload["last_error"] = status.LastError
	}
	if status.LastFailureUnix > 0 {
		payload["last_failure_unix"] = status.LastFailureUnix
	}
	if status.NextRetryUnix > 0 {
		payload["next_retry_unix"] = status.NextRetryUnix
	}
	if status.ConsecutiveFailures > 0 {
		payload["consecutive_failures"] = status.ConsecutiveFailures
	}
	return payload
}

func memoryStoreStatusPayload(status memory.StoreStatus) map[string]any {
	payload := map[string]any{
		"kind":    status.Kind,
		"primary": memoryBackendStatusPayload(status.Primary),
	}
	if status.FallbackActive {
		payload["fallback_active"] = true
	}
	if status.Fallback != nil {
		payload["fallback"] = memoryBackendStatusPayload(*status.Fallback)
	}
	return payload
}

func sessionMemoryStatusPayload(cfg state.ConfigDoc, sessionStore *state.SessionStore, runtime *sessionMemoryRuntime) map[string]any {
	memCfg := sessionMemoryConfigFromDoc(cfg)
	payload := map[string]any{
		"enabled":                    memCfg.Enabled,
		"runtime_available":          runtime != nil,
		"session_store_available":    sessionStore != nil,
		"init_chars":                 memCfg.InitChars,
		"update_chars":               memCfg.UpdateChars,
		"tool_calls_between_updates": memCfg.ToolCallsBetweenUpdates,
	}
	if runtime != nil {
		payload["in_flight_sessions"] = runtime.InFlightCount()
	}
	if sessionStore == nil {
		return payload
	}
	entries := sessionStore.List()
	tracked := 0
	initialized := 0
	artifactSessions := 0
	staleArtifactSessions := 0
	pending := 0
	latestUpdatedAt := int64(0)
	for _, entry := range entries {
		hasState := strings.TrimSpace(entry.SessionMemoryFile) != "" || entry.SessionMemoryInitialized || entry.SessionMemoryObservedChars > 0 || entry.SessionMemoryPendingChars > 0 || entry.SessionMemoryPendingToolCalls > 0 || entry.SessionMemoryUpdatedAt > 0
		if !hasState {
			continue
		}
		tracked++
		workspaceDir := strings.TrimSpace(entry.SpawnedWorkspace)
		if workspaceDir == "" {
			workspaceDir = workspaceDirForAgent(cfg, defaultAgentID(entry.AgentID))
		}
		if sessionMemoryArtifactCurrent(entry, workspaceDir, entry.SessionID) {
			artifactSessions++
		} else if strings.TrimSpace(entry.SessionMemoryFile) != "" || entry.SessionMemoryInitialized {
			staleArtifactSessions++
		}
		if entry.SessionMemoryInitialized {
			initialized++
		}
		if entry.SessionMemoryPendingChars > 0 || entry.SessionMemoryPendingToolCalls > 0 {
			pending++
		}
		if entry.SessionMemoryUpdatedAt > latestUpdatedAt {
			latestUpdatedAt = entry.SessionMemoryUpdatedAt
		}
	}
	payload["tracked_sessions"] = tracked
	payload["initialized_sessions"] = initialized
	payload["artifact_sessions"] = artifactSessions
	if staleArtifactSessions > 0 {
		payload["stale_artifact_sessions"] = staleArtifactSessions
	}
	if pending > 0 {
		payload["pending_sessions"] = pending
	}
	if latestUpdatedAt > 0 {
		payload["latest_update_unix"] = latestUpdatedAt
	}
	return payload
}

func fileMemoryStatusPayload(sessionStore *state.SessionStore) map[string]any {
	payload := map[string]any{
		"session_store_available": sessionStore != nil,
	}
	if sessionStore == nil {
		return payload
	}
	entries := sessionStore.List()
	sessionsWithSurfaceState := 0
	sessionsWithRecentRecall := 0
	surfacedPaths := 0
	recentRecallSamples := 0
	latestRecallRecordedAtMS := int64(0)
	for _, entry := range entries {
		if len(entry.FileMemorySurfaced) > 0 {
			sessionsWithSurfaceState++
			surfacedPaths += len(entry.FileMemorySurfaced)
		}
		if len(entry.RecentMemoryRecall) > 0 {
			sessionsWithRecentRecall++
			recentRecallSamples += len(entry.RecentMemoryRecall)
			for _, sample := range entry.RecentMemoryRecall {
				if sample.RecordedAtMS > latestRecallRecordedAtMS {
					latestRecallRecordedAtMS = sample.RecordedAtMS
				}
			}
		}
	}
	payload["sessions_with_surface_state"] = sessionsWithSurfaceState
	payload["surfaced_paths"] = surfacedPaths
	payload["sessions_with_recent_recall"] = sessionsWithRecentRecall
	payload["recent_recall_samples"] = recentRecallSamples
	if latestRecallRecordedAtMS > 0 {
		payload["latest_recall_recorded_at_ms"] = latestRecallRecordedAtMS
	}
	return payload
}

func memoryMaintenanceStatusPayload(sessionStore *state.SessionStore) map[string]any {
	payload := map[string]any{
		"session_store_available": sessionStore != nil,
	}
	if sessionStore == nil {
		return payload
	}
	entries := sessionStore.List()
	sessionsWithCompaction := 0
	totalCompactions := int64(0)
	sessionsWithMemoryFlush := 0
	latestMemoryFlushUnix := int64(0)
	for _, entry := range entries {
		if entry.CompactionCount > 0 {
			sessionsWithCompaction++
			totalCompactions += entry.CompactionCount
		}
		if entry.MemoryFlushAt > 0 {
			sessionsWithMemoryFlush++
			if entry.MemoryFlushAt > latestMemoryFlushUnix {
				latestMemoryFlushUnix = entry.MemoryFlushAt
			}
		}
	}
	payload["sessions_with_compaction"] = sessionsWithCompaction
	payload["total_compactions"] = totalCompactions
	payload["sessions_with_memory_flush"] = sessionsWithMemoryFlush
	if latestMemoryFlushUnix > 0 {
		payload["latest_memory_flush_unix"] = latestMemoryFlushUnix
	}
	return payload
}
