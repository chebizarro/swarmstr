package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"metiq/internal/nostr/events"
)

var (
	ErrTranscriptGraphCorrupt     = errors.New("transcript graph is corrupt")
	ErrTranscriptNodeConflict     = errors.New("transcript node conflict")
	ErrTranscriptSnapshotNotFound = errors.New("transcript snapshot not found")
	ErrTranscriptSnapshotCorrupt  = errors.New("transcript snapshot is corrupt")
)

const maxTranscriptSnapshotEntries = 100000

type transcriptSnapshotManifest struct {
	Version    int    `json:"version"`
	SnapshotID string `json:"snapshot_id"`
	SessionID  string `json:"session_id"`
	CreatedAt  int64  `json:"created_at"`
	EntryCount int    `json:"entry_count"`
	LeafID     string `json:"leaf_entry_id,omitempty"`
	Hash       string `json:"hash"`
}

type transcriptSnapshotChunk struct {
	Version    int                `json:"version"`
	SnapshotID string             `json:"snapshot_id"`
	SessionID  string             `json:"session_id"`
	Sequence   int                `json:"sequence"`
	Entry      TranscriptEntryDoc `json:"entry"`
}

func (r *TranscriptRepository) EnsureGraph(ctx context.Context, key, sessionID string) (TranscriptGraphState, error) {
	if r == nil || r.sessionStore == nil {
		return TranscriptGraphState{}, fmt.Errorf("session graph store unavailable")
	}
	graph, _, ok := r.sessionStore.TranscriptGraph(key)
	if !ok {
		if err := r.sessionStore.Put(key, SessionEntry{SessionID: sessionID}); err != nil {
			return TranscriptGraphState{}, err
		}
		graph, _, _ = r.sessionStore.TranscriptGraph(key)
	}
	if graph.Version > 0 {
		return graph, nil
	}
	entries, err := r.listSessionOrderedAll(ctx, sessionID)
	if err != nil {
		return TranscriptGraphState{}, err
	}
	parent := ""
	for _, entry := range entries {
		entry.ParentEntryID = parent
		if _, err := r.putEntryRaw(ctx, entry); err != nil {
			return TranscriptGraphState{}, err
		}
		parent = entry.EntryID
	}
	heads := []string(nil)
	if parent != "" {
		heads = []string{parent}
	}
	committed, err := r.sessionStore.CommitTranscriptGraph(key, graph.Revision, TranscriptGraphMutation{ActiveLeafID: parent, BranchHeads: heads})
	if err != nil {
		return TranscriptGraphState{}, err
	}
	return graphStateFromEntry(committed), nil
}

func (r *TranscriptRepository) ListSessionGraphAll(ctx context.Context, sessionID string) ([]TranscriptEntryDoc, error) {
	return r.listSessionOrderedAll(ctx, sessionID)
}

func (r *TranscriptRepository) GetEntry(ctx context.Context, sessionID, entryID string) (TranscriptEntryDoc, error) {
	sessionID, entryID = strings.TrimSpace(sessionID), strings.TrimSpace(entryID)
	if sessionID == "" || entryID == "" {
		return TranscriptEntryDoc{}, fmt.Errorf("session_id and entry_id are required")
	}
	evt, err := r.store.GetLatestReplaceable(ctx, Address{Kind: events.KindAppData, PubKey: r.author, DTag: fmt.Sprintf("metiq:tx:%s:%s", sessionID, entryID)})
	if err != nil {
		return TranscriptEntryDoc{}, err
	}
	entry, err := r.decodeTranscriptEvent(evt)
	if err != nil {
		return TranscriptEntryDoc{}, err
	}
	if entry.Deleted {
		return TranscriptEntryDoc{}, ErrNotFound
	}
	return entry, nil
}

func (r *TranscriptRepository) PutDetachedEntry(ctx context.Context, entry TranscriptEntryDoc) (Event, error) {
	if existing, err := r.GetEntry(ctx, entry.SessionID, entry.EntryID); err == nil {
		candidate := entry
		if candidate.Version == 0 {
			candidate.Version = 1
		}
		candidate.Text = strings.TrimSpace(candidate.Text)
		if reflect.DeepEqual(existing, candidate) {
			return Event{}, nil
		}
		return Event{}, fmt.Errorf("%w: entry %q", ErrTranscriptNodeConflict, entry.EntryID)
	} else if !errors.Is(err, ErrNotFound) {
		return Event{}, err
	}
	return r.putEntryRaw(ctx, entry)
}

func (r *TranscriptRepository) ListSessionPath(ctx context.Context, sessionID, leafID string) ([]TranscriptEntryDoc, error) {
	leafID = strings.TrimSpace(leafID)
	if leafID == "" {
		return nil, nil
	}
	all, err := r.listSessionOrderedAll(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]TranscriptEntryDoc, len(all))
	for _, entry := range all {
		byID[entry.EntryID] = entry
	}
	seen := make(map[string]struct{}, len(all))
	reverse := make([]TranscriptEntryDoc, 0, len(all))
	for current := leafID; current != ""; {
		if _, ok := seen[current]; ok {
			return nil, fmt.Errorf("%w: cycle at %q", ErrTranscriptGraphCorrupt, current)
		}
		seen[current] = struct{}{}
		entry, ok := byID[current]
		if !ok {
			return nil, fmt.Errorf("%w: missing entry %q", ErrTranscriptGraphCorrupt, current)
		}
		reverse = append(reverse, entry)
		current = entry.ParentEntryID
	}
	out := make([]TranscriptEntryDoc, len(reverse))
	for i := range reverse {
		out[len(reverse)-1-i] = reverse[i]
	}
	return out, nil
}

