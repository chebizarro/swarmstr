package permissions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"metiq/internal/security/commandanalysis"
)

// DiagnosticSeverity classifies an exec-approval policy finding.
type DiagnosticSeverity string

const (
	DiagnosticError   DiagnosticSeverity = "error"
	DiagnosticWarning DiagnosticSeverity = "warning"
)

// ExecApprovalFinding is a stable, machine-readable policy diagnostic.
type ExecApprovalFinding struct {
	Severity DiagnosticSeverity `json:"severity"`
	Code     string             `json:"code"`
	Field    string             `json:"field,omitempty"`
	Message  string             `json:"message"`
}

// ExecApprovalReport is returned by DoctorExecApprovalPolicy.
type ExecApprovalReport struct {
	Findings []ExecApprovalFinding `json:"findings"`
}

// Valid reports whether the policy has no error-severity findings.
func (r ExecApprovalReport) Valid() bool {
	for _, finding := range r.Findings {
		if finding.Severity == DiagnosticError {
			return false
		}
	}
	return true
}

// DoctorExecApprovalPolicy diagnoses the live exec.approvals policy map. The
// daemon currently enforces allow_always_signatures; legacy mode/tools/timeout
// fields are accepted by compatibility APIs but do not participate in runtime
// authorization, so they are reported as unreachable instead of silently
// implying enforcement.
func DoctorExecApprovalPolicy(policy map[string]any) ExecApprovalReport {
	var findings []ExecApprovalFinding
	add := func(severity DiagnosticSeverity, code, field, message string) {
		findings = append(findings, ExecApprovalFinding{Severity: severity, Code: code, Field: field, Message: message})
	}

	known := map[string]bool{
		"allow_always_signatures": true,
		"mode":                    true,
		"tools":                   true,
		"timeout_ms":              true,
	}
	keys := make([]string, 0, len(policy))
	for key := range policy {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !known[key] {
			add(DiagnosticWarning, "unknown-policy-field", key, "field is not recognized by the exec-approval runtime")
		}
	}

	mode := ""
	if raw, exists := policy["mode"]; exists {
		value, ok := raw.(string)
		mode = strings.ToLower(strings.TrimSpace(value))
		if !ok || (mode != "ask" && mode != "allow" && mode != "deny") {
			add(DiagnosticError, "invalid-mode", "mode", "mode must be ask, allow, or deny")
		} else {
			add(DiagnosticWarning, "unreachable-policy-field", "mode", "mode is compatibility metadata and is not consulted by runtime authorization")
		}
	}
	if raw, exists := policy["tools"]; exists {
		tools, ok := diagnosticStringSlice(raw)
		if !ok {
			add(DiagnosticError, "invalid-tools", "tools", "tools must be an array of non-empty strings")
		} else {
			seen := map[string]bool{}
			for i, tool := range tools {
				field := fmt.Sprintf("tools[%d]", i)
				if tool == "*" || strings.ContainsAny(tool, "?[") {
					add(DiagnosticWarning, "unsafe-tool-pattern", field, "broad tool patterns can unintentionally cover privileged commands")
				}
				if seen[tool] {
					add(DiagnosticWarning, "duplicate-tool", field, "duplicate tool entry is unreachable")
				}
				seen[tool] = true
			}
			add(DiagnosticWarning, "unreachable-policy-field", "tools", "tools is compatibility metadata; configure permissions rules or config extra.approvals.tools for enforcement")
		}
	}
	if raw, exists := policy["timeout_ms"]; exists {
		if n, ok := diagnosticPositiveInt(raw); !ok || n <= 0 {
			add(DiagnosticError, "invalid-timeout", "timeout_ms", "timeout_ms must be a positive integer")
		} else {
			add(DiagnosticWarning, "unreachable-policy-field", "timeout_ms", "timeout_ms is compatibility metadata and does not set request timeouts")
		}
	}

	signatures, ok := diagnosticStringSlice(policy["allow_always_signatures"])
	if _, exists := policy["allow_always_signatures"]; exists && !ok {
		add(DiagnosticError, "invalid-signatures", "allow_always_signatures", "allow_always_signatures must be an array of non-empty JSON signatures")
	}
	seenSignatures := map[string]bool{}
	for i, signature := range signatures {
		field := fmt.Sprintf("allow_always_signatures[%d]", i)
		if seenSignatures[signature] {
			add(DiagnosticWarning, "duplicate-signature", field, "duplicate allow-always signature is unreachable")
			continue
		}
		seenSignatures[signature] = true
		var parts []string
		if err := json.Unmarshal([]byte(signature), &parts); err != nil || len(parts) < 2 || parts[0] != "exec" {
			add(DiagnosticError, "invalid-signature", field, "signature must be a JSON array beginning with exec and containing an argv")
			continue
		}
		analysis := commandanalysis.Analyze("", parts[1:])
		if !commandanalysis.IsAllowAlwaysSafe(analysis) || analysis.Signature != signature {
			add(DiagnosticError, "unsafe-signature", field, "signature is not canonical for a safe-bin command and will never bypass approval")
		}
	}
	if len(signatures) > 0 && mode == "deny" {
		add(DiagnosticError, "conflicting-policy", "allow_always_signatures", "deny mode conflicts with configured allow-always signatures")
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity < findings[j].Severity
		}
		if findings[i].Field != findings[j].Field {
			return findings[i].Field < findings[j].Field
		}
		return findings[i].Code < findings[j].Code
	})
	return ExecApprovalReport{Findings: findings}
}

func diagnosticStringSlice(raw any) ([]string, bool) {
	if raw == nil {
		return nil, true
	}
	var source []any
	switch values := raw.(type) {
	case []string:
		out := make([]string, len(values))
		for i, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, false
			}
			out[i] = value
		}
		return out, true
	case []any:
		source = values
	default:
		return nil, false
	}
	out := make([]string, 0, len(source))
	for _, item := range source {
		value, ok := item.(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func diagnosticPositiveInt(raw any) (int64, bool) {
	switch value := raw.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if value != float64(int64(value)) {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}
