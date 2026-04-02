package state

import (
	"encoding/json"
	"fmt"

	"metiq/internal/nostr/events"
	"metiq/internal/nostr/secure"
)

func ensureCodec(codec secure.EnvelopeCodec) secure.EnvelopeCodec {
	if codec == nil {
		return secure.NewPlaintextCodec()
	}
	return codec
}

func encodeEnvelopePayload(typ string, value any, codec secure.EnvelopeCodec) (string, error) {
	codec = ensureCodec(codec)
	body, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal %s payload: %w", typ, err)
	}
	payload, enc, err := codec.Encrypt(string(body))
	if err != nil {
		return "", fmt.Errorf("encrypt %s payload: %w", typ, err)
	}
	env := events.NewEnvelopeWithEncoding(typ, payload, enc)
	raw, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal %s envelope: %w", typ, err)
	}
	return string(raw), nil
}

func decodeEnvelopePayload(content string, out any, codec secure.EnvelopeCodec) error {
	codec = ensureCodec(codec)
	var env events.Envelope
	if err := json.Unmarshal([]byte(content), &env); err == nil && env.Payload != "" {
		plaintext, err := codec.Decrypt(env.Payload, env.Enc)
		if err != nil {
			return fmt.Errorf("decrypt envelope payload: %w", err)
		}
		if err := json.Unmarshal([]byte(plaintext), out); err != nil {
			return fmt.Errorf("decode envelope payload json: %w", err)
		}
		return nil
	}

	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("decode legacy payload: %w", err)
	}
	return nil
}
