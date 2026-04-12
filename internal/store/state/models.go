package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigDoc is the canonical runtime configuration persisted to Nostr.
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
	AgentList     *AgentListConfig    `json:"agent_list,omitempty"`
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
	// Supported values: auto, nip17, nip04.
	Transport string `json:"transport,omitempty"`
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
	DefaultModel    string        `json:"default_model,omitempty"`
	Thinking        string        `json:"thinking,omitempty"`
	Verbose         string        `json:"verbose,omitempty"`
	DefaultAutonomy AutonomyMode  `json:"default_autonomy,omitempty"`
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
	TTLSeconds   int `json:"ttl_seconds,omitempty"`
	MaxSessions  int `json:"max_sessions,omitempty"`
	HistoryLimit int `json:"history_limit,omitempty"`
	// PruneAfterDays deletes transcript entries for sessions whose last
	// activity is older than this many days.  0 = disabled.
	PruneAfterDays int `json:"prune_after_days,omitempty"`
	// PruneIdleAfterDays deletes sessions that have received no inbound message
	// for this many days (more aggressive than PruneAfterDays).  0 = disabled.
	PruneIdleAfterDays int `json:"prune_idle_after_days,omitempty"`
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

const (
	NostrChannelKindDM          NostrChannelKind = "dm"
	NostrChannelKindNIP28       NostrChannelKind = "nip28"
	NostrChannelKindNIP29       NostrChannelKind = "nip29"
	NostrChannelKindChat        NostrChannelKind = "chat" // NIP-C7 kind:9 chat
	NostrChannelKindRelayFilter NostrChannelKind = "relay-filter"
	NostrChannelKindNIP34Inbox  NostrChannelKind = "nip34-inbox"
)

// NostrChannelConfig describes a single Nostr transport subscription.
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
	ToolProfile  string `json:"tool_profile,omitempty"` // minimal|coding|messaging|full
	HeartbeatMS  int    `json:"heartbeat_ms,omitempty"`
	HistoryLimit int    `json:"history_limit,omitempty"`
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
	// MaxContextTokens is the approximate token budget for assembled context.
	// When the context engine estimates the assembled messages exceed 80% of this
	// value, auto-compaction is triggered before the model call.
	// Defaults to 100,000 when 0.
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
	Version          int                       `json:"version"`
	Name             string                    `json:"name"`
	LastEvent        string                    `json:"last_event,omitempty"`
	LastUnix         int64                     `json:"last_unix,omitempty"`
	RecentEventIDs   []string                  `json:"recent_event_ids,omitempty"`
	ControlResponses []ControlResponseCacheDoc `json:"control_responses,omitempty"`
}

