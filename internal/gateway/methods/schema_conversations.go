package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Conversation method schemas. Params mirror the OpenClaw conversations.*
// wire contract; delivery reuses the daemon's channel send internals and the
// registry in internal/gateway/conversations.

const (
	conversationTurnMaxTimeoutMS = 300_000
	conversationListMaxLimit     = 100
	conversationListDefaultLimit = 50
)

type ConversationsListRequest struct {
	AgentID string `json:"agent_id"`
	Channel string `json:"channel,omitempty"`
	Query   string `json:"query,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type ConversationsSendRequest struct {
	AgentID          string `json:"agent_id"`
	SourceSessionKey string `json:"source_session_key,omitempty"`
	OperationID      string `json:"operationId"`
	ConversationRef  string `json:"conversationRef"`
	Message          string `json:"message"`
}

type ConversationsTurnRequest struct {
	AgentID          string `json:"agent_id"`
	SourceSessionKey string `json:"source_session_key,omitempty"`
	TurnID           string `json:"turnId"`
	ConversationRef  string `json:"conversationRef"`
	Message          string `json:"message"`
	TimeoutMS        int    `json:"timeout_ms"`
}

type ConversationsTurnCancelRequest struct {
	AgentID string `json:"agent_id"`
	TurnID  string `json:"turnId"`
}

func (r ConversationsListRequest) Normalize() (ConversationsListRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.Channel = strings.TrimSpace(r.Channel)
	r.Query = strings.TrimSpace(r.Query)
	if r.AgentID == "" {
		return r, fmt.Errorf("invalid conversations.list params: agentId is required")
	}
	if r.Limit < 0 || r.Limit > conversationListMaxLimit {
		return r, fmt.Errorf("invalid conversations.list params: limit must be 1-%d", conversationListMaxLimit)
	}
	if r.Limit == 0 {
		r.Limit = conversationListDefaultLimit
	}
	return r, nil
}

func (r ConversationsSendRequest) Normalize() (ConversationsSendRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SourceSessionKey = strings.TrimSpace(r.SourceSessionKey)
	r.OperationID = strings.TrimSpace(r.OperationID)
	r.ConversationRef = strings.TrimSpace(r.ConversationRef)
	if r.AgentID == "" {
		return r, fmt.Errorf("invalid conversations.send params: agentId is required")
	}
	if r.OperationID == "" {
		return r, fmt.Errorf("invalid conversations.send params: operationId is required")
	}
	if r.ConversationRef == "" {
		return r, fmt.Errorf("invalid conversations.send params: conversationRef is required")
	}
	if strings.TrimSpace(r.Message) == "" {
		return r, fmt.Errorf("invalid conversations.send params: message is required")
	}
	return r, nil
}

func (r ConversationsTurnRequest) Normalize() (ConversationsTurnRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SourceSessionKey = strings.TrimSpace(r.SourceSessionKey)
	r.TurnID = strings.TrimSpace(r.TurnID)
	r.ConversationRef = strings.TrimSpace(r.ConversationRef)
	if r.AgentID == "" {
		return r, fmt.Errorf("invalid conversations.turn params: agentId is required")
	}
	if r.TurnID == "" {
		return r, fmt.Errorf("invalid conversations.turn params: turnId is required")
	}
	if r.ConversationRef == "" {
		return r, fmt.Errorf("invalid conversations.turn params: conversationRef is required")
	}
	if strings.TrimSpace(r.Message) == "" {
		return r, fmt.Errorf("invalid conversations.turn params: message is required")
	}
	if r.TimeoutMS < 1 || r.TimeoutMS > conversationTurnMaxTimeoutMS {
		return r, fmt.Errorf("invalid conversations.turn params: timeoutMs must be 1-%d", conversationTurnMaxTimeoutMS)
	}
	return r, nil
}

func (r ConversationsTurnCancelRequest) Normalize() (ConversationsTurnCancelRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.TurnID = strings.TrimSpace(r.TurnID)
	if r.AgentID == "" {
		return r, fmt.Errorf("invalid conversations.turn.cancel params: agentId is required")
	}
	if r.TurnID == "" {
		return r, fmt.Errorf("invalid conversations.turn.cancel params: turnId is required")
	}
	return r, nil
}

func DecodeConversationsListParams(params json.RawMessage) (ConversationsListRequest, error) {
	return decodeMethodParams[ConversationsListRequest](params)
}

func DecodeConversationsSendParams(params json.RawMessage) (ConversationsSendRequest, error) {
	return decodeMethodParams[ConversationsSendRequest](params)
}

func DecodeConversationsTurnParams(params json.RawMessage) (ConversationsTurnRequest, error) {
	return decodeMethodParams[ConversationsTurnRequest](params)
}

func DecodeConversationsTurnCancelParams(params json.RawMessage) (ConversationsTurnCancelRequest, error) {
	return decodeMethodParams[ConversationsTurnCancelRequest](params)
}
