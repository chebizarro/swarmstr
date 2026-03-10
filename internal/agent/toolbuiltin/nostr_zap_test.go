package toolbuiltin

import (
	"context"
	"testing"
)

func TestNostrZapSendTool_NoKey(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"to_pubkey":   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"lud16":       "alice@wallet.example.com",
		"amount_sats": float64(100),
	})
	if err == nil {
		t.Fatal("expected error with no private key")
	}
}

func TestNostrZapSendTool_MissingToPubkey(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{PrivateKey: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"})
	_, err := tool(context.Background(), map[string]any{
		"lud16":       "alice@wallet.example.com",
		"amount_sats": float64(100),
	})
	if err == nil {
		t.Fatal("expected error with missing to_pubkey")
	}
}

func TestNostrZapSendTool_MissingLud16(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{PrivateKey: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"})
	_, err := tool(context.Background(), map[string]any{
		"to_pubkey":   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"amount_sats": float64(100),
	})
	if err == nil {
		t.Fatal("expected error with missing lud16")
	}
}

func TestNostrZapSendTool_ZeroAmount(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{PrivateKey: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"})
	_, err := tool(context.Background(), map[string]any{
		"to_pubkey":   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"lud16":       "alice@wallet.example.com",
		"amount_sats": float64(0),
	})
	if err == nil {
		t.Fatal("expected error with zero amount")
	}
}

func TestNostrZapListTool_NoRelays(t *testing.T) {
	tool := NostrZapListTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"pubkey": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err == nil {
		t.Fatal("expected error with no relays")
	}
}

func TestNostrZapListTool_MissingPubkey(t *testing.T) {
	tool := NostrZapListTool(NostrToolOpts{Relays: []string{"wss://example.com"}})
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error with missing pubkey")
	}
}
