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
	Extra     map[string]any  `json:"extra,omitempty"`
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
	Policy    string   `json:"policy"` // pairing|allowlist|open|disabled
	AllowFrom []string `json:"allow_from,omitempty"`
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
	APIKey  string         `json:"api_key,omitempty"`  // redacted on read
	BaseURL string         `json:"base_url,omitempty"`
	Model   string         `json:"model,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

// ProvidersConfig is a map of named provider configurations.
// Keys are provider identifiers such as "openai", "anthropic", "ollama".
type ProvidersConfig map[string]ProviderEntry

// SessionConfig controls per-session behaviour.
type SessionConfig struct {
	TTLSeconds   int `json:"ttl_seconds,omitempty"`
	MaxSessions  int `json:"max_sessions,omitempty"`
	HistoryLimit int `json:"history_limit,omitempty"`
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