type ControlResponseCacheDoc struct {
	CallerPubKey string     `json:"caller_pubkey"`
	RequestID    string     `json:"request_id"`
	Payload      string     `json:"payload"`
	Tags         [][]string `json:"tags,omitempty"`
	EventUnix    int64      `json:"event_unix,omitempty"`
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
	Type      string         `json:"type"` // fact|preference|profile|task|episodic
	SessionID string         `json:"session_id,omitempty"`
	Role      string         `json:"role,omitempty"`
	SourceRef string         `json:"source_ref,omitempty"`
	Text      string         `json:"text"`
	Keywords  []string       `json:"keywords,omitempty"`
	Topic     string         `json:"topic,omitempty"`
	Unix      int64          `json:"unix"`
	Meta      map[string]any `json:"meta,omitempty"`

	// Episodic memory fields — populated when Type == "episodic".
	GoalID      string `json:"goal_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	EpisodeKind string `json:"episode_kind,omitempty"` // outcome|decision|error|insight

	// Trust & provenance metadata.
	Confidence float64 `json:"confidence,omitempty"` // 0.0–1.0; 0 means unset (defaults to 0.5)
	Source     string  `json:"source,omitempty"`      // agent|user|system|import
	ReviewedAt int64   `json:"reviewed_at,omitempty"` // unix seconds; 0 means unreviewed
	ReviewedBy string  `json:"reviewed_by,omitempty"` // pubkey or agent ID of reviewer
	ExpiresAt  int64   `json:"expires_at,omitempty"`  // unix seconds; 0 means no expiry

	// Invalidation / lifecycle state.
	MemStatus        string `json:"mem_status,omitempty"`        // active|stale|superseded|contradicted (empty = active)
	SupersededBy     string `json:"superseded_by,omitempty"`     // memory_id of the replacement record
	InvalidatedAt    int64  `json:"invalidated_at,omitempty"`    // unix seconds when invalidated
	InvalidatedBy    string `json:"invalidated_by,omitempty"`    // who/what triggered invalidation
	InvalidateReason string `json:"invalidate_reason,omitempty"` // human-readable reason
}

// MemoryType constants for the MemoryDoc.Type field.
const (
	MemoryTypeFact       = "fact"
	MemoryTypePreference = "preference"
	MemoryTypeProfile    = "profile"
	MemoryTypeTask       = "task"
	MemoryTypeEpisodic   = "episodic"
)

// EpisodeKind constants for MemoryDoc.EpisodeKind.
const (
	EpisodeKindOutcome  = "outcome"
	EpisodeKindDecision = "decision"
	EpisodeKindError    = "error"
	EpisodeKindInsight  = "insight"
)

// MemorySource constants for MemoryDoc.Source.
const (
	MemorySourceAgent  = "agent"
	MemorySourceUser   = "user"
	MemorySourceSystem = "system"
	MemorySourceImport = "import"
)

// DefaultConfidence is used when Confidence is zero (unset).
const DefaultConfidence = 0.5

// MemStatus constants for MemoryDoc.MemStatus.
// Empty string is treated as active.
const (
	MemStatusActive       = "active"
	MemStatusStale        = "stale"
	MemStatusSuperseded   = "superseded"
	MemStatusContradicted = "contradicted"
)

// IsMemStatusValid returns true for recognized memory status values.
func IsMemStatusValid(s string) bool {
	switch s {
	case "", MemStatusActive, MemStatusStale, MemStatusSuperseded, MemStatusContradicted:
		return true
	}
	return false
}

// IsMemoryActive returns true if the memory is in an active (usable) state.
func (d MemoryDoc) IsMemoryActive() bool {
	return d.MemStatus == "" || d.MemStatus == MemStatusActive
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

// GoalStatus describes the canonical lifecycle state of a goal.
type GoalStatus string

const (
	GoalStatusPending   GoalStatus = "pending"
	GoalStatusActive    GoalStatus = "active"
	GoalStatusBlocked   GoalStatus = "blocked"
	GoalStatusCompleted GoalStatus = "completed"
	GoalStatusFailed    GoalStatus = "failed"
	GoalStatusCancelled GoalStatus = "cancelled"
)

func ParseGoalStatus(raw string) (GoalStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(GoalStatusPending):
		return GoalStatusPending, true
	case string(GoalStatusActive):
		return GoalStatusActive, true
	case string(GoalStatusBlocked):
		return GoalStatusBlocked, true
	case string(GoalStatusCompleted):
		return GoalStatusCompleted, true
	case string(GoalStatusFailed):
		return GoalStatusFailed, true
	case string(GoalStatusCancelled):
		return GoalStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizeGoalStatus(raw string) GoalStatus {
	status, _ := ParseGoalStatus(raw)
	return status
}

func (s GoalStatus) Valid() bool {
	_, ok := ParseGoalStatus(string(s))
	return ok
}

// TaskStatus describes the canonical lifecycle state of a task.
type TaskStatus string

const (
	TaskStatusPending          TaskStatus = "pending"
	TaskStatusPlanned          TaskStatus = "planned"
	TaskStatusReady            TaskStatus = "ready"
	TaskStatusInProgress       TaskStatus = "in_progress"
	TaskStatusBlocked          TaskStatus = "blocked"
	TaskStatusAwaitingApproval TaskStatus = "awaiting_approval"
	TaskStatusVerifying        TaskStatus = "verifying"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCancelled        TaskStatus = "cancelled"
)

func ParseTaskStatus(raw string) (TaskStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(TaskStatusPending):
		return TaskStatusPending, true
	case string(TaskStatusPlanned):
		return TaskStatusPlanned, true
	case string(TaskStatusReady):
		return TaskStatusReady, true
	case string(TaskStatusInProgress):
		return TaskStatusInProgress, true
	case string(TaskStatusBlocked):
		return TaskStatusBlocked, true
	case string(TaskStatusAwaitingApproval):
		return TaskStatusAwaitingApproval, true
	case string(TaskStatusVerifying):
		return TaskStatusVerifying, true
	case string(TaskStatusCompleted):
		return TaskStatusCompleted, true
	case string(TaskStatusFailed):
		return TaskStatusFailed, true
	case string(TaskStatusCancelled):
		return TaskStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizeTaskStatus(raw string) TaskStatus {
	status, _ := ParseTaskStatus(raw)
	return status
}

func (s TaskStatus) Valid() bool {
	_, ok := ParseTaskStatus(string(s))
	return ok
}

// TaskRunStatus describes the lifecycle state of an execution attempt for a task.
type TaskRunStatus string

const (
	TaskRunStatusQueued           TaskRunStatus = "queued"
	TaskRunStatusRunning          TaskRunStatus = "running"
	TaskRunStatusBlocked          TaskRunStatus = "blocked"
	TaskRunStatusAwaitingApproval TaskRunStatus = "awaiting_approval"
	TaskRunStatusRetrying         TaskRunStatus = "retrying"
	TaskRunStatusCompleted        TaskRunStatus = "completed"
	TaskRunStatusFailed           TaskRunStatus = "failed"
	TaskRunStatusCancelled        TaskRunStatus = "cancelled"
)

func ParseTaskRunStatus(raw string) (TaskRunStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(TaskRunStatusQueued):
		return TaskRunStatusQueued, true
	case string(TaskRunStatusRunning):
		return TaskRunStatusRunning, true
	case string(TaskRunStatusBlocked):
		return TaskRunStatusBlocked, true
	case string(TaskRunStatusAwaitingApproval):
		return TaskRunStatusAwaitingApproval, true
	case string(TaskRunStatusRetrying):
		return TaskRunStatusRetrying, true
	case string(TaskRunStatusCompleted):
		return TaskRunStatusCompleted, true
	case string(TaskRunStatusFailed):
		return TaskRunStatusFailed, true
	case string(TaskRunStatusCancelled):
		return TaskRunStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizeTaskRunStatus(raw string) TaskRunStatus {
	status, _ := ParseTaskRunStatus(raw)
	return status
}

func (s TaskRunStatus) Valid() bool {
	_, ok := ParseTaskRunStatus(string(s))
	return ok
}

// TaskPriority describes scheduling or triage priority.
type TaskPriority string

const (
	TaskPriorityHigh   TaskPriority = "high"
	TaskPriorityMedium TaskPriority = "medium"
	TaskPriorityLow    TaskPriority = "low"
)

func ParseTaskPriority(raw string) (TaskPriority, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(TaskPriorityHigh):
		return TaskPriorityHigh, true
	case string(TaskPriorityMedium), "":
		return TaskPriorityMedium, true
	case string(TaskPriorityLow):
		return TaskPriorityLow, true
	default:
		return "", false
	}
}

func NormalizeTaskPriority(raw string) TaskPriority {
	priority, _ := ParseTaskPriority(raw)
	return priority
}

func (p TaskPriority) Valid() bool {
	_, ok := ParseTaskPriority(string(p))
	return ok
}

// RiskClass categorizes the risk level of an operation.
type RiskClass string

const (
	RiskClassLow      RiskClass = "low"
	RiskClassMedium   RiskClass = "medium"
	RiskClassHigh     RiskClass = "high"
	RiskClassCritical RiskClass = "critical"
)

func ParseRiskClass(raw string) (RiskClass, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(RiskClassLow), "":
		return RiskClassLow, true
	case string(RiskClassMedium):
		return RiskClassMedium, true
	case string(RiskClassHigh):
		return RiskClassHigh, true
	case string(RiskClassCritical):
		return RiskClassCritical, true
	default:
		return "", false
	}
}

func NormalizeRiskClass(raw string) RiskClass {
	rc, ok := ParseRiskClass(raw)
	if !ok {
		return RiskClassLow
	}
	return rc
}

func (r RiskClass) Valid() bool {
	_, ok := ParseRiskClass(string(r))
	return ok
}

// TaskAuthority captures the authority contract attached to a goal or task.
// It defines what an agent is allowed to do and how much oversight is required.
type TaskAuthority struct {
	// AutonomyMode controls overall agent latitude (full, plan_approval, etc.).
	AutonomyMode       AutonomyMode `json:"autonomy_mode,omitempty"`
	// Role is a human-readable label for the authority scope (e.g. "engineer", "reviewer").
	Role               string       `json:"role,omitempty"`
	// RiskClass categorizes the risk level — higher risk triggers more oversight.
	RiskClass          RiskClass    `json:"risk_class,omitempty"`
	// CanAct permits the agent to take actions (tool calls, writes).
	CanAct             bool         `json:"can_act,omitempty"`
	// CanDelegate permits spawning sub-agents or delegating sub-tasks.
	CanDelegate        bool         `json:"can_delegate,omitempty"`
	// CanEscalate permits the agent to escalate to a higher authority.
	CanEscalate        bool         `json:"can_escalate,omitempty"`
	// EscalationRequired forces all tool actions to escalation review.
	EscalationRequired bool         `json:"escalation_required,omitempty"`
	// AllowedAgents restricts which agent IDs may be delegated to.
	AllowedAgents      []string     `json:"allowed_agents,omitempty"`
	// AllowedTools restricts which tools the agent may invoke.
	AllowedTools       []string     `json:"allowed_tools,omitempty"`
	// DeniedTools lists tools explicitly denied regardless of other permissions.
	DeniedTools        []string     `json:"denied_tools,omitempty"`
	// MaxDelegationDepth caps how many levels deep delegation chains can go.
	MaxDelegationDepth int          `json:"max_delegation_depth,omitempty"`

	// Deprecated: ApprovalMode is retained for backward-compatible JSON
	// deserialization of persisted state docs. Normalize() migrates it to
	// AutonomyMode. New code should use AutonomyMode directly.
	ApprovalMode string `json:"approval_mode,omitempty"`
}

// Normalize sets canonical defaults for zero-value authority fields.
// It also migrates the deprecated ApprovalMode field into AutonomyMode
// for backward compatibility with persisted state docs.
func (a TaskAuthority) Normalize() TaskAuthority {
	// Migrate legacy ApprovalMode → AutonomyMode when the new field is empty.
	if a.AutonomyMode == "" && a.ApprovalMode != "" {
		a.AutonomyMode = migrateApprovalMode(a.ApprovalMode)
		a.ApprovalMode = "" // clear deprecated field after migration
	}
	if a.AutonomyMode != "" {
		a.AutonomyMode = NormalizeAutonomyMode(string(a.AutonomyMode))
	}
	if a.RiskClass != "" {
		a.RiskClass = NormalizeRiskClass(string(a.RiskClass))
	}
	return a
}

// migrateApprovalMode maps legacy approval_mode strings to AutonomyMode values.
func migrateApprovalMode(legacy string) AutonomyMode {
	switch strings.ToLower(strings.TrimSpace(legacy)) {
	case "act_with_approval", "approval", "plan_approval":
		return AutonomyPlanApproval
	case "step_approval":
		return AutonomyStepApproval
	case "supervised", "observe_only", "recommend_only":
		return AutonomySupervised
	case "autonomous", "full", "bounded_autonomous", "fully_autonomous":
		return AutonomyFull
	default:
		// Unknown legacy value — default to plan_approval for safety
		// (more restrictive than full, less restrictive than supervised).
		return AutonomyPlanApproval
	}
}

// Validate checks that authority fields contain valid values.
func (a TaskAuthority) Validate() error {
	if a.AutonomyMode != "" && !a.AutonomyMode.Valid() {
		return fmt.Errorf("invalid autonomy_mode %q", a.AutonomyMode)
	}
	if a.RiskClass != "" && !a.RiskClass.Valid() {
		return fmt.Errorf("invalid risk_class %q", a.RiskClass)
	}
	if a.MaxDelegationDepth < 0 {
		return fmt.Errorf("max_delegation_depth must be >= 0")
	}
	return nil
}

// EffectiveAutonomyMode returns the authority's autonomy mode, or falls back
// to the given default when the authority doesn't specify one.
func (a TaskAuthority) EffectiveAutonomyMode(defaultMode AutonomyMode) AutonomyMode {
	if a.AutonomyMode != "" {
		return a.AutonomyMode
	}
	return defaultMode
}

// MayUseTool reports whether this authority permits the given tool name.
func (a TaskAuthority) MayUseTool(tool string) bool {
	for _, denied := range a.DeniedTools {
		if denied == tool {
			return false
		}
	}
	if len(a.AllowedTools) == 0 {
		return true // no allowlist means all tools permitted
	}
	for _, allowed := range a.AllowedTools {
		if allowed == tool {
			return true
		}
	}
	return false
}

// MayDelegateTo reports whether this authority permits delegation to the given agent.
func (a TaskAuthority) MayDelegateTo(agentID string) bool {
	if !a.CanDelegate {
		return false
	}
	if len(a.AllowedAgents) == 0 {
		return true // no restriction
	}
	for _, allowed := range a.AllowedAgents {
		if allowed == agentID {
			return true
		}
	}
	return false
}

// DefaultAuthority returns a reasonable default authority for the given autonomy mode.
func DefaultAuthority(mode AutonomyMode) TaskAuthority {
	switch mode {
	case AutonomySupervised:
		return TaskAuthority{
			AutonomyMode:       AutonomySupervised,
			CanAct:             false,
			CanDelegate:        false,
			CanEscalate:        true,
			EscalationRequired: true,
			RiskClass:          RiskClassHigh,
		}
	case AutonomyStepApproval:
		return TaskAuthority{
			AutonomyMode:       AutonomyStepApproval,
			CanAct:             true,
			CanDelegate:        false,
			CanEscalate:        true,
			RiskClass:          RiskClassMedium,
			MaxDelegationDepth: 1,
		}
	case AutonomyPlanApproval:
		return TaskAuthority{
			AutonomyMode:       AutonomyPlanApproval,
			CanAct:             true,
			CanDelegate:        true,
			CanEscalate:        true,
			RiskClass:          RiskClassMedium,
			MaxDelegationDepth: 2,
		}
	case AutonomyFull:
		return TaskAuthority{
			AutonomyMode:       AutonomyFull,
			CanAct:             true,
			CanDelegate:        true,
			CanEscalate:        true,
			RiskClass:          RiskClassLow,
			MaxDelegationDepth: 3,
		}
	default:
		return DefaultAuthority(AutonomyFull)
	}
}

// TaskBudget captures budget guardrails that downstream runtime layers enforce.
// Zero values mean "unlimited" for that dimension.
type TaskBudget struct {
	MaxPromptTokens     int   `json:"max_prompt_tokens,omitempty"`
	MaxCompletionTokens int   `json:"max_completion_tokens,omitempty"`
	MaxTotalTokens      int   `json:"max_total_tokens,omitempty"`
	MaxRuntimeMS        int64 `json:"max_runtime_ms,omitempty"`
	MaxToolCalls        int   `json:"max_tool_calls,omitempty"`
	MaxDelegations      int   `json:"max_delegations,omitempty"`
	MaxCostMicrosUSD    int64 `json:"max_cost_micros_usd,omitempty"`
}

// IsZero reports whether no budget limits have been set.
func (b TaskBudget) IsZero() bool {
	return b.MaxPromptTokens == 0 &&
		b.MaxCompletionTokens == 0 &&
		b.MaxTotalTokens == 0 &&
		b.MaxRuntimeMS == 0 &&
		b.MaxToolCalls == 0 &&
		b.MaxDelegations == 0 &&
		b.MaxCostMicrosUSD == 0
}

// Validate checks that budget values are non-negative and internally consistent.
func (b TaskBudget) Validate() error {
	if b.MaxPromptTokens < 0 {
		return fmt.Errorf("max_prompt_tokens must be >= 0")
	}
	if b.MaxCompletionTokens < 0 {
		return fmt.Errorf("max_completion_tokens must be >= 0")
	}
	if b.MaxTotalTokens < 0 {
		return fmt.Errorf("max_total_tokens must be >= 0")
	}
	if b.MaxRuntimeMS < 0 {
		return fmt.Errorf("max_runtime_ms must be >= 0")
	}
	if b.MaxToolCalls < 0 {
		return fmt.Errorf("max_tool_calls must be >= 0")
	}
	if b.MaxDelegations < 0 {
		return fmt.Errorf("max_delegations must be >= 0")
	}
	if b.MaxCostMicrosUSD < 0 {
		return fmt.Errorf("max_cost_micros_usd must be >= 0")
	}
	// If both component and total token limits are set, total must be >= sum.
	if b.MaxTotalTokens > 0 && b.MaxPromptTokens > 0 && b.MaxCompletionTokens > 0 {
		if b.MaxTotalTokens < b.MaxPromptTokens+b.MaxCompletionTokens {
			return fmt.Errorf("max_total_tokens (%d) < max_prompt_tokens (%d) + max_completion_tokens (%d)",
				b.MaxTotalTokens, b.MaxPromptTokens, b.MaxCompletionTokens)
		}
	}
	return nil
}

// Narrow returns a new budget that is the tighter of b and child for each
// dimension. This implements the inheritance rule: a child task's budget can
// only be equal to or stricter than its parent's. Zero (unlimited) in either
// input yields the other's limit.
func (b TaskBudget) Narrow(child TaskBudget) TaskBudget {
	return TaskBudget{
		MaxPromptTokens:     narrowInt(b.MaxPromptTokens, child.MaxPromptTokens),
		MaxCompletionTokens: narrowInt(b.MaxCompletionTokens, child.MaxCompletionTokens),
		MaxTotalTokens:      narrowInt(b.MaxTotalTokens, child.MaxTotalTokens),
		MaxRuntimeMS:        narrowInt64(b.MaxRuntimeMS, child.MaxRuntimeMS),
		MaxToolCalls:        narrowInt(b.MaxToolCalls, child.MaxToolCalls),
		MaxDelegations:      narrowInt(b.MaxDelegations, child.MaxDelegations),
		MaxCostMicrosUSD:    narrowInt64(b.MaxCostMicrosUSD, child.MaxCostMicrosUSD),
	}
}

// narrowInt returns the tighter of two limits. Zero means unlimited.
func narrowInt(parent, child int) int {
	if parent == 0 {
		return child
	}
	if child == 0 {
		return parent
	}
	if child < parent {
		return child
	}
	return parent
}

func narrowInt64(parent, child int64) int64 {
	if parent == 0 {
		return child
	}
	if child == 0 {
		return parent
	}
	if child < parent {
		return child
	}
	return parent
}

// TaskUsage captures measured runtime consumption for a task run.
type TaskUsage struct {
	PromptTokens     int   `json:"prompt_tokens,omitempty"`
	CompletionTokens int   `json:"completion_tokens,omitempty"`
	TotalTokens      int   `json:"total_tokens,omitempty"`
	WallClockMS      int64 `json:"wall_clock_ms,omitempty"`
	ToolCalls        int   `json:"tool_calls,omitempty"`
	Delegations      int   `json:"delegations,omitempty"`
	CostMicrosUSD    int64 `json:"cost_micros_usd,omitempty"`
}

// Add accumulates usage from another measurement.
func (u *TaskUsage) Add(other TaskUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.WallClockMS += other.WallClockMS
	u.ToolCalls += other.ToolCalls
	u.Delegations += other.Delegations
	u.CostMicrosUSD += other.CostMicrosUSD
}

// BudgetExceeded describes which budget dimensions have been exceeded.
type BudgetExceeded struct {
	PromptTokens     bool   `json:"prompt_tokens,omitempty"`
	CompletionTokens bool   `json:"completion_tokens,omitempty"`
	TotalTokens      bool   `json:"total_tokens,omitempty"`
	RuntimeMS        bool   `json:"runtime_ms,omitempty"`
	ToolCalls        bool   `json:"tool_calls,omitempty"`
	Delegations      bool   `json:"delegations,omitempty"`
	CostMicrosUSD    bool   `json:"cost_micros_usd,omitempty"`
}

// Any reports whether any budget dimension has been exceeded.
func (e BudgetExceeded) Any() bool {
	return e.PromptTokens || e.CompletionTokens || e.TotalTokens ||
		e.RuntimeMS || e.ToolCalls || e.Delegations || e.CostMicrosUSD
}

// Reasons returns human-readable descriptions of exceeded dimensions.
func (e BudgetExceeded) Reasons() []string {
	var reasons []string
	if e.PromptTokens {
		reasons = append(reasons, "prompt tokens exceeded")
	}
	if e.CompletionTokens {
		reasons = append(reasons, "completion tokens exceeded")
	}
	if e.TotalTokens {
		reasons = append(reasons, "total tokens exceeded")
	}
	if e.RuntimeMS {
		reasons = append(reasons, "runtime exceeded")
	}
	if e.ToolCalls {
		reasons = append(reasons, "tool calls exceeded")
	}
	if e.Delegations {
		reasons = append(reasons, "delegations exceeded")
	}
	if e.CostMicrosUSD {
		reasons = append(reasons, "cost exceeded")
	}
	return reasons
}

// CheckUsage compares measured usage against a budget and returns which
// dimensions are exceeded. Zero budget values mean unlimited.
func (b TaskBudget) CheckUsage(usage TaskUsage) BudgetExceeded {
	return BudgetExceeded{
		PromptTokens:     b.MaxPromptTokens > 0 && usage.PromptTokens > b.MaxPromptTokens,
		CompletionTokens: b.MaxCompletionTokens > 0 && usage.CompletionTokens > b.MaxCompletionTokens,
		TotalTokens:      b.MaxTotalTokens > 0 && usage.TotalTokens > b.MaxTotalTokens,
		RuntimeMS:        b.MaxRuntimeMS > 0 && usage.WallClockMS > b.MaxRuntimeMS,
		ToolCalls:        b.MaxToolCalls > 0 && usage.ToolCalls > b.MaxToolCalls,
		Delegations:      b.MaxDelegations > 0 && usage.Delegations > b.MaxDelegations,
		CostMicrosUSD:    b.MaxCostMicrosUSD > 0 && usage.CostMicrosUSD > b.MaxCostMicrosUSD,
	}
}

// Remaining returns a budget representing the unused capacity given current usage.
// Dimensions with no limit (zero) remain zero (unlimited) in the result.
func (b TaskBudget) Remaining(usage TaskUsage) TaskBudget {
	remaining := func(limit, used int) int {
		if limit == 0 {
			return 0
		}
		r := limit - used
		if r < 0 {
			return 0
		}
		return r
	}
	remaining64 := func(limit, used int64) int64 {
		if limit == 0 {
			return 0
		}
		r := limit - used
		if r < 0 {
			return 0
		}
		return r
	}
	return TaskBudget{
		MaxPromptTokens:     remaining(b.MaxPromptTokens, usage.PromptTokens),
		MaxCompletionTokens: remaining(b.MaxCompletionTokens, usage.CompletionTokens),
		MaxTotalTokens:      remaining(b.MaxTotalTokens, usage.TotalTokens),
		MaxRuntimeMS:        remaining64(b.MaxRuntimeMS, usage.WallClockMS),
		MaxToolCalls:        remaining(b.MaxToolCalls, usage.ToolCalls),
		MaxDelegations:      remaining(b.MaxDelegations, usage.Delegations),
		MaxCostMicrosUSD:    remaining64(b.MaxCostMicrosUSD, usage.CostMicrosUSD),
	}
}

// TaskOutputSpec describes an expected output contract for a task.
type TaskOutputSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
	SchemaRef   string `json:"schema_ref,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// TaskAcceptanceCriterion describes how task completion should be judged.
type TaskAcceptanceCriterion struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

// ── Verification schemas ──────────────────────────────────────────────────��────

// VerificationStatus describes the lifecycle of a verification check.
type VerificationStatus string

const (
	VerificationStatusPending  VerificationStatus = "pending"
	VerificationStatusRunning  VerificationStatus = "running"
	VerificationStatusPassed   VerificationStatus = "passed"
	VerificationStatusFailed   VerificationStatus = "failed"
	VerificationStatusSkipped  VerificationStatus = "skipped"
	VerificationStatusError    VerificationStatus = "error"
)

func ParseVerificationStatus(raw string) (VerificationStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(VerificationStatusPending), "":
		return VerificationStatusPending, true
	case string(VerificationStatusRunning):
		return VerificationStatusRunning, true
	case string(VerificationStatusPassed):
		return VerificationStatusPassed, true
	case string(VerificationStatusFailed):
		return VerificationStatusFailed, true
	case string(VerificationStatusSkipped):
		return VerificationStatusSkipped, true
	case string(VerificationStatusError):
		return VerificationStatusError, true
	default:
		return "", false
	}
}

