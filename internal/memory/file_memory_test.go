package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateMemoryEntrypointContent_LineAndByteCaps(t *testing.T) {
	longLines := make([]string, 0, MaxMemoryEntrypointLines+5)
	for i := 0; i < MaxMemoryEntrypointLines+5; i++ {
		longLines = append(longLines, strings.Repeat("x", 40))
	}
	truncation := TruncateMemoryEntrypointContent(strings.Join(longLines, "\n"))
	if !truncation.WasLineTruncated {
		t.Fatalf("expected line truncation: %#v", truncation)
	}
	if !strings.Contains(truncation.Content, "WARNING") {
		t.Fatalf("expected warning in truncated content: %q", truncation.Content)
	}

	byteHeavy := strings.Repeat("abcdefghij", MaxMemoryEntrypointBytes/10+10)
	truncation = TruncateMemoryEntrypointContent(byteHeavy)
	if !truncation.WasByteTruncated {
		t.Fatalf("expected byte truncation: %#v", truncation)
	}
	if !strings.Contains(truncation.Content, "prompt budget") {
		t.Fatalf("expected byte warning in truncated content: %q", truncation.Content)
	}
	if len(truncation.Content) > MaxMemoryEntrypointBytes {
		t.Fatalf("expected truncated content to stay within byte cap: got %d > %d", len(truncation.Content), MaxMemoryEntrypointBytes)
	}
	if gotLines := strings.Count(truncation.Content, "\n") + 1; gotLines > MaxMemoryEntrypointLines {
		t.Fatalf("expected truncated content to stay within line cap: got %d > %d", gotLines, MaxMemoryEntrypointLines)
	}
}

func TestScanFileMemoryTopics_OnlyReturnsValidTypedFrontmatterFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(filepath.Join(memoryDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	validPath := filepath.Join(memoryDir, "nested", "prefs.md")
	validContent := `---
name: user-prefs
description: Stable response style preferences
type: feedback
---
Use terse bullet summaries.
`
	if err := os.WriteFile(validPath, []byte(validContent), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(memoryDir, "broken.md")
	invalidContent := `---
name: broken
description: Missing valid type
type: nope
---
Invalid.
`
	if err := os.WriteFile(invalidPath, []byte(invalidContent), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(memoryDir, "2026-04-03-log.md")
	if err := os.WriteFile(logPath, []byte("# plain log\nno frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan, err := ScanFileMemoryTopics(workspaceDir)
	if err != nil {
		t.Fatalf("ScanFileMemoryTopics: %v", err)
	}
	if len(scan.Topics) != 1 {
		t.Fatalf("expected 1 valid topic, got %#v", scan.Topics)
	}
	topic := scan.Topics[0]
	if topic.RelativePath != "nested/prefs.md" || topic.Name != "user-prefs" || topic.Type != FileMemoryTypeFeedback {
		t.Fatalf("unexpected topic metadata: %#v", topic)
	}
	if scan.InvalidFileCount != 2 {
		t.Fatalf("expected two ignored files, got %d", scan.InvalidFileCount)
	}
}

func TestBuildFileMemoryPrompt_IncludesEntrypointAndTypedTopics(t *testing.T) {
	workspaceDir := t.TempDir()
	entrypointLines := make([]string, 0, MaxMemoryEntrypointLines+1)
	for i := 0; i < MaxMemoryEntrypointLines+1; i++ {
		entrypointLines = append(entrypointLines, "- entry")
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, FileMemoryEntrypointName), []byte(strings.Join(entrypointLines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "project.md"), []byte(`---
name: release-plan
description: Release timing and launch constraints
type: project
---
Launch is blocked on legal review.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := BuildFileMemoryPrompt(workspaceDir)
	for _, want := range []string{
		"## File-backed Memory",
		"### Valid memory types",
		"### MEMORY.md",
		"WARNING:",
		"`project.md` [project] release-plan — Release timing and launch constraints",
		"### Search guidance",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected %q in prompt, got: %s", want, prompt)
		}
	}
}

func TestBuildFileMemoryPrompt_IgnoresOversizedEntrypoint(t *testing.T) {
	workspaceDir := t.TempDir()
	oversized := strings.Repeat("oversized-entrypoint\n", 5000) + "SECRET_MARKER"
	if err := os.WriteFile(filepath.Join(workspaceDir, FileMemoryEntrypointName), []byte(oversized), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := BuildFileMemoryPrompt(workspaceDir)
	if !strings.Contains(prompt, "exceeds the safe read limit") {
		t.Fatalf("expected oversized entrypoint warning, got: %s", prompt)
	}
	if strings.Contains(prompt, "SECRET_MARKER") {
		t.Fatalf("expected oversized entrypoint content to be ignored, got: %s", prompt)
	}
}

func TestBuildFileMemoryPrompt_IgnoresEntrypointSymlinkOutsideWorkspace(t *testing.T) {
	workspaceDir := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, FileMemoryEntrypointName)
	if err := os.WriteFile(target, []byte("outside memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(workspaceDir, FileMemoryEntrypointName)); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	prompt := BuildFileMemoryPrompt(workspaceDir)
	if !strings.Contains(prompt, "resolves outside the workspace root") {
		t.Fatalf("expected symlink escape warning, got: %s", prompt)
	}
	if strings.Contains(prompt, "outside memory") {
		t.Fatalf("expected outside entrypoint content to be ignored, got: %s", prompt)
	}
}

func TestBuildFileMemoryPrompt_WarnsWhenEntrypointUnreadable(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspaceDir, FileMemoryEntrypointName), 0o755); err != nil {
		t.Fatal(err)
	}

	prompt := BuildFileMemoryPrompt(workspaceDir)
	if !strings.Contains(prompt, "could not be read because the path is a directory") {
		t.Fatalf("expected unreadable entrypoint warning, got: %s", prompt)
	}
}

func TestScanFileMemoryTopics_IgnoresTopicSymlinkOutsideWorkspace(t *testing.T) {
	workspaceDir := t.TempDir()
	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "prefs.md")
	if err := os.WriteFile(target, []byte(`---
name: escaped
description: Should not be loaded
type: feedback
---
outside
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(memoryDir, "escaped.md")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	scan, err := ScanFileMemoryTopics(workspaceDir)
	if err != nil {
		t.Fatalf("ScanFileMemoryTopics: %v", err)
	}
	if len(scan.Topics) != 0 {
		t.Fatalf("expected symlinked topic to be ignored, got %#v", scan.Topics)
	}
	if scan.InvalidFileCount != 1 {
		t.Fatalf("expected one ignored symlinked topic, got %d", scan.InvalidFileCount)
	}
}
