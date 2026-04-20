package config

import "metiq/internal/nostr/events"

// BootstrapConfig is local-only startup config needed before Nostr state can be fetched.
type BootstrapConfig struct {
	PrivateKey                      string   `json:"private_key"`
	Relays                          []string `json:"relays"`
	SignerURL                       string   `json:"signer_url,omitempty"`
	ControlSignerURL                string   `json:"control_signer_url,omitempty"`
	ControlTargetPubKey             string   `json:"control_target_pubkey,omitempty"`
	ConfigAddress                   string   `json:"config_address,omitempty"`
	StateKind                       int      `json:"state_kind,omitempty"`
	TranscriptKind                  int      `json:"transcript_kind,omitempty"`
	EnableNIP44                     bool     `json:"enable_nip44"`
	EnableNIP17                     bool     `json:"enable_nip17"`
	EnableAIHubKinds                bool     `json:"enable_ai_hub_kinds"`
	AdminListenAddr                 string   `json:"admin_listen_addr,omitempty"`
	AdminToken                      string   `json:"admin_token,omitempty"`
	GatewayWSListenAddr             string   `json:"gateway_ws_listen_addr,omitempty"`
	GatewayWSToken                  string   `json:"gateway_ws_token,omitempty"`
	GatewayWSPath                   string   `json:"gateway_ws_path,omitempty"`
	GatewayWSAllowedOrigins         []string `json:"gateway_ws_allowed_origins,omitempty"`
	GatewayWSTrustedProxies         []string `json:"gateway_ws_trusted_proxies,omitempty"`
	GatewayWSAllowInsecureControlUI bool     `json:"gateway_ws_allow_insecure_control_ui,omitempty"`

	// ContextWindowSize, when non-zero, overrides the model-registry value for
	// all sessions. Units: tokens. 0 = auto-detect from model ID.
	ContextWindowSize int `json:"context_window_size,omitempty"`

	// ModelContextOverrides maps model name patterns (case-insensitive prefix)
	// to context window sizes in tokens. Merged with the built-in registry at
	// daemon startup. Example: {"phi-3": 4096, "my-finetuned-llama": 8192}
	ModelContextOverrides map[string]int `json:"model_context_overrides,omitempty"`

	// FIPSEnabled activates the experimental FIPS mesh transport at the
	// bootstrap level. The agent will attempt to connect to the local FIPS
	// daemon on startup. Requires the experimental_fips build tag.
	FIPSEnabled bool `json:"fips_enabled,omitempty"`

	// FIPSControlSocket overrides the default FIPS daemon control socket path.
	FIPSControlSocket string `json:"fips_control_socket,omitempty"`
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
