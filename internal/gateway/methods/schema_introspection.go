package methods

// schema_introspection.go — request schemas + decoders for the gateway
// introspection long tail (swarmstr-wapc). Handlers live in
// cmd/metiqd/control_rpc_introspection.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// SystemInfoRequest is the (empty) input for system.info.
type SystemInfoRequest struct{}

func (r SystemInfoRequest) Normalize() (SystemInfoRequest, error) { return r, nil }

// DiagnosticsStabilityRequest is the (empty) input for diagnostics.stability.
type DiagnosticsStabilityRequest struct{}

func (r DiagnosticsStabilityRequest) Normalize() (DiagnosticsStabilityRequest, error) {
	return r, nil
}

// CommandsListRequest is the input for commands.list. Source optionally filters
// to one command provenance (e.g. "gateway", "plugin").
type CommandsListRequest struct {
	Source string `json:"source,omitempty"`
}

func (r CommandsListRequest) Normalize() (CommandsListRequest, error) {
	r.Source = strings.ToLower(strings.TrimSpace(r.Source))
	return r, nil
}

// UpdateStatusRequest is the (empty) input for update.status.
type UpdateStatusRequest struct{}

func (r UpdateStatusRequest) Normalize() (UpdateStatusRequest, error) { return r, nil }

// ToolsEffectiveRequest is the input for tools.effective. It mirrors
// ToolsCatalogRequest: the effective set is the catalog filtered by the agent's
// active profile (or an explicit Profile override).
type ToolsEffectiveRequest struct {
	AgentID        string  `json:"agent_id,omitempty"`
	Profile        *string `json:"profile,omitempty"`
	IncludePlugins *bool   `json:"include_plugins,omitempty"`
}

func (r ToolsEffectiveRequest) Normalize() (ToolsEffectiveRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	if r.Profile != nil {
		p := strings.TrimSpace(strings.ToLower(*r.Profile))
		r.Profile = &p
	}
	return r, nil
}

// ToolsInvokeRequest is the input for tools.invoke. Tool is the registered tool
// name; Args are the tool's JSON arguments.
type ToolsInvokeRequest struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

func (r ToolsInvokeRequest) Normalize() (ToolsInvokeRequest, error) {
	r.Tool = strings.TrimSpace(r.Tool)
	if r.Tool == "" {
		return r, fmt.Errorf("tool is required")
	}
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	return r, nil
}

// AuditListRequest is the input for audit.list. Time bounds are epoch
// milliseconds; empty/zero disables the bound.
type AuditListRequest struct {
	Type    string `json:"type,omitempty"`
	Tool    string `json:"tool,omitempty"`
	SinceMS int64  `json:"since_ms,omitempty"`
	UntilMS int64  `json:"until_ms,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func (r AuditListRequest) Normalize() (AuditListRequest, error) {
	r.Type = strings.ToLower(strings.TrimSpace(r.Type))
	r.Tool = strings.TrimSpace(r.Tool)
	if r.SinceMS < 0 {
		r.SinceMS = 0
	}
	if r.UntilMS < 0 {
		r.UntilMS = 0
	}
	r.Limit = normalizeLimit(r.Limit, 100, 1000)
	return r, nil
}

// AuditActivityListRequest is the input for audit.activity.list. It filters the
// permission-engine activity feed by agent/session and time window.
type AuditActivityListRequest struct {
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	SinceMS   int64  `json:"since_ms,omitempty"`
	UntilMS   int64  `json:"until_ms,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func (r AuditActivityListRequest) Normalize() (AuditActivityListRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SinceMS < 0 {
		r.SinceMS = 0
	}
	if r.UntilMS < 0 {
		r.UntilMS = 0
	}
	r.Limit = normalizeLimit(r.Limit, 100, 1000)
	return r, nil
}

