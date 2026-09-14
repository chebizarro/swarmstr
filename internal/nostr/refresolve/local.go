package refresolve

import (
	"context"
	"errors"
	"strings"

	"metiq/internal/store/state"

	nostrmeta "metiq/internal/nostr/metadata"
)

type TranscriptLookup interface {
	GetEntryByNostrEventID(ctx context.Context, eventID string) (state.TranscriptEntryDoc, error)
}

type LocalResolver struct {
	lookup TranscriptLookup
}

func NewLocalResolver(lookup TranscriptLookup) *LocalResolver {
	return &LocalResolver{lookup: lookup}
}

func (r *LocalResolver) Resolve(ctx context.Context, refs []Ref) []ReferencedMessage {
	if r.lookup == nil || len(refs) == 0 {
		return nil
	}
	out := make([]ReferencedMessage, 0, len(refs))
	for _, ref := range refs {
		msg := r.resolveOne(ctx, ref)
		if msg != nil {
			out = append(out, *msg)
		}
	}
	return out
}

func (r *LocalResolver) resolveOne(ctx context.Context, ref Ref) *ReferencedMessage {
	entry, err := r.lookup.GetEntryByNostrEventID(ctx, ref.Event.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return nil
	}
	if entry.Deleted {
		return &ReferencedMessage{
			ID:      ref.Event.ID,
			Deleted: true,
			Role:    ref.Role,
			Via:     "local",
		}
	}
	eventID, _ := metaString(entry.Meta, "nostr_event_id")
	if eventID == "" || eventID != ref.Event.ID {
		return nil
	}
	author, _ := metaString(entry.Meta, "nostr_pubkey")
	kind, _ := metaInt(entry.Meta, "nostr_kind")
	body, _ := nostrmeta.NormalizeText(entry.Text, MaxBodyChars)

	return &ReferencedMessage{
		ID:        ref.Event.ID,
		Author:    author,
		Kind:      kind,
		CreatedAt: entry.Unix,
		Body:      body,
		Role:      ref.Role,
		Via:       "local",
	}
}

func metaString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return strings.TrimSpace(s), ok
}

func metaInt(m map[string]any, key string) (int, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	}
	return 0, false
}
