package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/nostr/events"
	"metiq/internal/nostr/secure"
)

type TranscriptRepository struct {
	store        NostrStateStore
	author       string
	codec        secure.EnvelopeCodec
	sessionStore *SessionStore
}

const transcriptSessionPageLimit = 1024

var ErrTranscriptCheckpointNotFound = errors.New("transcript checkpoint not found")

type TranscriptPage struct {
	Entries []TranscriptEntryDoc
	HasMore bool
}

func NewTranscriptRepository(store NostrStateStore, authorPubKey string) *TranscriptRepository {
	return NewTranscriptRepositoryWithCodec(store, authorPubKey, nil)
}

func NewTranscriptRepositoryWithCodec(store NostrStateStore, authorPubKey string, codec secure.EnvelopeCodec) *TranscriptRepository {
	return &TranscriptRepository{store: store, author: authorPubKey, codec: ensureCodec(codec)}
}

// BindSessionStore enables durable active-path semantics. It is called once
// during daemon construction after the file-backed SessionStore is opened.
func (r *TranscriptRepository) BindSessionStore(store *SessionStore) *TranscriptRepository {
	if r != nil {
		r.sessionStore = store
	}
	return r
}

func (r *TranscriptRepository) PutEntry(ctx context.Context, entry TranscriptEntryDoc) (Event, error) {
	if r.sessionStore == nil {
		return r.putEntryRaw(ctx, entry)
	}
	key, _, ok, err := r.sessionStore.ResolveSessionKey(entry.SessionID)
	if err != nil {
		return Event{}, err
	}
	if !ok {
		return r.putEntryRaw(ctx, entry)
	}
	graph, err := r.EnsureGraph(ctx, key, entry.SessionID)
	if err != nil {
		return Event{}, err
	}
	entry.ParentEntryID = graph.ActiveLeafID
	evt, err := r.PutDetachedEntry(ctx, entry)
	if err != nil {
		return Event{}, err
	}
	heads := ReplaceTranscriptHead(graph.BranchHeads, graph.ActiveLeafID, entry.EntryID, false)
	if _, err := r.sessionStore.CommitTranscriptGraph(key, graph.Revision, TranscriptGraphMutation{ActiveLeafID: entry.EntryID, BranchHeads: heads}); err != nil {
		return Event{}, err
	}
	return evt, nil
}

func (r *TranscriptRepository) putEntryRaw(ctx context.Context, entry TranscriptEntryDoc) (Event, error) {
	if entry.Version == 0 {
		entry.Version = 1
	}
	if entry.Unix == 0 {
		entry.Unix = time.Now().Unix()
	}
	if entry.SessionID == "" {
		return Event{}, fmt.Errorf("session_id is required")
	}
	if entry.EntryID == "" {
		return Event{}, fmt.Errorf("entry_id is required")
	}
	entry.Text = strings.TrimSpace(entry.Text)
	if err := enforceTextLimit("text", entry.Text, maxTranscriptTextRunes); err != nil {
		return Event{}, err
	}
	if err := enforceMetaBytes("meta", entry.Meta, maxTranscriptMetaBytes); err != nil {
		return Event{}, err
	}
	if !isValidRole(entry.Role) {
		return Event{}, fmt.Errorf("invalid role %q", entry.Role)
	}

	raw, err := encodeEnvelopePayload("transcript_entry_doc", entry, r.codec)
	if err != nil {
		return Event{}, err
	}

	dTag := fmt.Sprintf("metiq:tx:%s:%s", entry.SessionID, entry.EntryID)
	tags := [][]string{
		{"type", events.AppDataTypeTranscript},
		{"session", protectedTagValue(entry.SessionID)},
		{"entry", entry.EntryID},
		{"role", entry.Role},
		{"t", "transcript"},
	}

	return r.store.PutReplaceable(ctx, Address{
		Kind:   events.KindAppData,
		PubKey: r.author,
		DTag:   dTag,
	}, raw, tags)
}

