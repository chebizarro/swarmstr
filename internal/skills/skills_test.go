package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ─── LoadManifest ─────────────────────────────────────────────────────────────

func TestLoadManifest_basic(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "python.yaml", `
name: Python
description: Python skill
requirements:
  bins:
    - python3
    - pip3
bins:
  - python3
`)
	skill, err := LoadManifest(filepath.Join(dir, "python.yaml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if skill.Manifest.Name != "Python" {
		t.Errorf("name: %q", skill.Manifest.Name)
	}
	if skill.SkillKey != "python" {
		t.Errorf("skillKey: %q", skill.SkillKey)
	}
	if skill.BaseDir != dir {
		t.Errorf("baseDir: %q", skill.BaseDir)
	}
}

func TestLoadManifest_nameDefaultsToFileName(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "my-tool.yaml", `description: no name field`)
	skill, err := LoadManifest(filepath.Join(dir, "my-tool.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if skill.Manifest.Name != "my-tool" {
		t.Errorf("default name: %q", skill.Manifest.Name)
	}
}

func TestLoadManifest_missingFile(t *testing.T) {
	_, err := LoadManifest("/nonexistent/skill.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ─── ScanWorkspace ────────────────────────────────────────────────────────────

func TestScanWorkspace_basic(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "git.yaml", "name: Git\ndescription: git skill\nbins:\n  - git\n")
	writeYAML(t, dir, "node.yml", "name: Node\ndescription: node skill\nbins:\n  - node\n")
	// Non-YAML file should be skipped.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := ScanWorkspace(dir)
	if err != nil {
		t.Fatalf("ScanWorkspace: %v", err)
	}
	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}
	// Should be sorted by skillKey.
	if skills[0].SkillKey != "git" {
		t.Errorf("first skill: %q", skills[0].SkillKey)
	}
}

func TestScanWorkspace_emptyDir(t *testing.T) {
	dir := t.TempDir()
	skills, err := ScanWorkspace(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills in empty dir, got %d", len(skills))
	}
}

func TestScanWorkspace_nonexistentDir(t *testing.T) {
	skills, err := ScanWorkspace("/nonexistent/workspace/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected nil/empty for missing dir, got %d", len(skills))
	}
}

func TestScanWorkspace_skipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, ".hidden")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeYAML(t, hiddenDir, "hidden.yaml", `name: Hidden`)
	writeYAML(t, dir, "visible.yaml", `name: Visible`)

	skills, err := ScanWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Manifest.Name != "Visible" {
		t.Errorf("expected 1 visible skill, got %d: %v", len(skills), skills)
	}
}

func TestScanWorkspace_skipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	nmDir := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeYAML(t, nmDir, "npm-skill.yaml", `name: NPM`)
	writeYAML(t, dir, "real.yaml", `name: Real`)

	skills, err := ScanWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Errorf("node_modules should be skipped, got %d skills", len(skills))
	}
}

// ─── CheckRequirements ────────────────────────────────────────────────────────

func TestCheckRequirements_satisfied(t *testing.T) {
	// "sh" is virtually guaranteed to exist everywhere.
	req := Requirements{Bins: []string{"sh"}}
	missing, eligible := CheckRequirements(req)
	if !eligible {
		t.Errorf("sh should satisfy bins requirement; missing: %v", missing.Bins)
	}
}

func TestCheckRequirements_missingBin(t *testing.T) {
	req := Requirements{Bins: []string{"definitely-not-a-real-binary-xyz-swarmstr"}}
	missing, eligible := CheckRequirements(req)
	if eligible {
		t.Error("expected ineligible for missing binary")
	}
	if len(missing.Bins) != 1 {
		t.Errorf("missing bins: %v", missing.Bins)
	}
}

func TestCheckRequirements_anyBinsOK(t *testing.T) {
	req := Requirements{AnyBins: []string{"definitely-not-a-real-binary-xyz", "sh"}}
	_, eligible := CheckRequirements(req)
	if !eligible {
		t.Error("expected eligible: sh satisfies anyBins")
	}
}

func TestCheckRequirements_anyBinsAllMissing(t *testing.T) {
	req := Requirements{AnyBins: []string{"bin-a-xyz", "bin-b-xyz"}}
	missing, eligible := CheckRequirements(req)
	if eligible {
		t.Error("expected ineligible")
	}
	if len(missing.AnyBins) != 2 {
		t.Errorf("missing anyBins: %v", missing.AnyBins)
	}
}

func TestCheckRequirements_missingEnv(t *testing.T) {
	// Use an env var we're sure is not set.
	req := Requirements{Env: []string{"SWARMSTR_DEFINITELY_UNSET_XYZ"}}
	missing, eligible := CheckRequirements(req)
	if eligible {
		t.Error("expected ineligible for missing env")
	}
	if len(missing.Env) != 1 {
		t.Errorf("missing env: %v", missing.Env)
	}
}

// ─── WorkspaceDir ─────────────────────────────────────────────────────────────

func TestWorkspaceDir_fromExtra(t *testing.T) {
	extra := map[string]any{
		"skills": map[string]any{"workspace": "/custom/workspace"},
	}
	dir := WorkspaceDir(extra, "main")
	if dir != "/custom/workspace" {
		t.Errorf("expected /custom/workspace, got %q", dir)
	}
}

func TestWorkspaceDir_fromEnv(t *testing.T) {
	t.Setenv("SWARMSTR_WORKSPACE", "/env/workspace")
	dir := WorkspaceDir(nil, "main")
	if dir != "/env/workspace" {
		t.Errorf("expected /env/workspace, got %q", dir)
	}
}

func TestWorkspaceDir_fallback(t *testing.T) {
	t.Setenv("SWARMSTR_WORKSPACE", "")
	dir := WorkspaceDir(nil, "myagent")
	if dir == "" {
		t.Error("expected non-empty fallback dir")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("expected absolute path, got %q", dir)
	}
}

// ─── AggregateBins ────────────────────────────────────────────────────────────

func TestAggregateBins(t *testing.T) {
	skills := []*Skill{
		{Manifest: Manifest{Bins: []string{"git"}, Requirements: Requirements{Bins: []string{"python3"}}}},
		{Manifest: Manifest{Bins: []string{"git", "node"}}},
	}
	bins := AggregateBins(skills)
	seen := map[string]bool{}
	for _, b := range bins {
		seen[b] = true
	}
	for _, expected := range []string{"git", "node", "python3"} {
		if !seen[expected] {
			t.Errorf("expected %q in aggregated bins %v", expected, bins)
		}
	}
	// No duplicates.
	if len(bins) != 3 {
		t.Errorf("expected 3 unique bins, got %d: %v", len(bins), bins)
	}
}
