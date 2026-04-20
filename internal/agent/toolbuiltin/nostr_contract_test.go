package toolbuiltin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNostrToolErr_Basic(t *testing.T) {
	err := nostrToolErr("nostr_fetch", "no_relays", "no relays configured", nil)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	s := err.Error()
	if !strings.HasPrefix(s, "nostr_fetch_error:") {
		t.Errorf("error prefix = %q", s)
	}
	// Parse the JSON payload.
	jsonStr := strings.TrimPrefix(s, "nostr_fetch_error:")
	var payload map[string]any
	if e := json.Unmarshal([]byte(jsonStr), &payload); e != nil {
		t.Fatalf("invalid JSON in error: %v", e)
	}
	if payload["tool"] != "nostr_fetch" {
		t.Errorf("tool = %v", payload["tool"])
	}
	if payload["code"] != "no_relays" {
		t.Errorf("code = %v", payload["code"])
	}
}

func TestNostrToolErr_WithContext(t *testing.T) {
	err := nostrToolErr("test_tool", "fail", "something broke", map[string]any{"relay": "wss://r.example"})
	s := err.Error()
	if !strings.Contains(s, "relay") {
		t.Errorf("expected context in error, got %q", s)
	}
}

func TestNostrToolErr_Defaults(t *testing.T) {
	err := nostrToolErr("", "", "msg", nil)
	s := err.Error()
	if !strings.HasPrefix(s, "nostr_tool_error:") {
		t.Errorf("expected default tool name, got %q", s)
	}
	if !strings.Contains(s, "operation_failed") {
		t.Errorf("expected default code, got %q", s)
	}
}

func TestMapNostrPublishErr_Nil(t *testing.T) {
	if err := mapNostrPublishErr("test", nil, nil); err != nil {
		t.Errorf("expected nil for nil error, got %v", err)
	}
}

func TestMapNostrPublishErr_SignFailed(t *testing.T) {
	err := mapNostrPublishErr("test", fmt.Errorf("sign event: key missing"), nil)
	if !strings.Contains(err.Error(), "sign_failed") {
		t.Errorf("expected sign_failed code, got %q", err.Error())
	}
}

func TestMapNostrPublishErr_NoRelays(t *testing.T) {
	err := mapNostrPublishErr("test", fmt.Errorf("no relays configured"), nil)
	if !strings.Contains(err.Error(), "no_relays") {
		t.Errorf("expected no_relays code, got %q", err.Error())
	}
}

func TestMapNostrPublishErr_Required(t *testing.T) {
	err := mapNostrPublishErr("test", fmt.Errorf("field required"), nil)
	if !strings.Contains(err.Error(), "invalid_input") {
		t.Errorf("expected invalid_input code, got %q", err.Error())
	}
}

func TestMapNostrPublishErr_Default(t *testing.T) {
	err := mapNostrPublishErr("test", fmt.Errorf("unknown error"), nil)
	if !strings.Contains(err.Error(), "operation_failed") {
		t.Errorf("expected operation_failed code, got %q", err.Error())
	}
}

func TestNostrWriteSuccessEnvelope(t *testing.T) {
	out := nostrWriteSuccessEnvelope("nostr_publish", "abc123", 1,
		map[string]any{"relays": 3},
		map[string]any{"note": "test"},
		map[string]any{"legacy_field": "val"},
	)
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if result["ok"] != true {
		t.Error("expected ok=true")
	}
	if result["tool"] != "nostr_publish" {
		t.Errorf("tool = %v", result["tool"])
	}
	if result["event_id"] != "abc123" {
		t.Errorf("event_id = %v", result["event_id"])
	}
	if result["legacy_field"] != "val" {
		t.Error("expected compat field")
	}
}

func TestNostrWriteSuccessEnvelope_NoOverwrite(t *testing.T) {
	out := nostrWriteSuccessEnvelope("test", "id", 1, nil, nil,
		map[string]any{"ok": false}, // should NOT overwrite ok=true
	)
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if result["ok"] != true {
		t.Error("compat should not overwrite existing keys")
	}
}