func (r *TranscriptRepository) HasEntry(ctx context.Context, sessionID, entryID string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("session_id is required")
	}
	if entryID == "" {
		return false, fmt.Errorf("entry_id is required")
	}
	_, err := r.store.GetLatestReplaceable(ctx, Address{
		Kind:   events.KindAppData,
		PubKey: r.author,
		DTag:   fmt.Sprintf("metiq:tx:%s:%s", sessionID, entryID),
	})
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *TranscriptRepository) ListSession(ctx context.Context, sessionID string, limit int) ([]TranscriptEntryDoc, error) {
	if limit <= 0 {
		limit = 100
	}
	out, err := r.ListSessionAll(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *TranscriptRepository) ListSessionAll(ctx context.Context, sessionID string) ([]TranscriptEntryDoc, error) {
	if r.sessionStore != nil {
		_, entry, ok, err := r.sessionStore.ResolveSessionKey(sessionID)
		if err != nil {
			return nil, err
		}
		if ok && entry.TranscriptGraphVersion > 0 {
			if entry.ActiveTranscriptLeafID == "" {
				return nil, nil
			}
			return r.ListSessionPath(ctx, sessionID, entry.ActiveTranscriptLeafID)
		}
	}
	return r.listSessionOrderedAll(ctx, sessionID)
}

// ListSessionAllBranches returns every transcript DAG node for archival and
// maintenance. Normal conversation reads must continue using ListSessionAll,
// which projects only the active path.
func (r *TranscriptRepository) ListSessionAllBranches(ctx context.Context, sessionID string) ([]TranscriptEntryDoc, error) {
	return r.listSessionOrderedAll(ctx, sessionID)
}

func (r *TranscriptRepository) ListSessionTail(ctx context.Context, sessionID string, limit int) ([]TranscriptEntryDoc, error) {
	if limit <= 0 {
		limit = 100
	}
	out, err := r.ListSessionAll(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (r *TranscriptRepository) ListSessionAfter(ctx context.Context, sessionID, afterEntryID string, limit int) ([]TranscriptEntryDoc, error) {
	page, err := r.ListSessionPage(ctx, sessionID, afterEntryID, limit)
	if err != nil {
		return nil, err
	}
	return page.Entries, nil
}

func (r *TranscriptRepository) ListSessionPage(ctx context.Context, sessionID, afterEntryID string, limit int) (TranscriptPage, error) {
	if limit <= 0 {
		limit = 100
	}
	out, err := r.ListSessionAll(ctx, sessionID)
	if err != nil {
		return TranscriptPage{}, err
	}
	start := 0
	if trimmed := strings.TrimSpace(afterEntryID); trimmed != "" {
		found := false
		for i, entry := range out {
			if entry.EntryID != trimmed {
				continue
			}
			start = i + 1
			found = true
			break
		}
		if !found {
			return TranscriptPage{}, ErrTranscriptCheckpointNotFound
		}
	}
	if start >= len(out) {
		return TranscriptPage{}, nil
	}
	end := start + limit
	hasMore := end < len(out)
	if end > len(out) {
		end = len(out)
	}
	return TranscriptPage{
		Entries: out[start:end],
		HasMore: hasMore,
	}, nil
}

func (r *TranscriptRepository) listSessionOrdered(ctx context.Context, sessionID string, limit int) ([]TranscriptEntryDoc, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindAppData, r.author, "session", protectedTagValue(sessionID), limit)
	if err != nil {
		return nil, err
	}
	return r.decodeOrderedSessionRows(sessionID, rows), nil
}

func (r *TranscriptRepository) listSessionOrderedAll(ctx context.Context, sessionID string) ([]TranscriptEntryDoc, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	var (
		cursor *EventPageCursor
		rows   []Event
	)
	for {
		page, err := r.store.ListByTagForAuthorPage(
			ctx,
			events.KindAppData,
			r.author,
			"session",
			protectedTagValue(sessionID),
			transcriptSessionPageLimit,
			cursor,
		)
		if err != nil {
			return nil, err
		}
		rows = append(rows, page.Events...)
		if page.NextCursor == nil || len(page.Events) == 0 {
			return r.decodeOrderedSessionRows(sessionID, rows), nil
		}
		cursor = page.NextCursor
	}
}

func (r *TranscriptRepository) decodeOrderedSessionRows(sessionID string, rows []Event) []TranscriptEntryDoc {
	byEntryID := make(map[string]TranscriptEntryDoc, len(rows))
	for _, row := range rows {
		// Filter for transcript documents only (NIP-78 type discrimination)
		if !hasTagValue(row.Tags, "type", events.AppDataTypeTranscript) {
			continue
		}
		doc, err := r.decodeTranscriptEvent(row)
		if err != nil || doc.SessionID != sessionID {
			continue
		}
		if prior, ok := byEntryID[doc.EntryID]; !ok || doc.Unix > prior.Unix {
			byEntryID[doc.EntryID] = doc
		}
	}

	out := make([]TranscriptEntryDoc, 0, len(byEntryID))
	for _, doc := range byEntryID {
		if doc.Deleted {
			continue // skip tombstoned entries
		}
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Unix == out[j].Unix {
			return out[i].EntryID < out[j].EntryID
		}
		return out[i].Unix < out[j].Unix
	})
	return out
}

// DeleteEntry tombstones a transcript entry so it is excluded from future ListSession results.
// The underlying Nostr event is overwritten (PutReplaceable) with a deleted=true marker.
func (r *TranscriptRepository) DeleteEntry(ctx context.Context, sessionID, entryID string) error {
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if entryID == "" {
		return fmt.Errorf("entry_id is required")
	}
	tombstone := TranscriptEntryDoc{
		Version:   1,
		SessionID: sessionID,
		EntryID:   entryID,
		Role:      "deleted",
		Text:      "",
		Unix:      time.Now().Unix(),
		Deleted:   true,
	}
	raw, err := encodeEnvelopePayload("transcript_entry_doc", tombstone, r.codec)
	if err != nil {
		return err
	}
	dTag := fmt.Sprintf("metiq:tx:%s:%s", sessionID, entryID)
	tags := [][]string{
		{"session", protectedTagValue(sessionID)},
		{"entry", entryID},
		{"role", "deleted"},
		{"t", "transcript"},
	}
	_, err = r.store.PutReplaceable(ctx, Address{
		Kind:   events.KindAppData,
		PubKey: r.author,
		DTag:   dTag,
	}, raw, tags)
	return err
}

// ReplaceEntry overwrites an existing transcript entry in place at its durable
// address (metiq:tx:<session>:<entry>), preserving graph topology (the EntryID +
// ParentEntryID are kept exactly as supplied). Unlike PutEntry it never appends a
// new branch leaf, and unlike PutDetachedEntry it does not reject a content
// change — it is the mutate-in-place primitive backing message.action edit/react.
// Callers supply the full, already-merged entry (typically read via GetEntry,
// mutated, then written back) so the overwrite is total, not a partial patch.
func (r *TranscriptRepository) ReplaceEntry(ctx context.Context, entry TranscriptEntryDoc) (Event, error) {
	return r.putEntryRaw(ctx, entry)
}

func (r *TranscriptRepository) decodeTranscriptEvent(evt Event) (TranscriptEntryDoc, error) {
	var doc TranscriptEntryDoc
	if err := decodeEnvelopePayload(evt.Content, &doc, r.codec); err != nil {
		return TranscriptEntryDoc{}, err
	}
	return doc, nil
}

func isValidRole(role string) bool {
	switch role {
	case "user", "assistant", "system", "tool":
		return true
	default:
		return false
	}
}
