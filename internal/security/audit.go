// Package security provides security posture auditing for metiq deployments.
//
// The audit system runs a series of checks against the bootstrap config and
// live config, categorising findings by severity (info, warn, critical).
// Findings include a checkId, human-readable message, and optional remediation hint.
//
// Usage:
//
//	report := security.Audit(security.AuditOptions{
//	    BootstrapPath: "~/.metiq/bootstrap.json",
//	})
//	for _, f := range report.Findings {
//	    fmt.Printf("[%s] %s: %s\n", f.Severity, f.CheckID, f.Message)
//	}
package security

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"metiq/internal/store/state"
)

// Severity levels for audit findings.
const (
	SeverityInfo     = "info"
	SeverityWarn     = "warn"
	SeverityCritical = "critical"
)

// Finding is a single security audit result.
type Finding struct {
	// CheckID is a stable machine-readable identifier (e.g. "admin-no-token").
	CheckID string `json:"check_id"`
	// Severity is one of "info", "warn", or "critical".
	Severity string `json:"severity"`
	// Message is a human-readable description of the finding.
	Message string `json:"message"`
	// Remediation is a suggested fix (may be empty for info-level findings).
	Remediation string `json:"remediation,omitempty"`
}

// AuditReport is the result of running all security checks.
type AuditReport struct {
	Findings []Finding `json:"findings"`
	// Counts by severity.
	Critical int `json:"critical"`
	Warn     int `json:"warn"`
	Info     int `json:"info"`
}

// AuditOptions controls what the auditor checks.
type AuditOptions struct {
	// BootstrapPath is the path to the bootstrap config JSON.
	// If empty, the default path (~/.metiq/bootstrap.json) is used.
	BootstrapPath string
	// ConfigDoc is the live config (optional; used for channel and plugin checks).
	ConfigDoc *state.ConfigDoc
}

// Audit runs all security checks and returns a report.
func Audit(opts AuditOptions) AuditReport {
	var findings []Finding

	// Load bootstrap config (lenient, no validation required).
	bs := loadBootstrapRaw(opts.BootstrapPath)

	findings = append(findings, checkAdminToken(bs)...)
	findings = append(findings, checkAdminBind(bs)...)
	findings = append(findings, checkPrivateKeyInConfig(bs)...)
	findings = append(findings, checkBootstrapFilePerms(opts.BootstrapPath)...)
	findings = append(findings, checkGatewayWSToken(bs)...)
	findings = append(findings, checkPrivateKeyStrength(bs)...)
	findings = append(findings, checkGatewayWSTrustedProxyAuth(bs)...)
	findings = append(findings, checkGatewayWSInsecureControlUI(bs)...)

	findings = append(findings, checkStateDocEncryption(bs, opts.ConfigDoc)...)
	findings = append(findings, checkPublishGuardPolicy(bs, opts.ConfigDoc)...)

	if opts.ConfigDoc != nil {
		findings = append(findings, checkControlAuthDisabled(bs, *opts.ConfigDoc)...)
		findings = append(findings, checkControlLegacyTokenFallback(bs, *opts.ConfigDoc)...)
		findings = append(findings, checkOpenDMPolicy(*opts.ConfigDoc)...)
		findings = append(findings, checkChannelSecrets(*opts.ConfigDoc)...)
	}

	report := AuditReport{Findings: findings}
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			report.Critical++
		case SeverityWarn:
			report.Warn++
		default:
			report.Info++
		}
	}
	return report
}

// ─── Individual checks ────────────────────────────────────────────────────────

func checkAdminToken(bs map[string]any) []Finding {
	addr, _ := bs["admin_listen_addr"].(string)
	if strings.TrimSpace(addr) == "" {
		// Admin API not exposed — nothing to check.
		return nil
	}
	token, _ := bs["admin_token"].(string)
	if strings.TrimSpace(token) == "" {
		return []Finding{{
			CheckID:     "admin-no-token",
			Severity:    SeverityWarn,
			Message:     fmt.Sprintf("admin API exposed at %s without a bearer token", addr),
			Remediation: "Set admin_token in bootstrap config to require Authorization header",
		}}
	}
	return nil
}

func checkAdminBind(bs map[string]any) []Finding {
	addr, _ := bs["admin_listen_addr"].(string)
	if strings.TrimSpace(addr) == "" {
		return nil
	}
	host := addr
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		host = addr[:idx]
	}
	if host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return []Finding{{
			CheckID:     "admin-public-bind",
			Severity:    SeverityCritical,
			Message:     fmt.Sprintf("admin API bound to non-loopback address %s", addr),
			Remediation: "Change admin_listen_addr to 127.0.0.1:<port>. Use a reverse proxy with auth for remote access.",
		}}
	}
	return nil
}

