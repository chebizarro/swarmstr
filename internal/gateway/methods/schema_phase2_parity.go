package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Phase 2 priority contracts mirror the current OpenClaw gateway wire names.
// They intentionally model validation/catalog parity only; runtime dispatch is
// owned by the daemon integration layer.

type SessionsGetRequest struct {
	Key        string `json:"key,omitempty"`
	SessionKey string `json:"sessionKey,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
}

func (r SessionsGetRequest) Normalize() (SessionsGetRequest, error) {
	r.Key, r.SessionKey, r.AgentID = strings.TrimSpace(r.Key), strings.TrimSpace(r.SessionKey), strings.TrimSpace(r.AgentID)
	if r.Key == "" {
		r.Key = r.SessionKey
	}
	if r.Key == "" {
		return r, fmt.Errorf("key or sessionKey is required")
	}
	if r.Limit < 0 {
		return r, fmt.Errorf("limit must be non-negative")
	}
	return r, nil
}

func DecodeSessionsGetParams(raw json.RawMessage) (SessionsGetRequest, error) {
	return decodeMethodParams[SessionsGetRequest](raw)
}

type SessionsResolveRequest struct {
	Key            string `json:"key,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	Label          string `json:"label,omitempty"`
	ShortID        string `json:"shortId,omitempty"`
	SlugHint       string `json:"slugHint,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	SpawnedBy      string `json:"spawnedBy,omitempty"`
	IncludeGlobal  bool   `json:"includeGlobal,omitempty"`
	IncludeUnknown bool   `json:"includeUnknown,omitempty"`
	AllowMissing   bool   `json:"allowMissing,omitempty"`
}

func (r SessionsResolveRequest) Normalize() (SessionsResolveRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Label = strings.TrimSpace(r.Label)
	r.ShortID = strings.TrimSpace(r.ShortID)
	r.SlugHint = strings.TrimSpace(r.SlugHint)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SpawnedBy = strings.TrimSpace(r.SpawnedBy)
	return r, nil
}

func DecodeSessionsResolveParams(raw json.RawMessage) (SessionsResolveRequest, error) {
	return decodeMethodParams[SessionsResolveRequest](raw)
}

type SessionsRecoverRequest struct {
	Key     string `json:"key"`
	AgentID string `json:"agent_id,omitempty"`
}

func (r SessionsRecoverRequest) Normalize() (SessionsRecoverRequest, error) {
	r.Key, r.AgentID = strings.TrimSpace(r.Key), strings.TrimSpace(r.AgentID)
	if r.Key == "" {
		return r, fmt.Errorf("key is required")
	}
	return r, nil
}

func DecodeSessionsRecoverParams(raw json.RawMessage) (SessionsRecoverRequest, error) {
	return decodeMethodParams[SessionsRecoverRequest](raw)
}

type SessionsSteerRequest struct {
	Key            string `json:"key"`
	Message        string `json:"message"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
}

func (r SessionsSteerRequest) Normalize() (SessionsSteerRequest, error) {
	r.Key, r.Message = strings.TrimSpace(r.Key), strings.TrimSpace(r.Message)
	r.IdempotencyKey, r.AgentID = strings.TrimSpace(r.IdempotencyKey), strings.TrimSpace(r.AgentID)
	if r.Key == "" || r.Message == "" {
		return r, fmt.Errorf("key and message are required")
	}
	return r, nil
}

func DecodeSessionsSteerParams(raw json.RawMessage) (SessionsSteerRequest, error) {
	return decodeMethodParams[SessionsSteerRequest](raw)
}

type SessionsViewersSetRequest struct {
	AgentID     string   `json:"agent_id,omitempty"`
	SessionKeys []string `json:"sessionKeys"`
}

func (r SessionsViewersSetRequest) Normalize() (SessionsViewersSetRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	if len(r.SessionKeys) > 32 {
		return r, fmt.Errorf("sessionKeys must contain at most 32 entries")
	}
	seen := make(map[string]struct{}, len(r.SessionKeys))
	out := make([]string, 0, len(r.SessionKeys))
	for _, key := range r.SessionKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			return r, fmt.Errorf("sessionKeys entries must be non-empty")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	r.SessionKeys = out
	return r, nil
}

func DecodeSessionsViewersSetParams(raw json.RawMessage) (SessionsViewersSetRequest, error) {
	return decodeMethodParams[SessionsViewersSetRequest](raw)
}

