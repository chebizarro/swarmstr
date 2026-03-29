package runtime

import (
	"errors"
	"testing"

	"fiatjaf.com/nostr"
)

func mustSecretKey(t *testing.T, skHex string) nostr.SecretKey {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("parse secret key: %v", err)
	}
	return sk
}

func mustPubKey(t *testing.T, sk nostr.SecretKey) nostr.PubKey {
	t.Helper()
	return nostr.GetPublicKey([32]byte(sk))
}

func TestDecryptNIP04RejectsSenderMismatch(t *testing.T) {
	recipient := mustSecretKey(t, "8f2a559490f4f35f4b2f8a8e02b2b3ec0ed0098f0d8b0f5e53f62f8c33f1f4a1")
	sender := mustSecretKey(t, "7d4d5ae5d62b37dd4ce1d85d17f9f5cc3a6f7d42b8f42ce1d0f615db2a0c2b83")
	wrongSender := mustSecretKey(t, "1c4c50d67b3f11a6c85aa9b9b97d3d5e4dcfc7f7d7f828948a1d1b57f96f0e2b")

	ciphertext, err := encryptNIP04(sender, mustPubKey(t, recipient), "hello from sender")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = decryptNIP04(recipient, mustPubKey(t, wrongSender), ciphertext)
	if err == nil {
		t.Fatal("expected sender mismatch to fail")
	}
	if !errors.Is(err, ErrInvalidPadding) && !errors.Is(err, ErrInvalidPlaintext) {
		t.Fatalf("expected padding/plaintext validation error, got %v", err)
	}
}

func TestPKCS7UnpadRejectsMalformedPadding(t *testing.T) {
	_, err := pkcs7Unpad([]byte("bad-padding\x02\x03"), 16)
	if !errors.Is(err, ErrInvalidPadding) {
		t.Fatalf("expected ErrInvalidPadding, got %v", err)
	}
}
