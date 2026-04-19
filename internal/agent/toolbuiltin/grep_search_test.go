package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepSearchTool_BasicSearch(t *testing.T) {
	dir := t.TempDir()
	// Create test files.
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "world.go"), []byte("package main\n\nfunc world() {\n\treturn \"world\"\n}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello World\nThis is a test.\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := GrepSearchTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"pattern":   "func",
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res grepResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.TotalMatches < 2 {
		t.Errorf("expected at least 2 matches, got %d", res.TotalMatches)
	}
	if len(res.Matches) == 0 {
		t.Fatal("expected matches, got none")
	}
	// Verify each match has required fields.
	for _, m := range res.Matches {
		if m.File == "" {
			t.Error("match missing file")
		}
		if m.Line == 0 {
			t.Error("match missing line number")
		}
		if m.Text == "" {
			t.Error("match missing text")
		}
	}
}

func TestGrepSearchTool_NoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := GrepSearchTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"pattern":   "xyznonexistent",
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res grepResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.TotalMatches != 0 {
		t.Errorf("expected 0 matches, got %d", res.TotalMatches)
	}
}

func TestGrepSearchTool_IncludeGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc hello() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "test.py"), []byte("def hello():\n    pass\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := GrepSearchTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"pattern":   "hello",
		"directory": dir,
		"include":   "*.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res grepResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.TotalMatches == 0 {
		t.Fatal("expected at least 1 match")
	}
	for _, m := range res.Matches {
		if !strings.HasSuffix(m.File, ".go") {
			t.Errorf("expected only .go files, got %s (tool: %s)", m.File, res.Tool)
		}
	}
}

func TestGrepSearchTool_FixedStrings(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("if x > 0 && y < 10 {\n}\n"), 0644)

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := GrepSearchTool(opts)

	// This would be invalid regex without fixed_strings.
	result, err := tool(context.Background(), map[string]any{
		"pattern":       "x > 0 && y < 10",
		"directory":     dir,
		"fixed_strings": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res grepResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.TotalMatches != 1 {
		t.Errorf("expected 1 match, got %d", res.TotalMatches)
	}
}

func TestGrepSearchTool_EmptyPattern(t *testing.T) {
	opts := FilesystemOpts{}
	tool := GrepSearchTool(opts)
	_, err := tool(context.Background(), map[string]any{"pattern": ""})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}