func NormalizeVerificationStatus(raw string) VerificationStatus {
	s, ok := ParseVerificationStatus(raw)
	if !ok {
		return VerificationStatusPending
	}
	return s
}

func (s VerificationStatus) Valid() bool {
	_, ok := ParseVerificationStatus(string(s))
	return ok
}

// IsTerminal reports whether the status represents a final verification outcome.
func (s VerificationStatus) IsTerminal() bool {
	switch s {
	case VerificationStatusPassed, VerificationStatusFailed, VerificationStatusSkipped, VerificationStatusError:
		return true
	}
	return false
}

// VerificationCheckType classifies verification strategies.
type VerificationCheckType string

const (
	VerificationCheckSchema   VerificationCheckType = "schema"    // JSON schema validation
	VerificationCheckEvidence VerificationCheckType = "evidence"  // evidence artifact present
	VerificationCheckCustom   VerificationCheckType = "custom"    // custom evaluator
	VerificationCheckReview   VerificationCheckType = "review"    // human/agent review
	VerificationCheckTest     VerificationCheckType = "test"      // automated test pass
)

// VerificationCheck describes a single verification rule.
type VerificationCheck struct {
	CheckID      string                `json:"check_id"`
	Type         VerificationCheckType `json:"type"`
	Description  string                `json:"description"`
	Required     bool                  `json:"required"`
	Status       VerificationStatus    `json:"status"`
	Result       string                `json:"result,omitempty"`
	Evidence     string                `json:"evidence,omitempty"`
	EvaluatedAt  int64                 `json:"evaluated_at,omitempty"`
	EvaluatedBy  string                `json:"evaluated_by,omitempty"`
	Meta         map[string]any        `json:"meta,omitempty"`
}

