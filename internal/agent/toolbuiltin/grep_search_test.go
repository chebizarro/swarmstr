package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
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

// ─── parseRipgrepJSON tests ──────────────────────────────────────────────────

func TestParseRipgrepJSON_Empty(t *testing.T) {
	out, err := parseRipgrepJSON("", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res grepResult
	json.Unmarshal([]byte(out), &res)
	if res.TotalMatches != 0 {
		t.Errorf("expected 0 total matches, got %d", res.TotalMatches)
	}
}

func TestParseRipgrepJSON_SingleMatch(t *testing.T) {
	jsonLine := `{"type":"match","data":{"path":{"text":"./main.go"},"line_number":42,"lines":{"text":"func main() {\n"},"submatches":[{"match":{"text":"func"},"start":0,"end":4}]}}`
	out, err := parseRipgrepJSON(jsonLine, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res grepResult
	json.Unmarshal([]byte(out), &res)
	if res.TotalMatches != 1 {
		t.Errorf("total matches = %d, want 1", res.TotalMatches)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(res.Matches))
	}
	m := res.Matches[0]
	if m.File != "main.go" {
		t.Errorf("file = %q, want 'main.go'", m.File)
	}
	if m.Line != 42 {
		t.Errorf("line = %d, want 42", m.Line)
	}
	if m.Column != 1 {
		t.Errorf("column = %d, want 1 (start=0 + 1)", m.Column)
	}
}

func TestParseRipgrepJSON_TruncatesResults(t *testing.T) {
	var lines string
	for i := 0; i < 10; i++ {
		lines += `{"type":"match","data":{"path":{"text":"file.go"},"line_number":` + fmt.Sprintf("%d", i+1) + `,"lines":{"text":"line\n"},"submatches":[]}}` + "\n"
	}
	out, err := parseRipgrepJSON(lines, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res grepResult
	json.Unmarshal([]byte(out), &res)
	if res.TotalMatches != 10 {
		t.Errorf("total = %d, want 10", res.TotalMatches)
	}
	if len(res.Matches) != 3 {
		t.Errorf("matches = %d, want 3 (max)", len(res.Matches))
	}
	if !res.Truncated {
		t.Error("expected truncated=true")
	}
}

func TestParseRipgrepJSON_SkipsNonMatch(t *testing.T) {
	input := `{"type":"begin","data":{"path":{"text":"file.go"}}}
{"type":"match","data":{"path":{"text":"file.go"},"line_number":1,"lines":{"text":"hello\n"},"submatches":[]}}
{"type":"end","data":{"path":{"text":"file.go"},"stats":{}}}
{"type":"summary","data":{}}`
	out, err := parseRipgrepJSON(input, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res grepResult
	json.Unmarshal([]byte(out), &res)
	if res.TotalMatches != 1 {
		t.Errorf("total = %d, want 1 (only match events)", res.TotalMatches)
	}
}

func TestParseRipgrepJSON_InvalidJSON(t *testing.T) {
	input := "not json\n{\"type\":\"match\",\"data\":{\"path\":{\"text\":\"f.go\"},\"line_number\":1,\"lines\":{\"text\":\"x\\n\"},\"submatches\":[]}}"
	out, err := parseRipgrepJSON(input, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res grepResult
	json.Unmarshal([]byte(out), &res)
	// Should skip the bad line and parse the good one.
	if res.TotalMatches != 1 {
		t.Errorf("total = %d, want 1", res.TotalMatches)
	}
}

func TestParseRipgrepJSON_LongLine(t *testing.T) {
	longText := strings.Repeat("x", 600) + "\n"
	input := `{"type":"match","data":{"path":{"text":"big.go"},"line_number":1,"lines":{"text":"` + strings.Repeat("x", 600) + `\n"},"submatches":[]}}`
	_ = longText
	out, err := parseRipgrepJSON(input, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res grepResult
	json.Unmarshal([]byte(out), &res)
	if len(res.Matches) != 1 {
		t.Fatalf("expected 1 match")
	}
	if len(res.Matches[0].Text) > 510 {
		t.Errorf("text should be truncated, len = %d", len(res.Matches[0].Text))
	}
}
