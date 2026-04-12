package nip38

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

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
	sk := nostr.Generate()
	kr := keyer.NewPlainKeySigner(sk)
	_, err := NewHeartbeat(context.Background(), HeartbeatOptions{
		Enabled: true,
		Keyer:   &kr,
		Relays:  nil,
	})
	if err == nil {
		t.Fatal("expected error for empty relays")
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