func checkPrivateKeyInConfig(bs map[string]any) []Finding {
	pk, _ := bs["private_key"].(string)
	if strings.TrimSpace(pk) == "" {
		return nil
	}
	return []Finding{{
		CheckID:     "private-key-in-config",
		Severity:    SeverityWarn,
		Message:     "Nostr private key is stored in plain text in bootstrap.json",
		Remediation: "Consider using signer_url to delegate signing to a NIP-46 signer, or ensure bootstrap.json has chmod 600",
	}}
}

func checkBootstrapFilePerms(path string) []Finding {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		path = home + "/.metiq/bootstrap.json"
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil // File doesn't exist yet — not a finding.
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return []Finding{{
			CheckID:     "bootstrap-file-perms",
			Severity:    SeverityWarn,
			Message:     fmt.Sprintf("bootstrap.json is group/world readable (mode %04o)", mode),
			Remediation: fmt.Sprintf("chmod 600 %s", path),
		}}
	}
	return nil
}

func checkGatewayWSToken(bs map[string]any) []Finding {
	wsAddr, _ := bs["gateway_ws_listen_addr"].(string)
	if strings.TrimSpace(wsAddr) == "" {
		return nil
	}
	token, _ := bs["gateway_ws_token"].(string)
	if strings.TrimSpace(token) == "" {
		return []Finding{{
			CheckID:     "gateway-ws-no-token",
			Severity:    SeverityWarn,
			Message:     fmt.Sprintf("gateway WebSocket exposed at %s without a token", wsAddr),
			Remediation: "Set gateway_ws_token in bootstrap config",
		}}
	}
	return nil
}

func checkPrivateKeyStrength(bs map[string]any) []Finding {
	pk, _ := bs["private_key"].(string)
	pk = strings.TrimSpace(pk)
	if pk == "" {
		return nil
	}
	// Warn if key looks suspiciously short or is a test key.
	if len(pk) < 32 {
		return []Finding{{
			CheckID:     "private-key-weak",
			Severity:    SeverityCritical,
			Message:     "private_key appears to be too short to be a valid Nostr key",
			Remediation: "Generate a new key with: metiqd --gen-key",
		}}
	}
	return nil
}

func checkControlAuthDisabled(bs map[string]any, cfg state.ConfigDoc) []Finding {
	if cfg.Control.RequireAuth {
		return nil
	}
	severity := SeverityWarn
	if hasNonLoopbackControlIngress(bs) {
		severity = SeverityCritical
	}
	return []Finding{{
		CheckID:     "control-auth-disabled",
		Severity:    severity,
		Message:     "Control policy authentication is disabled (control.require_auth=false)",
		Remediation: "Set control.require_auth=true and explicitly grant admin access via control.admins",
	}}
}

func checkControlLegacyTokenFallback(bs map[string]any, cfg state.ConfigDoc) []Finding {
	if !cfg.Control.LegacyTokenFallback {
		return nil
	}
	severity := SeverityWarn
	if hasNonLoopbackControlIngress(bs) {
		severity = SeverityCritical
	}
	return []Finding{{
		CheckID:     "control-legacy-token-fallback",
		Severity:    severity,
		Message:     "Control policy legacy token fallback is enabled (control.legacy_token_fallback=true)",
		Remediation: "Disable legacy token fallback and require pubkey-scoped control admins",
	}}
}

func checkGatewayWSTrustedProxyAuth(bs map[string]any) []Finding {
	if !hasTrustedProxies(bs["gateway_ws_trusted_proxies"]) {
		return nil
	}
	return []Finding{{
		CheckID:  "gateway-ws-trusted-proxy-auth",
		Severity: SeverityWarn,
		Message:  "Gateway WS trusted proxy authentication is enabled",
		Remediation: "Restrict gateway_ws_trusted_proxies to exact proxy CIDRs/IPs, ensure the proxy strips and re-adds X-Metiq-Trusted-Auth/X-Metiq-Proxy-User, " +
			"and avoid exposing WS directly on the same network path.",
	}}
}

func checkGatewayWSInsecureControlUI(bs map[string]any) []Finding {
	allowInsecure, _ := bs["gateway_ws_allow_insecure_control_ui"].(bool)
	if !allowInsecure {
		return nil
	}
	severity := SeverityWarn
	if isNonLoopbackBind(stringValue(bs["gateway_ws_listen_addr"])) {
		severity = SeverityCritical
	}
	return []Finding{{
		CheckID:     "gateway-ws-insecure-control-ui",
		Severity:    severity,
		Message:     "Gateway WS insecure control-ui bypass is enabled (gateway_ws_allow_insecure_control_ui=true)",
		Remediation: "Disable gateway_ws_allow_insecure_control_ui, or constrain gateway_ws_listen_addr to loopback-only",
	}}
}

