package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTreeTool_BasicTree(t *testing.T) {
	dir := t.TempDir()
	// Create a small project structure.
	os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0755)
	os.MkdirAll(filepath.Join(dir, "docs"), 0755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "app.go"), []byte("package src\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "pkg", "util.go"), []byte("package pkg\n"), 0644)
	os.WriteFile(filepath.Join(dir, "docs", "README.md"), []byte("# Docs\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileTreeTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res treeResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.Entries == 0 {
		t.Fatal("expected entries, got 0")
	}
	if res.Tree == "" {
		t.Fatal("expected tree output, got empty")
	}
	// Should contain our files.
	if !strings.Contains(res.Tree, "main.go") {
		t.Error("tree missing main.go")
	}
	if !strings.Contains(res.Tree, "src/") {
		t.Error("tree missing src/")
	}
	if !strings.Contains(res.Tree, "docs/") {
		t.Error("tree missing docs/")
	}
}

func TestFileTreeTool_SkipDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0755)
	os.MkdirAll(filepath.Join(dir, "__pycache__"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "app.go"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("x"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileTreeTool(opts)

	result, err := tool(context.Background(), map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res treeResult
	json.Unmarshal([]byte(result), &res)

	if strings.Contains(res.Tree, "node_modules") {
		t.Error("tree should skip node_modules")
	}
	if strings.Contains(res.Tree, "__pycache__") {
		t.Error("tree should skip __pycache__")
	}
	if !strings.Contains(res.Tree, "src/") {
		t.Error("tree should include src/")
	}
}

func TestFileTreeTool_MaxDepth(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b", "c", "d"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "d", "deep.txt"), []byte("x"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileTreeTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"path":      dir,
		"max_depth": 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res treeResult
	json.Unmarshal([]byte(result), &res)

	// Should have a/ and a/b/ but not deeper.
	if !strings.Contains(res.Tree, "a/") {
		t.Error("tree should include a/")
	}
	if strings.Contains(res.Tree, "deep.txt") {
		t.Error("tree should not show deep.txt at max_depth=2")
	}
}

func TestFileTreeTool_DirsOnly(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "app.go"), []byte("x"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileTreeTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"path":      dir,
		"dirs_only": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res treeResult
	json.Unmarshal([]byte(result), &res)

	if strings.Contains(res.Tree, "main.go") {
		t.Error("dirs_only should not show files")
	}
	if !strings.Contains(res.Tree, "src/") {
		t.Error("dirs_only should still show directories")
	}
}

func TestFileTreeDef_ParamAliases(t *testing.T) {
	// Verify the definition includes expected aliases that models commonly hallucinate.
	aliases := FileTreeDef.ParamAliases
	if aliases == nil {
		t.Fatal("FileTreeDef.ParamAliases is nil")
	}
	expected := map[string]string{
		"depth": "max_depth",
		"dir":   "path",
	}
	for alias, want := range expected {
		if got, ok := aliases[alias]; !ok || got != want {
			t.Errorf("alias %q: got %q (ok=%v), want %q", alias, got, ok, want)
		}
	}
}

func TestFileTreeTool_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileTreeTool(opts)

	result, err := tool(context.Background(), map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res treeResult
	json.Unmarshal([]byte(result), &res)
	if res.Entries != 0 {
		t.Errorf("expected 0 entries for empty dir, got %d", res.Entries)
	}
}
