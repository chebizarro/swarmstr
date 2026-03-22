package toolbuiltin

import (
	"context"
	"fmt"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
)

func TestNostrWatchTool_MissingName(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{"session_id": "s1"})
	if err == nil {
		t.Fatal("expected error with missing name")
	}
}

func TestNostrWatchTool_MissingSessionID(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{"name": "test"})
	if err == nil {
		t.Fatal("expected error with missing session_id")
	}
}

func TestNostrWatchTool_MissingFilter(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{
		"name":       "test",
		"session_id": "s1",
	})
	if err == nil {
		t.Fatal("expected error with missing filter")
	}
}

func TestNostrWatchTool_InvalidSessionIDType(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{Relays: []string{"wss://example.com"}}, reg, nil)
	_, err := tool(context.Background(), map[string]any{
		"name":       "test",
		"session_id": float64(123),
		"filter":     map[string]any{"kinds": []any{float64(1)}},
	})
	if err == nil {
		t.Fatal("expected error with non-string session_id")
	}
}

func TestNostrWatchTool_NoRelays(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchTool(NostrToolOpts{}, reg, nil)
	_, err := tool(context.Background(), map[string]any{
		"name":       "test",
		"session_id": "s1",
		"filter":     map[string]any{"kinds": []any{float64(1)}},
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestNostrUnwatchTool_MissingName(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrUnwatchTool(reg)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing name")
	}
}

func TestNostrUnwatchTool_NotFound(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrUnwatchTool(reg)
	_, err := tool(context.Background(), map[string]any{"name": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown watch name")
	}
}

func TestNostrWatchListTool_Empty(t *testing.T) {
	reg := NewWatchRegistry()
	tool := NostrWatchListTool(reg)
	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "[]" {
		t.Fatalf("expected empty array, got %q", out)
	}
}

func TestWatchRegistry_MaxWatches(t *testing.T) {
	reg := NewWatchRegistry()
	// Fill up to the max — use a long TTL so goroutines don't self-clean before the overflow check.
	for i := 0; i < maxActiveWatches; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := reg.start(ctx, NostrToolOpts{}, func() string {
			return fmt.Sprintf("watch%d", i)
		}(), "s1",
			nostrFilterEmpty(), nil, []string{"wss://unreachable.example.com"}, false,
			time.Hour, 0, func(_, _ string, _ map[string]any) {})
		if err != nil {
			t.Fatalf("entry %d: unexpected error: %v", i, err)
		}
	}
	// One more should fail.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := reg.start(ctx, NostrToolOpts{}, "overflow", "s1",
		nostrFilterEmpty(), nil, []string{"wss://unreachable.example.com"}, false,
		time.Hour, 0, func(_, _ string, _ map[string]any) {})
	if err == nil {
		t.Fatal("expected error when max watches exceeded")
	}
}

// nostrFilterEmpty returns an empty nostr.Filter for testing.
func nostrFilterEmpty() nostr.Filter {
	f, _ := buildNostrFilter(nil, 10)
	return f
}

func TestWatchSeenSetDedupAndEviction(t *testing.T) {
	s := newWatchSeenSet(3)

	if s.Add("a") {
		t.Fatal("first add of a should not be duplicate")
	}
	if s.Add("b") {
		t.Fatal("first add of b should not be duplicate")
	}
	if s.Add("c") {
		t.Fatal("first add of c should not be duplicate")
	}
	if !s.Add("a") {
		t.Fatal("second add of a should be duplicate")
	}

	if s.Add("d") {
		t.Fatal("first add of d should not be duplicate")
	}

	if s.Add("a") {
		t.Fatal("a should have been evicted and re-add should not be duplicate")
	}
}

// ─── Lifecycle / rebind tests ─────────────────────────────────────────────────

func TestApplyWatchSinceJitterBackdates(t *testing.T) {
	now := nostr.Timestamp(time.Now().Unix())
	f := nostr.Filter{Since: now}
	jittered := applyWatchSinceJitter(f)
	expected := now - nostr.Timestamp(watchSinceJitter.Seconds())
	if jittered.Since != expected {
		t.Fatalf("expected since=%d, got %d", expected, jittered.Since)
	}
}

func TestApplyWatchSinceJitterClampsToZero(t *testing.T) {
	f := nostr.Filter{Since: nostr.Timestamp(10)} // very small
	jittered := applyWatchSinceJitter(f)
	if jittered.Since != 0 {
		t.Fatalf("expected since clamped to 0, got %d", jittered.Since)
	}
}

func TestApplyWatchSinceJitterNoOpWhenZero(t *testing.T) {
	f := nostr.Filter{Since: 0}
	jittered := applyWatchSinceJitter(f)
	if jittered.Since != 0 {
		t.Fatalf("expected since to remain 0, got %d", jittered.Since)
	}
}

func TestWatchEntryRebindChannelNonBlocking(t *testing.T) {
	e := &watchEntry{
		rebindCh: make(chan struct{}, 1),
	}
	// First signal should succeed.
	select {
	case e.rebindCh <- struct{}{}:
	default:
		t.Fatal("expected rebind signal to succeed")
	}
	// Second signal should not block (buffered channel, already full).
	select {
	case e.rebindCh <- struct{}{}:
		t.Fatal("expected rebind channel to be full")
	default:
	}
}

func TestWatchRegistryRebindRelaysUpdatesEntries(t *testing.T) {
	reg := NewWatchRegistry()

	// Create a fake entry directly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entry := &watchEntry{
		name:     "test-watch",
		relays:   []string{"wss://old-relay"},
		rebindCh: make(chan struct{}, 1),
		cancel:   cancel,
	}
	reg.mu.Lock()
	reg.entries["test-watch"] = entry
	reg.mu.Unlock()

	newRelays := []string{"wss://new-a", "wss://new-b"}
	reg.RebindRelays(newRelays)

	// Verify relay list updated.
	entry.relaysMu.RLock()
	got := make([]string, len(entry.relays))
	copy(got, entry.relays)
	entry.relaysMu.RUnlock()

	if len(got) != 2 || got[0] != "wss://new-a" || got[1] != "wss://new-b" {
		t.Fatalf("unexpected relays after RebindRelays: %v", got)
	}

	// Verify rebind signal was sent.
	select {
	case <-entry.rebindCh:
	default:
		t.Fatal("expected rebind signal after RebindRelays")
	}

	_ = ctx
}

