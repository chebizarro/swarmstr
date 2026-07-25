package skills

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

// isolatedWorkspace points the skills subsystem at empty temp dirs and returns
// the workspace directory used for curator/proposal state.
func isolatedWorkspace(t *testing.T) string {
	t.Helper()
	workspaceDir := t.TempDir()
	t.Setenv("METIQ_WORKSPACE", workspaceDir)
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", t.TempDir())
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", t.TempDir())
	InvalidateSkillCatalogCache()
	return workspaceDir
}

func writeWorkspaceSkill(t *testing.T, workspaceDir, key, content string) {
	t.Helper()
	dir := filepath.Join(workspaceDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateSkillCatalogCache()
}

const validSkillDraft = `---
name: newskill
description: A test skill used by the proposal store tests.
---
# Newskill

Instruction body content.
`

func TestDeriveCuratorStatus(t *testing.T) {
	const now int64 = 1_000_000_000_000
	cases := []struct {
		name        string
		state       curatorSkillState
		wantStatus  string
		wantArchive bool
	}{
		{"pinned-overrides-idle", curatorSkillState{Pinned: true, FirstSeenAtMs: 1}, "active", false},
		{"explicit-archived", curatorSkillState{Archived: true}, "archived", false},
		{"fresh-first-seen", curatorSkillState{FirstSeenAtMs: now}, "active", false},
		{"stale-idle", curatorSkillState{FirstSeenAtMs: now - CuratorStaleAfterMs - 1}, "stale", false},
		{"archive-idle", curatorSkillState{FirstSeenAtMs: now - CuratorArchiveAfterMs - 1}, "archived", true},
		{"never-seen-active", curatorSkillState{}, "active", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, archive := deriveCuratorStatus(tc.state, now)
			if status != tc.wantStatus || archive != tc.wantArchive {
				t.Fatalf("got status=%q archive=%v; want status=%q archive=%v", status, archive, tc.wantStatus, tc.wantArchive)
			}
		})
	}
}

func TestCuratorStatusAndPinLifecycle(t *testing.T) {
	isolatedWorkspace(t)
	writeWorkspaceSkill(t, os.Getenv("METIQ_WORKSPACE"), "alpha", validSkillDraft)
	cfg := state.ConfigDoc{}

	report, err := BuildCuratorStatus(cfg, "main")
	if err != nil {
		t.Fatalf("BuildCuratorStatus: %v", err)
	}
	skillsList, _ := report["skills"].([]map[string]any)
	if len(skillsList) != 1 || skillsList[0]["skill"] != "alpha" {
		t.Fatalf("expected single alpha skill, got %#v", skillsList)
	}
	if skillsList[0]["status"] != "active" {
		t.Fatalf("expected freshly-seen skill active, got %v", skillsList[0]["status"])
	}
	if _, ok := report["overlaps"].([]any); !ok {
		t.Fatalf("expected overlaps list, got %#v", report["overlaps"])
	}
	counts, _ := report["counts"].(map[string]any)
	if counts["active"] != 1 {
		t.Fatalf("expected 1 active, got %#v", counts)
	}

	entry, err := SetCuratorPin(cfg, "main", "alpha", true)
	if err != nil {
		t.Fatalf("SetCuratorPin: %v", err)
	}
	if entry["pinned"] != true || entry["status"] != "active" {
		t.Fatalf("expected pinned active entry, got %#v", entry)
	}

	if _, err := SetCuratorPin(cfg, "main", "does-not-exist", true); err == nil {
		t.Fatal("expected error pinning unknown skill")
	}

	restored, err := RestoreCuratorSkill(cfg, "main", "alpha")
	if err != nil {
		t.Fatalf("RestoreCuratorSkill: %v", err)
	}
	if restored["archived"] != false {
		t.Fatalf("expected restored skill not archived, got %#v", restored)
	}
}

func TestProposalCreateScanApply(t *testing.T) {
	workspaceDir := isolatedWorkspace(t)
	cfg := state.ConfigDoc{}
	store := NewProposalStore(cfg, "main")

	listed, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if listed["schema"] != ProposalSchema {
		t.Fatalf("unexpected manifest schema: %#v", listed)
	}

	rec, err := store.Create(ProposalDraftInput{
		Title:     "Add newskill",
		Content:   validSkillDraft,
		SkillName: "newskill",
		SupportFiles: []ProposalFileInput{
			{Path: "reference/notes.md", Content: "notes"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Kind != "create" || rec.Status != "pending" {
		t.Fatalf("unexpected record kind/status: %#v", rec)
	}
	if rec.Scan.State != "clean" {
		t.Fatalf("expected clean scan, got %#v", rec.Scan)
	}
	if rec.Schema != ProposalSchema || rec.DraftHash == "" {
		t.Fatalf("unexpected record metadata: %#v", rec)
	}

	inspected, err := store.Inspect(rec.ID)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if inspected["content"] != validSkillDraft {
		t.Fatalf("inspect content mismatch: %#v", inspected["content"])
	}

	applied, err := store.Apply(rec.ID)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	targetFile, _ := applied["targetSkillFile"].(string)
	if targetFile == "" {
		t.Fatalf("expected target skill file, got %#v", applied)
	}
	if _, statErr := os.Stat(targetFile); statErr != nil {
		t.Fatalf("expected applied SKILL.md on disk: %v", statErr)
	}
	supportOut := filepath.Join(workspaceDir, "newskill", "reference", "notes.md")
	if _, statErr := os.Stat(supportOut); statErr != nil {
		t.Fatalf("expected applied support file on disk: %v", statErr)
	}

	if _, err := store.Revise(rec.ID, ProposalDraftInput{Content: validSkillDraft}); err == nil {
		t.Fatal("expected error revising an applied proposal")
	}
}

func TestProposalFailedScanBlocksApply(t *testing.T) {
	isolatedWorkspace(t)
	cfg := state.ConfigDoc{}
	store := NewProposalStore(cfg, "main")

	rec, err := store.Create(ProposalDraftInput{
		Title:     "Broken skill",
		Content:   "---\nname: broken\n---\n# Broken\n", // missing description
		SkillName: "broken",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Scan.State != "failed" || rec.Scan.Counts.Error == 0 {
		t.Fatalf("expected failed scan with errors, got %#v", rec.Scan)
	}
	if _, err := store.Apply(rec.ID); err == nil {
		t.Fatal("expected apply to fail on unclean scan")
	}

	quar, err := store.Quarantine(rec.ID, "unsafe draft")
	if err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	if quar.Status != "quarantined" || quar.Reason != "unsafe draft" {
		t.Fatalf("unexpected quarantine result: %#v", quar)
	}

	if _, err := store.Inspect("prop_missing"); err == nil {
		t.Fatal("expected not-found error for unknown proposal")
	}
}
