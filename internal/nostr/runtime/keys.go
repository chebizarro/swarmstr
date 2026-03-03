package runtime

import (
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

func ParseSecretKey(raw string) (nostr.SecretKey, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "nostr:"))
	if trimmed == "" {
		return nostr.SecretKey{}, fmt.Errorf("empty private key")
	}

	if strings.HasPrefix(trimmed, "nsec1") {
		prefix, value, err := nip19.Decode(trimmed)
		if err != nil {
			return nostr.SecretKey{}, fmt.Errorf("decode nsec: %w", err)
		}
		if prefix != "nsec" {
			return nostr.SecretKey{}, fmt.Errorf("expected nsec key, got %s", prefix)
		}
		sk, ok := value.(nostr.SecretKey)
		if !ok {
			return nostr.SecretKey{}, fmt.Errorf("unexpected nsec payload type")
		}
		return sk, nil
	}

	sk, err := nostr.SecretKeyFromHex(trimmed)
	if err != nil {
		return nostr.SecretKey{}, fmt.Errorf("parse hex secret key: %w", err)
	}
	return sk, nil
}

func PublicKeyHex(privateKey string) (string, error) {
	sk, err := ParseSecretKey(privateKey)
	if err != nil {
		return "", err
	}
	return sk.Public().Hex(), nil
}

func MustPublicKeyHex(privateKey string) string {
	pk, err := PublicKeyHex(privateKey)
	if err != nil {
		panic(err)
	}
	return pk
}

func ParsePubKey(raw string) (nostr.PubKey, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "nostr:"))
	if trimmed == "" {
		return nostr.PubKey{}, fmt.Errorf("empty pubkey")
	}

	if strings.HasPrefix(trimmed, "npub1") {
		prefix, value, err := nip19.Decode(trimmed)
		if err != nil {
			return nostr.PubKey{}, fmt.Errorf("decode npub: %w", err)
		}
		if prefix != "npub" {
			return nostr.PubKey{}, fmt.Errorf("expected npub key, got %s", prefix)
		}
		pk, ok := value.(nostr.PubKey)
		if !ok {
			return nostr.PubKey{}, fmt.Errorf("unexpected npub payload type")
		}
		return pk, nil
	}

	pk, err := nostr.PubKeyFromHex(trimmed)
	if err != nil {
		return nostr.PubKey{}, fmt.Errorf("parse hex pubkey: %w", err)
	}
	return pk, nil
}
