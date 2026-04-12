package state

import (
	"strings"
	"testing"
)

func TestEnforceTextLimit(t *testing.T) {
	// Required field.
	if err := enforceTextLimit("title", "", 100); err == nil {
		t.Error("expected error for empty string")
	}
	if err := enforceTextLimit("title", "   ", 100); err == nil {
		t.Error("expected error for whitespace-only")
	}
	if err := enforceTextLimit("title", "ok", 100); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := enforceTextLimit("title", strings.Repeat("x", 101), 100); err == nil {
		t.Error("expected error for exceeding limit")
	}
}

func TestEnforceOptionalTextLimit(t *testing.T) {
	// Empty is OK (optional).
	if err := enforceOptionalTextLimit("desc", "", 100); err != nil {
		t.Errorf("empty should be ok: %v", err)
	}
	if err := enforceOptionalTextLimit("desc", "   ", 100); err != nil {
		t.Errorf("whitespace-only should be ok: %v", err)
	}
	// Within limit.
	if err := enforceOptionalTextLimit("desc", "short", 100); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Exceeds limit.
	if err := enforceOptionalTextLimit("desc", strings.Repeat("x", 101), 100); err == nil {
		t.Error("expected error for exceeding limit")
	}
}

func TestEnforceMetaBytes(t *testing.T) {
	// Nil is OK.
	if err := enforceMetaBytes("meta", nil, 100); err != nil {
		t.Errorf("nil should be ok: %v", err)
	}
	// Empty map is OK.
	if err := enforceMetaBytes("meta", map[string]any{}, 100); err != nil {
		t.Errorf("empty should be ok: %v", err)
	}
	// Small map.
	if err := enforceMetaBytes("meta", map[string]any{"k": "v"}, 1000); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Oversized.
	big := map[string]any{"data": strings.Repeat("x", 500)}
	if err := enforceMetaBytes("meta", big, 10); err == nil {
		t.Error("expected error for oversized meta")
	}
}
