package secrets

import (
	"strings"
	"testing"
)

func TestRedactorRedactsKnownValuesAndPatterns(t *testing.T) {
	r := NewRedactor(map[string]string{"api": "super-secret-token"})
	out := r.RedactString("token=super-secret-token api_key=abcdef1234567890")
	if strings.Contains(out, "super-secret-token") || strings.Contains(out, "abcdef1234567890") {
		t.Fatalf("secret leaked after redaction: %s", out)
	}
	if !strings.Contains(out, "[REDACTED:api]") {
		t.Fatalf("known value marker missing: %s", out)
	}
}

func TestTargetRegistryValidate(t *testing.T) {
	reg := TargetRegistry{Rules: []TargetRule{{PathPattern: "providers.*.api_key", AllowedRefs: []string{"env:PROVIDER_*"}, Required: true}}}
	if err := reg.Validate("providers.openai.api_key", "plaintext-secret-value"); err == nil {
		t.Fatal("expected plaintext secret rejection")
	}
	if err := reg.Validate("providers.openai.api_key", "env:OTHER_KEY"); err == nil {
		t.Fatal("expected disallowed ref rejection")
	}
	if err := reg.Validate("providers.openai.api_key", "env:PROVIDER_OPENAI_KEY"); err != nil {
		t.Fatalf("expected allowed ref: %v", err)
	}
}

func TestPlanMigration(t *testing.T) {
	plan := PlanMigration(map[string]any{"providers": map[string]any{"x": map[string]any{"api_key": "sk-1234567890abcdef"}}})
	if len(plan.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(plan.Changes))
	}
	if plan.Changes[0].Replacement == "" || plan.Changes[0].Original == "" {
		t.Fatalf("bad change: %+v", plan.Changes[0])
	}
}
