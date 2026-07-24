package state

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const maxPersistedCompactionCheckpoints = 25

var ErrTranscriptRevisionConflict = errors.New("transcript revision conflict")

type TranscriptGraphState struct {
	Version      int
	Revision     int64
	ActiveLeafID string
	BranchHeads  []string
}

type TranscriptGraphMutation struct {
	ActiveLeafID    string
	BranchHeads     []string
	Checkpoint      *CompactionCheckpointRef
	CompactionDelta int64
}

func (s *SessionStore) ResolveSessionKey(sessionID string) (string, SessionEntry, bool, error) {
	if s == nil {
		return "", SessionEntry{}, false, fmt.Errorf("session store unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", SessionEntry{}, false, fmt.Errorf("session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[sessionID]; ok {
		return sessionID, cloneSessionEntry(entry), true, nil
	}
	var foundKey string
	var found SessionEntry
	for key, entry := range s.entries {
		if strings.TrimSpace(entry.SessionID) != sessionID {
			continue
		}
		if foundKey != "" {
			return "", SessionEntry{}, false, fmt.Errorf("multiple session keys resolve to session_id %q", sessionID)
		}
		foundKey, found = key, entry
	}
	if foundKey == "" {
		return "", SessionEntry{}, false, nil
	}
	return foundKey, cloneSessionEntry(found), true, nil
}

func (s *SessionStore) TranscriptGraph(key string) (TranscriptGraphState, SessionEntry, bool) {
	entry, ok := s.Get(strings.TrimSpace(key))
	if !ok {
		return TranscriptGraphState{}, SessionEntry{}, false
	}
	return graphStateFromEntry(entry), entry, true
}

func graphStateFromEntry(entry SessionEntry) TranscriptGraphState {
	return TranscriptGraphState{
		Version:      entry.TranscriptGraphVersion,
		Revision:     entry.TranscriptRevision,
		ActiveLeafID: entry.ActiveTranscriptLeafID,
		BranchHeads:  append([]string(nil), entry.TranscriptBranchHeads...),
	}
}

// CommitTranscriptGraph is the single durable publication point for transcript
// DAG mutations. Detached transcript nodes and snapshots must be persisted
// before this CAS journal commit.
func (s *SessionStore) CommitTranscriptGraph(key string, expectedRevision int64, mutation TranscriptGraphMutation) (SessionEntry, error) {
	if s == nil {
		return SessionEntry{}, fmt.Errorf("session store unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return SessionEntry{}, fmt.Errorf("session key is required")
	}
	heads, err := normalizeTranscriptHeads(mutation.BranchHeads)
	if err != nil {
		return SessionEntry{}, err
	}
	active := strings.TrimSpace(mutation.ActiveLeafID)
	if active != "" && !containsString(heads, active) {
		return SessionEntry{}, fmt.Errorf("active transcript leaf must be a branch head")
	}
	var committed SessionEntry
	err = s.mutateEntryAndJournal(key, func(entry *SessionEntry) error {
		if entry.SessionID == "" {
			return fmt.Errorf("session %q not found", key)
		}
		if entry.TranscriptRevision != expectedRevision {
			return fmt.Errorf("%w: expected %d, got %d", ErrTranscriptRevisionConflict, expectedRevision, entry.TranscriptRevision)
		}
		entry.TranscriptGraphVersion = 1
		entry.TranscriptRevision++
		entry.ActiveTranscriptLeafID = active
		entry.TranscriptBranchHeads = append([]string(nil), heads...)
		if mutation.CompactionDelta != 0 {
			entry.CompactionCount += mutation.CompactionDelta
		}
		if mutation.Checkpoint != nil {
			cp := cloneCompactionCheckpointRef(*mutation.Checkpoint)
			entry.CompactionCheckpoints = append(entry.CompactionCheckpoints, cp)
			if len(entry.CompactionCheckpoints) > maxPersistedCompactionCheckpoints {
				sort.SliceStable(entry.CompactionCheckpoints, func(i, j int) bool {
					if entry.CompactionCheckpoints[i].CreatedAt == entry.CompactionCheckpoints[j].CreatedAt {
						return entry.CompactionCheckpoints[i].CheckpointID < entry.CompactionCheckpoints[j].CheckpointID
					}
					return entry.CompactionCheckpoints[i].CreatedAt < entry.CompactionCheckpoints[j].CreatedAt
				})
				entry.CompactionCheckpoints = append([]CompactionCheckpointRef(nil), entry.CompactionCheckpoints[len(entry.CompactionCheckpoints)-maxPersistedCompactionCheckpoints:]...)
			}
		}
		entry.UpdatedAt = time.Now().UTC()
		committed = cloneSessionEntry(*entry)
		return nil
	})
	if err != nil {
		return SessionEntry{}, err
	}
	return committed, nil
}

func normalizeTranscriptHeads(in []string) ([]string, error) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func ReplaceTranscriptHead(heads []string, previous, next string, preservePrevious bool) []string {
	out := make([]string, 0, len(heads)+2)
	for _, head := range heads {
		if head == previous && !preservePrevious {
			continue
		}
		out = append(out, head)
	}
	if preservePrevious && strings.TrimSpace(previous) != "" {
		out = append(out, previous)
	}
	if strings.TrimSpace(next) != "" {
		out = append(out, next)
	}
	normalized, _ := normalizeTranscriptHeads(out)
	return normalized
}
