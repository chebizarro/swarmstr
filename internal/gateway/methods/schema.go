package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"swarmstr/internal/memory"
	"swarmstr/internal/store/state"
)

const (
	MethodSupportedMethods   = "supportedmethods"
	MethodHealth             = "health"
	MethodDoctorMemoryStatus = "doctor.memory.status"
	MethodLogsTail           = "logs.tail"
	MethodChannelsStatus     = "channels.status"
	MethodChannelsLogout     = "channels.logout"
	MethodStatus             = "status.get"
	MethodUsageStatus        = "usage.status"
	MethodUsageCost          = "usage.cost"
	MethodMemorySearch       = "memory.search"
	MethodAgent              = "agent"
	MethodAgentWait          = "agent.wait"
	MethodAgentIdentityGet   = "agent.identity.get"
	MethodChatSend           = "chat.send"
	MethodChatHistory        = "chat.history"
	MethodChatAbort          = "chat.abort"
	MethodSessionGet         = "session.get"
	MethodSessionsList       = "sessions.list"
	MethodSessionsPreview    = "sessions.preview"
	MethodSessionsPatch      = "sessions.patch"
	MethodSessionsReset      = "sessions.reset"
	MethodSessionsDelete     = "sessions.delete"
	MethodSessionsCompact    = "sessions.compact"
	MethodListGet            = "list.get"
	MethodListPut            = "list.put"
	MethodRelayPolicyGet     = "relay.policy.get"
	MethodConfigGet          = "config.get"
	MethodConfigPut          = "config.put"
	MethodConfigSet          = "config.set"
	MethodConfigApply        = "config.apply"
	MethodConfigPatch        = "config.patch"
	MethodConfigSchema       = "config.schema"
	MethodAgentsList         = "agents.list"
	MethodAgentsCreate       = "agents.create"
	MethodAgentsUpdate       = "agents.update"
	MethodAgentsDelete       = "agents.delete"
	MethodAgentsFilesList    = "agents.files.list"
	MethodAgentsFilesGet     = "agents.files.get"
	MethodAgentsFilesSet     = "agents.files.set"
	MethodModelsList         = "models.list"
	MethodToolsCatalog       = "tools.catalog"
	MethodSkillsStatus       = "skills.status"
	MethodSkillsInstall      = "skills.install"
	MethodSkillsUpdate       = "skills.update"
	MethodNodePairRequest    = "node.pair.request"
	MethodNodePairList       = "node.pair.list"
	MethodNodePairApprove    = "node.pair.approve"
	MethodNodePairReject     = "node.pair.reject"
	MethodNodePairVerify     = "node.pair.verify"
	MethodDevicePairList     = "device.pair.list"
	MethodDevicePairApprove  = "device.pair.approve"
	MethodDevicePairReject   = "device.pair.reject"
	MethodDevicePairRemove   = "device.pair.remove"
	MethodDeviceTokenRotate  = "device.token.rotate"
	MethodDeviceTokenRevoke  = "device.token.revoke"
)

type CallRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type CallResponse struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type StatusResponse struct {
	PubKey        string   `json:"pubkey"`
	Relays        []string `json:"relays"`
	DMPolicy      string   `json:"dm_policy"`
	UptimeSeconds int      `json:"uptime_seconds"`
}

type MemorySearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type MemorySearchResponse struct {
	Results []memory.IndexedMemory `json:"results"`
}

