package main

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

func TestConfigWithSkillEntriesPreservesSkillSiblings(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{
		"skills": map[string]any{
			"workspace":     "/tmp/workspace",
			"allow_bundled": []any{"keep-me"},
			"prompt":        map[string]any{"max_count": 10},
			"entries":       map[string]any{"existing": map[string]any{"enabled": true}},
		},
	}}
	next := configWithSkillEntries(cfg, map[string]map[string]any{"new": {"enabled": true}})
	rawSkills, _ := next.Extra["skills"].(map[string]any)
	if rawSkills["workspace"] != "/tmp/workspace" {
		t.Fatalf("expected workspace sibling to be preserved: %#v", rawSkills)
	}
	if _, ok := rawSkills["allow_bundled"]; !ok {
		t.Fatalf("expected allow_bundled sibling to be preserved: %#v", rawSkills)
	}
	if _, ok := rawSkills["prompt"]; !ok {
		t.Fatalf("expected prompt sibling to be preserved: %#v", rawSkills)
	}
}

func TestBuildSkillsStatusReportUsesMergedWorkspacePrecedence(t *testing.T) {
	bundledDir := t.TempDir()
	workspaceDir := t.TempDir()
	managedDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", managedDir)
	t.Setenv("METIQ_WORKSPACE", workspaceDir)

	writeSkill := func(root, name, content string) {
		t.Helper()
		skillDir := filepath.Join(root, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(bundledDir, "dup", `---
name: dup
description: bundled desc
---
# Bundled
`)
	writeSkill(workspaceDir, "dup", `---
name: dup
description: workspace desc
metadata:
  openclaw:
    requires:
      env: ["SKILL_STATUS_TOKEN"]
    primaryEnv: SKILL_STATUS_TOKEN
---
# Workspace
`)

	cfg := state.ConfigDoc{Extra: map[string]any{"skills": map[string]any{"entries": map[string]any{"dup": map[string]any{"api_key": "token"}}}}}
	status := buildSkillsStatusReport(cfg, "main")
	skills, _ := status["skills"].([]map[string]any)
	if len(skills) != 1 {
		t.Fatalf("expected one resolved skill, got %#v", skills)
	}
	entry := skills[0]
	if entry["source"] != "metiq-workspace" || entry["description"] != "workspace desc" {
		t.Fatalf("expected workspace skill to win precedence: %#v", entry)
	}
	if entry["id"] != "dup" || entry["status"] != "ready" || entry["primaryEnv"] != "SKILL_STATUS_TOKEN" {
		t.Fatalf("expected enriched status fields: %#v", entry)
	}
}

func TestBuildSkillsStatusReportSelectsPreferredInstallID(t *testing.T) {
	bundledDir := t.TempDir()
	workspaceDir := t.TempDir()
	managedDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", managedDir)
	t.Setenv("METIQ_WORKSPACE", workspaceDir)

	writeSkill := func(root, name, content string) {
		t.Helper()
		skillDir := filepath.Join(root, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(workspaceDir, "installable", `---
name: installable
description: install preference test
metadata:
  openclaw:
    install:
      - id: brew
        kind: brew
        formula: installable
      - id: go
        kind: go
        module: example.com/installable
---
# Installable
`)

	status := buildSkillsStatusReport(state.ConfigDoc{}, "main")
	skills, _ := status["skills"].([]map[string]any)
	if got := skills[0]["selectedInstallId"]; got != "brew" {
		t.Fatalf("expected brew to be selected by default, got %#v", got)
	}

	cfg := state.ConfigDoc{Extra: map[string]any{"skills": map[string]any{"install": map[string]any{"prefer_brew": false}}}}
	status = buildSkillsStatusReport(cfg, "main")
	skills, _ = status["skills"].([]map[string]any)
	if got := skills[0]["selectedInstallId"]; got != "go" {
		t.Fatalf("expected non-brew install to be selected when prefer_brew=false, got %#v", got)
	}
}
