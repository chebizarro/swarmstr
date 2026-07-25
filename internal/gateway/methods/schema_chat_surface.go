package methods

// Param schemas for the chat control-UI surface (swarmstr-viqq):
// chat.startup / chat.metadata / chat.message.get / chat.toolTitles.
//
// Shapes mirror OpenClaw src/gateway/server-methods/chat*.ts. All four accept a
// session identifier; Metiq resolves sessionKey (OpenClaw naming) or the native
// session_id/sessionId aliases interchangeably.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxChatToolTitleItems bounds one chat.toolTitles batch.
const maxChatToolTitleItems = 256

func resolveSessionIdentifier(sessionKey, sessionID string) string {
	if v := strings.TrimSpace(sessionKey); v != "" {
		return v
	}
	return strings.TrimSpace(sessionID)
}

// ChatStartupRequest returns bootstrap chat state for a session.
type ChatStartupRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Key        string `json:"-"`
}

// ChatMetadataRequest returns per-session chat metadata.
type ChatMetadataRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Key        string `json:"-"`
}

// ChatMessageGetRequest fetches one transcript entry by id.
type ChatMessageGetRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	MessageID  string `json:"messageId"`
	MaxChars   int    `json:"maxChars,omitempty"`
	Key        string `json:"-"`
}

// ChatToolTitleItem is one tool-call requiring a display title.
type ChatToolTitleItem struct {
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
}

// ChatToolTitlesRequest requests display titles for a batch of tool calls.
type ChatToolTitlesRequest struct {
	SessionKey string              `json:"sessionKey,omitempty"`
	SessionID  string              `json:"session_id,omitempty"`
	Items      []ChatToolTitleItem `json:"items"`
	Key        string              `json:"-"`
}

func (r ChatStartupRequest) Normalize() (ChatStartupRequest, error) {
	r.Key = resolveSessionIdentifier(r.SessionKey, r.SessionID)
	if r.Key == "" {
		return r, fmt.Errorf("invalid chat.startup params: sessionKey is required")
	}
	if r.Limit < 0 {
		return r, fmt.Errorf("invalid chat.startup params: limit must not be negative")
	}
	return r, nil
}

func (r ChatMetadataRequest) Normalize() (ChatMetadataRequest, error) {
	r.Key = resolveSessionIdentifier(r.SessionKey, r.SessionID)
	if r.Key == "" {
		return r, fmt.Errorf("invalid chat.metadata params: sessionKey is required")
	}
	return r, nil
}

func (r ChatMessageGetRequest) Normalize() (ChatMessageGetRequest, error) {
	r.Key = resolveSessionIdentifier(r.SessionKey, r.SessionID)
	r.MessageID = strings.TrimSpace(r.MessageID)
	if r.Key == "" {
		return r, fmt.Errorf("invalid chat.message.get params: sessionKey is required")
	}
	if r.MessageID == "" {
		return r, fmt.Errorf("invalid chat.message.get params: messageId is required")
	}
	if r.MaxChars < 0 {
		return r, fmt.Errorf("invalid chat.message.get params: maxChars must not be negative")
	}
	return r, nil
}

func (r ChatToolTitlesRequest) Normalize() (ChatToolTitlesRequest, error) {
	r.Key = resolveSessionIdentifier(r.SessionKey, r.SessionID)
	if len(r.Items) > maxChatToolTitleItems {
		return r, fmt.Errorf("invalid chat.toolTitles params: items exceeds %d entries", maxChatToolTitleItems)
	}
	cleaned := make([]ChatToolTitleItem, 0, len(r.Items))
	for _, item := range r.Items {
		item.ToolCallID = strings.TrimSpace(item.ToolCallID)
		item.ToolName = strings.TrimSpace(item.ToolName)
		if item.ToolCallID == "" {
			return r, fmt.Errorf("invalid chat.toolTitles params: toolCallId is required")
		}
		cleaned = append(cleaned, item)
	}
	r.Items = cleaned
	return r, nil
}

func DecodeChatStartupParams(params json.RawMessage) (ChatStartupRequest, error) {
	return decodeMethodParams[ChatStartupRequest](params)
}

func DecodeChatMetadataParams(params json.RawMessage) (ChatMetadataRequest, error) {
	return decodeMethodParams[ChatMetadataRequest](params)
}

func DecodeChatMessageGetParams(params json.RawMessage) (ChatMessageGetRequest, error) {
	return decodeMethodParams[ChatMessageGetRequest](params)
}

func DecodeChatToolTitlesParams(params json.RawMessage) (ChatToolTitlesRequest, error) {
	return decodeMethodParams[ChatToolTitlesRequest](params)
}

// BuildToolTitles derives deterministic display titles for a batch of tool
// calls, keyed by toolCallId. Metiq deviation: OpenClaw optionally spends
// utility-model tokens to summarize tool calls; Metiq returns a deterministic
// humanized title derived from the tool name (no model runtime, no token cost).
func BuildToolTitles(items []ChatToolTitleItem) map[string]string {
	titles := make(map[string]string, len(items))
	for _, item := range items {
		titles[item.ToolCallID] = humanizeToolName(item.ToolName)
	}
	return titles
}

func humanizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Tool call"
	}
	// Split on common separators (dot, underscore, hyphen, slash) and title-case
	// each word: "sessions.history" -> "Sessions History", "read_file" -> "Read File".
	replacer := strings.NewReplacer(".", " ", "_", " ", "-", " ", "/", " ", ":", " ")
	words := strings.Fields(replacer.Replace(name))
	if len(words) == 0 {
		return "Tool call"
	}
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
