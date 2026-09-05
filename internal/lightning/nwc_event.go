package lightning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	nostr "fiatjaf.com/nostr"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// SignEvent avoids fiatjaf.com/nostr's unsafe string pointer arithmetic, which
// violates checkptr under Go 1.26 race builds. Upstream
// v0.0.0-20260902034142-316ef6591fa2 was re-audited on 2026-09-05 and still had
// byte-for-byte identical unsafe event.go code. Keep this serializer compatible
// with nostr.Event's NIP-01 serialization until the dependency guard test forces
// a fresh audit on the next bump.
func (k nwcKeyer) SignEvent(_ context.Context, event *nostr.Event) error {
	if event.Tags == nil {
		event.Tags = make(nostr.Tags, 0)
	}

	privateKey, publicKey := btcec.PrivKeyFromBytes(k.secret[:])
	event.PubKey = nostr.PubKey(publicKey.SerializeCompressed()[1:])
	event.ID = nwcEventID(*event)

	signature, err := schnorr.Sign(privateKey, event.ID[:], schnorr.FastSign())
	if err != nil {
		return err
	}
	copy(event.Sig[:], signature.Serialize())
	return nil
}

func validSignedNWCEvent(event nostr.Event) bool {
	if nwcEventID(event) != event.ID {
		return false
	}
	publicKey, err := schnorr.ParsePubKey(event.PubKey[:])
	if err != nil {
		return false
	}
	signature, err := schnorr.ParseSignature(event.Sig[:])
	if err != nil {
		return false
	}
	return signature.Verify(event.ID[:], publicKey)
}

func nwcEventID(event nostr.Event) nostr.ID {
	serialized := make([]byte, 0, 100+len(event.Content)+len(event.Tags)*80)
	serialized = append(serialized, `[0,"`...)

	var publicKeyHex [64]byte
	hex.Encode(publicKeyHex[:], event.PubKey[:])
	serialized = append(serialized, publicKeyHex[:]...)
	serialized = append(serialized, `",`...)
	serialized = strconv.AppendInt(serialized, int64(event.CreatedAt), 10)
	serialized = append(serialized, ',')
	serialized = strconv.AppendUint(serialized, uint64(event.Kind), 10)
	serialized = append(serialized, ',')
	serialized = append(serialized, '[')
	for i, tag := range event.Tags {
		if i > 0 {
			serialized = append(serialized, ',')
		}
		serialized = append(serialized, '[')
		for j, value := range tag {
			if j > 0 {
				serialized = append(serialized, ',')
			}
			serialized = appendNWCJSONString(serialized, value)
		}
		serialized = append(serialized, ']')
	}
	serialized = append(serialized, ']', ',')
	serialized = appendNWCJSONString(serialized, event.Content)
	serialized = append(serialized, ']')

	sum := sha256.Sum256(serialized)
	return nostr.ID(sum)
}

func appendNWCJSONString(dst []byte, value string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(value); i++ {
		var escaped byte
		switch value[i] {
		case '"':
			escaped = '"'
		case '\\':
			escaped = '\\'
		case '\n':
			escaped = 'n'
		case '\r':
			escaped = 'r'
		case '\t':
			escaped = 't'
		default:
			continue
		}
		dst = append(dst, value[start:i]...)
		dst = append(dst, '\\', escaped)
		start = i + 1
	}
	dst = append(dst, value[start:]...)
	return append(dst, '"')
}
