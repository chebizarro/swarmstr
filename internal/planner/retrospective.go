package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Retrospective trigger policy ─────────────────────────────────────────────

// RetroPolicy controls when retrospectives are automatically generated.
type RetroPolicy struct {
	// OnRunCompleted generates a retro for every successfully completed run.
	OnRunCompleted bool
	// OnRunFailed generates a retro for failed runs.
	OnRunFailed bool
	// OnBudgetExhausted generates a retro when budget is fully consumed.
	OnBudgetExhausted bool
	// OnVerificationFailed generates a retro when verification checks fail.
	OnVerificationFailed bool
	// MinDurationMS skips retros for runs shorter than this threshold (0 = no minimum).
	MinDurationMS int64
}

// DefaultRetroPolicy returns a policy that fires on failures and budget exhaustion
// but not on every successful run (to avoid noise).
func DefaultRetroPolicy() RetroPolicy {
	return RetroPolicy{
		OnRunCompleted:       false,
		OnRunFailed:          true,
		OnBudgetExhausted:    true,
		OnVerificationFailed: true,
		MinDurationMS:        0,
	}
}

// AllRetroPolicy returns a policy that fires on every terminal run.
func AllRetroPolicy() RetroPolicy {
	return RetroPolicy{
		OnRunCompleted:       true,
		OnRunFailed:          true,
		OnBudgetExhausted:    true,
		OnVerificationFailed: true,
		MinDurationMS:        0,
	}
}

// ── Trigger + outcome classification ─────────────────────────────────────────

// DetermineTrigger inspects a completed run and returns the most specific trigger.
// For failed runs, priority: budget_exhausted > verification_failed > run_failed.
// For completed runs: verification_failed > run_completed.
// Budget exhaustion is only detected on failed runs (via error string heuristic).
func DetermineTrigger(run state.TaskRun) state.RetroTrigger {
	switch run.Status {
	case state.TaskRunStatusCompleted:
		if run.Verification.AnyRequiredFailed() {
			return state.RetroTriggerVerifyFailed
		}
		return state.RetroTriggerRunCompleted
	case state.TaskRunStatusFailed:
		if isBudgetError(run.Error) {
			return state.RetroTriggerBudgetExhausted
		}
		if run.Verification.AnyRequiredFailed() {
			return state.RetroTriggerVerifyFailed
		}
		return state.RetroTriggerRunFailed
	default:
		// Non-terminal; treat as failed if somehow called.
		return state.RetroTriggerRunFailed
	}
}

// ClassifyOutcome maps a run status + verification to a retrospective outcome.
func ClassifyOutcome(run state.TaskRun) state.RetroOutcome {
	switch run.Status {
	case state.TaskRunStatusCompleted:
		if run.Verification.AnyRequiredFailed() {
			return state.RetroOutcomePartial
		}
		return state.RetroOutcomeSuccess
	case state.TaskRunStatusFailed:
		return state.RetroOutcomeFailure
	default:
		return state.RetroOutcomeFailure
	}
}

// isBudgetError is a heuristic check for budget exhaustion in run error strings.
func isBudgetError(errStr string) bool {
	lower := strings.ToLower(errStr)
	return strings.Contains(lower, "budget") &&
		(strings.Contains(lower, "exhaust") || strings.Contains(lower, "exceed"))
}

// ── ShouldGenerate ───────────────────────────────────────────────────────────

// ShouldGenerate reports whether a retrospective should be created for the
// given run under the supplied policy.
func ShouldGenerate(policy RetroPolicy, run state.TaskRun) bool {
	// Check minimum duration.
	if policy.MinDurationMS > 0 && run.EndedAt > 0 && run.StartedAt > 0 {
		durationMS := (run.EndedAt - run.StartedAt) * 1000
		if durationMS < policy.MinDurationMS {
			return false
		}
	}

	trigger := DetermineTrigger(run)
	switch trigger {
	case state.RetroTriggerRunCompleted:
		return policy.OnRunCompleted
	case state.RetroTriggerRunFailed:
		return policy.OnRunFailed
	case state.RetroTriggerBudgetExhausted:
		return policy.OnBudgetExhausted
	case state.RetroTriggerVerifyFailed:
		return policy.OnVerificationFailed
	default:
		return false
	}
}

// ── Retrospective generation ─────────────────────────────────────────────────

// RetroInput bundles the data needed to generate a retrospective.
type RetroInput struct {
	Run          state.TaskRun
	Feedback     []state.FeedbackRecord
	Proposals    []state.PolicyProposal
	// WhatWorked / WhatFailed / Improvements can be supplied by the caller
	// (e.g. from an LLM summarisation step). If empty, the engine
	// derives them from structured data.
	WhatWorked   []string
	WhatFailed   []string
	Improvements []string
}

