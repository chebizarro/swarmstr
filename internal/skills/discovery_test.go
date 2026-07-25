package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

const alphaSkillDraft = `---
name: Alpha Sorter
description: Sorts alphabetic sequences quickly.
when_to_use: Use when ordering words.
---
# Alpha

Alpha instruction body.
`

const betaSkillDraft = `---
name: Beta Widget
description: Builds a beta widget dashboard.
---
# Beta

Beta instruction body.
`

func TestSearchSkillsRanksAndShapes(t *testing.T) {
	ws := isolatedWorkspace(t)
	writeWorkspaceSkill(t, ws, "alpha", alphaSkillDraft)
	writeWorkspaceSkill(t, ws, "beta", betaSkillDraft)
	cfg := state.ConfigDoc{}

	out, err := SearchSkills(cfg, "main", "alpha", 0)
	if err != nil {
		t.Fatalf("SearchSkills: %v", err)
	}
	results, _ := out["results"].([]map[string]any)
	if len(results) == 0 {
		t.Fatalf("expected alpha match, got %#v", out)
	}
	if results[0]["slug"] != "alpha" {
		t.Fatalf("expected alpha ranked first, got %#v", results[0])
	}
	if score, _ := results[0]["score"].(int); score <= 0 {
		t.Fatalf("expected positive score, got %#v", results[0]["score"])
	}
	for _, field := range []string{"slug", "displayName", "summary", "skillKey"} {
		if _, ok := results[0][field]; !ok {
			t.Fatalf("result missing %q: %#v", field, results[0])
		}
	}
	if results[0]["displayName"] != "Alpha Sorter" {
		t.Fatalf("unexpected displayName: %#v", results[0]["displayName"])
	}

	// A content-only match (description) still surfaces the right skill.
	widget, err := SearchSkills(cfg, "main", "widget", 5)
	if err != nil {
		t.Fatalf("SearchSkills widget: %v", err)
	}
	wResults, _ := widget["results"].([]map[string]any)
	if len(wResults) != 1 || wResults[0]["slug"] != "beta" {
		t.Fatalf("expected single beta match, got %#v", wResults)
	}

	// Empty query lists the full catalog.
	all, err := SearchSkills(cfg, "main", "", 5)
	if err != nil {
		t.Fatalf("SearchSkills empty: %v", err)
	}
	if all["count"] != 2 {
		t.Fatalf("expected 2 skills listed, got %#v", all["count"])
	}
}

func TestSkillDetailResolvesAndNotFound(t *testing.T) {
	ws := isolatedWorkspace(t)
	writeWorkspaceSkill(t, ws, "alpha", alphaSkillDraft)
	cfg := state.ConfigDoc{}

	detail, err := SkillDetail(cfg, "main", "alpha")
	if err != nil {
		t.Fatalf("SkillDetail: %v", err)
	}
	if detail["skillKey"] != "alpha" || detail["displayName"] != "Alpha Sorter" {
		t.Fatalf("unexpected detail: %#v", detail)
	}
	if detail["whenToUse"] != "Use when ordering words." {
		t.Fatalf("unexpected whenToUse: %#v", detail["whenToUse"])
	}
	if _, ok := detail["requirements"].(map[string]any); !ok {
		t.Fatalf("expected requirements map, got %#v", detail["requirements"])
	}

	if _, err := SkillDetail(cfg, "main", "missing-skill"); err != state.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBuildSecurityVerdictsAllowsCleanSkill(t *testing.T) {
	ws := isolatedWorkspace(t)
	writeWorkspaceSkill(t, ws, "alpha", alphaSkillDraft)
	cfg := state.ConfigDoc{}

	out, err := BuildSecurityVerdicts(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSecurityVerdicts: %v", err)
	}
	if out["schema_version"] != SecurityVerdictsSchema {
		t.Fatalf("unexpected schema: %#v", out["schema_version"])
	}
	if valid, _ := out["valid"].(bool); !valid {
		t.Fatalf("expected valid verdicts, got %#v", out)
	}
	items, _ := out["items"].([]map[string]any)
	if len(items) != 1 || items[0]["decision"] != "allow" {
		t.Fatalf("expected single allow verdict, got %#v", items)
	}
	if items[0]["skillKey"] != "alpha" {
		t.Fatalf("expected skillKey annotation, got %#v", items[0])
	}
}

func TestSecurityVerdictsFromPathsBlocksLintErrors(t *testing.T) {
	dir := t.TempDir()
	brokenDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	brokenFile := filepath.Join(brokenDir, "SKILL.md")
	// Missing description → lint error → block decision.
	if err := os.WriteFile(brokenFile, []byte("---\nname: broken\n---\n# Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := securityVerdictsFromPaths([]string{brokenFile}, nil)
	if valid, _ := out["valid"].(bool); valid {
		t.Fatalf("expected invalid verdicts, got %#v", out)
	}
	if blocked, _ := out["blockedCount"].(int); blocked != 1 {
		t.Fatalf("expected 1 blocked, got %#v", out["blockedCount"])
	}
	items, _ := out["items"].([]map[string]any)
	if len(items) != 1 || items[0]["decision"] != "block" {
		t.Fatalf("expected block verdict, got %#v", items)
	}
}

func TestBuildSkillCardReadsManifest(t *testing.T) {
	ws := isolatedWorkspace(t)
	writeWorkspaceSkill(t, ws, "alpha", alphaSkillDraft)
	cfg := state.ConfigDoc{}

	card, err := BuildSkillCard(cfg, "main", "alpha")
	if err != nil {
		t.Fatalf("BuildSkillCard: %v", err)
	}
	if card["skillKey"] != "alpha" {
		t.Fatalf("unexpected skillKey: %#v", card["skillKey"])
	}
	size, _ := card["sizeBytes"].(int64)
	if size != int64(len(alphaSkillDraft)) {
		t.Fatalf("unexpected sizeBytes: got %d want %d", size, len(alphaSkillDraft))
	}
	content, _ := card["content"].(string)
	if !strings.Contains(content, "Alpha instruction body.") {
		t.Fatalf("card content missing body: %#v", content)
	}
	if truncated, _ := card["truncated"].(bool); truncated {
		t.Fatalf("did not expect truncation for small manifest")
	}

	if _, err := BuildSkillCard(cfg, "main", "missing-skill"); err != state.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
