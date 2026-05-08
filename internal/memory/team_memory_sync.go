package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// TeamMemoryMergeOutcome describes what happened to a single entry during sync.
type TeamMemoryMergeOutcome string

const (
	MergeOutcomeApplied   TeamMemoryMergeOutcome = "applied"    // remote written to local
	MergeOutcomeKeptLocal TeamMemoryMergeOutcome = "kept_local" // local was newer, kept
	MergeOutcomeConflict  TeamMemoryMergeOutcome = "conflict"   // both sides changed
	MergeOutcomeUnchanged TeamMemoryMergeOutcome = "unchanged"  // identical, no action
	MergeOutcomeDeleted   TeamMemoryMergeOutcome = "deleted"    // remote removed, local removed
)

// TeamMemoryMergeEntry records the per-key result of a sync operation.
type TeamMemoryMergeEntry struct {
	Key            string                 `json:"key"`
	Outcome        TeamMemoryMergeOutcome `json:"outcome"`
	LocalChecksum  string                 `json:"local_checksum,omitempty"`
	RemoteChecksum string                 `json:"remote_checksum,omitempty"`
	BaseChecksum   string                 `json:"base_checksum,omitempty"`
}

// TeamMemoryApplyResult is the outcome of applying a remote snapshot locally.
type TeamMemoryApplyResult struct {
	OK        bool                   `json:"ok"`
	Applied   int                    `json:"applied"`
	KeptLocal int                    `json:"kept_local"`
	Conflicts []TeamMemoryMergeEntry `json:"conflicts,omitempty"`
	Unchanged int                    `json:"unchanged"`
	Deleted   int                    `json:"deleted"`
	Entries   []TeamMemoryMergeEntry `json:"entries"`
	Error     string                 `json:"error,omitempty"`
}

