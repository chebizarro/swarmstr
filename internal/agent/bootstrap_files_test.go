package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvedBootstrapFiles_RejectsOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, warnings := LoadResolvedBootstrapFiles(workspace, []string{outside})
	if len(files) != 0 {
		t.Fatalf("expected no files, got %+v", files)
	}
	if len(warnings) == 0 {
		t.Fatal("expected boundary warning")
	}
}

func TestBuildBootstrapContextFiles_TruncatesToBudget(t *testing.T) {
	files := []WorkspaceBootstrapFile{{
		Name:    "AGENTS.md",
		Path:    "/tmp/AGENTS.md",
		Content: strings.Repeat("a", DefaultBootstrapMaxChars+500),
	}}
	contextFiles := BuildBootstrapContextFiles(files, nil, 200, 200)
	if len(contextFiles) != 1 {
		t.Fatalf("expected 1 context file, got %d", len(contextFiles))
	}
	if len(contextFiles[0].Content) > 200 {
		t.Fatalf("expected truncated context <= 200 chars, got %d", len(contextFiles[0].Content))
	}
	analysis := AnalyzeBootstrapBudget(BuildBootstrapInjectionStats(files, contextFiles), 200, 200)
	if !analysis.HasTruncation {
		t.Fatal("expected truncation analysis")
	}
	if len(FormatBootstrapTruncationWarningLines(analysis, 3)) == 0 {
		t.Fatal("expected truncation warning lines")
	}
}
