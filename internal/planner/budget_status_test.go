package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── BudgetTracker tests ────────────────────────────────────────────────────────

func TestBudgetTracker_StatusOK(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{
		MaxTotalTokens: 100000,
		MaxToolCalls:   50,
	})
	bt.RecordTurn(TurnUsage{InputTokens: 2000, OutputTokens: 1000, ToolCalls: 3})
	status := bt.Status()
	if status.Status != "OK" {
		t.Errorf("expected OK, got %s", status.Status)
	}
	if status.TaskID != "task-1" || status.RunID != "run-1" {
		t.Error("should carry task/run IDs")
	}
	if len(status.PercentUsed) == 0 {
		t.Error("should have percent used")
	}
}

func TestBudgetTracker_StatusExceeded(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 5000})
	bt.RecordTurn(TurnUsage{InputTokens: 4000, OutputTokens: 3000})
	status := bt.Status()
	if status.Status != "EXCEEDED" {
		t.Errorf("expected EXCEEDED, got %s", status.Status)
	}
	if !status.Exceeded.TotalTokens {
		t.Error("total tokens should be exceeded")
	}
}

func TestBudgetTracker_StatusWarning(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 10000})
	bt.RecordTurn(TurnUsage{InputTokens: 4500, OutputTokens: 4000}) // 8500/10000 = 85%
	status := bt.Status()
	if status.Status != "WARNING" {
		t.Errorf("expected WARNING, got %s", status.Status)
	}
}

func TestBudgetTracker_StatusNoBudget(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{})
	bt.RecordTurn(TurnUsage{InputTokens: 1000})
	status := bt.Status()
	if status.Status != "NO_BUDGET" {
		t.Errorf("expected NO_BUDGET, got %s", status.Status)
	}
}

func TestBudgetTracker_RecordExhaustion(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 5000})
	event := ExhaustionEvent{
		EventID: "exhaust-1",
		Reasons: []ExhaustionReason{ExhaustionTokens},
		Action:  ActionFallback,
	}
	bt.RecordExhaustion(event)
	status := bt.Status()
	if len(status.ExhaustionHistory) != 1 {
		t.Errorf("expected 1 exhaustion event, got %d", len(status.ExhaustionHistory))
	}
}

func TestBudgetTracker_UpdateBudget(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 5000})
	bt.UpdateBudget(state.TaskBudget{MaxTotalTokens: 20000})
	if bt.Budget().MaxTotalTokens != 20000 {
		t.Errorf("budget should be updated to 20000, got %d", bt.Budget().MaxTotalTokens)
	}
}

func TestBudgetTracker_Collector(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{})
	if bt.Collector() == nil {
		t.Error("collector should not be nil")
	}
}

// ── PercentUsed tests ──────────────────────────────────────────────────────────

func TestPercentUsed_AllDimensions(t *testing.T) {
	budget := state.TaskBudget{
		MaxTotalTokens:  10000,
		MaxToolCalls:    20,
		MaxDelegations:  5,
		MaxCostMicrosUSD: 1000000,
	}
	usage := state.TaskUsage{
		TotalTokens:  5000,
		ToolCalls:    10,
		Delegations:  2,
		CostMicrosUSD: 500000,
	}
	pct := computePercentUsed(budget, usage)
	if pct["total_tokens"] != 50 {
		t.Errorf("total_tokens: expected 50, got %f", pct["total_tokens"])
	}
	if pct["tool_calls"] != 50 {
		t.Errorf("tool_calls: expected 50, got %f", pct["tool_calls"])
	}
	if pct["delegations"] != 40 {
		t.Errorf("delegations: expected 40, got %f", pct["delegations"])
	}
	if pct["cost_micros_usd"] != 50 {
		t.Errorf("cost_micros_usd: expected 50, got %f", pct["cost_micros_usd"])
	}
}

