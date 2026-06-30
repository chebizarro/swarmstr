package main

import (
	"encoding/json"
	"strings"
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

func execApprovalRememberSignature(reg *execApprovalsRegistry, signature string) {
	if reg == nil || signature == "" {
		return
	}
	approvals := reg.GetGlobal()
	if execApprovalSignatureAllowed(approvals, signature) {
		return
	}
	next := []any{}
	if raw, ok := approvals["allow_always_signatures"].([]any); ok {
		next = append(next, raw...)
	}
	next = append(next, signature)
	approvals["allow_always_signatures"] = next
	reg.SetGlobal(approvals)
}
