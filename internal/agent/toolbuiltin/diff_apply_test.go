package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiffApply_SimpleReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	diff := `--- a/main.go
+++ b/main.go
@@ -3,3 +3,3 @@
 func main() {
-	fmt.Println("hello")
+	fmt.Println("world")
 }
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalApplied != 1 {
		t.Errorf("expected 1 applied, got %d", res.TotalApplied)
	}
	if res.TotalFailed != 0 {
		t.Errorf("expected 0 failed, got %d", res.TotalFailed)
	}

	content, _ := os.ReadFile(path)
	if got := string(content); got != "package main\n\nfunc main() {\n\tfmt.Println(\"world\")\n}\n" {
		t.Errorf("unexpected content:\n%s", got)
	}
}

func TestDiffApply_MultipleHunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.go")
	os.WriteFile(path, []byte("package app\n\nimport \"fmt\"\n\nfunc hello() {\n\tfmt.Println(\"hello\")\n}\n\nfunc goodbye() {\n\tfmt.Println(\"goodbye\")\n}\n"), 0644)

	diff := `--- a/app.go
+++ b/app.go
@@ -5,3 +5,3 @@
 func hello() {
-	fmt.Println("hello")
+	fmt.Println("hi")
 }
@@ -9,3 +9,3 @@
 func goodbye() {
-	fmt.Println("goodbye")
+	fmt.Println("bye")
 }
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalApplied != 1 {
		t.Errorf("expected 1 file applied, got %d", res.TotalApplied)
	}
	if res.Files[0].Hunks != 2 {
		t.Errorf("expected 2 hunks, got %d", res.Files[0].Hunks)
	}

	content, _ := os.ReadFile(path)
	expected := "package app\n\nimport \"fmt\"\n\nfunc hello() {\n\tfmt.Println(\"hi\")\n}\n\nfunc goodbye() {\n\tfmt.Println(\"bye\")\n}\n"
	if got := string(content); got != expected {
		t.Errorf("unexpected content:\n%s\nexpected:\n%s", got, expected)
	}
}

func TestDiffApply_NewFile(t *testing.T) {
	dir := t.TempDir()

	diff := `--- /dev/null
+++ b/new_file.go
@@ -0,0 +1,5 @@
+package main
+
+func newFunc() {
+	return
+}
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalApplied != 1 {
		t.Errorf("expected 1 applied, got %d", res.TotalApplied)
	}
	if res.Files[0].Status != "created" {
		t.Errorf("expected status=created, got %s", res.Files[0].Status)
	}

	content, _ := os.ReadFile(filepath.Join(dir, "new_file.go"))
	if !contains(string(content), "func newFunc()") {
		t.Errorf("new file missing expected content: %s", content)
	}
}

func TestDiffApply_DeleteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.go")
	os.WriteFile(path, []byte("package old\n"), 0644)

	diff := `--- a/old.go
+++ /dev/null
@@ -1 +0,0 @@
-package old
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalApplied != 1 {
		t.Errorf("expected 1 applied, got %d", res.TotalApplied)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestDiffApply_MultiFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nvar x = 1\n"), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n\nvar y = 2\n"), 0644)

	diff := `--- a/a.go
+++ b/a.go
@@ -3 +3 @@
-var x = 1
+var x = 10
--- a/b.go
+++ b/b.go
@@ -3 +3 @@
-var y = 2
+var y = 20
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalApplied != 2 {
		t.Errorf("expected 2 applied, got %d", res.TotalApplied)
	}

	contentA, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !contains(string(contentA), "var x = 10") {
		t.Errorf("a.go not patched correctly: %s", contentA)
	}

	contentB, _ := os.ReadFile(filepath.Join(dir, "b.go"))
	if !contains(string(contentB), "var y = 20") {
		t.Errorf("b.go not patched correctly: %s", contentB)
	}
}

func TestDiffApply_AddLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n\nfunc main() {\n}\n"), 0644)

	diff := `--- a/main.go
+++ b/main.go
@@ -2,3 +2,5 @@
 
 func main() {
+	fmt.Println("hello")
+	fmt.Println("world")
 }
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.Files[0].Added != 2 {
		t.Errorf("expected 2 added, got %d", res.Files[0].Added)
	}

	content, _ := os.ReadFile(path)
	expected := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")\n}\n"
	if got := string(content); got != expected {
		t.Errorf("unexpected content:\n%s", got)
	}
}

