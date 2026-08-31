package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"metiq/internal/permissions"
)

func approvalCommandDisplay(toolName string, args map[string]any) (string, []string) {
	for _, key := range []string{"command", "cmd", "script"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
	if v, ok := args["cmd"].([]any); ok {
		argv := stringSliceFromAny(v)
		if len(argv) > 0 {
			return strings.Join(argv, " "), argv
		}
	}
	for _, key := range []string{"argv", "command_argv", "args"} {
		if v, ok := args[key].([]any); ok {
			argv := stringSliceFromAny(v)
			if len(argv) > 0 {
				return strings.Join(argv, " "), argv
			}
		}
	}
	if len(args) > 0 {
		if b, err := json.Marshal(args); err == nil {
			return toolName + " " + string(b), []string{toolName}
		}
	}
	return toolName, []string{toolName}
}

func stringSliceFromAny(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		s, ok := value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func execApprovalSignatureAllowed(approvals map[string]any, signature string) bool {
	if signature == "" || approvals == nil {
		return false
	}
	raw, ok := approvals["allow_always_signatures"]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == signature {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == signature {
				return true
			}
		}
	}
	return false
}

func execApprovalRememberSignature(reg *execApprovalsRegistry, signature string) error {
	if reg == nil || signature == "" {
		return fmt.Errorf("exec approval registry and signature are required")
	}
	approvals := reg.GetGlobal()
	if execApprovalSignatureAllowed(approvals, signature) {
		return nil
	}
	next := []any{}
	switch raw := approvals["allow_always_signatures"].(type) {
	case []any:
		next = append(next, raw...)
	case []string:
		for _, item := range raw {
			next = append(next, item)
		}
	}
	next = append(next, signature)
	approvals["allow_always_signatures"] = next
	if _, err := reg.SetGlobalChecked(approvals); err != nil {
		return fmt.Errorf("persist allow-always signature: %w", err)
	}
	return nil
}

func execPolicyFindingSummary(report permissions.ExecApprovalReport) string {
	parts := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		if finding.Severity != permissions.DiagnosticError {
			continue
		}
		if finding.Field != "" {
			parts = append(parts, finding.Field+": "+finding.Message)
		} else {
			parts = append(parts, finding.Message)
		}
	}
	if len(parts) == 0 {
		return "policy validation failed"
	}
	return strings.Join(parts, "; ")
}
