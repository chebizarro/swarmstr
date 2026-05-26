package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordAndVerifyPluginIntegrity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("module.exports = {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := RecordPluginIntegrity(dir)
	if err != nil {
		t.Fatalf("RecordPluginIntegrity: %v", err)
	}
	if rec.Hash == "" || rec.FileCount != 1 {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if err := VerifyPluginIntegrity(dir); err != nil {
		t.Fatalf("VerifyPluginIntegrity: %v", err)
	}
}

func TestVerifyPluginIntegrityDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.js")
	if err := os.WriteFile(path, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordPluginIntegrity(dir); err != nil {
		t.Fatalf("RecordPluginIntegrity: %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifyPluginIntegrity(dir)
	if err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatalf("expected integrity mismatch, got %v", err)
	}
}