func (c VerificationCheck) Validate() error {
	if strings.TrimSpace(c.CheckID) == "" {
		return fmt.Errorf("check_id is required")
	}
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("check description is required")
	}
	if raw := strings.TrimSpace(string(c.Status)); raw != "" && !c.Status.Valid() {
		return fmt.Errorf("invalid check status %q", c.Status)
	}
	return nil
}

func (c VerificationCheck) Normalize() VerificationCheck {
	c.Status = NormalizeVerificationStatus(string(c.Status))
	return c
}

// VerificationPolicy controls how verification gates task completion.
type VerificationPolicy string

const (
	// VerificationPolicyRequired blocks completion until all required checks pass.
	VerificationPolicyRequired VerificationPolicy = "required"

	// VerificationPolicyAdvisory records verification results but does not block.
	VerificationPolicyAdvisory VerificationPolicy = "advisory"

	// VerificationPolicyNone disables verification gating entirely.
	VerificationPolicyNone VerificationPolicy = "none"
)

func ParseVerificationPolicy(raw string) (VerificationPolicy, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(VerificationPolicyRequired):
		return VerificationPolicyRequired, true
	case string(VerificationPolicyAdvisory):
		return VerificationPolicyAdvisory, true
	case string(VerificationPolicyNone), "":
		return VerificationPolicyNone, true
	default:
		return "", false
	}
}

