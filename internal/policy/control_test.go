package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/store/state"
)

func TestAuthenticateControlCall_ValidNIP98Event(t *testing.T) {
	payload := []byte(`{"method":"status.get"}`)
	req, authHeader, caller := buildNIP98AuthHeader(t, http.MethodPost, "http://localhost:7423/call", payload, nil)
	req.Header.Set("X-Nostr-Authorization", authHeader)

	auth := AuthenticateControlCall(req, payload, 30*time.Second)
	if !auth.Authenticated {
		t.Fatalf("expected authenticated call, got reason=%q", auth.Reason)
	}
	if auth.CallerPubKey != caller {
		t.Fatalf("caller pubkey mismatch got=%q want=%q", auth.CallerPubKey, caller)
	}
}

func TestAuthenticateControlCall_RejectsMethodTagMismatch(t *testing.T) {
	payload := []byte(`{"method":"status.get"}`)
	req, authHeader, _ := buildNIP98AuthHeader(t, http.MethodPost, "http://localhost:7423/call", payload, map[string]string{"method": http.MethodGet})
	req.Header.Set("X-Nostr-Authorization", authHeader)

	auth := AuthenticateControlCall(req, payload, 30*time.Second)
	if auth.Authenticated {
		t.Fatal("expected method tag mismatch to be rejected")
	}
	if auth.Reason == "" {
		t.Fatal("expected rejection reason")
	}
}

func TestAuthenticateControlCall_RejectsPayloadMismatch(t *testing.T) {
	payload := []byte(`{"method":"status.get"}`)
	req, authHeader, _ := buildNIP98AuthHeader(t, http.MethodPost, "http://localhost:7423/call", payload, map[string]string{"payload": "deadbeef"})
	req.Header.Set("X-Nostr-Authorization", authHeader)

	auth := AuthenticateControlCall(req, payload, 30*time.Second)
	if auth.Authenticated {
		t.Fatal("expected payload hash mismatch to be rejected")
	}
	if auth.Reason == "" {
		t.Fatal("expected rejection reason")
	}
}

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

func TestEvaluateControlCall_AllowUnauthMethodsBypassesAuth(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, AllowUnauthMethods: []string{"supportedmethods"}}}
	dec := EvaluateControlCall("", "supportedmethods", false, cfg)
	if !dec.Allowed {
		t.Fatalf("expected allow_unauth_methods bypass, got: %+v", dec)
	}
	if dec.Authenticated {
		t.Fatal("expected unauthenticated decision")
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

func TestEvaluateControlCallSensitiveMethodsRequireAuthenticatedAdmin(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false, AllowUnauthMethods: []string{"*", "config.*", "list.put"}}}
	for _, method := range []string{"config.put", "config.set", "config.apply", "config.patch", "list.put", "secrets.resolve", "node.pending.pull"} {
		dec := EvaluateControlCall("", method, false, cfg)
		if dec.Allowed {
			t.Fatalf("expected unauthenticated %s to be rejected despite permissive config", method)
		}
		dec = EvaluateControlCall("deadbeef", method, true, cfg)
		if dec.Allowed {
			t.Fatalf("expected authenticated %s to be rejected without configured admin", method)
		}
	}
}

func TestEvaluateControlCallAppliesActualCallerMethodRestrictions(t *testing.T) {
	cfg := state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{
		{PubKey: "operator-a", Methods: []string{"status.get"}},
		{PubKey: "operator-b", Methods: []string{"config.set"}},
	}}}
	if dec := EvaluateControlCall("operator-a", "status.get", true, cfg); !dec.Allowed {
		t.Fatalf("operator-a should be allowed status.get: %+v", dec)
	}
	if dec := EvaluateControlCall("operator-a", "config.set", true, cfg); dec.Allowed {
		t.Fatal("operator-a should not inherit config.set")
	}
	if dec := EvaluateControlCall("operator-b", "config.set", true, cfg); !dec.Allowed {
		t.Fatalf("operator-b should be allowed config.set: %+v", dec)
	}
}

func TestValidateConfigRejectsUnsafeAllowUnauthMethods(t *testing.T) {
	for _, method := range []string{"*", "config.*", "config.set", "list.put"} {
		cfg := state.ConfigDoc{Control: state.ControlPolicy{AllowUnauthMethods: []string{method}}}
		if err := ValidateConfig(cfg); err == nil {
			t.Fatalf("expected unsafe allow_unauth_methods entry %q to be rejected", method)
		}
	}
	cfg := state.ConfigDoc{Relays: state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}}, Control: state.ControlPolicy{AllowUnauthMethods: []string{"supportedmethods", "health"}}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected safe allow_unauth_methods to validate: %v", err)
	}
}

func buildNIP98AuthHeader(t *testing.T, method, url string, payload []byte, overrides map[string]string) (*http.Request, string, string) {
	t.Helper()
	req := httptest.NewRequest(method, url, bytes.NewReader(payload))
	hash := sha256.Sum256(payload)
	payloadHash := nostr.HexEncodeToString(hash[:])
	if v, ok := overrides["payload"]; ok {
		payloadHash = v
	}
	tagMethod := method
	if v, ok := overrides["method"]; ok {
		tagMethod = v
	}
	tagURL := url
	if v, ok := overrides["u"]; ok {
		tagURL = v
	}
	createdAt := nostr.Now()
	if v, ok := overrides["created_at"]; ok && v == "old" {
		createdAt = createdAt - nostr.Timestamp(90)
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(27235),
		CreatedAt: createdAt,
		Tags: nostr.Tags{
			{"method", tagMethod},
			{"u", tagURL},
			{"payload", payloadHash},
		},
	}
	sk := nostr.Generate()
	if err := evt.Sign([32]byte(sk)); err != nil {
		t.Fatalf("sign auth event: %v", err)
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal auth event: %v", err)
	}
	caller := evt.PubKey.Hex()
	return req, "Nostr " + base64.StdEncoding.EncodeToString(raw), caller
}
