package checkpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var ErrRevisionConflict = errors.New("checkpoint revision conflict")

type repositorySession struct {
	Revision           int64        `json:"revision"`
	ActiveCheckpointID string       `json:"active_checkpoint_id,omitempty"`
	Checkpoints        []Checkpoint `json:"checkpoints,omitempty"`
}

type repositoryFile struct {
	Version  int                          `json:"version"`
	Sessions map[string]repositorySession `json:"sessions"`
}

// Repository owns atomic checkpoint persistence and revision-CAS mutations.
// It is intentionally independent from transcript storage: checkpoint refs
// contain the durable graph/snapshot boundaries used by transcript restore.
type Repository struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	data     repositoryFile
	cleanup  func(ArtifactRef) error
}

// OpenRepository opens or creates a durable checkpoint repository. The file is
// rewritten with temp-file+fsync+rename for each successful mutation.
func OpenRepository(path string, maxBytes int64, cleanup func(ArtifactRef) error) (*Repository, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("checkpoint repository path is required")
	}
	if maxBytes == 0 {
		maxBytes = MaxCheckpointBytesPerSession
	}
	r := &Repository{path: path, maxBytes: maxBytes, cleanup: cleanup, data: repositoryFile{Version: 1, Sessions: map[string]repositorySession{}}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("read checkpoint repository: %w", err)
	}
	if len(raw) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(raw, &r.data); err != nil {
		return nil, fmt.Errorf("decode checkpoint repository: %w", err)
	}
	if r.data.Sessions == nil {
		r.data.Sessions = map[string]repositorySession{}
	}
	var removed []Checkpoint
	trimmed := false
	for sessionKey, session := range r.data.Sessions {
		kept, dropped := trimCheckpointsByBudget(session.Checkpoints, r.maxBytes)
		if len(dropped) == 0 {
			continue
		}
		session.Checkpoints = kept
		if !containsCheckpoint(kept, session.ActiveCheckpointID) {
			if len(kept) > 0 {
				session.ActiveCheckpointID = kept[len(kept)-1].CheckpointID
			} else {
				session.ActiveCheckpointID = ""
			}
		}
		session.Revision++
		r.data.Sessions[sessionKey] = session
		removed = append(removed, dropped...)
		trimmed = true
	}
	if trimmed {
		if err := r.writeLocked(); err != nil {
			return nil, fmt.Errorf("enforce checkpoint retention: %w", err)
		}
		r.cleanupRemovedLocked(removed)
	}
	return r, nil
}

func (r *Repository) Revision(sessionKey string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Sessions[strings.TrimSpace(sessionKey)].Revision
}

func (r *Repository) List(sessionKey string) []Checkpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]Checkpoint(nil), r.data.Sessions[strings.TrimSpace(sessionKey)].Checkpoints...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].CheckpointID > out[j].CheckpointID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

func (r *Repository) Get(sessionKey, checkpointID string) *Checkpoint {
	for _, cp := range r.List(sessionKey) {
		if cp.CheckpointID == strings.TrimSpace(checkpointID) {
			out := cp
			return &out
		}
	}
	return nil
}

// Persist atomically appends a checkpoint when expectedRevision matches. It
// enforces count and retained-byte caps and cleans artifacts trimmed by the
// committed mutation.
func (r *Repository) Persist(p PersistParams, expectedRevision int64) (Checkpoint, int64, error) {
	if strings.TrimSpace(p.SessionKey) == "" || strings.TrimSpace(p.SessionID) == "" {
		return Checkpoint{}, 0, fmt.Errorf("session key and session id are required")
	}
	builder := NewStoreWithOptions(r.maxBytes, nil)
	cp := builder.Persist(p)

	r.mu.Lock()
	defer r.mu.Unlock()
	p.SessionKey = strings.TrimSpace(p.SessionKey)
	sess := r.data.Sessions[p.SessionKey]
	if sess.Revision != expectedRevision {
		return Checkpoint{}, sess.Revision, fmt.Errorf("%w: expected %d, got %d", ErrRevisionConflict, expectedRevision, sess.Revision)
	}
	combined := append(append([]Checkpoint(nil), sess.Checkpoints...), cp)
	kept, removed := trimCheckpointsByBudget(combined, r.maxBytes)
	previous, existed := r.data.Sessions[p.SessionKey]
	sess.Checkpoints = kept
	sess.ActiveCheckpointID = cp.CheckpointID
	sess.Revision++
	r.data.Sessions[p.SessionKey] = sess
	if err := r.writeLocked(); err != nil {
		if existed {
			r.data.Sessions[p.SessionKey] = previous
		} else {
			delete(r.data.Sessions, p.SessionKey)
		}
		return Checkpoint{}, expectedRevision, err
	}
	r.cleanupRemovedLocked(removed)
	return cp, sess.Revision, nil
}

