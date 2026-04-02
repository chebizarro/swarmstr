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
