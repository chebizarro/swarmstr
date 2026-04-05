package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncPromptSkillsToWorkspaceUsesCollisionResistantMirrorPaths(t *testing.T) {
	workspaceDir := t.TempDir()
	externalOne := t.TempDir()
	externalTwo := t.TempDir()
	pathOne := writeSkillMD(t, externalOne, "A.B", `---
name: A.B
description: first
---
# First
`)
	pathTwo := writeSkillMD(t, externalTwo, "a b", `---
name: a b
description: second
---
# Second
`)
	catalog := &SkillCatalog{
		WorkspaceDir: workspaceDir,
		Skills: []*ResolvedSkill{
			{
				Skill: &Skill{
					SkillKey: "A.B",
					FilePath: pathOne,
					BaseDir:  filepath.Dir(pathOne),
				},
				PromptEligible: true,
			},
			{
				Skill: &Skill{
					SkillKey: "a b",
					FilePath: pathTwo,
					BaseDir:  filepath.Dir(pathTwo),
				},
				PromptEligible: true,
			},
		},
	}

	mirror, err := SyncPromptSkillsToWorkspace(catalog)
	if err != nil {
		t.Fatalf("SyncPromptSkillsToWorkspace: %v", err)
	}
	if mirror["A.B"] == "" || mirror["a b"] == "" {
		t.Fatalf("expected mirrored paths for both skills: %#v", mirror)
	}
	if mirror["A.B"] == mirror["a b"] {
		t.Fatalf("expected distinct mirror paths, got %#v", mirror)
	}
	for _, mirrored := range []string{mirror["A.B"], mirror["a b"]} {
		if !strings.HasPrefix(mirrored, filepath.Join(workspaceDir, ".metiq", "skills")+string(os.PathSeparator)) {
			t.Fatalf("expected mirrored path under workspace, got %q", mirrored)
		}
	}
	firstBody, err := os.ReadFile(mirror["A.B"])
	if err != nil {
		t.Fatalf("read first mirrored skill: %v", err)
	}
	secondBody, err := os.ReadFile(mirror["a b"])
	if err != nil {
		t.Fatalf("read second mirrored skill: %v", err)
	}
	if strings.Contains(string(firstBody), "# Second") || strings.Contains(string(secondBody), "# First") {
		t.Fatalf("expected mirrored content to stay isolated: first=%q second=%q", string(firstBody), string(secondBody))
	}
}
