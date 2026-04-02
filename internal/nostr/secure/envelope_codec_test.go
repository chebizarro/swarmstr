package secure

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip44"
)

type secureTestKeyer struct {
	keyer.KeySigner
	sk nostr.SecretKey
}

func newSecureTestKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("SecretKeyFromHex: %v", err)
	}
	return secureTestKeyer{KeySigner: keyer.NewPlainKeySigner([32]byte(sk)), sk: sk}
}

func (k secureTestKeyer) Encrypt(_ context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(recipient, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}

func (k secureTestKeyer) Decrypt(_ context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(sender, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, ck)
}

func TestMutableSelfEnvelopeCodecToggle(t *testing.T) {
	codec, err := NewMutableSelfEnvelopeCodec(newSecureTestKeyer(t), true)
	if err != nil {
		t.Fatalf("NewMutableSelfEnvelopeCodec: %v", err)
	}

	ciphertext, enc, err := codec.Encrypt(`{"secret":"value"}`)
	if err != nil {
		t.Fatalf("Encrypt encrypted: %v", err)
	}
	if enc != EncNIP44 {
		t.Fatalf("Encrypt encoding = %q, want %q", enc, EncNIP44)
	}
	if plaintext, err := codec.Decrypt(ciphertext, enc); err != nil || plaintext != `{"secret":"value"}` {
		t.Fatalf("Decrypt encrypted = %q, %v", plaintext, err)
	}

	codec.SetEncrypt(false)
	plaintext, enc, err := codec.Encrypt(`{"secret":"value"}`)
	if err != nil {
		t.Fatalf("Encrypt plaintext: %v", err)
	}
	if enc != EncNone || plaintext != `{"secret":"value"}` {
		t.Fatalf("plaintext mode = %q %q", enc, plaintext)
	}
	if decrypted, err := codec.Decrypt(ciphertext, EncNIP44); err != nil || decrypted != `{"secret":"value"}` {
		t.Fatalf("Decrypt legacy encrypted = %q, %v", decrypted, err)
	}
}

func TestMutableSelfEnvelopeCodecDecryptUnknownLegacyEncodingFallsBackToPlaintext(t *testing.T) {
	codec, err := NewMutableSelfEnvelopeCodec(newSecureTestKeyer(t), true)
	if err != nil {
		t.Fatalf("NewMutableSelfEnvelopeCodec: %v", err)
	}
	plaintext, err := codec.Decrypt(`{"legacy":"value"}`, "legacy-plain")
	if err != nil {
		t.Fatalf("Decrypt legacy plaintext: %v", err)
	}
	if plaintext != `{"legacy":"value"}` {
		t.Fatalf("Decrypt legacy plaintext = %q", plaintext)
	}
}
