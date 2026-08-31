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

// DoctorExecApprovalPolicy diagnoses the live, authoritative exec-approval
// policy. The same parser is used by runtime evaluation, so accepted fields are
// never merely compatibility metadata.
func DoctorExecApprovalPolicy(policy map[string]any) ExecApprovalReport {
	var findings []ExecApprovalFinding
	add := func(severity DiagnosticSeverity, code, field, message string) {
		findings = append(findings, ExecApprovalFinding{Severity: severity, Code: code, Field: field, Message: message})
	}

	known := map[string]bool{
		"version":                 true,
		"socket":                  true,
		"defaults":                true,
		"agents":                  true,
		"allow_always_signatures": true,
		"mode":                    true,
		"security":                true,
		"ask":                     true,
		"askFallback":             true,
		"ask_fallback":            true,
		"allowlist":               true,
		"autoAllowSkills":         true,
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
		if !ok || (mode != "deny" && mode != "allowlist" && mode != "ask" && mode != "auto" && mode != "full" && mode != "allow") {
			add(DiagnosticError, "invalid-mode", "mode", "mode must be deny, allowlist, ask, auto, or full")
		} else if mode == "allow" {
			add(DiagnosticWarning, "legacy-mode", "mode", "legacy allow mode is accepted as full; prefer the explicit full value")
		} else if mode == "full" {
			add(DiagnosticWarning, "open-policy", "mode", "full mode permits matching host execution without approval")
		}
	}
	if raw, exists := policy["tools"]; exists {
		tools, ok := diagnosticStringSlice(raw)
		if !ok {
			add(DiagnosticError, "invalid-tools", "tools", "tools must be a string or array of non-empty strings")
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
		}
	}
	if raw, exists := policy["timeout_ms"]; exists {
		if n, ok := diagnosticPositiveInt(raw); !ok || n <= 0 {
			add(DiagnosticError, "invalid-timeout", "timeout_ms", "timeout_ms must be a positive integer")
		}
	}

	if _, err := parseExecPolicyLayer(policy, "doctor", "doctor", 5*60*1000); err != nil {
		add(DiagnosticError, "invalid-effective-policy", "", err.Error())
	}
	if raw, exists := policy["askFallback"]; exists {
		if value, ok := raw.(string); ok && strings.EqualFold(strings.TrimSpace(value), "full") {
			add(DiagnosticWarning, "open-fallback", "askFallback", "full fallback permits execution when an approval prompt is unavailable or times out")
		}
	}
	if raw, exists := policy["ask_fallback"]; exists {
		if value, ok := raw.(string); ok && strings.EqualFold(strings.TrimSpace(value), "full") {
			add(DiagnosticWarning, "open-fallback", "ask_fallback", "full fallback permits execution when an approval prompt is unavailable or times out")
		}
	}
	if raw, exists := policy["allowlist"]; exists {
		entries, err := parseExecAllowlist(raw)
		if err != nil {
			add(DiagnosticError, "invalid-allowlist", "allowlist", err.Error())
		} else {
			seen := map[string]bool{}
			for i, entry := range entries {
				encoded, _ := json.Marshal(entry)
				key := string(encoded)
				if seen[key] {
					add(DiagnosticWarning, "duplicate-allowlist-entry", fmt.Sprintf("allowlist[%d]", i), "duplicate allowlist entry is unreachable")
				}
				seen[key] = true
			}
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
	case string:
		for _, value := range strings.Split(values, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			source = append(source, value)
		}
		if len(source) == 0 {
			return nil, false
		}
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
