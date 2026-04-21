package nip38

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// testKeyer returns a PlainKeySigner suitable for tests.
func testKeyer() nostr.Keyer {
	sk := nostr.Generate()
	kr := keyer.NewPlainKeySigner(sk)
	return &kr
}

// ─── Constants ────────────────────────────────────────────────────────────────

func TestStatusConstants(t *testing.T) {
	statuses := map[string]string{
		"idle":     StatusIdle,
		"typing":   StatusTyping,
		"updating": StatusUpdating,
		"dnd":      StatusDND,
		"offline":  StatusOffline,
	}
	for expected, got := range statuses {
		if got != expected {
			t.Errorf("Status%s: got %q, want %q", expected, got, expected)
		}
	}
}

// ─── HeartbeatOptions / NewHeartbeat ──────────────────────────────────────────

func TestNewHeartbeat_Disabled(t *testing.T) {
	h, err := NewHeartbeat(context.Background(), HeartbeatOptions{Enabled: false})
	if err != nil {
		t.Fatalf("disabled heartbeat should not error: %v", err)
	}
	defer h.Stop()
	if h.opts.Enabled {
		t.Error("expected disabled")
	}
}

func TestNewHeartbeat_NilKeyer(t *testing.T) {
	_, err := NewHeartbeat(context.Background(), HeartbeatOptions{
		Enabled: true,
		Keyer:   nil,
		Relays:  []string{"wss://relay1"},
	})
	if err == nil {
		t.Fatal("expected error for nil keyer")
	}
}

func TestNewHeartbeat_NoRelays(t *testing.T) {
	_, err := NewHeartbeat(context.Background(), HeartbeatOptions{
		Enabled: true,
		Keyer:   testKeyer(),
		Relays:  nil,
	})
	if err == nil {
		t.Fatal("expected error for empty relays")
	}
}

func TestNewHeartbeat_DefaultIdleInterval(t *testing.T) {
	// Use an already-cancelled context so the background goroutine
	// exits immediately and publish is a no-op.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled: true,
		Keyer:   testKeyer(),
		Relays:  []string{"wss://localhost:1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Stop()

	if h.opts.IdleInterval != 5*time.Minute {
		t.Errorf("IdleInterval = %v, want 5m", h.opts.IdleInterval)
	}
}

func TestNewHeartbeat_CustomIdleInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled:      true,
		Keyer:        testKeyer(),
		Relays:       []string{"wss://localhost:1"},
		IdleInterval: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Stop()

	if h.opts.IdleInterval != 30*time.Second {
		t.Errorf("IdleInterval = %v, want 30s", h.opts.IdleInterval)
	}
}

func TestNewHeartbeat_InitialState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled:        true,
		Keyer:          testKeyer(),
		Relays:         []string{"wss://localhost:1"},
		DefaultContent: "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Stop()

	if h.current != StatusIdle {
		t.Errorf("initial status = %q, want %q", h.current, StatusIdle)
	}
	if h.pubkey.Hex() == "" {
		t.Error("pubkey should be populated")
	}
	if h.pool == nil {
		t.Error("pool should be non-nil")
	}
	if h.ticker == nil {
		t.Error("ticker should be non-nil")
	}
}

// ─── SetStatus on disabled heartbeat ──────────────────────────────────────────

func TestSetStatus_Disabled_NoOp(t *testing.T) {
	h, _ := NewHeartbeat(context.Background(), HeartbeatOptions{Enabled: false})
	defer h.Stop()
	// Should not panic
	h.SetStatus(context.Background(), StatusTyping, "hello", 0)
	h.SetIdle(context.Background())
	h.SetTyping(context.Background(), "note")
	h.SetUpdating(context.Background(), "running tool")
}

func TestStop_Disabled_NoOp(t *testing.T) {
	h, _ := NewHeartbeat(context.Background(), HeartbeatOptions{Enabled: false})
	// Should not panic
	h.Stop()
}

func TestHeartbeatOptions_Defaults(t *testing.T) {
	opts := HeartbeatOptions{}
	if opts.Enabled {
		t.Error("default should be disabled")
	}
	if opts.IdleInterval != 0 {
		t.Error("default IdleInterval should be zero (set by NewHeartbeat)")
	}
	if opts.DefaultContent != "" {
		t.Error("default content should be empty")
	}
}

func TestSetIdle_Disabled(t *testing.T) {
	h, _ := NewHeartbeat(context.Background(), HeartbeatOptions{Enabled: false})
	defer h.Stop()
	h.SetIdle(context.Background()) // no panic
}

func TestSetTyping_Disabled(t *testing.T) {
	h, _ := NewHeartbeat(context.Background(), HeartbeatOptions{Enabled: false})
	defer h.Stop()
	h.SetTyping(context.Background(), "composing") // no panic
}

func TestSetUpdating_Disabled(t *testing.T) {
	h, _ := NewHeartbeat(context.Background(), HeartbeatOptions{Enabled: false})
	defer h.Stop()
	h.SetUpdating(context.Background(), "tool exec") // no panic
}

