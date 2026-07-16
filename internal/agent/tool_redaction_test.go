package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolRedactorRedactsPaymentAndCredentialArgsWithoutMutatingInput(t *testing.T) {
	secrets := map[string]string{
		"invoice":         "lnbc1invoice-secret",
		"payment_request": "lnbc1dynamic-lnd-secret",
		"macaroon":        "AgEDbG5k-secret",
		"preimage":        strings.Repeat("ab", 32),
		"authorization":   "Bearer authorization-secret",
		"l402":            "l402-token-secret",
		"lsat":            "lsat-token-secret",
	}
	args := map[string]any{
		"invoice": secrets["invoice"],
		"request": map[string]any{
			"payment_request": secrets["payment_request"],
			"preimage":        secrets["preimage"],
		},
		"metadata": map[string]any{
			"authorization": secrets["authorization"],
			"x-request-id":  "req-safe",
		},
		"macaroon":    secrets["macaroon"],
		"l402":        secrets["l402"],
		"nested":      map[string]any{"lsat": secrets["lsat"], "payment_hash": "hash-safe"},
		"amount_msat": float64(21000),
		"memo":        "safe memo",
	}
	call := ToolCall{Name: "nwc_pay_invoice", Args: args}

	safe := NewToolRedactor().RedactToolCall(call, ToolDescriptor{Name: call.Name})

	for _, path := range []struct {
		name string
		got  any
	}{
		{"invoice", safe.Args["invoice"]},
		{"payment_request", safe.Args["request"].(map[string]any)["payment_request"]},
		{"preimage", safe.Args["request"].(map[string]any)["preimage"]},
		{"authorization", safe.Args["metadata"].(map[string]any)["authorization"]},
		{"macaroon", safe.Args["macaroon"]},
		{"l402", safe.Args["l402"]},
		{"lsat", safe.Args["nested"].(map[string]any)["lsat"]},
	} {
		if path.got != RedactedToolValue {
			t.Errorf("%s = %#v, want %q", path.name, path.got, RedactedToolValue)
		}
	}
	if got := safe.Args["amount_msat"]; got != float64(21000) {
		t.Errorf("safe amount changed: %#v", got)
	}
	if got := safe.Args["memo"]; got != "safe memo" {
		t.Errorf("safe memo changed: %#v", got)
	}
	if got := safe.Args["metadata"].(map[string]any)["x-request-id"]; got != "req-safe" {
		t.Errorf("safe metadata changed: %#v", got)
	}
	if got := safe.Args["nested"].(map[string]any)["payment_hash"]; got != "hash-safe" {
		t.Errorf("safe payment hash changed: %#v", got)
	}

	for key, secret := range secrets {
		encoded, err := json.Marshal(safe.Args)
		if err != nil {
			t.Fatalf("marshal safe args: %v", err)
		}
		if strings.Contains(string(encoded), secret) {
			t.Errorf("%s secret leaked in redacted args: %s", key, encoded)
		}
	}
	if got := args["invoice"]; got != secrets["invoice"] {
		t.Fatalf("input invoice was mutated: %#v", got)
	}
	if got := args["request"].(map[string]any)["payment_request"]; got != secrets["payment_request"] {
		t.Fatalf("nested input was mutated: %#v", got)
	}
}

func TestToolRedactorUsesDescriptorSensitivityMarkers(t *testing.T) {
	call := ToolCall{Name: "custom", Args: map[string]any{
		"opaque": "descriptor-secret",
		"label":  "safe-label",
	}}
	descriptor := ToolDescriptor{
		Name: "custom",
		InputJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"opaque": map[string]any{"type": "string", "x-sensitive": true},
				"label":  map[string]any{"type": "string"},
			},
		},
	}

	safe := NewToolRedactor().RedactToolCall(call, descriptor)
	if safe.Args["opaque"] != RedactedToolValue {
		t.Fatalf("descriptor-sensitive field = %#v", safe.Args["opaque"])
	}
	if safe.Args["label"] != "safe-label" {
		t.Fatalf("safe descriptor field changed: %#v", safe.Args["label"])
	}
}

func TestToolCallRefForPersistenceRedactsAndKeepsSafeFields(t *testing.T) {
	call := ToolCall{ID: "call-1", Name: "lnd_send_payment", Args: map[string]any{
		"payment_request": "lnbc1secret",
		"fee_limit_msat":  float64(25),
	}}
	ref := ToolCallRefForPersistence(nil, call)

	var args map[string]any
	if err := json.Unmarshal([]byte(ref.ArgsJSON), &args); err != nil {
		t.Fatalf("decode persisted args: %v", err)
	}
	if args["payment_request"] != RedactedToolValue {
		t.Fatalf("payment request persisted: %#v", args)
	}
	if args["fee_limit_msat"] != float64(25) {
		t.Fatalf("safe fee changed: %#v", args)
	}
	if call.Args["payment_request"] != "lnbc1secret" {
		t.Fatalf("original execution args changed: %#v", call.Args)
	}
}

func TestRestoreRedactedToolArgsPreservesSecretsAndAppliesSafeMutations(t *testing.T) {
	original := map[string]any{
		"invoice":     "lnbc1original",
		"amount_msat": float64(1000),
		"nested":      map[string]any{"authorization": "Bearer original", "safe": "old"},
	}
	mutatedSafe := map[string]any{
		"invoice":     RedactedToolValue,
		"amount_msat": float64(2000),
		"nested":      map[string]any{"authorization": RedactedToolValue, "safe": "new"},
	}

	restored := RestoreRedactedToolArgs(original, mutatedSafe)
	if restored["invoice"] != "lnbc1original" {
		t.Fatalf("invoice not restored: %#v", restored)
	}
	nested := restored["nested"].(map[string]any)
	if nested["authorization"] != "Bearer original" {
		t.Fatalf("authorization not restored: %#v", nested)
	}
	if restored["amount_msat"] != float64(2000) || nested["safe"] != "new" {
		t.Fatalf("safe hook mutations not applied: %#v", restored)
	}
}
