package state

// SessionPlacementDoc is the durable execution ownership record for a session.
// Generation monotonically fences stale dispatch and reclaim operations.
type SessionPlacementDoc struct {
	Version           int    `json:"version"`
	SessionID         string `json:"session_id"`
	State             string `json:"state"`
	Generation        uint64 `json:"generation"`
	AgentID           string `json:"agent_id,omitempty"`
	Backend           string `json:"backend,omitempty"`
	OwnerConnectionID string `json:"owner_connection_id,omitempty"`
	OwnerSubject      string `json:"owner_subject,omitempty"`
	PreviousAgentID   string `json:"previous_agent_id,omitempty"`
	PreviousBackend   string `json:"previous_backend,omitempty"`
	CreatedAtMS       int64  `json:"created_at_ms"`
	UpdatedAtMS       int64  `json:"updated_at_ms"`
	StateChangedAtMS  int64  `json:"state_changed_at_ms"`
	ReclaimReason     string `json:"reclaim_reason,omitempty"`
}
