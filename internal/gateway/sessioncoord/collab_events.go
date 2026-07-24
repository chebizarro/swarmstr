package sessioncoord

// Collaboration event names (OpenClaw gateway-protocol parity).
const (
	EventSessionSuggestion = "session.suggestion"
	EventSessionTyping     = "session.typing"
)

// Suggestion is the wire shape of one session suggestion.
type Suggestion struct {
	ID         string   `json:"id"`
	SessionKey string   `json:"sessionKey"`
	AgentID    string   `json:"agentId"`
	Author     Identity `json:"author"`
	Text       string   `json:"text"`
	CreatedAt  int64    `json:"createdAt"`
	State      string   `json:"state"`
}

// SessionSuggestionEventPayload is the session.suggestion event body.
type SessionSuggestionEventPayload struct {
	Action     string     `json:"action"` // "added" | "resolved"
	Suggestion Suggestion `json:"suggestion"`
}

// SessionTypingEventPayload is the session.typing event body.
type SessionTypingEventPayload struct {
	SessionKey string   `json:"sessionKey"`
	SessionID  string   `json:"sessionId"`
	AgentID    string   `json:"agentId"`
	Actor      Identity `json:"actor"`
	Typing     bool     `json:"typing"`
	TS         int64    `json:"ts"`
}
