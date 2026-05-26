package security

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

func TestAuditSuppressionsAndAttestation(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{"sandbox": map[string]any{"driver": "nop"}}}
	report := Audit(AuditOptions{ConfigDoc: &cfg, Suppressions: []AuditSuppression{{CheckID: "sandbox-nop-driver", Reason: "accepted local dev risk"}}})
	assertNoFinding(t, report, "sandbox-nop-driver")
	if len(report.SuppressedFindings) != 1 {
		t.Fatalf("suppressed = %d, want 1", len(report.SuppressedFindings))
	}
	if report.AttestationHash == "" {
		t.Fatal("expected attestation hash")
	}
	drift := Audit(AuditOptions{ConfigDoc: &cfg, ExpectedAttestation: "sha256:not-it"})
	if !drift.AttestationDrift {
		t.Fatal("expected attestation drift")
	}
	assertFindingSeverity(t, drift, "audit-attestation-drift", SeverityWarn)
}

func TestPluginIntegrityAndSafetyAudit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte("eval('1+1')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := Audit(AuditOptions{PluginPaths: []string{dir}})
	assertFindingSeverity(t, report, "plugin-integrity-missing", SeverityWarn)
	assertFindingSeverity(t, report, "plugin-dangerous-eval", SeverityCritical)

	if _, err := RecordPluginIntegrity(dir); err != nil {
		t.Fatalf("RecordPluginIntegrity: %v", err)
	}
	okReport := Audit(AuditOptions{PluginPaths: []string{dir}})
	assertNoFinding(t, okReport, "plugin-integrity-missing")
	if err := os.WriteFile(filepath.Join(dir, "extra.js"), []byte("console.log('tamper')"), 0o644); err != nil {
		t.Fatal(err)
	}
	mismatch := Audit(AuditOptions{PluginPaths: []string{dir}})
	assertFindingSeverity(t, mismatch, "plugin-integrity-mismatch", SeverityCritical)
}

func TestAuditSecretAndSandboxPolicyFindings(t *testing.T) {
	cfg := state.ConfigDoc{
		NostrChannels: state.NostrChannelsConfig{"x": {Config: map[string]any{"token": "plain-secret-token-12345"}}},
		Extra:         map[string]any{"sandbox": map[string]any{"driver": "docker", "allow_network": true}},
	}
	report := Audit(AuditOptions{ConfigDoc: &cfg})
	assertFindingSeverity(t, report, "plaintext-secret-in-config", SeverityCritical)
	assertFindingSeverity(t, report, "sandbox-egress-unrestricted", SeverityWarn)
	assertFindingSeverity(t, report, "managed-settings-absent", SeverityInfo)
}

func TestFixChmodsSecretFile(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, ".env")
	if err := os.WriteFile(secret, []byte("API_KEY=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Fix(FixOptions{BootstrapPath: filepath.Join(dir, "missing.json"), SecretPaths: []string{secret}})
	if len(res.Actions) == 0 || !res.Actions[len(res.Actions)-1].Applied {
		t.Fatalf("expected applied chmod: %+v", res)
	}
	info, err := os.Stat(secret)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
