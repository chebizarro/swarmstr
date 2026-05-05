package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTaskToolInspect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Taskfile.yml")
	content := `version: '3'
tasks:
  build:
    desc: Build the project
    cmds:
      - go build ./...
  lint:
    internal: true
    deps: [build]
    cmds:
      - golangci-lint run
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	taskFn := TaskTool(FilesystemOpts{})
	out, err := taskFn(context.Background(), map[string]any{
		"action": "inspect",
		"path":   path,
	})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	var result struct {
		Action    string                `json:"action"`
		Version   string                `json:"version"`
		TaskCount int                   `json:"task_count"`
		Tasks     []taskfileTaskSummary `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Action != taskActionInspect {
		t.Fatalf("action = %q", result.Action)
	}
	if result.Version != "3" {
		t.Fatalf("version = %q, want 3", result.Version)
	}
	if result.TaskCount != 2 {
		t.Fatalf("task_count = %d, want 2", result.TaskCount)
	}
	if result.Tasks[0].Name != "build" || result.Tasks[0].Description != "Build the project" {
		t.Fatalf("unexpected first task: %+v", result.Tasks[0])
	}
	if !result.Tasks[1].Internal || !result.Tasks[1].HasDeps {
		t.Fatalf("unexpected second task: %+v", result.Tasks[1])
	}
}

func TestTaskToolWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "Taskfile.yaml")
	content := `version: '3'
tasks:
  test:
    desc: Run tests
    cmds:
      - go test ./...
`
	taskFn := TaskTool(FilesystemOpts{})
	out, err := taskFn(context.Background(), map[string]any{
		"action":  "write",
		"path":    path,
		"content": content,
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.TrimSpace(string(raw)) != strings.TrimSpace(content) {
		t.Fatalf("written content mismatch:\n%s", string(raw))
	}
	if !strings.Contains(out, `"task_count":1`) {
		t.Fatalf("expected task_count in result, got %s", out)
	}
}

func TestTaskToolRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Taskfile.yml")
	content := `version: '3'
tasks:
  hello:
    cmds:
      - echo hello
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	logPath := filepath.Join(dir, "task-invocation.log")
	scriptPath := filepath.Join(binDir, "task")
	script := "#!/bin/sh\n" +
		"printf 'PWD=%s\n' \"$PWD\" > \"$TASK_TEST_LOG\"\n" +
		"i=1\n" +
		"for arg in \"$@\"; do printf 'ARG%d=%s\n' \"$i\" \"$arg\" >> \"$TASK_TEST_LOG\"; i=$((i+1)); done\n" +
		"printf 'task-stdout'\n" +
		"printf 'task-stderr' >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)
	t.Setenv("TASK_TEST_LOG", logPath)

	taskFn := TaskTool(FilesystemOpts{})
	out, err := taskFn(context.Background(), map[string]any{
		"action":          "run",
		"path":            path,
		"task":            "hello",
		"vars_json":       `{"NAME":"swarmstr","COUNT":2}`,
		"timeout_seconds": 5,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var result struct {
		Action   string            `json:"action"`
		Task     string            `json:"task"`
		ExitCode int               `json:"exit_code"`
		Stdout   string            `json:"stdout"`
		Stderr   string            `json:"stderr"`
		Vars     map[string]string `json:"vars"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Action != taskActionRun || result.Task != "hello" {
		t.Fatalf("unexpected run result: %+v", result)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "task-stdout" || result.Stderr != "task-stderr" {
		t.Fatalf("unexpected output: %+v", result)
	}
	if result.Vars["NAME"] != "swarmstr" || result.Vars["COUNT"] != "2" {
		t.Fatalf("unexpected vars: %+v", result.Vars)
	}
	invocationRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read invocation log: %v", err)
	}
	invocation := string(invocationRaw)
	if !strings.Contains(invocation, "PWD="+dir) {
		t.Fatalf("expected task to run in taskfile dir, got %s", invocation)
	}
	if !strings.Contains(invocation, "ARG1=--taskfile") || !strings.Contains(invocation, "ARG2="+path) {
		t.Fatalf("expected taskfile args, got %s", invocation)
	}
	if !strings.Contains(invocation, "ARG3=hello") {
		t.Fatalf("expected task name arg, got %s", invocation)
	}
}

func TestTaskToolRunMissingTaskName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Taskfile.yml")
	if err := os.WriteFile(path, []byte("version: '3'\ntasks:\n  demo:\n    cmds: [echo ok]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskFn := TaskTool(FilesystemOpts{})
	_, err := taskFn(context.Background(), map[string]any{
		"action": "run",
		"path":   path,
	})
	if err == nil || !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("expected missing task error, got %v", err)
	}
}

func TestTaskToolRejectsOutsideWorkspace(t *testing.T) {
	workspaceDir := t.TempDir()
	outsideDir := t.TempDir()
	path := filepath.Join(outsideDir, "Taskfile.yml")
	if err := os.WriteFile(path, []byte("version: '3'\ntasks:\n  demo:\n    cmds: [echo ok]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskFn := TaskTool(FilesystemOpts{WorkspaceDir: func() string { return workspaceDir }})
	_, err := taskFn(context.Background(), map[string]any{
		"action": "inspect",
		"path":   path,
	})
	if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("expected workspace containment error, got %v", err)
	}
}

func TestTaskToolRunMissingBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH clearing is shell-specific on this test")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "Taskfile.yml")
	if err := os.WriteFile(path, []byte("version: '3'\ntasks:\n  demo:\n    cmds: [echo ok]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyBin := t.TempDir()
	t.Setenv("PATH", emptyBin)
	taskFn := TaskTool(FilesystemOpts{})
	_, err := taskFn(context.Background(), map[string]any{
		"action": "run",
		"path":   path,
		"task":   "demo",
	})
	if err == nil || !strings.Contains(err.Error(), "binary not found") {
		t.Fatalf("expected missing binary error, got %v", err)
	}
}
