// budget_status.go exposes budget status, exhaustion history, and controls
// for operator surfaces. It provides structured payloads for status/control
// APIs and correlated events for budget consumption.
package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Budget status ──────────────────────────────────────────────────────────────

// BudgetStatus is the operator-facing view of a task/run's budget state.
type BudgetStatus struct {
	// TaskID identifies the task.
	TaskID string `json:"task_id"`
	// RunID identifies the run (empty for task-level view).
	RunID string `json:"run_id,omitempty"`
	// Budget is the effective budget.
	Budget state.TaskBudget `json:"budget"`
	// Usage is the current cumulative usage.
	Usage state.TaskUsage `json:"usage"`
	// Remaining is the remaining budget capacity.
	Remaining state.TaskBudget `json:"remaining"`
	// Exceeded lists which dimensions are exceeded.
	Exceeded state.BudgetExceeded `json:"exceeded"`
	// PercentUsed maps dimension names to percentage consumed (0–100+).
	PercentUsed map[string]float64 `json:"percent_used"`
	// Status is a human-readable summary.
	Status string `json:"status"`
	// ExhaustionHistory lists past exhaustion events.
	ExhaustionHistory []ExhaustionEvent `json:"exhaustion_history,omitempty"`
	// Source describes the data quality.
	Source SourceBreakdown `json:"source"`
	// ComputedAt is the Unix timestamp of this snapshot.
	ComputedAt int64 `json:"computed_at"`
}

// ── Budget tracker ─────────────────────────────────────────────────────────────

// BudgetTracker maintains per-task/run budget state for operator inspection.
// It wraps a UsageCollector and adds exhaustion history.
//
// Thread safety: BudgetTracker uses its own RWMutex to protect budget and
// exhaustion state. The embedded UsageCollector is independently thread-safe
// (it has its own sync.Mutex), so RecordTurn and Status can be called
// concurrently without BudgetTracker holding its lock for collector access.
type BudgetTracker struct {
	mu          sync.RWMutex     // protects budget, exhaustions only
	taskID      string
	runID       string
	budget      state.TaskBudget
	collector   *UsageCollector  // independently thread-safe via its own sync.Mutex
	exhaustions []ExhaustionEvent
}

// NewBudgetTracker creates a tracker for a specific task/run.
func NewBudgetTracker(taskID, runID string, budget state.TaskBudget) *BudgetTracker {
	return &BudgetTracker{
		taskID:    taskID,
		runID:     runID,
		budget:    budget,
		collector: NewUsageCollector(runID, taskID),
	}
}

// RecordTurn delegates to the underlying UsageCollector.
func (bt *BudgetTracker) RecordTurn(turn TurnUsage) {
	bt.collector.RecordTurn(turn)
}

// RecordExhaustion adds an exhaustion event to the history.
func (bt *BudgetTracker) RecordExhaustion(event ExhaustionEvent) {
	bt.mu.Lock()
	bt.exhaustions = append(bt.exhaustions, event)
	bt.mu.Unlock()
}

// Collector returns the underlying UsageCollector.
func (bt *BudgetTracker) Collector() *UsageCollector {
	return bt.collector
}

// Budget returns the effective budget.
func (bt *BudgetTracker) Budget() state.TaskBudget {
	return bt.budget
}

// UpdateBudget allows operators to adjust the budget (e.g. increase after
// escalation). This is the control surface for budget modifications.
func (bt *BudgetTracker) UpdateBudget(newBudget state.TaskBudget) {
	bt.mu.Lock()
	bt.budget = newBudget
	bt.mu.Unlock()
}

// Status computes and returns the current budget status snapshot.
func (bt *BudgetTracker) Status() BudgetStatus {
	bt.mu.RLock()
	budget := bt.budget
	history := make([]ExhaustionEvent, len(bt.exhaustions))
	copy(history, bt.exhaustions)
	bt.mu.RUnlock()

	usage := bt.collector.Cumulative()
	exceeded := budget.CheckUsage(usage)
	remaining := budget.Remaining(usage)
	percentUsed := computePercentUsed(budget, usage)
	source := bt.collector.Breakdown()

	statusStr := "OK"
	if exceeded.Any() {
		statusStr = "EXCEEDED"
	} else if hasHighUsage(percentUsed) {
		statusStr = "WARNING"
	} else if budget.IsZero() {
		statusStr = "NO_BUDGET"
	}

	return BudgetStatus{
		TaskID:            bt.taskID,
		RunID:             bt.runID,
		Budget:            budget,
		Usage:             usage,
		Remaining:         remaining,
		Exceeded:          exceeded,
		PercentUsed:       percentUsed,
		Status:            statusStr,
		ExhaustionHistory: history,
		Source:             source,
		ComputedAt:        time.Now().Unix(),
	}
}

// ── Budget registry ────────────────────────────────────────────────────────────

// BudgetRegistry maintains trackers for multiple tasks/runs and supports
// inspection by task, run, or globally.
type BudgetRegistry struct {
	mu       sync.RWMutex
	trackers map[string]*BudgetTracker // keyed by "taskID" or "taskID:runID"
}

