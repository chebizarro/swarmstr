package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestTraceContext_IsZero(t *testing.T) {
	var tc TraceContext
	if !tc.IsZero() {
		t.Fatal("zero value should be zero")
	}
	tc.TaskID = "task-1"
	if tc.IsZero() {
		t.Fatal("non-zero TaskID should make it non-zero")
	}
}

func TestTraceContext_WithStep(t *testing.T) {
	tc := TraceContext{GoalID: "g1", TaskID: "t1", RunID: "r1"}
	stepped := tc.WithStep("step-A")
	if stepped.StepID != "step-A" {
		t.Fatalf("expected step-A, got %q", stepped.StepID)
	}
	if stepped.GoalID != "g1" || stepped.TaskID != "t1" || stepped.RunID != "r1" {
		t.Fatalf("WithStep should preserve other fields: %+v", stepped)
	}
	// Original unchanged.
	if tc.StepID != "" {
		t.Fatal("original should be unchanged")
	}
}

func TestTraceContext_Child(t *testing.T) {
	parent := TraceContext{GoalID: "g1", TaskID: "parent-t", RunID: "parent-r"}
	child := parent.Child("child-t", "child-r")
	if child.GoalID != "g1" {
		t.Fatalf("child should inherit GoalID, got %q", child.GoalID)
	}
	if child.TaskID != "child-t" || child.RunID != "child-r" {
		t.Fatalf("child IDs wrong: %+v", child)
	}
	if child.ParentTaskID != "parent-t" || child.ParentRunID != "parent-r" {
		t.Fatalf("parent IDs wrong: %+v", child)
	}
	if child.StepID != "" {
		t.Fatal("child should not inherit StepID")
	}
}

func TestTraceContext_JSONRoundTrip(t *testing.T) {
	tc := TraceContext{
		GoalID:       "goal-1",
		TaskID:       "task-1",
		RunID:        "run-1",
		StepID:       "step-1",
		ParentTaskID: "ptask-1",
		ParentRunID:  "prun-1",
	}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded TraceContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != tc {
		t.Fatalf("round-trip mismatch: %+v vs %+v", decoded, tc)
	}
}

func TestTraceContext_JSONOmitsEmpty(t *testing.T) {
	tc := TraceContext{}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}" {
		t.Fatalf("zero TraceContext should marshal to {}, got %s", data)
	}
}

// ─── Propagation through tool lifecycle ──────────────────────────────────────

func TestToolLifecycleEvent_CarriesTrace(t *testing.T) {
	trace := TraceContext{TaskID: "t1", RunID: "r1", StepID: "s1"}
	var captured []ToolLifecycleEvent
	var mu sync.Mutex

	sink := ToolLifecycleSink(func(evt ToolLifecycleEvent) {
		mu.Lock()
		captured = append(captured, evt)
		mu.Unlock()
	})

	executor := ToolExecutorFunc(func(_ context.Context, call ToolCall) (string, error) {
		return "ok", nil
	})

	calls := []ToolCall{{ID: "call-1", Name: "test_tool", Args: map[string]any{}}}
	executeToolBatches(context.Background(), executor, calls, "sess-1", "turn-1", sink, trace)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) < 2 {
		t.Fatalf("expected at least start+result events, got %d", len(captured))
	}
	for _, evt := range captured {
		if evt.Trace.TaskID != "t1" {
			t.Fatalf("expected TaskID=t1 on event type=%s, got %q", evt.Type, evt.Trace.TaskID)
		}
		if evt.Trace.RunID != "r1" {
			t.Fatalf("expected RunID=r1 on event type=%s, got %q", evt.Type, evt.Trace.RunID)
		}
		if evt.Trace.StepID != "s1" {
			t.Fatalf("expected StepID=s1 on event type=%s, got %q", evt.Type, evt.Trace.StepID)
		}
	}
}

func TestToolLifecycleEvent_ZeroTraceWhenAbsent(t *testing.T) {
	var captured []ToolLifecycleEvent
	sink := ToolLifecycleSink(func(evt ToolLifecycleEvent) {
		captured = append(captured, evt)
	})

	executor := ToolExecutorFunc(func(_ context.Context, call ToolCall) (string, error) {
		return "ok", nil
	})

	calls := []ToolCall{{ID: "call-1", Name: "test_tool", Args: map[string]any{}}}
	executeToolBatches(context.Background(), executor, calls, "sess-1", "turn-1", sink, TraceContext{})

	if len(captured) < 2 {
		t.Fatalf("expected at least start+result events, got %d", len(captured))
	}
	for _, evt := range captured {
		if !evt.Trace.IsZero() {
			t.Fatalf("expected zero trace when none provided, got %+v", evt.Trace)
		}
	}
}

func TestToolLifecycleEvent_TraceJSONIncludesFields(t *testing.T) {
	evt := ToolLifecycleEvent{
		Type:       ToolLifecycleEventStart,
		TS:         1000,
		SessionID:  "s1",
		TurnID:     "t1",
		ToolCallID: "c1",
		ToolName:   "tool",
		Trace:      TraceContext{TaskID: "task-1", RunID: "run-1"},
	}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	trace, ok := m["trace"].(map[string]any)
	if !ok {
		t.Fatalf("expected trace object in JSON, got %v", m["trace"])
	}
	if trace["task_id"] != "task-1" {
		t.Fatalf("expected task_id in trace JSON, got %v", trace)
	}
}

func TestAgenticLoopConfig_PropagatesTrace(t *testing.T) {
	// Verify the Turn → AgenticLoopConfig → events propagation path.
	trace := TraceContext{GoalID: "g1", TaskID: "t1", RunID: "r1"}

	var captured []ToolLifecycleEvent
	var mu sync.Mutex
	sink := ToolLifecycleSink(func(evt ToolLifecycleEvent) {
		mu.Lock()
		captured = append(captured, evt)
		mu.Unlock()
	})

	// The generateWithAgenticLoop function internally builds config from Turn.
	// We test the config-level propagation directly.
	cfg := AgenticLoopConfig{
		SessionID:     "sess-1",
		TurnID:        "turn-1",
		ToolEventSink: sink,
		Trace:         trace,
	}

	executor := ToolExecutorFunc(func(_ context.Context, call ToolCall) (string, error) {
		return "result", nil
	})
	calls := []ToolCall{{ID: "c1", Name: "search", Args: map[string]any{}}}
	executeToolBatches(context.Background(), executor, calls, cfg.SessionID, cfg.TurnID, cfg.ToolEventSink, cfg.Trace)

	mu.Lock()
	defer mu.Unlock()
	for _, evt := range captured {
		if evt.Trace.GoalID != "g1" {
			t.Fatalf("expected GoalID=g1 propagated from config, got %+v", evt.Trace)
		}
	}
}

// ─── ToolExecutorFunc helper ─────────────────────────────────────────────────

// ToolExecutorFunc is a simple adapter to use a function as a ToolExecutor.
type ToolExecutorFunc func(context.Context, ToolCall) (string, error)

func (f ToolExecutorFunc) Execute(ctx context.Context, call ToolCall) (string, error) {
	return f(ctx, call)
}
