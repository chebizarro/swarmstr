package devtools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchFiles_ByPattern(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create test files
	os.WriteFile(filepath.Join(tmpDir, "file1.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file3.go"), []byte("package test"), 0644)

	opts := DefaultFileSearchOptions()
	opts.Pattern = "*.go"

	results, err := SearchFiles(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("SearchFiles failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if !strings.HasSuffix(r.Path, ".go") {
			t.Errorf("unexpected file: %s", r.Path)
		}
	}
}

func TestSearchFiles_ByContent(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create test files
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("hello world\nfoo bar"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("goodbye world\nbaz"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file3.txt"), []byte("nothing here"), 0644)

	opts := DefaultFileSearchOptions()
	opts.ContentPattern = "world"

	results, err := SearchFiles(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("SearchFiles failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results with 'world', got %d", len(results))
	}

	for _, r := range results {
		if len(r.Matches) == 0 {
			t.Errorf("expected matches in %s", r.Path)
		}
	}
}

func TestSearchFiles_ExcludePatterns(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create directories
	os.MkdirAll(filepath.Join(tmpDir, "src"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "node_modules"), 0755)

	os.WriteFile(filepath.Join(tmpDir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "node_modules", "pkg.js"), []byte("module"), 0644)

	opts := DefaultFileSearchOptions()
	opts.Pattern = "*"

	results, err := SearchFiles(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("SearchFiles failed: %v", err)
	}

	for _, r := range results {
		if strings.Contains(r.Path, "node_modules") {
			t.Errorf("node_modules should be excluded: %s", r.Path)
		}
	}
}

func TestSearchFiles_MaxResults(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create many files
	for i := 0; i < 20; i++ {
		os.WriteFile(filepath.Join(tmpDir, filepath.Base(tmpDir)+string(rune('a'+i))+".txt"), []byte("content"), 0644)
	}

	opts := DefaultFileSearchOptions()
	opts.MaxResults = 5

	results, err := SearchFiles(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("SearchFiles failed: %v", err)
	}

	if len(results) > 5 {
		t.Errorf("expected max 5 results, got %d", len(results))
	}
}

func TestGenerateTree(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create directory structure
	os.MkdirAll(filepath.Join(tmpDir, "src", "pkg"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "docs"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "pkg", "util.go"), []byte("package pkg"), 0644)

	opts := DefaultTreeOptions()
	tree, err := GenerateTree(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("GenerateTree failed: %v", err)
	}

	if tree == nil {
		t.Fatal("tree is nil")
	}

	if !tree.IsDir {
		t.Error("root should be a directory")
	}

	// Should have children
	if len(tree.Children) == 0 {
		t.Error("expected children in tree")
	}

	// Convert to string
	treeStr := TreeToString(tree)
	if !strings.Contains(treeStr, "src/") {
		t.Error("tree should contain src/")
	}
	if !strings.Contains(treeStr, "README.md") {
		t.Error("tree should contain README.md")
	}
}

func TestGenerateTree_MaxDepth(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create deep directory structure
	deepDir := filepath.Join(tmpDir, "a", "b", "c", "d", "e")
	os.MkdirAll(deepDir, 0755)
	os.WriteFile(filepath.Join(deepDir, "deep.txt"), []byte("deep"), 0644)

	opts := DefaultTreeOptions()
	opts.MaxDepth = 2

	tree, err := GenerateTree(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("GenerateTree failed: %v", err)
	}

	treeStr := TreeToString(tree)
	if strings.Contains(treeStr, "deep.txt") {
		t.Error("deep.txt should not be included with MaxDepth=2")
	}
}

func TestSearchSymbols(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a Go file with various symbols
	goCode := `package main

type MyStruct struct {
	field string
}

func (m *MyStruct) Method() string {
	return m.field
}

func main() {
	// main function
}

const Version = "1.0.0"

var globalVar = "test"
`
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(goCode), 0644)

	opts := SymbolSearchOptions{MaxResults: 100}
	symbols, err := SearchSymbols(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}

	// Should find: MyStruct (struct), Method (method), main (function), Version (const), globalVar (var)
	if len(symbols) < 4 {
		t.Errorf("expected at least 4 symbols, got %d", len(symbols))
	}

	// Check for specific symbols
	found := make(map[string]bool)
	for _, s := range symbols {
		found[s.Name+":"+s.Kind] = true
	}

	if !found["MyStruct:struct"] {
		t.Error("missing MyStruct struct")
	}
	if !found["Method:method"] {
		t.Error("missing Method method")
	}
	if !found["main:function"] {
		t.Error("missing main function")
	}
}

func TestSearchSymbols_FilterByKind(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	goCode := `package main

type MyType struct{}
func MyFunc() {}
const MyConst = 1
`
	os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte(goCode), 0644)

	opts := SymbolSearchOptions{
		Kinds:      []string{"function"},
		MaxResults: 100,
	}

	symbols, err := SearchSymbols(ctx, tmpDir, opts)
	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}

	for _, s := range symbols {
		if s.Kind != "function" {
			t.Errorf("unexpected kind: %s (expected function)", s.Kind)
		}
	}
}

func TestGetCodeContext(t *testing.T) {
	tmpDir := t.TempDir()

	content := `line 1
line 2
line 3
line 4
line 5
line 6
line 7
`
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte(content), 0644)

	ctx, err := GetCodeContext(testFile, 4, 2)
	if err != nil {
		t.Fatalf("GetCodeContext failed: %v", err)
	}

	if ctx.StartLine != 2 {
		t.Errorf("StartLine = %d, want 2", ctx.StartLine)
	}
	if ctx.EndLine != 6 {
		t.Errorf("EndLine = %d, want 6", ctx.EndLine)
	}
	if len(ctx.Lines) != 5 {
		t.Errorf("expected 5 lines, got %d", len(ctx.Lines))
	}
}

func TestGetFunctionContext(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package main

func example() {
	line1 := 1
	line2 := 2
	line3 := 3
}

func other() {
}
`
	testFile := filepath.Join(tmpDir, "test.go")
	os.WriteFile(testFile, []byte(content), 0644)

	ctx, err := GetFunctionContext(testFile, 5) // Line inside example()
	if err != nil {
		t.Fatalf("GetFunctionContext failed: %v", err)
	}

	if ctx.StartLine != 3 {
		t.Errorf("StartLine = %d, want 3", ctx.StartLine)
	}
	// Should include the whole function
	foundFunc := false
	for _, line := range ctx.Lines {
		if strings.Contains(line, "func example()") {
			foundFunc = true
			break
		}
	}
	if !foundFunc {
		t.Error("context should include function definition")
	}
}

func TestTreeToString(t *testing.T) {
	root := &TreeNode{
		Name:  "root",
		IsDir: true,
		Children: []*TreeNode{
			{Name: "dir1", IsDir: true, Children: []*TreeNode{
				{Name: "file1.txt", IsDir: false},
			}},
			{Name: "file2.txt", IsDir: false},
		},
	}

	result := TreeToString(root)

	if !strings.Contains(result, "root/") {
		t.Error("should contain root/")
	}
	if !strings.Contains(result, "dir1/") {
		t.Error("should contain dir1/")
	}
	if !strings.Contains(result, "file1.txt") {
		t.Error("should contain file1.txt")
	}
	// Just verify the tree is not empty and has structure
	if len(result) < 10 {
		t.Errorf("tree output too short: %q", result)
	}
}
