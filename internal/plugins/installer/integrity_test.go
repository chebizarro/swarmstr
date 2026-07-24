package installer

import (
	"encoding/json"
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

func TestRecordPluginIntegrityWithProvenance(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provenance := &InstallProvenance{
		SourceURL: "https://plugins.example/p.zip", FinalURL: "https://cdn.example/p.zip",
		ResolvedHosts: []ResolvedHost{{Host: "plugins.example", IPs: []string{"93.184.216.34"}}},
		Artifact:      ArtifactDigest{Algorithm: "sha256", Hash: strings.Repeat("a", 64), SizeBytes: 12},
	}
	if _, err := RecordPluginIntegrityWithProvenance(dir, provenance); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, PluginIntegrityFile))
	if err != nil {
		t.Fatal(err)
	}
	var record PluginIntegrityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Provenance == nil || record.Provenance.SourceURL != provenance.SourceURL {
		t.Fatalf("provenance = %+v", record.Provenance)
	}
	if info, err := os.Stat(filepath.Join(dir, PluginIntegrityFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("sidecar mode = %v, err=%v", info.Mode().Perm(), err)
	}
	if err := VerifyPluginIntegrity(dir); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPluginIntegrityPolicyRequiresProvenance(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := IntegrityPolicy{RequireRecord: true, RequireProvenance: true}
	if err := VerifyPluginIntegrityWithPolicy(dir, policy); err == nil || !strings.Contains(err.Error(), "record is required") {
		t.Fatalf("missing record accepted: %v", err)
	}
	if _, err := RecordPluginIntegrity(dir); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPluginIntegrityWithPolicy(dir, policy); err == nil || !strings.Contains(err.Error(), "provenance is required") {
		t.Fatalf("missing provenance accepted: %v", err)
	}
	if _, err := RecordPluginIntegrityWithProvenance(dir, &InstallProvenance{SourceType: "npm", SourceRef: "demo@1", ResolvedRef: "demo@1"}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPluginIntegrityWithPolicy(dir, policy); err != nil {
		t.Fatalf("valid provenance rejected: %v", err)
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
