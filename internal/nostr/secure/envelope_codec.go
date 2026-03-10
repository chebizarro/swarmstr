package secure

import (
	"context"
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
	nostruntime "swarmstr/internal/nostr/runtime"
)

const (
	EncNone  = ""
	EncNIP44 = "nip44"
)

type EnvelopeCodec interface {
	Encrypt(plaintext string) (ciphertext string, enc string, err error)
	Decrypt(ciphertext string, enc string) (string, error)
}

type PlaintextCodec struct{}

func NewPlaintextCodec() PlaintextCodec {
	return PlaintextCodec{}
}

func (PlaintextCodec) Encrypt(plaintext string) (string, string, error) {
	return plaintext, EncNone, nil
}

func (PlaintextCodec) Decrypt(ciphertext string, enc string) (string, error) {
	if strings.TrimSpace(enc) != "" && strings.TrimSpace(enc) != EncNone {
		return "", fmt.Errorf("unsupported envelope encoding for plaintext codec: %s", enc)
	}
	return ciphertext, nil
}

type NIP44SelfCodec struct {
	keyer nostr.Keyer
	pub   nostr.PubKey
}

func NewNIP44SelfCodec(keyer nostr.Keyer) (*NIP44SelfCodec, error) {
	if keyer == nil {
		return nil, fmt.Errorf("nip44 self codec: signing keyer is required")
	}
	pk, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		return nil, fmt.Errorf("nip44 self codec: get public key: %w", err)
	}
	return &NIP44SelfCodec{keyer: keyer, pub: pk}, nil
}

func (c *NIP44SelfCodec) Encrypt(plaintext string) (string, string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", EncNIP44, fmt.Errorf("cannot encrypt empty plaintext")
	}
	ciphertext, err := c.keyer.Encrypt(context.Background(), plaintext, c.pub)
	if err != nil {
		return "", EncNIP44, fmt.Errorf("nip44 encrypt: %w", err)
	}
	return ciphertext, EncNIP44, nil
}

func (c *NIP44SelfCodec) Decrypt(ciphertext string, enc string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case EncNone:
		return ciphertext, nil
	case EncNIP44:
		plaintext, err := c.keyer.Decrypt(context.Background(), ciphertext, c.pub)
		if err != nil {
			return "", fmt.Errorf("nip44 decrypt: %w", err)
		}
		return plaintext, nil
	default:
		return "", fmt.Errorf("unsupported envelope encoding: %s", enc)
	}
}

// ─── NIP-44 peer codec ────────────────────────────────────────────────────────

// NIP44PeerCodec encrypts and decrypts messages for a specific remote pubkey.
// Outbound messages are encrypted for the peer; inbound messages from the peer
// are decrypted using the shared conversation key.
type NIP44PeerCodec struct {
	secret nostr.SecretKey
	peer   nostr.PubKey
}

// NewNIP44PeerCodec constructs a codec for communicating with peerPubKey.
// Both privateKey and peerPubKey are expected in hex format.
func NewNIP44PeerCodec(privateKey, peerPubKey string) (*NIP44PeerCodec, error) {
	sk, err := nostruntime.ParseSecretKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("nip44 peer codec: parse private key: %w", err)
	}
	pk, err := nostr.PubKeyFromHex(peerPubKey)
	if err != nil {
		return nil, fmt.Errorf("nip44 peer codec: parse peer pubkey %q: %w", peerPubKey, err)
	}
	return &NIP44PeerCodec{secret: sk, peer: pk}, nil
}

// Encrypt encrypts plaintext for the remote peer using NIP-44.
func (c *NIP44PeerCodec) Encrypt(plaintext string) (string, string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", EncNIP44, fmt.Errorf("cannot encrypt empty plaintext")
	}
	ck, err := nip44.GenerateConversationKey(c.peer, c.secret)
	if err != nil {
		return "", EncNIP44, fmt.Errorf("nip44 derive key: %w", err)
	}
	ciphertext, err := nip44.Encrypt(plaintext, ck)
	if err != nil {
		return "", EncNIP44, fmt.Errorf("nip44 encrypt: %w", err)
	}
	return ciphertext, EncNIP44, nil
}

// NewNIP44PeerCodecFromPrivKeys is a convenience constructor for symmetric
// setups where both parties' private keys are known locally (e.g. tests).
// It derives peerPubKey from peerPrivKey automatically.
func NewNIP44PeerCodecFromPrivKeys(localPrivKey, peerPrivKey string) (*NIP44PeerCodec, error) {
	peerSK, err := nostruntime.ParseSecretKey(peerPrivKey)
	if err != nil {
		return nil, fmt.Errorf("nip44 peer codec: parse peer private key: %w", err)
	}
	peerPub := peerSK.Public()
	peerPubHex := peerPub.Hex()
	return NewNIP44PeerCodec(localPrivKey, peerPubHex)
}

// PubKeyHexFromPrivKeyHex derives the x-only compressed public key hex string
// from a private key hex string.  Useful for configuration setup and tests.
func PubKeyHexFromPrivKeyHex(privateKeyHex string) (string, error) {
	sk, err := nostruntime.ParseSecretKey(privateKeyHex)
	if err != nil {
		return "", fmt.Errorf("derive pubkey: %w", err)
	}
	return sk.Public().Hex(), nil
}

// Decrypt decrypts a NIP-44 ciphertext received from the remote peer.
func (c *NIP44PeerCodec) Decrypt(ciphertext string, enc string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case EncNone:
		return ciphertext, nil
	case EncNIP44:
		ck, err := nip44.GenerateConversationKey(c.peer, c.secret)
		if err != nil {
			return "", fmt.Errorf("nip44 derive key: %w", err)
		}
		plaintext, err := nip44.Decrypt(ciphertext, ck)
		if err != nil {
			return "", fmt.Errorf("nip44 decrypt: %w", err)
		}
		return plaintext, nil
	default:
		return "", fmt.Errorf("unsupported encoding: %q", enc)
	}
}
