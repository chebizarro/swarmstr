package channels

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/store/state"
)

// ─── applyJitter ─────────────────────────────────────────────────────────────

func TestApplyJitter(t *testing.T) {
	since := nostr.Timestamp(1000)
	got := applyJitter(since, 30*time.Second)
	if got != 970 {
		t.Errorf("expected 970, got %d", got)
	}

	// Clamp to zero
	got = applyJitter(nostr.Timestamp(10), 60*time.Second)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}

	// Zero jitter
	got = applyJitter(since, 0)
	if got != 1000 {
		t.Errorf("expected 1000, got %d", got)
	}
}

// ─── SeenCache ───────────────────────────────────────────────────────────────

func TestSeenCache_BasicDedup(t *testing.T) {
	cache := NewSeenCache()

	// First add — not duplicate
	if cache.Add("evt-1") {
		t.Error("first add should not be duplicate")
	}
	if cache.Len() != 1 {
		t.Errorf("len: %d", cache.Len())
	}

	// Second add — duplicate
	if !cache.Add("evt-1") {
		t.Error("second add should be duplicate")
	}

	// Different event
	if cache.Add("evt-2") {
		t.Error("new event should not be duplicate")
	}
	if cache.Len() != 2 {
		t.Errorf("len: %d", cache.Len())
	}
}

// ─── channelConfigToMap ──────────────────────────────────────────────────────

func TestChannelConfigToMap(t *testing.T) {
	cfg := state.NostrChannelConfig{
		Kind:         "telegram",
		Enabled:      true,
		GroupAddress: "grp-1",
		ChannelID:    "ch-1",
		Relays:       []string{"wss://r1"},
		AgentID:      "agent-1",
		Tags:         map[string][]string{"t": {"t1"}},
		Config:       map[string]any{"token": "tok"},
	}
	m := channelConfigToMap(cfg)
	if m["kind"] != "telegram" {
		t.Errorf("kind: %v", m["kind"])
	}
	if m["group_address"] != "grp-1" {
		t.Errorf("group_address: %v", m["group_address"])
	}
	if m["token"] != "tok" {
		t.Errorf("token: %v", m["token"])
	}
}

func TestChannelConfigToMap_Minimal(t *testing.T) {
	cfg := state.NostrChannelConfig{Kind: "test", Enabled: false}
	m := channelConfigToMap(cfg)
	if m["kind"] != "test" {
		t.Errorf("kind: %v", m["kind"])
	}
	if _, ok := m["group_address"]; ok {
		t.Error("group_address should not be set")
	}
}

// ─── cloneNostrFilter ────────────────────────────────────────────────────────

func TestCloneNostrFilter(t *testing.T) {
	original := nostr.Filter{
		Kinds: []nostr.Kind{1},
		Tags:  nostr.TagMap{"p": {"pk1", "pk2"}},
	}
	clone := cloneNostrFilter(original)

	// Verify values match
	if len(clone.Kinds) != 1 || clone.Kinds[0] != 1 {
		t.Errorf("kinds: %v", clone.Kinds)
	}
	if len(clone.Tags["p"]) != 2 {
		t.Errorf("tags: %v", clone.Tags)
	}

	// Verify independence (modifying clone doesn't affect original)
	clone.Kinds[0] = 99
	if original.Kinds[0] == 99 {
		t.Error("clone should be independent")
	}
}

func TestCloneNostrFilter_Empty(t *testing.T) {
	clone := cloneNostrFilter(nostr.Filter{})
	if len(clone.Kinds) != 0 {
		t.Errorf("kinds: %v", clone.Kinds)
	}
}

// ─── TypingKeepalive ─────────────────────────────────────────────────────────

func TestTypingKeepalive_StartStop(t *testing.T) {
	var calls atomic.Int32
	send := func(ctx context.Context, durationMS int) error {
		calls.Add(1)
		return nil
	}

	ka := NewTypingKeepalive(send, 50*time.Millisecond, 2*time.Second, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ka.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	ka.Stop()

	if c := calls.Load(); c < 2 {
		t.Errorf("expected at least 2 calls, got %d", c)
	}
}

func TestTypingKeepalive_Defaults(t *testing.T) {
	ka := NewTypingKeepalive(func(ctx context.Context, d int) error { return nil }, 0, 0, 0)
	if ka.interval != 3*time.Second {
		t.Errorf("interval: %v", ka.interval)
	}
	if ka.maxTTL != 60*time.Second {
		t.Errorf("maxTTL: %v", ka.maxTTL)
	}
	if ka.maxFails != 2 {
		t.Errorf("maxFails: %d", ka.maxFails)
	}
}

// ─── ExtensionHandle ─────────────────────────────────────────────────────────

type fakeHandle struct{}

func (f fakeHandle) ID() string                                       { return "fake-ch" }
func (f fakeHandle) Type() string                                     { return "test" }
func (f fakeHandle) Send(ctx context.Context, text string) error      { return nil }
func (f fakeHandle) Close()                                           {}

func TestExtensionHandle(t *testing.T) {
	h := &ExtensionHandle{handle: fakeHandle{}}
	if h.ID() != "fake-ch" {
		t.Errorf("ID: %q", h.ID())
	}
	if h.Type() != "extension" {
		t.Errorf("Type: %q", h.Type())
	}
	if err := h.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	h.Close() // should not panic
}
