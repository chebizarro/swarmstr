package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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

func (m *memStateStore) ListByTagPage(_ context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	return m.listByTagPage(kind, "", tagName, tagValue, limit, cursor), nil
}

func (m *memStateStore) ListByTagForAuthorPage(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	return m.listByTagPage(kind, authorPubKey, tagName, tagValue, limit, cursor), nil
}

func (m *memStateStore) listByTag(kind events.Kind, authorPubKey, tagName, tagValue string, limit int) []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]Event, 0, len(m.replaceable))
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
	}
	sort.Slice(out, func(i, j int) bool {
		return transcriptDTag(out[i]) < transcriptDTag(out[j])
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *memStateStore) listByTagPage(kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *EventPageCursor) EventPage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]Event, 0, len(m.replaceable))
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
	}
	sortEventsNewestFirst(out)
	filtered := filterEventsForPage(out, cursor)
	page := EventPage{Events: filtered}
	if len(filtered) > limit {
		page.Events = filtered[:limit]
		page.NextCursor = nextCursorForPage(cursor, page.Events)
	}
	return page
}

func transcriptDTag(evt Event) string {
	for _, tag := range evt.Tags {
		if len(tag) >= 2 && tag[0] == "d" {
			return tag[1]
		}
	}
	return ""
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

func TestTranscriptListSessionTail_ReturnsLatestEntries(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s-tail",
			EntryID:   fmt.Sprintf("e%d", i),
			Role:      "user",
			Text:      fmt.Sprintf("message %d", i),
			Unix:      int64(i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%d: %v", i, err)
		}
	}

	entries, err := repo.ListSessionTail(ctx, "s-tail", 2)
	if err != nil {
		t.Fatalf("ListSessionTail: %v", err)
	}
	if len(entries) != 2 || entries[0].EntryID != "e4" || entries[1].EntryID != "e5" {
		t.Fatalf("unexpected tail entries: %+v", entries)
	}
}

func TestTranscriptListSessionAfter_ReturnsEntriesAfterCheckpoint(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s-after",
			EntryID:   fmt.Sprintf("e%d", i),
			Role:      "assistant",
			Text:      fmt.Sprintf("message %d", i),
			Unix:      int64(i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%d: %v", i, err)
		}
	}

	entries, err := repo.ListSessionAfter(ctx, "s-after", "e3", 10)
	if err != nil {
		t.Fatalf("ListSessionAfter: %v", err)
	}
	if len(entries) != 2 || entries[0].EntryID != "e4" || entries[1].EntryID != "e5" {
		t.Fatalf("unexpected after entries: %+v", entries)
	}
}

func TestTranscriptListSessionPage_StartsAtBeginningWhenCheckpointEmpty(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()
	for i := 1; i <= 6; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s-page-start",
			EntryID:   fmt.Sprintf("e%d", i),
			Role:      "assistant",
			Text:      fmt.Sprintf("message %d", i),
			Unix:      int64(i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%d: %v", i, err)
		}
	}

	page, err := repo.ListSessionPage(ctx, "s-page-start", "", 3)
	if err != nil {
		t.Fatalf("ListSessionPage: %v", err)
	}
	if !page.HasMore {
		t.Fatal("expected initial page to report more entries")
	}
	if len(page.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %+v", page.Entries)
	}
	got := []string{page.Entries[0].EntryID, page.Entries[1].EntryID, page.Entries[2].EntryID}
	want := []string{"e1", "e2", "e3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected page entries: got %v want %v", got, want)
	}
}

func TestTranscriptListSessionTail_ReturnsLatestEntriesAcrossLargeSessions(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()
	for i := 1; i <= 12; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s-tail-large",
			EntryID:   fmt.Sprintf("e%02d", i),
			Role:      "user",
			Text:      fmt.Sprintf("message %d", i),
			Unix:      int64(i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%02d: %v", i, err)
		}
	}

	entries, err := repo.ListSessionTail(ctx, "s-tail-large", 2)
	if err != nil {
		t.Fatalf("ListSessionTail: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 tail entries, got %+v", entries)
	}
	got := []string{entries[0].EntryID, entries[1].EntryID}
	want := []string{"e11", "e12"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected tail entries: got %v want %v", got, want)
	}
}

