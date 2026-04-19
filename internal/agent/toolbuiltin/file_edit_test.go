package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileEditTool_SingleReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("func hello() {\n\treturn \"hello\"\n}\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileEditTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"path":    path,
		"search":  "\"hello\"",
		"replace": "\"world\"",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res fileEditResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Edits != 1 {
		t.Errorf("expected 1 edit, got %d", res.Edits)
	}

	content, _ := os.ReadFile(path)
	if got := string(content); got != "func hello() {\n\treturn \"world\"\n}\n" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestFileEditTool_ReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("foo bar foo baz foo\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileEditTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"path":    path,
		"search":  "foo",
		"replace": "qux",
		"all":     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res fileEditResult
	json.Unmarshal([]byte(result), &res)
	if res.Edits != 3 {
		t.Errorf("expected 3 edits, got %d", res.Edits)
	}

	content, _ := os.ReadFile(path)
	if got := string(content); got != "qux bar qux baz qux\n" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestFileEditTool_BatchEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileEditTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"search": "package main", "replace": "package app"},
			map[string]any{"search": "\"hello\"", "replace": "\"goodbye\""},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res fileEditResult
	json.Unmarshal([]byte(result), &res)
	if res.Edits != 2 {
		t.Errorf("expected 2 edits, got %d", res.Edits)
	}

	content, _ := os.ReadFile(path)
	got := string(content)
	if got != "package app\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"goodbye\")\n}\n" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestFileEditTool_SearchNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileEditTool(opts)

	_, err := tool(context.Background(), map[string]any{
		"path":    path,
		"search":  "nonexistent text",
		"replace": "replacement",
	})
	if err == nil {
		t.Fatal("expected error when search text not found")
	}

	// File should be unchanged.
	content, _ := os.ReadFile(path)
	if string(content) != "hello world\n" {
		t.Error("file was modified despite search not found error")
	}
}

func TestFileEditTool_Delete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("keep this\ndelete this\nkeep this too\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := FileEditTool(opts)

	_, err := tool(context.Background(), map[string]any{
		"path":    path,
		"search":  "delete this\n",
		"replace": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(path)
	if got := string(content); got != "keep this\nkeep this too\n" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestFileEditTool_EmptyPath(t *testing.T) {
	opts := FilesystemOpts{}
	tool := FileEditTool(opts)
	_, err := tool(context.Background(), map[string]any{"path": "", "search": "x", "replace": "y"})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}