type AgentRequest struct {
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message"`
	Context   string `json:"context,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type AgentWaitRequest struct {
	RunID     string `json:"run_id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type AgentIdentityRequest struct {
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

type ChatSendRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type ChatHistoryRequest struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type ChatAbortRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

type SessionGetRequest struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type SessionsListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type SessionsPreviewRequest struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type SessionsPatchRequest struct {
	SessionID string         `json:"session_id"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type SessionsResetRequest struct {
	SessionID string `json:"session_id"`
}

type SessionsDeleteRequest struct {
	SessionID string `json:"session_id"`
}

type SessionsCompactRequest struct {
	SessionID string `json:"session_id"`
	Keep      int    `json:"keep,omitempty"`
}

type SessionGetResponse struct {
	Session    state.SessionDoc           `json:"session"`
	Transcript []state.TranscriptEntryDoc `json:"transcript"`
}

type ListGetRequest struct {
	Name string `json:"name"`
}

type ListPutRequest struct {
	Name            string   `json:"name"`
	Items           []string `json:"items"`
	ExpectedVersion int      `json:"expected_version,omitempty"`
	ExpectedEvent   string   `json:"expected_event,omitempty"`
}

type ConfigPutRequest struct {
	Config          state.ConfigDoc `json:"config"`
	ExpectedVersion int             `json:"expected_version,omitempty"`
	ExpectedEvent   string          `json:"expected_event,omitempty"`
}

type ConfigSetRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type ConfigApplyRequest struct {
	Config state.ConfigDoc `json:"config"`
}

type ConfigPatchRequest struct {
	Patch map[string]any `json:"patch"`
}

type LogsTailRequest struct {
	Cursor   int64 `json:"cursor,omitempty"`
	Limit    int   `json:"limit,omitempty"`
	MaxBytes int   `json:"max_bytes,omitempty"`
	Lines    int   `json:"lines,omitempty"`
}

type ChannelsStatusRequest struct {
	Probe     bool `json:"probe,omitempty"`
	TimeoutMS int  `json:"timeout_ms,omitempty"`
}

type ChannelsLogoutRequest struct {
	Channel string `json:"channel"`
}

type UsageCostRequest struct {
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
	Days      int    `json:"days,omitempty"`
	Mode      string `json:"mode,omitempty"`
	UTCOffset string `json:"utcOffset,omitempty"`
}

type RelayPolicyResponse struct {
	ReadRelays           []string `json:"read_relays"`
	WriteRelays          []string `json:"write_relays"`
	RuntimeDMRelays      []string `json:"runtime_dm_relays"`
	RuntimeControlRelays []string `json:"runtime_control_relays"`
}

type AgentsListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type AgentsCreateRequest struct {
	AgentID   string         `json:"agent_id"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentsUpdateRequest struct {
	AgentID   string         `json:"agent_id"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentsDeleteRequest struct {
	AgentID string `json:"agent_id"`
}

type AgentsFilesListRequest struct {
	AgentID string `json:"agent_id"`
	Limit   int    `json:"limit,omitempty"`
}

type AgentsFilesGetRequest struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
}

type AgentsFilesSetRequest struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type ModelsListRequest struct{}

type ToolsCatalogRequest struct {
	AgentID        string `json:"agent_id,omitempty"`
	IncludePlugins *bool  `json:"include_plugins,omitempty"`
}

type SkillsStatusRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

type SkillsInstallRequest struct {
	Name      string `json:"name"`
	InstallID string `json:"install_id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type SkillsUpdateRequest struct {
	SkillKey string            `json:"skill_key"`
	Enabled  *bool             `json:"enabled,omitempty"`
	APIKey   *string           `json:"api_key,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

type NodePairRequest struct {
	NodeID          string         `json:"node_id"`
	DisplayName     string         `json:"display_name,omitempty"`
	Platform        string         `json:"platform,omitempty"`
	Version         string         `json:"version,omitempty"`
	CoreVersion     string         `json:"core_version,omitempty"`
	UIVersion       string         `json:"ui_version,omitempty"`
	DeviceFamily    string         `json:"device_family,omitempty"`
	ModelIdentifier string         `json:"model_identifier,omitempty"`
	Caps            []string       `json:"caps,omitempty"`
	Commands        []string       `json:"commands,omitempty"`
	Permissions     map[string]any `json:"permissions,omitempty"`
	RemoteIP        string         `json:"remote_ip,omitempty"`
	Silent          bool           `json:"silent,omitempty"`
}

type NodePairListRequest struct{}

type NodePairApproveRequest struct {
	RequestID string `json:"request_id"`
}

type NodePairRejectRequest struct {
	RequestID string `json:"request_id"`
}

type NodePairVerifyRequest struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"`
}

type DevicePairListRequest struct{}

type DevicePairApproveRequest struct {
	RequestID string `json:"request_id"`
}

type DevicePairRejectRequest struct {
	RequestID string `json:"request_id"`
}

type DevicePairRemoveRequest struct {
	DeviceID string `json:"device_id"`
}

type DeviceTokenRotateRequest struct {
	DeviceID string   `json:"device_id"`
	Role     string   `json:"role"`
	Scopes   []string `json:"scopes,omitempty"`
}

type DeviceTokenRevokeRequest struct {
	DeviceID string `json:"device_id"`
	Role     string `json:"role"`
}

func (r MemorySearchRequest) Normalize() (MemorySearchRequest, error) {
	r.Query = strings.TrimSpace(r.Query)
	if r.Query == "" {
		return r, fmt.Errorf("query is required")
	}
	if utf8.RuneCountInString(r.Query) > 256 {
		r.Query = truncateRunes(r.Query, 256)
	}
	r.Limit = normalizeLimit(r.Limit, 20, 200)
	return r, nil
}

func (r AgentRequest) Normalize() (AgentRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Message = strings.TrimSpace(r.Message)
	r.Context = strings.TrimSpace(r.Context)
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
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func (r ChatSendRequest) Normalize() (ChatSendRequest, error) {
	r.To = strings.TrimSpace(r.To)
	r.Text = strings.TrimSpace(r.Text)
	if r.To == "" || r.Text == "" {
		return r, fmt.Errorf("to and text are required")
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
	return r, nil
}

func (r SessionGetRequest) Normalize() (SessionGetRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func (r SessionsListRequest) Normalize() (SessionsListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r SessionsPreviewRequest) Normalize() (SessionsPreviewRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 25, 200)
	return r, nil
}

func (r SessionsPatchRequest) Normalize() (SessionsPatchRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
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
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r SessionsDeleteRequest) Normalize() (SessionsDeleteRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r SessionsCompactRequest) Normalize() (SessionsCompactRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Keep = normalizeLimit(r.Keep, 50, 500)
	return r, nil
}

func (r ListGetRequest) Normalize() (ListGetRequest, error) {
	r.Name = normalizeListName(r.Name)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	return r, nil
}

func (r ListPutRequest) Normalize() (ListPutRequest, error) {
	r.Name = normalizeListName(r.Name)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	out := make([]string, 0, len(r.Items))
	seen := map[string]struct{}{}
	for _, item := range r.Items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	r.Items = out
	if r.ExpectedVersion < 0 {
		return r, fmt.Errorf("expected_version must be >= 0")
	}
	r.ExpectedEvent = strings.TrimSpace(r.ExpectedEvent)
	return r, nil
}

func (r ConfigPutRequest) Normalize() (ConfigPutRequest, error) {
	if strings.TrimSpace(r.Config.DM.Policy) == "" {
		return r, fmt.Errorf("config.dm.policy is required")
	}
	if r.Config.Version == 0 {
		r.Config.Version = 1
	}
	if r.ExpectedVersion < 0 {
		return r, fmt.Errorf("expected_version must be >= 0")
	}
	r.ExpectedEvent = strings.TrimSpace(r.ExpectedEvent)
	return r, nil
}

func (r ConfigSetRequest) Normalize() (ConfigSetRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	if r.Key == "" {
		return r, fmt.Errorf("key is required")
	}
	return r, nil
}

func (r ConfigApplyRequest) Normalize() (ConfigApplyRequest, error) {
	if strings.TrimSpace(r.Config.DM.Policy) == "" {
		return r, fmt.Errorf("config.dm.policy is required")
	}
	if r.Config.Version == 0 {
		r.Config.Version = 1
	}
	return r, nil
}

func (r ConfigPatchRequest) Normalize() (ConfigPatchRequest, error) {
	if len(r.Patch) == 0 {
		return r, fmt.Errorf("patch is required")
	}
	return r, nil
}

func (r LogsTailRequest) Normalize() (LogsTailRequest, error) {
	if r.Limit == 0 && r.Lines != 0 {
		r.Limit = r.Lines
	}
	r.Limit = normalizeLimit(r.Limit, 100, 2000)
	r.MaxBytes = normalizeLimit(r.MaxBytes, 64*1024, 2*1024*1024)
	if r.Cursor < 0 {
		r.Cursor = 0
	}
	return r, nil
}

func (r ChannelsStatusRequest) Normalize() (ChannelsStatusRequest, error) {
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 10_000, 60_000)
	return r, nil
}

func (r ChannelsLogoutRequest) Normalize() (ChannelsLogoutRequest, error) {
	r.Channel = strings.ToLower(strings.TrimSpace(r.Channel))
	if r.Channel == "" {
		return r, fmt.Errorf("channel is required")
	}
	return r, nil
}

func (r UsageCostRequest) Normalize() (UsageCostRequest, error) {
	r.StartDate = strings.TrimSpace(r.StartDate)
	r.EndDate = strings.TrimSpace(r.EndDate)
	r.Mode = strings.TrimSpace(r.Mode)
	r.UTCOffset = strings.TrimSpace(r.UTCOffset)
	if r.Days < 0 {
		return r, fmt.Errorf("days must be >= 0")
	}
	return r, nil
}

func (r AgentsListRequest) Normalize() (AgentsListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r AgentsCreateRequest) Normalize() (AgentsCreateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.Workspace = strings.TrimSpace(r.Workspace)
	r.Model = strings.TrimSpace(r.Model)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r AgentsUpdateRequest) Normalize() (AgentsUpdateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.Workspace = strings.TrimSpace(r.Workspace)
	r.Model = strings.TrimSpace(r.Model)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r AgentsDeleteRequest) Normalize() (AgentsDeleteRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	return r, nil
}

func (r AgentsFilesListRequest) Normalize() (AgentsFilesListRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 200, 1000)
	return r, nil
}

func (r AgentsFilesGetRequest) Normalize() (AgentsFilesGetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if !isSafeAgentFileName(r.Name) {
		return r, fmt.Errorf("invalid file name")
	}
	return r, nil
}

func (r AgentsFilesSetRequest) Normalize() (AgentsFilesSetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if !isSafeAgentFileName(r.Name) {
		return r, fmt.Errorf("invalid file name")
	}
	return r, nil
}

func (r ModelsListRequest) Normalize() (ModelsListRequest, error) {
	return r, nil
}

func (r ToolsCatalogRequest) Normalize() (ToolsCatalogRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r SkillsStatusRequest) Normalize() (SkillsStatusRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r SkillsInstallRequest) Normalize() (SkillsInstallRequest, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.InstallID = strings.TrimSpace(r.InstallID)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	if r.InstallID == "" {
		return r, fmt.Errorf("install_id is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 120_000, 600_000)
	return r, nil
}

func (r SkillsUpdateRequest) Normalize() (SkillsUpdateRequest, error) {
	r.SkillKey = strings.TrimSpace(r.SkillKey)
	if r.SkillKey == "" {
		return r, fmt.Errorf("skill_key is required")
	}
	if r.Env == nil {
		r.Env = map[string]string{}
	}
	for key, value := range r.Env {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			delete(r.Env, key)
			continue
		}
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			delete(r.Env, key)
			continue
		}
		if trimmedKey != key {
			delete(r.Env, key)
		}
		r.Env[trimmedKey] = trimmedValue
	}
	if r.APIKey != nil {
		trimmed := strings.TrimSpace(*r.APIKey)
		r.APIKey = &trimmed
	}
	return r, nil
}

func (r NodePairRequest) Normalize() (NodePairRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.DisplayName = strings.TrimSpace(r.DisplayName)
	r.Platform = strings.TrimSpace(r.Platform)
	r.Version = strings.TrimSpace(r.Version)
	r.CoreVersion = strings.TrimSpace(r.CoreVersion)
	r.UIVersion = strings.TrimSpace(r.UIVersion)
	r.DeviceFamily = strings.TrimSpace(r.DeviceFamily)
	r.ModelIdentifier = strings.TrimSpace(r.ModelIdentifier)
	r.RemoteIP = strings.TrimSpace(r.RemoteIP)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	if r.Permissions == nil {
		r.Permissions = map[string]any{}
	}
	return r, nil
}

func (r NodePairListRequest) Normalize() (NodePairListRequest, error) { return r, nil }

func (r NodePairApproveRequest) Normalize() (NodePairApproveRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r NodePairRejectRequest) Normalize() (NodePairRejectRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r NodePairVerifyRequest) Normalize() (NodePairVerifyRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Token = strings.TrimSpace(r.Token)
	if r.NodeID == "" || r.Token == "" {
		return r, fmt.Errorf("node_id and token are required")
	}
	return r, nil
}

func (r DevicePairListRequest) Normalize() (DevicePairListRequest, error) { return r, nil }

func (r DevicePairApproveRequest) Normalize() (DevicePairApproveRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r DevicePairRejectRequest) Normalize() (DevicePairRejectRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r DevicePairRemoveRequest) Normalize() (DevicePairRemoveRequest, error) {
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	if r.DeviceID == "" {
		return r, fmt.Errorf("device_id is required")
	}
	return r, nil
}

func (r DeviceTokenRotateRequest) Normalize() (DeviceTokenRotateRequest, error) {
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	r.Role = strings.TrimSpace(r.Role)
	if r.DeviceID == "" || r.Role == "" {
		return r, fmt.Errorf("device_id and role are required")
	}
	return r, nil
}

func (r DeviceTokenRevokeRequest) Normalize() (DeviceTokenRevokeRequest, error) {
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	r.Role = strings.TrimSpace(r.Role)
	if r.DeviceID == "" || r.Role == "" {
		return r, fmt.Errorf("device_id and role are required")
	}
	return r, nil
}

func SupportedMethods() []string {
	return []string{
		MethodSupportedMethods,
		MethodHealth,
		MethodDoctorMemoryStatus,
		MethodLogsTail,
		MethodChannelsStatus,
		MethodChannelsLogout,
		MethodStatus,
		MethodUsageStatus,
		MethodUsageCost,
		MethodMemorySearch,
		MethodAgent,
		MethodAgentWait,
		MethodAgentIdentityGet,
		MethodChatSend,
		MethodChatHistory,
		MethodChatAbort,
		MethodSessionGet,
		MethodSessionsList,
		MethodSessionsPreview,
		MethodSessionsPatch,
		MethodSessionsReset,
		MethodSessionsDelete,
		MethodSessionsCompact,
		MethodListGet,
		MethodListPut,
		MethodRelayPolicyGet,
		MethodConfigGet,
		MethodConfigPut,
		MethodConfigSet,
		MethodConfigApply,
		MethodConfigPatch,
		MethodConfigSchema,
		MethodAgentsList,
		MethodAgentsCreate,
		MethodAgentsUpdate,
		MethodAgentsDelete,
		MethodAgentsFilesList,
		MethodAgentsFilesGet,
		MethodAgentsFilesSet,
		MethodModelsList,
		MethodToolsCatalog,
		MethodSkillsStatus,
		MethodSkillsInstall,
		MethodSkillsUpdate,
		MethodNodePairRequest,
		MethodNodePairList,
		MethodNodePairApprove,
		MethodNodePairReject,
		MethodNodePairVerify,
		MethodDevicePairList,
		MethodDevicePairApprove,
		MethodDevicePairReject,
		MethodDevicePairRemove,
		MethodDeviceTokenRotate,
		MethodDeviceTokenRevoke,
	}
}

func DecodeMemorySearchParams(params json.RawMessage) (MemorySearchRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		query, ok := arr[0].(string)
		if !ok {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		req := MemorySearchRequest{Query: query}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return MemorySearchRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	return decodeMethodParams[MemorySearchRequest](params)
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
	return decodeMethodParams[ChatSendRequest](params)
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
	return decodeMethodParams[SessionGetRequest](params)
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
	return decodeMethodParams[ChatHistoryRequest](params)
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
	return decodeMethodParams[ChatAbortRequest](params)
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
	return decodeMethodParams[SessionsPreviewRequest](params)
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
	return decodeMethodParams[SessionsPatchRequest](params)
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
	return decodeMethodParams[SessionsResetRequest](params)
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
	return decodeMethodParams[SessionsDeleteRequest](params)
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
	return decodeMethodParams[SessionsCompactRequest](params)
}

func DecodeConfigPutParams(params json.RawMessage) (ConfigPutRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		var cfg state.ConfigDoc
		if err := json.Unmarshal(arr[0], &cfg); err != nil {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		req := ConfigPutRequest{Config: cfg}
		if len(arr) == 2 {
			if err := decodeWritePrecondition(arr[1], &req.ExpectedVersion, &req.ExpectedEvent); err != nil {
				return ConfigPutRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[ConfigPutRequest](params)
}

func DecodeConfigSetParams(params json.RawMessage) (ConfigSetRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		var req ConfigSetRequest
		if err := json.Unmarshal(arr[0], &req.Key); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		if err := json.Unmarshal(arr[1], &req.Value); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		return req, nil
	}
	return decodeMethodParams[ConfigSetRequest](params)
}

func DecodeConfigApplyParams(params json.RawMessage) (ConfigApplyRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		var cfg state.ConfigDoc
		if err := json.Unmarshal(arr[0], &cfg); err != nil {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		return ConfigApplyRequest{Config: cfg}, nil
	}
	return decodeMethodParams[ConfigApplyRequest](params)
}

func DecodeConfigPatchParams(params json.RawMessage) (ConfigPatchRequest, error) {
	if isJSONArray(params) {
		var arr []map[string]any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		return ConfigPatchRequest{Patch: arr[0]}, nil
	}
	req, err := decodeMethodParams[ConfigPatchRequest](params)
	if err != nil {
		return ConfigPatchRequest{}, err
	}
	if len(req.Patch) > 0 {
		return req, nil
	}
	patch, err := decodeMethodParams[map[string]any](params)
	if err != nil {
		return ConfigPatchRequest{}, err
	}
	return ConfigPatchRequest{Patch: patch}, nil
}

func DecodeLogsTailParams(params json.RawMessage) (LogsTailRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return LogsTailRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 3 {
			return LogsTailRequest{}, fmt.Errorf("invalid params")
		}
		req := LogsTailRequest{}
		if len(arr) >= 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.Cursor = int64(v)
			case int:
				req.Cursor = int64(v)
			}
		}
		if len(arr) >= 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return LogsTailRequest{}, fmt.Errorf("invalid params")
			}
		}
		if len(arr) == 3 {
			switch v := arr[2].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.MaxBytes = int(v)
			case int:
				req.MaxBytes = v
			default:
				return LogsTailRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[LogsTailRequest](params)
}

func DecodeChannelsStatusParams(params json.RawMessage) (ChannelsStatusRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 2 {
			return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
		}
		req := ChannelsStatusRequest{}
		if len(arr) >= 1 {
			b, ok := arr[0].(bool)
			if !ok {
				return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
			}
			req.Probe = b
		}
		if len(arr) == 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[ChannelsStatusRequest](params)
}

func DecodeChannelsLogoutParams(params json.RawMessage) (ChannelsLogoutRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		channel, ok := arr[0].(string)
		if !ok {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		return ChannelsLogoutRequest{Channel: channel}, nil
	}
	return decodeMethodParams[ChannelsLogoutRequest](params)
}

func DecodeUsageCostParams(params json.RawMessage) (UsageCostRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 {
			return UsageCostRequest{}, nil
		}
		var req UsageCostRequest
		if err := json.Unmarshal(arr[0], &req); err != nil {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return UsageCostRequest{}, nil
	}
	return decodeMethodParams[UsageCostRequest](params)
}

func DecodeListGetParams(params json.RawMessage) (ListGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[0].(string)
		if !ok {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		return ListGetRequest{Name: name}, nil
	}
	return decodeMethodParams[ListGetRequest](params)
}

func DecodeListPutParams(params json.RawMessage) (ListPutRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) < 2 || len(arr) > 3 {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		var name string
		if err := json.Unmarshal(arr[0], &name); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		var items []string
		if err := json.Unmarshal(arr[1], &items); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		req := ListPutRequest{Name: name, Items: items}
		if len(arr) == 3 {
			if err := decodeWritePrecondition(arr[2], &req.ExpectedVersion, &req.ExpectedEvent); err != nil {
				return ListPutRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[ListPutRequest](params)
}

func DecodeAgentsListParams(params json.RawMessage) (AgentsListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return AgentsListRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentsListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentsListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return AgentsListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return AgentsListRequest{}, nil
	}
	return decodeMethodParams[AgentsListRequest](params)
}

func DecodeAgentsCreateParams(params json.RawMessage) (AgentsCreateRequest, error) {
	return decodeMethodParams[AgentsCreateRequest](params)
}

func DecodeAgentsUpdateParams(params json.RawMessage) (AgentsUpdateRequest, error) {
	return decodeMethodParams[AgentsUpdateRequest](params)
}

func DecodeAgentsDeleteParams(params json.RawMessage) (AgentsDeleteRequest, error) {
	return decodeMethodParams[AgentsDeleteRequest](params)
}

func DecodeAgentsFilesListParams(params json.RawMessage) (AgentsFilesListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentsFilesListRequest{AgentID: agentID}
		if len(arr) == 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[AgentsFilesListRequest](params)
}

func DecodeAgentsFilesGetParams(params json.RawMessage) (AgentsFilesGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		return AgentsFilesGetRequest{AgentID: agentID, Name: name}, nil
	}
	return decodeMethodParams[AgentsFilesGetRequest](params)
}

func DecodeAgentsFilesSetParams(params json.RawMessage) (AgentsFilesSetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 3 {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		content, ok := arr[2].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		return AgentsFilesSetRequest{AgentID: agentID, Name: name, Content: content}, nil
	}
	return decodeMethodParams[AgentsFilesSetRequest](params)
}

func DecodeModelsListParams(params json.RawMessage) (ModelsListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ModelsListRequest{}, nil
	}
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ModelsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 0 {
			return ModelsListRequest{}, fmt.Errorf("invalid params")
		}
		return ModelsListRequest{}, nil
	}
	return decodeMethodParams[ModelsListRequest](params)
}