// Restore marks an existing checkpoint as the active recovery boundary under
// revision CAS. Transcript callers then restore its PreCompaction graph leaf or
// SnapshotID; no history is destroyed here.
func (r *Repository) Restore(sessionKey, checkpointID string, expectedRevision int64) (Checkpoint, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessionKey, checkpointID = strings.TrimSpace(sessionKey), strings.TrimSpace(checkpointID)
	sess, ok := r.data.Sessions[sessionKey]
	if !ok {
		return Checkpoint{}, 0, fmt.Errorf("session %q not found", sessionKey)
	}
	if sess.Revision != expectedRevision {
		return Checkpoint{}, sess.Revision, fmt.Errorf("%w: expected %d, got %d", ErrRevisionConflict, expectedRevision, sess.Revision)
	}
	var found *Checkpoint
	for i := range sess.Checkpoints {
		if sess.Checkpoints[i].CheckpointID == checkpointID {
			copy := sess.Checkpoints[i]
			found = &copy
			break
		}
	}
	if found == nil {
		return Checkpoint{}, sess.Revision, fmt.Errorf("checkpoint %q not found", checkpointID)
	}
	if found.PreCompaction.LeafEntryID == "" && found.SnapshotID == "" {
		return Checkpoint{}, sess.Revision, fmt.Errorf("checkpoint source unavailable")
	}
	previous := r.data.Sessions[sessionKey]
	sess.ActiveCheckpointID = checkpointID
	sess.Revision++
	r.data.Sessions[sessionKey] = sess
	if err := r.writeLocked(); err != nil {
		r.data.Sessions[sessionKey] = previous
		return Checkpoint{}, expectedRevision, err
	}
	return *found, sess.Revision, nil
}

// Branch creates a new durable session ledger rooted at a checkpoint boundary.
// Transcript entry copying remains the caller's responsibility, but the source
// selection and destination creation are committed atomically in one file.
func (r *Repository) Branch(sourceKey, checkpointID, destinationKey, destinationSessionID string, expectedRevision int64) (Checkpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sourceKey, destinationKey = strings.TrimSpace(sourceKey), strings.TrimSpace(destinationKey)
	if sourceKey == "" || destinationKey == "" || strings.TrimSpace(destinationSessionID) == "" {
		return Checkpoint{}, fmt.Errorf("source, destination, and destination session id are required")
	}
	if _, exists := r.data.Sessions[destinationKey]; exists {
		return Checkpoint{}, fmt.Errorf("destination session %q already exists", destinationKey)
	}
	source, ok := r.data.Sessions[sourceKey]
	if !ok {
		return Checkpoint{}, fmt.Errorf("source session %q not found", sourceKey)
	}
	if source.Revision != expectedRevision {
		return Checkpoint{}, fmt.Errorf("%w: expected %d, got %d", ErrRevisionConflict, expectedRevision, source.Revision)
	}
	var root *Checkpoint
	for _, cp := range source.Checkpoints {
		if cp.CheckpointID == checkpointID {
			copy := cp
			copy.SessionKey = destinationKey
			copy.SessionID = destinationSessionID
			root = &copy
			break
		}
	}
	if root == nil {
		return Checkpoint{}, fmt.Errorf("checkpoint %q not found", checkpointID)
	}
	if root.PreCompaction.LeafEntryID == "" && root.SnapshotID == "" {
		return Checkpoint{}, fmt.Errorf("checkpoint source unavailable")
	}
	r.data.Sessions[destinationKey] = repositorySession{Revision: 1, ActiveCheckpointID: root.CheckpointID, Checkpoints: []Checkpoint{*root}}
	if err := r.writeLocked(); err != nil {
		delete(r.data.Sessions, destinationKey)
		return Checkpoint{}, err
	}
	return *root, nil
}

func (r *Repository) DeleteSession(sessionKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess, ok := r.data.Sessions[strings.TrimSpace(sessionKey)]
	if !ok {
		return nil
	}
	delete(r.data.Sessions, strings.TrimSpace(sessionKey))
	if err := r.writeLocked(); err != nil {
		r.data.Sessions[sessionKey] = sess
		return err
	}
	r.cleanupRemovedLocked(sess.Checkpoints)
	return nil
}

func (r *Repository) writeLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("create checkpoint repository dir: %w", err)
	}
	raw, err := json.MarshalIndent(r.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".checkpoints-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, r.path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(r.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (r *Repository) cleanupRemovedLocked(removed []Checkpoint) {
	if r.cleanup == nil {
		return
	}
	refs := map[string]struct{}{}
	for _, session := range r.data.Sessions {
		for _, cp := range session.Checkpoints {
			if cp.SnapshotArtifact != nil {
				refs[cp.SnapshotArtifact.ID] = struct{}{}
			}
		}
	}
	for _, cp := range removed {
		if cp.SnapshotArtifact == nil {
			continue
		}
		if _, stillReferenced := refs[cp.SnapshotArtifact.ID]; stillReferenced {
			continue
		}
		_ = r.cleanup(*cp.SnapshotArtifact)
	}
}

func containsCheckpoint(checkpoints []Checkpoint, checkpointID string) bool {
	for _, cp := range checkpoints {
		if cp.CheckpointID == checkpointID {
			return true
		}
	}
	return false
}
