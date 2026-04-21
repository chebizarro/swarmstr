package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type ConfigDoc struct {
	Version       int                 `json:"version"`
	DM            DMPolicy            `json:"dm"`
	Relays        RelayPolicy         `json:"relays"`
	Agent         AgentPolicy         `json:"agent"`
	Control       ControlPolicy       `json:"control,omitempty"`
	ACP           ACPConfig           `json:"acp,omitempty"`
	Agents        AgentsConfig        `json:"agents,omitempty"`
	NostrChannels NostrChannelsConfig `json:"nostr_channels,omitempty"`
	Providers     ProvidersConfig     `json:"providers,omitempty"`
	Session       SessionConfig       `json:"session,omitempty"`
	Storage       StorageConfig       `json:"storage,omitempty"`
	Heartbeat     HeartbeatConfig     `json:"heartbeat,omitempty"`
	TTS           TTSConfig           `json:"tts,omitempty"`
	Secrets       SecretsConfig       `json:"secrets,omitempty"`
	CronCfg       CronConfig          `json:"cron,omitempty"`
	Hooks         HooksConfig         `json:"hooks,omitempty"`
	Timeouts      TimeoutsConfig      `json:"timeouts,omitempty"`
	AgentList     *AgentListConfig    `json:"agent_list,omitempty"`
	FIPS          FIPSConfig          `json:"fips,omitempty"`
	Extra         map[string]any      `json:"extra,omitempty"`
}

// AgentListConfig controls Strand's own NIP-51 kind:30000 agent list publishing.
type AgentListConfig struct {
	DTag     string `json:"d"`               // d-tag identifier (e.g. "cascadia-agents")
	Relay    string `json:"relay,omitempty"` // optional hint relay for publishing
	AutoSync bool   `json:"auto_sync"`       // publish on startup + when peers change
}

// Hash returns a stable SHA-256 hex digest of the ConfigDoc's JSON serialization.
// This is used for optimistic concurrency control (base_hash) in config.put/set.
// Fields not serialised to JSON (unexported) are excluded.
func (c ConfigDoc) Hash() string {
	// Use a temp struct without the hash field to avoid recursive hashing.
	data, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (c ConfigDoc) StorageEncryptEnabled() bool {
	return c.Storage.EncryptEnabled()
}

func (c ConfigDoc) DMReplyScheme() string {
	return c.DM.ReplySchemeMode()
}

func (c ConfigDoc) ACPTransportMode() string {
	return c.ACP.TransportMode()
}

func BoolPtr(v bool) *bool {
	return &v
}

// ACPConfig controls outbound ACP DM transport selection.
type ACPConfig struct {
	// Transport selects which DM family ACP uses when sending tasks/results.
	// Supported values: auto, nip17, nip04, fips.
	Transport string `json:"transport,omitempty"`
}

// FIPSConfig holds configuration for experimental FIPS mesh transport.
// FIPS (Free Internetworking Peering System) is a self-organizing mesh network
// that uses Nostr keypairs as native node identities, enabling direct
// peer-to-peer agent communication without relay dependency.
//
// All FIPS functionality requires the experimental_fips build tag.
type FIPSConfig struct {
	// Enabled activates the FIPS transport. Requires experimental_fips build tag
	// and a persistent identity (nsec must be set in bootstrap config).
	Enabled bool `json:"enabled"`

	// ControlSocket is the path to the FIPS daemon's control socket.
	// Used to query mesh state (peer reachability, sessions, bloom filters).
	// If empty, searches default paths: $XDG_RUNTIME_DIR/fips/control.sock,
	// /run/fips/control.sock, /tmp/fips-control.sock.
	ControlSocket string `json:"control_socket,omitempty"`

	// AgentPort is the FSP port for agent-to-agent messages over the mesh.
	// The agent listens on this port (bound to its fd00::/8 address) for
	// incoming DMs, ACP tasks, and fleet messages. Default: 1337.
	AgentPort int `json:"agent_port,omitempty"`

	// ControlPort is the FSP port for control RPC over FIPS. Default: 1338.
	ControlPort int `json:"control_port,omitempty"`

	// TransportPref controls routing priority when both FIPS and relay
	// transports are available.
	//   fips-first  — try FIPS, fall back to relay (default)
	//   relay-first — use relay by default, FIPS for tagged peers only
	//   fips-only   — FIPS mesh only, no relay fallback
	TransportPref string `json:"transport_pref,omitempty"`

	// Peers is a list of static FIPS peer npubs for mesh bootstrapping.
	// These are in addition to any peers discovered via the fleet directory.
	Peers []string `json:"peers,omitempty"`

	// ConnTimeout is the connection timeout for FIPS sends. Default: "5s".
	ConnTimeout string `json:"conn_timeout,omitempty"`

	// ReachCacheTTL is the TTL for FIPS reachability cache entries. Default: "30s".
	ReachCacheTTL string `json:"reach_cache_ttl,omitempty"`
}

// EffectiveAgentPort returns the configured agent port or the default (1337).
func (f FIPSConfig) EffectiveAgentPort() int {
	if f.AgentPort > 0 {
		return f.AgentPort
	}
	return 1337
}

// EffectiveControlPort returns the configured control port or the default (1338).
func (f FIPSConfig) EffectiveControlPort() int {
	if f.ControlPort > 0 {
		return f.ControlPort
	}
	return 1338
}

// ParseFIPSTransportPref normalizes a configured FIPS transport preference.
func ParseFIPSTransportPref(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "fips-first":
		return "fips-first", true
	case "relay-first":
		return "relay-first", true
	case "fips-only":
		return "fips-only", true
	default:
		return "", false
	}
}