func DecodeToolsCatalogParams(params json.RawMessage) (ToolsCatalogRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolsCatalogRequest{}, nil
	}
	return decodeMethodParams[ToolsCatalogRequest](params)
}

func DecodeSkillsStatusParams(params json.RawMessage) (SkillsStatusRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SkillsStatusRequest{}, nil
	}
	return decodeMethodParams[SkillsStatusRequest](params)
}

func DecodeSkillsInstallParams(params json.RawMessage) (SkillsInstallRequest, error) {
	return decodeMethodParams[SkillsInstallRequest](params)
}

func DecodeSkillsUpdateParams(params json.RawMessage) (SkillsUpdateRequest, error) {
	return decodeMethodParams[SkillsUpdateRequest](params)
}

func DecodeNodePairRequestParams(params json.RawMessage) (NodePairRequest, error) {
	return decodeMethodParams[NodePairRequest](params)
}

func DecodeNodePairListParams(params json.RawMessage) (NodePairListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return NodePairListRequest{}, nil
	}
	return decodeMethodParams[NodePairListRequest](params)
}

func DecodeNodePairApproveParams(params json.RawMessage) (NodePairApproveRequest, error) {
	return decodeMethodParams[NodePairApproveRequest](params)
}

func DecodeNodePairRejectParams(params json.RawMessage) (NodePairRejectRequest, error) {
	return decodeMethodParams[NodePairRejectRequest](params)
}

