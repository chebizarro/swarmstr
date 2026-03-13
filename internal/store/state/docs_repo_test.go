package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"swarmstr/internal/nostr/events"
)

type fakeStateStore struct {
	mu      sync.Mutex
	nowUnix int64
	repl    map[Address]Event
	appends []Event
}

func newFakeStateStore() *fakeStateStore {
	return &fakeStateStore{nowUnix: time.Now().Unix(), repl: map[Address]Event{}}
}

func (s *fakeStateStore) nextEventID() string {
	s.nowUnix++
	return fmt.Sprintf("evt-%d", s.nowUnix)
}

func (s *fakeStateStore) GetLatestReplaceable(_ context.Context, addr Address) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evt, ok := s.repl[addr]
	if !ok {
		return Event{}, ErrNotFound
	}
	return evt, nil
}

func (s *fakeStateStore) PutReplaceable(_ context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := make([][]string, 0, len(extraTags)+1)
	tags = append(tags, []string{"d", addr.DTag})
	tags = append(tags, extraTags...)
	evt := Event{ID: s.nextEventID(), PubKey: addr.PubKey, Kind: addr.Kind, CreatedAt: s.nowUnix, Tags: tags, Content: content}
	s.repl[addr] = evt
	return evt, nil
}

func (s *fakeStateStore) PutAppend(_ context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := make([][]string, 0, len(extraTags)+1)
	tags = append(tags, []string{"d", addr.DTag})
	tags = append(tags, extraTags...)
	evt := Event{ID: s.nextEventID(), PubKey: addr.PubKey, Kind: addr.Kind, CreatedAt: s.nowUnix, Tags: tags, Content: content}
	s.appends = append(s.appends, evt)
	return evt, nil
}

func (s *fakeStateStore) ListByTag(_ context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, 0, limit)
	for _, evt := range s.repl {
		if evt.Kind != kind || !hasTagValue(evt.Tags, tagName, tagValue) {
			continue
		}
		out = append(out, evt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeStateStore) ListByTagForAuthor(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, 0, limit)
	for _, evt := range s.repl {
		if evt.Kind != kind || evt.PubKey != authorPubKey || !hasTagValue(evt.Tags, tagName, tagValue) {
			continue
		}
		out = append(out, evt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func TestDocsRepository_ConfigListSessionCheckpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	cfg := ConfigDoc{Version: 1, DM: DMPolicy{Policy: "open"}}
	evt, err := repo.PutConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("PutConfig: %v", err)
	}
	if !strings.HasPrefix(evt.ID, "evt-") {
		t.Fatalf("unexpected config event id: %s", evt.ID)
	}
	gotCfg, gotEvt, err := repo.GetConfigWithEvent(ctx)
	if err != nil {
		t.Fatalf("GetConfigWithEvent: %v", err)
	}
	if gotCfg.DM.Policy != "open" {
		t.Fatalf("unexpected config policy: %q", gotCfg.DM.Policy)
	}
	if gotEvt.ID == "" {
		t.Fatal("expected config event metadata")
	}

	listDoc := ListDoc{Version: 1, Name: "allow", Items: []string{"npub1a", "npub1b"}}
	if _, err := repo.PutList(ctx, "allow", listDoc); err != nil {
		t.Fatalf("PutList: %v", err)
	}
	gotList, err := repo.GetList(ctx, "allow")
	if err != nil {
		t.Fatalf("GetList: %v", err)
	}
	if len(gotList.Items) != 2 {
		t.Fatalf("unexpected list item count: %d", len(gotList.Items))
	}

	if _, err := repo.PutSession(ctx, "s1", SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer-a", LastInboundAt: 10}); err != nil {
		t.Fatalf("PutSession first: %v", err)
	}
	if _, err := repo.PutSession(ctx, "s1", SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer-a", LastInboundAt: 20}); err != nil {
		t.Fatalf("PutSession second: %v", err)
	}
	sessions, err := repo.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected deduped session list, got %d", len(sessions))
	}
	if sessions[0].LastInboundAt != 20 {
		t.Fatalf("expected latest session activity, got %d", sessions[0].LastInboundAt)
	}

	if _, err := repo.PutCheckpoint(ctx, "dm_ingest", CheckpointDoc{Version: 1, Name: "dm_ingest", LastEvent: "evt-9", LastUnix: 99}); err != nil {
		t.Fatalf("PutCheckpoint: %v", err)
	}
	cp, err := repo.GetCheckpoint(ctx, "dm_ingest")
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if cp.LastEvent != "evt-9" || cp.LastUnix != 99 {
		t.Fatalf("unexpected checkpoint %+v", cp)
	}
}