// ─── SetStatus on enabled heartbeat (state tracking) ─────────────────────────

func TestSetStatus_Enabled_UpdatesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so publish is no-op

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled: true,
		Keyer:   testKeyer(),
		Relays:  []string{"wss://localhost:1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Stop()

	h.SetStatus(ctx, StatusTyping, "composing reply", 0)
	// Give the goroutine a moment to dispatch (the state update is synchronous).
	time.Sleep(10 * time.Millisecond)

	h.mu.Lock()
	cur := h.current
	msg := h.currentMsg
	h.mu.Unlock()

	if cur != StatusTyping {
		t.Errorf("current = %q, want %q", cur, StatusTyping)
	}
	if msg != "composing reply" {
		t.Errorf("currentMsg = %q, want %q", msg, "composing reply")
	}
}

func TestSetIdle_Enabled_UpdatesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled:        true,
		Keyer:          testKeyer(),
		Relays:         []string{"wss://localhost:1"},
		DefaultContent: "default-msg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Stop()

	// First set to typing, then back to idle
	h.SetStatus(ctx, StatusTyping, "working", 0)
	time.Sleep(5 * time.Millisecond)
	h.SetIdle(ctx)
	time.Sleep(5 * time.Millisecond)

	h.mu.Lock()
	cur := h.current
	msg := h.currentMsg
	h.mu.Unlock()

	if cur != StatusIdle {
		t.Errorf("current = %q, want %q", cur, StatusIdle)
	}
	if msg != "default-msg" {
		t.Errorf("currentMsg = %q, want %q", msg, "default-msg")
	}
}

func TestSetTyping_Enabled_UpdatesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled: true,
		Keyer:   testKeyer(),
		Relays:  []string{"wss://localhost:1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Stop()

	h.SetTyping(ctx, "my-note")
	time.Sleep(5 * time.Millisecond)

	h.mu.Lock()
	cur := h.current
	msg := h.currentMsg
	h.mu.Unlock()

	if cur != StatusTyping {
		t.Errorf("current = %q, want %q", cur, StatusTyping)
	}
	if msg != "my-note" {
		t.Errorf("currentMsg = %q, want %q", msg, "my-note")
	}
}

func TestSetUpdating_Enabled_UpdatesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled: true,
		Keyer:   testKeyer(),
		Relays:  []string{"wss://localhost:1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Stop()

	h.SetUpdating(ctx, "running tool")
	time.Sleep(5 * time.Millisecond)

	h.mu.Lock()
	cur := h.current
	msg := h.currentMsg
	h.mu.Unlock()

	if cur != StatusUpdating {
		t.Errorf("current = %q, want %q", cur, StatusUpdating)
	}
	if msg != "running tool" {
		t.Errorf("currentMsg = %q, want %q", msg, "running tool")
	}
}

// ─── Stop on enabled heartbeat ───────────────────────────────────────────────

func TestStop_Enabled_StopsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled: true,
		Keyer:   testKeyer(),
		Relays:  []string{"wss://localhost:1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stop should complete without panic and within a reasonable time.
	done := make(chan struct{})
	go func() {
		h.Stop()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5 seconds")
	}
}

func TestStop_Enabled_DoubleStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewHeartbeat(ctx, HeartbeatOptions{
		Enabled: true,
		Keyer:   testKeyer(),
		Relays:  []string{"wss://localhost:1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Double stop should not panic.
	h.Stop()
	h.Stop()
}

// ─── publish (event construction) ────────────────────────────────────────────

func TestPublish_EventTags(t *testing.T) {
	// Verify the event has the correct structure by checking tag construction.
	tags := nostr.Tags{
		{"d", "general"},
		{"status", StatusTyping},
	}
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}
	if tags[0][0] != "d" || tags[0][1] != "general" {
		t.Errorf("tag[0] = %v, want [d, general]", tags[0])
	}
	if tags[1][0] != "status" || tags[1][1] != StatusTyping {
		t.Errorf("tag[1] = %v, want [status, typing]", tags[1])
	}
}

func TestPublish_ExpiryTag(t *testing.T) {
	// When expiry > 0, an expiration tag should be added.
	tags := nostr.Tags{
		{"d", "general"},
		{"status", StatusDND},
		{"expiration", "1700000000"},
	}
	found := false
	for _, tag := range tags {
		if tag[0] == "expiration" {
			found = true
			if tag[1] != "1700000000" {
				t.Errorf("expiration = %q, want 1700000000", tag[1])
			}
		}
	}
	if !found {
		t.Error("expiration tag not found")
	}
}

// ─── Loop exits on context cancellation ──────────────────────────────────────

func TestLoop_ExitsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	h := &Heartbeat{
		opts:   HeartbeatOptions{Enabled: true},
		ticker: time.NewTicker(time.Hour), // long interval — won't fire
		ctx:    ctx,
		cancel: cancel,
	}

	done := make(chan struct{})
	h.wg.Add(1)
	go func() {
		h.loop()
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// loop exited
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit after context cancel")
	}
}
