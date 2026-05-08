package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/memory"
)

func TestRunMemoryCompactJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".metiq", "memory.sqlite")
	b, err := memory.OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.WriteMemoryRecord(context.Background(), memory.MemoryRecord{ID: "dup-a", Type: memory.MemoryRecordTypeFact, Scope: memory.MemoryRecordScopeProject, Text: "duplicate cli memory"})
	_ = b.WriteMemoryRecord(context.Background(), memory.MemoryRecord{ID: "dup-b", Type: memory.MemoryRecordTypeFact, Scope: memory.MemoryRecordScopeProject, Text: "duplicate cli memory"})
	_ = b.Close()
	out, err := captureStdout(t, func() error { return runMemoryCompact([]string{"--json", "--path", dbPath}) })
	if err != nil {
		t.Fatalf("runMemoryCompact: %v", err)
	}
	if !strings.Contains(out, "\"deduped\"") || !strings.Contains(out, "\"ok\": true") {
		t.Fatalf("expected compact JSON, got: %s", out)
	}
}

func TestRunMemoryHealthReportAndFixAllConfirmation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".metiq", "memory.sqlite")
	b, err := memory.OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-24 * time.Hour)
	_ = b.WriteMemoryRecord(context.Background(), memory.MemoryRecord{ID: "expired-cli", Type: memory.MemoryRecordTypeEpisode, Scope: memory.MemoryRecordScopeSession, Text: "old cli episode", ValidUntil: &past})
	_ = b.Close()
	out, err := captureStdout(t, func() error { return runMemoryHealth([]string{"--report", "--path", dbPath}) })
	if err != nil {
		t.Fatalf("runMemoryHealth report: %v", err)
	}
	if !strings.Contains(out, "Health score") || !strings.Contains(out, "metiq memory compact --expire-stale") {
		t.Fatalf("expected actionable health report, got: %s", out)
	}
	if err := runMemoryHealth([]string{"--fix-all", "--path", dbPath}); err == nil || !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("expected fix-all confirmation error, got %v", err)
	}
}

func TestRunMemoryHealthFixSafeJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".metiq", "memory.sqlite")
	b, err := memory.OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-24 * time.Hour)
	_ = b.WriteMemoryRecord(context.Background(), memory.MemoryRecord{ID: "safe-cli", Type: memory.MemoryRecordTypeEpisode, Scope: memory.MemoryRecordScopeSession, Text: "safe cli episode", ValidUntil: &past})
	_ = b.Close()
	out, err := captureStdout(t, func() error { return runMemoryHealth([]string{"--fix-safe", "--json", "--path", dbPath}) })
	if err != nil {
		t.Fatalf("runMemoryHealth fix-safe: %v", err)
	}
	if !strings.Contains(out, "\"actions\"") || !strings.Contains(out, "health_score") {
		t.Fatalf("expected fix-safe JSON, got: %s", out)
	}
}
