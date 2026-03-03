package secure

import (
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
	secret nostr.SecretKey
	pub    nostr.PubKey
}

func NewNIP44SelfCodec(privateKey string) (*NIP44SelfCodec, error) {
	sk, err := nostruntime.ParseSecretKey(privateKey)
	if err != nil {
		return nil, err
	}
	return &NIP44SelfCodec{secret: sk, pub: sk.Public()}, nil
}

func (c *NIP44SelfCodec) Encrypt(plaintext string) (string, string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", EncNIP44, fmt.Errorf("cannot encrypt empty plaintext")
	}
	ck, err := nip44.GenerateConversationKey(c.pub, c.secret)
	if err != nil {
		return "", EncNIP44, fmt.Errorf("derive nip44 key: %w", err)
	}
	ciphertext, err := nip44.Encrypt(plaintext, ck)
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
		ck, err := nip44.GenerateConversationKey(c.pub, c.secret)
		if err != nil {
			return "", fmt.Errorf("derive nip44 key: %w", err)
		}
		plaintext, err := nip44.Decrypt(ciphertext, ck)
		if err != nil {
			return "", fmt.Errorf("nip44 decrypt: %w", err)
		}
		return plaintext, nil
	default:
		return "", fmt.Errorf("unsupported envelope encoding: %s", enc)
	}
}
