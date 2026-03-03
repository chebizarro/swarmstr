package policy

import (
	"testing"

	"swarmstr/internal/store/state"
)

func TestEvaluateControlCallRequiresAuthentication(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true}}
	dec := EvaluateControlCall("", "status.get", false, cfg)
	if dec.Allowed {
		t.Fatal("expected unauthenticated call to be rejected")
	}
	if dec.Authenticated {
		t.Fatal("expected decision to indicate unauthenticated")
	}
}

func TestEvaluateControlCallRejectsWhenNoAdminsConfigured(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true}}
	dec := EvaluateControlCall("deadbeef", "status.get", true, cfg)
	if dec.Allowed {
		t.Fatal("expected rejection when no admins are configured")
	}
}

func TestEvaluateControlCallAdminWildcard(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: "deadbeef", Methods: []string{"config.*"}}}}}
	dec := EvaluateControlCall("deadbeef", "config.put", true, cfg)
	if !dec.Allowed {
		t.Fatalf("expected wildcard admin method match, got: %+v", dec)
	}
}

func TestEvaluateControlCallAdminDeniedMethod(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: "deadbeef", Methods: []string{"status.get"}}}}}
	dec := EvaluateControlCall("deadbeef", "config.put", true, cfg)
	if dec.Allowed {
		t.Fatal("expected method denial")
	}
	if !dec.Authenticated {
		t.Fatal("expected authenticated decision")
	}
}