// EffectiveTransportPref returns the normalized transport preference.
func (f FIPSConfig) EffectiveTransportPref() string {
	if pref, ok := ParseFIPSTransportPref(f.TransportPref); ok {
		return pref
	}
	return "fips-first"
}

// ParseACPTransportMode normalizes a configured ACP transport mode.
func ParseACPTransportMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto", true
	case "nip17", "nip-17":
		return "nip17", true
	case "nip04", "nip-04":
		return "nip04", true
	case "fips":
		return "fips", true
	default:
		return "", false
	}
}

func (c ACPConfig) TransportMode() string {
	if mode, ok := ParseACPTransportMode(c.Transport); ok {
		return mode
	}
	return "auto"
}

type DMPolicy struct {
	Policy         string             `json:"policy"` // pairing|allowlist|open|disabled
	ReplyScheme    string             `json:"reply_scheme,omitempty"`
	AllowFrom      []string           `json:"allow_from,omitempty"`
	AllowFromLists []AllowFromListRef `json:"allow_from_lists,omitempty"`
}

// ParseDMReplyScheme normalizes a configured DM reply scheme.
func ParseDMReplyScheme(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto", true
	case "nip17", "nip-17":
		return "nip17", true
	case "nip04", "nip-04":
		return "nip04", true
	default:
		return "", false
	}
}

func (p DMPolicy) ReplySchemeMode() string {
	if mode, ok := ParseDMReplyScheme(p.ReplyScheme); ok {
		return mode
	}
	return "auto"
}

// AllowFromListRef references a NIP-51 kind:30000 list whose "p" tags are
// merged into the DM allowlist at runtime.
type AllowFromListRef struct {
	Pubkey string `json:"pubkey"`          // hex or npub of the list owner
	D      string `json:"d"`               // d-tag identifier (e.g. "cascadia-agents")
	Relay  string `json:"relay,omitempty"` // optional hint relay
}

type RelayPolicy struct {
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
}

type AgentPolicy struct {
	DefaultModel     string         `json:"default_model,omitempty"`
	Thinking         string         `json:"thinking,omitempty"`
	Verbose          string         `json:"verbose,omitempty"`
	DefaultAutonomy  AutonomyMode   `json:"default_autonomy,omitempty"`
	DefaultAuthority *TaskAuthority `json:"default_authority,omitempty"`
}

