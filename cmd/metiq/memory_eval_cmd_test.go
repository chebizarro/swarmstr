package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/memory"
)

func TestRunMemoryEvalJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".metiq", "memory.sqlite")
	b, err := memory.OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	defer b.Close()
	_ = b.WriteMemoryRecord(context.Background(), memory.MemoryRecord{ID: "eval-1", Type: memory.MemoryRecordTypeDecision, Scope: memory.MemoryRecordScopeProject, Subject: "deployment", Text: "Canary before rollout"})
	out, err := captureStdout(t, func() error { return runMemoryEval([]string{"--json", "--path", dbPath}) })
	if err != nil {
		t.Fatalf("runMemoryEval: %v", err)
	}
	if !strings.Contains(out, "\"recall_at_5\"") || !strings.Contains(out, "\"token_cost\"") {
		t.Fatalf("expected eval JSON metrics, got: %s", out)
	}
}

func TestRunMemoryEvalUpdateBaselineRequiresApproval(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".metiq", "memory.sqlite")
	b, err := memory.OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	defer b.Close()
	if err := runMemoryEval([]string{"--path", dbPath, "--update-baseline"}); err == nil {
		t.Fatal("expected approval-gated error")
	}
	if err := runMemoryEval([]string{"--path", dbPath, "--update-baseline", "--approve-baseline-update"}); err != nil {
		t.Fatalf("runMemoryEval update baseline: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".metiq", "memory-evals", "baselines", "*.json"))
	if len(matches) == 0 {
		t.Fatal("expected dated baseline file")
	}
	if _, err := os.Stat(matches[0]); err != nil {
		t.Fatalf("baseline stat: %v", err)
	}
}

func TestRunMemoryEvalInvalidBaselineReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".metiq", "memory.sqlite")
	b, err := memory.OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	defer b.Close()
	baselineDir := memory.BaselineDir(home)
	if err := os.MkdirAll(baselineDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baselineDir, "2026-05-08.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMemoryEval([]string{"--path", dbPath}); err == nil || !strings.Contains(err.Error(), "load baseline") {
		t.Fatalf("expected baseline load error, got %v", err)
	}
}

func TestRunMemoryEvalHumanReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".metiq", "memory.sqlite")
	b, err := memory.OpenSQLiteBackend(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLiteBackend: %v", err)
	}
	defer b.Close()
	out, err := captureStdout(t, func() error { return runMemoryEval([]string{"--path", dbPath}) })
	if err != nil {
		t.Fatalf("runMemoryEval: %v", err)
	}
	if !strings.Contains(out, "recall@5") || !strings.Contains(out, "latency ms p50/p95/p99") {
		t.Fatalf("expected human report metrics, got: %s", out)
	}
}
