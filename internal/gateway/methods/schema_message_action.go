package methods

// Param schema for message.action (swarmstr-ko2f): a single verb-dispatched
// mutation over one transcript entry identified by {sessionKey, messageId}.
// Verbs: react / edit / delete / retry. Mirrors OpenClaw
// src/gateway/server-methods message.action, backed by metiq's durable
// per-session transcript store (TranscriptRepository). Published Nostr entries
// also carry event/relay provenance so delete and reaction-add actions propagate
// as NIP-09 kind 5 and NIP-25 kind 7 events before the local mutation.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// message.action verbs.
const (
	MessageActionVerbReact  = "react"
	MessageActionVerbEdit   = "edit"
	MessageActionVerbDelete = "delete"
	MessageActionVerbRetry  = "retry"
)

// MessageActionReactor is the default reaction actor when the caller does not
// name one. message.action is OperatorAdmin, so an unattributed reaction is the
// operator's.
const MessageActionReactor = "operator"

// MessageActionRequest is the unified message.action request. It dispatches on
// Verb over the transcript entry {Key, MessageID}. Verb-specific fields are
// validated per verb in Normalize.
type MessageActionRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	MessageID  string `json:"messageId"`
	Verb       string `json:"verb"`

	// edit
	Text string `json:"text,omitempty"`

	// react
	Reaction string `json:"reaction,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Remove   bool   `json:"remove,omitempty"`

	// retry
	AgentID        string `json:"agentId,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`

	Key string `json:"-"`
}

func (r MessageActionRequest) Normalize() (MessageActionRequest, error) {
	r.Key = resolveSessionIdentifier(r.SessionKey, r.SessionID)
	r.MessageID = strings.TrimSpace(r.MessageID)
	r.Verb = strings.ToLower(strings.TrimSpace(r.Verb))
	if r.Key == "" {
		return r, fmt.Errorf("invalid message.action params: sessionKey is required")
	}
	if r.MessageID == "" {
		return r, fmt.Errorf("invalid message.action params: messageId is required")
	}
	switch r.Verb {
	case MessageActionVerbReact:
		r.Reaction = strings.TrimSpace(r.Reaction)
		r.Actor = strings.TrimSpace(r.Actor)
		if r.Actor == "" {
			r.Actor = MessageActionReactor
		}
		if r.Reaction == "" {
			return r, fmt.Errorf("invalid message.action params: reaction is required for verb %q", r.Verb)
		}
	case MessageActionVerbEdit:
		r.Text = strings.TrimSpace(r.Text)
		if r.Text == "" {
			return r, fmt.Errorf("invalid message.action params: text is required for verb %q", r.Verb)
		}
	case MessageActionVerbDelete:
		// no verb-specific params
	case MessageActionVerbRetry:
		r.AgentID = strings.TrimSpace(r.AgentID)
		r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	case "":
		return r, fmt.Errorf("invalid message.action params: verb is required")
	default:
		return r, fmt.Errorf("invalid message.action params: unknown verb %q (want react|edit|delete|retry)", r.Verb)
	}
	return r, nil
}

func DecodeMessageActionParams(params json.RawMessage) (MessageActionRequest, error) {
	return decodeMethodParams[MessageActionRequest](params)
}
