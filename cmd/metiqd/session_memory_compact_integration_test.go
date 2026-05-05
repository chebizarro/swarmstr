package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	ctxengine "metiq/internal/context"
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

func TestTrySessionMemoryCompact_PassesLastSummarizedBoundary(t *testing.T) {
	ctx := context.Background()
	engine := ctxengine.NewWindowedEngine(100)
	for i := 0; i < 20; i++ {
		if _, err := engine.Ingest(ctx, "sess-a", ctxengine.Message{ID: fmt.Sprintf("msg-%02d", i), Role: "user", Content: strings.Repeat("x", 4000)}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	summary := testSessionMemoryDocument("Boundary-aware compaction keeps unsummarized tail")
	path, err := memory.WriteSessionMemoryFile(dir, "sess-a", summary)
	if err != nil {
		t.Fatal(err)
	}

	cr, ok := trySessionMemoryCompact(ctx, engine, "sess-a", path, "msg-09")
	if !ok || !cr.Compacted {
		t.Fatalf("expected session-memory compaction, ok=%v result=%+v", ok, cr)
	}
	assembled, err := engine.Assemble(ctx, "sess-a", 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(assembled.Messages) != 10 || assembled.Messages[0].ID != "msg-10" {
		t.Fatalf("expected boundary to keep msg-10..msg-19, got len=%d first=%q", len(assembled.Messages), assembled.Messages[0].ID)
	}
	if !strings.Contains(assembled.SystemPromptAddition, "Boundary-aware compaction keeps unsummarized tail") {
		t.Fatalf("expected summary in assemble, got %q", assembled.SystemPromptAddition)
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
