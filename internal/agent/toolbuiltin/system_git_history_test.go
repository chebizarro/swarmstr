package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a temp git repo with a few commits for testing.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test User"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init setup %v: %v — %s", args, err, out)
		}
	}

	// Commit 1.
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v — %s", args, err, out)
		}
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "Initial commit: add main.go")

	// Commit 2.
	os.WriteFile(filepath.Join(dir, "util.go"), []byte("package main\n\nfunc helper() {}\n"), 0644)
	run("git", "add", ".")
	run("git", "commit", "-m", "Add util.go with helper function")

	// Commit 3.
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)
	run("git", "add", ".")
	run("git", "commit", "-m", "Update main to print hello")

	return dir
}

func TestGitLogTool_BasicLog(t *testing.T) {
	dir := initTestRepo(t)

	result, err := GitLogTool(context.Background(), map[string]any{
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res gitLogResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.Total != 3 {
		t.Errorf("expected 3 commits, got %d", res.Total)
	}
	// Most recent first.
	if res.Commits[0].Subject != "Update main to print hello" {
		t.Errorf("unexpected first commit subject: %s", res.Commits[0].Subject)
	}
	for _, c := range res.Commits {
		if c.Hash == "" {
			t.Error("commit missing hash")
		}
		if c.Author == "" {
			t.Error("commit missing author")
		}
	}
}

func TestGitLogTool_MaxCount(t *testing.T) {
	dir := initTestRepo(t)

	result, err := GitLogTool(context.Background(), map[string]any{
		"directory": dir,
		"max_count": 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res gitLogResult
	json.Unmarshal([]byte(result), &res)
	if res.Total != 1 {
		t.Errorf("expected 1 commit with max_count=1, got %d", res.Total)
	}
}

func TestGitLogTool_FileFilter(t *testing.T) {
	dir := initTestRepo(t)

	result, err := GitLogTool(context.Background(), map[string]any{
		"directory": dir,
		"file":      "util.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res gitLogResult
	json.Unmarshal([]byte(result), &res)
	if res.Total != 1 {
		t.Errorf("expected 1 commit touching util.go, got %d", res.Total)
	}
}

func TestGitLogTool_Grep(t *testing.T) {
	dir := initTestRepo(t)

	result, err := GitLogTool(context.Background(), map[string]any{
		"directory": dir,
		"grep":      "helper",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res gitLogResult
	json.Unmarshal([]byte(result), &res)
	if res.Total != 1 {
		t.Errorf("expected 1 commit matching 'helper', got %d", res.Total)
	}
}

func TestGitBlameTool_Basic(t *testing.T) {
	dir := initTestRepo(t)

	result, err := GitBlameTool(context.Background(), map[string]any{
		"directory": dir,
		"file":      "main.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res gitBlameResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.File != "main.go" {
		t.Errorf("expected file=main.go, got %s", res.File)
	}
	if len(res.Entries) == 0 {
		t.Fatal("expected blame entries, got none")
	}
	for _, e := range res.Entries {
		if e.Hash == "" {
			t.Error("blame entry missing hash")
		}
		if e.Author == "" {
			t.Error("blame entry missing author")
		}
		if e.LineStart == 0 {
			t.Error("blame entry missing line_start")
		}
	}
}

func TestGitBlameTool_LineRange(t *testing.T) {
	dir := initTestRepo(t)

	result, err := GitBlameTool(context.Background(), map[string]any{
		"directory":  dir,
		"file":       "main.go",
		"start_line": 1,
		"end_line":   3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res gitBlameResult
	json.Unmarshal([]byte(result), &res)
	if len(res.Entries) == 0 {
		t.Fatal("expected blame entries for line range")
	}
}

func TestGitBlameTool_MissingFile(t *testing.T) {
	dir := initTestRepo(t)

	_, err := GitBlameTool(context.Background(), map[string]any{
		"directory": dir,
		"file":      "nonexistent.go",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
