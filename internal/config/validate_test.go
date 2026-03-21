package config

import (
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestValidateConfigDoc_empty(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{})
	if len(errs) != 0 {
		t.Fatalf("empty ConfigDoc should be valid, got: %v", errs)
	}
}

// ── DM Policy ─────────────────────────────────────────────────────────────────

func TestValidateDMPolicy_valid(t *testing.T) {
	for _, p := range []string{"pairing", "allowlist", "open", "disabled"} {
		errs := ValidateConfigDoc(state.ConfigDoc{DM: state.DMPolicy{Policy: p}})
		if len(errs) != 0 {
			t.Errorf("policy %q should be valid, got: %v", p, errs)
		}
	}
}

func TestValidateDMPolicy_unknown(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{DM: state.DMPolicy{Policy: "unknown"}})
	if len(errs) == 0 {
		t.Fatal("expected error for unknown DM policy")
	}
	if !strings.Contains(errs[0].Error(), "dm.policy") {
		t.Errorf("expected error to mention dm.policy, got: %v", errs[0])
	}
}

func TestValidateDMPolicy_empty_ok(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{DM: state.DMPolicy{Policy: ""}})
	if len(errs) != 0 {
		t.Fatalf("empty policy should be allowed, got: %v", errs)
	}
}

// ── Relay URLs ────────────────────────────────────────────────────────────────

func TestValidateRelays_valid(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Relays: state.RelayPolicy{
			Read:  []string{"wss://relay.example.com", "ws://localhost:8080"},
			Write: []string{"wss://relay2.example.com"},
		},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestValidateRelays_badScheme(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Relays: state.RelayPolicy{Read: []string{"https://not-a-ws-relay.com"}},
	})
	if len(errs) == 0 {
		t.Fatal("expected error for https relay URL")
	}
	if !strings.Contains(errs[0].Error(), "relays.read[0]") {
		t.Errorf("expected relays.read[0] in error, got: %v", errs[0])
	}
}

func TestValidateRelays_malformed(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Relays: state.RelayPolicy{Write: []string{"://bad"}},
	})
	if len(errs) == 0 {
		t.Fatal("expected error for malformed relay URL")
	}
}

func TestValidateRelays_empty(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Relays: state.RelayPolicy{Read: []string{""}},
	})
	if len(errs) == 0 {
		t.Fatal("expected error for empty relay URL")
	}
}

// ── Providers ─────────────────────────────────────────────────────────────────

func TestValidateProviders_validBaseURL(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Providers: state.ProvidersConfig{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestValidateProviders_badBaseURL(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Providers: state.ProvidersConfig{
			"myapi": {BaseURL: "ftp://wrong.scheme.com"},
		},
	})
	if len(errs) == 0 {
		t.Fatal("expected error for ftp base_url")
	}
	if !strings.Contains(errs[0].Error(), "providers.myapi.base_url") {
		t.Errorf("expected providers.myapi.base_url in error, got: %v", errs[0])
	}
}

// ── Session ───────────────────────────────────────────────────────────────────

func TestValidateSession_valid(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Session: state.SessionConfig{TTLSeconds: 3600, MaxSessions: 10, HistoryLimit: 50},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestValidateSession_negative(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Session: state.SessionConfig{TTLSeconds: -1},
	})
	if len(errs) == 0 {
		t.Fatal("expected error for negative ttl_seconds")
	}
}

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func TestValidateHeartbeat_enabled_negative_interval(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Heartbeat: state.HeartbeatConfig{Enabled: true, IntervalMS: -100},
	})
	if len(errs) == 0 {
		t.Fatal("expected error for negative interval_ms when enabled")
	}
}

func TestValidateHeartbeat_disabled_negative_interval_ok(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		Heartbeat: state.HeartbeatConfig{Enabled: false, IntervalMS: -100},
	})
	if len(errs) != 0 {
		t.Fatalf("disabled heartbeat with negative interval should be ok, got: %v", errs)
	}
}

// ── Multi-error ───────────────────────────────────────────────────────────────

func TestValidateConfigDoc_multipleErrors(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{
		DM:      state.DMPolicy{Policy: "bad-policy"},
		Relays:  state.RelayPolicy{Read: []string{"http://wrong-scheme"}},
		Session: state.SessionConfig{TTLSeconds: -5},
	})
	if len(errs) < 3 {
		t.Fatalf("expected at least 3 errors, got %d: %v", len(errs), errs)
	}
}
