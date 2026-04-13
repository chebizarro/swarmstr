package state

const (
	NostrChannelKindDM          NostrChannelKind = "dm"
	NostrChannelKindNIP28       NostrChannelKind = "nip28"
	NostrChannelKindNIP29       NostrChannelKind = "nip29"
	NostrChannelKindChat        NostrChannelKind = "chat" // NIP-C7 kind:9 chat
	NostrChannelKindRelayFilter NostrChannelKind = "relay-filter"
	NostrChannelKindNIP34Inbox  NostrChannelKind = "nip34-inbox"
)

// NostrChannelConfig describes a single Nostr transport subscription.
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
	Confidence float64 `json:"confidence,omitempty"`  // 0.0–1.0; 0 means unset (defaults to 0.5)
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