func TestWatchRegistryRebindRelaysCoalesces(t *testing.T) {
	reg := NewWatchRegistry()

	entry := &watchEntry{
		name:     "test-watch",
		relays:   []string{"wss://old"},
		rebindCh: make(chan struct{}, 1),
		cancel:   func() {},
	}
	reg.mu.Lock()
	reg.entries["test-watch"] = entry
	reg.mu.Unlock()

	// Multiple RebindRelays calls should not block.
	for i := 0; i < 5; i++ {
		reg.RebindRelays([]string{fmt.Sprintf("wss://relay-%d", i)})
	}

	// Only one signal queued.
	select {
	case <-entry.rebindCh:
	default:
		t.Fatal("expected at least one rebind signal")
	}
	select {
	case <-entry.rebindCh:
		t.Fatal("expected only one coalesced rebind signal")
	default:
	}

	// Last relay set should be applied.
	entry.relaysMu.RLock()
	got := entry.relays
	entry.relaysMu.RUnlock()
	if len(got) != 1 || got[0] != "wss://relay-4" {
		t.Fatalf("expected last relay set, got %v", got)
	}
}

func TestWatchRegistrySetHubFunc(t *testing.T) {
	reg := NewWatchRegistry()

	// Before setting, hub() should return nil.
	if reg.hub() != nil {
		t.Fatal("expected nil hub before SetHubFunc")
	}

	// After setting a nil-returning func, still nil.
	reg.SetHubFunc(func() *nostruntime.NostrHub { return nil })
	if reg.hub() != nil {
		t.Fatal("expected nil hub when func returns nil")
	}
}

func TestWatchRegistrySpecsIncludesReboundRelays(t *testing.T) {
	reg := NewWatchRegistry()

	entry := &watchEntry{
		name:      "test-watch",
		sessionID: "s1",
		relays:    []string{"wss://original"},
		filterRaw: map[string]any{"kinds": []any{float64(1)}},
		createdAt: time.Now(),
		deadline:  time.Now().Add(time.Hour),
		ttlSec:    3600,
		rebindCh:  make(chan struct{}, 1),
		cancel:    func() {},
	}
	reg.mu.Lock()
	reg.entries["test-watch"] = entry
	reg.mu.Unlock()

	// Rebind relays.
	reg.RebindRelays([]string{"wss://rebound-a", "wss://rebound-b"})

	// Specs should reflect the new relays.
	specs := reg.Specs()
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	if len(specs[0].Relays) != 2 || specs[0].Relays[0] != "wss://rebound-a" {
		t.Fatalf("expected rebound relays in spec, got %v", specs[0].Relays)
	}
}

func TestWatchSeenSetMinimumCapacity(t *testing.T) {
	s := newWatchSeenSet(0)

	if s.Add("x") {
		t.Fatal("first add of x should not be duplicate")
	}
	if !s.Add("x") {
		t.Fatal("second add of x should be duplicate")
	}
	if s.Add("y") {
		t.Fatal("first add of y should not be duplicate")
	}
	if s.Add("x") {
		t.Fatal("x should have been evicted at capacity 1")
	}
}
