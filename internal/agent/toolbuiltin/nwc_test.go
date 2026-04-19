package toolbuiltin

import (
	"context"
	"slices"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip44"

	"metiq/internal/agent"
)

// testNWCKeyer builds a nostr.Keyer for NWC tests.
func testNWCKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	skHex := "1111111111111111111111111111111111111111111111111111111111111111"
	sk, err := nostr.SecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("SecretKeyFromHex: %v", err)
	}
	return nwcTestKeyer{KeySigner: keyer.NewPlainKeySigner([32]byte(sk)), sk: sk}
}

type nwcTestKeyer struct {
	keyer.KeySigner
	sk nostr.SecretKey
}

func (k nwcTestKeyer) Encrypt(_ context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(recipient, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}

func (k nwcTestKeyer) Decrypt(_ context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(sender, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, ck)
}

// helper: execute a named tool on the registry.
func execTool(t *testing.T, reg *agent.ToolRegistry, name string, args map[string]any) (string, error) {
	t.Helper()
	return reg.Execute(context.Background(), agent.ToolCall{Name: name, Args: args})
}

// ── parseNWCUri tests ─────────────────────���─────────────────────────────────

func TestParseNWCUri_Valid(t *testing.T) {
	uri := "nostrwalletconnect://abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234?relay=wss://relay.example.com&secret=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	conn, err := parseNWCUri(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn.walletPubkey != "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234" {
		t.Errorf("unexpected wallet pubkey: %s", conn.walletPubkey)
	}
	if conn.secret != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("unexpected secret: %s", conn.secret)
	}
	if len(conn.relays) != 1 || conn.relays[0] != "wss://relay.example.com" {
		t.Errorf("unexpected relays: %v", conn.relays)
	}
}

