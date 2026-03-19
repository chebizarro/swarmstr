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

func TestPoolOptsNIP42_NilKeyer(t *testing.T) {
	opts := PoolOptsNIP42(nil)
	if !opts.PenaltyBox {
		t.Fatal("expected PenaltyBox=true for nil keyer")
	}
	if opts.AuthRequiredHandler != nil {
		t.Fatal("expected nil AuthRequiredHandler for nil keyer")
	}
	if opts.RelayOptions.AuthHandler != nil {
		t.Fatal("expected nil AuthHandler for nil keyer")
	}
}

func TestPoolOptsNIP42_WithKeyer(t *testing.T) {
	keyer := &mockKeyer{}

	opts := PoolOptsNIP42(keyer)
	if !opts.PenaltyBox {
		t.Fatal("expected PenaltyBox=true")
	}
	if opts.AuthRequiredHandler == nil {
		t.Fatal("expected non-nil AuthRequiredHandler")
	}
	if opts.RelayOptions.AuthHandler == nil {
		t.Fatal("expected non-nil AuthHandler")
	}
}

func TestNewPoolNIP42_NilKeyer(t *testing.T) {
	pool := NewPoolNIP42(nil)
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
	pool.Close("test done")
}

func TestNewPoolNIP42_WithKeyer(t *testing.T) {
	pool := NewPoolNIP42(&mockKeyer{})
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
	pool.Close("test done")
}
