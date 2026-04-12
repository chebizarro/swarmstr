package methods

import (
	"encoding/json"
	"testing"

	"metiq/internal/planner"
	"metiq/internal/store/state"
)

// ─── TasksTraceRequest ───────────────────────────────────────────────────────

func TestTasksTraceRequest_Normalize_RequiresTaskID(t *testing.T) {
	_, err := TasksTraceRequest{}.Normalize()
	if err == nil {
		t.Fatal("expected error when task_id missing")
	}
}

func TestTasksTraceRequest_Normalize_DefaultLimit(t *testing.T) {
	req, err := TasksTraceRequest{TaskID: "t1"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if req.Limit != 200 {
		t.Fatalf("expected default limit 200, got %d", req.Limit)
	}
}

func TestTasksTraceRequest_Normalize_CapsLimit(t *testing.T) {
	req, err := TasksTraceRequest{TaskID: "t1", Limit: 99999}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if req.Limit != 2000 {
		t.Fatalf("expected capped limit 2000, got %d", req.Limit)
	}
}

// ─── AssembleTaskTrace ───────────────────────────────────────────────────────

func baseTask() state.TaskSpec {
	return state.TaskSpec{
		Version:      1,
		TaskID:       "t1",
		GoalID:       "g1",
		Title:        "Test task",
		Instructions: "Do something",
		Status:       state.TaskStatusCompleted,
	}
}

func TestAssembleTaskTrace_TurnEvents(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "turn-1", TaskID: "t1", RunID: "r1", EndedAtMS: 1000, Outcome: "success", DurationMS: 500, InputTokens: 100, OutputTokens: 50},
			{TurnID: "turn-2", TaskID: "t1", RunID: "r1", EndedAtMS: 2000, Outcome: "success", DurationMS: 300},
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if resp.TaskID != "t1" {
		t.Fatalf("expected task_id t1, got %q", resp.TaskID)
	}
	if resp.GoalID != "g1" {
		t.Fatalf("expected goal_id g1, got %q", resp.GoalID)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
	if resp.Events[0].Kind != TraceKindTurn {
		t.Fatalf("expected turn kind, got %q", resp.Events[0].Kind)
	}
	if resp.Events[0].Turn == nil {
		t.Fatal("expected turn detail")
	}
	if resp.Events[0].Turn.TurnID != "turn-1" {
		t.Fatalf("expected turn-1, got %q", resp.Events[0].Turn.TurnID)
	}
	if resp.Events[0].Turn.InputTokens != 100 {
		t.Fatalf("expected 100 input tokens, got %d", resp.Events[0].Turn.InputTokens)
	}
}

func TestAssembleTaskTrace_MemoryRecallEvents(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		MemoryRecall: []state.MemoryRecallSample{
			{
				RecordedAtMS: 500,
				TurnID:       "turn-1",
				TaskID:       "t1",
				RunID:        "r1",
				Strategy:     "deterministic",
				Scope:        "session",
				IndexedSession: []state.MemoryRecallIndexedHit{
					{MemoryID: "m1", Topic: "topic1"},
				},
				FileSelected: []state.MemoryRecallFileHit{
					{RelativePath: "file.md", Score: 5},
					{RelativePath: "notes.md", Score: 3},
				},
				InjectedAny:      true,
				IndexedLatencyMS: 15,
				FileLatencyMS:    8,
			},
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(resp.Events))
	}
	evt := resp.Events[0]
	if evt.Kind != TraceKindMemoryRecall {
		t.Fatalf("expected memory_recall kind, got %q", evt.Kind)
	}
	if evt.MemoryRecall == nil {
		t.Fatal("expected memory recall detail")
	}
	if evt.MemoryRecall.IndexedHits != 1 {
		t.Fatalf("expected 1 indexed hit, got %d", evt.MemoryRecall.IndexedHits)
	}
	if evt.MemoryRecall.FileHits != 2 {
		t.Fatalf("expected 2 file hits, got %d", evt.MemoryRecall.FileHits)
	}
	if !evt.MemoryRecall.InjectedAny {
		t.Fatal("expected injected_any true")
	}
	if evt.Summary != "memory_recall:injected" {
		t.Fatalf("expected memory_recall:injected summary, got %q", evt.Summary)
	}
}