func NormalizeVerificationPolicy(raw string) VerificationPolicy {
	p, ok := ParseVerificationPolicy(raw)
	if !ok {
		return VerificationPolicyNone
	}
	return p
}

func (p VerificationPolicy) Valid() bool {
	_, ok := ParseVerificationPolicy(string(p))
	return ok
}

// VerificationSpec is the complete verification contract for a task or run.
type VerificationSpec struct {
	Policy     VerificationPolicy  `json:"policy"`
	Checks     []VerificationCheck `json:"checks,omitempty"`
	VerifiedAt int64               `json:"verified_at,omitempty"`
	VerifiedBy string              `json:"verified_by,omitempty"`
	Meta       map[string]any      `json:"meta,omitempty"`
}

func (v VerificationSpec) Normalize() VerificationSpec {
	v.Policy = NormalizeVerificationPolicy(string(v.Policy))
	for i := range v.Checks {
		v.Checks[i] = v.Checks[i].Normalize()
	}
	return v
}

func (v VerificationSpec) Validate() error {
	if raw := strings.TrimSpace(string(v.Policy)); raw != "" && !v.Policy.Valid() {
		return fmt.Errorf("invalid verification policy %q", v.Policy)
	}
	if v.Policy == VerificationPolicyRequired && len(v.Checks) == 0 {
		return fmt.Errorf("verification policy is 'required' but no checks are defined")
	}
	checkIDs := make(map[string]bool, len(v.Checks))
	for i, check := range v.Checks {
		if err := check.Validate(); err != nil {
			return fmt.Errorf("checks[%d]: %w", i, err)
		}
		if checkIDs[check.CheckID] {
			return fmt.Errorf("duplicate check_id %q at checks[%d]", check.CheckID, i)
		}
		checkIDs[check.CheckID] = true
	}
	return nil
}

// RequiredChecks returns only the checks marked as required.
func (v VerificationSpec) RequiredChecks() []VerificationCheck {
	var out []VerificationCheck
	for _, c := range v.Checks {
		if c.Required {
			out = append(out, c)
		}
	}
	return out
}

// AllRequiredPassed reports whether all required checks have passed or been skipped.
func (v VerificationSpec) AllRequiredPassed() bool {
	for _, c := range v.Checks {
		if !c.Required {
			continue
		}
		if c.Status != VerificationStatusPassed && c.Status != VerificationStatusSkipped {
			return false
		}
	}
	return true
}

// AnyRequiredFailed reports whether any required check has failed.
func (v VerificationSpec) AnyRequiredFailed() bool {
	for _, c := range v.Checks {
		if c.Required && (c.Status == VerificationStatusFailed || c.Status == VerificationStatusError) {
			return true
		}
	}
	return false
}

