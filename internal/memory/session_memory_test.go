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

func TestValidateSessionMemoryDocument_CanonicalizesModelDescriptions(t *testing.T) {
	canonical := strings.TrimSpace(DefaultSessionMemoryTemplate)

	t.Run("canonical template round-trips unchanged", func(t *testing.T) {
		got, err := ValidateSessionMemoryDocument(canonical, MaxSessionMemoryBytes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != canonical {
			t.Fatalf("round-trip drift:\n--- got ---\n%s\n--- want ---\n%s", got, canonical)
		}
	})

	t.Run("missing description replaced by body is repaired", func(t *testing.T) {
		doc := strings.Replace(canonical,
			"# Key results\n"+sectionDescription(t, "# Key results"),
			"# Key results\nThe actual result is 42.", 1)
		if doc == canonical {
			t.Fatal("test setup failed to strip the description")
		}
		got, err := ValidateSessionMemoryDocument(doc, MaxSessionMemoryBytes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "# Key results\n" + sectionDescription(t, "# Key results") + "\nThe actual result is 42."
		if !strings.Contains(got, want) {
			t.Fatalf("expected canonical description with preserved body, want substring:\n%s\ngot:\n%s", want, got)
		}
	})

	t.Run("blank line between header and description is repaired", func(t *testing.T) {
		doc := strings.Replace(canonical,
			"# Key results\n"+sectionDescription(t, "# Key results"),
			"# Key results\n\n"+sectionDescription(t, "# Key results"), 1)
		got, err := ValidateSessionMemoryDocument(doc, MaxSessionMemoryBytes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "# Key results\n"+sectionDescription(t, "# Key results")) {
			t.Fatalf("expected canonical header+description, got:\n%s", got)
		}
	})

	t.Run("reworded description is canonicalized", func(t *testing.T) {
		doc := strings.Replace(canonical,
			"# Key results\n"+sectionDescription(t, "# Key results"),
			"# Key results\n_Results summary_\nThe result is 42.", 1)
		got, err := ValidateSessionMemoryDocument(doc, MaxSessionMemoryBytes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(got, "_Results summary_") {
			t.Fatalf("expected the model description to be replaced, got:\n%s", got)
		}
		if !strings.Contains(got, "# Key results\n"+sectionDescription(t, "# Key results")+"\nThe result is 42.") {
			t.Fatalf("expected canonical description plus preserved body, got:\n%s", got)
		}
	})

	t.Run("unknown trailing section still rejected", func(t *testing.T) {
		if _, err := ValidateSessionMemoryDocument(canonical+"\n\n# Extra\nnope\n", MaxSessionMemoryBytes); err == nil {
			t.Fatal("expected unknown trailing section to be rejected")
		}
	})
}

func sectionDescription(t *testing.T, header string) string {
	t.Helper()
	for _, section := range sessionMemorySections {
		if section.Header == header {
			return section.Description
		}
	}
	t.Fatalf("unknown section header %q", header)
	return ""
}

func TestWriteSessionMemoryFile_FormatErrorIsTyped(t *testing.T) {
	workspaceDir := t.TempDir()
	_, err := WriteSessionMemoryFile(workspaceDir, "session-a", "# Nope\nnot a managed document\n")
	if err == nil {
		t.Fatal("expected an invalid document to be rejected")
	}
	if !IsSessionMemoryFormatError(err) {
		t.Fatalf("expected SessionMemoryFormatError, got %T: %v", err, err)
	}
	if _, err := WriteSessionMemoryFile(workspaceDir, "session-a", DefaultSessionMemoryTemplate); err != nil {
		t.Fatalf("valid document rejected: %v", err)
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
