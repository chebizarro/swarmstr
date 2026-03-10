package toolbuiltin

import (
	"context"
	"testing"
)

func TestNostrFollowsTool_NoRelays(t *testing.T) {
	tool := NostrFollowsTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestNostrFollowsTool_MissingPubkey(t *testing.T) {
	tool := NostrFollowsTool(NostrToolOpts{Relays: []string{"wss://example.com"}})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing pubkey")
	}
}

func TestNostrFollowersTool_NoRelays(t *testing.T) {
	tool := NostrFollowersTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestNostrWotDistanceTool_MissingFrom(t *testing.T) {
	tool := NostrWotDistanceTool(NostrToolOpts{Relays: []string{"wss://example.com"}})
	_, err := tool(context.Background(), map[string]any{
		"to_pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with missing from_pubkey")
	}
}

func TestNostrWotDistanceTool_MissingTo(t *testing.T) {
	tool := NostrWotDistanceTool(NostrToolOpts{Relays: []string{"wss://example.com"}})
	_, err := tool(context.Background(), map[string]any{
		"from_pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with missing to_pubkey")
	}
}

func TestNostrWotDistanceTool_NoRelays(t *testing.T) {
	tool := NostrWotDistanceTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"from_pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"to_pubkey":   "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestStringArg(t *testing.T) {
	args := map[string]any{"key": "value", "num": float64(42)}
	if got := stringArg(args, "key"); got != "value" {
		t.Errorf("want 'value', got %q", got)
	}
	if got := stringArg(args, "num"); got != "" {
		t.Errorf("want empty string for non-string, got %q", got)
	}
	if got := stringArg(args, "missing"); got != "" {
		t.Errorf("want empty string for missing key, got %q", got)
	}
}