// EffectiveDefaultAutonomy returns the configured default autonomy mode,
// falling back to AutonomyFull when not specified.
func (a AgentPolicy) EffectiveDefaultAutonomy() AutonomyMode {
	if a.DefaultAutonomy != "" && a.DefaultAutonomy.Valid() {
		return a.DefaultAutonomy
	}
	return AutonomyFull
}

// EffectiveDefaultAuthority returns the configured default authority,
// or builds one from the effective autonomy mode.
func (a AgentPolicy) EffectiveDefaultAuthority() TaskAuthority {
	if a.DefaultAuthority != nil {
		return *a.DefaultAuthority
	}
	return DefaultAuthority(a.EffectiveDefaultAutonomy())
}

type ControlPolicy struct {
	RequireAuth         bool           `json:"require_auth,omitempty"`
	AllowUnauthMethods  []string       `json:"allow_unauth_methods,omitempty"`
	Admins              []ControlAdmin `json:"admins,omitempty"`
	LegacyTokenFallback bool           `json:"legacy_token_fallback,omitempty"`
}

type ControlAdmin struct {
	PubKey  string   `json:"pubkey"`
	Methods []string `json:"methods,omitempty"`
}

// ── Typed config sections ──────────────────────────────────────────────────────

// ProviderEntry holds per-provider settings (API key, base URL, default model…).
// Unknown fields are preserved in Extra so new OpenClaw provider keys survive round-trips.
type ProviderEntry struct {
	Enabled bool           `json:"enabled,omitempty"`
	APIKey  string         `json:"api_key,omitempty"`  // redacted on read; first key used by default
	APIKeys []string       `json:"api_keys,omitempty"` // multi-key pool for round-robin rotation
	BaseURL string         `json:"base_url,omitempty"`
	Model   string         `json:"model,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

// ProvidersConfig is a map of named provider configurations.
// Keys are provider identifiers such as "openai", "anthropic", "ollama".
type ProvidersConfig map[string]ProviderEntry

// SessionConfig controls per-session behaviour.
type SessionConfig struct {
	TTLSeconds int `json:"ttl_seconds,omitempty"`
	// PruneAfterDays deletes transcript entries for sessions whose last
	// activity is older than this many days.  0 = disabled.
	PruneAfterDays int `json:"prune_after_days,omitempty"`
	// PruneOnBoot runs a pruning pass at daemon startup.
	PruneOnBoot bool `json:"prune_on_boot,omitempty"`
}

// StorageConfig controls how relay-persisted state documents are stored.
type StorageConfig struct {
	// Encrypt enables NIP-44 self-encryption for config, transcript, memory,
	// and other relay-persisted state documents.
	Encrypt *bool `json:"encrypt,omitempty"`
}

func (s StorageConfig) EncryptEnabled() bool {
	return s.Encrypt == nil || *s.Encrypt
}

// HeartbeatConfig controls the LLM heartbeat runner schedule.
// This is distinct from NIP-38 presence/status publishing, which lives in Extra.
type HeartbeatConfig struct {
	Enabled    bool `json:"enabled,omitempty"`
	IntervalMS int  `json:"interval_ms,omitempty"`
}

// AgentHeartbeatConfig holds per-agent overrides for LLM heartbeat turns.
type AgentHeartbeatConfig struct {
	Model string `json:"model,omitempty"`
}

// TTSConfig controls text-to-speech.
type TTSConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"`
	Voice    string `json:"voice,omitempty"`
}

// SecretsConfig holds named secret references (values are intentionally opaque).
// The map key is a secret name; the value is a reference path (e.g. env var name).
type SecretsConfig map[string]string

// CronConfig holds top-level cron scheduler settings.
type CronConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	// JobTimeoutSecs is the maximum wall-clock seconds a single cron job may
	// run before it is cancelled.  Default: 300 (5 minutes).
	JobTimeoutSecs int `json:"job_timeout_secs,omitempty"`
}