// ApplyTeamMemorySnapshot performs a three-way merge of an incoming remote
// snapshot against the local team-memory directory, using syncState as the
// common ancestor. This is the "pull" operation.
//
// Merge rules per key:
//  1. Local unchanged from base, remote changed → apply remote (applied)
//  2. Remote unchanged from base, local changed → keep local (kept_local)
//  3. Both unchanged → skip (unchanged)
//  4. Both changed to same value → skip (unchanged)
//  5. Both changed to different values → conflict (written to result, not applied)
//  6. Key exists in remote but not locally and not in base → apply (new entry)
//  7. Key exists locally but not in remote and exists in base → delete (remote removed)
//  8. Key exists locally but not in remote and not in base → keep local (local-only)
func ApplyTeamMemorySnapshot(workspaceDir string, incoming TeamMemorySnapshot, syncState TeamMemorySyncState) TeamMemoryApplyResult {
	surface := ResolveTeamMemorySurface(workspaceDir)
	if surface.RootDir == "" {
		return TeamMemoryApplyResult{Error: "team memory workspace is not available"}
	}

	// Build local snapshot for comparison.
	localExport := BuildTeamMemorySyncPayload(workspaceDir)
	if !localExport.OK {
		return TeamMemoryApplyResult{Error: fmt.Sprintf("failed to read local state: %s", localExport.Error)}
	}

	// Read the per-entry base checksums from the sync state.
	// These record what each entry looked like at the last sync point.
	// When no base exists (first sync), any key present on both sides with
	// differing content will surface as a conflict. This is intentional:
	// we prefer explicit conflict resolution over silent data loss on first
	// sync. Callers can resolve by accepting one side before re-syncing.
	baseChecksums := map[string]string{}
	if syncState.Version > 0 && len(syncState.EntryChecksums) > 0 {
		for k, v := range syncState.EntryChecksums {
			baseChecksums[k] = v
		}
	}

	// Collect all keys from local, remote, and base.
	allKeys := map[string]struct{}{}
	for k := range incoming.Content.Entries {
		allKeys[k] = struct{}{}
	}
	for k := range localExport.Snapshot.Content.Entries {
		allKeys[k] = struct{}{}
	}
	for k := range baseChecksums {
		allKeys[k] = struct{}{}
	}

	sortedKeys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var result TeamMemoryApplyResult
	result.Entries = make([]TeamMemoryMergeEntry, 0, len(sortedKeys))
	workspaceRoot := resolvedWorkspaceRoot(workspaceDir)

	for _, key := range sortedKeys {
		_, localExists := localExport.Snapshot.Content.Entries[key]
		localChecksum := localExport.Snapshot.Content.EntryChecksums[key]
		remoteContent, remoteExists := incoming.Content.Entries[key]
		remoteChecksum := incoming.Content.EntryChecksums[key]
		baseChecksum := baseChecksums[key]

		entry := TeamMemoryMergeEntry{
			Key:            key,
			LocalChecksum:  localChecksum,
			RemoteChecksum: remoteChecksum,
			BaseChecksum:   baseChecksum,
		}

		localChanged := localChecksum != baseChecksum
		remoteChanged := remoteChecksum != baseChecksum

		switch {
		case !localExists && !remoteExists:
			// Both gone — nothing to do.
			entry.Outcome = MergeOutcomeUnchanged
			result.Unchanged++

		case !localExists && remoteExists:
			if baseChecksum != "" && !remoteChanged {
				// Was in base, local deleted it, remote unchanged → keep deleted.
				entry.Outcome = MergeOutcomeKeptLocal
				result.KeptLocal++
			} else {
				// New from remote or remote changed → apply.
				if err := writeTeamMemoryKey(surface.RootDir, workspaceRoot, key, remoteContent); err != nil {
					return TeamMemoryApplyResult{Error: fmt.Sprintf("write %q: %v", key, err)}
				}
				entry.Outcome = MergeOutcomeApplied
				result.Applied++
			}

		case localExists && !remoteExists:
			if baseChecksum != "" && !localChanged {
				// Was in base, remote deleted it, local unchanged → delete.
				if err := deleteTeamMemoryKey(surface.RootDir, workspaceRoot, key); err != nil {
					return TeamMemoryApplyResult{Error: fmt.Sprintf("delete %q: %v", key, err)}
				}
				entry.Outcome = MergeOutcomeDeleted
				result.Deleted++
			} else {
				// Local-only or local changed → keep local.
				entry.Outcome = MergeOutcomeKeptLocal
				result.KeptLocal++
			}

		default: // both exist
			if localChecksum == remoteChecksum {
				entry.Outcome = MergeOutcomeUnchanged
				result.Unchanged++
			} else if !localChanged && remoteChanged {
				// Local at base, remote changed → apply.
				if err := writeTeamMemoryKey(surface.RootDir, workspaceRoot, key, remoteContent); err != nil {
					return TeamMemoryApplyResult{Error: fmt.Sprintf("write %q: %v", key, err)}
				}
				entry.Outcome = MergeOutcomeApplied
				result.Applied++
			} else if localChanged && !remoteChanged {
				entry.Outcome = MergeOutcomeKeptLocal
				result.KeptLocal++
			} else {
				// Both changed differently → conflict.
				entry.Outcome = MergeOutcomeConflict
				result.Conflicts = append(result.Conflicts, entry)
			}
		}

		result.Entries = append(result.Entries, entry)
	}

	result.OK = len(result.Conflicts) == 0
	return result
}