// AgentsWorkspaceListRequest is the input for agents.workspace.list. Workspace
// optionally filters to a single workspace.
type AgentsWorkspaceListRequest struct {
	Workspace string `json:"workspace,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func (r AgentsWorkspaceListRequest) Normalize() (AgentsWorkspaceListRequest, error) {
	r.Workspace = strings.TrimSpace(r.Workspace)
	r.Limit = normalizeLimit(r.Limit, 200, 1000)
	return r, nil
}

// AgentsWorkspaceGetRequest is the input for agents.workspace.get.
type AgentsWorkspaceGetRequest struct {
	AgentID string `json:"agent_id"`
}

func (r AgentsWorkspaceGetRequest) Normalize() (AgentsWorkspaceGetRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	return r, nil
}

// ApprovalHistoryRequest is the input for approval.history (resolved records).
// Kind optionally filters by approval kind (exec|plugin|system).
type ApprovalHistoryRequest struct {
	Kind  string `json:"kind,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (r ApprovalHistoryRequest) Normalize() (ApprovalHistoryRequest, error) {
	r.Kind = strings.ToLower(strings.TrimSpace(r.Kind))
	if r.Kind != "" && r.Kind != "exec" && r.Kind != "plugin" && r.Kind != "system" {
		return r, fmt.Errorf("unsupported approval kind %q", r.Kind)
	}
	r.Limit = normalizeLimit(r.Limit, 100, 1000)
	return r, nil
}

// UICommandRequest is the input for ui.command. Command is the slash command
// (e.g. "/status" or "status"); Args are forwarded to the resolved method.
type UICommandRequest struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

func (r UICommandRequest) Normalize() (UICommandRequest, error) {
	r.Command = strings.TrimSpace(r.Command)
	if r.Command == "" {
		return r, fmt.Errorf("command is required")
	}
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	return r, nil
}

// ─── Decoders ────────────────────────────────────────────────────────────────

func emptyOrDecode[T any](params json.RawMessage) (T, error) {
	var zero T
	if len(bytes.TrimSpace(params)) == 0 {
		return zero, nil
	}
	return decodeMethodParams[T](params)
}

func DecodeSystemInfoParams(params json.RawMessage) (SystemInfoRequest, error) {
	return emptyOrDecode[SystemInfoRequest](params)
}

func DecodeDiagnosticsStabilityParams(params json.RawMessage) (DiagnosticsStabilityRequest, error) {
	return emptyOrDecode[DiagnosticsStabilityRequest](params)
}

func DecodeCommandsListParams(params json.RawMessage) (CommandsListRequest, error) {
	return emptyOrDecode[CommandsListRequest](params)
}

func DecodeUpdateStatusParams(params json.RawMessage) (UpdateStatusRequest, error) {
	return emptyOrDecode[UpdateStatusRequest](params)
}

func DecodeToolsEffectiveParams(params json.RawMessage) (ToolsEffectiveRequest, error) {
	return emptyOrDecode[ToolsEffectiveRequest](params)
}

func DecodeToolsInvokeParams(params json.RawMessage) (ToolsInvokeRequest, error) {
	return decodeMethodParams[ToolsInvokeRequest](params)
}

func DecodeAuditListParams(params json.RawMessage) (AuditListRequest, error) {
	return emptyOrDecode[AuditListRequest](params)
}

func DecodeAuditActivityListParams(params json.RawMessage) (AuditActivityListRequest, error) {
	return emptyOrDecode[AuditActivityListRequest](params)
}

func DecodeAgentsWorkspaceListParams(params json.RawMessage) (AgentsWorkspaceListRequest, error) {
	return emptyOrDecode[AgentsWorkspaceListRequest](params)
}

func DecodeAgentsWorkspaceGetParams(params json.RawMessage) (AgentsWorkspaceGetRequest, error) {
	return decodeMethodParams[AgentsWorkspaceGetRequest](params)
}

func DecodeApprovalHistoryParams(params json.RawMessage) (ApprovalHistoryRequest, error) {
	return emptyOrDecode[ApprovalHistoryRequest](params)
}

func DecodeUICommandParams(params json.RawMessage) (UICommandRequest, error) {
	return decodeMethodParams[UICommandRequest](params)
}