// RetrospectiveEngine builds retrospective records from completed runs.
// It is safe for concurrent use.
type RetrospectiveEngine struct {
	mu     sync.Mutex
	nextID int
	prefix string
}

// NewRetrospectiveEngine creates an engine with the given ID prefix.
func NewRetrospectiveEngine(prefix string) *RetrospectiveEngine {
	if prefix == "" {
		prefix = "retro"
	}
	return &RetrospectiveEngine{prefix: prefix}
}

func (e *RetrospectiveEngine) generateID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	return fmt.Sprintf("%s-%d", e.prefix, e.nextID)
}

// Generate creates a Retrospective from the supplied input.
// The returned record is validated and ready for persistence.
func (e *RetrospectiveEngine) Generate(input RetroInput, now int64) (state.Retrospective, error) {
	if now <= 0 {
		now = time.Now().Unix()
	}

	run := input.Run
	trigger := DetermineTrigger(run)
	outcome := ClassifyOutcome(run)

	// Collect feedback IDs.
	var feedbackIDs []string
	for _, fb := range input.Feedback {
		if fb.FeedbackID != "" {
			feedbackIDs = append(feedbackIDs, fb.FeedbackID)
		}
	}

	// Collect proposal IDs.
	var proposalIDs []string
	for _, p := range input.Proposals {
		if p.ProposalID != "" {
			proposalIDs = append(proposalIDs, p.ProposalID)
		}
	}

	// Derive what-worked / what-failed from structured data if not supplied.
	whatWorked := input.WhatWorked
	whatFailed := input.WhatFailed
	improvements := input.Improvements

	if len(whatWorked) == 0 && len(whatFailed) == 0 {
		whatWorked, whatFailed = deriveFromRun(run, input.Feedback)
	}
	if len(improvements) == 0 {
		improvements = deriveImprovements(input.Feedback, input.Proposals)
	}

	// Compute duration.  StartedAt/EndedAt are Unix seconds, so multiply
	// by 1000 to get milliseconds.  Prefer WallClockMS when available
	// since it is already in ms and more precise.
	var durationMS int64
	if run.EndedAt > 0 && run.StartedAt > 0 {
		durationMS = (run.EndedAt - run.StartedAt) * 1000
	}
	if run.Usage.WallClockMS > 0 {
		durationMS = run.Usage.WallClockMS
	}

	summary := buildSummary(run, trigger, outcome)

	retro := state.Retrospective{
		Version:      1,
		RetroID:      e.generateID(),
		GoalID:       run.GoalID,
		TaskID:       run.TaskID,
		RunID:        run.RunID,
		AgentID:      run.AgentID,
		Trigger:      trigger,
		Outcome:      outcome,
		Summary:      summary,
		WhatWorked:   whatWorked,
		WhatFailed:   whatFailed,
		Improvements: improvements,
		FeedbackIDs:  feedbackIDs,
		ProposalIDs:  proposalIDs,
		Usage:        run.Usage,
		DurationMS:   durationMS,
		CreatedAt:    now,
		CreatedBy:    "system",
	}

	retro = retro.Normalize()
	if err := retro.Validate(); err != nil {
		return state.Retrospective{}, fmt.Errorf("generate retrospective: %w", err)
	}
	return retro, nil
}

// GenerateValidated is like Generate but also returns an error if the run
// is not in a terminal state.
func (e *RetrospectiveEngine) GenerateValidated(input RetroInput, now int64) (state.Retrospective, error) {
	if !isTerminalRun(input.Run) {
		return state.Retrospective{}, fmt.Errorf("run %s is not terminal (status=%s)", input.Run.RunID, input.Run.Status)
	}
	return e.Generate(input, now)
}

// ── Derivation helpers ───────────────────────────────────────────────────────

func deriveFromRun(run state.TaskRun, feedback []state.FeedbackRecord) (worked, failed []string) {
	// Successes.
	if run.Status == state.TaskRunStatusCompleted {
		worked = append(worked, "Run completed successfully")
	}
	if run.Usage.TotalTokens > 0 {
		worked = append(worked, fmt.Sprintf("Used %d total tokens", run.Usage.TotalTokens))
	}
	passedChecks := 0
	failedChecks := 0
	for _, c := range run.Verification.Checks {
		switch c.Status {
		case state.VerificationStatusPassed:
			passedChecks++
		case state.VerificationStatusFailed:
			failedChecks++
		}
	}
	if passedChecks > 0 {
		worked = append(worked, fmt.Sprintf("%d verification check(s) passed", passedChecks))
	}

	// Failures.
	if run.Status == state.TaskRunStatusFailed {
		reason := "Run failed"
		if run.Error != "" {
			reason += ": " + truncateStr(run.Error, 120)
		}
		failed = append(failed, reason)
	}
	if failedChecks > 0 {
		failed = append(failed, fmt.Sprintf("%d verification check(s) failed", failedChecks))
	}

	// Feedback-derived failures.
	for _, fb := range feedback {
		if fb.Severity == state.FeedbackSeverityError || fb.Severity == state.FeedbackSeverityCritical {
			failed = append(failed, truncateStr(fb.Summary, 100))
		}
	}

	return worked, failed
}