func DecodeNodePairVerifyParams(params json.RawMessage) (NodePairVerifyRequest, error) {
	return decodeMethodParams[NodePairVerifyRequest](params)
}

func DecodeDevicePairListParams(params json.RawMessage) (DevicePairListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return DevicePairListRequest{}, nil
	}
	return decodeMethodParams[DevicePairListRequest](params)
}

func DecodeDevicePairApproveParams(params json.RawMessage) (DevicePairApproveRequest, error) {
	return decodeMethodParams[DevicePairApproveRequest](params)
}

func DecodeDevicePairRejectParams(params json.RawMessage) (DevicePairRejectRequest, error) {
	return decodeMethodParams[DevicePairRejectRequest](params)
}

func DecodeDevicePairRemoveParams(params json.RawMessage) (DevicePairRemoveRequest, error) {
	return decodeMethodParams[DevicePairRemoveRequest](params)
}

func DecodeDeviceTokenRotateParams(params json.RawMessage) (DeviceTokenRotateRequest, error) {
	return decodeMethodParams[DeviceTokenRotateRequest](params)
}

func DecodeDeviceTokenRevokeParams(params json.RawMessage) (DeviceTokenRevokeRequest, error) {
	return decodeMethodParams[DeviceTokenRevokeRequest](params)
}

func decodeMethodParams[T any](params json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(params)) == 0 {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid params")
	}
	return out, nil
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func normalizeLimit(value, def, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func normalizeListName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if utf8.RuneCountInString(name) > 64 {
		name = truncateRunes(name, 64)
	}
	return name
}

func normalizeAgentID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if utf8.RuneCountInString(id) > 64 {
		id = truncateRunes(id, 64)
	}
	return id
}

func isSafeAgentFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	if strings.ContainsAny(name, "\\/") {
		return false
	}
	return true
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func decodeWritePrecondition(raw json.RawMessage, expectedVersion *int, expectedEvent *string) error {
	var pre struct {
		ExpectedVersion *int   `json:"expected_version"`
		ExpectedEvent   string `json:"expected_event"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pre); err != nil {
		return err
	}
	if pre.ExpectedVersion != nil {
		*expectedVersion = *pre.ExpectedVersion
	}
	*expectedEvent = pre.ExpectedEvent
	return nil
}
