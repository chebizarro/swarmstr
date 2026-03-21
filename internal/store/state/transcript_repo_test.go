package state

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"metiq/internal/nostr/events"
)

// ─── minimal in-memory NostrStateStore for tests ─────────────────────────────

type memStateStore struct {
	mu          sync.Mutex
	replaceable map[string]Event
}

func newMemStateStore() *memStateStore {
	return &memStateStore{replaceable: map[string]Event{}}
}

func (m *memStateStore) storeKey(addr Address) string {
	return fmt.Sprintf("%d|%s|%s", addr.Kind, addr.PubKey, addr.DTag)
}

func (m *memStateStore) GetLatestReplaceable(_ context.Context, addr Address) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evt, ok := m.replaceable[m.storeKey(addr)]
	if !ok {
		return Event{}, ErrNotFound
	}
	return evt, nil
}

func (m *memStateStore) PutReplaceable(_ context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evt := Event{
		ID:        fmt.Sprintf("evt:%s", m.storeKey(addr)),
		PubKey:    addr.PubKey,
		Kind:      addr.Kind,
		CreatedAt: time.Now().Unix(),
		Tags:      append(extraTags, []string{"d", addr.DTag}),
		Content:   content,
	}
	m.replaceable[m.storeKey(addr)] = evt
	return evt, nil
}

func (m *memStateStore) PutAppend(_ context.Context, _ Address, _ string, _ [][]string) (Event, error) {
	return Event{}, nil
}

func (m *memStateStore) ListByTag(_ context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]Event, error) {
	return m.listByTag(kind, "", tagName, tagValue, limit), nil
}

func (m *memStateStore) ListByTagForAuthor(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error) {
	return m.listByTag(kind, authorPubKey, tagName, tagValue, limit), nil
}

func (m *memStateStore) listByTag(kind events.Kind, authorPubKey, tagName, tagValue string, limit int) []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]Event, 0, limit)
	for _, evt := range m.replaceable {
		if evt.Kind != kind {
			continue
		}
		if authorPubKey != "" && evt.PubKey != authorPubKey {
			continue
		}
		for _, tag := range evt.Tags {
			if len(tag) >= 2 && tag[0] == tagName && tag[1] == tagValue {
				out = append(out, evt)
				break
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestTranscriptDeleteEntry_tombstonesEntry(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()

	// Write three entries.
	for i := 1; i <= 3; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s1",
			EntryID:   fmt.Sprintf("e%d", i),
			Role:      "user",
			Text:      fmt.Sprintf("message %d", i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%d: %v", i, err)
		}
	}

	// Verify 3 entries visible.
	entries, err := repo.ListSession(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("ListSession: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Delete the first entry.
	if err := repo.DeleteEntry(ctx, "s1", "e1"); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}

	// Only 2 entries should remain.
	entries, err = repo.ListSession(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("ListSession after delete: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after tombstone, got %d", len(entries))
	}
	for _, e := range entries {
		if e.EntryID == "e1" {
			t.Error("tombstoned entry e1 should not appear in listing")
		}
	}
}

func TestTranscriptDeleteEntry_missingSessionIDErrors(t *testing.T) {
	repo := NewTranscriptRepository(newMemStateStore(), "author")
	if err := repo.DeleteEntry(context.Background(), "", "e1"); err == nil {
		t.Error("expected error for empty session_id")
	}
}

func TestTranscriptDeleteEntry_missingEntryIDErrors(t *testing.T) {
	repo := NewTranscriptRepository(newMemStateStore(), "author")
	if err := repo.DeleteEntry(context.Background(), "s1", ""); err == nil {
		t.Error("expected error for empty entry_id")
	}
}

func TestTranscriptDeleteEntry_idempotent(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()

	_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
		SessionID: "s1",
		EntryID:   "e1",
		Role:      "user",
		Text:      "hello",
	})
	if err != nil {
		t.Fatalf("PutEntry: %v", err)
	}

	// Delete twice — should not error.
	if err := repo.DeleteEntry(ctx, "s1", "e1"); err != nil {
		t.Fatalf("first DeleteEntry: %v", err)
	}
	if err := repo.DeleteEntry(ctx, "s1", "e1"); err != nil {
		t.Fatalf("second DeleteEntry: %v", err)
	}

	entries, err := repo.ListSession(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("ListSession: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after double tombstone, got %d", len(entries))
	}
}