func TestTranscriptListSessionAfter_LimitsForwardFromCheckpoint(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()
	for i := 1; i <= 12; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s-after-large",
			EntryID:   fmt.Sprintf("e%02d", i),
			Role:      "assistant",
			Text:      fmt.Sprintf("message %d", i),
			Unix:      int64(i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%02d: %v", i, err)
		}
	}

	entries, err := repo.ListSessionAfter(ctx, "s-after-large", "e03", 4)
	if err != nil {
		t.Fatalf("ListSessionAfter: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 after entries, got %+v", entries)
	}
	got := []string{entries[0].EntryID, entries[1].EntryID, entries[2].EntryID, entries[3].EntryID}
	want := []string{"e04", "e05", "e06", "e07"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected after entries: got %v want %v", got, want)
	}
}

func TestTranscriptListSessionAfter_ReturnsCheckpointNotFound(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s-after-missing",
			EntryID:   fmt.Sprintf("e%d", i),
			Role:      "assistant",
			Text:      fmt.Sprintf("message %d", i),
			Unix:      int64(i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%d: %v", i, err)
		}
	}

	entries, err := repo.ListSessionAfter(ctx, "s-after-missing", "e99", 4)
	if !errors.Is(err, ErrTranscriptCheckpointNotFound) {
		t.Fatalf("expected checkpoint not found error, got entries=%+v err=%v", entries, err)
	}
}

func TestTranscriptListSessionAfter_FindsCheckpointBeyondLegacyWindow(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	ctx := context.Background()
	for i := 1; i <= 6005; i++ {
		_, err := repo.PutEntry(ctx, TranscriptEntryDoc{
			SessionID: "s-after-window",
			EntryID:   fmt.Sprintf("e%05d", i),
			Role:      "assistant",
			Text:      fmt.Sprintf("message %d", i),
			Unix:      int64(i),
		})
		if err != nil {
			t.Fatalf("PutEntry e%05d: %v", i, err)
		}
	}

	entries, err := repo.ListSessionAfter(ctx, "s-after-window", "e05550", 4)
	if err != nil {
		t.Fatalf("ListSessionAfter: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 after entries, got %+v", entries)
	}
	got := []string{entries[0].EntryID, entries[1].EntryID, entries[2].EntryID, entries[3].EntryID}
	want := []string{"e05551", "e05552", "e05553", "e05554"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected after entries: got %v want %v", got, want)
	}
}

func TestTranscriptListSessionAll_ExhaustsBeyondLegacyCap(t *testing.T) {
	store := newMemStateStore()
	repo := NewTranscriptRepository(store, "author")
	for i := 1; i <= 100005; i++ {
		seedTranscriptEventDirect(t, store, "author", TranscriptEntryDoc{
			SessionID: "s-all-large",
			EntryID:   fmt.Sprintf("e%06d", i),
			Role:      "user",
			Text:      "x",
			Unix:      int64(i),
		})
	}

	entries, err := repo.ListSessionAll(context.Background(), "s-all-large")
	if err != nil {
		t.Fatalf("ListSessionAll: %v", err)
	}
	if len(entries) != 100005 {
		t.Fatalf("expected 100005 entries, got %d", len(entries))
	}
	if entries[0].EntryID != "e000001" || entries[len(entries)-1].EntryID != "e100005" {
		t.Fatalf("unexpected entry bounds: first=%s last=%s", entries[0].EntryID, entries[len(entries)-1].EntryID)
	}
}

type pagedOnlyTranscriptStore struct {
	*memStateStore
	pageCalls int
}

func (s *pagedOnlyTranscriptStore) ListByTag(_ context.Context, _ events.Kind, _, _ string, _ int) ([]Event, error) {
	return nil, fmt.Errorf("unexpected non-paged query")
}

func (s *pagedOnlyTranscriptStore) ListByTagForAuthor(_ context.Context, _ events.Kind, _, _, _ string, _ int) ([]Event, error) {
	return nil, fmt.Errorf("unexpected non-paged query")
}

