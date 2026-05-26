package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/plugins/installer"
	"metiq/internal/store/state"
)

func TestManagerLoadRejectsPluginIntegrityMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("module.exports = {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.RecordPluginIntegrity(dir); err != nil {
		t.Fatalf("RecordPluginIntegrity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("module.exports = { tampered: true };\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := New(nil)
	err := mgr.Load(context.Background(), state.ConfigDoc{Extra: map[string]any{
		"extensions": map[string]any{
			"load_paths": []any{filepath.Dir(dir)},
			"entries": map[string]any{
				"plug": map[string]any{"enabled": true, "plugin_type": "goja", "install_path": dir},
			},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatalf("expected integrity mismatch load error, got %v", err)
	}
}
