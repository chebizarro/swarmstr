package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSkillMD(t *testing.T, dir, skillName, content string) string {
	t.Helper()
	skillDir := filepath.Join(dir, skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ─── LoadSkillMD ─────────────────────────────────────────────────────────────

func TestLoadSkillMD_basic(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "weather", `---
name: weather
description: "Get current weather via wttr.in. No API key needed."
homepage: https://wttr.in/:help
metadata: { "openclaw": { "emoji": "🌤️", "requires": { "bins": ["curl"] } } }
---

# Weather Skill

Get weather for any location.
`)
	p := filepath.Join(dir, "weather", "SKILL.md")
	s, err := LoadSkillMD(p)
	if err != nil {
		t.Fatalf("LoadSkillMD: %v", err)
	}
	if s.SkillKey != "weather" {
		t.Errorf("skillKey: got %q want %q", s.SkillKey, "weather")
	}
	if s.Manifest.Name != "weather" {
		t.Errorf("name: %q", s.Manifest.Name)
	}
	if s.Manifest.Homepage != "https://wttr.in/:help" {
		t.Errorf("homepage: %q", s.Manifest.Homepage)
	}
	if s.Emoji() != "🌤️" {
		t.Errorf("emoji: %q", s.Emoji())
	}
	req := s.EffectiveRequirements()
	if len(req.Bins) != 1 || req.Bins[0] != "curl" {
		t.Errorf("requires.bins: %v", req.Bins)
	}
	if !strings.Contains(s.Manifest.Body, "Weather Skill") {
		t.Errorf("body missing heading: %q", s.Manifest.Body[:min(50, len(s.Manifest.Body))])
	}
}

func TestLoadSkillMD_withInstallSpecs(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "github", `---
name: github
description: "GitHub CLI operations."
metadata:
	{
	"openclaw":
		{
		"emoji": "🐙",
		"requires": { "bins": ["gh"] },
		"install":
			[
			{
				"id": "brew",
				"kind": "brew",
				"formula": "gh",
				"bins": ["gh"],
				"label": "Install GitHub CLI (brew)",
			},
			],
		},
	}
---

# GitHub
`)
	p := filepath.Join(dir, "github", "SKILL.md")
	s, err := LoadSkillMD(p)
	if err != nil {
		t.Fatalf("LoadSkillMD: %v", err)
	}
	specs := s.InstallSpecs()
	if len(specs) != 1 {
		t.Fatalf("install specs: got %d want 1", len(specs))
	}
	if specs[0].Kind != "brew" {
		t.Errorf("spec.kind: %q", specs[0].Kind)
	}
	if specs[0].Formula != "gh" {
		t.Errorf("spec.formula: %q", specs[0].Formula)
	}
}

func TestLoadSkillMD_osFilter(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "imsg", `---
name: imsg
description: "iMessage."
metadata: { "openclaw": { "emoji": "💬", "os": ["darwin"], "requires": { "bins": ["osascript"] } } }
---
# iMessage
`)
	s, err := LoadSkillMD(filepath.Join(dir, "imsg", "SKILL.md"))
	if err != nil {
		t.Fatalf("LoadSkillMD: %v", err)
	}
	req := s.EffectiveRequirements()
	found := false
	for _, os := range req.OS {
		if os == "darwin" {
			found = true
		}
	}
	if !found {
		t.Errorf("OS constraint 'darwin' not in effective requirements: %v", req.OS)
	}
}

func TestLoadSkillMD_noFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "bare", "# Bare Skill\nNo frontmatter here.\n")
	s, err := LoadSkillMD(filepath.Join(dir, "bare", "SKILL.md"))
	if err != nil {
		t.Fatalf("LoadSkillMD: %v", err)
	}
	if s.SkillKey != "bare" {
		t.Errorf("skillKey: %q", s.SkillKey)
	}
	if s.Manifest.Name != "bare" {
		t.Errorf("name default: %q", s.Manifest.Name)
	}
}

// ─── ScanBundledDir ───────────────────────────────────────────────────────────

