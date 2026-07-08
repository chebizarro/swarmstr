package runtime

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
)

// mockKeyer implements nostr.Keyer for testing purposes.
type mockKeyer struct{}

func (m *mockKeyer) GetPublicKey(ctx context.Context) (nostr.PubKey, error) {
	return nostr.PubKey{}, nil
}
func (m *mockKeyer) SignEvent(ctx context.Context, evt *nostr.Event) error { return nil }
func (m *mockKeyer) Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return "", nil
}
func (m *mockKeyer) Decrypt(ctx context.Context, base64ciphertext string, sender nostr.PubKey) (string, error) {
	return "", nil
}
func (m *mockKeyer) Nip04Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return "", nil
}
func (m *mockKeyer) Nip04Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return "", nil
}

func TestNewPoolNIP42_NilKeyer(t *testing.T) {
	pool := NewPoolNIP42(nil)
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
	if pool.AuthRequiredHandler != nil {
		t.Fatal("expected nil AuthRequiredHandler for nil keyer")
	}
	if pool.RelayOptions.AuthHandler != nil {
		t.Fatal("expected nil AuthHandler for nil keyer")
	}
	pool.Close("test done")
}

func TestNewPoolNIP42_WithKeyer(t *testing.T) {
	pool := NewPoolNIP42(&mockKeyer{})
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
	if pool.AuthRequiredHandler == nil {
		t.Fatal("expected non-nil AuthRequiredHandler")
	}
	if pool.RelayOptions.AuthHandler == nil {
		t.Fatal("expected non-nil AuthHandler")
	}
	pool.Close("test done")
}
