package refresolve

import (
	"context"
	"errors"
	"testing"

	"metiq/internal/store/state"

	nostrmeta "metiq/internal/nostr/metadata"
)

type fakeTranscriptLookup struct {
	entries map[string]fakeEntry
}

type fakeEntry struct {
	doc     state.TranscriptEntryDoc
	notFound bool
}

func (f *fakeTranscriptLookup) GetEntryByNostrEventID(_ context.Context, eventID string) (state.TranscriptEntryDoc, error) {
	e, ok := f.entries[eventID]
	if !ok || e.notFound {
		return state.TranscriptEntryDoc{}, state.ErrNotFound
	}
	return e.doc, nil
}

func TestLocalResolver_NilLookup(t *testing.T) {
	r := NewLocalResolver(nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: hex64('a')}}}
	results := r.Resolve(context.Background(), refs)
	if results != nil {
		t.Errorf("expected nil for nil lookup, got %v", results)
	}
}

func TestLocalResolver_EmptyRefs(t *testing.T) {
	r := NewLocalResolver(&fakeTranscriptLookup{})
	results := r.Resolve(context.Background(), nil)
	if results != nil {
		t.Errorf("expected nil for empty refs, got %v", results)
	}
}

func TestLocalResolver_Hit(t *testing.T) {
	eventID := hex64('a')
	author := hex64('b')
	lookup := &fakeTranscriptLookup{
		entries: map[string]fakeEntry{
			eventID: {
				doc: state.TranscriptEntryDoc{
					SessionID: "test-session",
					EntryID:   eventID,
					Role:      "user",
					Text:      "hello world",
					Unix:      1700000000,
					Meta: map[string]any{
						"nostr_event_id": eventID,
						"nostr_pubkey":   author,
						"nostr_kind":     1,
					},
				},
			},
		},
	}
	r := NewLocalResolver(lookup)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: eventID}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	msg := results[0]
	if msg.ID != eventID {
		t.Errorf("expected ID %s, got %s", eventID, msg.ID)
	}
	if msg.Author != author {
		t.Errorf("expected author %s, got %s", author, msg.Author)
	}
	if msg.Kind != 1 {
		t.Errorf("expected kind 1, got %d", msg.Kind)
	}
	if msg.CreatedAt != 1700000000 {
		t.Errorf("expected createdAt 1700000000, got %d", msg.CreatedAt)
	}
	if msg.Body != "hello world" {
		t.Errorf("expected body 'hello world', got %q", msg.Body)
	}
	if msg.Via != "local" {
		t.Errorf("expected via 'local', got %q", msg.Via)
	}
	if msg.Deleted {
		t.Errorf("expected deleted=false")
	}
}

func TestLocalResolver_Tombstone(t *testing.T) {
	eventID := hex64('a')
	lookup := &fakeTranscriptLookup{
		entries: map[string]fakeEntry{
			eventID: {
				doc: state.TranscriptEntryDoc{
					SessionID: "test-session",
					EntryID:   eventID,
					Role:      "deleted",
					Text:      "",
					Unix:      1700000000,
					Deleted:   true,
					Meta: map[string]any{
						"nostr_event_id": eventID,
					},
				},
			},
		},
	}
	r := NewLocalResolver(lookup)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: eventID}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	msg := results[0]
	if !msg.Deleted {
		t.Errorf("expected deleted=true")
	}
	if msg.ID != eventID {
		t.Errorf("expected ID %s, got %s", eventID, msg.ID)
	}
	if msg.Via != "local" {
		t.Errorf("expected via 'local', got %q", msg.Via)
	}
}

func TestLocalResolver_Miss(t *testing.T) {
	eventID := hex64('a')
	lookup := &fakeTranscriptLookup{
		entries: map[string]fakeEntry{
			eventID: {notFound: true},
		},
	}
	r := NewLocalResolver(lookup)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: eventID}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Errorf("expected 0 results for miss, got %d", len(results))
	}
}

