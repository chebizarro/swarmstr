package toolbuiltin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"metiq/internal/memory"
)

func TestMemoryDiagnosticToolsReturnStableJSON(t *testing.T) {
	ctx := context.Background()
	b, err := memory.OpenSQLiteBackend(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if err := b.WriteMemoryRecord(ctx, memory.MemoryRecord{ID: "tool-pref", Type: memory.MemoryRecordTypePreference, Scope: memory.MemoryRecordScopeUser, Subject: "editor", Text: "User preference: editor is Vim.", Confidence: 0.9, Salience: 0.9}); err != nil {
		t.Fatal(err)
	}

	statsOut, err := MemoryStatsTool(b)(ctx, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var stats memory.MemoryStatsReport
	if err := json.Unmarshal([]byte(statsOut), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.TotalRecords != 1 || stats.ByType[memory.MemoryRecordTypePreference] != 1 {
		t.Fatalf("unexpected stats output: %s", statsOut)
	}

	explainOut, err := MemoryExplainQueryTool(b)(ctx, map[string]any{"query": "what preferences about editor", "include_sources": false})
	if err != nil {
		t.Fatal(err)
	}
	var explain memory.MemoryQueryExplanation
	if err := json.Unmarshal([]byte(explainOut), &explain); err != nil {
		t.Fatal(err)
	}
	if explain.Intent.Name != memory.QueryIntentPreference || explain.ResultCount != 1 || explain.Results[0].Why == nil {
		t.Fatalf("unexpected explain output: %s", explainOut)
	}

	healthOut, err := MemoryHealthTool(b)(ctx, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var health memory.MemoryHealthReport
	if err := json.Unmarshal([]byte(healthOut), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status == "" || health.RecordCount != 1 {
		t.Fatalf("unexpected health output: %s", healthOut)
	}
}
