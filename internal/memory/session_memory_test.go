package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShouldExtractSessionMemory_InitializationAndThresholds(t *testing.T) {
	cfg := DefaultSessionMemoryConfig

	progress := AccumulateSessionMemoryProgress(SessionMemoryProgress{}, SessionMemoryObservation{
		DeltaChars:           cfg.InitChars - 1,
		LastTurnHadToolCalls: false,
	})
	if ShouldExtractSessionMemory(cfg, progress, SessionMemoryObservation{}) {
		t.Fatal("did not expect extraction before initialization threshold")
	}

	progress = AccumulateSessionMemoryProgress(progress, SessionMemoryObservation{
		DeltaChars:           1,
		LastTurnHadToolCalls: false,
	})
	if !ShouldExtractSessionMemory(cfg, progress, SessionMemoryObservation{}) {
		t.Fatal("expected extraction at initialization threshold")
	}

	progress = ResetSessionMemoryProgressAfterExtraction(progress)
	progress = AccumulateSessionMemoryProgress(progress, SessionMemoryObservation{
		DeltaChars:           cfg.UpdateChars - 1,
		ToolCalls:            cfg.ToolCallsBetweenUpdates,
		LastTurnHadToolCalls: true,
	})
	if ShouldExtractSessionMemory(cfg, progress, SessionMemoryObservation{LastTurnHadToolCalls: true}) {
		t.Fatal("did not expect extraction before update chars threshold")
	}

	progress = AccumulateSessionMemoryProgress(progress, SessionMemoryObservation{
		DeltaChars:           1,
		ToolCalls:            0,
		LastTurnHadToolCalls: false,
	})
	if !ShouldExtractSessionMemory(cfg, progress, SessionMemoryObservation{LastTurnHadToolCalls: false}) {
		t.Fatal("expected extraction at natural break once update chars threshold is met")
	}
}

func TestValidateSessionMemoryDocument_RejectsUnexpectedSections(t *testing.T) {
	invalid := strings.TrimSpace(DefaultSessionMemoryTemplate) + "\n\n# Extra\nnope\n"
	if _, err := ValidateSessionMemoryDocument(invalid, MaxSessionMemoryBytes); err == nil {
		t.Fatal("expected extra section to be rejected")
	}
}

func TestEnsureSessionMemoryFile_RejectsManualTemplateCorruption(t *testing.T) {
	workspaceDir := t.TempDir()
	path, current, created, err := EnsureSessionMemoryFile(workspaceDir, "session-a")
	if err != nil {
		t.Fatalf("EnsureSessionMemoryFile create: %v", err)
	}
	if !created || !strings.Contains(current, "# Current State") {
		t.Fatalf("unexpected initial file state created=%v current=%q", created, current)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# Current State") {
		t.Fatalf("expected template file, got %q", string(raw))
	}

	corrupted := strings.ReplaceAll(strings.TrimSpace(DefaultSessionMemoryTemplate), "# Current State", "# Changed")
	if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := EnsureSessionMemoryFile(workspaceDir, "session-a"); err == nil {
		t.Fatal("expected corrupted managed file to be rejected")
	}
}

func TestWriteSessionMemoryFile_UsesCanonicalWorkspacePath(t *testing.T) {
	workspaceDir := t.TempDir()
	path, err := WriteSessionMemoryFile(workspaceDir, "../../weird/session", DefaultSessionMemoryTemplate)
	if err != nil {
		t.Fatalf("WriteSessionMemoryFile: %v", err)
	}
	if !strings.Contains(filepath.ToSlash(path), "/.metiq/session-memory/") {
		t.Fatalf("expected canonical session-memory path, got %q", path)
	}
	if strings.Contains(filepath.Base(path), "..") {
		t.Fatalf("expected sanitized filename, got %q", filepath.Base(path))
	}
}

func TestWriteSessionMemoryFile_RejectsAncestorSymlinkEscape(t *testing.T) {
	workspaceDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(workspaceDir, ".metiq")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	if _, err := WriteSessionMemoryFile(workspaceDir, "session-a", DefaultSessionMemoryTemplate); err == nil {
		t.Fatal("expected ancestor symlink escape to be rejected")
	}
}
