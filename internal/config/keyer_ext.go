package config

import (
	"context"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip04"
)

// ExtendedSigner wraps keyer.KeySigner and adds NIP-04 decrypt support.
// It satisfies nostr.Keyer and the NIP04Decrypter interface used by DMBus
// so callers never need to handle raw private keys outside of this package.
type ExtendedSigner struct {
	keyer.KeySigner
	sk nostr.SecretKey
}

// NewExtendedSigner creates an ExtendedSigner from a parsed secret key.
func NewExtendedSigner(sk nostr.SecretKey) *ExtendedSigner {
	return &ExtendedSigner{
		KeySigner: keyer.NewPlainKeySigner([32]byte(sk)),
		sk:        sk,
	}
}

// DecryptNIP04 decrypts a NIP-04 (kind:4) ciphertext from sender using
// AES-CBC + ECDH shared secret.  This is distinct from the Keyer.Decrypt
// method which uses NIP-44 (ChaCha20-Poly1305).
func (s *ExtendedSigner) DecryptNIP04(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	shared, err := nip04.ComputeSharedSecret(sender, [32]byte(s.sk))
	if err != nil {
		return "", err
	}
	return nip04.Decrypt(ciphertext, shared)
}

// EncryptNIP04 encrypts a plaintext for recipient using NIP-04 AES-CBC.
func (s *ExtendedSigner) EncryptNIP04(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	shared, err := nip04.ComputeSharedSecret(recipient, [32]byte(s.sk))
	if err != nil {
		return "", err
	}
	return nip04.Encrypt(plaintext, shared)
}
