package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMemoryEntrypointContentExactBoundaries(t *testing.T) {
	if err := ValidateMemoryEntrypointContent(strings.Repeat("x", MaxMemoryEntrypointBytes)); err != nil {
		t.Fatalf("exact byte boundary rejected: %v", err)
	}
	if err := ValidateMemoryEntrypointContent(strings.Repeat("x", MaxMemoryEntrypointBytes+1)); err == nil {
		t.Fatal("expected byte rejection")
	} else {
		var budget *MemoryEntrypointBudgetError
		if !errors.As(err, &budget) || budget.ByteCount != MaxMemoryEntrypointBytes+1 {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	exactLines := strings.Repeat("x\r\n", MaxMemoryEntrypointLines-1) + "x"
	if err := ValidateMemoryEntrypointContent(exactLines); err != nil {
		t.Fatalf("exact CRLF line boundary rejected: %v", err)
	}
	tooManyLines := exactLines + "\r\nx"
	if err := ValidateMemoryEntrypointContent(tooManyLines); err == nil {
		t.Fatal("expected line rejection")
	}
	utf8AtBoundary := strings.Repeat("é", MaxMemoryEntrypointBytes/2)
	if err := ValidateMemoryEntrypointContent(utf8AtBoundary); err != nil {
		t.Fatalf("exact UTF-8 byte boundary rejected: %v", err)
	}
}

func TestWriteMemoryEntrypointRejectsWithoutReplacingExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileMemoryEntrypointName)
	if err := WriteMemoryEntrypoint(path, "# Existing\n"); err != nil {
		t.Fatal(err)
	}
	if err := WriteMemoryEntrypoint(path, strings.Repeat("x", MaxMemoryEntrypointBytes+1)); err == nil {
		t.Fatal("expected rejection")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "# Existing\n" {
		t.Fatalf("existing content changed: %q", raw)
	}
}

func TestBuildFileMemoryPromptOmitsOverBudgetEntrypoint(t *testing.T) {
	root := t.TempDir()
	marker := "SHOULD-NOT-BE-INJECTED"
	content := marker + strings.Repeat("x", MaxMemoryEntrypointBytes)
	if err := os.WriteFile(filepath.Join(root, FileMemoryEntrypointName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := BuildFileMemoryPrompt(root)
	if strings.Contains(prompt, marker) || !strings.Contains(prompt, "was rejected and not loaded") {
		t.Fatalf("unexpected prompt: %s", prompt)
	}
}
