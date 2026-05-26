package qa

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMarkdownScenario(t *testing.T) {
	_, err := ParseMarkdown("---\ncoverage_id: QA-1\ntitle: Test\nparity_tier: P1\nlane: deterministic\n---\n## Steps\n- inspect\n## Expected\n- pass\n")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunChecks(t *testing.T) {
	repo := t.TempDir()
	scenDir := filepath.Join(repo, "qa/scenarios/nostr")
	_ = os.MkdirAll(scenDir, 0o755)
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello nostr"), 0o644)
	sc := "---\ncoverage_id: QA-1\ntitle: Test\nparity_tier: P1\nlane: deterministic\nchecks:\n  - type: file_exists\n    path: README.md\n  - type: grep\n    path: README.md\n    pattern: nostr\n    must_find: true\n---\n## Steps\n- inspect\n## Expected\n- pass\n"
	_ = os.WriteFile(filepath.Join(scenDir, "readme.md"), []byte(sc), 0o644)
	report, err := Run(filepath.Join(repo, "qa/scenarios"), repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("bad report: %+v", report)
	}
}

func TestRepositoryScenariosAreRunnable(t *testing.T) {
	repo := findRepoRoot(t)
	report, err := Run(filepath.Join(repo, "qa/scenarios"), repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 0 || report.Passed < 7 {
		t.Fatalf("repository scenarios should pass, report: %+v", report)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	t.Fatal("repo root with go.mod not found")
	return ""
}
