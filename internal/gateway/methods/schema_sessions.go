package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"metiq/internal/store/state"
	"strings"
	"unicode/utf8"
)

// AgentInternalEvent represents a task completion event from a subagent or
// cron job, forwarded into the next agent turn as context.
type AgentInternalEvent struct {
	Type             string `json:"type"`   // "task_completion"
	Source           string `json:"source"` // "subagent" | "cron"
	ChildSessionKey  string `json:"child_session_key"`
	ChildSessionID   string `json:"child_session_id,omitempty"`
	AnnounceType     string `json:"announce_type"`
	TaskLabel        string `json:"task_label"`
	Status           string `json:"status"` // "ok" | "timeout" | "error" | "unknown"
	StatusLabel      string `json:"status_label"`
	Result           string `json:"result"`
	StatsLine        string `json:"stats_line,omitempty"`
	ReplyInstruction string `json:"reply_instruction"`
}

// InputProvenance tracks the origin of a message so the agent can reason
// about where its input came from.
type InputProvenance struct {
	Kind             string `json:"kind"`
	OriginSessionID  string `json:"origin_session_id,omitempty"`
	SourceSessionKey string `json:"source_session_key,omitempty"`
	SourceChannel    string `json:"source_channel,omitempty"`
	SourceTool       string `json:"source_tool,omitempty"`
}

type AgentRequest struct {
	SessionID         string                 `json:"session_id,omitempty"`
	SessionKey        string                 `json:"sessionKey,omitempty"`
	Message           string                 `json:"message"`
	Context           string                 `json:"context,omitempty"`
	MemoryScope       state.AgentMemoryScope `json:"memory_scope,omitempty"`
	TimeoutMS         int                    `json:"timeout_ms,omitempty"`
	AgentID           string                 `json:"agent_id,omitempty"`
	To                string                 `json:"to,omitempty"`
	ReplyTo           string                 `json:"reply_to,omitempty"`
	Thinking          string                 `json:"thinking,omitempty"`
	Deliver           *bool                  `json:"deliver,omitempty"`
	Attachments       []AttachmentInput      `json:"attachments,omitempty"`
	Channel           string                 `json:"channel,omitempty"`
	ReplyChannel      string                 `json:"reply_channel,omitempty"`
	AccountID         string                 `json:"account_id,omitempty"`
	ReplyAccountID    string                 `json:"reply_account_id,omitempty"`
	ThreadID          string                 `json:"thread_id,omitempty"`
	GroupID           string                 `json:"group_id,omitempty"`
	GroupChannel      string                 `json:"group_channel,omitempty"`
	GroupSpace        string                 `json:"group_space,omitempty"`
	BestEffortDeliver *bool                  `json:"best_effort_deliver,omitempty"`
	Lane              string                 `json:"lane,omitempty"`
	ExtraSystemPrompt string                 `json:"extra_system_prompt,omitempty"`
	InternalEvents    []AgentInternalEvent   `json:"internal_events,omitempty"`
	InputProvenance   *InputProvenance       `json:"input_provenance,omitempty"`
	IdempotencyKey    string                 `json:"idempotency_key,omitempty"`
	Label             string                 `json:"label,omitempty"`
}

type AgentWaitRequest struct {
	RunID     string `json:"run_id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type AgentIdentityRequest struct {
	SessionID  string `json:"session_id,omitempty"`
	SessionKey string `json:"sessionKey,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
}

// AttachmentInput represents a media file attached to a chat.send or agent.run request.
// The handler pre-processes attachments before forwarding: audio is transcribed,
// PDFs are text-extracted, and images are resolved to ImageRef for vision providers.
type AttachmentInput struct {
	Type     string `json:"type"`             // "image", "audio", "pdf"
	URL      string `json:"url,omitempty"`    // remote URL (optional)
	Base64   string `json:"base64,omitempty"` // base64-encoded content (optional)
	MimeType string `json:"mime_type,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type ChatSendRequest struct {
	To             string            `json:"to"`
	Text           string            `json:"text"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	RunID          string            `json:"run_id,omitempty"`
	Attachments    []AttachmentInput `json:"attachments,omitempty"`
}

