package toolbuiltin

import (
	"strings"
	"testing"
)

// ─── gitStatusChar tests ──────────────────────────────────────────────────────

func TestGitStatusChar(t *testing.T) {
	tests := []struct {
		c    byte
		want string
	}{
		{'A', "added"},
		{'M', "modified"},
		{'D', "deleted"},
		{'R', "renamed"},
		{'C', "copied"},
		{'T', "type_changed"},
		{'X', "X"}, // unknown → raw char
	}
	for _, tt := range tests {
		got := gitStatusChar(tt.c)
		if got != tt.want {
			t.Errorf("gitStatusChar(%q) = %q, want %q", tt.c, got, tt.want)
		}
	}
}

// ─── parseInt tests ───────────────────────────────────────────────────────────

func TestParseInt(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"42", 42},
		{"100", 100},
		{"-", 0},   // binary file indicator
		{"", 0},    // empty string
		{"12a", 12}, // ignores non-digits
	}
	for _, tt := range tests {
		got := parseInt(tt.in)
		if got != tt.want {
			t.Errorf("parseInt(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// ─── parseNameStatus tests ────────────────────────────────────────────────────

func TestParseNameStatus_Empty(t *testing.T) {
	result := parseNameStatus("")
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}
}

func TestParseNameStatus_Added(t *testing.T) {
	result := parseNameStatus("A\tnewfile.go")
	entry, ok := result["newfile.go"]
	if !ok {
		t.Fatal("expected newfile.go entry")
	}
	if entry.status != "added" {
		t.Errorf("status = %q, want added", entry.status)
	}
}

func TestParseNameStatus_Modified(t *testing.T) {
	result := parseNameStatus("M\tmain.go")
	entry, ok := result["main.go"]
	if !ok {
		t.Fatal("expected main.go entry")
	}
	if entry.status != "modified" {
		t.Errorf("status = %q, want modified", entry.status)
	}
}

func TestParseNameStatus_Deleted(t *testing.T) {
	result := parseNameStatus("D\told.go")
	entry, ok := result["old.go"]
	if !ok {
		t.Fatal("expected old.go entry")
	}
	if entry.status != "deleted" {
		t.Errorf("status = %q, want deleted", entry.status)
	}
}

func TestParseNameStatus_Renamed(t *testing.T) {
	result := parseNameStatus("R100\told.go\tnew.go")
	entry, ok := result["new.go"]
	if !ok {
		t.Fatal("expected new.go entry")
	}
	if entry.status != "renamed" {
		t.Errorf("status = %q, want renamed", entry.status)
	}
	if entry.oldPath != "old.go" {
		t.Errorf("oldPath = %q, want old.go", entry.oldPath)
	}
}

func TestParseNameStatus_Copied(t *testing.T) {
	result := parseNameStatus("C100\tsrc.go\tdst.go")
	entry, ok := result["dst.go"]
	if !ok {
		t.Fatal("expected dst.go entry")
	}
	if entry.status != "copied" {
		t.Errorf("status = %q, want copied", entry.status)
	}
	if entry.oldPath != "src.go" {
		t.Errorf("oldPath = %q, want src.go", entry.oldPath)
	}
}

func TestParseNameStatus_TypeChanged(t *testing.T) {
	result := parseNameStatus("T\tlink.go")
	entry, ok := result["link.go"]
	if !ok {
		t.Fatal("expected link.go entry")
	}
	if entry.status != "type_changed" {
		t.Errorf("status = %q, want type_changed", entry.status)
	}
}

func TestParseNameStatus_UnknownCode(t *testing.T) {
	result := parseNameStatus("X\tweird.go")
	entry, ok := result["weird.go"]
	if !ok {
		t.Fatal("expected weird.go entry")
	}
	if entry.status != "modified" {
		t.Errorf("unknown codes should default to modified, got %q", entry.status)
	}
}

func TestParseNameStatus_MultipleEntries(t *testing.T) {
	input := "A\tnew.go\nM\texisting.go\nD\tremoved.go\n"
	result := parseNameStatus(input)
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result["new.go"].status != "added" {
		t.Errorf("new.go: want added, got %q", result["new.go"].status)
	}
	if result["existing.go"].status != "modified" {
		t.Errorf("existing.go: want modified, got %q", result["existing.go"].status)
	}
	if result["removed.go"].status != "deleted" {
		t.Errorf("removed.go: want deleted, got %q", result["removed.go"].status)
	}
}

func TestParseNameStatus_SkipsMalformedLines(t *testing.T) {
	input := "A\tgood.go\nbadline\nM\talso_good.go"
	result := parseNameStatus(input)
	// "badline" has no tab, so SplitN returns <2 parts, should be skipped.
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(result), result)
	}
}

// ─── attachFileDiffs tests ────────────────────────────────────────────────────

func TestAttachFileDiffs_Basic(t *testing.T) {
	result := &gitDiffResult{
		Files: []gitDiffFile{
			{Path: "main.go", Status: "modified"},
			{Path: "utils.go", Status: "added"},
		},
	}

	diffOut := `diff --git a/main.go b/main.go
index abc..def 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
+package main
diff --git a/utils.go b/utils.go
new file mode 100644
--- /dev/null
+++ b/utils.go
@@ -0,0 +1,5 @@
+package main`

	attachFileDiffs(result, diffOut)

	if result.Files[0].Diff == "" {
		t.Error("expected main.go to have diff attached")
	}
	if !strings.Contains(result.Files[0].Diff, "diff --git") {
		t.Error("expected diff header in main.go diff")
	}
	if result.Files[1].Diff == "" {
		t.Error("expected utils.go to have diff attached")
	}
}

func TestAttachFileDiffs_NoMatch(t *testing.T) {
	result := &gitDiffResult{
		Files: []gitDiffFile{
			{Path: "missing.go", Status: "modified"},
		},
	}
	attachFileDiffs(result, "diff --git a/other.go b/other.go\nsome diff")
	if result.Files[0].Diff != "" {
		t.Error("expected no diff for unmatched file")
	}
}

func TestAttachFileDiffs_Truncation(t *testing.T) {
	result := &gitDiffResult{
		Files: []gitDiffFile{
			{Path: "big.go", Status: "modified"},
		},
	}
	// Build a diff that exceeds 4000 chars.
	diffOut := "diff --git a/big.go b/big.go\n" + strings.Repeat("+ line of code\n", 500)
	attachFileDiffs(result, diffOut)
	if len(result.Files[0].Diff) > 4100 {
		t.Errorf("expected truncation, diff length = %d", len(result.Files[0].Diff))
	}
	if !strings.Contains(result.Files[0].Diff, "truncated") {
		t.Error("expected truncation marker")
	}
}

func TestAttachFileDiffs_EmptyDiff(t *testing.T) {
	result := &gitDiffResult{
		Files: []gitDiffFile{
			{Path: "empty.go", Status: "modified"},
		},
	}
	attachFileDiffs(result, "")
	if result.Files[0].Diff != "" {
		t.Error("expected empty diff for empty input")
	}
}
