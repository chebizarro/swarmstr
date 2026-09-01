package memory

import (
	"errors"
	"testing"
)

func TestMaintenanceConfirmationPreviewIsRandomAndBound(t *testing.T) {
	a, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "agentA", "state-1", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "agentA", "state-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ConfirmToken == b.ConfirmToken {
		t.Fatal("confirmation challenges must be random")
	}
	if !a.Required || a.Confirmed || a.StateVersion != "state-1" || a.ExpiresAt == 0 {
		t.Fatalf("unexpected preview: %+v", a)
	}
	if _, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "agentB", "state-1", a.ConfirmToken); !errors.Is(err, ErrMaintenanceConfirmationMismatch) {
		t.Fatalf("scope mismatch: %v", err)
	}
}

func TestMaintenanceConfirmationMatchIsSingleUse(t *testing.T) {
	preview, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "state-1", "")
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "state-1", preview.ConfirmToken)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !confirmed.Confirmed || confirmed.Required {
		t.Fatalf("expected confirmed: %+v", confirmed)
	}
	if _, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "state-1", preview.ConfirmToken); !errors.Is(err, ErrMaintenanceConfirmationMismatch) {
		t.Fatalf("replay should fail: %v", err)
	}
}

func TestMaintenanceConfirmationRejectsStaleState(t *testing.T) {
	preview, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "state-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "state-2", preview.ConfirmToken); !errors.Is(err, ErrMaintenanceConfirmationStale) {
		t.Fatalf("expected stale-state error, got %v", err)
	}
}

func TestMaintenanceConfirmationMismatch(t *testing.T) {
	_, err := EvaluateMaintenanceConfirmation("resetDreamDiary", "s1", "state-1", "wrong-token")
	if !errors.Is(err, ErrMaintenanceConfirmationMismatch) {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}