func (s *pagedOnlyTranscriptStore) ListByTagPage(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	s.pageCalls++
	return s.memStateStore.ListByTagPage(ctx, kind, tagName, tagValue, limit, cursor)
}

func (s *pagedOnlyTranscriptStore) ListByTagForAuthorPage(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	s.pageCalls++
	return s.memStateStore.ListByTagForAuthorPage(ctx, kind, authorPubKey, tagName, tagValue, limit, cursor)
}

func TestTranscriptListSessionAll_UsesPagedStateStorePath(t *testing.T) {
	store := &pagedOnlyTranscriptStore{memStateStore: newMemStateStore()}
	repo := NewTranscriptRepository(store, "author")
	for i := 1; i <= transcriptSessionPageLimit+25; i++ {
		seedTranscriptEventDirect(t, store.memStateStore, "author", TranscriptEntryDoc{
			SessionID: "s-paged",
			EntryID:   fmt.Sprintf("e%05d", i),
			Role:      "user",
			Text:      "x",
			Unix:      int64(i),
		})
	}

	entries, err := repo.ListSessionAll(context.Background(), "s-paged")
	if err != nil {
		t.Fatalf("ListSessionAll: %v", err)
	}
	if len(entries) != transcriptSessionPageLimit+25 {
		t.Fatalf("expected %d entries, got %d", transcriptSessionPageLimit+25, len(entries))
	}
	if store.pageCalls < 2 {
		t.Fatalf("expected multiple paged state-store queries, got %d", store.pageCalls)
	}
}

func TestTranscriptListSessionAll_PaginatesEntriesSharingBoundaryTimestamp(t *testing.T) {
	store := &pagedOnlyTranscriptStore{memStateStore: newMemStateStore()}
	repo := NewTranscriptRepository(store, "author")
	for i := 1; i <= transcriptSessionPageLimit+25; i++ {
		seedTranscriptEventDirect(t, store.memStateStore, "author", TranscriptEntryDoc{
			SessionID: "s-paged-same-unix",
			EntryID:   fmt.Sprintf("e%05d", i),
			Role:      "assistant",
			Text:      "x",
			Unix:      1700000000,
		})
	}

	entries, err := repo.ListSessionAll(context.Background(), "s-paged-same-unix")
	if err != nil {
		t.Fatalf("ListSessionAll: %v", err)
	}
	if len(entries) != transcriptSessionPageLimit+25 {
		t.Fatalf("expected %d entries, got %d", transcriptSessionPageLimit+25, len(entries))
	}
	if entries[0].EntryID != "e00001" || entries[len(entries)-1].EntryID != fmt.Sprintf("e%05d", transcriptSessionPageLimit+25) {
		t.Fatalf("unexpected entry bounds: first=%s last=%s", entries[0].EntryID, entries[len(entries)-1].EntryID)
	}
	if store.pageCalls < 2 {
		t.Fatalf("expected multiple paged queries for same-timestamp boundary, got %d", store.pageCalls)
	}
}

func seedTranscriptEventDirect(t *testing.T, store *memStateStore, author string, doc TranscriptEntryDoc) {
	t.Helper()
	if doc.Version == 0 {
		doc.Version = 1
	}
	if doc.Unix == 0 {
		doc.Unix = time.Now().Unix()
	}
	raw, err := encodeEnvelopePayload("transcript_entry_doc", doc, ensureCodec(nil))
	if err != nil {
		t.Fatalf("encode transcript event: %v", err)
	}
	addr := Address{Kind: events.KindTranscriptDoc, PubKey: author, DTag: fmt.Sprintf("metiq:tx:%s:%s", doc.SessionID, doc.EntryID)}
	store.mu.Lock()
	store.replaceable[store.storeKey(addr)] = Event{
		ID:        fmt.Sprintf("evt:%s", store.storeKey(addr)),
		PubKey:    author,
		Kind:      events.KindTranscriptDoc,
		CreatedAt: doc.Unix,
		Tags:      [][]string{{"session", protectedTagValue(doc.SessionID)}, {"entry", doc.EntryID}, {"role", doc.Role}, {"t", "transcript"}, {"d", addr.DTag}},
		Content:   raw,
	}
	store.mu.Unlock()
}
