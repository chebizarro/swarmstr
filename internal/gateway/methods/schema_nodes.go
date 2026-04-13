package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

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

type NodeListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type NodeDescribeRequest struct {
	NodeID string `json:"node_id"`
}

type NodeRenameRequest struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
}

type NodeCanvasCapabilityRefreshRequest struct {
	NodeID string `json:"node_id"`
}

type NodeInvokeRequest struct {
	NodeID    string         `json:"node_id"`
	Command   string         `json:"command"`
	Args      map[string]any `json:"args,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
}

type NodeEventRequest struct {
	RunID   string         `json:"run_id"`
	NodeID  string         `json:"node_id,omitempty"`
	Type    string         `json:"type"`
	Status  string         `json:"status,omitempty"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

type NodeResultRequest struct {
	RunID  string `json:"run_id"`
	NodeID string `json:"node_id,omitempty"`
	Status string `json:"status,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type NodePendingEnqueueRequest struct {
	NodeID         string         `json:"node_id"`
	Command        string         `json:"command"`
	Args           map[string]any `json:"args,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	TTLMS          int            `json:"ttl_ms,omitempty"`
}

type NodePendingPullRequest struct {
	NodeID string `json:"node_id,omitempty"`
}

type NodePendingAckRequest struct {
	NodeID string   `json:"node_id"`
	IDs    []string `json:"ids,omitempty"`
}

type NodePendingDrainRequest struct {
	NodeID   string `json:"node_id"`
	MaxItems int    `json:"max_items,omitempty"`
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

func (r NodeListRequest) Normalize() (NodeListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r NodeDescribeRequest) Normalize() (NodeDescribeRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r NodeRenameRequest) Normalize() (NodeRenameRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Name = strings.TrimSpace(r.Name)
	if r.NodeID == "" || r.Name == "" {
		return r, fmt.Errorf("node_id and name are required")
	}
	return r, nil
}

func (r NodeCanvasCapabilityRefreshRequest) Normalize() (NodeCanvasCapabilityRefreshRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r NodeInvokeRequest) Normalize() (NodeInvokeRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Command = strings.TrimSpace(r.Command)
	r.RunID = strings.TrimSpace(r.RunID)
	if r.NodeID == "" || r.Command == "" {
		return r, fmt.Errorf("node_id and command are required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 30_000, 300_000)
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	return r, nil
}

func (r NodeEventRequest) Normalize() (NodeEventRequest, error) {
	r.RunID = strings.TrimSpace(r.RunID)
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Type = strings.TrimSpace(r.Type)
	r.Status = strings.TrimSpace(r.Status)
	r.Message = strings.TrimSpace(r.Message)
	if r.RunID == "" || r.Type == "" {
		return r, fmt.Errorf("run_id and type are required")
	}
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	return r, nil
}

func (r NodeResultRequest) Normalize() (NodeResultRequest, error) {
	r.RunID = strings.TrimSpace(r.RunID)
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Status = strings.TrimSpace(r.Status)
	r.Error = strings.TrimSpace(r.Error)
	if r.RunID == "" {
		return r, fmt.Errorf("run_id is required")
	}
	if r.Status == "" {
		r.Status = "ok"
	}
	return r, nil
}

func (r NodePendingEnqueueRequest) Normalize() (NodePendingEnqueueRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Command = strings.TrimSpace(r.Command)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.NodeID == "" || r.Command == "" {
		return r, fmt.Errorf("node_id and command are required")
	}
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	if r.TTLMS < 0 {
		r.TTLMS = 0
	}
	return r, nil
}

func (r NodePendingPullRequest) Normalize() (NodePendingPullRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r NodePendingAckRequest) Normalize() (NodePendingAckRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	out := make([]string, 0, len(r.IDs))
	for _, id := range r.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	r.IDs = out
	return r, nil
}

func (r NodePendingDrainRequest) Normalize() (NodePendingDrainRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	if r.MaxItems < 0 {
		r.MaxItems = 0
	}
	return r, nil
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

func DecodeNodeListParams(params json.RawMessage) (NodeListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return NodeListRequest{}, nil
	}
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return NodeListRequest{}, fmt.Errorf("invalid params")
		}
		req := NodeListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return NodeListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return NodeListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[NodeListRequest](params)
}

func DecodeNodeDescribeParams(params json.RawMessage) (NodeDescribeRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeDescribeRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return NodeDescribeRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeDescribeRequest{}, fmt.Errorf("invalid params")
		}
		return NodeDescribeRequest{NodeID: nodeID}, nil
	}
	return decodeMethodParams[NodeDescribeRequest](params)
}

func DecodeNodeRenameParams(params json.RawMessage) (NodeRenameRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		return NodeRenameRequest{NodeID: nodeID, Name: name}, nil
	}
	return decodeMethodParams[NodeRenameRequest](params)
}

func DecodeNodeCanvasCapabilityRefreshParams(params json.RawMessage) (NodeCanvasCapabilityRefreshRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeCanvasCapabilityRefreshRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return NodeCanvasCapabilityRefreshRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeCanvasCapabilityRefreshRequest{}, fmt.Errorf("invalid params")
		}
		return NodeCanvasCapabilityRefreshRequest{NodeID: nodeID}, nil
	}
	return decodeMethodParams[NodeCanvasCapabilityRefreshRequest](params)
}

func DecodeNodeInvokeParams(params json.RawMessage) (NodeInvokeRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeInvokeRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 4 {
			return NodeInvokeRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeInvokeRequest{}, fmt.Errorf("invalid params")
		}
		req := NodeInvokeRequest{NodeID: nodeID}
		if len(arr) > 1 {
			command, ok := arr[1].(string)
			if !ok {
				return NodeInvokeRequest{}, fmt.Errorf("invalid params")
			}
			req.Command = command
		}
		if len(arr) > 2 {
			args, ok := arr[2].(map[string]any)
			if !ok {
				return NodeInvokeRequest{}, fmt.Errorf("invalid params")
			}
			req.Args = args
		}
		if len(arr) > 3 {
			switch v := arr[3].(type) {
			case float64:
				if math.Trunc(v) != v {
					return NodeInvokeRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return NodeInvokeRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[NodeInvokeRequest](params)
}

func DecodeNodeEventParams(params json.RawMessage) (NodeEventRequest, error) {
	return decodeMethodParams[NodeEventRequest](params)
}

func DecodeNodeResultParams(params json.RawMessage) (NodeResultRequest, error) {
	return decodeMethodParams[NodeResultRequest](params)
}

func DecodeNodePendingEnqueueParams(params json.RawMessage) (NodePendingEnqueueRequest, error) {
	return decodeMethodParams[NodePendingEnqueueRequest](params)
}

func DecodeNodePendingPullParams(params json.RawMessage) (NodePendingPullRequest, error) {
	return decodeMethodParams[NodePendingPullRequest](params)
}

func DecodeNodePendingAckParams(params json.RawMessage) (NodePendingAckRequest, error) {
	return decodeMethodParams[NodePendingAckRequest](params)
}

func DecodeNodePendingDrainParams(params json.RawMessage) (NodePendingDrainRequest, error) {
	return decodeMethodParams[NodePendingDrainRequest](params)
}

func DecodeExecApprovalsNodeGetParams(params json.RawMessage) (ExecApprovalsNodeGetRequest, error) {
	return decodeMethodParams[ExecApprovalsNodeGetRequest](params)
}

func DecodeExecApprovalsNodeSetParams(params json.RawMessage) (ExecApprovalsNodeSetRequest, error) {
	return decodeMethodParams[ExecApprovalsNodeSetRequest](params)
}
