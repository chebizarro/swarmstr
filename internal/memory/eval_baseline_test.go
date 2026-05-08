package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAndLoadMemoryEvalBaseline(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".metiq", "memory-evals", "baselines")
	run := MemoryEvalRun{CaseCount: 10, RecallAt5: 0.8, P95LatencyMS: 12.5}
	path, err := SaveMemoryEvalBaseline(dir, NewMemoryEvalBaseline(run), time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SaveMemoryEvalBaseline: %v", err)
	}
	if !strings.HasSuffix(path, "2026-05-07.json") {
		t.Fatalf("path=%q", path)
	}
	loaded, err := LoadMemoryEvalBaseline(path)
	if err != nil {
		t.Fatalf("LoadMemoryEvalBaseline: %v", err)
	}
	if loaded.SchemaVersion != MemoryEvalBaselineSchemaVersion || loaded.DatasetVersion == "" {
		t.Fatalf("unexpected baseline metadata: %+v", loaded)
	}
	if loaded.Run.RecallAt5 != run.RecallAt5 {
		t.Fatalf("run mismatch: %+v", loaded.Run)
	}
}

func TestCompareMemoryEvalRunsThresholds(t *testing.T) {
	baseline := MemoryEvalRun{RecallAt5: 1.0, RecallAt10: 1.0, P95LatencyMS: 100, TokenCost: 1000}
	current := MemoryEvalRun{RecallAt5: 0.89, RecallAt10: 0.9, P95LatencyMS: 151, TokenCost: 1200}
	reg := CompareMemoryEvalRuns(baseline, current)
	if len(reg.Failures) == 0 {
		t.Fatalf("expected failures, got %+v", reg)
	}
	if len(reg.Warnings) == 0 {
		t.Fatalf("expected warnings, got %+v", reg)
	}
}
