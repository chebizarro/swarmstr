package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type ExecApprovalsGetRequest struct{}

type ExecApprovalsSetRequest struct {
	Approvals map[string]any `json:"approvals"`
}

type ExecApprovalsNodeGetRequest struct {
	NodeID string `json:"node_id"`
}

type ExecApprovalsNodeSetRequest struct {
	NodeID    string         `json:"node_id"`
	Approvals map[string]any `json:"approvals"`
}

// ExecApprovalMutableFileOperand tracks a file operand that may be mutated
// by the approved command, enabling content-aware approval decisions.
type ExecApprovalMutableFileOperand struct {
	ArgvIndex int    `json:"argvIndex"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
}

// ExecApprovalSystemRunPlan describes the fully resolved execution plan for
// a system.run command awaiting approval.
type ExecApprovalSystemRunPlan struct {
	Argv               []string                        `json:"argv"`
	CWD                *string                         `json:"cwd"`
	CommandText        string                          `json:"commandText"`
	CommandPreview     *string                         `json:"commandPreview,omitempty"`
	AgentID            *string                         `json:"agentId"`
	SessionKey         *string                         `json:"sessionKey"`
	MutableFileOperand *ExecApprovalMutableFileOperand `json:"mutableFileOperand,omitempty"`
}

type ExecApprovalRequestRequest struct {
	ID                   string                     `json:"id,omitempty"`
	NodeID               string                     `json:"node_id,omitempty"`
	Command              string                     `json:"command"`
	CommandArgv          []string                   `json:"command_argv,omitempty"`
	SystemRunPlan        *ExecApprovalSystemRunPlan  `json:"system_run_plan,omitempty"`
	Args                 map[string]any             `json:"args,omitempty"`
	Env                  map[string]string          `json:"env,omitempty"`
	CWD                  *string                    `json:"cwd,omitempty"`
	Host                 *string                    `json:"host,omitempty"`
	Security             *string                    `json:"security,omitempty"`
	Ask                  *string                    `json:"ask,omitempty"`
	AgentID              *string                    `json:"agent_id,omitempty"`
	ResolvedPath         *string                    `json:"resolved_path,omitempty"`
	SessionKey           *string                    `json:"sessionKey,omitempty"`
	TurnSourceChannel    *string                    `json:"turn_source_channel,omitempty"`
	TurnSourceTo         *string                    `json:"turn_source_to,omitempty"`
	TurnSourceAccountID  *string                    `json:"turn_source_account_id,omitempty"`
	TurnSourceThreadID   *string                    `json:"turn_source_thread_id,omitempty"`
	TimeoutMS            int                        `json:"timeout_ms,omitempty"`
	TwoPhase             *bool                      `json:"two_phase,omitempty"`
}

type ExecApprovalWaitDecisionRequest struct {
	ID        string `json:"id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type ExecApprovalResolveRequest struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

func (r ExecApprovalsGetRequest) Normalize() (ExecApprovalsGetRequest, error) { return r, nil }

func (r ExecApprovalsSetRequest) Normalize() (ExecApprovalsSetRequest, error) {
	if r.Approvals == nil {
		r.Approvals = map[string]any{}
	}
	return r, nil
}

func (r ExecApprovalsNodeGetRequest) Normalize() (ExecApprovalsNodeGetRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r ExecApprovalsNodeSetRequest) Normalize() (ExecApprovalsNodeSetRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	if r.Approvals == nil {
		r.Approvals = map[string]any{}
	}
	return r, nil
}

func (r ExecApprovalRequestRequest) Normalize() (ExecApprovalRequestRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Command = strings.TrimSpace(r.Command)
	if r.Command == "" && len(r.CommandArgv) == 0 {
		return r, fmt.Errorf("command or command_argv is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 60_000, 600_000)
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	return r, nil
}

func (r ExecApprovalWaitDecisionRequest) Normalize() (ExecApprovalWaitDecisionRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 30_000, 600_000)
	return r, nil
}

func (r ExecApprovalResolveRequest) Normalize() (ExecApprovalResolveRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Decision = strings.TrimSpace(r.Decision)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.ID == "" || r.Decision == "" {
		return r, fmt.Errorf("id and decision are required")
	}
	return r, nil
}

func DecodeExecApprovalsGetParams(params json.RawMessage) (ExecApprovalsGetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ExecApprovalsGetRequest{}, nil
	}
	return decodeMethodParams[ExecApprovalsGetRequest](params)
}

func DecodeExecApprovalsSetParams(params json.RawMessage) (ExecApprovalsSetRequest, error) {
	return decodeMethodParams[ExecApprovalsSetRequest](params)
}

func DecodeExecApprovalRequestParams(params json.RawMessage) (ExecApprovalRequestRequest, error) {
	return decodeMethodParams[ExecApprovalRequestRequest](params)
}

func DecodeExecApprovalWaitDecisionParams(params json.RawMessage) (ExecApprovalWaitDecisionRequest, error) {
	return decodeMethodParams[ExecApprovalWaitDecisionRequest](params)
}

func DecodeExecApprovalResolveParams(params json.RawMessage) (ExecApprovalResolveRequest, error) {
	return decodeMethodParams[ExecApprovalResolveRequest](params)
}