func TestScanBundledDir_findsSkillMDs(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "weather", "---\nname: weather\ndescription: weather\n---\n# Weather\n")
	writeSkillMD(t, dir, "github", "---\nname: github\ndescription: github\n---\n# GitHub\n")
	// A stray file should be ignored.
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme"), 0o644)

	skills, err := ScanBundledDir(dir)
	if err != nil {
		t.Fatalf("ScanBundledDir: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills want 2", len(skills))
	}
	// Should be sorted by skillKey.
	if skills[0].SkillKey != "github" || skills[1].SkillKey != "weather" {
		t.Errorf("order: %v %v", skills[0].SkillKey, skills[1].SkillKey)
	}
	// Both should be marked bundled.
	for _, s := range skills {
		if !s.Bundled {
			t.Errorf("skill %q not marked Bundled", s.SkillKey)
		}
	}
}

func TestScanBundledDir_emptyDir(t *testing.T) {
	dir := t.TempDir()
	skills, err := ScanBundledDir(dir)
	if err != nil {
		t.Fatalf("ScanBundledDir empty: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("want 0 skills, got %d", len(skills))
	}
}

func TestScanBundledDir_missingDir(t *testing.T) {
	skills, err := ScanBundledDir("/nonexistent/path/skills")
	if err != nil {
		t.Fatalf("ScanBundledDir missing: %v", err)
	}
	if skills != nil {
		t.Errorf("want nil, got %v", skills)
	}
}

// ─── looksLikeBundledSkillsDir ────────────────────────────────────────────────

func TestLooksLikeBundledSkillsDir(t *testing.T) {
	dir := t.TempDir()
	// Empty dir.
	if looksLikeBundledSkillsDir(dir) {
		t.Error("empty dir should not look like bundled dir")
	}
	// Dir with SKILL.md subdir.
	writeSkillMD(t, dir, "weather", "# Weather\n")
	if !looksLikeBundledSkillsDir(dir) {
		t.Error("dir with SKILL.md subdir should look like bundled dir")
	}
}

// ─── ScanWorkspace with SKILL.md ─────────────────────────────────────────────

func TestScanWorkspace_findsSkillMDandYAML(t *testing.T) {
	dir := t.TempDir()
	// SKILL.md skill.
	writeSkillMD(t, dir, "tmux", "---\nname: tmux\ndescription: tmux skill\n---\n# Tmux\n")
	// Legacy YAML skill.
	writeYAML(t, dir, "python.yaml", "name: Python\ndescription: Python\n")

	skills, err := ScanWorkspace(dir)
	if err != nil {
		t.Fatalf("ScanWorkspace: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills want 2", len(skills))
	}
	keys := map[string]bool{}
	for _, s := range skills {
		keys[s.SkillKey] = true
	}
	if !keys["tmux"] || !keys["python"] {
		t.Errorf("unexpected skill keys: %v", keys)
	}
}

// ─── AggregateBins with SKILL.md ─────────────────────────────────────────────

func TestAggregateBins_includersSkillMDBins(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "github", `---
name: github
description: github
metadata: { "openclaw": { "emoji": "🐙", "requires": { "bins": ["gh"] }, "install": [{"id":"brew","kind":"brew","formula":"gh","bins":["gh"],"label":"brew"}] } }
---
# GitHub
`)
	skills, _ := ScanBundledDir(dir)
	bins := AggregateBins(skills)
	found := false
	for _, b := range bins {
		if b == "gh" {
			found = true
		}
	}
	if !found {
		t.Errorf("'gh' not in aggregated bins: %v", bins)
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
	req := Requirements{Bins: []string{"definitely-not-a-real-binary-xyz-metiq"}}
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
	req := Requirements{Env: []string{"METIQ_DEFINITELY_UNSET_XYZ"}}
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
	t.Setenv("METIQ_WORKSPACE", "/env/workspace")
	dir := WorkspaceDir(nil, "main")
	if dir != "/env/workspace" {
		t.Errorf("expected /env/workspace, got %q", dir)
	}
}

func TestWorkspaceDir_fallback(t *testing.T) {
	t.Setenv("METIQ_WORKSPACE", "")
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
