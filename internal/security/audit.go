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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"metiq/internal/netpolicy"
	"metiq/internal/nostr/runtime"
	"metiq/internal/policy"
	"metiq/internal/secrets"
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
	// SuppressedFindings are matched by AuditOptions.Suppressions and excluded
	// from severity counts while preserving audit evidence.
	SuppressedFindings []SuppressedFinding `json:"suppressed_findings,omitempty"`
	// AttestationHash is a stable SHA-256 digest over active findings and config
	// posture metadata. It intentionally excludes secret values.
	AttestationHash  string `json:"attestation_hash,omitempty"`
	AttestationDrift bool   `json:"attestation_drift,omitempty"`
	// Counts by severity.
	Critical int `json:"critical"`
	Warn     int `json:"warn"`
	Info     int `json:"info"`
}

// AuditSuppression suppresses accepted findings by check ID and optional title
// substring. Reason is required for accountability.
type AuditSuppression struct {
	CheckID    string `json:"check_id"`
	TitleMatch string `json:"title_match,omitempty"`
	Reason     string `json:"reason"`
}

type SuppressedFinding struct {
	Finding Finding `json:"finding"`
	Reason  string  `json:"reason"`
}

// AuditOptions controls what the auditor checks.
type AuditOptions struct {
	// BootstrapPath is the path to the bootstrap config JSON.
	// If empty, the default path (~/.metiq/bootstrap.json) is used.
	BootstrapPath string
	// ConfigDoc is the live config (optional; used for channel and plugin checks).
	ConfigDoc *state.ConfigDoc
	// SecretPaths are plaintext secret files to audit. If nil, default metiq
	// plaintext fallback locations are checked when present.
	SecretPaths []string
	// PluginPaths are installed plugin roots to verify and scan.
	PluginPaths []string
	// Suppressions move accepted findings out of active counts.
	Suppressions []AuditSuppression
	// ExpectedAttestation, when set, is compared to the generated attestation.
	ExpectedAttestation string
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
	findings = append(findings, checkSecretFileModes(opts.SecretPaths)...)
	findings = append(findings, checkGatewayWSToken(bs)...)
	findings = append(findings, checkPrivateKeyStrength(bs)...)
	findings = append(findings, checkGatewayWSTrustedProxyAuth(bs)...)
	findings = append(findings, checkGatewayWSInsecureControlUI(bs)...)
	findings = append(findings, checkSecretPlaintextContent(opts.SecretPaths)...)
	findings = append(findings, auditPluginIntegrity(opts.PluginPaths)...)
	findings = append(findings, auditPluginSafety(opts.PluginPaths)...)

	findings = append(findings, checkStateDocEncryption(bs, opts.ConfigDoc)...)
	findings = append(findings, checkPublishGuardPolicy(bs, opts.ConfigDoc)...)
	findings = append(findings, checkSandboxNopDriver(bs, opts.ConfigDoc)...)
	findings = append(findings, checkDockerSandboxHardening(bs, opts.ConfigDoc)...)

	if opts.ConfigDoc != nil {
		findings = append(findings, checkControlAuthDisabled(bs, *opts.ConfigDoc)...)
		findings = append(findings, checkControlLegacyTokenFallback(bs, *opts.ConfigDoc)...)
		findings = append(findings, checkControlAllowUnauthMethods(bs, *opts.ConfigDoc)...)
		findings = append(findings, checkControlMissingAdmins(bs, *opts.ConfigDoc)...)
		findings = append(findings, checkOpenDMPolicy(*opts.ConfigDoc)...)
		findings = append(findings, checkChannelSecrets(*opts.ConfigDoc)...)
		findings = append(findings, checkE2EChannelPolicy(*opts.ConfigDoc)...)
		findings = append(findings, checkSecretRefsAndScopes(*opts.ConfigDoc)...)
		findings = append(findings, checkToolPolicyConfig(*opts.ConfigDoc)...)
		findings = append(findings, checkManagedSettings(*opts.ConfigDoc)...)
		findings = append(findings, checkSandboxEgressPolicy(*opts.ConfigDoc)...)
	}

	active, suppressed := applySuppressions(findings, opts.Suppressions)
	report := AuditReport{Findings: active, SuppressedFindings: suppressed}
	report.recount()
	report.AttestationHash = attestationHash(bs, opts.ConfigDoc, report.Findings, report.SuppressedFindings)
	if strings.TrimSpace(opts.ExpectedAttestation) != "" && !strings.EqualFold(strings.TrimSpace(opts.ExpectedAttestation), report.AttestationHash) {
		report.AttestationDrift = true
		report.Findings = append(report.Findings, Finding{CheckID: "audit-attestation-drift", Severity: SeverityWarn, Message: "security audit attestation does not match expected posture hash", Remediation: "Review active findings and managed policy before accepting the new attestation."})
		report.recount()
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

func checkSecretFileModes(paths []string) []Finding {
	if paths == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		paths = []string{home + "/.metiq/.env", home + "/.metiq/mcp-auth.json"}
	}
	var findings []Finding
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		mode := info.Mode().Perm()
		if mode&0o077 != 0 {
			findings = append(findings, Finding{
				CheckID:     "secret-file-perms",
				Severity:    SeverityCritical,
				Message:     fmt.Sprintf("plaintext secret file %s is group/world readable (mode %04o)", path, mode),
				Remediation: fmt.Sprintf("chmod 600 %s and migrate secrets to the OS-backed secret store", path),
			})
			continue
		}
		findings = append(findings, Finding{
			CheckID:     "plaintext-secret-file",
			Severity:    SeverityWarn,
			Message:     fmt.Sprintf("plaintext secret file exists at %s", path),
			Remediation: "Migrate secrets to the OS-backed secret store and remove plaintext fallback files when no longer needed",
		})
	}
	return findings
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

func checkControlAllowUnauthMethods(bs map[string]any, cfg state.ConfigDoc) []Finding {
	var findings []Finding
	for i, method := range cfg.Control.AllowUnauthMethods {
		m := strings.ToLower(strings.TrimSpace(method))
		if m == "" || policy.IsUnauthAllowedControlMethod(m) {
			continue
		}
		severity := SeverityWarn
		if m == "*" || strings.HasSuffix(m, ".*") || policy.IsSensitiveControlMethod(m) || hasNonLoopbackControlIngress(bs) {
			severity = SeverityCritical
		}
		findings = append(findings, Finding{
			CheckID:     "control-unsafe-allow-unauth-method",
			Severity:    severity,
			Message:     fmt.Sprintf("Control allow_unauth_methods[%d]=%q is not an explicitly safe unauthenticated method", i, method),
			Remediation: "Restrict control.allow_unauth_methods to supportedmethods, health, status, or status.get; sensitive methods always require configured admins.",
		})
	}
	return findings
}

func checkControlMissingAdmins(bs map[string]any, cfg state.ConfigDoc) []Finding {
	if !cfg.Control.RequireAuth || len(usableControlAdminPubKeys(cfg.Control.Admins)) > 0 || cfg.Control.LegacyTokenFallback {
		return nil
	}
	severity := SeverityWarn
	if hasNonLoopbackControlIngress(bs) {
		severity = SeverityCritical
	}
	return []Finding{{
		CheckID:     "control-no-admins",
		Severity:    severity,
		Message:     "Control policy requires authentication but no control admins are configured",
		Remediation: "Add at least one pubkey to control.admins, or intentionally enable a documented local-only fallback during bootstrap.",
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

func checkE2EChannelPolicy(cfg state.ConfigDoc) []Finding {
	var findings []Finding
	for name, ch := range cfg.NostrChannels {
		if ch.Config == nil {
			continue
		}
		privKey := strings.TrimSpace(stringValue(ch.Config["e2e_private_key"]))
		peerPubKey := strings.TrimSpace(stringValue(ch.Config["e2e_peer_pubkey"]))
		required := boolValue(ch.Config["e2e.required"]) || boolValue(ch.Config["e2e_required"])
		configured := privKey != "" || peerPubKey != "" || required
		if !configured {
			continue
		}
		if !required {
			findings = append(findings, Finding{
				CheckID:     "channel-e2e-not-required",
				Severity:    SeverityWarn,
				Message:     fmt.Sprintf("channel %q has E2E configuration but e2e.required is not enabled", name),
				Remediation: "Set config.e2e.required=true so encrypted channels fail closed instead of accepting plaintext downgrade paths.",
			})
		}
		if privKey == "" || peerPubKey == "" {
			findings = append(findings, Finding{
				CheckID:     "channel-e2e-incomplete",
				Severity:    SeverityCritical,
				Message:     fmt.Sprintf("channel %q has incomplete E2E key configuration", name),
				Remediation: "Configure both e2e_private_key and e2e_peer_pubkey using secret-store references.",
			})
			continue
		}
		if !isSecretRef(privKey) {
			findings = append(findings, Finding{
				CheckID:     "channel-e2e-private-key-in-config",
				Severity:    SeverityCritical,
				Message:     fmt.Sprintf("channel %q stores e2e_private_key directly in config", name),
				Remediation: "Move the E2E private key to the OS-backed secret store and reference it with env:NAME or $NAME.",
			})
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
		if pgExtra, ok := asStringAnyMap(cfg.Extra["publish_guard"]); ok {
			policy, _ = pgExtra["policy"].(string)
		}
	}
	if strings.TrimSpace(policy) == "" && bs != nil {
		if extra, ok := asStringAnyMap(bs["extra"]); ok {
			if pgExtra, ok := asStringAnyMap(extra["publish_guard"]); ok {
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

func checkDockerSandboxHardening(bs map[string]any, cfg *state.ConfigDoc) []Finding {
	sandboxMap := sandboxConfigMap(bs, cfg)
	if sandboxMap == nil {
		return nil
	}
	driver := strings.ToLower(strings.TrimSpace(stringValue(sandboxMap["driver"])))
	if driver != "" && driver != "docker" {
		return nil
	}

	var findings []Finding
	if boolValue(sandboxMap["allow_network"]) || boolValue(sandboxMap["network_enabled"]) {
		findings = append(findings, Finding{
			CheckID:     "sandbox-docker-network-enabled",
			Severity:    SeverityWarn,
			Message:     "Docker sandbox network access is enabled",
			Remediation: "Remove extra.sandbox.allow_network or set it to false unless the sandbox workload explicitly requires network access.",
		})
	}
	if boolValue(sandboxMap["writable_rootfs"]) || boolValue(sandboxMap["read_only_rootfs"]) == false && hasKey(sandboxMap, "read_only_rootfs") {
		findings = append(findings, Finding{
			CheckID:     "sandbox-docker-writable-rootfs",
			Severity:    SeverityWarn,
			Message:     "Docker sandbox root filesystem is configured writable",
			Remediation: "Use the read-only root filesystem default and add explicit tmpfs/workspace mounts for writable paths.",
		})
	}
	if strings.TrimSpace(stringValue(sandboxMap["user"])) == "0" || strings.TrimSpace(stringValue(sandboxMap["user"])) == "root" || strings.HasPrefix(strings.TrimSpace(stringValue(sandboxMap["user"])), "0:") {
		findings = append(findings, Finding{
			CheckID:     "sandbox-docker-root-user",
			Severity:    SeverityWarn,
			Message:     "Docker sandbox is configured to run as root",
			Remediation: "Remove extra.sandbox.user to use the non-root default, or set it to a non-root uid:gid.",
		})
	}
	if pids, ok := numericValue(sandboxMap["pids_limit"]); ok && pids <= 0 {
		findings = append(findings, Finding{
			CheckID:     "sandbox-docker-unlimited-pids",
			Severity:    SeverityWarn,
			Message:     "Docker sandbox process limit is disabled",
			Remediation: "Remove extra.sandbox.pids_limit to use the default, or set a positive process limit.",
		})
	}
	if capDropConfiguredWeak(sandboxMap["cap_drop"]) {
		findings = append(findings, Finding{
			CheckID:     "sandbox-docker-capabilities",
			Severity:    SeverityWarn,
			Message:     "Docker sandbox does not drop all Linux capabilities",
			Remediation: "Remove extra.sandbox.cap_drop to use [\"ALL\"], or explicitly include ALL.",
		})
	}
	if securityOptMissingNoNewPrivileges(sandboxMap["security_opt"]) {
		findings = append(findings, Finding{
			CheckID:     "sandbox-docker-new-privileges",
			Severity:    SeverityWarn,
			Message:     "Docker sandbox does not enforce no-new-privileges",
			Remediation: "Remove extra.sandbox.security_opt to use the default, or include no-new-privileges.",
		})
	}
	return findings
}

func checkSandboxNopDriver(bs map[string]any, cfg *state.ConfigDoc) []Finding {
	driver := ""
	if cfg != nil && cfg.Extra != nil {
		driver = sandboxDriverFromExtra(cfg.Extra)
	}
	if strings.TrimSpace(driver) == "" {
		driver = sandboxDriverFromBootstrap(bs)
	}
	if !strings.EqualFold(strings.TrimSpace(driver), "nop") {
		return nil
	}

	severity := SeverityWarn
	message := "Sandbox driver is explicitly configured as \"nop\", which executes commands directly on the host without isolation"
	if isProductionDeployment(bs, cfg) {
		severity = SeverityCritical
		message = "Production deployment is configured to use sandbox driver \"nop\", which executes commands directly on the host with daemon privileges"
	}
	return []Finding{{
		CheckID:     "sandbox-nop-driver",
		Severity:    severity,
		Message:     message,
		Remediation: "Remove the nop opt-in or set extra.sandbox.driver to \"docker\". Use nop only for isolated local development.",
	}}
}

func checkSecretPlaintextContent(paths []string) []Finding {
	if paths == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		paths = []string{home + "/.metiq/.env", home + "/.metiq/mcp-auth.json"}
	}
	var findings []Finding
	for _, path := range paths {
		data, err := os.ReadFile(strings.TrimSpace(path))
		if err != nil || len(data) == 0 {
			continue
		}
		text := string(data)
		if strings.Contains(strings.ToLower(text), "private_key") || strings.Contains(strings.ToLower(text), "api_key") || strings.Contains(strings.ToLower(text), "access_token") || strings.Contains(strings.ToLower(text), "client_secret") {
			findings = append(findings, Finding{CheckID: "plaintext-secret-content", Severity: SeverityWarn, Message: fmt.Sprintf("plaintext secret file %s contains credential-looking keys", path), Remediation: "Move plaintext values into the OS-backed secret store and replace config values with env: or $ references."})
		}
	}
	return findings
}

func checkSecretRefsAndScopes(cfg state.ConfigDoc) []Finding {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	var findings []Finding
	walkConfigStrings("", root, func(path, value string) {
		refs := secrets.SecretRefs(value)
		if len(refs) == 0 {
			if sensitiveConfigPath(path) && len(strings.TrimSpace(value)) >= 16 {
				findings = append(findings, Finding{CheckID: "plaintext-secret-in-config", Severity: SeverityCritical, Message: fmt.Sprintf("config field %s appears to contain a plaintext secret", path), Remediation: "Store the value in the OS-backed secret store and replace it with an env: or $ reference."})
			}
			return
		}
		for _, ref := range refs {
			name := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(ref, "env:"), "secret:"), "$")
			if name == "" {
				continue
			}
			// Only explicit env: references can be checked from this process. Other
			// forms may resolve through the OS-backed store at runtime.
			if strings.HasPrefix(ref, "env:") {
				if _, ok := os.LookupEnv(name); !ok {
					findings = append(findings, Finding{CheckID: "secret-ref-unresolved", Severity: SeverityWarn, Message: fmt.Sprintf("config field %s references unresolved environment secret %s", path, ref), Remediation: "Set the referenced environment variable or update the config reference."})
				}
			}
			if cfg.Secrets != nil {
				if mapped, exists := cfg.Secrets[name]; exists && strings.TrimSpace(mapped) != "" && mapped != ref {
					findings = append(findings, Finding{CheckID: "secret-ref-shadowed", Severity: SeverityWarn, Message: fmt.Sprintf("config field %s references %s but secrets.%s maps to %s", path, ref, name, mapped), Remediation: "Use one canonical secret reference per field to avoid shadowing between env and configured secret registry."})
				}
			}
		}
	})
	return findings
}

func checkToolPolicyConfig(cfg state.ConfigDoc) []Finding {
	if len(cfg.Permissions.Rules) == 0 && len(cfg.Permissions.Agents) == 0 {
		return []Finding{{CheckID: "tool-policy-empty", Severity: SeverityInfo, Message: "no per-tool permission rules are configured", Remediation: "Configure permissions.rules with allow/ask/deny entries for dangerous tools and agent-scoped exceptions."}}
	}
	var findings []Finding
	if strings.EqualFold(cfg.Permissions.DefaultBehavior, "allow") && len(cfg.Permissions.Rules) == 0 {
		findings = append(findings, Finding{CheckID: "tool-policy-default-allow", Severity: SeverityWarn, Message: "tool permission default behavior is allow without explicit rules", Remediation: "Add deny/ask rules for exec, network, plugin, and MCP tools or set default_behavior to ask."})
	}
	for _, rule := range cfg.Permissions.Rules {
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Tool) == "" || strings.TrimSpace(rule.Behavior) == "" {
			findings = append(findings, Finding{CheckID: "tool-policy-invalid-rule", Severity: SeverityWarn, Message: fmt.Sprintf("tool permission rule %q is missing id/tool/behavior", rule.ID), Remediation: "Ensure each permissions.rules entry has id, behavior, and tool."})
		}
	}
	return findings
}

func checkManagedSettings(cfg state.ConfigDoc) []Finding {
	managed, ok := asStringAnyMap(cfg.Extra["managed_settings"])
	if !ok {
		return []Finding{{CheckID: "managed-settings-absent", Severity: SeverityInfo, Message: "managed settings lockdown is not configured", Remediation: "For enterprise deployments, set extra.managed_settings flags such as disable_bypass, require_tool_approval, and managed_hooks_only."}}
	}
	if boolValue(managed["allow_managed_permission_rules_only"]) && !boolValue(managed["require_tool_approval"]) {
		return []Finding{{CheckID: "managed-settings-incomplete", Severity: SeverityWarn, Message: "managed settings restrict permission rules but do not require tool approval", Remediation: "Enable extra.managed_settings.require_tool_approval for lockdown deployments."}}
	}
	return nil
}

func checkSandboxEgressPolicy(cfg state.ConfigDoc) []Finding {
	sandboxMap := sandboxConfigMap(nil, &cfg)
	if sandboxMap == nil || !(boolValue(sandboxMap["allow_network"]) || boolValue(sandboxMap["network_enabled"])) {
		return nil
	}
	allowedDomains := stringSliceValue(firstPresent(sandboxMap, "allowed_domains", "egress_allowed_domains"))
	allowedCIDRs := stringSliceValue(firstPresent(sandboxMap, "allowed_cidrs", "egress_allowed_cidrs"))
	if len(allowedDomains) == 0 && len(allowedCIDRs) == 0 {
		return []Finding{{CheckID: "sandbox-egress-unrestricted", Severity: SeverityWarn, Message: "Docker sandbox network access is enabled without an egress allowlist", Remediation: "Set extra.sandbox.allowed_domains/allowed_cidrs with an enforcing wrapper/proxy, or disable allow_network."}}
	}
	var findings []Finding
	policy, err := netpolicy.NormalizePolicy(netpolicy.Policy{AllowedDomains: allowedDomains, AllowedCIDRs: allowedCIDRs})
	if err != nil {
		findings = append(findings, Finding{CheckID: "sandbox-egress-invalid-allowlist", Severity: SeverityWarn, Message: fmt.Sprintf("Docker sandbox egress allowlist is invalid: %v", err), Remediation: "Use valid domains and CIDR/IP entries in extra.sandbox.allowed_domains/allowed_cidrs."})
	}
	if !boolValue(sandboxMap["egress_enforced"]) {
		findings = append(findings, Finding{CheckID: "sandbox-egress-allowlist-advisory", Severity: SeverityWarn, Message: "Docker sandbox egress allowlist is configured as metadata but no enforcing backend is declared", Remediation: "Set extra.sandbox.egress_enforced=true to enforce the allowlist with sandbox egress controls."})
	}
	for _, domain := range policy.Domains {
		if domain == "*" || strings.HasPrefix(domain, "*.") {
			findings = append(findings, Finding{CheckID: "sandbox-egress-broad-allowlist", Severity: SeverityWarn, Message: fmt.Sprintf("Docker sandbox egress allowlist includes broad domain pattern %q", domain), Remediation: "Use exact domains for sandbox egress whenever possible."})
		}
	}
	for _, cidr := range policy.CIDRs {
		addr := cidr.Addr().String()
		if netpolicy.IsBlockedIP(addr) {
			findings = append(findings, Finding{CheckID: "sandbox-egress-private-allowlist", Severity: SeverityWarn, Message: fmt.Sprintf("Docker sandbox egress allowlist includes private/bogon CIDR %q", cidr.String()), Remediation: "Avoid allowing private, link-local, loopback, or documentation ranges from sandbox egress."})
		}
	}
	return findings
}

func walkConfigStrings(prefix string, value any, fn func(string, string)) {
	switch x := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			walkConfigStrings(path, x[k], fn)
		}
	case []any:
		for i, v := range x {
			walkConfigStrings(fmt.Sprintf("%s[%d]", prefix, i), v, fn)
		}
	case string:
		fn(prefix, x)
	}
}

func sensitiveConfigPath(path string) bool {
	lower := strings.ToLower(path)
	for _, token := range []string{"secret", "token", "password", "private_key", "api_key", "credential"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func applySuppressions(findings []Finding, suppressions []AuditSuppression) ([]Finding, []SuppressedFinding) {
	if len(suppressions) == 0 {
		return findings, nil
	}
	active := make([]Finding, 0, len(findings))
	var suppressed []SuppressedFinding
	for _, finding := range findings {
		if reason, ok := suppressionReason(finding, suppressions); ok {
			suppressed = append(suppressed, SuppressedFinding{Finding: finding, Reason: reason})
			continue
		}
		active = append(active, finding)
	}
	if len(suppressed) > 0 {
		active = append(active, Finding{CheckID: "audit-suppressions-active", Severity: SeverityInfo, Message: fmt.Sprintf("%d audit finding(s) suppressed by configured suppressions", len(suppressed)), Remediation: "Review suppression reasons regularly and remove them once remediated."})
	}
	return active, suppressed
}

func suppressionReason(f Finding, suppressions []AuditSuppression) (string, bool) {
	for _, s := range suppressions {
		if strings.TrimSpace(s.Reason) == "" {
			continue
		}
		if s.CheckID != "" && s.CheckID != f.CheckID {
			continue
		}
		if s.TitleMatch != "" && !strings.Contains(strings.ToLower(f.Message), strings.ToLower(s.TitleMatch)) {
			continue
		}
		return s.Reason, true
	}
	return "", false
}

func (r *AuditReport) recount() {
	r.Critical, r.Warn, r.Info = 0, 0, 0
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityCritical:
			r.Critical++
		case SeverityWarn:
			r.Warn++
		default:
			r.Info++
		}
	}
}

func attestationHash(bs map[string]any, cfg *state.ConfigDoc, findings []Finding, suppressed []SuppressedFinding) string {
	findings = sortedFindings(findings)
	suppressed = sortedSuppressedFindings(suppressed)
	payload := map[string]any{
		"schema":     "metiq-security-audit-v1",
		"bootstrap":  redactedMap(bs),
		"findings":   findings,
		"suppressed": suppressed,
	}
	if cfg != nil {
		payload["config_hash"] = cfg.Hash()
		payload["permissions"] = cfg.Permissions
		payload["storage_encrypt"] = cfg.StorageEncryptEnabled()
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func redactedMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out, _ := redactSensitiveKeys("", in).(map[string]any)
	return out
}

func sortedFindings(in []Finding) []Finding {
	out := append([]Finding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CheckID != out[j].CheckID {
			return out[i].CheckID < out[j].CheckID
		}
		if out[i].Severity != out[j].Severity {
			return out[i].Severity < out[j].Severity
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func sortedSuppressedFindings(in []SuppressedFinding) []SuppressedFinding {
	out := append([]SuppressedFinding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Finding.CheckID != out[j].Finding.CheckID {
			return out[i].Finding.CheckID < out[j].Finding.CheckID
		}
		return out[i].Finding.Message < out[j].Finding.Message
	})
	return out
}

func redactSensitiveKeys(path string, value any) any {
	switch x := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			out[k] = redactSensitiveKeys(childPath, v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = redactSensitiveKeys(path, v)
		}
		return out
	case string:
		if sensitiveConfigPath(path) {
			return "[REDACTED]"
		}
		return secrets.NewRedactor(nil).RedactString(x)
	default:
		return value
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func sandboxConfigMap(bs map[string]any, cfg *state.ConfigDoc) map[string]any {
	if cfg != nil && cfg.Extra != nil {
		if sandboxMap, ok := asStringAnyMap(cfg.Extra["sandbox"]); ok {
			return sandboxMap
		}
	}
	if bs == nil {
		return nil
	}
	if sandboxMap, ok := asStringAnyMap(bs["sandbox"]); ok {
		return sandboxMap
	}
	if extra, ok := asStringAnyMap(bs["extra"]); ok {
		if sandboxMap, ok := asStringAnyMap(extra["sandbox"]); ok {
			return sandboxMap
		}
	}
	return nil
}

func sandboxDriverFromExtra(extra map[string]any) string {
	if sandboxMap, ok := asStringAnyMap(extra["sandbox"]); ok {
		return stringValue(sandboxMap["driver"])
	}
	return ""
}

func sandboxDriverFromBootstrap(bs map[string]any) string {
	if bs == nil {
		return ""
	}
	if sandboxMap, ok := asStringAnyMap(bs["sandbox"]); ok {
		return stringValue(sandboxMap["driver"])
	}
	if extra, ok := asStringAnyMap(bs["extra"]); ok {
		return sandboxDriverFromExtra(extra)
	}
	return ""
}

func isProductionDeployment(bs map[string]any, cfg *state.ConfigDoc) bool {
	if cfg != nil && cfg.Extra != nil && mapHasProductionMarker(cfg.Extra) {
		return true
	}
	if mapHasProductionMarker(bs) {
		return true
	}
	if extra, ok := asStringAnyMap(bs["extra"]); ok && mapHasProductionMarker(extra) {
		return true
	}
	return hasNonLoopbackControlIngress(bs)
}

func mapHasProductionMarker(m map[string]any) bool {
	if m == nil {
		return false
	}
	if production, ok := m["production"].(bool); ok && production {
		return true
	}
	for _, key := range []string{"environment", "env", "deployment", "mode"} {
		value := strings.ToLower(strings.TrimSpace(stringValue(m[key])))
		if value == "prod" || value == "production" || value == "live" {
			return true
		}
	}
	return false
}

func asStringAnyMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func usableControlAdminPubKeys(admins []state.ControlAdmin) []string {
	out := make([]string, 0, len(admins))
	for _, admin := range admins {
		pubkey := strings.TrimSpace(admin.PubKey)
		if pubkey == "" {
			continue
		}
		if _, err := runtime.ParsePubKey(pubkey); err != nil {
			continue
		}
		out = append(out, pubkey)
	}
	return out
}

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

func boolValue(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		value = strings.TrimSpace(value)
		return strings.EqualFold(value, "true") || value == "1"
	default:
		return false
	}
}

func isSecretRef(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "$") || strings.HasPrefix(value, "env:")
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func stringSliceValue(v any) []string {
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{value}
	default:
		return nil
	}
}

func capDropConfiguredWeak(v any) bool {
	values := stringSliceValue(v)
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), "ALL") {
			return false
		}
	}
	return true
}

func securityOptMissingNoNewPrivileges(v any) bool {
	values := stringSliceValue(v)
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), "no-new-privileges") {
			return false
		}
	}
	return true
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
