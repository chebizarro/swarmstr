package sessioncoord

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"metiq/internal/nostr/events"
	"metiq/internal/store/state"
)

type memoryStateStore struct {
	mu   sync.Mutex
	seq  int
	docs map[state.Address]state.Event
}

func newMemoryStateStore() *memoryStateStore {
	return &memoryStateStore{docs: map[state.Address]state.Event{}}
}
func (s *memoryStateStore) GetLatestReplaceable(_ context.Context, addr state.Address) (state.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.docs[addr]
	if !ok {
		return state.Event{}, state.ErrNotFound
	}
	return e, nil
}
func (s *memoryStateStore) PutReplaceable(_ context.Context, addr state.Address, content string, tags [][]string) (state.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	all := append([][]string{{"d", addr.DTag}}, tags...)
	e := state.Event{ID: fmt.Sprintf("e-%d", s.seq), PubKey: addr.PubKey, Kind: addr.Kind, CreatedAt: int64(s.seq), Tags: all, Content: content}
	s.docs[addr] = e
	return e, nil
}
func (s *memoryStateStore) PutAppend(ctx context.Context, addr state.Address, content string, tags [][]string) (state.Event, error) {
	return s.PutReplaceable(ctx, addr, content, tags)
}
func matches(tags [][]string, name, value string) bool {
	for _, tag := range tags {
		if len(tag) > 1 && tag[0] == name && tag[1] == value {
			return true
		}
	}
	return false
}
func (s *memoryStateStore) list(kind events.Kind, author, name, value string, limit int) []state.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []state.Event{}
	for _, e := range s.docs {
		if e.Kind == kind && (author == "" || e.PubKey == author) && matches(e.Tags, name, value) {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
func (s *memoryStateStore) ListByTag(_ context.Context, kind events.Kind, name, value string, limit int) ([]state.Event, error) {
	return s.list(kind, "", name, value, limit), nil
}
func (s *memoryStateStore) ListByTagForAuthor(_ context.Context, kind events.Kind, author, name, value string, limit int) ([]state.Event, error) {
	return s.list(kind, author, name, value, limit), nil
}
func (s *memoryStateStore) ListByTagPage(ctx context.Context, kind events.Kind, name, value string, limit int, _ *state.EventPageCursor) (state.EventPage, error) {
	e, _ := s.ListByTag(ctx, kind, name, value, limit)
	return state.EventPage{Events: e}, nil
}
func (s *memoryStateStore) ListByTagForAuthorPage(ctx context.Context, kind events.Kind, author, name, value string, limit int, _ *state.EventPageCursor) (state.EventPage, error) {
	e, _ := s.ListByTagForAuthor(ctx, kind, author, name, value, limit)
	return state.EventPage{Events: e}, nil
}

func newTestService(t *testing.T) (*Service, *state.DocsRepository, *state.SessionStore) {
	t.Helper()
	repo := state.NewDocsRepository(newMemoryStateStore(), "author")
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return New(repo, store), repo, store
}

func TestDispatchConflictAndDisconnectReclaim(t *testing.T) {
	ctx := context.Background()
	svc, repo, store := newTestService(t)
	if _, err := repo.PutSession(ctx, "s1", state.SessionDoc{Version: 1, SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("s1", state.SessionEntry{SessionID: "s1", AgentID: "old", ProviderOverride: "local"}); err != nil {
		t.Fatal(err)
	}
	placement, err := svc.Dispatch(ctx, DispatchRequest{Key: "s1", AgentID: "worker", Backend: "remote", ConnectionID: "c1", Subject: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if placement.State != "active" || placement.Generation != 1 {
		t.Fatalf("unexpected placement: %+v", placement)
	}
	if entry, _ := store.Get("s1"); entry.AgentID != "worker" || entry.ProviderOverride != "remote" {
		t.Fatalf("route not applied: %+v", entry)
	}
	if _, err := svc.Dispatch(ctx, DispatchRequest{Key: "s1", Backend: "other", ConnectionID: "c2"}); err == nil {
		t.Fatal("expected active-owner conflict")
	}
	if errs := svc.ReclaimConnection(ctx, "c1"); len(errs) != 0 {
		t.Fatalf("disconnect reclaim: %v", errs)
	}
	got, err := repo.GetSessionPlacement(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "reclaimed" || got.Generation != 2 || got.ReclaimReason != "owner disconnected" {
		t.Fatalf("unexpected reclaim: %+v", got)
	}
	if entry, _ := store.Get("s1"); entry.AgentID != "old" || entry.ProviderOverride != "local" {
		t.Fatalf("route not restored: %+v", entry)
	}
}

func TestBufferedEventsFlushExactlyOnce(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newTestService(t)
	if _, err := repo.PutSession(ctx, "s1", state.SessionDoc{Version: 1, SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Dispatch(ctx, DispatchRequest{Key: "s1", Backend: "remote", ConnectionID: "c1"}); err != nil {
		t.Fatal(err)
	}
	var events []string
	svc.SetBroadcaster(func(event string, _ any) { events = append(events, event) })
	svc.SetBroadcaster(func(event string, _ any) { events = append(events, event) })
	if len(events) != 1 || events[0] != "session.placement" {
		t.Fatalf("unexpected buffered events: %v", events)
	}
}

func TestGroupCatalogRenameAndDelete(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newTestService(t)
	_, _ = repo.PutSession(ctx, "s1", state.SessionDoc{Version: 1, SessionID: "s1", Meta: map[string]any{"group": "Work"}})
	groups, err := svc.PutGroups(ctx, []string{"Work", "Later"})
	if err != nil || len(groups) != 2 {
		t.Fatalf("put groups: %v %v", groups, err)
	}
	cwd := filepath.Join(t.TempDir(), "work")
	defaults, err := svc.UpdateGroupDefaults(ctx, "Work", &cwd, true)
	if err != nil || len(defaults) != 2 || defaults[0].Name != "Work" || defaults[0].CWD == nil || *defaults[0].CWD != cwd || !defaults[0].Worktree {
		t.Fatalf("update group defaults: %+v err=%v", defaults, err)
	}
	restarted := New(repo, nil)
	persisted, err := restarted.ListGroups(ctx)
	if err != nil || len(persisted) != 2 {
		t.Fatalf("list groups after restart: %v %v", persisted, err)
	}
	if updated, err := svc.RenameGroup(ctx, "Work", "Projects"); err != nil || updated != 1 {
		t.Fatalf("rename: updated=%d err=%v", updated, err)
	}
	session, _ := repo.GetSession(ctx, "s1")
	if session.Meta["group"] != "Projects" {
		t.Fatalf("group not renamed: %+v", session.Meta)
	}
	defaults, err = restarted.GroupDefaults(ctx)
	if err != nil || len(defaults) != 2 || defaults[0].Name != "Projects" || defaults[0].CWD == nil || *defaults[0].CWD != cwd {
		t.Fatalf("renamed defaults: %+v err=%v", defaults, err)
	}
	if updated, err := svc.DeleteGroup(ctx, "Projects"); err != nil || updated != 1 {
		t.Fatalf("delete: updated=%d err=%v", updated, err)
	}
	session, _ = repo.GetSession(ctx, "s1")
	if _, ok := session.Meta["group"]; ok {
		t.Fatalf("group not cleared: %+v", session.Meta)
	}
	defaults, err = restarted.GroupDefaults(ctx)
	if err != nil || len(defaults) != 1 || defaults[0].Name != "Later" {
		t.Fatalf("deleted defaults: %+v err=%v", defaults, err)
	}
}
