package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/store/state"
)

func TestAuditStateDocEncryptionEnabledByDefault(t *testing.T) {
	report := Audit(AuditOptions{
		ConfigDoc: &state.ConfigDoc{},
	})
	for _, finding := range report.Findings {
		if finding.CheckID == "nip44-disabled" {
			t.Fatalf("unexpected storage encryption finding: %+v", finding)
		}
	}
}

func TestAuditStateDocEncryptionDisabledFinding(t *testing.T) {
	cfg := state.ConfigDoc{
		Storage: state.StorageConfig{Encrypt: state.BoolPtr(false)},
	}
	report := Audit(AuditOptions{ConfigDoc: &cfg})
	found := false
	for _, finding := range report.Findings {
		if finding.CheckID != "nip44-disabled" {
			continue
		}
		found = true
		if finding.Severity != SeverityWarn {
			t.Fatalf("unexpected severity: %+v", finding)
		}
		if finding.Message == "" || finding.Remediation == "" {
			t.Fatalf("expected message/remediation: %+v", finding)
		}
	}
	if !found {
		t.Fatal("expected storage encryption warning")
	}
}

func TestAuditStateDocEncryptionUnknownWithoutConfigDoc(t *testing.T) {
	tmpDir := t.TempDir()
	bootstrapPath := filepath.Join(tmpDir, "bootstrap.json")
	if err := os.WriteFile(bootstrapPath, []byte(`{"enable_nip44":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile bootstrap: %v", err)
	}
	report := Audit(AuditOptions{BootstrapPath: bootstrapPath})
	for _, finding := range report.Findings {
		if finding.CheckID == "nip44-disabled" {
			return
		}
	}
	t.Fatal("expected storage encryption verification warning when live config is unavailable")
}

func TestAuditControlFindingsByExposure(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false, LegacyTokenFallback: true}}
	remoteBootstrap := writeBootstrap(t, `{"admin_listen_addr":"0.0.0.0:7777"}`)
	localBootstrap := writeBootstrap(t, `{"admin_listen_addr":"127.0.0.1:7777"}`)

	remoteReport := Audit(AuditOptions{BootstrapPath: remoteBootstrap, ConfigDoc: &cfg})
	assertFindingSeverity(t, remoteReport, "control-auth-disabled", SeverityCritical)
	assertFindingSeverity(t, remoteReport, "control-legacy-token-fallback", SeverityCritical)

	localReport := Audit(AuditOptions{BootstrapPath: localBootstrap, ConfigDoc: &cfg})
	assertFindingSeverity(t, localReport, "control-auth-disabled", SeverityWarn)
	assertFindingSeverity(t, localReport, "control-legacy-token-fallback", SeverityWarn)
}

func TestAuditControlUnsafeAllowUnauthMethods(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: "admin"}}, AllowUnauthMethods: []string{"supportedmethods", "config.set", "*"}}}
	remoteBootstrap := writeBootstrap(t, `{"gateway_ws_listen_addr":"0.0.0.0:9100"}`)
	report := Audit(AuditOptions{BootstrapPath: remoteBootstrap, ConfigDoc: &cfg})
	assertFindingSeverity(t, report, "control-unsafe-allow-unauth-method", SeverityCritical)
}

func TestAuditControlNoAdmins(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true}}
	localBootstrap := writeBootstrap(t, `{"admin_listen_addr":"127.0.0.1:7777"}`)
	localReport := Audit(AuditOptions{BootstrapPath: localBootstrap, ConfigDoc: &cfg})
	assertFindingSeverity(t, localReport, "control-no-admins", SeverityWarn)

	remoteBootstrap := writeBootstrap(t, `{"admin_listen_addr":"0.0.0.0:7777"}`)
	remoteReport := Audit(AuditOptions{BootstrapPath: remoteBootstrap, ConfigDoc: &cfg})
	assertFindingSeverity(t, remoteReport, "control-no-admins", SeverityCritical)

	invalidAdminCfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: "not-a-pubkey"}}}}
	invalidAdminReport := Audit(AuditOptions{BootstrapPath: localBootstrap, ConfigDoc: &invalidAdminCfg})
	assertFindingSeverity(t, invalidAdminReport, "control-no-admins", SeverityWarn)

	secureCfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: testAuditPubKey(t)}}}}
	secureReport := Audit(AuditOptions{BootstrapPath: localBootstrap, ConfigDoc: &secureCfg})
	assertNoFinding(t, secureReport, "control-no-admins")
	assertNoFinding(t, secureReport, "control-unsafe-allow-unauth-method")
}

func TestAuditGatewayWSTrustedProxyAndInsecureControlUIFindings(t *testing.T) {
	localBootstrap := writeBootstrap(t, `{"gateway_ws_listen_addr":"127.0.0.1:9100","gateway_ws_allow_insecure_control_ui":true,"gateway_ws_trusted_proxies":["10.0.0.0/8"]}`)
	localReport := Audit(AuditOptions{BootstrapPath: localBootstrap, ConfigDoc: &state.ConfigDoc{}})
	assertFindingSeverity(t, localReport, "gateway-ws-trusted-proxy-auth", SeverityWarn)
	assertFindingSeverity(t, localReport, "gateway-ws-insecure-control-ui", SeverityWarn)

	remoteBootstrap := writeBootstrap(t, `{"gateway_ws_listen_addr":"0.0.0.0:9100","gateway_ws_allow_insecure_control_ui":true,"gateway_ws_trusted_proxies":["10.0.0.0/8"]}`)
	remoteReport := Audit(AuditOptions{BootstrapPath: remoteBootstrap, ConfigDoc: &state.ConfigDoc{}})
	assertFindingSeverity(t, remoteReport, "gateway-ws-trusted-proxy-auth", SeverityWarn)
	assertFindingSeverity(t, remoteReport, "gateway-ws-insecure-control-ui", SeverityCritical)
}

func TestAuditDockerSandboxHardeningFindings(t *testing.T) {
	weakCfg := state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{
			"driver":           "docker",
			"allow_network":    true,
			"read_only_rootfs": false,
			"user":             "0:0",
			"pids_limit":       0,
			"cap_drop":         []any{"NET_RAW"},
			"security_opt":     []any{"seccomp=/tmp/seccomp.json"},
		},
	}}
	weakReport := Audit(AuditOptions{ConfigDoc: &weakCfg})
	assertFindingSeverity(t, weakReport, "sandbox-docker-network-enabled", SeverityWarn)
	assertFindingSeverity(t, weakReport, "sandbox-docker-writable-rootfs", SeverityWarn)
	assertFindingSeverity(t, weakReport, "sandbox-docker-root-user", SeverityWarn)
	assertFindingSeverity(t, weakReport, "sandbox-docker-unlimited-pids", SeverityWarn)
	assertFindingSeverity(t, weakReport, "sandbox-docker-capabilities", SeverityWarn)
	assertFindingSeverity(t, weakReport, "sandbox-docker-new-privileges", SeverityWarn)

	defaultCfg := state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{"driver": "docker"},
	}}
	defaultReport := Audit(AuditOptions{ConfigDoc: &defaultCfg})
	assertNoFinding(t, defaultReport, "sandbox-docker-network-enabled")
	assertNoFinding(t, defaultReport, "sandbox-docker-writable-rootfs")
	assertNoFinding(t, defaultReport, "sandbox-docker-root-user")
	assertNoFinding(t, defaultReport, "sandbox-docker-unlimited-pids")
	assertNoFinding(t, defaultReport, "sandbox-docker-capabilities")
	assertNoFinding(t, defaultReport, "sandbox-docker-new-privileges")
}

func TestAuditE2EChannelPolicyFindings(t *testing.T) {
	cfg := state.ConfigDoc{NostrChannels: state.NostrChannelsConfig{
		"plain-key": {Config: map[string]any{
			"e2e_private_key": "1111111111111111111111111111111111111111111111111111111111111111",
			"e2e_peer_pubkey": "peer",
		}},
		"missing-key": {Config: map[string]any{"e2e.required": true}},
		"secret-ref": {Config: map[string]any{
			"e2e_private_key": "env:E2E_PRIVATE",
			"e2e_peer_pubkey": "env:E2E_PEER",
			"e2e.required":    true,
		}},
	}}
	report := Audit(AuditOptions{ConfigDoc: &cfg})
	assertFindingSeverity(t, report, "channel-e2e-not-required", SeverityWarn)
	assertFindingSeverity(t, report, "channel-e2e-incomplete", SeverityCritical)
	assertFindingSeverity(t, report, "channel-e2e-private-key-in-config", SeverityCritical)
	for _, finding := range report.Findings {
		if finding.CheckID == "channel-e2e-private-key-in-config" && !strings.Contains(finding.Message, "plain-key") {
			t.Fatalf("private-key finding should reference plaintext channel only: %+v", finding)
		}
	}
}

func TestAuditPlaintextSecretFileFindings(t *testing.T) {
	dir := t.TempDir()
	securePath := filepath.Join(dir, ".env")
	if err := os.WriteFile(securePath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile secure secret: %v", err)
	}
	report := Audit(AuditOptions{SecretPaths: []string{securePath}})
	assertFindingSeverity(t, report, "plaintext-secret-file", SeverityWarn)

	loosePath := filepath.Join(dir, "mcp-auth.json")
	if err := os.WriteFile(loosePath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile loose secret: %v", err)
	}
	looseReport := Audit(AuditOptions{SecretPaths: []string{loosePath}})
	assertFindingSeverity(t, looseReport, "secret-file-perms", SeverityCritical)
}

func TestAuditSandboxNopDriverFindings(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{"driver": "nop"},
	}}
	localReport := Audit(AuditOptions{ConfigDoc: &cfg})
	assertFindingSeverity(t, localReport, "sandbox-nop-driver", SeverityWarn)

	productionCfg := state.ConfigDoc{Extra: map[string]any{
		"environment": "production",
		"sandbox":     map[string]any{"driver": "nop"},
	}}
	productionReport := Audit(AuditOptions{ConfigDoc: &productionCfg})
	assertFindingSeverity(t, productionReport, "sandbox-nop-driver", SeverityCritical)

	dockerCfg := state.ConfigDoc{Extra: map[string]any{
		"environment": "production",
		"sandbox":     map[string]any{"driver": "docker"},
	}}
	dockerReport := Audit(AuditOptions{ConfigDoc: &dockerCfg})
	assertNoFinding(t, dockerReport, "sandbox-nop-driver")

	bootstrapSandbox := writeBootstrap(t, `{"sandbox":{"driver":"nop"},"admin_listen_addr":"127.0.0.1:7777"}`)
	bootstrapSandboxReport := Audit(AuditOptions{BootstrapPath: bootstrapSandbox})
	assertFindingSeverity(t, bootstrapSandboxReport, "sandbox-nop-driver", SeverityWarn)

	bootstrapExtraSandbox := writeBootstrap(t, `{"extra":{"sandbox":{"driver":"nop"}},"gateway_ws_listen_addr":"0.0.0.0:9100"}`)
	bootstrapExtraSandboxReport := Audit(AuditOptions{BootstrapPath: bootstrapExtraSandbox})
	assertFindingSeverity(t, bootstrapExtraSandboxReport, "sandbox-nop-driver", SeverityCritical)
}

func testAuditPubKey(t *testing.T) string {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("SecretKeyFromHex: %v", err)
	}
	return nostr.GetPublicKey(sk).Hex()
}

func writeBootstrap(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile bootstrap: %v", err)
	}
	return path
}

func assertNoFinding(t *testing.T, report AuditReport, checkID string) {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.CheckID == checkID {
			t.Fatalf("unexpected finding %s: %+v", checkID, finding)
		}
	}
}

func assertFindingSeverity(t *testing.T, report AuditReport, checkID string, severity string) {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.CheckID != checkID {
			continue
		}
		if finding.Severity != severity {
			t.Fatalf("finding %s severity=%s want=%s", checkID, finding.Severity, severity)
		}
		return
	}
	t.Fatalf("missing finding %s", checkID)
}