func TestDiffApply_RemoveLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n\nimport \"fmt\"\nimport \"os\"\n\nfunc main() {\n}\n"), 0644)

	diff := `--- a/main.go
+++ b/main.go
@@ -3,2 +3 @@
 import "fmt"
-import "os"
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.Files[0].Removed != 1 {
		t.Errorf("expected 1 removed, got %d", res.Files[0].Removed)
	}

	content, _ := os.ReadFile(path)
	if contains(string(content), "import \"os\"") {
		t.Error("os import should have been removed")
	}
}

func TestDiffApply_FuzzyMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	// File has 2 extra lines at the top compared to what the diff expects.
	os.WriteFile(path, []byte("// Copyright 2024\n// License: MIT\npackage main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	// Diff says the hunk starts at line 3, but because of extra lines it's at line 5.
	diff := `--- a/main.go
+++ b/main.go
@@ -3,3 +3,3 @@
 func main() {
-	fmt.Println("hello")
+	fmt.Println("world")
 }
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalApplied != 1 {
		t.Errorf("expected 1 applied (fuzzy), got %d — errors: %+v", res.TotalApplied, res.Files)
	}

	content, _ := os.ReadFile(path)
	if !contains(string(content), "\"world\"") {
		t.Errorf("fuzzy match should have applied the change: %s", content)
	}
}

func TestDiffApply_ContextMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n\nfunc main() {\n}\n"), 0644)

	// This diff has context that doesn't match the file at all.
	diff := `--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package app
-func nonexistent() {
+func replacement() {
 }
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalFailed != 1 {
		t.Errorf("expected 1 failed (context mismatch), got %d failed", res.TotalFailed)
	}
	if res.Files[0].Status != "error" {
		t.Errorf("expected status=error, got %s", res.Files[0].Status)
	}
}

func TestDiffApply_EmptyDiff(t *testing.T) {
	opts := FilesystemOpts{}
	tool := DiffApplyTool(opts)
	_, err := tool(context.Background(), map[string]any{"diff": ""})
	if err == nil {
		t.Fatal("expected error for empty diff")
	}
}

func TestDiffApply_NewFileInSubdir(t *testing.T) {
	dir := t.TempDir()

	diff := `--- /dev/null
+++ b/pkg/util/helper.go
@@ -0,0 +1,3 @@
+package util
+
+func Helper() {}
`

	opts := FilesystemOpts{WorkspaceDir: func() string { return dir }}
	tool := DiffApplyTool(opts)

	result, err := tool(context.Background(), map[string]any{
		"diff":      diff,
		"directory": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res diffApplyResult
	json.Unmarshal([]byte(result), &res)

	if res.TotalApplied != 1 {
		t.Errorf("expected 1 applied, got %d", res.TotalApplied)
	}

	content, _ := os.ReadFile(filepath.Join(dir, "pkg", "util", "helper.go"))
	if !contains(string(content), "func Helper()") {
		t.Errorf("subdirectory file not created correctly: %s", content)
	}
}

// ─── parser unit tests ────────────────────────────────────────────────────────

func TestParseUnifiedDiff_Basic(t *testing.T) {
	diff := `--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
 package foo
-var x = 1
+var x = 2
`

	patches, err := parseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(patches))
	}
	if patches[0].path != "foo.go" {
		t.Errorf("expected path=foo.go, got %s", patches[0].path)
	}
	if len(patches[0].hunks) != 1 {
		t.Errorf("expected 1 hunk, got %d", len(patches[0].hunks))
	}
}

func TestParseUnifiedDiff_MultiFile(t *testing.T) {
	diff := `--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-old
+new
--- a/b.go
+++ b/b.go
@@ -1 +1 @@
-old2
+new2
`

	patches, err := parseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("expected 2 patches, got %d", len(patches))
	}
}

func TestParseHunkHeader(t *testing.T) {
	h, err := parseHunkHeader("@@ -10,5 +12,7 @@ func main()")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.origStart != 10 || h.origCount != 5 {
		t.Errorf("orig: expected 10,5 got %d,%d", h.origStart, h.origCount)
	}
	if h.newStart != 12 || h.newCount != 7 {
		t.Errorf("new: expected 12,7 got %d,%d", h.newStart, h.newCount)
	}
}

func TestParseHunkHeader_NoCount(t *testing.T) {
	h, err := parseHunkHeader("@@ -1 +1 @@")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.origCount != 1 || h.newCount != 1 {
		t.Errorf("expected count=1 when omitted, got orig=%d new=%d", h.origCount, h.newCount)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
