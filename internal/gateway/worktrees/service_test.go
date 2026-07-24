package worktrees

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func TestServiceCreateListRemoveRestore(t *testing.T) {
	gitAvailable(t)
	repo := initRepo(t)
	svc := NewService(filepath.Join(t.TempDir(), "wt"))
	ctx := context.Background()

	rec, err := svc.Create(ctx, CreateParams{RepoRoot: repo, Name: "feature-x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.Name != "feature-x" || rec.Branch != "worktree/feature-x" {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if _, err := os.Stat(rec.Path); err != nil {
		t.Fatalf("worktree path missing: %v", err)
	}

	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	// Duplicate name rejected.
	if _, err := svc.Create(ctx, CreateParams{RepoRoot: repo, Name: "feature-x"}); err == nil {
		t.Fatal("expected duplicate name error")
	}

	rm, err := svc.Remove(ctx, rec.ID, false)
	if err != nil || !rm.Removed {
		t.Fatalf("remove: %v %+v", err, rm)
	}
	list, _ = svc.List(ctx)
	if len(list) != 0 {
		t.Fatalf("expected empty list after remove, got %d", len(list))
	}

	restored, err := svc.Restore(ctx, rec.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(restored.Path); err != nil {
		t.Fatalf("restored path missing: %v", err)
	}
}

func TestServiceBranches(t *testing.T) {
	gitAvailable(t)
	repo := initRepo(t)
	svc := NewService(filepath.Join(t.TempDir(), "wt"))
	listing, err := svc.Branches(context.Background(), repo, true)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	if listing.RepositoryStatus != "git" {
		t.Fatalf("expected git status, got %q", listing.RepositoryStatus)
	}
	found := false
	for _, b := range listing.Branches {
		if b.Name == "main" && b.Kind == "local" {
			found = true
		}
	}
	if !found {
		t.Fatalf("main branch not listed: %+v", listing.Branches)
	}
}

func TestServiceBranchesNonGit(t *testing.T) {
	gitAvailable(t)
	svc := NewService(filepath.Join(t.TempDir(), "wt"))
	dir := t.TempDir()
	listing, err := svc.Branches(context.Background(), dir, true)
	if err != nil {
		t.Fatalf("branches non-git with status must not error: %v", err)
	}
	if listing.RepositoryStatus != "not_git" {
		t.Fatalf("expected not_git status, got %q", listing.RepositoryStatus)
	}
}

func TestServiceGcDropsRemovedRecords(t *testing.T) {
	gitAvailable(t)
	repo := initRepo(t)
	svc := NewService(filepath.Join(t.TempDir(), "wt"))
	ctx := context.Background()
	rec, err := svc.Create(ctx, CreateParams{RepoRoot: repo, Name: "gc-me"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Remove(ctx, rec.ID, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	res, err := svc.Gc(ctx)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if res.OrphansDeleted == 0 {
		t.Fatalf("expected orphan deletion, got %+v", res)
	}
}
