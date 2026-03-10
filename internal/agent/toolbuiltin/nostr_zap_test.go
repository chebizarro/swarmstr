package toolbuiltin

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func testSigner(t *testing.T) nostr.Keyer {
	t.Helper()
	skHex := "8f2a559490f4f35f4b2f8a8e02b2b3ec0ed0098f0d8b0f5e53f62f8c33f1f4a1"
	sk, err := nostr.SecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("parse generated key: %v", err)
	}
	return keyer.NewPlainKeySigner([32]byte(sk))
}

func TestNostrZapSendTool_NoKey(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{})
	_, err := tool(context.Background(), map[string]any{
		"to_pubkey":   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"lud16":       "alice@wallet.example.com",
		"amount_sats": float64(100),
	})
	if err == nil {
		t.Fatal("expected error with no keyer")
	}
}

func TestNostrZapSendTool_MissingToPubkey(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{Keyer: testSigner(t)})
	_, err := tool(context.Background(), map[string]any{
		"lud16":       "alice@wallet.example.com",
		"amount_sats": float64(100),
	})
	if err == nil {
		t.Fatal("expected error with missing to_pubkey")
	}
}

func TestNostrZapSendTool_MissingLud16(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{Keyer: testSigner(t)})
	_, err := tool(context.Background(), map[string]any{
		"to_pubkey":   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"amount_sats": float64(100),
	})
	if err == nil {
		t.Fatal("expected error with missing lud16")
	}
}

func TestNostrZapSendTool_ZeroAmount(t *testing.T) {
	tool := NostrZapSendTool(NostrToolOpts{Keyer: testSigner(t)})
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
