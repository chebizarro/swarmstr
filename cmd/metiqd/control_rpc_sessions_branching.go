package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

type sessionBranchView struct {
	LeafEntryID  string `json:"leafEntryId"`
	Headline     string `json:"headline"`
	MessageCount int    `json:"messageCount"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	Active       bool   `json:"active"`
	updatedUnix  int64
}

func resolveHistorySession(store *state.SessionStore, key, agentID string) (state.SessionEntry, error) {
	if store == nil {
		return state.SessionEntry{}, fmt.Errorf("session store unavailable")
	}
	entry, ok := store.Get(strings.TrimSpace(key))
	if !ok {
		return state.SessionEntry{}, fmt.Errorf("session not found: %s", key)
	}
	if agentID = strings.TrimSpace(agentID); agentID != "" && strings.TrimSpace(entry.AgentID) != "" && strings.TrimSpace(entry.AgentID) != agentID {
		return state.SessionEntry{}, fmt.Errorf("session %q does not belong to agent %q", key, agentID)
	}
	return entry, nil
}

func findCompactionCheckpoint(entry state.SessionEntry, checkpointID string) (state.CompactionCheckpointRef, error) {
	for _, checkpoint := range entry.CompactionCheckpoints {
		if checkpoint.CheckpointID == checkpointID {
			if strings.TrimSpace(checkpoint.SnapshotID) == "" {
				return state.CompactionCheckpointRef{}, fmt.Errorf("checkpoint does not contain a restorable snapshot")
			}
			return checkpoint, nil
		}
	}
	return state.CompactionCheckpointRef{}, fmt.Errorf("checkpoint %q not found", checkpointID)
}

func listSessionBranches(ctx context.Context, transcripts *state.TranscriptRepository, sessions *state.SessionStore, key, agentID string) (map[string]any, error) {
	entry, err := resolveHistorySession(sessions, key, agentID)
	if err != nil {
		return nil, err
	}
	graph, err := transcripts.EnsureGraph(ctx, key, entry.SessionID)
	if err != nil {
		return nil, err
	}
	branches := make([]sessionBranchView, 0, len(graph.BranchHeads))
	for _, head := range graph.BranchHeads {
		path, err := transcripts.ListSessionPath(ctx, entry.SessionID, head)
		if err != nil {
			return nil, err
		}
		view := sessionBranchView{LeafEntryID: head, Headline: "Empty branch", Active: head == graph.ActiveLeafID}
		for _, item := range path {
			if item.Role == "user" || item.Role == "assistant" {
				view.MessageCount++
				if strings.TrimSpace(item.Text) != "" {
					view.Headline = truncateHistoryRunes(strings.TrimSpace(item.Text), 120)
				}
			} else if view.Headline == "Empty branch" && item.Role == "system" && strings.TrimSpace(item.Text) != "" {
				view.Headline = truncateHistoryRunes(strings.TrimSpace(item.Text), 120)
			}
		}
		if len(path) > 0 {
			view.updatedUnix = path[len(path)-1].Unix
			if view.updatedUnix > 0 {
				view.UpdatedAt = time.Unix(view.updatedUnix, 0).UTC().Format(time.RFC3339)
			}
		}
		branches = append(branches, view)
	}
	sort.SliceStable(branches, func(i, j int) bool {
		if branches[i].Active != branches[j].Active {
			return branches[i].Active
		}
		if branches[i].updatedUnix != branches[j].updatedUnix {
			return branches[i].updatedUnix > branches[j].updatedUnix
		}
		return branches[i].LeafEntryID < branches[j].LeafEntryID
	})
	return map[string]any{"branches": branches}, nil
}

func switchSessionBranch(ctx context.Context, transcripts *state.TranscriptRepository, sessions *state.SessionStore, key, agentID, leafID string) (map[string]any, error) {
	entry, err := resolveHistorySession(sessions, key, agentID)
	if err != nil {
		return nil, err
	}
	graph, err := transcripts.EnsureGraph(ctx, key, entry.SessionID)
	if err != nil {
		return nil, err
	}
	if graph.ActiveLeafID == leafID {
		return nil, fmt.Errorf("branch is already active: %s", leafID)
	}
	if !stringSliceContains(graph.BranchHeads, leafID) {
		if _, err := transcripts.GetEntry(ctx, entry.SessionID, leafID); err == nil {
			return nil, fmt.Errorf("entry is not a branch tip: %s", leafID)
		}
		return nil, fmt.Errorf("branch entry not found: %s", leafID)
	}
	if _, err := transcripts.ListSessionPath(ctx, entry.SessionID, leafID); err != nil {
		return nil, err
	}
	if _, err := sessions.CommitTranscriptGraph(key, graph.Revision, state.TranscriptGraphMutation{ActiveLeafID: leafID, BranchHeads: graph.BranchHeads}); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func rewindSessionAtEntry(ctx context.Context, transcripts *state.TranscriptRepository, sessions *state.SessionStore, key, agentID, entryID string) (map[string]any, error) {
	entry, err := resolveHistorySession(sessions, key, agentID)
	if err != nil {
		return nil, err
	}
	graph, err := transcripts.EnsureGraph(ctx, key, entry.SessionID)
	if err != nil {
		return nil, err
	}
	path, err := transcripts.ListSessionPath(ctx, entry.SessionID, graph.ActiveLeafID)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range path {
		if path[i].EntryID == entryID {
			index = i
			break
		}
	}
	if index < 0 {
		if node, getErr := transcripts.GetEntry(ctx, entry.SessionID, entryID); getErr == nil {
			if node.Role != "user" {
				return nil, fmt.Errorf("entry is not a user message: %s", entryID)
			}
			return nil, fmt.Errorf("message entry is not on the active path: %s", entryID)
		}
		return nil, fmt.Errorf("message entry not found: %s", entryID)
	}
	target := path[index]
	if target.Role != "user" {
		return nil, fmt.Errorf("entry is not a user message: %s", entryID)
	}
	newLeaf := target.ParentEntryID
	heads := state.ReplaceTranscriptHead(graph.BranchHeads, graph.ActiveLeafID, newLeaf, true)
	if _, err := sessions.CommitTranscriptGraph(key, graph.Revision, state.TranscriptGraphMutation{ActiveLeafID: newLeaf, BranchHeads: heads}); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if target.Text != "" {
		out["editorText"] = target.Text
	}
	return out, nil
}

func forkSessionAtEntry(ctx context.Context, docs *state.DocsRepository, transcripts *state.TranscriptRepository, sessions *state.SessionStore, key, agentID, entryID string) (map[string]any, error) {
	entry, err := resolveHistorySession(sessions, key, agentID)
	if err != nil {
		return nil, err
	}
	graph, err := transcripts.EnsureGraph(ctx, key, entry.SessionID)
	if err != nil {
		return nil, err
	}
	path, err := transcripts.ListSessionPath(ctx, entry.SessionID, graph.ActiveLeafID)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range path {
		if path[i].EntryID == entryID {
			index = i
			break
		}
	}
	if index < 0 {
		if node, getErr := transcripts.GetEntry(ctx, entry.SessionID, entryID); getErr == nil {
			if node.Role != "user" {
				return nil, fmt.Errorf("entry is not a user message: %s", entryID)
			}
			return nil, fmt.Errorf("message entry is not on the active path: %s", entryID)
		}
		return nil, fmt.Errorf("message entry not found: %s", entryID)
	}
	if path[index].Role != "user" {
		return nil, fmt.Errorf("entry is not a user message: %s", entryID)
	}
	forkedKey, _, err := createHistorySession(ctx, docs, transcripts, sessions, key, entry, path[:index], "message-fork", entryID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"sessionKey": forkedKey}
	if path[index].Text != "" {
		out["editorText"] = path[index].Text
	}
	return out, nil
}

func branchSessionAtCheckpoint(ctx context.Context, docs *state.DocsRepository, transcripts *state.TranscriptRepository, sessions *state.SessionStore, key, agentID, checkpointID string) (map[string]any, error) {
	entry, err := resolveHistorySession(sessions, key, agentID)
	if err != nil {
		return nil, err
	}
	checkpoint, err := findCompactionCheckpoint(entry, checkpointID)
	if err != nil {
		return nil, err
	}
	snapshot, err := transcripts.ReadSnapshot(ctx, checkpoint.SnapshotID, entry.SessionID)
	if err != nil {
		return nil, err
	}
	branchedKey, branchedEntry, err := createHistorySession(ctx, docs, transcripts, sessions, key, entry, snapshot, "checkpoint-branch", checkpointID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok": true, "sourceKey": key, "key": branchedKey, "sessionId": branchedEntry.SessionID,
		"checkpoint": compactionCheckpointResponse(checkpoint),
		"entry":      map[string]any{"sessionId": branchedEntry.SessionID, "updatedAt": branchedEntry.UpdatedAt.UnixMilli()},
	}, nil
}

func restoreSessionCheckpoint(ctx context.Context, transcripts *state.TranscriptRepository, sessions *state.SessionStore, key, agentID, checkpointID string) (map[string]any, error) {
	entry, err := resolveHistorySession(sessions, key, agentID)
	if err != nil {
		return nil, err
	}
	checkpoint, err := findCompactionCheckpoint(entry, checkpointID)
	if err != nil {
		return nil, err
	}
	snapshot, err := transcripts.ReadSnapshot(ctx, checkpoint.SnapshotID, entry.SessionID)
	if err != nil {
		return nil, err
	}
	graph, err := transcripts.EnsureGraph(ctx, key, entry.SessionID)
	if err != nil {
		return nil, err
	}
	for _, item := range snapshot {
		if _, err := transcripts.PutDetachedEntry(ctx, item); err != nil {
			return nil, err
		}
	}
	leaf := ""
	if len(snapshot) > 0 {
		leaf = snapshot[len(snapshot)-1].EntryID
	}
	heads := state.ReplaceTranscriptHead(graph.BranchHeads, graph.ActiveLeafID, leaf, true)
	committed, err := sessions.CommitTranscriptGraph(key, graph.Revision, state.TranscriptGraphMutation{ActiveLeafID: leaf, BranchHeads: heads})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok": true, "key": key, "sessionId": committed.SessionID,
		"checkpoint": compactionCheckpointResponse(checkpoint),
		"entry":      map[string]any{"sessionId": committed.SessionID, "updatedAt": committed.UpdatedAt.UnixMilli()},
	}, nil
}

func createHistorySession(ctx context.Context, docs *state.DocsRepository, transcripts *state.TranscriptRepository, sessions *state.SessionStore, sourceKey string, source state.SessionEntry, entries []state.TranscriptEntryDoc, reason, sourceRef string) (string, state.SessionEntry, error) {
	if docs == nil || transcripts == nil || sessions == nil {
		return "", state.SessionEntry{}, fmt.Errorf("session repositories unavailable")
	}
	newKey := sourceKey + ":fork:" + uuid.NewString()
	newSessionID := newKey
	for _, original := range entries {
		copy := original
		copy.SessionID = newSessionID
		if _, err := transcripts.PutDetachedEntry(ctx, copy); err != nil {
			return "", state.SessionEntry{}, err
		}
	}
	newEntry := source.CarryOverFlags(newSessionID)
	newEntry.AgentID = source.AgentID
	newEntry.SpawnedBy = sourceKey
	newEntry.ForkedFromParent = true
	newEntry.TranscriptGraphVersion = 1
	newEntry.TranscriptRevision = 1
	if len(entries) > 0 {
		newEntry.ActiveTranscriptLeafID = entries[len(entries)-1].EntryID
		newEntry.TranscriptBranchHeads = []string{newEntry.ActiveTranscriptLeafID}
	}
	if err := sessions.Put(newKey, newEntry); err != nil {
		return "", state.SessionEntry{}, err
	}
	stored, _ := sessions.Get(newKey)
	sourceDoc, sourceErr := docs.GetSession(ctx, source.SessionID)
	if sourceErr != nil && !errors.Is(sourceErr, state.ErrNotFound) {
		_ = sessions.Delete(newKey)
		return "", state.SessionEntry{}, sourceErr
	}
	requirement := sourceDoc.SandboxRequirement
	if requirement.IsZero() {
		requirement, sourceErr = sandbox.NewSessionRequirement("system", sandbox.CreatorSandboxInherit, "")
		if sourceErr != nil {
			_ = sessions.Delete(newKey)
			return "", state.SessionEntry{}, sourceErr
		}
	}
	doc := state.SessionDoc{Version: 1, SessionID: newSessionID, LastInboundAt: time.Now().Unix(), SandboxRequirement: requirement, Meta: map[string]any{"parent_session_key": sourceKey, "history_reason": reason, "history_source_ref": sourceRef}}
	if _, err := docs.PutSession(ctx, newSessionID, doc); err != nil {
		_ = sessions.Delete(newKey)
		return "", state.SessionEntry{}, err
	}
	return newKey, stored, nil
}

func truncateHistoryRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
