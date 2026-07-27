package channels

import (
	"context"
	"path/filepath"
	"testing"

	nostr "fiatjaf.com/nostr"
)

// Startup replay re-dispatches unsettled events once and dedups already-seen
// ones (cross-restart at-least-once).
func TestReplayPending_RedispatchesUnsettled(t *testing.T) {
	store, _ := NewPendingEventStore(filepath.Join(t.TempDir(), "pending.json"))
	ev := signedEvent(t, "replay me")
	if err := store.Add(ev.ID.Hex(), ev); err != nil {
		t.Fatal(err)
	}

	var dispatched []string
	c := &NIP29GroupChannel{
		seen:    NewSeenCache(),
		ctx:     context.Background(),
		pending: store,
		onMsg:   func(m InboundMessage) { dispatched = append(dispatched, m.EventID) },
	}
	c.replayPending()
	if len(dispatched) != 1 || dispatched[0] != ev.ID.Hex() {
		t.Fatalf("replay should re-dispatch the pending event, got %v", dispatched)
	}
	// A second replay is deduped by the seen cache.
	dispatched = nil
	c.replayPending()
	if len(dispatched) != 0 {
		t.Errorf("already-seen event must not replay again, got %v", dispatched)
	}
}

func signedEvent(t *testing.T, content string) nostr.Event {
	t.Helper()
	sk := nostr.Generate()
	ev := nostr.Event{Kind: 9, Content: content, CreatedAt: 1000, Tags: nostr.Tags{{"h", "group1"}}}
	if err := ev.Sign(sk); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return ev
}

func TestPendingEventStore_AddRemovePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	s, err := NewPendingEventStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ev := signedEvent(t, "retry me")
	id := ev.ID.Hex()

	if err := s.Add(id, ev); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(id, ev); err != nil { // idempotent
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("len=%d, want 1", s.Len())
	}

	// A fresh store loads the persisted pending event (cross-restart replay).
	s2, err := NewPendingEventStore(path)
	if err != nil {
		t.Fatal(err)
	}
	pending := s2.Pending()
	if len(pending) != 1 || pending[0].ID.Hex() != id {
		t.Fatalf("reload pending = %v, want [%s]", pending, id)
	}
	if pending[0].Content != "retry me" {
		t.Errorf("reloaded content = %q, want 'retry me'", pending[0].Content)
	}

	// Remove persists; a later reload sees nothing.
	if err := s2.Remove(id); err != nil {
		t.Fatal(err)
	}
	s3, _ := NewPendingEventStore(path)
	if s3.Len() != 0 {
		t.Errorf("after remove+reload len=%d, want 0", s3.Len())
	}
}

func TestPendingEventStore_MultipleAndCorruptTolerant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	s, _ := NewPendingEventStore(path)
	a := signedEvent(t, "alpha")
	b := signedEvent(t, "beta")
	s.Add(a.ID.Hex(), a)
	s.Add(b.ID.Hex(), b)
	if s.Len() != 2 {
		t.Fatalf("len=%d, want 2", s.Len())
	}
	// Absent file / empty path handling.
	if _, err := NewPendingEventStore(""); err == nil {
		t.Error("empty path must error")
	}
	// A brand-new path is an empty store.
	fresh, _ := NewPendingEventStore(filepath.Join(t.TempDir(), "none.json"))
	if fresh.Len() != 0 {
		t.Error("absent file should yield empty store")
	}
}

func TestSanitizeChannelPathSegment(t *testing.T) {
	if got := SanitizeChannelPathSegment("relay.example.com'my-group"); got != "relay_example_com_my-group" {
		t.Errorf("sanitize = %q", got)
	}
	if got := SanitizeChannelPathSegment(""); got != "channel" {
		t.Errorf("empty -> %q, want channel", got)
	}
}
