package config

import "swarmstr/internal/nostr/events"

// BootstrapConfig is local-only startup config needed before Nostr state can be fetched.
type BootstrapConfig struct {
	PrivateKey       string   `json:"private_key"`
	Relays           []string `json:"relays"`
	SignerURL        string   `json:"signer_url,omitempty"`
	ConfigAddress    string   `json:"config_address,omitempty"`
	StateKind        int      `json:"state_kind,omitempty"`
	TranscriptKind   int      `json:"transcript_kind,omitempty"`
	EnableNIP44      bool     `json:"enable_nip44"`
	EnableAIHubKinds bool     `json:"enable_ai_hub_kinds"`
	AdminListenAddr  string   `json:"admin_listen_addr,omitempty"`
	AdminToken       string   `json:"admin_token,omitempty"`
}

func (c BootstrapConfig) EffectiveStateKind() events.Kind {
	if c.StateKind > 0 {
		return events.Kind(c.StateKind)
	}
	return events.KindStateDoc
}

func (c BootstrapConfig) EffectiveTranscriptKind() events.Kind {
	if c.TranscriptKind > 0 {
		return events.Kind(c.TranscriptKind)
	}
	return events.KindTranscriptDoc
}