func TestLocalResolver_ErrNotFoundFromLookup(t *testing.T) {
	lookup := &fakeTranscriptLookup{entries: map[string]fakeEntry{}}
	r := NewLocalResolver(lookup)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: hex64('a')}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Errorf("expected 0 results for not-found lookup, got %d", len(results))
	}
}

func TestLocalResolver_MissingMeta(t *testing.T) {
	eventID := hex64('a')
	lookup := &fakeTranscriptLookup{
		entries: map[string]fakeEntry{
			eventID: {
				doc: state.TranscriptEntryDoc{
					SessionID: "test-session",
					EntryID:   eventID,
					Role:      "user",
					Text:      "hello",
					Unix:      1700000000,
					Meta:      nil,
				},
			},
		},
	}
	r := NewLocalResolver(lookup)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: eventID}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Errorf("expected 0 results for entry with nil meta, got %d", len(results))
	}
}

func TestLocalResolver_WrongEventIDInMeta(t *testing.T) {
	eventID := hex64('a')
	lookup := &fakeTranscriptLookup{
		entries: map[string]fakeEntry{
			eventID: {
				doc: state.TranscriptEntryDoc{
					SessionID: "test-session",
					EntryID:   eventID,
					Role:      "user",
					Text:      "hello",
					Unix:      1700000000,
					Meta: map[string]any{
						"nostr_event_id": hex64('b'),
					},
				},
			},
		},
	}
	r := NewLocalResolver(lookup)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: eventID}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Errorf("expected 0 results for mismatched meta event ID, got %d", len(results))
	}
}

func TestLocalResolver_MultipleRefs(t *testing.T) {
	idA := hex64('a')
	idB := hex64('b')
	idC := hex64('c')
	lookup := &fakeTranscriptLookup{
		entries: map[string]fakeEntry{
			idA: {
				doc: state.TranscriptEntryDoc{
					EntryID: idA,
					Text:    "msg a",
					Unix:    1700000000,
					Meta:    map[string]any{"nostr_event_id": idA, "nostr_pubkey": hex64('x'), "nostr_kind": 1},
				},
			},
			idB: {
				doc: state.TranscriptEntryDoc{
					EntryID: idB,
					Text:    "msg b",
					Unix:    1700000001,
					Meta:    map[string]any{"nostr_event_id": idB, "nostr_pubkey": hex64('y'), "nostr_kind": 1},
				},
			},
			idC: {
				doc: state.TranscriptEntryDoc{
					EntryID: idC,
					Text:    "msg c",
					Unix:    1700000002,
					Deleted: true,
					Meta:    map[string]any{"nostr_event_id": idC},
				},
			},
		},
	}
	r := NewLocalResolver(lookup)
	refs := []Ref{
		{Role: "parent", Event: nostrmeta.EventRef{ID: idA}},
		{Role: "quote", Event: nostrmeta.EventRef{ID: idB}},
		{Role: "root", Event: nostrmeta.EventRef{ID: idC}},
	}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].ID != idA || results[0].Body != "msg a" {
		t.Errorf("unexpected first result")
	}
	if results[1].ID != idB || results[1].Body != "msg b" {
		t.Errorf("unexpected second result")
	}
	if results[2].ID != idC || !results[2].Deleted {
		t.Errorf("expected third result to be deleted tombstone")
	}
}

func TestLocalResolver_ErrorIsNotErrNotFound(t *testing.T) {
	lookup := &errLookup{err: errors.New("some other error")}
	r := NewLocalResolver(lookup)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: hex64('a')}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-ErrNotFound error, got %d", len(results))
	}
}

type errLookup struct {
	err error
}

func (e *errLookup) GetEntryByNostrEventID(_ context.Context, _ string) (state.TranscriptEntryDoc, error) {
	return state.TranscriptEntryDoc{}, e.err
}