// UpdateSyncStateAfterPull updates the sync state after a successful pull.
// Call this only when ApplyResult.OK is true (no conflicts).
// It snapshots the current local state as the new base for future merges.
func UpdateSyncStateAfterPull(workspaceDir string, remoteChecksum string) error {
	start := time.Now()
	syncState, err := ReadTeamMemorySyncState(workspaceDir)
	if err != nil {
		recordMemoryTelemetry("sync", start, map[string]any{"ok": false, "direction": "pull", "error": err.Error()})
		return fmt.Errorf("read sync state: %w", err)
	}
	// Snapshot current local entry checksums as the new merge base.
	localExport := BuildTeamMemorySyncPayload(workspaceDir)
	if localExport.OK {
		syncState.EntryChecksums = localExport.Snapshot.Content.EntryChecksums
		syncState.Checksum = localExport.Snapshot.Checksum
	} else {
		syncState.Checksum = remoteChecksum
	}
	syncState.LastPulledAt = time.Now().UTC().Format(time.RFC3339Nano)
	syncState.LastError = ""
	if syncState.Version == 0 {
		syncState.Version = 1
	}
	err = WriteTeamMemorySyncState(workspaceDir, syncState)
	recordMemoryTelemetry("sync", start, map[string]any{"ok": err == nil, "direction": "pull"})
	return err
}

// UpdateSyncStateAfterPush updates the sync state after a successful push.
// It snapshots the current local state as the new base.
func UpdateSyncStateAfterPush(workspaceDir string, pushedChecksum string) error {
	start := time.Now()
	syncState, err := ReadTeamMemorySyncState(workspaceDir)
	if err != nil {
		recordMemoryTelemetry("sync", start, map[string]any{"ok": false, "direction": "push", "error": err.Error()})
		return fmt.Errorf("read sync state: %w", err)
	}
	localExport := BuildTeamMemorySyncPayload(workspaceDir)
	if localExport.OK {
		syncState.EntryChecksums = localExport.Snapshot.Content.EntryChecksums
		syncState.Checksum = localExport.Snapshot.Checksum
	} else {
		syncState.Checksum = pushedChecksum
	}
	syncState.LastPushedAt = time.Now().UTC().Format(time.RFC3339Nano)
	syncState.LastError = ""
	if syncState.Version == 0 {
		syncState.Version = 1
	}
	err = WriteTeamMemorySyncState(workspaceDir, syncState)
	recordMemoryTelemetry("sync", start, map[string]any{"ok": err == nil, "direction": "push"})
	return err
}

// RecordSyncError writes a sync error to the sync state without changing checksums.
func RecordSyncError(workspaceDir string, syncErr string) error {
	syncState, err := ReadTeamMemorySyncState(workspaceDir)
	if err != nil {
		return fmt.Errorf("read sync state: %w", err)
	}
	syncState.LastError = syncErr
	return WriteTeamMemorySyncState(workspaceDir, syncState)
}

// writeTeamMemoryKey writes a single entry, validating the key and path safety.
func writeTeamMemoryKey(rootDir, workspaceRoot, key, content string) error {
	normalizedKey, err := ValidateTeamMemoryKey(key)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(rootDir, filepath.FromSlash(normalizedKey))
	if !isContainedWithin(workspaceRoot, targetPath) {
		return PathTraversalError{Message: fmt.Sprintf("team memory path %q resolves outside the workspace root", key)}
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return err
	}
	return writeAtomicFile(targetPath, []byte(content), 0o644)
}

// deleteTeamMemoryKey removes a single entry, validating path safety.
func deleteTeamMemoryKey(rootDir, workspaceRoot, key string) error {
	normalizedKey, err := ValidateTeamMemoryKey(key)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(rootDir, filepath.FromSlash(normalizedKey))
	if !isContainedWithin(workspaceRoot, targetPath) {
		return PathTraversalError{Message: fmt.Sprintf("team memory path %q resolves outside the workspace root", key)}
	}
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Clean up empty parent directories up to rootDir. Clean both paths before
	// comparing so equivalent spellings (trailing slash, dot segments) cannot
	// escape the intended stop boundary.
	cleanRoot := filepath.Clean(rootDir)
	dir := filepath.Clean(filepath.Dir(targetPath))
	for dir != cleanRoot && isContainedWithin(cleanRoot, dir) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
			break
		}
		parent := filepath.Clean(filepath.Dir(dir))
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}
