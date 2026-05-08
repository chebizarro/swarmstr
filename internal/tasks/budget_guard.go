package tasks

import (
	"context"
	"strings"
	"sync"

	"metiq/internal/store/state"
)

type budgetGuardContextKey struct{}

// BudgetGuard tracks the effective budget and measured usage for one task run.
// It is intentionally policy-free: callers decide how to translate an exceeded
// budget into task/run lifecycle outcomes.
type BudgetGuard struct {
	mu sync.Mutex

	taskID string
	runID  string
	budget state.TaskBudget

	// baseline is cumulative usage from other runs of the same task. current is
	// the usage measured for the guarded run itself.
	baseline state.TaskUsage
	current  state.TaskUsage
}

// NewBudgetGuard computes the effective task-run budget and initial cumulative
// usage. TaskSpec.Budget is currently the canonical run budget; prior run usage
// is aggregated separately so a task budget applies across attempts.
func NewBudgetGuard(task state.TaskSpec, run state.TaskRun, runs []state.TaskRun) *BudgetGuard {
	run = run.Normalize()
	return &BudgetGuard{
		taskID:   strings.TrimSpace(task.TaskID),
		runID:    strings.TrimSpace(run.RunID),
		budget:   EffectiveTaskRunBudget(task, run),
		baseline: AggregateTaskUsage(runs, run.RunID),
		current:  run.Usage,
	}
}

// EffectiveTaskRunBudget returns the budget that applies to a run. There is no
// run-level budget override today, so this normalizes to the task budget. Keeping
// this as a named helper makes the future inheritance/override seam explicit.
func EffectiveTaskRunBudget(task state.TaskSpec, _ state.TaskRun) state.TaskBudget {
	return task.Normalize().Budget
}

// AggregateTaskUsage sums usage for all supplied runs except excludeRunID.
func AggregateTaskUsage(runs []state.TaskRun, excludeRunID string) state.TaskUsage {
	excludeRunID = strings.TrimSpace(excludeRunID)
	var usage state.TaskUsage
	for _, run := range runs {
		if excludeRunID != "" && strings.TrimSpace(run.RunID) == excludeRunID {
			continue
		}
		usage.Add(run.Usage)
	}
	return usage
}

func (g *BudgetGuard) TaskID() string {
	if g == nil {
		return ""
	}
	return g.taskID
}

func (g *BudgetGuard) RunID() string {
	if g == nil {
		return ""
	}
	return g.runID
}

func (g *BudgetGuard) Budget() state.TaskBudget {
	if g == nil {
		return state.TaskBudget{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.budget
}

func (g *BudgetGuard) BaselineUsage() state.TaskUsage {
	if g == nil {
		return state.TaskUsage{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.baseline
}

func (g *BudgetGuard) CurrentUsage() state.TaskUsage {
	if g == nil {
		return state.TaskUsage{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current
}

// Usage returns cumulative task usage at this guard checkpoint.
func (g *BudgetGuard) Usage() state.TaskUsage {
	if g == nil {
		return state.TaskUsage{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return addUsage(g.baseline, g.current)
}

// SetCurrentUsage replaces the measured usage for this run.
func (g *BudgetGuard) SetCurrentUsage(usage state.TaskUsage) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.current = usage
	g.mu.Unlock()
}

// TryReserveUsage checks whether adding delta to this run would exceed budget.
// If allowed, delta is recorded and the returned exceeded value is empty.
func (g *BudgetGuard) TryReserveUsage(delta state.TaskUsage) (state.BudgetExceeded, bool) {
	if g == nil {
		return state.BudgetExceeded{}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.budget.IsZero() {
		g.current.Add(delta)
		return state.BudgetExceeded{}, true
	}
	projectedCurrent := g.current
	projectedCurrent.Add(delta)
	projected := addUsage(g.baseline, projectedCurrent)
	exceeded := g.budget.CheckUsage(projected)
	if exceeded.Any() {
		return exceeded, false
	}
	g.current = projectedCurrent
	return state.BudgetExceeded{}, true
}

func (g *BudgetGuard) Check() state.BudgetExceeded {
	if g == nil {
		return state.BudgetExceeded{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.budget.IsZero() {
		return state.BudgetExceeded{}
	}
	return g.budget.CheckUsage(addUsage(g.baseline, g.current))
}

// ContextWithBudgetGuard attaches the task-run budget guard to ctx for runtime
// helpers that can enforce or report budget consumption without extra plumbing.
func ContextWithBudgetGuard(ctx context.Context, guard *BudgetGuard) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, budgetGuardContextKey{}, guard)
}

func BudgetGuardFromContext(ctx context.Context) (*BudgetGuard, bool) {
	if ctx == nil {
		return nil, false
	}
	guard, ok := ctx.Value(budgetGuardContextKey{}).(*BudgetGuard)
	return guard, ok && guard != nil
}

func addUsage(a, b state.TaskUsage) state.TaskUsage {
	a.Add(b)
	return a
}
