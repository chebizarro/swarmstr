package security

import (
	"os"
	"path/filepath"
	"testing"

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

func writeBootstrap(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile bootstrap: %v", err)
	}
	return path
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