// TimeoutsConfig holds configurable timeout defaults for operations that are
// not scoped to a single agent.  All values are in seconds; zero means
// "use the built-in default".  These map to the configurable knobs that
// OpenClaw exposes across its config schema.
type TimeoutsConfig struct {
	// SessionMemoryExtractionSecs is the timeout for LLM-based session memory
	// extraction.  Default: 45.
	SessionMemoryExtractionSecs int `json:"session_memory_extraction_secs,omitempty"`
	// SessionCompactSummarySecs is the timeout for session context compaction.
	// Default: 30.
	SessionCompactSummarySecs int `json:"session_compact_summary_secs,omitempty"`
	// GrepSearchSecs is the timeout for grep/rg search operations.
	// Default: 30.
	GrepSearchSecs int `json:"grep_search_secs,omitempty"`
	// ImageFetchSecs is the timeout for image URL downloads.
	// Default: 30.
	ImageFetchSecs int `json:"image_fetch_secs,omitempty"`
	// ToolChainExecSecs is the timeout for chained tool execution.
	// Default: 120.
	ToolChainExecSecs int `json:"tool_chain_exec_secs,omitempty"`
	// GitOpsSecs is the timeout for git subprocess operations.
	// Default: 15.
	GitOpsSecs int `json:"git_ops_secs,omitempty"`
	// LLMProviderHTTPSecs is the default HTTP client timeout for LLM provider
	// API calls (Anthropic, OpenAI, Gemini, Cohere).  Default: 120.
	LLMProviderHTTPSecs int `json:"llm_provider_http_secs,omitempty"`
	// WebhookWakeSecs is the timeout for webhook wake operations.
	// Default: 30.
	WebhookWakeSecs int `json:"webhook_wake_secs,omitempty"`
	// WebhookAgentStartSecs is the timeout for starting an agent from a
	// webhook.  Default: 120.
	WebhookAgentStartSecs int `json:"webhook_agent_start_secs,omitempty"`
	// SignerConnectSecs is the timeout for NWC/NIP-46 signer connection.
	// Default: 30.
	SignerConnectSecs int `json:"signer_connect_secs,omitempty"`
	// MemoryPersistSecs is the timeout for writing memory entries to the
	// store.  Default: 30.
	MemoryPersistSecs int `json:"memory_persist_secs,omitempty"`
	// SubagentDefaultSecs is the default timeout for sub-agent orchestrator
	// runs when no explicit timeout is provided.  Default: 60.
	SubagentDefaultSecs int `json:"subagent_default_secs,omitempty"`
}

// HooksConfig configures the HTTP webhook ingress on the admin server.
// When enabled, the admin server exposes POST /hooks/wake and POST /hooks/agent
// endpoints authenticated by Token.  Custom path mappings route arbitrary
// POST /hooks/<name> requests to wake or agent actions.
type HooksConfig struct {
	// Enabled activates the webhook ingress.  Token is required when true.
	Enabled bool `json:"enabled,omitempty"`
	// Token is the shared secret used to authenticate inbound requests.
	// Send via "Authorization: Bearer <token>" or "X-Metiq-Token: <token>".
	Token string `json:"token,omitempty"`
	// AllowedAgentIDs restricts which agent IDs callers may target via
	// /hooks/agent.  Empty slice = no restriction.  Use "*" for wildcard.
	AllowedAgentIDs []string `json:"allowed_agent_ids,omitempty"`
	// DefaultSessionKey overrides the default session key for /hooks/agent
	// requests that do not specify an agent_id.  Defaults to "hook:ingress".
	DefaultSessionKey string `json:"default_session_key,omitempty"`
	// AllowRequestSessionKey permits callers to supply a custom session_key in
	// the /hooks/agent payload.  Disabled by default for security.
	AllowRequestSessionKey bool `json:"allow_request_session_key,omitempty"`
	// Mappings maps arbitrary POST /hooks/<name> paths to wake or agent actions.
	Mappings []HookMapping `json:"mappings,omitempty"`
}

// HookMappingMatch defines which path segment triggers a mapping.
type HookMappingMatch struct {
	// Path is the segment after /hooks/ (e.g. "github" → POST /hooks/github).
	Path string `json:"path"`
}