type ChatHistoryRequest struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type ChatAbortRequest struct {
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

type SessionGetRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type SessionsListRequest struct {
	Limit                int    `json:"limit,omitempty"`
	ActiveMinutes        int    `json:"activeMinutes,omitempty"`
	IncludeGlobal        bool   `json:"includeGlobal,omitempty"`
	IncludeUnknown       bool   `json:"includeUnknown,omitempty"`
	IncludeDerivedTitles bool   `json:"includeDerivedTitles,omitempty"`
	IncludeLastMessage   bool   `json:"includeLastMessage,omitempty"`
	Label                string `json:"label,omitempty"`
	SpawnedBy            string `json:"spawnedBy,omitempty"`
	AgentID              string `json:"agentId,omitempty"`
	AgentIDSnake         string `json:"agent_id,omitempty"`
	Search               string `json:"search,omitempty"`
}

type SessionsPreviewRequest struct {
	SessionID string   `json:"session_id"`
	Key       string   `json:"key,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	MaxChars  int      `json:"maxChars,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

type SessionsPatchRequest struct {
	SessionID string         `json:"session_id"`
	Key       string         `json:"key,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type SessionsResetRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
}

type SessionsDeleteRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
}

// SessionsExportRequest requests exporting a session transcript.
type SessionsExportRequest struct {
	// SessionID is the session to export.
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
	// Format is the export format. Currently only "html" is supported.
	Format string `json:"format,omitempty"`
}

// SessionsExportResponse holds the exported content.
type SessionsExportResponse struct {
	// HTML is set when Format == "html".
	HTML   string `json:"html,omitempty"`
	Format string `json:"format"`
}

type SessionsCompactRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
	Keep      int    `json:"keep,omitempty"`
}

// SessionsPruneRequest deletes transcript entries for sessions that are older
// than OlderThanDays days (measured by last_inbound_at).  When All is true
// every session is deleted regardless of age.  DryRun reports what would be
// deleted without actually removing anything.
type SessionsPruneRequest struct {
	OlderThanDays int  `json:"older_than_days,omitempty"`
	DryRun        bool `json:"dry_run,omitempty"`
	All           bool `json:"all,omitempty"`
}

type SessionsSpawnRequest struct {
	// Message is the initial prompt for the spawned subagent.
	Message string `json:"message"`
	// ParentSessionID is the caller's session ID for depth/ancestry tracking.
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// AgentID selects which configured agent handles the sub-session.
	AgentID string `json:"agent_id,omitempty"`
	// MemoryScope carries the explicit worker memory scope contract.
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	// Context is extra system context to inject into the child session.
	Context string `json:"context,omitempty"`
	// TimeoutMS limits how long the caller will wait via agent.wait.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

func (r SessionsSpawnRequest) Normalize() (SessionsSpawnRequest, error) {
	r.Message = strings.TrimSpace(r.Message)
	if r.Message == "" {
		return r, fmt.Errorf("message is required")
	}
	r.ParentSessionID = strings.TrimSpace(r.ParentSessionID)
	r.AgentID = normalizeAgentID(r.AgentID)
	if raw := strings.TrimSpace(string(r.MemoryScope)); raw != "" {
		scope, ok := state.ParseAgentMemoryScope(raw)
		if !ok {
			return r, fmt.Errorf("memory_scope must be one of: user, project, local")
		}
		r.MemoryScope = scope
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 60_000, 300_000)
	return r, nil
}

func DecodeSessionsSpawnParams(params json.RawMessage) (SessionsSpawnRequest, error) {
	return decodeMethodParams[SessionsSpawnRequest](params)
}

func DecodeSessionsExportParams(params json.RawMessage) (SessionsExportRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsExportRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionsExportRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsExportRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionsExportRequest{SessionID: sessionID}
		if len(arr) > 1 {
			format, ok := arr[1].(string)
			if !ok {
				return SessionsExportRequest{}, fmt.Errorf("invalid params")
			}
			req.Format = format
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type sessionsExportCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
		Format         string `json:"format,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsExportCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsExportRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsExportRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key), Format: compat.Format}, nil
}

type SessionGetResponse struct {
	Session    state.SessionDoc           `json:"session"`
	Transcript []state.TranscriptEntryDoc `json:"transcript"`
}

func (r AgentRequest) Normalize() (AgentRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Message = strings.TrimSpace(r.Message)
	r.Context = strings.TrimSpace(r.Context)
	r.AgentID = normalizeAgentID(r.AgentID)
	r.To = strings.TrimSpace(r.To)
	r.ReplyTo = strings.TrimSpace(r.ReplyTo)
	r.Thinking = strings.TrimSpace(r.Thinking)
	r.Channel = strings.TrimSpace(r.Channel)
	r.ReplyChannel = strings.TrimSpace(r.ReplyChannel)
	r.AccountID = strings.TrimSpace(r.AccountID)
	r.ReplyAccountID = strings.TrimSpace(r.ReplyAccountID)
	r.ThreadID = strings.TrimSpace(r.ThreadID)
	r.GroupID = strings.TrimSpace(r.GroupID)
	r.GroupChannel = strings.TrimSpace(r.GroupChannel)
	r.GroupSpace = strings.TrimSpace(r.GroupSpace)
	r.Lane = strings.TrimSpace(r.Lane)
	r.ExtraSystemPrompt = strings.TrimSpace(r.ExtraSystemPrompt)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	r.Label = strings.TrimSpace(r.Label)
	if raw := strings.TrimSpace(string(r.MemoryScope)); raw != "" {
		scope, ok := state.ParseAgentMemoryScope(raw)
		if !ok {
			return r, fmt.Errorf("memory_scope must be one of: user, project, local")
		}
		r.MemoryScope = scope
	}
	if r.Message == "" {
		return r, fmt.Errorf("message is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 60_000, 300_000)
	return r, nil
}

func (r AgentWaitRequest) Normalize() (AgentWaitRequest, error) {
	r.RunID = strings.TrimSpace(r.RunID)
	if r.RunID == "" {
		return r, fmt.Errorf("run_id is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 30_000, 120_000)
	return r, nil
}

func (r AgentIdentityRequest) Normalize() (AgentIdentityRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func (r ChatSendRequest) Normalize() (ChatSendRequest, error) {
	r.To = strings.TrimSpace(r.To)
	r.Text = strings.TrimSpace(r.Text)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	r.RunID = strings.TrimSpace(r.RunID)
	if r.RunID == "" {
		r.RunID = r.IdempotencyKey
	}
	if r.To == "" {
		return r, fmt.Errorf("to is required")
	}
	// text is optional when attachments are present (e.g. image-only messages).
	if r.Text == "" && len(r.Attachments) == 0 {
		return r, fmt.Errorf("text or attachments are required")
	}
	const maxTextRunes = 4096
	if utf8.RuneCountInString(r.Text) > maxTextRunes {
		return r, fmt.Errorf("text exceeds %d characters", maxTextRunes)
	}
	return r, nil
}

func (r ChatHistoryRequest) Normalize() (ChatHistoryRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func (r ChatAbortRequest) Normalize() (ChatAbortRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.RunID = strings.TrimSpace(r.RunID)
	return r, nil
}

func (r SessionGetRequest) Normalize() (SessionGetRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func (r SessionsListRequest) Normalize() (SessionsListRequest, error) {
	if strings.TrimSpace(r.AgentID) == "" {
		r.AgentID = strings.TrimSpace(r.AgentIDSnake)
	}
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r SessionsPreviewRequest) Normalize() (SessionsPreviewRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	cleaned := make([]string, 0, len(r.Keys))
	for _, key := range r.Keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cleaned = append(cleaned, key)
	}
	r.Keys = cleaned
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" && len(r.Keys) > 0 {
		r.SessionID = r.Keys[0]
	}
	if r.SessionID == "" && len(r.Keys) == 0 {
		return r, fmt.Errorf("session_id or keys is required")
	}
	r.Limit = normalizeLimit(r.Limit, 25, 200)
	return r, nil
}

func (r SessionsPatchRequest) Normalize() (SessionsPatchRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r SessionsResetRequest) Normalize() (SessionsResetRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r SessionsDeleteRequest) Normalize() (SessionsDeleteRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r SessionsExportRequest) Normalize() (SessionsExportRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	r.Format = strings.ToLower(strings.TrimSpace(r.Format))
	if r.Format == "" {
		r.Format = "html"
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	if r.Format != "html" {
		return r, fmt.Errorf("unsupported format %q (only 'html' is supported)", r.Format)
	}
	return r, nil
}

func (r SessionsCompactRequest) Normalize() (SessionsCompactRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Keep = normalizeLimit(r.Keep, 50, 500)
	return r, nil
}

func DecodeAgentParams(params json.RawMessage) (AgentRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 4 {
			return AgentRequest{}, fmt.Errorf("invalid params")
		}
		message, ok := arr[0].(string)
		if !ok {
			return AgentRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentRequest{Message: message}
		if len(arr) > 1 {
			sessionID, ok := arr[1].(string)
			if !ok {
				return AgentRequest{}, fmt.Errorf("invalid params")
			}
			req.SessionID = sessionID
		}
		if len(arr) > 2 {
			contextText, ok := arr[2].(string)
			if !ok {
				return AgentRequest{}, fmt.Errorf("invalid params")
			}
			req.Context = contextText
		}
		if len(arr) > 3 {
			switch v := arr[3].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return AgentRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[AgentRequest](params)
}

func DecodeAgentWaitParams(params json.RawMessage) (AgentWaitRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentWaitRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return AgentWaitRequest{}, fmt.Errorf("invalid params")
		}
		runID, ok := arr[0].(string)
		if !ok {
			return AgentWaitRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentWaitRequest{RunID: runID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentWaitRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return AgentWaitRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[AgentWaitRequest](params)
}

func DecodeAgentIdentityParams(params json.RawMessage) (AgentIdentityRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentIdentityRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 2 {
			return AgentIdentityRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentIdentityRequest{}
		if len(arr) > 0 {
			sessionID, ok := arr[0].(string)
			if !ok {
				return AgentIdentityRequest{}, fmt.Errorf("invalid params")
			}
			req.SessionID = sessionID
		}
		if len(arr) > 1 {
			agentID, ok := arr[1].(string)
			if !ok {
				return AgentIdentityRequest{}, fmt.Errorf("invalid params")
			}
			req.AgentID = agentID
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return AgentIdentityRequest{}, nil
	}
	return decodeMethodParams[AgentIdentityRequest](params)
}

func DecodeChatSendParams(params json.RawMessage) (ChatSendRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		to, ok := arr[0].(string)
		if !ok {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		text, ok := arr[1].(string)
		if !ok {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		return ChatSendRequest{To: to, Text: text}, nil
	}
	params = normalizeObjectParamAliases(params)
	type chatSendCompatRequest struct {
		To             string            `json:"to,omitempty"`
		Text           string            `json:"text,omitempty"`
		SessionID      string            `json:"session_id,omitempty"`
		SessionIDCamel string            `json:"sessionId,omitempty"`
		SessionKey     string            `json:"sessionKey,omitempty"`
		Message        string            `json:"message,omitempty"`
		Thinking       string            `json:"thinking,omitempty"`
		Deliver        *bool             `json:"deliver,omitempty"`
		TimeoutMS      int               `json:"timeoutMs,omitempty"`
		TimeoutMSSnake int               `json:"timeout_ms,omitempty"`
		IdempotencyKey string            `json:"idempotencyKey,omitempty"`
		IdempotencyAlt string            `json:"idempotency_key,omitempty"`
		RunID          string            `json:"runId,omitempty"`
		RunIDAlt       string            `json:"run_id,omitempty"`
		Attachments    []AttachmentInput `json:"attachments,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat chatSendCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ChatSendRequest{}, fmt.Errorf("invalid params")
	}
	to := strings.TrimSpace(compat.To)
	if to == "" {
		to = strings.TrimSpace(compat.SessionID)
	}
	if to == "" {
		to = strings.TrimSpace(compat.SessionIDCamel)
	}
	if to == "" {
		to = strings.TrimSpace(compat.SessionKey)
	}
	text := strings.TrimSpace(compat.Text)
	if text == "" {
		text = strings.TrimSpace(compat.Message)
	}
	idempotency := strings.TrimSpace(compat.IdempotencyKey)
	if idempotency == "" {
		idempotency = strings.TrimSpace(compat.IdempotencyAlt)
	}
	runID := strings.TrimSpace(compat.RunID)
	if runID == "" {
		runID = strings.TrimSpace(compat.RunIDAlt)
	}
	return ChatSendRequest{To: to, Text: text, IdempotencyKey: idempotency, RunID: runID, Attachments: compat.Attachments}, nil
}

func DecodeSessionGetParams(params json.RawMessage) (SessionGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionGetRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionGetRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type sessionGetCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
		Limit          int    `json:"limit,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionGetCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionGetRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionGetRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key), Limit: compat.Limit}, nil
}

func DecodeChatHistoryParams(params json.RawMessage) (ChatHistoryRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChatHistoryRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return ChatHistoryRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return ChatHistoryRequest{}, fmt.Errorf("invalid params")
		}
		req := ChatHistoryRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return ChatHistoryRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type chatHistoryCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
		Limit          int    `json:"limit,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat chatHistoryCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ChatHistoryRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return ChatHistoryRequest{SessionID: sessionID, Limit: compat.Limit}, nil
}

func DecodeChatAbortParams(params json.RawMessage) (ChatAbortRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChatAbortRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return ChatAbortRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 {
			return ChatAbortRequest{}, nil
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return ChatAbortRequest{}, fmt.Errorf("invalid params")
		}
		return ChatAbortRequest{SessionID: sessionID}, nil
	}
	params = normalizeObjectParamAliases(params)
	type chatAbortCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		RunID          string `json:"run_id,omitempty"`
		RunIDCamel     string `json:"runId,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat chatAbortCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ChatAbortRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	runID := strings.TrimSpace(compat.RunID)
	if runID == "" {
		runID = strings.TrimSpace(compat.RunIDCamel)
	}
	return ChatAbortRequest{SessionID: sessionID, RunID: runID}, nil
}

func DecodeSessionsListParams(params json.RawMessage) (SessionsListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return SessionsListRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionsListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionsListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return SessionsListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[SessionsListRequest](params)
}

func DecodeSessionsPreviewParams(params json.RawMessage) (SessionsPreviewRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionsPreviewRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type sessionsPreviewCompatRequest struct {
		SessionID      string   `json:"session_id,omitempty"`
		SessionIDCamel string   `json:"sessionId,omitempty"`
		SessionKey     string   `json:"sessionKey,omitempty"`
		Key            string   `json:"key,omitempty"`
		Keys           []string `json:"keys,omitempty"`
		Limit          int      `json:"limit,omitempty"`
		MaxChars       int      `json:"maxChars,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsPreviewCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsPreviewRequest{
		SessionID: sessionID,
		Key:       strings.TrimSpace(compat.Key),
		Keys:      compat.Keys,
		Limit:     compat.Limit,
		MaxChars:  compat.MaxChars,
	}, nil
}

func DecodeSessionsPatchParams(params json.RawMessage) (SessionsPatchRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsPatchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionsPatchRequest{}, fmt.Errorf("invalid params")
		}
		var req SessionsPatchRequest
		if err := json.Unmarshal(arr[0], &req.SessionID); err != nil {
			return SessionsPatchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 2 {
			if err := json.Unmarshal(arr[1], &req.Meta); err != nil {
				return SessionsPatchRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type sessionsPatchCompatRequest struct {
		SessionID      string         `json:"session_id,omitempty"`
		SessionIDCamel string         `json:"sessionId,omitempty"`
		SessionKey     string         `json:"sessionKey,omitempty"`
		Key            string         `json:"key,omitempty"`
		Meta           map[string]any `json:"meta,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsPatchCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsPatchRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsPatchRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key), Meta: compat.Meta}, nil
}

func DecodeSessionsResetParams(params json.RawMessage) (SessionsResetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsResetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return SessionsResetRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsResetRequest{}, fmt.Errorf("invalid params")
		}
		return SessionsResetRequest{SessionID: sessionID}, nil
	}
	params = normalizeObjectParamAliases(params)
	type sessionsResetCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsResetCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsResetRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsResetRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key)}, nil
}

func DecodeSessionsDeleteParams(params json.RawMessage) (SessionsDeleteRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
		}
		return SessionsDeleteRequest{SessionID: sessionID}, nil
	}
	params = normalizeObjectParamAliases(params)
	type sessionsDeleteCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsDeleteCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsDeleteRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key)}, nil
}

func DecodeSessionsCompactParams(params json.RawMessage) (SessionsCompactRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsCompactRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionsCompactRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsCompactRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionsCompactRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionsCompactRequest{}, fmt.Errorf("invalid params")
				}
				req.Keep = int(v)
			case int:
				req.Keep = v
			default:
				return SessionsCompactRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type sessionsCompactCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
		Keep           int    `json:"keep,omitempty"`
		MaxLines       int    `json:"maxLines,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsCompactCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsCompactRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	keep := compat.Keep
	if keep <= 0 {
		keep = compat.MaxLines
	}
	return SessionsCompactRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key), Keep: keep}, nil
}
