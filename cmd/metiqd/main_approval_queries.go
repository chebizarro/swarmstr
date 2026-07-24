package main

import (
	"fmt"
	"strings"

	"metiq/internal/gateway/methods"
)

func applyExecApprovalGet(reg *execApprovalsRegistry, req methods.ExecApprovalGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec, err := reg.GetPending(req.ID)
	if err != nil {
		return nil, err
	}
	return execApprovalPendingSummary(rec), nil
}

func applyExecApprovalList(reg *execApprovalsRegistry, _ methods.ExecApprovalListRequest) ([]map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	records := reg.ListPending()
	out := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		out = append(out, execApprovalPendingSummary(rec))
	}
	return out, nil
}

func execApprovalAllowedDecisions(rec execApprovalPendingRecord) []string {
	out := []string{"allow-once"}
	if rec.AllowAlwaysAvailable {
		out = append(out, "allow-always")
	}
	return append(out, "deny")
}

func execApprovalPendingSummary(rec execApprovalPendingRecord) map[string]any {
	commandText := strings.TrimSpace(rec.Command)
	if commandText == "" {
		commandText = strings.Join(rec.CommandArgv, " ")
	}
	out := map[string]any{
		"id":                     rec.ID,
		"kind":                   "exec",
		"status":                 "pending",
		"command":                commandText,
		"commandText":            commandText,
		"commandPreview":         commandText,
		"command_argv":           append([]string(nil), rec.CommandArgv...),
		"allowedDecisions":       execApprovalAllowedDecisions(rec),
		"expires_at":             rec.ExpiresAt,
		"expiresAtMs":            rec.ExpiresAt,
		"requested_at":           rec.Requested,
		"createdAtMs":            rec.Requested,
		"allow_always_available": rec.AllowAlwaysAvailable,
	}
	if rec.NodeID != "" {
		out["node_id"] = rec.NodeID
		out["nodeId"] = rec.NodeID
	}
	if rec.AgentID != nil {
		out["agent_id"] = *rec.AgentID
		out["agentId"] = *rec.AgentID
	}
	if rec.SessionKey != nil {
		out["session_key"] = *rec.SessionKey
		out["sessionKey"] = *rec.SessionKey
	}
	if rec.Host != nil {
		out["host"] = *rec.Host
	}
	if rec.CWD != nil {
		out["cwd"] = *rec.CWD
	}
	if len(rec.AnalysisWarnings) > 0 {
		out["analysis_warnings"] = append([]string(nil), rec.AnalysisWarnings...)
	}
	if rec.AnalysisSummary != "" {
		out["analysis_summary"] = rec.AnalysisSummary
	}
	if rec.AnalysisSignature != "" {
		out["analysis_signature"] = rec.AnalysisSignature
	}
	if rec.AllowAlwaysReason != "" {
		out["allow_always_reason"] = rec.AllowAlwaysReason
	}
	if rec.ApprovalMode != "" {
		out["approval_mode"] = rec.ApprovalMode
	}
	return out
}

func approvalPendingSnapshot(rec execApprovalPendingRecord) map[string]any {
	summary := execApprovalPendingSummary(rec)
	presentation := map[string]any{
		"kind":             "exec",
		"commandText":      summary["commandText"],
		"commandPreview":   summary["commandPreview"],
		"warningText":      nil,
		"host":             summary["host"],
		"nodeId":           summary["nodeId"],
		"agentId":          summary["agentId"],
		"allowedDecisions": summary["allowedDecisions"],
	}
	return map[string]any{
		"id":           rec.ID,
		"urlPath":      "/approvals/" + rec.ID,
		"createdAtMs":  rec.Requested,
		"expiresAtMs":  rec.ExpiresAt,
		"presentation": presentation,
		"status":       "pending",
	}
}

func applyApprovalGet(reg *execApprovalsRegistry, req methods.ExecApprovalGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec, err := reg.GetPending(req.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"approval": approvalPendingSnapshot(rec)}, nil
}

func applyApprovalResolve(reg *execApprovalsRegistry, req methods.ApprovalResolveRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	decision := "deny"
	reason := "user"
	if req.Decision == "allow-once" || req.Decision == "allow-always" {
		decision = "approve"
	}
	if req.Decision == "allow-always" {
		reason = "always allow selected in web UI"
	}
	rec, err := reg.Resolve(methods.ExecApprovalResolveRequest{ID: req.ID, Decision: decision, Reason: reason})
	if err != nil {
		return nil, err
	}
	approval := approvalPendingSnapshot(rec)
	approval["status"] = "denied"
	if req.Decision != "deny" {
		approval["status"] = "allowed"
	}
	approval["decision"] = req.Decision
	approval["reason"] = "user"
	approval["resolvedAtMs"] = rec.ResolvedAt
	return map[string]any{"applied": true, "approval": approval}, nil
}