// HookMapping routes an inbound webhook request to a wake or agent action.
type HookMapping struct {
	// Match identifies the URL path segment to match.
	Match HookMappingMatch `json:"match"`
	// Action is "wake" (system event) or "agent" (isolated agent turn).
	Action string `json:"action"`
	// Name is an optional human-readable label for this mapping.
	Name string `json:"name,omitempty"`
	// MessageTemplate is the prompt or event text.
	// Use {{field.path}} tokens to interpolate JSON body values.
	// Example: "New event: {{action}} on {{repository.full_name}}"
	MessageTemplate string `json:"message_template,omitempty"`
	// Deliver, when true for action="agent", sends the reply via SendDM.
	Deliver bool `json:"deliver,omitempty"`
	// Channel selects the delivery channel ("nostr" is currently supported).
	Channel string `json:"channel,omitempty"`
	// To is the delivery recipient (Nostr npub for channel="nostr").
	To string `json:"to,omitempty"`
	// SessionKey overrides the session key for this mapping's agent turns.
	SessionKey string `json:"session_key,omitempty"`
}

// ── Nostr channel config ──────────────────────────────────────────────────────

// NostrChannelKind enumerates the supported Nostr transport types.
// "dm" uses NIP-04/NIP-17 direct messages.
// "nip28" uses NIP-28 public channel events (kind 40/42).
// "nip29" uses NIP-29 relay-managed groups.
// "relay-filter" subscribes to arbitrary relay filters and routes to an agent.
// "nip34-inbox" is a repo-targeted NIP-34 relay-filter preset.
type NostrChannelKind = string

type NostrChannelConfig struct {
	// Kind identifies the transport type (dm, nip28, nip29, relay-filter, nip34-inbox).
	Kind string `json:"kind"`
	// Enabled controls whether this channel is active at startup.
	Enabled bool `json:"enabled,omitempty"`
	// GroupAddress is the NIP-29 group address ("relay'groupID").
	// Used when Kind is "nip29".
	GroupAddress string `json:"group_address,omitempty"`
	// ChannelID is the NIP-28 channel event ID.
	// Used when Kind is "nip28".
	ChannelID string `json:"channel_id,omitempty"`
	// Relays is the list of relays to subscribe on for this channel.
	// Defaults to the global relay config when empty.
	Relays []string `json:"relays,omitempty"`
	// AgentID routes inbound messages to a specific agent.
	// Empty string means the default/session-assigned agent.
	AgentID string `json:"agent_id,omitempty"`
	// Tags is an optional set of Nostr filter tags for relay-filter channels.
	Tags map[string][]string `json:"tags,omitempty"`
	// Config holds channel-plugin-specific configuration (arbitrary key/value pairs).
	// Used by extension channel plugins (telegram, discord, etc.) for their settings.
	Config map[string]any `json:"config,omitempty"`
	// AllowFrom restricts which senders can interact via this channel.
	// Use "*" for wildcard (allow all). Empty = inherit DM policy allowlist.
	AllowFrom []string `json:"allow_from,omitempty"`
}

// NostrChannelsConfig is a named map of Nostr channel configurations.
// Keys are human-readable channel names (e.g. "main-group", "public-chat").
type NostrChannelsConfig map[string]NostrChannelConfig

