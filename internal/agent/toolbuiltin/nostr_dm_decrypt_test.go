package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
)

type stubDecryptKeyer struct{}

func (stubDecryptKeyer) GetPublicKey(context.Context) (nostr.PubKey, error) {
	return nostr.PubKey{}, nil
}
func (stubDecryptKeyer) SignEvent(context.Context, *nostr.Event) error { return nil }
func (stubDecryptKeyer) Encrypt(context.Context, string, nostr.PubKey) (string, error) {
	return "", nil
}
func (stubDecryptKeyer) Decrypt(_ context.Context, ciphertext string, _ nostr.PubKey) (string, error) {
	return "nip44:" + ciphertext, nil
}
func (stubDecryptKeyer) DecryptNIP04(_ context.Context, ciphertext string, _ nostr.PubKey) (string, error) {
	return "nip04:" + ciphertext, nil
}

func validSenderHex(t *testing.T) string {
	t.Helper()
	pk, err := testSigner(t).GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("get pubkey: %v", err)
	}
	return pk.Hex()
}

func TestNostrDMDecryptToolRequiresKeyer(t *testing.T) {
	tool := NostrDMDecryptTool(NostrToolOpts{})
	if _, err := tool(context.Background(), map[string]any{"ciphertext": "x", "sender_pubkey": validSenderHex(t)}); err == nil {
		t.Fatal("expected keyer error")
	}
}

func TestNostrDMDecryptToolDirectAutoUsesNIP44(t *testing.T) {
	tool := NostrDMDecryptTool(NostrToolOpts{Keyer: stubDecryptKeyer{}})
	out, err := tool(context.Background(), map[string]any{
		"ciphertext":    "abc",
		"sender_pubkey": validSenderHex(t),
	})
	if err != nil {
		t.Fatalf("tool error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if body["scheme"] != "nip44" || body["plaintext"] != "nip44:abc" {
		t.Fatalf("unexpected output: %#v", body)
	}
}

func TestNostrDMDecryptToolDirectNIP04(t *testing.T) {
	tool := NostrDMDecryptTool(NostrToolOpts{Keyer: stubDecryptKeyer{}})
	out, err := tool(context.Background(), map[string]any{
		"ciphertext":    "eHl6?iv=aXY=",
		"sender_pubkey": validSenderHex(t),
		"scheme":        "nip04",
	})
	if err != nil {
		t.Fatalf("tool error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if body["scheme"] != "nip04" || body["plaintext"] != "nip04:eHl6?iv=aXY=" {
		t.Fatalf("unexpected output: %#v", body)
	}
}

func TestNostrDMDecryptToolDirectNIP04_InvalidFormat(t *testing.T) {
	tool := NostrDMDecryptTool(NostrToolOpts{Keyer: stubDecryptKeyer{}})
	_, err := tool(context.Background(), map[string]any{
		"ciphertext":    "xyz",
		"sender_pubkey": validSenderHex(t),
		"scheme":        "nip04",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "nostr_dm_decrypt_error:") {
		t.Fatalf("expected machine-readable error prefix, got: %v", err)
	}
}

type invalidPaddingKeyer struct{ stubDecryptKeyer }

func (invalidPaddingKeyer) DecryptNIP04(_ context.Context, _ string, _ nostr.PubKey) (string, error) {
	return "", nostruntime.ErrInvalidPadding
}

func TestNostrDMDecryptToolDirectNIP04_InvalidPaddingSurfacesMachineReadableError(t *testing.T) {
	tool := NostrDMDecryptTool(NostrToolOpts{Keyer: invalidPaddingKeyer{}})
	_, err := tool(context.Background(), map[string]any{
		"ciphertext":    "eHl6?iv=aXYxMjM0NTY3ODkwMTIzNA==",
		"sender_pubkey": validSenderHex(t),
		"scheme":        "nip04",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "nostr_dm_decrypt_error:") {
		t.Fatalf("expected machine-readable error prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"code\":\"decrypt_failed\"") {
		t.Fatalf("expected decrypt_failed code, got: %v", err)
	}
}

func TestNostrDMDecryptToolEventKind14ReturnsContent(t *testing.T) {
	tool := NostrDMDecryptTool(NostrToolOpts{Keyer: stubDecryptKeyer{}})
	out, err := tool(context.Background(), map[string]any{
		"event": map[string]any{
			"kind":       14,
			"content":    "hello",
			"created_at": 1,
			"pubkey":     validSenderHex(t),
		},
	})
	if err != nil {
		t.Fatalf("tool error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if body["scheme"] != "nip17" || body["plaintext"] != "hello" {
		t.Fatalf("unexpected output: %#v", body)
	}
}