func TestPercentUsed_UnlimitedOmitted(t *testing.T) {
	budget := state.TaskBudget{MaxTotalTokens: 10000}
	usage := state.TaskUsage{TotalTokens: 5000, ToolCalls: 100}
	pct := computePercentUsed(budget, usage)
	if _, ok := pct["tool_calls"]; ok {
		t.Error("unlimited dimension should be omitted")
	}
	if _, ok := pct["total_tokens"]; !ok {
		t.Error("set dimension should be present")
	}
}

func TestPercentUsed_OverHundred(t *testing.T) {
	budget := state.TaskBudget{MaxTotalTokens: 10000}
	usage := state.TaskUsage{TotalTokens: 15000}
	pct := computePercentUsed(budget, usage)
	if pct["total_tokens"] != 150 {
		t.Errorf("expected 150%%, got %f", pct["total_tokens"])
	}
}

// ── BudgetRegistry tests ───────────────────────────────────────────────────────

func TestBudgetRegistry_RegisterAndGet(t *testing.T) {
	reg := NewBudgetRegistry()
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 10000})
	reg.Register(bt)

	got := reg.Get("task-1", "run-1")
	if got == nil {
		t.Fatal("should find registered tracker")
	}
	if got.taskID != "task-1" {
		t.Errorf("task ID mismatch: %s", got.taskID)
	}
}

func TestBudgetRegistry_GetMissing(t *testing.T) {
	reg := NewBudgetRegistry()
	got := reg.Get("nonexistent", "")
	if got != nil {
		t.Error("should return nil for missing")
	}
}

func TestBudgetRegistry_StatusAll(t *testing.T) {
	reg := NewBudgetRegistry()
	reg.Register(NewBudgetTracker("t1", "r1", state.TaskBudget{MaxTotalTokens: 10000}))
	reg.Register(NewBudgetTracker("t2", "r2", state.TaskBudget{MaxToolCalls: 20}))

	statuses := reg.StatusAll()
	if len(statuses) != 2 {
		t.Errorf("expected 2, got %d", len(statuses))
	}
}

func TestBudgetRegistry_StatusForTask(t *testing.T) {
	reg := NewBudgetRegistry()
	reg.Register(NewBudgetTracker("t1", "r1", state.TaskBudget{}))
	reg.Register(NewBudgetTracker("t1", "r2", state.TaskBudget{}))
	reg.Register(NewBudgetTracker("t2", "r1", state.TaskBudget{}))

	statuses := reg.StatusForTask("t1")
	if len(statuses) != 2 {
		t.Errorf("expected 2 for t1, got %d", len(statuses))
	}
}

func TestBudgetRegistry_ExceededTasks(t *testing.T) {
	reg := NewBudgetRegistry()
	ok := NewBudgetTracker("t1", "r1", state.TaskBudget{MaxTotalTokens: 100000})
	ok.RecordTurn(TurnUsage{InputTokens: 100})
	reg.Register(ok)

	exceeded := NewBudgetTracker("t2", "r1", state.TaskBudget{MaxTotalTokens: 5000})
	exceeded.RecordTurn(TurnUsage{InputTokens: 4000, OutputTokens: 3000})
	reg.Register(exceeded)

	results := reg.ExceededTasks()
	if len(results) != 1 {
		t.Errorf("expected 1 exceeded, got %d", len(results))
	}
	if results[0].TaskID != "t2" {
		t.Errorf("expected t2, got %s", results[0].TaskID)
	}
}

func TestBudgetRegistry_Remove(t *testing.T) {
	reg := NewBudgetRegistry()
	reg.Register(NewBudgetTracker("t1", "r1", state.TaskBudget{}))
	reg.Remove("t1", "r1")
	if reg.Count() != 0 {
		t.Errorf("expected 0 after remove, got %d", reg.Count())
	}
}

func TestBudgetRegistry_Count(t *testing.T) {
	reg := NewBudgetRegistry()
	if reg.Count() != 0 {
		t.Error("empty registry should have count 0")
	}
	reg.Register(NewBudgetTracker("t1", "", state.TaskBudget{}))
	if reg.Count() != 1 {
		t.Errorf("expected 1, got %d", reg.Count())
	}
}

