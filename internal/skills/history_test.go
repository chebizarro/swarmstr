package skills

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.email=test@example.com",
		"-c", "user.name=Test",
		"-c", "commit.gpgsign=false",
	}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeSkill(t *testing.T, dir, key, body string) {
	t.Helper()
	sd := filepath.Join(dir, key)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupSkillHistoryRepo builds a temp git workspace with a 3-commit skill history
// and returns the workspace dir + the ordered commit shas (c1 oldest, c3 newest).
func setupSkillHistoryRepo(t *testing.T) (string, string, string, string) {
	t.Helper()
	ws := t.TempDir()
	gitRun(t, ws, "init", "-q")

	writeSkill(t, ws, "foo", "# foo v1\n")
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "add foo")
	c1 := gitRun(t, ws, "rev-parse", "HEAD")

	writeSkill(t, ws, "foo", "# foo v2\n")
	writeSkill(t, ws, "bar", "# bar v1\n")
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "modify foo, add bar")
	c2 := gitRun(t, ws, "rev-parse", "HEAD")

	if err := os.Remove(filepath.Join(ws, "bar", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "delete bar")
	c3 := gitRun(t, ws, "rev-parse", "HEAD")

	t.Setenv("METIQ_WORKSPACE", ws)
	return ws, c1, c2, c3
}

func cfgForWorkspace() state.ConfigDoc { return state.ConfigDoc{} }

func TestHistoryStatus_ReportsRepoSummary(t *testing.T) {
	ws, _, _, c3 := setupSkillHistoryRepo(t)
	status, err := HistoryStatus(context.Background(), cfgForWorkspace(), "main")
	if err != nil {
		t.Fatalf("HistoryStatus: %v", err)
	}
	if !status.Available {
		t.Fatalf("expected available, got reason=%q", status.Reason)
	}
	if status.Head != c3 {
		t.Fatalf("head=%q want %q", status.Head, c3)
	}
	if status.Dirty {
		t.Fatalf("expected clean tree")
	}
	if status.SkillFileCount != 1 { // only foo/SKILL.md remains
		t.Fatalf("skillFileCount=%d want 1", status.SkillFileCount)
	}
	if status.SkillChangeCommits != 3 {
		t.Fatalf("skillChangeCommits=%d want 3", status.SkillChangeCommits)
	}
	_ = ws
}

func TestHistoryStatus_NotAGitRepo(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("METIQ_WORKSPACE", ws)
	status, err := HistoryStatus(context.Background(), cfgForWorkspace(), "main")
	if err != nil {
		t.Fatalf("HistoryStatus: %v", err)
	}
	if status.Available {
		t.Fatalf("expected unavailable for non-git workspace")
	}
	if status.Reason == "" {
		t.Fatalf("expected a reason")
	}
}

func changeFor(entries []SkillHistoryEntry, skillKey string) (SkillHistoryEntry, bool) {
	for _, e := range entries {
		if e.SkillKey == skillKey {
			return e, true
		}
	}
	return SkillHistoryEntry{}, false
}

