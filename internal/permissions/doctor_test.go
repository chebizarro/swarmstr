package permissions

import (
	"strings"
	"testing"
)

func TestDoctorExecApprovalPolicyValidSignature(t *testing.T) {
	report := DoctorExecApprovalPolicy(map[string]any{
		"allow_always_signatures": []any{`["exec","ls","-la"]`},
	})
	if !report.Valid() || len(report.Findings) != 0 {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestDoctorExecApprovalPolicyReportsConflictsAndLiveFields(t *testing.T) {
	report := DoctorExecApprovalPolicy(map[string]any{
		"mode":                    "deny",
		"tools":                   []any{"*", "bash", "bash"},
		"timeout_ms":              0,
		"allow_always_signatures": []any{`["exec","ls"]`, `["exec","bash","-c","echo hi"]`, `["exec","ls"]`},
		"mystery":                 true,
	})
	if report.Valid() {
		t.Fatalf("conflicting policy passed: %+v", report)
	}
	var codes []string
	for _, finding := range report.Findings {
		codes = append(codes, finding.Code)
	}
	joined := strings.Join(codes, ",")
	for _, want := range []string{"conflicting-policy", "duplicate-signature", "duplicate-tool", "invalid-timeout", "unknown-policy-field", "unsafe-signature", "unsafe-tool-pattern"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %+v", want, report.Findings)
		}
	}
}

func TestDoctorExecApprovalPolicyRejectsInvalidShapes(t *testing.T) {
	report := DoctorExecApprovalPolicy(map[string]any{
		"mode":                    "sometimes",
		"tools":                   "bash",
		"allow_always_signatures": []any{"not-json"},
	})
	if report.Valid() {
		t.Fatal("invalid policy passed")
	}
}