type SessionOwner struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type SessionsAssignOwnerRequest struct {
	Key     string       `json:"key"`
	AgentID string       `json:"agent_id,omitempty"`
	Owner   SessionOwner `json:"owner"`
}

func (r SessionsAssignOwnerRequest) Normalize() (SessionsAssignOwnerRequest, error) {
	r.Key, r.AgentID = strings.TrimSpace(r.Key), strings.TrimSpace(r.AgentID)
	r.Owner.Type, r.Owner.ID = strings.TrimSpace(r.Owner.Type), strings.TrimSpace(r.Owner.ID)
	if r.Key == "" || r.Owner.ID == "" || (r.Owner.Type != "agent" && r.Owner.Type != "human") {
		return r, fmt.Errorf("key and a valid agent or human owner are required")
	}
	return r, nil
}

func DecodeSessionsAssignOwnerParams(raw json.RawMessage) (SessionsAssignOwnerRequest, error) {
	return decodeMethodParams[SessionsAssignOwnerRequest](raw)
}

type SessionsGroupsDefaultsRequest struct{}

func DecodeSessionsGroupsDefaultsParams(raw json.RawMessage) (SessionsGroupsDefaultsRequest, error) {
	return decodeMethodParams[SessionsGroupsDefaultsRequest](raw)
}

type SessionsGroupsUpdateRequest struct {
	Name     string  `json:"name"`
	CWD      *string `json:"cwd"`
	Worktree bool    `json:"worktree"`
}

func (r SessionsGroupsUpdateRequest) Normalize() (SessionsGroupsUpdateRequest, error) {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	if r.CWD != nil {
		cwd := strings.TrimSpace(*r.CWD)
		if cwd == "" {
			return r, fmt.Errorf("cwd must be non-empty or null")
		}
		r.CWD = &cwd
	}
	return r, nil
}

func DecodeSessionsGroupsUpdateParams(raw json.RawMessage) (SessionsGroupsUpdateRequest, error) {
	return decodeMethodParams[SessionsGroupsUpdateRequest](raw)
}

type TasksRecoveryRequest struct {
	TaskIDs []string `json:"taskIds"`
}

func (r TasksRecoveryRequest) Normalize() (TasksRecoveryRequest, error) {
	if len(r.TaskIDs) < 1 || len(r.TaskIDs) > 10 {
		return r, fmt.Errorf("taskIds must contain between 1 and 10 entries")
	}
	for i := range r.TaskIDs {
		r.TaskIDs[i] = strings.TrimSpace(r.TaskIDs[i])
		if r.TaskIDs[i] == "" {
			return r, fmt.Errorf("taskIds entries must be non-empty")
		}
	}
	return r, nil
}

func DecodeTasksRecoveryParams(raw json.RawMessage) (TasksRecoveryRequest, error) {
	return decodeMethodParams[TasksRecoveryRequest](raw)
}

type ExecApprovalGrantsListRequest struct {
	Limit int `json:"limit,omitempty"`
}

func (r ExecApprovalGrantsListRequest) Normalize() (ExecApprovalGrantsListRequest, error) {
	if r.Limit < 0 || r.Limit > 500 {
		return r, fmt.Errorf("limit must be between 1 and 500 when provided")
	}
	return r, nil
}

func DecodeExecApprovalGrantsListParams(raw json.RawMessage) (ExecApprovalGrantsListRequest, error) {
	return decodeMethodParams[ExecApprovalGrantsListRequest](raw)
}

type ExecApprovalGrantsRevokeRequest struct {
	GrantID string `json:"grantId"`
}

func (r ExecApprovalGrantsRevokeRequest) Normalize() (ExecApprovalGrantsRevokeRequest, error) {
	r.GrantID = strings.TrimSpace(r.GrantID)
	if r.GrantID == "" {
		return r, fmt.Errorf("grantId is required")
	}
	return r, nil
}

func DecodeExecApprovalGrantsRevokeParams(raw json.RawMessage) (ExecApprovalGrantsRevokeRequest, error) {
	return decodeMethodParams[ExecApprovalGrantsRevokeRequest](raw)
}

type DevicePairSetupCodeRequest struct {
	PublicURL        string `json:"publicUrl,omitempty"`
	PreferRemoteURL  bool   `json:"preferRemoteUrl,omitempty"`
	IncludeQR        bool   `json:"includeQr,omitempty"`
	BootstrapProfile string `json:"bootstrapProfile,omitempty"`
	JoinURL          *bool  `json:"joinUrl,omitempty"`
}

