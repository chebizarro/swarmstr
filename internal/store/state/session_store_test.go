package state

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSessionStore_RecordTurn(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.LinkTask("sess-1", "task-1", "run-1", "parent-task", "parent-run"); err != nil {
		t.Fatalf("link task: %v", err)
	}
	if err := ss.RecordTurn("sess-1", TurnTelemetry{
		TurnID:         "turn-1",
		StartedAtMS:    100,
		EndedAtMS:      250,
		DurationMS:     150,
		Outcome:        "completed",
		StopReason:     "model_text",
		FallbackUsed:   true,
		FallbackFrom:   "a",
		FallbackTo:     "b",
		FallbackReason: "429",
		InputTokens:    10,
		OutputTokens:   5,
	}); err != nil {
		t.Fatalf("record turn: %v", err)
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if got.LastTurn == nil {
		t.Fatal("expected last turn snapshot")
	}
	if got.LastTurn.TurnID != "turn-1" || got.LastTurn.DurationMS != 150 || got.LastTurn.Outcome != "completed" {
		t.Fatalf("unexpected last turn snapshot: %+v", got.LastTurn)
	}
	if !got.LastTurn.FallbackUsed || got.LastTurn.FallbackTo != "b" {
		t.Fatalf("expected fallback data on last turn: %+v", got.LastTurn)
	}
	if got.LastTurn.TaskID != "task-1" || got.LastTurn.RunID != "run-1" {
		t.Fatalf("expected task linkage on last turn: %+v", got.LastTurn)
	}
	if got.LastTurn.ParentTaskID != "parent-task" || got.LastTurn.ParentRunID != "parent-run" {
		t.Fatalf("expected parent linkage on last turn: %+v", got.LastTurn)
	}
}

func TestTurnTelemetry_JSONShape(t *testing.T) {
	raw, err := json.Marshal(TurnTelemetry{
		TurnID:      "turn-1",
		TaskID:      "task-1",
		RunID:       "run-1",
		StartedAtMS: 1,
		EndedAtMS:   2,
		Outcome:     "completed",
		StopReason:  "model_text",
		Result:      TaskResultRef{Kind: "transcript_entry", ID: "entry-1"},
	})
	if err != nil {
		t.Fatalf("marshal telemetry: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal telemetry: %v", err)
	}
	for _, field := range []string{"turn_id", "task_id", "run_id", "started_at_ms", "ended_at_ms", "outcome", "stop_reason", "result"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing field %q in telemetry JSON: %s", field, string(raw))
		}
	}
}

func TestSessionStore_LinkTaskAndRecordTaskResult(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.LinkTask("sess-1", "task-1", "run-1", "parent-task", "parent-run"); err != nil {
		t.Fatalf("link task: %v", err)
	}
	if err := ss.AppendChildTask("sess-1", "child-1"); err != nil {
		t.Fatalf("append child task: %v", err)
	}
	if err := ss.AppendChildTask("sess-1", "child-1"); err != nil {
		t.Fatalf("append child task dedupe: %v", err)
	}
	result := TaskResultRef{Kind: "transcript_entry", ID: "entry-1"}
	if err := ss.RecordTaskResult("sess-1", "task-1", "run-1", result); err != nil {
		t.Fatalf("record task result: %v", err)
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if got.ActiveTaskID != "" || got.ActiveRunID != "" {
		t.Fatalf("expected active task cleared after result: %+v", got)
	}
	if got.LastCompletedTaskID != "task-1" || got.LastCompletedRunID != "run-1" {
		t.Fatalf("expected completed task linkage: %+v", got)
	}
	if got.LastTaskResult.Kind != "transcript_entry" || got.LastTaskResult.ID != "entry-1" {
		t.Fatalf("expected result ref to persist: %+v", got.LastTaskResult)
	}
	if len(got.ChildTaskIDs) != 1 || got.ChildTaskIDs[0] != "child-1" {
		t.Fatalf("expected child task dedupe: %+v", got.ChildTaskIDs)
	}
}

func TestSessionStore_RecordMemoryRecall_MergesAndCaps(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	for i := 0; i < memoryRecallSampleCap+2; i++ {
		if err := ss.RecordMemoryRecall("sess-1", "turn-"+string(rune('a'+i)), &MemoryRecallSample{
			QueryHash: "q",
			FileSelected: []MemoryRecallFileHit{{
				RelativePath: "prefs.md",
				Reasons:      []string{"name"},
			}},
		}, map[string]string{"root::prefs.md": "signal-1"}); err != nil {
			t.Fatalf("record memory recall %d: %v", i, err)
		}
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if len(got.FileMemorySurfaced) != 1 || got.FileMemorySurfaced["root::prefs.md"] != "signal-1" {
		t.Fatalf("unexpected surfaced file-memory state: %+v", got.FileMemorySurfaced)
	}
	if len(got.RecentMemoryRecall) != memoryRecallSampleCap {
		t.Fatalf("expected capped recall samples, got %d", len(got.RecentMemoryRecall))
	}
	if got.RecentMemoryRecall[0].TurnID != "turn-c" || got.RecentMemoryRecall[len(got.RecentMemoryRecall)-1].TurnID != "turn-j" {
		t.Fatalf("expected append-capped recall ordering, got %+v", got.RecentMemoryRecall)
	}
	if got.RecentMemoryRecall[0].Strategy != "deterministic" {
		t.Fatalf("expected default recall strategy, got %+v", got.RecentMemoryRecall[0])
	}
}

func TestMemoryRecallSample_JSONShape(t *testing.T) {
	raw, err := json.Marshal(MemoryRecallSample{
		TurnID:          "turn-1",
		Strategy:        "deterministic",
		QueryHash:       "abc123",
		QueryRuneCount:  12,
		QueryTokenCount: 3,
		Scope:           "project",
		IndexedSession:  []MemoryRecallIndexedHit{{MemoryID: "m1", Topic: "task"}},
		FileSelected:    []MemoryRecallFileHit{{RelativePath: "prefs.md", Reasons: []string{"name"}, Score: 4}},
		InjectedAny:     true,
	})
	if err != nil {
		t.Fatalf("marshal recall sample: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal recall sample: %v", err)
	}
	for _, field := range []string{"turn_id", "strategy", "query_hash", "indexed_session", "file_selected", "injected_any"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing field %q in recall sample JSON: %s", field, string(raw))
		}
	}
}
