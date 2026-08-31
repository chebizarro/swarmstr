package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func getStoredSession(ctx context.Context, docs *state.DocsRepository, transcripts *state.TranscriptRepository, sessions *state.SessionStore, req methods.SessionsGetRequest) (map[string]any, error) {
	if sessions == nil || transcripts == nil {
		return nil, fmt.Errorf("session repositories unavailable")
	}
	key, entry, found, err := sessions.ResolveSessionKey(req.Key)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{"key": req.Key, "messages": []state.TranscriptEntryDoc{}}, nil
	}
	messages, err := transcripts.ListSessionTail(ctx, entry.SessionID, req.Limit)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"key":       key,
		"sessionId": entry.SessionID,
		"entry":     entry,
		"messages":  messages,
	}
	if docs != nil {
		doc, docErr := docs.GetSession(ctx, entry.SessionID)
		if docErr == nil {
			result["session"] = doc
		} else if !errors.Is(docErr, state.ErrNotFound) {
			return nil, docErr
		}
	}
	return result, nil
}

type storedSessionCandidate struct {
	Key   string
	Entry state.SessionEntry
}

func resolveStoredSession(sessions *state.SessionStore, req methods.SessionsResolveRequest) (map[string]any, error) {
	if sessions == nil {
		return nil, fmt.Errorf("session store unavailable")
	}
	if req.ShortID != "" && !validSessionShortID(req.ShortID) {
		return nil, fmt.Errorf("shortId must be 8-32 hexadecimal characters")
	}
	if req.SlugHint != "" && req.ShortID == "" {
		return nil, fmt.Errorf("slugHint requires shortId")
	}
	entries := sessions.List()
	if req.Key != "" {
		if entry, ok := entries[req.Key]; ok && storedSessionMatchesFilters(req.Key, entry, req) {
			return resolvedSessionPayload(req.Key, entry), nil
		}
	}
	candidates := make([]storedSessionCandidate, 0)
	for key, entry := range entries {
		if !storedSessionMatchesFilters(key, entry, req) {
			continue
		}
		matched := false
		switch {
		case req.SessionID != "":
			matched = entry.SessionID == req.SessionID
		case req.Label != "":
			matched = strings.EqualFold(strings.TrimSpace(entry.Label), req.Label)
		case req.ShortID != "":
			needle := strings.ToLower(req.ShortID)
			matched = strings.Contains(strings.ToLower(key), needle) || strings.Contains(strings.ToLower(entry.SessionID), needle)
			if matched && req.SlugHint != "" {
				slug := strings.ToLower(req.SlugHint)
				matched = strings.Contains(strings.ToLower(key), slug) || strings.Contains(strings.ToLower(entry.Label), slug)
			}
		case req.Key != "":
			matched = entry.SessionID == req.Key || strings.EqualFold(strings.TrimSpace(entry.Label), req.Key)
		default:
			return nil, fmt.Errorf("one of key, session_id, label, or shortId is required")
		}
		if matched {
			candidates = append(candidates, storedSessionCandidate{Key: key, Entry: entry})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Key < candidates[j].Key })
	if len(candidates) == 1 {
		return resolvedSessionPayload(candidates[0].Key, candidates[0].Entry), nil
	}
	if len(candidates) > 1 {
		rows := make([]map[string]any, 0, len(candidates))
		for _, candidate := range candidates {
			rows = append(rows, sessionResolutionCandidate(candidate.Key, candidate.Entry))
		}
		return map[string]any{"ok": false, "candidates": rows}, nil
	}
	if req.AllowMissing {
		return map[string]any{"ok": false}, nil
	}
	selector := firstNonEmpty(req.Key, req.SessionID, req.Label, req.ShortID)
	return nil, fmt.Errorf("No session found: %s", selector)
}

func storedSessionMatchesFilters(key string, entry state.SessionEntry, req methods.SessionsResolveRequest) bool {
	if req.AgentID != "" && !strings.EqualFold(strings.TrimSpace(entry.AgentID), req.AgentID) {
		return false
	}
	if req.SpawnedBy != "" && entry.SpawnedBy != req.SpawnedBy {
		return false
	}
	if !req.IncludeGlobal && req.Key == "" && key == "global" {
		return false
	}
	return true
}

