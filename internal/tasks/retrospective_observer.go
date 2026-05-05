package tasks

import (
	"context"
	"strings"
	"time"

	"metiq/internal/planner"
	"metiq/internal/store/state"
)

// RetroDocsRepo captures the docs repository methods used by the retrospective observer.
type RetroDocsRepo interface {
	GetTask(ctx context.Context, taskID string) (state.TaskSpec, error)
	PutRetrospective(ctx context.Context, doc state.Retrospective) (state.Event, error)
	ListRetrospectivesByRun(ctx context.Context, runID string, limit int) ([]state.Retrospective, error)
	ListRetrospectivesByTask(ctx context.Context, taskID string, limit int) ([]state.Retrospective, error)
}

// RetroObserverConfig controls automatic retrospective generation.
type RetroObserverConfig struct {
	Enabled        bool
	Policy         planner.RetroPolicy
	IDPrefix       string
	MinTaskSpacing time.Duration
}

// RetrospectiveObserver auto-generates and persists retrospectives on terminal run updates.
type RetrospectiveObserver struct {
	docsRepo RetroDocsRepo
	engine   *planner.RetrospectiveEngine
	cfg      RetroObserverConfig
}

// AddRetrospectiveObserver constructs and registers a retrospective observer on
// the ledger, returning the observer for optional further inspection.
func (l *Ledger) AddRetrospectiveObserver(docsRepo RetroDocsRepo, cfg RetroObserverConfig) *RetrospectiveObserver {
	if l == nil || docsRepo == nil {
		return nil
	}
	obs := NewRetrospectiveObserver(docsRepo, cfg)
	l.AddObserver(obs)
	return obs
}

func NewRetrospectiveObserver(docsRepo RetroDocsRepo, cfg RetroObserverConfig) *RetrospectiveObserver {
	if cfg.Policy == (planner.RetroPolicy{}) {
		cfg.Policy = planner.DefaultRetroPolicy()
	}
	if cfg.MinTaskSpacing <= 0 {
		cfg.MinTaskSpacing = time.Hour
	}
	return &RetrospectiveObserver{
		docsRepo: docsRepo,
		engine:   planner.NewRetrospectiveEngine(cfg.IDPrefix),
		cfg:      cfg,
	}
}

func (o *RetrospectiveObserver) OnTaskCreated(context.Context, LedgerEntry)                       {}
func (o *RetrospectiveObserver) OnTaskUpdated(context.Context, LedgerEntry, state.TaskTransition) {}
func (o *RetrospectiveObserver) OnRunCreated(context.Context, RunEntry)                           {}

func (o *RetrospectiveObserver) OnRunUpdated(ctx context.Context, entry RunEntry, transition state.TaskRunTransition) {
	if o == nil || o.docsRepo == nil || o.engine == nil {
		return
	}
	if !isTerminalRunStatus(transition.To) {
		return
	}

	task, err := o.docsRepo.GetTask(ctx, entry.Run.TaskID)
	if err != nil {
		return
	}
	if !o.shouldAutoGenerate(task) {
		return
	}
	policy := o.policyForTask(task)
	if !planner.ShouldGenerate(policy, entry.Run) {
		return
	}

	// De-duplicate by run.
	if existing, err := o.docsRepo.ListRetrospectivesByRun(ctx, entry.Run.RunID, 1); err == nil && len(existing) > 0 {
		return
	}

	// Bound noise: at most one auto retro per task within MinTaskSpacing.
	if o.cfg.MinTaskSpacing > 0 {
		if existing, err := o.docsRepo.ListRetrospectivesByTask(ctx, entry.Run.TaskID, 1); err == nil && len(existing) > 0 {
			latest := existing[0]
			if latest.CreatedAt > 0 && transition.At-latest.CreatedAt < int64(o.cfg.MinTaskSpacing/time.Second) {
				return
			}
		}
	}

	retro, err := o.engine.GenerateValidated(planner.RetroInput{Run: entry.Run}, transition.At)
	if err != nil {
		return
	}
	_, _ = o.docsRepo.PutRetrospective(ctx, retro)
}

func (o *RetrospectiveObserver) shouldAutoGenerate(task state.TaskSpec) bool {
	if enabled, ok := taskLearningAutoRetrospective(task); ok {
		return enabled
	}
	return o.cfg.Enabled
}

func (o *RetrospectiveObserver) policyForTask(task state.TaskSpec) planner.RetroPolicy {
	policy := o.cfg.Policy
	lcRaw, ok := task.Meta["learning_config"]
	if !ok {
		return policy
	}
	lc, ok := lcRaw.(map[string]any)
	if !ok {
		return policy
	}
	if v, ok := lc["retro_on_run_completed"].(bool); ok {
		policy.OnRunCompleted = v
	}
	if v, ok := lc["retro_on_run_failed"].(bool); ok {
		policy.OnRunFailed = v
	}
	if v, ok := lc["retro_on_budget_exhausted"].(bool); ok {
		policy.OnBudgetExhausted = v
	}
	if v, ok := lc["retro_on_verification_failed"].(bool); ok {
		policy.OnVerificationFailed = v
	}
	if v, ok := asInt64(lc["retro_min_duration_ms"]); ok && v >= 0 {
		policy.MinDurationMS = v
	}
	return policy
}

func taskLearningAutoRetrospective(task state.TaskSpec) (bool, bool) {
	if task.Meta == nil {
		return false, false
	}
	raw, ok := task.Meta["learning_config"]
	if !ok {
		return false, false
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return false, false
	}
	if v, ok := cfg["auto_retrospective"].(bool); ok {
		return v, true
	}
	if v, ok := cfg["autoRetrospective"].(bool); ok {
		return v, true
	}
	if v, ok := cfg["auto_retrospective"].(string); ok {
		lower := strings.ToLower(strings.TrimSpace(v))
		if lower == "true" {
			return true, true
		}
		if lower == "false" {
			return false, true
		}
	}
	return false, false
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
