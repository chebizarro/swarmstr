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
	"time"

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
type TaskPayload struct {
	// Instructions is the natural-language task description for the worker agent.
	Instructions string `json:"instructions"`
	// ContextMessages is an optional slice of prior messages to seed context.
	ContextMessages []map[string]any `json:"context_messages,omitempty"`
	// MemoryScope carries the explicit worker memory scope contract.
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	// TimeoutMS, when > 0, sets the maximum processing time in milliseconds.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	// ReplyTo is the Nostr pubkey the worker should send its result DM to.
	ReplyTo string `json:"reply_to,omitempty"`
}

// ResultPayload is the Payload for messages with ACPType = "result".
type ResultPayload struct {
	// Text is the agent's text response.
	Text string `json:"text"`
	// Error, when non-empty, indicates the task failed.
	Error string `json:"error,omitempty"`
	// TokensUsed is an optional token usage hint.
	TokensUsed int `json:"tokens_used,omitempty"`
	// CompletedAt is the Unix timestamp of task completion.
	CompletedAt int64 `json:"completed_at,omitempty"`
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
			"timeout_ms":       p.TimeoutMS,
			"reply_to":         p.ReplyTo,
		},
		CreatedAt: time.Now().Unix(),
	}
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
		},
		CreatedAt: time.Now().Unix(),
	}
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