func checkOpenDMPolicy(cfg state.ConfigDoc) []Finding {
	if strings.EqualFold(cfg.DM.Policy, "open") {
		return []Finding{{
			CheckID:     "dm-policy-open",
			Severity:    SeverityInfo,
			Message:     "DM policy is 'open': any Nostr user can send messages to this agent",
			Remediation: "Consider using 'allowlist' policy to restrict access to known pubkeys",
		}}
	}
	return nil
}

func checkChannelSecrets(cfg state.ConfigDoc) []Finding {
	var findings []Finding
	for name, ch := range cfg.NostrChannels {
		// Check if any channel config field contains what looks like an API token
		// stored in an insecure way (e.g., agent_id used as a token placeholder).
		if ch.Config != nil {
			if token, ok := ch.Config["token"].(string); ok && len(token) > 0 {
				findings = append(findings, Finding{
					CheckID:     "channel-token-in-config",
					Severity:    SeverityInfo,
					Message:     fmt.Sprintf("channel %q has a token stored in config (ensure config is chmod 600)", name),
					Remediation: "Consider using the secrets store: set token via metiq secrets set and reference with $secret:key_name",
				})
			}
		}
	}
	return findings
}

func checkStateDocEncryption(bs map[string]any, cfg *state.ConfigDoc) []Finding {
	if cfg != nil {
		if cfg.StorageEncryptEnabled() {
			return nil
		}
		return []Finding{{
			CheckID:  "nip44-disabled",
			Severity: SeverityWarn,
			Message:  "State document encryption is disabled: config, transcripts, and memory docs are stored on relays in plaintext",
			Remediation: "Set storage.encrypt: true in runtime config. " +
				"Legacy plaintext docs remain readable for migration and will be re-encrypted on the next write.",
		}}
	}
	enabled, _ := bs["enable_nip44"].(bool)
	message := "Runtime storage encryption could not be verified without a live config document"
	if !enabled {
		message = "Runtime storage encryption could not be verified and bootstrap NIP-44 transport encryption is disabled"
	}
	return []Finding{{
		CheckID:  "nip44-disabled",
		Severity: SeverityWarn,
		Message:  message,
		Remediation: "Verify storage.encrypt: true in runtime config. " +
			"bootstrap enable_nip44 protects DM/control transport, not relay-persisted state docs.",
	}}
}

func checkPublishGuardPolicy(bs map[string]any, cfg *state.ConfigDoc) []Finding {
	// Check for publish_guard.policy in either live config or bootstrap config.
	// Live config takes precedence when both are present.
	var policy string
	if cfg != nil && cfg.Extra != nil {
		if pgExtra, ok := cfg.Extra["publish_guard"].(map[string]any); ok {
			policy, _ = pgExtra["policy"].(string)
		}
	}
	if strings.TrimSpace(policy) == "" && bs != nil {
		if extra, ok := bs["extra"].(map[string]any); ok {
			if pgExtra, ok := extra["publish_guard"].(map[string]any); ok {
				policy, _ = pgExtra["policy"].(string)
			}
		}
	}

	lower := strings.ToLower(strings.TrimSpace(policy))
	if lower == "off" || lower == "disabled" || lower == "none" {
		return []Finding{{
			CheckID:     "publish-guard-disabled",
			Severity:    SeverityCritical,
			Message:     "Outbound publish content guard is disabled: agent tools can publish secrets, API keys, and credentials to relays without detection",
			Remediation: "Remove extra.publish_guard.policy or set it to \"block\" (recommended) or \"warn\"",
		}}
	}
	if lower == "warn" {
		return []Finding{{
			CheckID:     "publish-guard-warn-only",
			Severity:    SeverityWarn,
			Message:     "Outbound publish content guard is in warn-only mode: secrets detected in outbound events are logged but NOT blocked",
			Remediation: "Set extra.publish_guard.policy to \"block\" for production deployments",
		}}
	}
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func hasNonLoopbackControlIngress(bs map[string]any) bool {
	if isNonLoopbackBind(stringValue(bs["admin_listen_addr"])) {
		return true
	}
	return isNonLoopbackBind(stringValue(bs["gateway_ws_listen_addr"]))
}

func isNonLoopbackBind(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	} else if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		host = addr[:idx]
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" || host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	return true
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func hasTrustedProxies(v any) bool {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []string:
		for _, item := range value {
			if strings.TrimSpace(item) != "" {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if strings.TrimSpace(stringValue(item)) != "" {
				return true
			}
		}
	}
	return false
}

func loadBootstrapRaw(path string) map[string]any {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		path = home + "/.metiq/bootstrap.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}