// ── Concurrency tests ──────────────────────────────────────────────────────────

func TestBudgetTracker_ConcurrentAccess(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 1000000})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bt.RecordTurn(TurnUsage{InputTokens: 100, OutputTokens: 50})
			bt.RecordExhaustion(ExhaustionEvent{EventID: "e"})
			_ = bt.Status()
			bt.UpdateBudget(state.TaskBudget{MaxTotalTokens: 2000000})
		}()
	}
	wg.Wait()
}

func TestBudgetRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewBudgetRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			taskID := "task"
			runID := string(rune('a' + i%26))
			bt := NewBudgetTracker(taskID, runID, state.TaskBudget{MaxTotalTokens: 10000})
			reg.Register(bt)
			_ = reg.Get(taskID, runID)
			_ = reg.StatusAll()
			_ = reg.Count()
		}(i)
	}
	wg.Wait()
}

// ── Formatting tests ───────────────────────────────────────────────────────────

func TestFormatBudgetStatus_OK(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 100000})
	bt.RecordTurn(TurnUsage{InputTokens: 2000, OutputTokens: 1000})
	output := FormatBudgetStatus(bt.Status())
	if !strings.Contains(output, "task-1") {
		t.Error("should include task ID")
	}
	if !strings.Contains(output, "OK") {
		t.Error("should show OK status")
	}
}

func TestFormatBudgetStatus_Exceeded(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 5000})
	bt.RecordTurn(TurnUsage{InputTokens: 4000, OutputTokens: 3000})
	output := FormatBudgetStatus(bt.Status())
	if !strings.Contains(output, "EXCEEDED") {
		t.Error("should show EXCEEDED")
	}
}

func TestFormatBudgetStatus_NoBudget(t *testing.T) {
	bt := NewBudgetTracker("task-1", "", state.TaskBudget{})
	output := FormatBudgetStatus(bt.Status())
	if !strings.Contains(output, "No budget") {
		t.Error("should note no budget configured")
	}
}

func TestFormatBudgetStatus_WithExhaustionHistory(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{MaxTotalTokens: 5000})
	bt.RecordExhaustion(ExhaustionEvent{
		EventID: "exhaust-1",
		Reasons: []ExhaustionReason{ExhaustionTokens},
		Action:  ActionFallback,
	})
	output := FormatBudgetStatus(bt.Status())
	if !strings.Contains(output, "Exhaustion events") {
		t.Error("should show exhaustion history")
	}
}

// ── JSON round-trip ────────────────────────────────────────────────────────────

func TestBudgetStatus_JSONRoundTrip(t *testing.T) {
	bt := NewBudgetTracker("task-1", "run-1", state.TaskBudget{
		MaxTotalTokens: 10000, MaxToolCalls: 20,
	})
	bt.RecordTurn(TurnUsage{InputTokens: 2000, OutputTokens: 1000, ToolCalls: 5})
	status := bt.Status()

	blob, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded BudgetStatus
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.TaskID != status.TaskID || decoded.Status != status.Status {
		t.Errorf("round-trip mismatch: got %+v", decoded)
	}
	if len(decoded.PercentUsed) != len(status.PercentUsed) {
		t.Errorf("percent used mismatch: got %d", len(decoded.PercentUsed))
	}
}

// ── hasHighUsage tests ─────────────────────────────────────────────────────────

func TestHasHighUsage_Below(t *testing.T) {
	if hasHighUsage(map[string]float64{"a": 50, "b": 70}) {
		t.Error("70% should not trigger high usage")
	}
}

func TestHasHighUsage_Above(t *testing.T) {
	if !hasHighUsage(map[string]float64{"a": 50, "b": 85}) {
		t.Error("85% should trigger high usage")
	}
}

func TestHasHighUsage_Empty(t *testing.T) {
	if hasHighUsage(map[string]float64{}) {
		t.Error("empty should not trigger")
	}
}
