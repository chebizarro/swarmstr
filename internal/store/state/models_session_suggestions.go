package state

// SessionSuggestionEntry is one durable collaboration suggestion.
type SessionSuggestionEntry struct {
	ID           string `json:"id"`
	AuthorID     string `json:"author_id"`
	AuthorLabel  string `json:"author_label,omitempty"`
	Text         string `json:"text"`
	CreatedAtMS  int64  `json:"created_at_ms"`
	State        string `json:"state"` // "pending" | "accepted" | "dismissed"
	Resolution   string `json:"resolution,omitempty"`
	ResolvedBy   string `json:"resolved_by,omitempty"`
	ResolvedAtMS int64  `json:"resolved_at_ms,omitempty"`
}

// SessionSuggestionsDoc is the durable per-session suggestion queue.
type SessionSuggestionsDoc struct {
	Version     int                      `json:"version"`
	SessionID   string                   `json:"session_id"`
	Suggestions []SessionSuggestionEntry `json:"suggestions,omitempty"`
	UpdatedAtMS int64                    `json:"updated_at_ms"`
}