// PendingChecks returns checks that haven't been evaluated yet.
func (v VerificationSpec) PendingChecks() []VerificationCheck {
	var out []VerificationCheck
	for _, c := range v.Checks {
		if c.Status == VerificationStatusPending {
			out = append(out, c)
		}
	}
	return out
}

// TaskResultRef points at a durable result, artifact, or event produced by a task run.
type TaskResultRef struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
	URI  string `json:"uri,omitempty"`
	Hash string `json:"hash,omitempty"`
}

// GoalSpec is the canonical persisted representation of a user or system goal.
type GoalSpec struct {
	Version         int                       `json:"version"`
	GoalID          string                    `json:"goal_id"`
	Title           string                    `json:"title"`
	Instructions    string                    `json:"instructions,omitempty"`
	RequestedBy     string                    `json:"requested_by,omitempty"`
	SessionID       string                    `json:"session_id,omitempty"`
	Status          GoalStatus                `json:"status"`
	Priority        TaskPriority              `json:"priority,omitempty"`
	Constraints     []string                  `json:"constraints,omitempty"`
	SuccessCriteria []string                  `json:"success_criteria,omitempty"`
	Authority       TaskAuthority             `json:"authority,omitempty"`
	Budget          TaskBudget                `json:"budget,omitempty"`
	CreatedAt       int64                     `json:"created_at,omitempty"`
	UpdatedAt       int64                     `json:"updated_at,omitempty"`
	Meta            map[string]any            `json:"meta,omitempty"`
}

func (g GoalSpec) Normalize() GoalSpec {
	if g.Version == 0 {
		g.Version = 1
	}
	if !g.Status.Valid() {
		g.Status = GoalStatusPending
	}
	if strings.TrimSpace(string(g.Priority)) == "" {
		g.Priority = TaskPriorityMedium
	} else if !g.Priority.Valid() {
		g.Priority = TaskPriorityMedium
	}
	return g
}

