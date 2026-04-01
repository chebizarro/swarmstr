// Package acp implements Metiq's Agent Control Protocol.
//
// ACP uses Nostr-native NIP-17 encrypted DMs between agent keypairs to
// coordinate multi-agent workflows without any central broker.
//
// Message flow:
//
//	Director                              Worker
//	  │                                    │
//	  │── acp_dispatch DM ──────────────►  │
//	  │   {type:"task", task_id, payload}  │
//	  │                                    │  (agent processes task)
//	  │◄─ acp_result DM ────────────────── │
//	  │   {type:"result", task_id, ...}    │
//
// Agents advertise their pubkey out-of-band (e.g. via the nodes list or
// shared config).  The recipient recognises an ACP message because its JSON
// starts with an "acp_type" discriminator field.
package acp

import (
	"encoding/json"
	"time"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

// Version is the current ACP wire format version.
const Version = 1

// Message is an ACP control message exchanged between agent DM keypairs.
type Message struct {
	// ACPType discriminates this message as ACP (value: "task" | "result" | "ping" | "pong").
	ACPType string `json:"acp_type"`
	// Version is the ACP protocol version.
	Version int `json:"acp_version"`
	// TaskID ties a result back to the originating task.
	TaskID string `json:"task_id,omitempty"`
	// SenderPubKey is the Nostr pubkey of the sender (hex, no-prefix).
	SenderPubKey string `json:"sender_pubkey,omitempty"`
	// Payload is message-type–specific data.
	Payload map[string]any `json:"payload,omitempty"`
	// CreatedAt is the Unix timestamp when the message was created.
	CreatedAt int64 `json:"created_at,omitempty"`
}

// TaskPayload is the Payload for messages with ACPType = "task".
type ParentContext struct {
	// SessionID identifies the parent session that originated the task.
	SessionID string `json:"session_id,omitempty"`
	// AgentID identifies the parent agent/runtime that originated the task.
	AgentID string `json:"agent_id,omitempty"`
}

type TaskPayload struct {
	// Instructions is the natural-language task description for the worker agent.
	Instructions string `json:"instructions"`
	// ContextMessages is an optional slice of prior messages to seed context.
	ContextMessages []map[string]any `json:"context_messages,omitempty"`
	// MemoryScope carries the explicit worker memory scope contract.
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	// ToolProfile carries the inherited worker tool profile contract.
	ToolProfile string `json:"tool_profile,omitempty"`
	// EnabledTools carries an explicit inherited tool allowlist.
	EnabledTools []string `json:"enabled_tools,omitempty"`
	// ParentContext carries optional metadata about the originating runtime.
	ParentContext *ParentContext `json:"parent_context,omitempty"`
	// TimeoutMS, when > 0, sets the maximum processing time in milliseconds.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	// ReplyTo is the Nostr pubkey the worker should send its result DM to.
	ReplyTo string `json:"reply_to,omitempty"`
}

// ResultPayload is the Payload for messages with ACPType = "result".
type WorkerMetadata struct {
	// SessionID identifies the worker-side session that processed the task.
	SessionID string `json:"session_id,omitempty"`
	// AgentID identifies the worker-side agent/runtime that processed the task.
	AgentID string `json:"agent_id,omitempty"`
	// ParentContext preserves the originating parent context when the worker
	// reflects completion metadata back to the caller.
	ParentContext *ParentContext `json:"parent_context,omitempty"`
	// HistoryEntryIDs identifies the worker transcript entries persisted for this
	// task in order, including inherited seed history and turn-produced messages.
	HistoryEntryIDs []string `json:"history_entry_ids,omitempty"`
	// TurnResult carries canonical terminal completion metadata aligned with the
	// shared runtime taxonomy.
	TurnResult *agent.TurnResultMetadata `json:"turn_result,omitempty"`
}

type ResultPayload struct {
	// Text is the agent's text response.
	Text string `json:"text"`
	// Error, when non-empty, indicates the task failed.
	Error string `json:"error,omitempty"`
	// TokensUsed is an optional token usage hint.
	TokensUsed int `json:"tokens_used,omitempty"`
	// CompletedAt is the Unix timestamp of task completion.
	CompletedAt int64 `json:"completed_at,omitempty"`
	// Worker carries the worker-side session/history/completion metadata needed
	// to correlate parent/worker execution after the fact.
	Worker *WorkerMetadata `json:"worker,omitempty"`
}

// NewTask builds a task Message ready to send.
func NewTask(taskID, senderPubKey string, p TaskPayload) Message {
	return Message{
		ACPType:      "task",
		Version:      Version,
		TaskID:       taskID,
		SenderPubKey: senderPubKey,
		Payload: map[string]any{
			"instructions":     p.Instructions,
			"context_messages": p.ContextMessages,
			"memory_scope":     p.MemoryScope,
			"tool_profile":     p.ToolProfile,
			"enabled_tools":    p.EnabledTools,
			"parent_context":   p.ParentContext,
			"timeout_ms":       p.TimeoutMS,
			"reply_to":         p.ReplyTo,
		},
		CreatedAt: time.Now().Unix(),
	}
}

// DecodeTaskPayload normalizes a generic ACP payload map into the typed task payload.
func DecodeTaskPayload(payload map[string]any) (TaskPayload, error) {
	if payload == nil {
		return TaskPayload{}, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return TaskPayload{}, err
	}
	var out TaskPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		return TaskPayload{}, err
	}
	return out, nil
}

// NewResult builds a result Message in response to a task.
func NewResult(taskID, senderPubKey string, p ResultPayload) Message {
	return Message{
		ACPType:      "result",
		Version:      Version,
		TaskID:       taskID,
		SenderPubKey: senderPubKey,
		Payload: map[string]any{
			"text":         p.Text,
			"error":        p.Error,
			"tokens_used":  p.TokensUsed,
			"completed_at": p.CompletedAt,
			"worker":       p.Worker,
		},
		CreatedAt: time.Now().Unix(),
	}
}

// DecodeResultPayload normalizes a generic ACP payload map into the typed result payload.
func DecodeResultPayload(payload map[string]any) (ResultPayload, error) {
	if payload == nil {
		return ResultPayload{}, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ResultPayload{}, err
	}
	var out ResultPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		return ResultPayload{}, err
	}
	return out, nil
}

// IsACPMessage reports whether raw JSON bytes look like an ACP message.
// It does a cheap prefix scan before attempting a full JSON parse.
func IsACPMessage(raw []byte) bool {
	for i, b := range raw {
		if b == '{' {
			// Scan forward to find "acp_type" key.
			rest := raw[i:]
			for j := 0; j < len(rest)-10; j++ {
				if rest[j] == '"' && j+10 < len(rest) {
					if string(rest[j:j+10]) == `"acp_type"` {
						return true
					}
				}
			}
			return false
		}
	}
	return false
}
