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
	Agents    AgentsConfig    `json:"agents,omitempty"`
	Providers ProvidersConfig `json:"providers,omitempty"`
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

// AgentConfig holds per-agent configuration stored in the ConfigDoc.
// This is distinct from AgentDoc (runtime state); AgentConfig is config-plane only.
type AgentConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Model        string `json:"model,omitempty"`
	WorkspaceDir string `json:"workspace_dir,omitempty"`
	ToolProfile  string `json:"tool_profile,omitempty"` // minimal|coding|messaging|full
	HeartbeatMS  int    `json:"heartbeat_ms,omitempty"`
	HistoryLimit int    `json:"history_limit,omitempty"`
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
	Role      string         `json:"role"` // user|assistant|system
	Text      string         `json:"text"`
	Unix      int64          `json:"unix"`
	Meta      map[string]any `json:"meta,omitempty"`
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
