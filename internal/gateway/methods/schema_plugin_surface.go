package methods

// Plugin-surface method schemas (WS-G plugin.* / plugins.* long tail).
//
// Param shapes mirror OpenClaw's gateway-protocol plugin-surface contracts:
// plugins.list/search/setEnabled/refresh and the durable plugin.approval.*
// request/waitDecision/resolve/list lifecycle. plugin.approval.waitDecision is
// a pose-and-block primitive like question.waitAnswer.

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// maxPluginsSearchLimit bounds a plugins.search page.
	maxPluginsSearchLimit = 100
	// DefaultPluginsSearchLimit applies when a search omits limit.
	DefaultPluginsSearchLimit = 50
	// maxPluginApprovalTimeoutMS caps a requested approval window (1h).
	maxPluginApprovalTimeoutMS = 60 * 60 * 1000
	// maxPluginApprovalWaitMS caps a single waitDecision block (5m).
	maxPluginApprovalWaitMS = 5 * 60 * 1000
)

// PluginsListRequest lists installed/loaded plugins. When Enabled is set it
// filters to entries whose enabled state matches.
type PluginsListRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (r PluginsListRequest) Normalize() (PluginsListRequest, error) { return r, nil }

// PluginsSearchRequest ranks the merged plugin catalog by substring query.
type PluginsSearchRequest struct {
	Query   string `json:"query,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

func (r PluginsSearchRequest) Normalize() (PluginsSearchRequest, error) {
	r.Query = strings.TrimSpace(r.Query)
	if r.Limit < 0 {
		return r, fmt.Errorf("invalid plugins.search params: limit must be positive")
	}
	if r.Limit == 0 {
		r.Limit = DefaultPluginsSearchLimit
	}
	if r.Limit > maxPluginsSearchLimit {
		return r, fmt.Errorf("invalid plugins.search params: limit must be between 1 and %d", maxPluginsSearchLimit)
	}
	return r, nil
}

// PluginsSetEnabledRequest toggles plugins.entries.<id>.enabled.
type PluginsSetEnabledRequest struct {
	PluginID string `json:"pluginId"`
	Enabled  bool   `json:"enabled"`
	BaseHash string `json:"baseHash,omitempty"`
}

func (r PluginsSetEnabledRequest) Normalize() (PluginsSetEnabledRequest, error) {
	r.PluginID = strings.TrimSpace(r.PluginID)
	if r.PluginID == "" {
		return r, fmt.Errorf("invalid plugins.setEnabled params: pluginId is required")
	}
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	return r, nil
}

// PluginsRefreshRequest reloads the plugin manager against current config.
type PluginsRefreshRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (r PluginsRefreshRequest) Normalize() (PluginsRefreshRequest, error) { return r, nil }

// PluginApprovalListRequest lists pending plugin approvals.
type PluginApprovalListRequest struct{}

func (r PluginApprovalListRequest) Normalize() (PluginApprovalListRequest, error) { return r, nil }

// PluginApprovalRequestRequest poses one plugin-approval request.
type PluginApprovalRequestRequest struct {
	ID         string         `json:"id,omitempty"`
	PluginID   string         `json:"pluginId,omitempty"`
	Action     string         `json:"action"`
	Reason     string         `json:"reason,omitempty"`
	SessionKey string         `json:"sessionKey,omitempty"`
	AgentID    string         `json:"agentId,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
	TimeoutMS  int            `json:"timeoutMs,omitempty"`
}

func (r PluginApprovalRequestRequest) Normalize() (PluginApprovalRequestRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.PluginID = strings.TrimSpace(r.PluginID)
	r.Action = strings.TrimSpace(r.Action)
	if r.Action == "" {
		return r, fmt.Errorf("invalid plugin.approval.request params: action is required")
	}
	r.Reason = strings.TrimSpace(r.Reason)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.AgentID = strings.TrimSpace(r.AgentID)
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("invalid plugin.approval.request params: timeoutMs must be positive")
	}
	if r.TimeoutMS > maxPluginApprovalTimeoutMS {
		return r, fmt.Errorf("invalid plugin.approval.request params: timeoutMs must be between 1 and %d", maxPluginApprovalTimeoutMS)
	}
	return r, nil
}

// PluginApprovalWaitDecisionRequest blocks until an approval resolves.
type PluginApprovalWaitDecisionRequest struct {
	ID        string `json:"id"`
	TimeoutMS int    `json:"timeoutMs,omitempty"`
}

func (r PluginApprovalWaitDecisionRequest) Normalize() (PluginApprovalWaitDecisionRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("invalid plugin.approval.waitDecision params: id is required")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("invalid plugin.approval.waitDecision params: timeoutMs must be positive")
	}
	if r.TimeoutMS > maxPluginApprovalWaitMS {
		return r, fmt.Errorf("invalid plugin.approval.waitDecision params: timeoutMs must be between 1 and %d", maxPluginApprovalWaitMS)
	}
	return r, nil
}

// PluginApprovalResolveRequest records an operator decision.
type PluginApprovalResolveRequest struct {
	ID        string `json:"id"`
	Decision  string `json:"decision"`
	DecidedBy string `json:"decidedBy,omitempty"`
	Note      string `json:"note,omitempty"`
}

func (r PluginApprovalResolveRequest) Normalize() (PluginApprovalResolveRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("invalid plugin.approval.resolve params: id is required")
	}
	r.Decision = strings.ToLower(strings.TrimSpace(r.Decision))
	switch r.Decision {
	case "approve", "deny":
	default:
		return r, fmt.Errorf("invalid plugin.approval.resolve params: decision must be \"approve\" or \"deny\"")
	}
	r.DecidedBy = strings.TrimSpace(r.DecidedBy)
	r.Note = strings.TrimSpace(r.Note)
	return r, nil
}

func DecodePluginsListParams(params json.RawMessage) (PluginsListRequest, error) {
	return decodeMethodParams[PluginsListRequest](params)
}

func DecodePluginsSearchParams(params json.RawMessage) (PluginsSearchRequest, error) {
	return decodeMethodParams[PluginsSearchRequest](params)
}

func DecodePluginsSetEnabledParams(params json.RawMessage) (PluginsSetEnabledRequest, error) {
	return decodeMethodParams[PluginsSetEnabledRequest](params)
}

func DecodePluginsRefreshParams(params json.RawMessage) (PluginsRefreshRequest, error) {
	return decodeMethodParams[PluginsRefreshRequest](params)
}

func DecodePluginApprovalListParams(params json.RawMessage) (PluginApprovalListRequest, error) {
	return decodeMethodParams[PluginApprovalListRequest](params)
}

func DecodePluginApprovalRequestParams(params json.RawMessage) (PluginApprovalRequestRequest, error) {
	return decodeMethodParams[PluginApprovalRequestRequest](params)
}

func DecodePluginApprovalWaitDecisionParams(params json.RawMessage) (PluginApprovalWaitDecisionRequest, error) {
	return decodeMethodParams[PluginApprovalWaitDecisionRequest](params)
}

func DecodePluginApprovalResolveParams(params json.RawMessage) (PluginApprovalResolveRequest, error) {
	return decodeMethodParams[PluginApprovalResolveRequest](params)
}