func TestAssembleTaskTrace_VerificationEvents(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		VerificationEvents: []planner.VerificationEvent{
			{
				Type:      planner.VerifEventStarted,
				TaskID:    "t1",
				RunID:     "r1",
				GoalID:    "g1",
				CreatedAt: 10, // unix seconds
			},
			{
				Type:      planner.VerifEventCheckPass,
				TaskID:    "t1",
				RunID:     "r1",
				CheckID:   "c1",
				CheckType: "schema",
				Status:    "passed",
				CreatedAt: 11,
			},
			{
				Type:       planner.VerifEventCompleted,
				TaskID:     "t1",
				RunID:      "r1",
				CreatedAt:  12,
			},
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if len(resp.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(resp.Events))
	}
	// Verify ordering: 10000ms, 11000ms, 12000ms.
	if resp.Events[0].Timestamp != 10000 {
		t.Fatalf("expected 10000, got %d", resp.Events[0].Timestamp)
	}
	if resp.Events[1].Verification.CheckID != "c1" {
		t.Fatalf("expected check c1, got %q", resp.Events[1].Verification.CheckID)
	}
	if resp.Events[2].Summary != string(planner.VerifEventCompleted) {
		t.Fatalf("expected completed summary, got %q", resp.Events[2].Summary)
	}
}

func TestAssembleTaskTrace_DelegationEvents(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		WorkerEvents: []planner.WorkerEvent{
			{
				EventID:   "e1",
				TaskID:    "t1",
				RunID:     "r1",
				WorkerID:  "w1",
				State:     planner.WorkerStatePending,
				CreatedAt: 5,
			},
			{
				EventID:  "e2",
				TaskID:   "t1",
				RunID:    "r1",
				WorkerID: "w1",
				State:    planner.WorkerStateRunning,
				Progress: &planner.ProgressInfo{PercentComplete: 0.5, Message: "halfway"},
				CreatedAt: 8,
			},
			{
				EventID:   "e3",
				TaskID:    "t1",
				RunID:     "r1",
				WorkerID:  "w1",
				State:     planner.WorkerStateCompleted,
				CreatedAt: 15,
			},
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if len(resp.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(resp.Events))
	}
	if resp.Events[0].Delegation.WorkerState != string(planner.WorkerStatePending) {
		t.Fatalf("expected assigned, got %q", resp.Events[0].Delegation.WorkerState)
	}
	if resp.Events[1].Delegation.Progress != 0.5 {
		t.Fatalf("expected 0.5 progress, got %f", resp.Events[1].Delegation.Progress)
	}
	if resp.Events[2].Summary != "delegation:"+string(planner.WorkerStateCompleted) {
		t.Fatalf("expected delegation:completed, got %q", resp.Events[2].Summary)
	}
}

func TestAssembleTaskTrace_MixedTimeOrdering(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "turn-1", EndedAtMS: 2000, Outcome: "success"},
		},
		MemoryRecall: []state.MemoryRecallSample{
			{RecordedAtMS: 1500, TurnID: "turn-1", InjectedAny: true},
		},
		VerificationEvents: []planner.VerificationEvent{
			{Type: planner.VerifEventStarted, TaskID: "t1", CreatedAt: 3}, // 3000ms
		},
		WorkerEvents: []planner.WorkerEvent{
			{EventID: "e1", TaskID: "t1", WorkerID: "w1", State: planner.WorkerStatePending, CreatedAt: 1}, // 1000ms
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if len(resp.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(resp.Events))
	}
	// Order: delegation(1000) → memory(1500) → turn(2000) → verification(3000)
	expected := []TraceEventKind{TraceKindDelegation, TraceKindMemoryRecall, TraceKindTurn, TraceKindVerification}
	for i, want := range expected {
		if resp.Events[i].Kind != want {
			t.Errorf("event[%d]: expected %s, got %s", i, want, resp.Events[i].Kind)
		}
	}
}

func TestAssembleTaskTrace_RunFilter(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "turn-1", RunID: "r1", EndedAtMS: 1000, Outcome: "success"},
			{TurnID: "turn-2", RunID: "r2", EndedAtMS: 2000, Outcome: "success"},
		},
		MemoryRecall: []state.MemoryRecallSample{
			{RecordedAtMS: 500, RunID: "r1"},
			{RecordedAtMS: 1500, RunID: "r2"},
		},
	}
	resp := AssembleTaskTrace(input, "r1", 200)
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events for r1, got %d", len(resp.Events))
	}
	if resp.RunID != "r1" {
		t.Fatalf("expected run_id r1, got %q", resp.RunID)
	}
}

func TestAssembleTaskTrace_Limit(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "t1", EndedAtMS: 1000},
			{TurnID: "t2", EndedAtMS: 2000},
			{TurnID: "t3", EndedAtMS: 3000},
		},
	}
	resp := AssembleTaskTrace(input, "", 2)
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events (limited), got %d", len(resp.Events))
	}
	if !resp.Truncated {
		t.Fatal("expected truncated=true")
	}
	// Limit returns the most recent N events (tail).
	if resp.Events[0].Turn.TurnID != "t2" {
		t.Fatalf("expected most recent events; first should be t2, got %q", resp.Events[0].Turn.TurnID)
	}
	if resp.Events[1].Turn.TurnID != "t3" {
		t.Fatalf("expected t3 as last, got %q", resp.Events[1].Turn.TurnID)
	}
}