func (r DevicePairSetupCodeRequest) Normalize() (DevicePairSetupCodeRequest, error) {
	r.PublicURL = strings.TrimSpace(r.PublicURL)
	r.BootstrapProfile = strings.TrimSpace(r.BootstrapProfile)
	if r.BootstrapProfile != "" && r.BootstrapProfile != "limited" && r.BootstrapProfile != "node" {
		return r, fmt.Errorf("bootstrapProfile must be limited or node")
	}
	if r.JoinURL != nil && !*r.JoinURL {
		return r, fmt.Errorf("joinUrl may only be true when provided")
	}
	return r, nil
}

func DecodeDevicePairSetupCodeParams(raw json.RawMessage) (DevicePairSetupCodeRequest, error) {
	return decodeMethodParams[DevicePairSetupCodeRequest](raw)
}

type DevicePairSetupStatusRequest struct {
	SetupID string `json:"setupId"`
}

func (r DevicePairSetupStatusRequest) Normalize() (DevicePairSetupStatusRequest, error) {
	r.SetupID = strings.TrimSpace(r.SetupID)
	if r.SetupID == "" {
		return r, fmt.Errorf("setupId is required")
	}
	return r, nil
}

func DecodeDevicePairSetupStatusParams(raw json.RawMessage) (DevicePairSetupStatusRequest, error) {
	return decodeMethodParams[DevicePairSetupStatusRequest](raw)
}

type DeviceScopesRequestUpgradeRequest struct {
	Scopes []string `json:"scopes"`
}

func (r DeviceScopesRequestUpgradeRequest) Normalize() (DeviceScopesRequestUpgradeRequest, error) {
	if len(r.Scopes) < 1 || len(r.Scopes) > 8 {
		return r, fmt.Errorf("scopes must contain between 1 and 8 entries")
	}
	seen := make(map[string]struct{}, len(r.Scopes))
	for i := range r.Scopes {
		r.Scopes[i] = strings.TrimSpace(r.Scopes[i])
		if r.Scopes[i] == "" {
			return r, fmt.Errorf("scopes entries must be non-empty")
		}
		if _, ok := seen[r.Scopes[i]]; ok {
			return r, fmt.Errorf("scopes entries must be unique")
		}
		seen[r.Scopes[i]] = struct{}{}
	}
	return r, nil
}

func DecodeDeviceScopesRequestUpgradeParams(raw json.RawMessage) (DeviceScopesRequestUpgradeRequest, error) {
	return decodeMethodParams[DeviceScopesRequestUpgradeRequest](raw)
}

type DeviceScopesWaitUpgradeRequest struct {
	RequestID string `json:"request_id"`
}

func (r DeviceScopesWaitUpgradeRequest) Normalize() (DeviceScopesWaitUpgradeRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("requestId is required")
	}
	return r, nil
}

func DecodeDeviceScopesWaitUpgradeParams(raw json.RawMessage) (DeviceScopesWaitUpgradeRequest, error) {
	return decodeMethodParams[DeviceScopesWaitUpgradeRequest](raw)
}

type NodeWorkerCapacity struct {
	Total     int `json:"total"`
	Available int `json:"available"`
}

type NodeWorkerHostDeclaration struct {
	Enabled  bool               `json:"enabled"`
	Capacity NodeWorkerCapacity `json:"capacity"`
}

type NodeRunnerInventoryUpdateRequest struct {
	ProtocolFeatures []string                   `json:"protocolFeatures"`
	WorkerHost       *NodeWorkerHostDeclaration `json:"workerHost,omitempty"`
}

func (r NodeRunnerInventoryUpdateRequest) Normalize() (NodeRunnerInventoryUpdateRequest, error) {
	for i := range r.ProtocolFeatures {
		r.ProtocolFeatures[i] = strings.TrimSpace(r.ProtocolFeatures[i])
		if r.ProtocolFeatures[i] == "" {
			return r, fmt.Errorf("protocolFeatures entries must be non-empty")
		}
	}
	if r.WorkerHost != nil && (r.WorkerHost.Capacity.Total < 0 || r.WorkerHost.Capacity.Available < 0 || r.WorkerHost.Capacity.Available > r.WorkerHost.Capacity.Total) {
		return r, fmt.Errorf("workerHost capacity is invalid")
	}
	return r, nil
}

func DecodeNodeRunnerInventoryUpdateParams(raw json.RawMessage) (NodeRunnerInventoryUpdateRequest, error) {
	return decodeMethodParams[NodeRunnerInventoryUpdateRequest](raw)
}
