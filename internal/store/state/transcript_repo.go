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
	store  NostrStateStore
	author string
	codec  secure.EnvelopeCodec
}

func NewTranscriptRepository(store NostrStateStore, authorPubKey string) *TranscriptRepository {
	return NewTranscriptRepositoryWithCodec(store, authorPubKey, nil)
}

func NewTranscriptRepositoryWithCodec(store NostrStateStore, authorPubKey string, codec secure.EnvelopeCodec) *TranscriptRepository {
	return &TranscriptRepository{store: store, author: authorPubKey, codec: ensureCodec(codec)}
}

func (r *TranscriptRepository) PutEntry(ctx context.Context, entry TranscriptEntryDoc) (Event, error) {
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
		{"session", protectedTagValue(entry.SessionID)},
		{"entry", entry.EntryID},
		{"role", entry.Role},
		{"t", "transcript"},
	}

	return r.store.PutReplaceable(ctx, Address{
		Kind:   events.KindTranscriptDoc,
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
		Kind:   events.KindTranscriptDoc,
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
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindTranscriptDoc, r.author, "session", protectedTagValue(sessionID), limit)
	if err != nil {
		return nil, err
	}
	byEntryID := make(map[string]TranscriptEntryDoc, len(rows))
	for _, row := range rows {
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
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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
		Kind:   events.KindTranscriptDoc,
		PubKey: r.author,
		DTag:   dTag,
	}, raw, tags)
	return err
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
