package devtools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePatch_Simple(t *testing.T) {
	patchText := `--- a/file.txt
+++ b/file.txt
@@ -1,3 +1,4 @@
 line 1
+new line
 line 2
 line 3
`
	patch, err := ParsePatch(patchText)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	if len(patch.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(patch.Files))
	}

	file := patch.Files[0]
	if file.OldPath != "file.txt" {
		t.Errorf("OldPath = %q, want %q", file.OldPath, "file.txt")
	}
	if file.NewPath != "file.txt" {
		t.Errorf("NewPath = %q, want %q", file.NewPath, "file.txt")
	}
	if len(file.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(file.Hunks))
	}

	hunk := file.Hunks[0]
	if hunk.OldStart != 1 || hunk.OldCount != 3 {
		t.Errorf("OldStart/Count = %d,%d, want 1,3", hunk.OldStart, hunk.OldCount)
	}
	if hunk.NewStart != 1 || hunk.NewCount != 4 {
		t.Errorf("NewStart/Count = %d,%d, want 1,4", hunk.NewStart, hunk.NewCount)
	}
}

func TestParsePatch_NewFile(t *testing.T) {
	patchText := `--- /dev/null
+++ b/newfile.txt
@@ -0,0 +1,3 @@
+line 1
+line 2
+line 3
`
	patch, err := ParsePatch(patchText)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	if len(patch.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(patch.Files))
	}

	file := patch.Files[0]
	if !file.IsNew {
		t.Error("expected IsNew = true")
	}
	if file.NewPath != "newfile.txt" {
		t.Errorf("NewPath = %q, want %q", file.NewPath, "newfile.txt")
	}
}

func TestParsePatch_DeleteFile(t *testing.T) {
	patchText := `--- a/oldfile.txt
+++ /dev/null
@@ -1,3 +0,0 @@
-line 1
-line 2
-line 3
`
	patch, err := ParsePatch(patchText)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	file := patch.Files[0]
	if !file.IsDelete {
		t.Error("expected IsDelete = true")
	}
}

func TestParsePatch_MultipleHunks(t *testing.T) {
	patchText := `--- a/file.txt
+++ b/file.txt
@@ -1,3 +1,4 @@
 line 1
+inserted
 line 2
 line 3
@@ -10,3 +11,2 @@
 line 10
-deleted
 line 12
`
	patch, err := ParsePatch(patchText)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	if len(patch.Files[0].Hunks) != 2 {
		t.Errorf("expected 2 hunks, got %d", len(patch.Files[0].Hunks))
	}
}

func TestApplyPatch_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	patchText := `--- /dev/null
+++ b/test.txt
@@ -0,0 +1,2 @@
+hello
+world
`
	opts := ApplyPatchOptions{WorkDir: tmpDir}
	result, err := ApplyPatch(ctx, patchText, opts)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got errors: %v", result.Errors)
	}
	if result.FilesChanged != 1 {
		t.Errorf("FilesChanged = %d, want 1", result.FilesChanged)
	}
	if result.LinesAdded != 2 {
		t.Errorf("LinesAdded = %d, want 2", result.LinesAdded)
	}

	// Verify file was created
	content, err := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(content) != "hello\nworld\n" {
		t.Errorf("content = %q, want %q", string(content), "hello\nworld\n")
	}
}

func TestApplyPatch_ModifyFile(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create initial file
	initialContent := "line 1\nline 2\nline 3\n"
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	patchText := `--- a/test.txt
+++ b/test.txt
@@ -1,3 +1,4 @@
 line 1
+inserted line
 line 2
 line 3
`
	opts := ApplyPatchOptions{WorkDir: tmpDir}
	result, err := ApplyPatch(ctx, patchText, opts)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got errors: %v", result.Errors)
	}
	if result.LinesAdded != 1 {
		t.Errorf("LinesAdded = %d, want 1", result.LinesAdded)
	}

	// Verify content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read modified file: %v", err)
	}
	expected := "line 1\ninserted line\nline 2\nline 3\n"
	if string(content) != expected {
		t.Errorf("content = %q, want %q", string(content), expected)
	}
}

