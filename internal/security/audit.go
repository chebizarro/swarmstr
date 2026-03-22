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

	findings = append(findings, checkNIP44Disabled(bs)...)
	findings = append(findings, checkPublishGuardPolicy(bs, opts.ConfigDoc)...)

	if opts.ConfigDoc != nil {
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

func checkNIP44Disabled(bs map[string]any) []Finding {
	enabled, ok := bs["enable_nip44"].(bool)
	if ok && enabled {
		return nil // NIP-44 is on — good
	}
	return []Finding{{
		CheckID:  "nip44-disabled",
		Severity: SeverityWarn,
		Message:  "NIP-44 envelope encryption is disabled: config, sessions, transcripts, and memory docs are stored on relays in plaintext",
		Remediation: "Set enable_nip44: true in bootstrap config. " +
			"This encrypts all state documents with NIP-44 self-encryption before publishing to relays.",
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