func TestAssembleTaskTrace_Empty(t *testing.T) {
	input := TraceInput{Task: baseTask()}
	resp := AssembleTaskTrace(input, "", 200)
	if len(resp.Events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(resp.Events))
	}
	if resp.Truncated {
		t.Fatal("expected truncated=false")
	}
}

func TestAssembleTaskTrace_GoalIDPropagation(t *testing.T) {
	task := baseTask()
	task.GoalID = "goal-42"
	input := TraceInput{
		Task: task,
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "t1", EndedAtMS: 1000},
		},
		MemoryRecall: []state.MemoryRecallSample{
			{RecordedAtMS: 500},
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if resp.GoalID != "goal-42" {
		t.Fatalf("expected goal-42, got %q", resp.GoalID)
	}
	for i, evt := range resp.Events {
		if evt.GoalID != "goal-42" {
			t.Errorf("event[%d] goal_id = %q, want goal-42", i, evt.GoalID)
		}
	}
}

func TestAssembleTaskTrace_TurnErrorSummary(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "t1", EndedAtMS: 1000, Error: "context cancelled"},
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if resp.Events[0].Summary != "turn:error" {
		t.Fatalf("expected turn:error summary, got %q", resp.Events[0].Summary)
	}
	if resp.Events[0].Turn.Error != "context cancelled" {
		t.Fatalf("expected error propagated, got %q", resp.Events[0].Turn.Error)
	}
}

// ─── JSON shape ──────────────────────────────────────────────────────────────

func TestTasksTraceResponse_JSONShape(t *testing.T) {
	input := TraceInput{
		Task: baseTask(),
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "turn-1", RunID: "r1", EndedAtMS: 1000, Outcome: "success", DurationMS: 500},
		},
		MemoryRecall: []state.MemoryRecallSample{
			{RecordedAtMS: 800, TurnID: "turn-1", RunID: "r1", Strategy: "deterministic", InjectedAny: true},
		},
		VerificationEvents: []planner.VerificationEvent{
			{Type: planner.VerifEventCheckPass, TaskID: "t1", RunID: "r1", CheckID: "c1", CreatedAt: 2},
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"task_id", "goal_id", "events"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}
	eventsArr, ok := raw["events"].([]any)
	if !ok || len(eventsArr) != 3 {
		t.Fatalf("expected 3 events array, got %v", raw["events"])
	}
	// First event (memory_recall at 800ms) should have memory_recall detail.
	evt0, ok := eventsArr[0].(map[string]any)
	if !ok {
		t.Fatal("expected event object")
	}
	if _, ok := evt0["kind"]; !ok {
		t.Error("missing kind field")
	}
	if _, ok := evt0["ts"]; !ok {
		t.Error("missing ts field")
	}
	if _, ok := evt0["summary"]; !ok {
		t.Error("missing summary field")
	}
}

func TestAssembleTaskTrace_StableKindOrderOnTimeTie(t *testing.T) {
	// When events have the same timestamp, kind ordering should be deterministic.
	input := TraceInput{
		Task: baseTask(),
		TurnTelemetry: []state.TurnTelemetry{
			{TurnID: "t1", EndedAtMS: 1000, Outcome: "success"},
		},
		MemoryRecall: []state.MemoryRecallSample{
			{RecordedAtMS: 1000},
		},
		VerificationEvents: []planner.VerificationEvent{
			{Type: planner.VerifEventStarted, TaskID: "t1", CreatedAt: 1}, // 1000ms
		},
		WorkerEvents: []planner.WorkerEvent{
			{EventID: "e1", TaskID: "t1", WorkerID: "w1", State: planner.WorkerStatePending, CreatedAt: 1}, // 1000ms
		},
	}
	resp := AssembleTaskTrace(input, "", 200)
	if len(resp.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(resp.Events))
	}
	// All at 1000ms — order by kind: turn(0) < tool(1) < memory(2) < verification(3) < delegation(4)
	expected := []TraceEventKind{TraceKindTurn, TraceKindMemoryRecall, TraceKindVerification, TraceKindDelegation}
	for i, want := range expected {
		if resp.Events[i].Kind != want {
			t.Errorf("event[%d]: expected %s, got %s (stable kind order on tie)", i, want, resp.Events[i].Kind)
		}
	}
}
