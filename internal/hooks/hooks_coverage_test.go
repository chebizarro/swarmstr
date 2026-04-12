package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── StatusToMap / MarshalStatus ─────────────────────────────────────────────

func TestStatusToMap(t *testing.T) {
	s := HookStatus{
		HookKey:     "test-hook",
		Name:        "Test Hook",
		Description: "A test",
		Source:      SourceBundled,
		Emoji:       "🔧",
		Events:      []string{"command:new"},
		Enabled:     true,
		Eligible:    true,
		FilePath:    "/tmp/hook",
		Install: []HookInstallSpec{
			{ID: "sp1", Kind: "npm", Label: "npm install"},
		},
		Requires: &HookRequires{
			Bins:   []string{"node"},
			Env:    []string{"API_KEY"},
			OS:     []string{"linux"},
		},
	}

	m := StatusToMap(s)
	if m["hookKey"] != "test-hook" {
		t.Errorf("hookKey: %v", m["hookKey"])
	}
	if m["name"] != "Test Hook" {
		t.Errorf("name: %v", m["name"])
	}
	if m["enabled"] != true {
		t.Errorf("enabled: %v", m["enabled"])
	}
	req, ok := m["requires"].(map[string]any)
	if !ok {
		t.Fatal("requires should be a map")
	}
	if req["bins"] == nil {
		t.Error("requires.bins should be set")
	}
	install, ok := m["install"].([]map[string]any)
	if !ok || len(install) != 1 {
		t.Errorf("install: %v", m["install"])
	}
}

func TestStatusToMap_NoRequires(t *testing.T) {
	s := HookStatus{HookKey: "minimal"}
	m := StatusToMap(s)
	if _, ok := m["requires"]; ok {
		t.Error("requires should not be present when nil")
	}
}

func TestMarshalStatus(t *testing.T) {
	statuses := []HookStatus{
		{HookKey: "h1", Name: "Hook One"},
		{HookKey: "h2", Name: "Hook Two"},
	}
	raw, err := MarshalStatus(statuses)
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

// ─── looksLikeBundledHooksDir ────────────────────────────────────────────────

func TestLooksLikeBundledHooksDir(t *testing.T) {
	// Empty dir should not look like hooks dir
	dir := t.TempDir()
	if looksLikeBundledHooksDir(dir) {
		t.Error("empty dir should return false")
	}

	// Non-existent dir
	if looksLikeBundledHooksDir("/nonexistent/path") {
		t.Error("nonexistent should return false")
	}

	// Dir with a subdirectory containing HOOK.md
	hookDir := filepath.Join(dir, "my-hook")
	os.MkdirAll(hookDir, 0755)
	os.WriteFile(filepath.Join(hookDir, "HOOK.md"), []byte("---\nname: test\n---\n"), 0644)
	if !looksLikeBundledHooksDir(dir) {
		t.Error("dir with HOOK.md should return true")
	}
}

// ─── BundledHooksDir with env override ───────────────────────────────────────

func TestBundledHooksDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_HOOKS_DIR", dir)
	got := BundledHooksDir()
	if got != dir {
		t.Errorf("expected %q, got %q", dir, got)
	}
}

// ─── ManagedHooksDir with env override ───────────────────────────────────────

func TestManagedHooksDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("METIQ_MANAGED_HOOKS_DIR", dir)
	got := ManagedHooksDir()
	if got != dir {
		t.Errorf("expected %q, got %q", dir, got)
	}
}
