package methods

// schema_openclaw.go — request schemas + decoders for the OpenClaw-branded
// control-surface compat aliases (swarmstr-i413). Each alias re-dispatches to a
// native Metiq method, so its request shape intentionally mirrors that native
// method's request; the decoders validate the OpenClaw-facing params and are
// translated to the native param shape in the handler
// (cmd/metiqd/control_rpc_openclaw.go).

import (
	"encoding/json"
	"strings"
)

// OpenclawChatRequest is the input for openclaw.chat (alias of chat.send). It
// mirrors ChatSendRequest: a peer target plus message text and optional
// attachments.
type OpenclawChatRequest struct {
	To          string            `json:"to"`
	Text        string            `json:"text"`
	Attachments []AttachmentInput `json:"attachments,omitempty"`
}

// Normalize trims the addressing/text fields.
func (r OpenclawChatRequest) Normalize() (OpenclawChatRequest, error) {
	r.To = strings.TrimSpace(r.To)
	return r, nil
}

// ToNative projects the alias request onto the native chat.send request.
func (r OpenclawChatRequest) ToNative() ChatSendRequest {
	return ChatSendRequest{To: r.To, Text: r.Text, Attachments: r.Attachments}
}

// OpenclawChatHistoryRequest is the input for openclaw.chat.history (alias of
// chat.history). It mirrors ChatHistoryRequest.
type OpenclawChatHistoryRequest struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

// Normalize trims the session identifier.
func (r OpenclawChatHistoryRequest) Normalize() (OpenclawChatHistoryRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	return r, nil
}

// ToNative projects the alias request onto the native chat.history request.
func (r OpenclawChatHistoryRequest) ToNative() ChatHistoryRequest {
	return ChatHistoryRequest{SessionID: r.SessionID, Limit: r.Limit}
}

// OpenclawChangesListRequest is the input for openclaw.changes.list (alias of
// sessions.files.list — the session's touched/changed workspace files). It
// mirrors SessionsFilesListRequest.
type OpenclawChangesListRequest struct {
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agentId,omitempty"`
	Path       string `json:"path,omitempty"`
	Search     string `json:"search,omitempty"`
}

// Normalize trims the addressing fields.
func (r OpenclawChangesListRequest) Normalize() (OpenclawChangesListRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

// ToNative projects the alias request onto the native sessions.files.list
// request.
func (r OpenclawChangesListRequest) ToNative() SessionsFilesListRequest {
	return SessionsFilesListRequest{SessionKey: r.SessionKey, AgentID: r.AgentID, Path: r.Path, Search: r.Search}
}

// OpenclawApprovalListRequest is the input for openclaw.approval.list (alias of
// approval.list). It mirrors ApprovalListRequest.
type OpenclawApprovalListRequest struct {
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
}

// Normalize lower-cases the filter fields to match the native ledger filters.
func (r OpenclawApprovalListRequest) Normalize() (OpenclawApprovalListRequest, error) {
	r.Kind = strings.TrimSpace(r.Kind)
	r.Status = strings.TrimSpace(r.Status)
	return r, nil
}

// ToNative projects the alias request onto the native approval.list request.
func (r OpenclawApprovalListRequest) ToNative() ApprovalListRequest {
	return ApprovalListRequest{Kind: r.Kind, Status: r.Status}
}

// DecodeOpenclawChatParams decodes openclaw.chat params.
func DecodeOpenclawChatParams(params json.RawMessage) (OpenclawChatRequest, error) {
	return emptyOrDecode[OpenclawChatRequest](params)
}

// DecodeOpenclawChatHistoryParams decodes openclaw.chat.history params.
func DecodeOpenclawChatHistoryParams(params json.RawMessage) (OpenclawChatHistoryRequest, error) {
	return emptyOrDecode[OpenclawChatHistoryRequest](params)
}

// DecodeOpenclawChangesListParams decodes openclaw.changes.list params.
func DecodeOpenclawChangesListParams(params json.RawMessage) (OpenclawChangesListRequest, error) {
	return emptyOrDecode[OpenclawChangesListRequest](params)
}

// DecodeOpenclawApprovalListParams decodes openclaw.approval.list params.
func DecodeOpenclawApprovalListParams(params json.RawMessage) (OpenclawApprovalListRequest, error) {
	return emptyOrDecode[OpenclawApprovalListRequest](params)
}
