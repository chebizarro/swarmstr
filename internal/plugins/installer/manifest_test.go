package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOpenClawManifest_FromOpenClawFile(t *testing.T) {
	dir := t.TempDir()
	data := `{"id":"oc.test","name":"Test","version":"1.0.0","kind":["tool"],"entry":"dist/index.js"}`
	if err := os.WriteFile(filepath.Join(dir, "openclaw.plugin.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	mf, err := LoadOpenClawManifest(dir)
	if err != nil {
		t.Fatalf("LoadOpenClawManifest failed: %v", err)
	}
	if mf.ID != "oc.test" {
		t.Fatalf("expected id oc.test, got %q", mf.ID)
	}
}

func TestLoadOpenClawManifest_FallbackPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkg := `{
		"name":"@scope/plugin",
		"version":"1.2.3",
		"description":"desc",
		"main":"main.js",
		"openclaw":{"id":"claw.plugin","kind":["tool"],"entry":"entry.js"}
	}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	mf, err := LoadOpenClawManifest(dir)
	if err != nil {
		t.Fatalf("LoadOpenClawManifest fallback failed: %v", err)
	}
	if mf.ID != "claw.plugin" || mf.Entry != "entry.js" {
		t.Fatalf("unexpected manifest: %+v", mf)
	}
}

func TestLoadOpenClawManifest_EmptyPath(t *testing.T) {
	_, err := LoadOpenClawManifest("")
	if err == nil {
		t.Fatal("expected error")
	}
}
