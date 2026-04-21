package main

import (
	"strings"
	"testing"

	"metiq/internal/memory"
)

func TestIsSessionMemoryEmpty_Template(t *testing.T) {
	if !isSessionMemoryEmpty(memory.DefaultSessionMemoryTemplate) {
		t.Error("expected default template to be detected as empty")
	}
}

func TestIsSessionMemoryEmpty_WithContent(t *testing.T) {
	content := strings.Replace(memory.DefaultSessionMemoryTemplate,
		"_A short and distinctive 5-10 word descriptive title for the session. Super info dense, no filler_",
		"_A short and distinctive 5-10 word descriptive title for the session. Super info dense, no filler_\nImplementing session memory compaction",
		1)
	if isSessionMemoryEmpty(content) {
		t.Error("expected non-empty session memory to not be detected as empty")
	}
}

func TestIsSessionMemoryEmpty_Blank(t *testing.T) {
	if !isSessionMemoryEmpty("") {
		t.Error("expected blank string to be empty")
	}
	if !isSessionMemoryEmpty("  \n  ") {
		t.Error("expected whitespace to be empty")
	}
}

func TestReadSessionMemoryContent_InvalidPath(t *testing.T) {
	_, err := readSessionMemoryContent("/nonexistent/path.md")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestReadSessionMemoryContent_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path, err := memory.WriteSessionMemoryFile(dir, "test-session", memory.DefaultSessionMemoryTemplate)
	if err != nil {
		t.Fatal(err)
	}
	content, err := readSessionMemoryContent(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "# Session Title") {
		t.Error("expected session memory content to contain section headers")
	}
}
