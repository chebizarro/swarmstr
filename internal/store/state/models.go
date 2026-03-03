package state

// ConfigDoc is the canonical runtime configuration persisted to Nostr.
type ConfigDoc struct {
	Version int            `json:"version"`
	DM      DMPolicy       `json:"dm"`
	Relays  RelayPolicy    `json:"relays"`
	Agent   AgentPolicy    `json:"agent"`
	Control ControlPolicy  `json:"control,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
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