func (r *TranscriptRepository) WriteSnapshot(ctx context.Context, snapshotID, sessionID string, entries []TranscriptEntryDoc) error {
	snapshotID, sessionID = strings.TrimSpace(snapshotID), strings.TrimSpace(sessionID)
	if snapshotID == "" || sessionID == "" {
		return fmt.Errorf("snapshot_id and session_id are required")
	}
	if len(entries) > maxTranscriptSnapshotEntries {
		return fmt.Errorf("snapshot exceeds %d entries", maxTranscriptSnapshotEntries)
	}
	canonical, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(canonical)
	for i, entry := range entries {
		chunk := transcriptSnapshotChunk{Version: 1, SnapshotID: snapshotID, SessionID: sessionID, Sequence: i, Entry: entry}
		raw, err := encodeEnvelopePayload("transcript_snapshot_chunk", chunk, r.codec)
		if err != nil {
			return err
		}
		if _, err := r.store.PutReplaceable(ctx, Address{Kind: events.KindAppData, PubKey: r.author, DTag: snapshotChunkDTag(sessionID, snapshotID, i)}, raw, [][]string{{"type", "transcript_snapshot_chunk"}, {"session", protectedTagValue(sessionID)}, {"snapshot", snapshotID}}); err != nil {
			return err
		}
	}
	leaf := ""
	if len(entries) > 0 {
		leaf = entries[len(entries)-1].EntryID
	}
	manifest := transcriptSnapshotManifest{Version: 1, SnapshotID: snapshotID, SessionID: sessionID, CreatedAt: time.Now().UnixMilli(), EntryCount: len(entries), LeafID: leaf, Hash: hex.EncodeToString(hash[:])}
	raw, err := encodeEnvelopePayload("transcript_snapshot_manifest", manifest, r.codec)
	if err != nil {
		return err
	}
	_, err = r.store.PutReplaceable(ctx, Address{Kind: events.KindAppData, PubKey: r.author, DTag: snapshotManifestDTag(sessionID, snapshotID)}, raw, [][]string{{"type", "transcript_snapshot_manifest"}, {"session", protectedTagValue(sessionID)}, {"snapshot", snapshotID}})
	return err
}

func (r *TranscriptRepository) ReadSnapshot(ctx context.Context, snapshotID, sessionID string) ([]TranscriptEntryDoc, error) {
	evt, err := r.store.GetLatestReplaceable(ctx, Address{Kind: events.KindAppData, PubKey: r.author, DTag: snapshotManifestDTag(sessionID, snapshotID)})
	if errors.Is(err, ErrNotFound) {
		return nil, ErrTranscriptSnapshotNotFound
	}
	if err != nil {
		return nil, err
	}
	var manifest transcriptSnapshotManifest
	if err := decodeEnvelopePayload(evt.Content, &manifest, r.codec); err != nil {
		return nil, fmt.Errorf("%w: manifest", ErrTranscriptSnapshotCorrupt)
	}
	if manifest.SnapshotID != snapshotID || manifest.SessionID != sessionID || manifest.EntryCount < 0 || manifest.EntryCount > maxTranscriptSnapshotEntries {
		return nil, fmt.Errorf("%w: invalid manifest", ErrTranscriptSnapshotCorrupt)
	}
	entries := make([]TranscriptEntryDoc, manifest.EntryCount)
	for i := 0; i < manifest.EntryCount; i++ {
		chunkEvt, err := r.store.GetLatestReplaceable(ctx, Address{Kind: events.KindAppData, PubKey: r.author, DTag: snapshotChunkDTag(sessionID, snapshotID, i)})
		if err != nil {
			return nil, fmt.Errorf("%w: missing chunk %d", ErrTranscriptSnapshotCorrupt, i)
		}
		var chunk transcriptSnapshotChunk
		if err := decodeEnvelopePayload(chunkEvt.Content, &chunk, r.codec); err != nil || chunk.SnapshotID != snapshotID || chunk.SessionID != sessionID || chunk.Sequence != i {
			return nil, fmt.Errorf("%w: chunk %d", ErrTranscriptSnapshotCorrupt, i)
		}
		entries[i] = chunk.Entry
	}
	canonical, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(canonical)
	if hex.EncodeToString(hash[:]) != manifest.Hash {
		return nil, fmt.Errorf("%w: hash mismatch", ErrTranscriptSnapshotCorrupt)
	}
	if len(entries) > 0 && entries[len(entries)-1].EntryID != manifest.LeafID {
		return nil, fmt.Errorf("%w: leaf mismatch", ErrTranscriptSnapshotCorrupt)
	}
	return entries, nil
}

func snapshotManifestDTag(sessionID, snapshotID string) string {
	return fmt.Sprintf("metiq:txsnap:%s:%s:manifest", sessionID, snapshotID)
}

func snapshotChunkDTag(sessionID, snapshotID string, sequence int) string {
	return fmt.Sprintf("metiq:txsnap:%s:%s:%08d", sessionID, snapshotID, sequence)
}