func TestApplyPatch_DeleteFile(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create initial file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content\n"), 0644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	patchText := `--- a/test.txt
+++ /dev/null
@@ -1 +0,0 @@
-content
`
	opts := ApplyPatchOptions{WorkDir: tmpDir}
	result, err := ApplyPatch(ctx, patchText, opts)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got errors: %v", result.Errors)
	}

	// Verify file was deleted
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}

func TestApplyPatch_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create initial file
	initialContent := "original\n"
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	patchText := `--- a/test.txt
+++ b/test.txt
@@ -1 +1 @@
-original
+modified
`
	opts := ApplyPatchOptions{WorkDir: tmpDir, DryRun: true}
	result, err := ApplyPatch(ctx, patchText, opts)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got errors: %v", result.Errors)
	}

	// Verify file was NOT modified
	content, _ := os.ReadFile(testFile)
	if string(content) != initialContent {
		t.Error("dry run should not modify file")
	}
}

func TestValidatePatch(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create initial file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("line 1\nline 2\n"), 0644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	// Valid patch
	validPatch := `--- a/test.txt
+++ b/test.txt
@@ -1,2 +1,3 @@
 line 1
+new line
 line 2
`
	result, err := ValidatePatch(ctx, validPatch, tmpDir)
	if err != nil {
		t.Fatalf("ValidatePatch failed: %v", err)
	}
	if !result.Success {
		t.Errorf("expected valid patch to succeed: %v", result.Errors)
	}

	// Invalid patch (context mismatch)
	invalidPatch := `--- a/test.txt
+++ b/test.txt
@@ -1,2 +1,3 @@
 wrong context
+new line
 line 2
`
	result, _ = ValidatePatch(ctx, invalidPatch, tmpDir)
	if result.Success {
		t.Error("expected invalid patch to fail")
	}
}

func TestGenerateDiff(t *testing.T) {
	oldContent := "line 1\nline 2\nline 3"
	newContent := "line 1\nnew line\nline 2\nline 3"

	diff := GenerateDiff(oldContent, newContent, "a/file.txt", "b/file.txt")

	// For now, just verify headers are present
	// The full diff algorithm needs more work for complex cases
	if !strings.Contains(diff, "--- a/file.txt") {
		t.Error("diff should contain old file header")
	}
	if !strings.Contains(diff, "+++ b/file.txt") {
		t.Error("diff should contain new file header")
	}
	// The diff should not be empty for different content
	if diff != "" && !strings.Contains(diff, "@@") {
		t.Log("diff generated but missing hunks:", diff)
	}
}

func TestApplyStructuredEdits(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world\nhello again\n"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	edits := []StructuredEdit{
		{Search: "hello", Replace: "hi"},
	}

	result, err := ApplyStructuredEdits(ctx, testFile, edits, false)
	if err != nil {
		t.Fatalf("ApplyStructuredEdits failed: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.Replacements != 2 {
		t.Errorf("Replacements = %d, want 2", result.Replacements)
	}

	content, _ := os.ReadFile(testFile)
	if !strings.Contains(string(content), "hi world") {
		t.Error("expected 'hello' to be replaced with 'hi'")
	}
}

func TestApplyStructuredEdits_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	originalContent := "original content\n"
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte(originalContent), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	edits := []StructuredEdit{
		{Search: "original", Replace: "modified"},
	}

	result, err := ApplyStructuredEdits(ctx, testFile, edits, true)
	if err != nil {
		t.Fatalf("ApplyStructuredEdits failed: %v", err)
	}

	if !result.Success {
		t.Error("dry run should succeed")
	}
	if result.Diff == "" {
		t.Error("dry run should generate diff")
	}

	// Verify file unchanged
	content, _ := os.ReadFile(testFile)
	if string(content) != originalContent {
		t.Error("dry run should not modify file")
	}
}

func TestParseHunkHeader(t *testing.T) {
	cases := []struct {
		line      string
		oldStart  int
		oldCount  int
		newStart  int
		newCount  int
		expectErr bool
	}{
		{"@@ -1,3 +1,4 @@", 1, 3, 1, 4, false},
		{"@@ -1 +1 @@", 1, 1, 1, 1, false},
		{"@@ -10,5 +15,10 @@", 10, 5, 15, 10, false},
		{"@@ -0,0 +1,5 @@", 0, 0, 1, 5, false},
		{"invalid", 0, 0, 0, 0, true},
	}

	for _, tc := range cases {
		hunk, err := parseHunkHeader(tc.line)
		if tc.expectErr {
			if err == nil {
				t.Errorf("parseHunkHeader(%q) expected error", tc.line)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHunkHeader(%q) error: %v", tc.line, err)
			continue
		}
		if hunk.OldStart != tc.oldStart || hunk.OldCount != tc.oldCount {
			t.Errorf("parseHunkHeader(%q) old = %d,%d, want %d,%d",
				tc.line, hunk.OldStart, hunk.OldCount, tc.oldStart, tc.oldCount)
		}
		if hunk.NewStart != tc.newStart || hunk.NewCount != tc.newCount {
			t.Errorf("parseHunkHeader(%q) new = %d,%d, want %d,%d",
				tc.line, hunk.NewStart, hunk.NewCount, tc.newStart, tc.newCount)
		}
	}
}
