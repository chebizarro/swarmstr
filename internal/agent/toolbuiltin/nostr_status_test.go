package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/nostr/nip38"
)

func TestNostrStatusTool_NilHeartbeat(t *testing.T) {
	tool := NostrStatusTool(NostrStatusToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"status": "idle",
	})
	if err == nil {
		t.Fatal("expected error with nil heartbeat")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %q, expected 'not configured'", err.Error())
	}
}

func TestNostrStatusTool_InvalidStatus(t *testing.T) {
	tool := NostrStatusTool(NostrStatusToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"status": "dancing",
	})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("error = %q, expected 'invalid status'", err.Error())
	}
}

func TestNostrStatusTool_EmptyDefaultsToIdle(t *testing.T) {
	// With no heartbeat, validation runs first. Empty status is valid (maps to idle).
	// But then nil heartbeat check triggers — confirms validation passed.
	tool := NostrStatusTool(NostrStatusToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"status": "",
	})
	if err == nil {
		t.Fatal("expected error (nil heartbeat)")
	}
	// Error should be about heartbeat, not about validation.
	if strings.Contains(err.Error(), "invalid status") {
		t.Error("empty status should default to idle, not fail validation")
	}
}

func TestNostrStatusTool_ValidStatuses(t *testing.T) {
	validStatuses := []string{
		nip38.StatusIdle,
		nip38.StatusTyping,
		nip38.StatusUpdating,
		nip38.StatusDND,
		nip38.StatusOffline,
	}
	for _, s := range validStatuses {
		tool := NostrStatusTool(NostrStatusToolOpts{})
		_, err := tool(context.Background(), map[string]any{
			"status": s,
		})
		// Should fail on nil heartbeat, NOT on validation.
		if err == nil {
			t.Fatalf("expected error for status %q (nil heartbeat)", s)
		}
		if strings.Contains(err.Error(), "invalid status") {
			t.Errorf("status %q should be valid, got validation error", s)
		}
	}
}

func TestNostrStatusTool_WithHeartbeat(t *testing.T) {
	// Create a no-op heartbeat (Enabled=false).
	hb, err := nip38.NewHeartbeat(context.Background(), nip38.HeartbeatOptions{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("NewHeartbeat: %v", err)
	}
	defer hb.Stop()

	tool := NostrStatusTool(NostrStatusToolOpts{Heartbeat: hb})
	out, err := tool(context.Background(), map[string]any{
		"status":  "typing",
		"content": "working on it",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if result["ok"] != true {
		t.Error("expected ok=true")
	}
	if result["status"] != "typing" {
		t.Errorf("status = %v, want 'typing'", result["status"])
	}
	if result["content"] != "working on it" {
		t.Errorf("content = %v", result["content"])
	}
}

func TestNostrStatusTool_WithExpiry(t *testing.T) {
	hb, _ := nip38.NewHeartbeat(context.Background(), nip38.HeartbeatOptions{Enabled: false})
	defer hb.Stop()

	tool := NostrStatusTool(NostrStatusToolOpts{Heartbeat: hb})
	out, err := tool(context.Background(), map[string]any{
		"status":         "dnd",
		"expiry_seconds": float64(3600),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if result["expires_at"] == nil {
		t.Error("expected expires_at to be set")
	}
}
