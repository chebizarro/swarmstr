package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/permissions"
	"metiq/internal/store/state"
)

func auditDetailCount(details map[string]any, key string) int {
	switch value := details[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func boundedAuditSummary(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

func boundedAuditRef(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func auditDecisionDisplay(event permissions.AuditEvent) (map[string]any, string) {
	outcome := "unknown"
	coverage := "attribution-only"
	switch event.Behavior {
	case permissions.BehaviorAllow:
		outcome, coverage = "allowed", "enforced"
	case permissions.BehaviorDeny:
		outcome, coverage = "denied", "enforced"
	}
	reasonCode := boundedAuditRef(event.Reason, "metiq.permission."+string(event.Behavior))
	fields := make([]string, 0, 4)
	if event.AgentID != "" {
		fields = append(fields, "agent_id")
	}
	if event.SessionID != "" {
		fields = append(fields, "session_id")
	}
	if event.RunID != "" {
		fields = append(fields, "run_id")
	}
	if event.ExecutionID != "" {
		fields = append(fields, "execution_id")
	}
	action := map[string]any{"family": "tool", "operation": boundedAuditRef(event.ToolName, "unknown-tool")}
	if summary := boundedAuditSummary(event.Reason); summary != "" {
		action["summary"] = summary
	}
	return map[string]any{
		"schemaVersion": 1,
		"selectorId":    event.ID,
		"occurredAt":    event.Timestamp.UnixMilli(),
		"action":        action,
		"decision":      map[string]any{"outcome": outcome, "reasonCode": reasonCode},
		"enforcement": map[string]any{
			"coverageState":     coverage,
			"policyCount":       min(auditDetailCount(event.Details, "matched_rules"), 16),
			"grantCount":        0,
			"contextFieldsUsed": fields,
		},
		// The legacy permission auditor predates signed execution receipts. The
		// display is safe and correlated, but its provenance is explicitly unverified.
		"provenance":      map[string]any{"state": "unverified"},
		"missingEvidence": []string{"execution.identity_context"},
		"remediation":     []any{},
	}, coverage
}

func unknownAuditIdentity(reason string) map[string]any {
	return map[string]any{
		"state": "unknown", "reasonCode": reason,
		"missingEvidence": []string{"execution.identity_context"},
		"remediation": []map[string]any{{
			"code": "capture-execution-identity", "text": "Enable a versioned execution identity producer for full principal and lineage inspection.",
		}},
	}
}

func (h controlRPCHandler) inspectAuditRun(req methods.AuditRunInspectRequest) (map[string]any, error) {
	runID, executionID := req.RunID, req.ExecutionID
	query := permissions.AuditQueryOptions{RunID: runID, ExecutionID: executionID}
	available := h.deps.permEngine != nil && h.deps.permEngine.AuditEnabled()
	var events []permissions.AuditEvent
	var err error
	if available {
		events, err = h.deps.permEngine.QueryAudit(query)
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].ID < events[j].ID
		}
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	if len(events) > 0 {
		if runID == "" {
			runID = events[0].RunID
		}
		if executionID == "" {
			executionID = events[0].ExecutionID
		}
	}

	offset := req.DecisionOffset()
	if offset > len(events) {
		offset = len(events)
	}
	end := offset + req.DecisionLimit
	if end > len(events) {
		end = len(events)
	}
	displays := make([]map[string]any, 0, end-offset)
	coverage := "unknown"
	for _, event := range events[offset:end] {
		display, state := auditDecisionDisplay(event)
		displays = append(displays, display)
		if coverage == "unknown" || (coverage == "enforced" && state != "enforced") {
			coverage = state
		}
	}
	missing := []string{"execution.identity_context"}
	if !available {
		missing = append(missing, "permission.audit")
	}
	status := "unknown"
	if len(events) > 0 {
		status = "known"
	}
	run := map[string]any{"status": status}
	if runID != "" {
		run["runId"] = runID
	}
	if executionID != "" {
		run["executionId"] = executionID
	}
	result := map[string]any{
		"schemaVersion":    1,
		"run":              run,
		"identity":         unknownAuditIdentity("metiq.execution-identity-context-not-recorded"),
		"decisionDisplays": displays,
		"coverage":         map[string]any{"state": coverage, "missingEvidence": missing},
	}
	if end < len(events) {
		result["nextDecisionCursor"] = fmt.Sprintf("%d", end)
	}
	return result, nil
}

func (h controlRPCHandler) handleAuditRunRPC(_ context.Context, in nostruntime.ControlRPCInbound, method string, _ state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	if method != methods.MethodAuditRunInspect {
		return nostruntime.ControlRPCResult{}, false, nil
	}
	req, err := methods.DecodeAuditRunInspectParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	result, err := h.inspectAuditRun(req)
	return nostruntime.ControlRPCResult{Result: result}, true, err
}