// NewBudgetRegistry creates an empty registry.
func NewBudgetRegistry() *BudgetRegistry {
	return &BudgetRegistry{
		trackers: make(map[string]*BudgetTracker),
	}
}

// Register adds a tracker for a task/run.
func (r *BudgetRegistry) Register(tracker *BudgetTracker) {
	r.mu.Lock()
	key := trackerKey(tracker.taskID, tracker.runID)
	r.trackers[key] = tracker
	r.mu.Unlock()
}

// Get returns the tracker for a task/run, or nil if not found.
func (r *BudgetRegistry) Get(taskID, runID string) *BudgetTracker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.trackers[trackerKey(taskID, runID)]
}

// StatusAll returns status snapshots for all tracked tasks/runs.
func (r *BudgetRegistry) StatusAll() []BudgetStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	statuses := make([]BudgetStatus, 0, len(r.trackers))
	for _, tracker := range r.trackers {
		statuses = append(statuses, tracker.Status())
	}
	return statuses
}

// StatusForTask returns all statuses for a specific task (all runs).
func (r *BudgetRegistry) StatusForTask(taskID string) []BudgetStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var statuses []BudgetStatus
	prefix := taskID + ":"
	for key, tracker := range r.trackers {
		if key == taskID || strings.HasPrefix(key, prefix) {
			statuses = append(statuses, tracker.Status())
		}
	}
	return statuses
}

// ExceededTasks returns statuses only for tasks/runs that have exceeded budgets.
func (r *BudgetRegistry) ExceededTasks() []BudgetStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var statuses []BudgetStatus
	for _, tracker := range r.trackers {
		status := tracker.Status()
		if status.Exceeded.Any() {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

// Remove removes a tracker from the registry.
func (r *BudgetRegistry) Remove(taskID, runID string) {
	r.mu.Lock()
	delete(r.trackers, trackerKey(taskID, runID))
	r.mu.Unlock()
}

// Count returns the number of tracked tasks/runs.
func (r *BudgetRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.trackers)
}

func trackerKey(taskID, runID string) string {
	if runID == "" {
		return taskID
	}
	return taskID + ":" + runID
}

// ── Percentage computation ─────────────────────────────────────────────────────

func computePercentUsed(budget state.TaskBudget, usage state.TaskUsage) map[string]float64 {
	pct := make(map[string]float64)
	if budget.MaxTotalTokens > 0 {
		pct["total_tokens"] = pctOf(usage.TotalTokens, budget.MaxTotalTokens)
	}
	if budget.MaxPromptTokens > 0 {
		pct["prompt_tokens"] = pctOf(usage.PromptTokens, budget.MaxPromptTokens)
	}
	if budget.MaxCompletionTokens > 0 {
		pct["completion_tokens"] = pctOf(usage.CompletionTokens, budget.MaxCompletionTokens)
	}
	if budget.MaxToolCalls > 0 {
		pct["tool_calls"] = pctOf(usage.ToolCalls, budget.MaxToolCalls)
	}
	if budget.MaxDelegations > 0 {
		pct["delegations"] = pctOf(usage.Delegations, budget.MaxDelegations)
	}
	if budget.MaxRuntimeMS > 0 {
		pct["runtime_ms"] = pctOf64(usage.WallClockMS, budget.MaxRuntimeMS)
	}
	if budget.MaxCostMicrosUSD > 0 {
		pct["cost_micros_usd"] = pctOf64(usage.CostMicrosUSD, budget.MaxCostMicrosUSD)
	}
	return pct
}

func pctOf(used, max int) float64 {
	if max == 0 {
		return 0
	}
	return float64(used) / float64(max) * 100
}

func pctOf64(used, max int64) float64 {
	if max == 0 {
		return 0
	}
	return float64(used) / float64(max) * 100
}

func hasHighUsage(pct map[string]float64) bool {
	for _, v := range pct {
		if v >= 80 {
			return true
		}
	}
	return false
}

// ── Formatting ─────────────────────────────────────────────────────────────────

// FormatBudgetStatus returns a human-readable budget status summary.
func FormatBudgetStatus(status BudgetStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Budget status for %s", status.TaskID)
	if status.RunID != "" {
		fmt.Fprintf(&b, " (run: %s)", status.RunID)
	}
	fmt.Fprintf(&b, " [%s]\n", status.Status)

	if status.Budget.IsZero() {
		b.WriteString("  No budget configured\n")
		return b.String()
	}

	for dim, pct := range status.PercentUsed {
		marker := ""
		if pct >= 100 {
			marker = " ⛔ EXCEEDED"
		} else if pct >= 80 {
			marker = " ⚠️ WARNING"
		}
		fmt.Fprintf(&b, "  %s: %.1f%%%s\n", dim, pct, marker)
	}

	if len(status.ExhaustionHistory) > 0 {
		fmt.Fprintf(&b, "  Exhaustion events: %d\n", len(status.ExhaustionHistory))
		// Show last 3.
		start := 0
		if len(status.ExhaustionHistory) > 3 {
			start = len(status.ExhaustionHistory) - 3
		}
		for _, e := range status.ExhaustionHistory[start:] {
			fmt.Fprintf(&b, "    [%s] %s → %s\n", e.EventID, e.Reasons, e.Action)
		}
	}

	return b.String()
}