func resolvedSessionPayload(key string, entry state.SessionEntry) map[string]any {
	return map[string]any{
		"ok":        true,
		"key":       key,
		"sessionId": entry.SessionID,
		"agentId":   entry.AgentID,
		"entry":     entry,
	}
}

func sessionResolutionCandidate(key string, entry state.SessionEntry) map[string]any {
	return map[string]any{
		"key":         key,
		"sessionId":   entry.SessionID,
		"agentId":     entry.AgentID,
		"displayName": entry.Label,
		"spawnedBy":   entry.SpawnedBy,
	}
}

func validSessionShortID(value string) bool {
	if len(value) < 8 || len(value) > 32 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "session reference"
}

func recoverStoredSession(ctx context.Context, docs *state.DocsRepository, transcripts *state.TranscriptRepository, sessions *state.SessionStore, key, agentID string) (map[string]any, error) {
	if docs == nil || transcripts == nil || sessions == nil {
		return nil, fmt.Errorf("session repositories unavailable")
	}
	source, err := resolveHistorySession(sessions, key, agentID)
	if err != nil {
		return nil, err
	}
	entries, sourceRef, err := selectRecoveryTranscript(ctx, transcripts, key, source)
	if err != nil {
		return nil, err
	}
	if _, err := sessions.SetArchived(key, true); err != nil {
		return nil, fmt.Errorf("archive recovery source: %w", err)
	}
	successorKey, successor, err := createHistorySession(ctx, docs, transcripts, sessions, key, source, entries, "recovery", sourceRef)
	if err != nil {
		_, _ = sessions.SetArchived(key, false)
		return nil, err
	}
	return map[string]any{
		"ok":            true,
		"key":           successorKey,
		"sessionId":     successor.SessionID,
		"sourceKey":     key,
		"recoveredFrom": sourceRef,
		"continuation":  map[string]any{"status": "rejected", "error": map[string]any{"code": "UNAVAILABLE", "message": "Continuation was not started."}},
	}, nil
}

func selectRecoveryTranscript(ctx context.Context, transcripts *state.TranscriptRepository, key string, source state.SessionEntry) ([]state.TranscriptEntryDoc, string, error) {
	graph, err := transcripts.EnsureGraph(ctx, key, source.SessionID)
	if err != nil {
		return nil, "", err
	}
	leafCandidates := append([]string{graph.ActiveLeafID}, graph.BranchHeads...)
	seen := map[string]struct{}{}
	for _, leaf := range leafCandidates {
		leaf = strings.TrimSpace(leaf)
		if _, ok := seen[leaf]; ok {
			continue
		}
		seen[leaf] = struct{}{}
		if leaf == "" {
			continue
		}
		path, pathErr := transcripts.ListSessionPath(ctx, source.SessionID, leaf)
		if pathErr == nil {
			return path, "branch:" + leaf, nil
		}
	}
	if strings.TrimSpace(graph.ActiveLeafID) == "" && len(graph.BranchHeads) == 0 {
		return nil, "empty-transcript", nil
	}
	checkpoints := append([]state.CompactionCheckpointRef(nil), source.CompactionCheckpoints...)
	sort.SliceStable(checkpoints, func(i, j int) bool { return checkpoints[i].CreatedAt > checkpoints[j].CreatedAt })
	for _, checkpoint := range checkpoints {
		if strings.TrimSpace(checkpoint.SnapshotID) == "" {
			continue
		}
		snapshot, snapshotErr := transcripts.ReadSnapshot(ctx, checkpoint.SnapshotID, source.SessionID)
		if snapshotErr == nil && validRecoveryPath(snapshot) {
			return snapshot, "checkpoint:" + checkpoint.CheckpointID, nil
		}
	}
	return nil, "", fmt.Errorf("session transcript has no valid recoverable branch or checkpoint")
}

func validRecoveryPath(entries []state.TranscriptEntryDoc) bool {
	parent := ""
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.EntryID) == "" || entry.ParentEntryID != parent {
			return false
		}
		if _, ok := seen[entry.EntryID]; ok {
			return false
		}
		seen[entry.EntryID] = struct{}{}
		parent = entry.EntryID
	}
	return true
}