func deriveImprovements(feedback []state.FeedbackRecord, proposals []state.PolicyProposal) []string {
	var improvements []string
	// Each proposal is a concrete improvement suggestion.
	for _, p := range proposals {
		if p.Title != "" {
			improvements = append(improvements, "Proposal: "+truncateStr(p.Title, 100))
		}
	}
	// Review feedback with detail can suggest improvements.
	for _, fb := range feedback {
		if fb.Source == state.FeedbackSourceReview && fb.Detail != "" {
			improvements = append(improvements, "Review: "+truncateStr(fb.Detail, 100))
		}
	}
	return improvements
}

func buildSummary(run state.TaskRun, trigger state.RetroTrigger, outcome state.RetroOutcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Retrospective for run %s", run.RunID)
	if run.TaskID != "" {
		fmt.Fprintf(&b, " (task %s)", run.TaskID)
	}
	fmt.Fprintf(&b, ": %s", outcome)
	switch trigger {
	case state.RetroTriggerBudgetExhausted:
		b.WriteString(" — budget exhausted")
	case state.RetroTriggerVerifyFailed:
		b.WriteString(" — verification failed")
	case state.RetroTriggerRunFailed:
		if run.Error != "" {
			fmt.Fprintf(&b, " — %s", truncateStr(run.Error, 80))
		}
	}
	return b.String()
}

func isTerminalRun(run state.TaskRun) bool {
	switch run.Status {
	case state.TaskRunStatusCompleted, state.TaskRunStatusFailed, state.TaskRunStatusCancelled:
		return true
	}
	return false
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// ── Formatting ───────────────────────────────────────────────────────────────

// FormatRetrospective returns a human-readable summary of a retrospective.
func FormatRetrospective(r state.Retrospective) string {
	var b strings.Builder
	icon := retroOutcomeIcon(r.Outcome)
	fmt.Fprintf(&b, "%s Retrospective [%s]\n", icon, r.RetroID)
	fmt.Fprintf(&b, "  Trigger: %s  Outcome: %s\n", r.Trigger, r.Outcome)
	if r.RunID != "" {
		fmt.Fprintf(&b, "  Run: %s", r.RunID)
		if r.TaskID != "" {
			fmt.Fprintf(&b, "  Task: %s", r.TaskID)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "  Summary: %s\n", r.Summary)
	if len(r.WhatWorked) > 0 {
		b.WriteString("  ✅ What worked:\n")
		for _, w := range r.WhatWorked {
			fmt.Fprintf(&b, "     - %s\n", w)
		}
	}
	if len(r.WhatFailed) > 0 {
		b.WriteString("  ❌ What failed:\n")
		for _, f := range r.WhatFailed {
			fmt.Fprintf(&b, "     - %s\n", f)
		}
	}
	if len(r.Improvements) > 0 {
		b.WriteString("  💡 Improvements:\n")
		for _, i := range r.Improvements {
			fmt.Fprintf(&b, "     - %s\n", i)
		}
	}
	if len(r.FeedbackIDs) > 0 {
		fmt.Fprintf(&b, "  Feedback: %d record(s)\n", len(r.FeedbackIDs))
	}
	if len(r.ProposalIDs) > 0 {
		fmt.Fprintf(&b, "  Proposals: %d linked\n", len(r.ProposalIDs))
	}
	return b.String()
}

func retroOutcomeIcon(o state.RetroOutcome) string {
	switch o {
	case state.RetroOutcomeSuccess:
		return "✅"
	case state.RetroOutcomePartial:
		return "⚠️"
	case state.RetroOutcomeFailure:
		return "❌"
	default:
		return "❓"
	}
}

// FormatRetroSummary returns a compact multi-line summary of several retrospectives.
func FormatRetroSummary(retros []state.Retrospective) string {
	if len(retros) == 0 {
		return "No retrospectives."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Retrospectives: %d total\n", len(retros))
	for _, r := range retros {
		icon := retroOutcomeIcon(r.Outcome)
		fmt.Fprintf(&b, "  %s %s — %s (%s)\n", icon, r.RetroID, r.Outcome, r.Trigger)
	}
	return b.String()
}
