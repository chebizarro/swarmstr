package policy

import (
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

type ConformanceStatus string

const (
	ConformancePass ConformanceStatus = "pass"
	ConformanceWarn ConformanceStatus = "warn"
	ConformanceFail ConformanceStatus = "fail"
)

type ConformanceFinding struct {
	ID          string            `json:"id"`
	Status      ConformanceStatus `json:"status"`
	Message     string            `json:"message"`
	Remediation string            `json:"remediation,omitempty"`
}

type ConformanceReport struct {
	Findings []ConformanceFinding `json:"findings"`
}

func (r ConformanceReport) Summary() (pass, warn, fail int) {
	for _, f := range r.Findings {
		switch f.Status {
		case ConformanceFail:
			fail++
		case ConformanceWarn:
			warn++
		default:
			pass++
		}
	}
	return pass, warn, fail
}

// CheckPolicyConformance runs read-only security posture checks over the loaded
// config. It intentionally does not read or mutate runtime state.
func CheckPolicyConformance(cfg state.ConfigDoc) ConformanceReport {
	var findings []ConformanceFinding
	findings = append(findings, checkApprovalRules(cfg.Permissions))
	findings = append(findings, checkDefaultAllow(cfg.Permissions))
	findings = append(findings, checkSandboxEgress(cfg))
	findings = append(findings, checkSandboxDriver(cfg))
	findings = append(findings, checkPluginPermissions(cfg))
	return ConformanceReport{Findings: findings}
}

func checkApprovalRules(perms state.PermissionsConfig) ConformanceFinding {
	for _, rule := range perms.Rules {
		behavior := strings.ToLower(strings.TrimSpace(rule.Behavior))
		if behavior != "ask" && behavior != "deny" {
			continue
		}
		tool := strings.ToLower(strings.TrimSpace(rule.Tool))
		cat := strings.ToLower(strings.TrimSpace(rule.Category))
		if tool == "*" || strings.Contains(tool, "bash") || strings.Contains(tool, "exec") || strings.Contains(tool, "shell") || strings.Contains(tool, "sandbox") || cat == "exec" || cat == "filesystem" || cat == "network" {
			return ConformanceFinding{ID: "approval-rules", Status: ConformancePass, Message: "approval/deny rule exists for high-risk tools"}
		}
	}
	return ConformanceFinding{ID: "approval-rules", Status: ConformanceFail, Message: "missing or weak approval rules for high-risk tools", Remediation: "Add ask/deny rules for exec, filesystem, network, sandbox, plugin, and MCP tools."}
}

func checkDefaultAllow(perms state.PermissionsConfig) ConformanceFinding {
	behavior := strings.ToLower(strings.TrimSpace(perms.DefaultBehavior))
	if behavior == "" || behavior == "allow" || behavior == "autonomous" || behavior == "permissive" {
		return ConformanceFinding{ID: "default-tool-policy", Status: ConformanceWarn, Message: "tool policy defaults to allow/profile behavior", Remediation: "Set permissions.default_behavior to ask or deny for managed/high-risk environments."}
	}
	return ConformanceFinding{ID: "default-tool-policy", Status: ConformancePass, Message: fmt.Sprintf("tool policy default is %q", behavior)}
}

func checkSandboxEgress(cfg state.ConfigDoc) ConformanceFinding {
	sandbox := extraMap(cfg.Extra, "sandbox")
	if len(sandbox) == 0 {
		sandbox = extraMap(cfg.Extra, "runtime")
	}
	if boolValue(sandbox, "egress_enforced") || boolValue(sandbox, "enforce_egress") || boolValue(sandbox, "network_disabled") || strings.EqualFold(stringValue(sandbox, "network"), "none") {
		return ConformanceFinding{ID: "sandbox-egress", Status: ConformancePass, Message: "sandbox egress controls appear configured"}
	}
	return ConformanceFinding{ID: "sandbox-egress", Status: ConformanceWarn, Message: "sandbox egress enforcement is not configured", Remediation: "Enable sandbox egress enforcement or disable sandbox network access."}
}

func checkSandboxDriver(cfg state.ConfigDoc) ConformanceFinding {
	sandbox := extraMap(cfg.Extra, "sandbox")
	driver := strings.ToLower(strings.TrimSpace(firstNonEmptyString(stringValue(sandbox, "driver"), stringValue(sandbox, "backend"))))
	if driver == "" {
		return ConformanceFinding{ID: "sandbox-driver", Status: ConformanceWarn, Message: "sandbox driver is not explicitly configured", Remediation: "Configure a real sandbox driver such as nsjail, firecracker, docker, or seatbelt."}
	}
	if driver == "nop" || driver == "none" || driver == "disabled" {
		return ConformanceFinding{ID: "sandbox-driver", Status: ConformanceFail, Message: "unsafe sandbox driver configured: " + driver, Remediation: "Replace nop/none with an enforcing sandbox driver."}
	}
	return ConformanceFinding{ID: "sandbox-driver", Status: ConformancePass, Message: "sandbox driver is " + driver}
}

func checkPluginPermissions(cfg state.ConfigDoc) ConformanceFinding {
	plugins := extraMap(cfg.Extra, "extensions")
	if len(plugins) == 0 {
		plugins = extraMap(cfg.Extra, "plugins")
	}
	if len(plugins) == 0 {
		return ConformanceFinding{ID: "plugin-permissions", Status: ConformancePass, Message: "no plugin permissions configured"}
	}
	managed := extraMap(cfg.Extra, "managed_settings")
	if boolValue(managed, "allow_managed_permission_rules_only") || boolValue(managed, "require_tool_approval") {
		return ConformanceFinding{ID: "plugin-permissions", Status: ConformancePass, Message: "plugin permissions are covered by managed settings"}
	}
	for _, rule := range cfg.Permissions.Rules {
		if strings.EqualFold(rule.Origin, "plugin") || strings.Contains(strings.ToLower(rule.Tool), "plugin") {
			return ConformanceFinding{ID: "plugin-permissions", Status: ConformancePass, Message: "plugin-origin permission rules are configured"}
		}
	}
	return ConformanceFinding{ID: "plugin-permissions", Status: ConformanceWarn, Message: "plugin permissions are not managed by policy", Remediation: "Add plugin-origin permission rules or enable managed_settings.require_tool_approval."}
}

func extraMap(extra map[string]any, key string) map[string]any {
	if extra == nil {
		return nil
	}
	if m, ok := extra[key].(map[string]any); ok {
		return m
	}
	return nil
}

func boolValue(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "1" || s == "true" || s == "yes" || s == "on"
	default:
		return false
	}
}

func stringValue(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