func TestParseNWCUri_AlternateScheme(t *testing.T) {
	uri := "nostr+walletconnect://abc123?relay=wss://r1.example.com&relay=wss://r2.example.com"
	conn, err := parseNWCUri(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn.walletPubkey != "abc123" {
		t.Errorf("unexpected wallet pubkey: %s", conn.walletPubkey)
	}
	if len(conn.relays) != 2 {
		t.Errorf("expected 2 relays, got %d", len(conn.relays))
	}
	if conn.secret != "" {
		t.Errorf("expected empty secret, got %s", conn.secret)
	}
}

func TestParseNWCUri_NwcScheme(t *testing.T) {
	uri := "nwc://abc123?relay=wss://relay.example.com"
	conn, err := parseNWCUri(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn.walletPubkey != "abc123" {
		t.Errorf("unexpected wallet pubkey: %s", conn.walletPubkey)
	}
}

func TestParseNWCUri_Empty(t *testing.T) {
	_, err := parseNWCUri("")
	if err == nil {
		t.Fatal("expected error for empty URI")
	}
}

func TestParseNWCUri_BadScheme(t *testing.T) {
	_, err := parseNWCUri("http://not-a-wallet")
	if err == nil {
		t.Fatal("expected error for invalid scheme")
	}
}

func TestParseNWCUri_MissingPubkey(t *testing.T) {
	_, err := parseNWCUri("nostrwalletconnect://?relay=wss://relay.example.com")
	if err == nil {
		t.Fatal("expected error for missing wallet pubkey")
	}
}

// ── newNWCKeyer tests ──────────────────────────────────────────────────���────

func TestNewNWCKeyer_Valid(t *testing.T) {
	k, err := newNWCKeyer("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pk, err := k.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	if pk.Hex() == "" {
		t.Error("expected non-empty pubkey")
	}
}

func TestNewNWCKeyer_Invalid(t *testing.T) {
	_, err := newNWCKeyer("not-a-hex-key")
	if err == nil {
		t.Fatal("expected error for invalid secret")
	}
}

func TestNewNWCKeyer_EncryptDecryptRoundTrip(t *testing.T) {
	k, err := newNWCKeyer("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Get our own pubkey for self-encryption test.
	pk, _ := k.GetPublicKey(context.Background())

	original := `{"method":"get_balance","params":{}}`
	encrypted, err := k.Encrypt(context.Background(), original, pk)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	decrypted, err := k.Decrypt(context.Background(), encrypted, pk)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if decrypted != original {
		t.Errorf("round-trip mismatch: got %q, want %q", decrypted, original)
	}
}

// ── Tool registration tests ────────────────────────────────────────────────

func TestRegisterNWCTools_AllRegistered(t *testing.T) {
	reg := agent.NewToolRegistry()
	RegisterNWCTools(reg, NWCToolOpts{
		Keyer:  testNWCKeyer(t),
		NWCUri: "nostrwalletconnect://abcd1234?relay=wss://relay.example.com&secret=1111111111111111111111111111111111111111111111111111111111111111",
	})

	expected := []string{
		"nwc_get_balance",
		"nwc_pay_invoice",
		"nwc_make_invoice",
		"nwc_lookup_invoice",
		"nwc_list_transactions",
	}
	registered := reg.List()
	for _, name := range expected {
		if !slices.Contains(registered, name) {
			t.Errorf("tool %q not registered; got %v", name, registered)
		}
	}
}

func TestNWCTools_NoUri(t *testing.T) {
	reg := agent.NewToolRegistry()
	RegisterNWCTools(reg, NWCToolOpts{
		Keyer: testNWCKeyer(t),
	})

	// Tools should be registered (they return helpful config error).
	registered := reg.List()
	if !slices.Contains(registered, "nwc_get_balance") {
		t.Fatal("expected nwc_get_balance to be registered even without URI")
	}

	// Calling without a URI should return a config error, not panic.
	_, err := execTool(t, reg, "nwc_get_balance", map[string]any{})
	if err == nil {
		t.Fatal("expected error when calling without NWC URI configured")
	}
}

func TestNWCPayInvoice_MissingInvoice(t *testing.T) {
	reg := agent.NewToolRegistry()
	RegisterNWCTools(reg, NWCToolOpts{
		Keyer:  testNWCKeyer(t),
		NWCUri: "nostrwalletconnect://abcd1234?relay=wss://relay.example.com&secret=1111111111111111111111111111111111111111111111111111111111111111",
	})

	_, err := execTool(t, reg, "nwc_pay_invoice", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing invoice")
	}
}

func TestNWCMakeInvoice_MissingAmount(t *testing.T) {
	reg := agent.NewToolRegistry()
	RegisterNWCTools(reg, NWCToolOpts{
		Keyer:  testNWCKeyer(t),
		NWCUri: "nostrwalletconnect://abcd1234?relay=wss://relay.example.com&secret=1111111111111111111111111111111111111111111111111111111111111111",
	})

	_, err := execTool(t, reg, "nwc_make_invoice", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing amount_msats")
	}
}

func TestNWCLookupInvoice_MissingIdentifier(t *testing.T) {
	reg := agent.NewToolRegistry()
	RegisterNWCTools(reg, NWCToolOpts{
		Keyer:  testNWCKeyer(t),
		NWCUri: "nostrwalletconnect://abcd1234?relay=wss://relay.example.com&secret=1111111111111111111111111111111111111111111111111111111111111111",
	})

	_, err := execTool(t, reg, "nwc_lookup_invoice", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing payment_hash and invoice")
	}
}

func TestNWCTools_NoRelays(t *testing.T) {
	reg := agent.NewToolRegistry()
	RegisterNWCTools(reg, NWCToolOpts{
		Keyer:  testNWCKeyer(t),
		NWCUri: "nostrwalletconnect://abcd1234?secret=1111111111111111111111111111111111111111111111111111111111111111",
		// No relays in URI or opts.
	})

	_, err := execTool(t, reg, "nwc_get_balance", map[string]any{})
	if err == nil {
		t.Fatal("expected error when no relays configured")
	}
}
