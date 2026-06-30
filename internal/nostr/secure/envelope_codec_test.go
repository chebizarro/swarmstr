package secure

import (
	"context"
	"encoding/base64"
	"strings"
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

func TestNIP44CiphertextStartsWithVersion2(t *testing.T) {
	codec, err := NewNIP44SelfCodec(newSecureTestKeyer(t))
	if err != nil {
		t.Fatalf("NewNIP44SelfCodec: %v", err)
	}
	ciphertext, enc, err := codec.Encrypt("version check")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if enc != EncNIP44 {
		t.Fatalf("enc = %q, want %q", enc, EncNIP44)
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(raw) == 0 || raw[0] != 0x02 {
		t.Fatalf("NIP-44 version byte = 0x%02x, want 0x02", raw[0])
	}
}

func TestNIP44RejectsShortBase64Payloads(t *testing.T) {
	codec, err := NewNIP44SelfCodec(newSecureTestKeyer(t))
	if err != nil {
		t.Fatalf("NewNIP44SelfCodec: %v", err)
	}
	if _, err := codec.Decrypt(base64.StdEncoding.EncodeToString([]byte{0x02}), EncNIP44); err == nil {
		t.Fatal("expected short base64 payload to fail")
	}
}

func TestNIP44PaddingBoundaries(t *testing.T) {
	local := "1111111111111111111111111111111111111111111111111111111111111111"
	peer := "2222222222222222222222222222222222222222222222222222222222222222"
	localCodec, err := NewNIP44PeerCodecFromPrivKeys(local, peer)
	if err != nil {
		t.Fatalf("local codec: %v", err)
	}
	peerCodec, err := NewNIP44PeerCodecFromPrivKeys(peer, local)
	if err != nil {
		t.Fatalf("peer codec: %v", err)
	}

	for _, n := range []int{65535, 65536, 65537} {
		t.Run("len", func(t *testing.T) {
			plaintext := strings.Repeat("a", n)
			ciphertext, enc, err := localCodec.Encrypt(plaintext)
			if err != nil {
				t.Fatalf("Encrypt len %d: %v", n, err)
			}
			got, err := peerCodec.Decrypt(ciphertext, enc)
			if err != nil {
				t.Fatalf("Decrypt len %d: %v", n, err)
			}
			if got != plaintext {
				t.Fatalf("round trip len %d produced len %d", n, len(got))
			}
			raw, err := base64.StdEncoding.DecodeString(ciphertext)
			if err != nil {
				t.Fatalf("base64 decode len %d: %v", n, err)
			}
			wantRawLen := 65 + 2 + 65536
			if n >= 65536 {
				wantRawLen = 65 + 6 + 65536
			}
			if n == 65537 {
				wantRawLen = 65 + 6 + 81920
			}
			if len(raw) != wantRawLen {
				t.Fatalf("raw payload len for plaintext %d = %d, want %d", n, len(raw), wantRawLen)
			}
		})
	}
}

func TestNIP44RejectsMACTamper(t *testing.T) {
	codec, err := NewNIP44SelfCodec(newSecureTestKeyer(t))
	if err != nil {
		t.Fatalf("NewNIP44SelfCodec: %v", err)
	}
	ciphertext, enc, err := codec.Encrypt("tamper me")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	if _, err := codec.Decrypt(base64.StdEncoding.EncodeToString(raw), enc); err == nil {
		t.Fatal("expected MAC tamper to fail")
	}
}

func TestNIP44RejectsUnsupportedVersionAndHashFlag(t *testing.T) {
	codec, err := NewNIP44SelfCodec(newSecureTestKeyer(t))
	if err != nil {
		t.Fatalf("NewNIP44SelfCodec: %v", err)
	}
	ciphertext, enc, err := codec.Encrypt("version tamper")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	raw[0] = 0x03
	if _, err := codec.Decrypt(base64.StdEncoding.EncodeToString(raw), enc); err == nil {
		t.Fatal("expected unsupported version to fail")
	}
	if _, err := codec.Decrypt("#future", enc); err == nil {
		t.Fatal("expected # flag to fail as unsupported version")
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
