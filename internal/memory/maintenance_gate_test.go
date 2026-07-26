package memory

import (
	"errors"
	"testing"
)

func TestMaintenanceConfirmation_TokenBindsToScope(t *testing.T) {
	a := MaintenanceConfirmToken("resetDreamDiary", "agentA")
	b := MaintenanceConfirmToken("resetDreamDiary", "agentB")
	if a == b {
		t.Fatalf("token must bind to scope: %s == %s", a, b)
	}
	if a != MaintenanceConfirmToken("resetDreamDiary", "agentA") {
		t.Fatalf("token must be deterministic")
	}
	if MaintenanceConfirmToken("resetGroundedShortTerm", "agentA") == a {
		t.Fatalf("token must bind to operation")
	}
}

func TestMaintenanceConfirmation_Preview(t *testing.T) {
	c, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "")
	if err != nil {
		t.Fatalf("preview should not error: %v", err)
	}
	if c.Confirmed || !c.Required {
		t.Fatalf("expected preview (required, not confirmed): %+v", c)
	}
	if c.ConfirmToken != MaintenanceConfirmToken("resetDreamDiary", "s1") {
		t.Fatalf("preview must surface the expected token")
	}
}

func TestMaintenanceConfirmation_Match(t *testing.T) {
	token := MaintenanceConfirmToken("resetDreamDiary", "s1")
	c, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", token)
	if err != nil {
		t.Fatalf("match should not error: %v", err)
	}
	if !c.Confirmed || c.Required {
		t.Fatalf("expected confirmed: %+v", c)
	}
}

func TestMaintenanceConfirmation_Mismatch(t *testing.T) {
	_, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "wrong-token")
	if !errors.Is(err, ErrMaintenanceConfirmationMismatch) {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}
