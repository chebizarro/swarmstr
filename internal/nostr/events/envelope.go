package events

import "time"

// Envelope wraps state payloads to support schema evolution and encryption metadata.
type Envelope struct {
	Version int            `json:"v"`
	Type    string         `json:"type"`
	Enc     string         `json:"enc,omitempty"`
	Payload string         `json:"payload"`
	Meta    map[string]any `json:"meta,omitempty"`
}

func NewEnvelope(typ, payload string, encrypted bool) Envelope {
	enc := ""
	if encrypted {
		enc = "nip44"
	}
	return NewEnvelopeWithEncoding(typ, payload, enc)
}

func NewEnvelopeWithEncoding(typ, payload, enc string) Envelope {
	env := Envelope{
		Version: 1,
		Type:    typ,
		Payload: payload,
		Meta: map[string]any{
			"created_at_unix": time.Now().Unix(),
		},
	}
	if enc != "" {
		env.Enc = enc
	}
	return env
}