// AgentConfig holds per-agent configuration stored in the ConfigDoc.
// This is distinct from AgentDoc (runtime state); AgentConfig is config-plane only.
type AgentConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Model        string `json:"model,omitempty"`
	WorkspaceDir string `json:"workspace_dir,omitempty"`
	ToolProfile string `json:"tool_profile,omitempty"` // minimal|coding|messaging|full
	// Provider names the providers[] entry to use for this agent (e.g. "anthropic", "ollama").
	// When set, credentials from ProvidersConfig[Provider] override the default env-based provider.
	Provider string `json:"provider,omitempty"`
	// DmPeers is a list of Nostr pubkeys (hex) whose DMs are routed to this agent.
	// At startup, each pubkey is pre-seeded into the session router with this agent's ID.
	DmPeers []string `json:"dm_peers,omitempty"`
	// FallbackModels is an ordered list of model identifiers to try when the
	// primary Model request fails (e.g. 429 rate limit or context-too-long).
	// Each entry can be a plain model name or "provider/model" to also switch provider.
	FallbackModels []string `json:"fallback_models,omitempty"`
	// LightModel is a cheaper/faster model used for simple messages (greetings,
	// short questions). When set, a ModelRouter scores each inbound message and
	// routes low-complexity ones to this model instead of the primary Model.
	LightModel string `json:"light_model,omitempty"`
	// LightModelThreshold is the complexity score (0.0–1.0) below which messages
	// are routed to LightModel. Default: 0.3.
	LightModelThreshold float64 `json:"light_model_threshold,omitempty"`
	// Heartbeat holds per-agent heartbeat overrides for future LLM-backed
	// heartbeat turns. Current heartbeat handling is presence-only.
	Heartbeat AgentHeartbeatConfig `json:"heartbeat,omitempty"`
	// ContextWindow is the model's total context window size in tokens.
	// This represents the actual model capacity (e.g. 4096 for Phi-3-mini,
	// 8192 for Gemma-2B) and drives budget allocation, compaction thresholds,
	// tool compression, and iteration limits. When 0, the value is resolved
	// from the model registry or defaults to 200K.
	//
	// ContextWindow is distinct from MaxContextTokens: ContextWindow is the
	// model's native capacity, while MaxContextTokens is an optional ceiling
	// that can further restrict how much context is assembled.
	ContextWindow int `json:"context_window,omitempty"`
	// MaxContextTokens is the approximate token budget for assembled context.
	// When the context engine estimates the assembled messages exceed 80% of this
	// value, auto-compaction is triggered before the model call.
	// When set alongside ContextWindow, the effective window is
	// min(ContextWindow, MaxContextTokens). Defaults to 100,000 when 0.
	MaxContextTokens int `json:"max_context_tokens,omitempty"`
	// SystemPrompt is injected as the system/context for every turn processed
	// by this agent. It is prepended before any memory or context-engine additions.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// MemoryScope mirrors the canonical src worker-memory contract.
	// Values: user | project | local. Empty preserves legacy unscoped behavior.
	MemoryScope AgentMemoryScope `json:"memory_scope,omitempty"`
	// EnabledTools is an explicit allowlist of tool names to expose to this agent.
	// When non-empty, only listed tools are included in the model's tool schema.
	// When empty, all registered tools are available (subject to ToolProfile).
	EnabledTools []string `json:"enabled_tools,omitempty"`
	// ThinkingLevel sets the extended-thinking budget for Anthropic models.
	// Values: "off", "minimal" (1 024), "low" (5 000), "medium" (10 000),
	// "high" (20 000), "xhigh" (40 000).  Empty string inherits the default
	// ("medium" when the session's Thinking flag is set).
	ThinkingLevel string `json:"thinking_level,omitempty"`
	// TurnTimeoutSecs is the maximum wall-clock seconds a single agent turn
	// (including the full agentic tool loop) may run before it is cancelled.
	// When 0 or negative the global default of 180 seconds is used.
	// Set to a negative value like -1 in config if you truly want no timeout
	// (not recommended for production).
	TurnTimeoutSecs int `json:"turn_timeout_secs,omitempty"`
	// MaxAgenticIterations caps the number of tool→LLM round-trips in the
	// agentic loop for this agent.  When 0 the model-tier default is used
	// (Micro=5, Small=10, Standard=30).  Useful for limiting token spend on
	// small-context models or increasing tool-call budget on capable ones.
	MaxAgenticIterations int `json:"max_agentic_iterations,omitempty"`
}

// AgentsConfig is an ordered list of per-agent configurations.
// The first entry whose ID matches is used; unmatched agents fall back to
// the top-level AgentPolicy defaults.
type AgentsConfig []AgentConfig

const (
	// VerificationPolicyRequired blocks completion until all required checks pass.
	VerificationPolicyRequired VerificationPolicy = "required"

	// VerificationPolicyAdvisory records verification results but does not block.
	VerificationPolicyAdvisory VerificationPolicy = "advisory"

	// VerificationPolicyNone disables verification gating entirely.
	VerificationPolicyNone VerificationPolicy = "none"
)