func (g GoalSpec) Validate() error {
	if strings.TrimSpace(g.GoalID) == "" {
		return fmt.Errorf("goal_id is required")
	}
	if strings.TrimSpace(g.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if raw := strings.TrimSpace(string(g.Status)); raw != "" && !g.Status.Valid() {
		return fmt.Errorf("invalid goal status %q", g.Status)
	}
	if raw := strings.TrimSpace(string(g.Priority)); raw != "" && !g.Priority.Valid() {
		return fmt.Errorf("invalid goal priority %q", g.Priority)
	}
	return nil
}

// TaskSpec is the canonical persisted representation of a unit of work.
type TaskSpec struct {
	Version            int                       `json:"version"`
	TaskID             string                    `json:"task_id"`
	GoalID             string                    `json:"goal_id,omitempty"`
	ParentTaskID       string                    `json:"parent_task_id,omitempty"`
	PlanID             string                    `json:"plan_id,omitempty"`
	SessionID          string                    `json:"session_id,omitempty"`
	Title              string                    `json:"title"`
	Instructions       string                    `json:"instructions"`
	Inputs             map[string]any            `json:"inputs,omitempty"`
	ExpectedOutputs    []TaskOutputSpec          `json:"expected_outputs,omitempty"`
	AcceptanceCriteria []TaskAcceptanceCriterion `json:"acceptance_criteria,omitempty"`
	Dependencies       []string                  `json:"dependencies,omitempty"`
	AssignedAgent      string                    `json:"assigned_agent,omitempty"`
	CurrentRunID       string                    `json:"current_run_id,omitempty"`
	LastRunID          string                    `json:"last_run_id,omitempty"`
	Status             TaskStatus                `json:"status"`
	Priority           TaskPriority              `json:"priority,omitempty"`
	Authority          TaskAuthority             `json:"authority,omitempty"`
	MemoryScope        AgentMemoryScope          `json:"memory_scope,omitempty"`
	ToolProfile        string                    `json:"tool_profile,omitempty"`
	EnabledTools       []string                  `json:"enabled_tools,omitempty"`
	Budget             TaskBudget                `json:"budget,omitempty"`
	Verification       VerificationSpec          `json:"verification,omitempty"`
	CreatedAt          int64                     `json:"created_at,omitempty"`
	UpdatedAt          int64                     `json:"updated_at,omitempty"`
	Transitions        []TaskTransition          `json:"transitions,omitempty"`
	Meta               map[string]any            `json:"meta,omitempty"`
}

func (t TaskSpec) Normalize() TaskSpec {
	if t.Version == 0 {
		t.Version = 1
	}
	if !t.Status.Valid() {
		t.Status = TaskStatusPending
	}
	if strings.TrimSpace(string(t.Priority)) == "" {
		t.Priority = TaskPriorityMedium
	} else if !t.Priority.Valid() {
		t.Priority = TaskPriorityMedium
	}
	if t.MemoryScope != "" {
		t.MemoryScope = NormalizeAgentMemoryScope(string(t.MemoryScope))
	}
	return t
}

func (t TaskSpec) Validate() error {
	if strings.TrimSpace(t.TaskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(t.Instructions) == "" {
		return fmt.Errorf("instructions are required")
	}
	if raw := strings.TrimSpace(string(t.Status)); raw != "" && !t.Status.Valid() {
		return fmt.Errorf("invalid task status %q", t.Status)
	}
	if raw := strings.TrimSpace(string(t.Priority)); raw != "" && !t.Priority.Valid() {
		return fmt.Errorf("invalid task priority %q", t.Priority)
	}
	norm := t.Normalize()
	if norm.MemoryScope == "" && t.MemoryScope != "" {
		return fmt.Errorf("invalid memory_scope %q", t.MemoryScope)
	}
	for i, output := range t.ExpectedOutputs {
		if strings.TrimSpace(output.Name) == "" {
			return fmt.Errorf("expected_outputs[%d].name is required", i)
		}
	}
	for i, criterion := range t.AcceptanceCriteria {
		if strings.TrimSpace(criterion.Description) == "" {
			return fmt.Errorf("acceptance_criteria[%d].description is required", i)
		}
	}
	return nil
}

// TaskRun is the canonical persisted representation of a task execution attempt.
type TaskRun struct {
	Version       int            `json:"version"`
	RunID         string         `json:"run_id"`
	TaskID        string         `json:"task_id"`
	GoalID        string         `json:"goal_id,omitempty"`
	ParentRunID   string         `json:"parent_run_id,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	AgentID       string         `json:"agent_id,omitempty"`
	Attempt       int            `json:"attempt"`
	Status        TaskRunStatus  `json:"status"`
	StartedAt     int64          `json:"started_at,omitempty"`
	EndedAt       int64          `json:"ended_at,omitempty"`
	Trigger       string         `json:"trigger,omitempty"`
	CheckpointRef string              `json:"checkpoint_ref,omitempty"`
	Result        TaskResultRef       `json:"result,omitempty"`
	Error         string              `json:"error,omitempty"`
	Usage         TaskUsage           `json:"usage,omitempty"`
	Verification  VerificationSpec    `json:"verification,omitempty"`
	Transitions   []TaskRunTransition `json:"transitions,omitempty"`
	Meta          map[string]any      `json:"meta,omitempty"`
}

func (r TaskRun) Normalize() TaskRun {
	if r.Version == 0 {
		r.Version = 1
	}
	if r.Attempt <= 0 {
		r.Attempt = 1
	}
	if !r.Status.Valid() {
		r.Status = TaskRunStatusQueued
	}
	return r
}

func (r TaskRun) Validate() error {
	if strings.TrimSpace(r.RunID) == "" {
		return fmt.Errorf("run_id is required")
	}
	if strings.TrimSpace(r.TaskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if raw := strings.TrimSpace(string(r.Status)); raw != "" && !r.Status.Valid() {
		return fmt.Errorf("invalid run status %q", r.Status)
	}
	if r.Attempt < 0 {
		return fmt.Errorf("attempt must be >= 0")
	}
	return nil
}

// ── Plan schemas ───────────────────────────────────────────────────────────────

// PlanStatus describes the lifecycle state of a plan.
type PlanStatus string

const (
	PlanStatusDraft     PlanStatus = "draft"
	PlanStatusActive    PlanStatus = "active"
	PlanStatusRevising  PlanStatus = "revising"
	PlanStatusCompleted PlanStatus = "completed"
	PlanStatusFailed    PlanStatus = "failed"
	PlanStatusCancelled PlanStatus = "cancelled"
)

func ParsePlanStatus(raw string) (PlanStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PlanStatusDraft):
		return PlanStatusDraft, true
	case string(PlanStatusActive):
		return PlanStatusActive, true
	case string(PlanStatusRevising):
		return PlanStatusRevising, true
	case string(PlanStatusCompleted):
		return PlanStatusCompleted, true
	case string(PlanStatusFailed):
		return PlanStatusFailed, true
	case string(PlanStatusCancelled):
		return PlanStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizePlanStatus(raw string) PlanStatus {
	status, _ := ParsePlanStatus(raw)
	return status
}

func (s PlanStatus) Valid() bool {
	_, ok := ParsePlanStatus(string(s))
	return ok
}

// PlanStepStatus describes the state of an individual plan step.
type PlanStepStatus string

const (
	PlanStepStatusPending    PlanStepStatus = "pending"
	PlanStepStatusReady      PlanStepStatus = "ready"
	PlanStepStatusBlocked    PlanStepStatus = "blocked"
	PlanStepStatusInProgress PlanStepStatus = "in_progress"
	PlanStepStatusCompleted  PlanStepStatus = "completed"
	PlanStepStatusFailed     PlanStepStatus = "failed"
	PlanStepStatusSkipped    PlanStepStatus = "skipped"
)

func ParsePlanStepStatus(raw string) (PlanStepStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PlanStepStatusPending):
		return PlanStepStatusPending, true
	case string(PlanStepStatusReady):
		return PlanStepStatusReady, true
	case string(PlanStepStatusBlocked):
		return PlanStepStatusBlocked, true
	case string(PlanStepStatusInProgress):
		return PlanStepStatusInProgress, true
	case string(PlanStepStatusCompleted):
		return PlanStepStatusCompleted, true
	case string(PlanStepStatusFailed):
		return PlanStepStatusFailed, true
	case string(PlanStepStatusSkipped):
		return PlanStepStatusSkipped, true
	default:
		return "", false
	}
}

func NormalizePlanStepStatus(raw string) PlanStepStatus {
	status, _ := ParsePlanStepStatus(raw)
	return status
}

func (s PlanStepStatus) Valid() bool {
	_, ok := ParsePlanStepStatus(string(s))
	return ok
}

// PlanApprovalDecision describes the outcome of a plan approval review.
type PlanApprovalDecision string

const (
	PlanApprovalPending  PlanApprovalDecision = "pending"
	PlanApprovalApproved PlanApprovalDecision = "approved"
	PlanApprovalRejected PlanApprovalDecision = "rejected"
	PlanApprovalAmended  PlanApprovalDecision = "amended"
)

func ParsePlanApprovalDecision(raw string) (PlanApprovalDecision, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PlanApprovalPending), "":
		return PlanApprovalPending, true
	case string(PlanApprovalApproved):
		return PlanApprovalApproved, true
	case string(PlanApprovalRejected):
		return PlanApprovalRejected, true
	case string(PlanApprovalAmended):
		return PlanApprovalAmended, true
	default:
		return "", false
	}
}

func (d PlanApprovalDecision) Valid() bool {
	_, ok := ParsePlanApprovalDecision(string(d))
	return ok
}

// PlanApproval records a durable approval or rejection decision for a plan.
type PlanApproval struct {
	PlanID    string               `json:"plan_id"`
	Revision  int                  `json:"revision"`
	Decision  PlanApprovalDecision `json:"decision"`
	Actor     string               `json:"actor"`
	Reason    string               `json:"reason,omitempty"`
	CreatedAt int64                `json:"created_at"`
	Meta      map[string]any       `json:"meta,omitempty"`
}

// AutonomyMode controls how much latitude an agent has before requiring
// operator intervention. Plan approval requirements are keyed off this.
type AutonomyMode string

const (
	// AutonomyFull allows the agent to plan, execute, and complete tasks
	// without operator approval.
	AutonomyFull AutonomyMode = "full"

	// AutonomyPlanApproval requires operator approval of plans before
	// task compilation and execution begin. Execution is autonomous.
	AutonomyPlanApproval AutonomyMode = "plan_approval"

	// AutonomyStepApproval requires approval before each plan step is
	// compiled into a task.
	AutonomyStepApproval AutonomyMode = "step_approval"

	// AutonomySupervised requires approval of plans and individual tool
	// calls within task execution. Most restrictive mode.
	AutonomySupervised AutonomyMode = "supervised"
)

func ParseAutonomyMode(raw string) (AutonomyMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(AutonomyFull), "":
		return AutonomyFull, true
	case string(AutonomyPlanApproval):
		return AutonomyPlanApproval, true
	case string(AutonomyStepApproval):
		return AutonomyStepApproval, true
	case string(AutonomySupervised):
		return AutonomySupervised, true
	default:
		return "", false
	}
}

func NormalizeAutonomyMode(raw string) AutonomyMode {
	mode, ok := ParseAutonomyMode(raw)
	if !ok {
		return AutonomyFull
	}
	return mode
}

func (m AutonomyMode) Valid() bool {
	_, ok := ParseAutonomyMode(string(m))
	return ok
}

// RequiresPlanApproval reports whether this mode requires plan-level
// approval before execution begins.
func (m AutonomyMode) RequiresPlanApproval() bool {
	switch m {
	case AutonomyPlanApproval, AutonomyStepApproval, AutonomySupervised:
		return true
	}
	return false
}

// RequiresStepApproval reports whether this mode requires per-step
// approval before task compilation.
func (m AutonomyMode) RequiresStepApproval() bool {
	switch m {
	case AutonomyStepApproval, AutonomySupervised:
		return true
	}
	return false
}

// PlanStep describes one unit of work inside a plan.
type PlanStep struct {
	StepID       string           `json:"step_id"`
	Title        string           `json:"title"`
	Instructions string           `json:"instructions,omitempty"`
	DependsOn    []string         `json:"depends_on,omitempty"`
	Status       PlanStepStatus   `json:"status"`
	TaskID       string           `json:"task_id,omitempty"`
	Agent        string           `json:"agent,omitempty"`
	Outputs      []TaskOutputSpec `json:"outputs,omitempty"`
	Meta         map[string]any   `json:"meta,omitempty"`
}

func (s PlanStep) Normalize() PlanStep {
	if !s.Status.Valid() {
		s.Status = PlanStepStatusPending
	}
	return s
}

func (s PlanStep) Validate() error {
	if strings.TrimSpace(s.StepID) == "" {
		return fmt.Errorf("step_id is required")
	}
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("step title is required")
	}
	if raw := strings.TrimSpace(string(s.Status)); raw != "" && !s.Status.Valid() {
		return fmt.Errorf("invalid step status %q", s.Status)
	}
	for i, out := range s.Outputs {
		if strings.TrimSpace(out.Name) == "" {
			return fmt.Errorf("step %q outputs[%d].name is required", s.StepID, i)
		}
	}
	return nil
}

// PlanSpec is the canonical persisted representation of a task decomposition plan.
type PlanSpec struct {
	Version          int            `json:"version"`
	PlanID           string         `json:"plan_id"`
	GoalID           string         `json:"goal_id,omitempty"`
	Title            string         `json:"title"`
	Revision         int            `json:"revision"`
	Status           PlanStatus     `json:"status"`
	Steps            []PlanStep     `json:"steps"`
	Assumptions      []string       `json:"assumptions,omitempty"`
	Risks            []string       `json:"risks,omitempty"`
	RollbackStrategy string         `json:"rollback_strategy,omitempty"`
	CreatedAt        int64          `json:"created_at,omitempty"`
	UpdatedAt        int64          `json:"updated_at,omitempty"`
	Meta             map[string]any `json:"meta,omitempty"`
}

func (p PlanSpec) Normalize() PlanSpec {
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Revision <= 0 {
		p.Revision = 1
	}
	if !p.Status.Valid() {
		p.Status = PlanStatusDraft
	}
	for i := range p.Steps {
		p.Steps[i] = p.Steps[i].Normalize()
	}
	return p
}

func (p PlanSpec) Validate() error {
	if strings.TrimSpace(p.PlanID) == "" {
		return fmt.Errorf("plan_id is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("plan title is required")
	}
	if raw := strings.TrimSpace(string(p.Status)); raw != "" && !p.Status.Valid() {
		return fmt.Errorf("invalid plan status %q", p.Status)
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("plan must have at least one step")
	}
	stepIDs := make(map[string]bool, len(p.Steps))
	for i, step := range p.Steps {
		if err := step.Validate(); err != nil {
			return fmt.Errorf("steps[%d]: %w", i, err)
		}
		if stepIDs[step.StepID] {
			return fmt.Errorf("duplicate step_id %q at steps[%d]", step.StepID, i)
		}
		stepIDs[step.StepID] = true
	}
	// Validate dependency references.
	for i, step := range p.Steps {
		for _, dep := range step.DependsOn {
			if !stepIDs[dep] {
				return fmt.Errorf("steps[%d] depends_on unknown step_id %q", i, dep)
			}
			if dep == step.StepID {
				return fmt.Errorf("steps[%d] depends on itself", i)
			}
		}
	}
	return nil
}

// HasCycle reports whether the step dependency graph contains a cycle.
func (p PlanSpec) HasCycle() bool {
	adj := make(map[string][]string, len(p.Steps))
	for _, step := range p.Steps {
		adj[step.StepID] = step.DependsOn
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(p.Steps))
	var dfs func(string) bool
	dfs = func(id string) bool {
		color[id] = gray
		for _, dep := range adj[id] {
			switch color[dep] {
			case gray:
				return true
			case white:
				if dfs(dep) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for _, step := range p.Steps {
		if color[step.StepID] == white {
			if dfs(step.StepID) {
				return true
			}
		}
	}
	return false
}

// ReadySteps returns steps whose status is pending and whose dependencies
// are all completed or skipped.
func (p PlanSpec) ReadySteps() []PlanStep {
	statusByID := make(map[string]PlanStepStatus, len(p.Steps))
	for _, step := range p.Steps {
		statusByID[step.StepID] = step.Status
	}
	var ready []PlanStep
	for _, step := range p.Steps {
		if step.Status != PlanStepStatusPending {
			continue
		}
		allDone := true
		for _, dep := range step.DependsOn {
			ds := statusByID[dep]
			if ds != PlanStepStatusCompleted && ds != PlanStepStatusSkipped {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, step)
		}
	}
	return ready
}

// IsTerminal reports whether the plan is in a terminal state.
func (p PlanSpec) IsTerminal() bool {
	switch p.Status {
	case PlanStatusCompleted, PlanStatusFailed, PlanStatusCancelled:
		return true
	}
	return false
}

// ── Workflow journal schemas ──────────────────────────────────────────────────

// WorkflowJournalDoc is the persisted representation of a task run's execution
// journal. It is stored as a replaceable state doc keyed by run ID.
// On each append the full doc is re-persisted; the entry list is bounded by
// the runtime (see WorkflowJournal.maxEntries).
type WorkflowJournalDoc struct {
	Version    int                       `json:"version"`
	TaskID     string                    `json:"task_id"`
	RunID      string                    `json:"run_id"`
	Entries    []WorkflowJournalEntryDoc `json:"entries,omitempty"`
	Checkpoint *WorkflowCheckpointDoc    `json:"checkpoint,omitempty"`
	NextSeq    int64                     `json:"next_seq"`
	UpdatedAt  int64                     `json:"updated_at,omitempty"`
}

// WorkflowJournalEntryDoc is a single journal entry within a workflow journal.
type WorkflowJournalEntryDoc struct {
	EntryID   string         `json:"entry_id"`
	Sequence  int64          `json:"sequence"`
	Type      string         `json:"type"`
	CreatedAt int64          `json:"created_at"`
	Summary   string         `json:"summary,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// WorkflowCheckpointDoc captures the resumable state of a task run at a point
// in time. It records accumulated progress and pending work so a crashed run
// can resume from the last checkpoint instead of replaying the full history.
type WorkflowCheckpointDoc struct {
	StepID         string         `json:"step_id,omitempty"`
	Attempt        int            `json:"attempt"`
	Status         string         `json:"status"`
	Usage          TaskUsage      `json:"usage,omitempty"`
	PendingActions []PendingActionDoc `json:"pending_actions,omitempty"`
	CreatedAt      int64          `json:"created_at"`
	Meta           map[string]any `json:"meta,omitempty"`
}

// PendingActionDoc describes a deferred action that was scheduled but not yet
// executed when the checkpoint was taken.
type PendingActionDoc struct {
	ActionID    string         `json:"action_id"`
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	CreatedAt   int64          `json:"created_at"`
}