func TestHistoryScan_OlderPagingAndCursor(t *testing.T) {
	_, c1, c2, c3 := setupSkillHistoryRepo(t)

	page1, err := HistoryScan(context.Background(), cfgForWorkspace(), "main", SkillHistoryScanParams{Direction: "older", Limit: 2})
	if err != nil {
		t.Fatalf("scan page1: %v", err)
	}
	if !page1.Available {
		t.Fatalf("expected available")
	}
	if !page1.HasMore {
		t.Fatalf("expected hasMore (c1 not yet returned)")
	}
	if page1.NextCursor != c2 {
		t.Fatalf("nextCursor=%q want c2=%q", page1.NextCursor, c2)
	}
	// c3 deleted bar.
	if e, ok := changeFor(page1.Entries, "bar"); !ok || e.Change != "deleted" || e.Commit != c3 {
		t.Fatalf("expected bar deleted at c3, got %+v ok=%v", e, ok)
	}
	// c2 modified foo + added bar.
	if e, ok := changeFor(page1.Entries, "foo"); !ok || e.Change != "modified" || e.Commit != c2 {
		t.Fatalf("expected foo modified at c2, got %+v ok=%v", e, ok)
	}

	page2, err := HistoryScan(context.Background(), cfgForWorkspace(), "main", SkillHistoryScanParams{Direction: "older", Cursor: page1.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("scan page2: %v", err)
	}
	if page2.HasMore {
		t.Fatalf("expected no more after c1")
	}
	if e, ok := changeFor(page2.Entries, "foo"); !ok || e.Change != "added" || e.Commit != c1 {
		t.Fatalf("expected foo added at c1, got %+v ok=%v", e, ok)
	}
}

func TestHistoryScan_NewerPaging(t *testing.T) {
	_, c1, c2, c3 := setupSkillHistoryRepo(t)
	page, err := HistoryScan(context.Background(), cfgForWorkspace(), "main", SkillHistoryScanParams{Direction: "newer", Cursor: c1, Limit: 10})
	if err != nil {
		t.Fatalf("scan newer: %v", err)
	}
	// Newer than c1 => c2 then c3, oldest-first.
	if len(page.Entries) == 0 {
		t.Fatalf("expected entries newer than c1")
	}
	first := page.Entries[0]
	if first.Commit != c2 {
		t.Fatalf("first newer entry commit=%q want c2=%q", first.Commit, c2)
	}
	last := page.Entries[len(page.Entries)-1]
	if last.Commit != c3 {
		t.Fatalf("last newer entry commit=%q want c3=%q", last.Commit, c3)
	}
}

func TestHistoryScan_RejectsBadInput(t *testing.T) {
	setupSkillHistoryRepo(t)
	if _, err := HistoryScan(context.Background(), cfgForWorkspace(), "main", SkillHistoryScanParams{Cursor: "not-a-sha; rm -rf /"}); err == nil {
		t.Fatalf("expected invalid cursor rejection")
	}
	if _, err := HistoryScan(context.Background(), cfgForWorkspace(), "main", SkillHistoryScanParams{Direction: "sideways"}); err == nil {
		t.Fatalf("expected invalid direction rejection")
	}
}

func TestHistoryScan_RenameAcrossSkillKeys(t *testing.T) {
	ws := t.TempDir()
	gitRun(t, ws, "init", "-q")
	writeSkill(t, ws, "foo", "# foo skill\nsome stable body line\nanother stable line\n")
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "add foo")

	// Move foo/SKILL.md -> qux/SKILL.md (rename across skill keys).
	if err := os.MkdirAll(filepath.Join(ws, "qux"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, ws, "mv", filepath.Join("foo", "SKILL.md"), filepath.Join("qux", "SKILL.md"))
	gitRun(t, ws, "commit", "-q", "-m", "rename foo -> qux")
	renameCommit := gitRun(t, ws, "rev-parse", "HEAD")
	t.Setenv("METIQ_WORKSPACE", ws)

	page, err := HistoryScan(context.Background(), cfgForWorkspace(), "main", SkillHistoryScanParams{Direction: "older", Limit: 1})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// The rename commit must record BOTH the new key (renamed/added) and the old
	// key (deleted) so either skill's history reflects the move.
	newEntry, okNew := changeFor(page.Entries, "qux")
	if !okNew || newEntry.Commit != renameCommit {
		t.Fatalf("missing new-key entry: %+v ok=%v", newEntry, okNew)
	}
	if newEntry.Change != "renamed" && newEntry.Change != "added" {
		t.Fatalf("new-key change kind=%q want renamed/added", newEntry.Change)
	}
	oldEntry, okOld := changeFor(page.Entries, "foo")
	if !okOld || oldEntry.Change != "deleted" {
		t.Fatalf("expected foo deleted on rename, got %+v ok=%v", oldEntry, okOld)
	}
}
