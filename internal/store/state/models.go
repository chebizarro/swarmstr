package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ConfigDoc is the canonical runtime configuration persisted to Nostr.
type ConfigDoc struct {
	Version   int             `json:"version"`
	DM        DMPolicy        `json:"dm"`
	Relays    RelayPolicy     `json:"relays"`
	Agent     AgentPolicy     `json:"agent"`
	Control   ControlPolicy   `json:"control,omitempty"`
	Agents         AgentsConfig         `json:"agents,omitempty"`
	NostrChannels  NostrChannelsConfig  `json:"nostr_channels,omitempty"`
	Providers      ProvidersConfig      `json:"providers,omitempty"`
	Session   SessionConfig   `json:"session,omitempty"`
	Heartbeat HeartbeatConfig `json:"heartbeat,omitempty"`
	TTS       TTSConfig       `json:"tts,omitempty"`
	Secrets   SecretsConfig   `json:"secrets,omitempty"`
	CronCfg   CronConfig      `json:"cron,omitempty"`
	Hooks     HooksConfig     `json:"hooks,omitempty"`
	AgentList *AgentListConfig `json:"agent_list,omitempty"`
	Extra     map[string]any  `json:"extra,omitempty"`
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

type DMPolicy struct {
	Policy         string             `json:"policy"` // pairing|allowlist|open|disabled
	AllowFrom      []string           `json:"allow_from,omitempty"`
	AllowFromLists []AllowFromListRef `json:"allow_from_lists,omitempty"`
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
	DefaultModel string `json:"default_model,omitempty"`
	Thinking     string `json:"thinking,omitempty"`
	Verbose      string `json:"verbose,omitempty"`
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
	TTLSeconds         int  `json:"ttl_seconds,omitempty"`
	MaxSessions        int  `json:"max_sessions,omitempty"`
	HistoryLimit       int  `json:"history_limit,omitempty"`
	// PruneAfterDays deletes transcript entries for sessions whose last
	// activity is older than this many days.  0 = disabled.
	PruneAfterDays     int  `json:"prune_after_days,omitempty"`
	// PruneIdleAfterDays deletes sessions that have received no inbound message
	// for this many days (more aggressive than PruneAfterDays).  0 = disabled.
	PruneIdleAfterDays int  `json:"prune_idle_after_days,omitempty"`
	// PruneOnBoot runs a pruning pass at daemon startup.
	PruneOnBoot        bool `json:"prune_on_boot,omitempty"`
}

// HeartbeatConfig controls the periodic heartbeat pulse.
type HeartbeatConfig struct {
	Enabled    bool `json:"enabled,omitempty"`
	IntervalMS int  `json:"interval_ms,omitempty"`
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
}

// HooksConfig configures the HTTP webhook ingress on the admin server.
// When enabled, the admin server exposes POST /hooks/wake and POST /hooks/agent
// endpoints authenticated by Token.  Custom path mappings route arbitrary
// POST /hooks/<name> requests to wake or agent actions.
type HooksConfig struct {
	// Enabled activates the webhook ingress.  Token is required when true.
	Enabled bool `json:"enabled,omitempty"`
	// Token is the shared secret used to authenticate inbound requests.
	// Send via "Authorization: Bearer <token>" or "X-Swarmstr-Token: <token>".
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
type NostrChannelKind = string

const (
	NostrChannelKindDM          NostrChannelKind = "dm"
	NostrChannelKindNIP28       NostrChannelKind = "nip28"
	NostrChannelKindNIP29       NostrChannelKind = "nip29"
	NostrChannelKindRelayFilter NostrChannelKind = "relay-filter"
)

// NostrChannelConfig describes a single Nostr transport subscription.
type NostrChannelConfig struct {
	// Kind identifies the transport type (dm, nip28, nip29, relay-filter).
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
	ToolProfile  string   `json:"tool_profile,omitempty"` // minimal|coding|messaging|full
	HeartbeatMS  int      `json:"heartbeat_ms,omitempty"`
	HistoryLimit int      `json:"history_limit,omitempty"`
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
	// MaxContextTokens is the approximate token budget for assembled context.
	// When the context engine estimates the assembled messages exceed 80% of this
	// value, auto-compaction is triggered before the model call.
	// Defaults to 100,000 when 0.
	MaxContextTokens int `json:"max_context_tokens,omitempty"`
	// SystemPrompt is injected as the system/context for every turn processed
	// by this agent. It is prepended before any memory or context-engine additions.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// EnabledTools is an explicit allowlist of tool names to expose to this agent.
	// When non-empty, only listed tools are included in the model's tool schema.
	// When empty, all registered tools are available (subject to ToolProfile).
	EnabledTools []string `json:"enabled_tools,omitempty"`
	// ThinkingLevel sets the extended-thinking budget for Anthropic models.
	// Values: "off", "minimal" (1 024), "low" (5 000), "medium" (10 000),
	// "high" (20 000), "xhigh" (40 000).  Empty string inherits the default
	// ("medium" when the session's Thinking flag is set).
	ThinkingLevel string `json:"thinking_level,omitempty"`
}

// AgentsConfig is an ordered list of per-agent configurations.
// The first entry whose ID matches is used; unmatched agents fall back to
// the top-level AgentPolicy defaults.
type AgentsConfig []AgentConfig

type SessionDoc struct {
	Version       int            `json:"version"`
	SessionID     string         `json:"session_id"`
	PeerPubKey    string         `json:"peer_pubkey"`
	LastInboundAt int64          `json:"last_inbound_at"`
	LastReplyAt   int64          `json:"last_reply_at"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type ListDoc struct {
	Version int      `json:"version"`
	Name    string   `json:"name"`
	Items   []string `json:"items"`
}

type CheckpointDoc struct {
	Version   int    `json:"version"`
	Name      string `json:"name"`
	LastEvent string `json:"last_event,omitempty"`
	LastUnix  int64  `json:"last_unix,omitempty"`
}

type TranscriptEntryDoc struct {
	Version   int            `json:"version"`
	SessionID string         `json:"session_id"`
	EntryID   string         `json:"entry_id"`
	Role      string         `json:"role"` // user|assistant|system|deleted
	Text      string         `json:"text"`
	Unix      int64          `json:"unix"`
	Meta      map[string]any `json:"meta,omitempty"`
	// Deleted is true for tombstoned entries that have been compacted away.
	// ListSession filters these out automatically.
	Deleted bool `json:"deleted,omitempty"`
}

type MemoryDoc struct {
	Version   int            `json:"version"`
	MemoryID  string         `json:"memory_id"`
	Type      string         `json:"type"` // fact|preference|profile|task
	SessionID string         `json:"session_id,omitempty"`
	Role      string         `json:"role,omitempty"`
	SourceRef string         `json:"source_ref,omitempty"`
	Text      string         `json:"text"`
	Keywords  []string       `json:"keywords,omitempty"`
	Topic     string         `json:"topic,omitempty"`
	Unix      int64          `json:"unix"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentDoc struct {
	Version   int            `json:"version"`
	AgentID   string         `json:"agent_id"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
	Deleted   bool           `json:"deleted,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentFileDoc struct {
	Version int            `json:"version"`
	AgentID string         `json:"agent_id"`
	Name    string         `json:"name"`
	Content string         `json:"content"`
	Meta    map[string]any `json:"meta,omitempty"`
}
