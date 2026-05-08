package methods

import (
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ─── tasks.doctor ────────────────────────────────────────────────────────────

// TasksDoctorRequest is the input for the tasks.doctor method.
type TasksDoctorRequest struct {
	TaskID    string `json:"task_id"`
	RunsLimit int    `json:"runs_limit,omitempty"`
}

func (r TasksDoctorRequest) Normalize() (TasksDoctorRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	if r.TaskID == "" {
		return r, errRequiredField("task_id")
	}
	r.RunsLimit = normalizeLimit(r.RunsLimit, 20, 200)
	return r, nil
}

func DecodeTasksDoctorParams(params []byte) (TasksDoctorRequest, error) {
	return decodeMethodParams[TasksDoctorRequest](params)
}

// TasksDoctorResponse is the enriched diagnostic output for a single task.
type TasksDoctorResponse struct {
	Task   state.TaskSpec  `json:"task"`
	Runs   []state.TaskRun `json:"runs,omitempty"`
	Doctor TaskDiagnostic  `json:"doctor"`
}

// TaskDiagnostic provides an operator-friendly summary of a task's health.
type TaskDiagnostic struct {
	// Status summary.
	Status          string `json:"status"`
	StatusSince     int64  `json:"status_since,omitempty"`
	StatusAge       string `json:"status_age,omitempty"`
	TransitionCount int    `json:"transition_count"`

	// Run summary.
	TotalRuns       int    `json:"total_runs"`
	ActiveRun       string `json:"active_run,omitempty"`
	ActiveRunStatus string `json:"active_run_status,omitempty"`
	LastRunStatus   string `json:"last_run_status,omitempty"`
	LastRunEndedAt  int64  `json:"last_run_ended_at,omitempty"`

	// Budget summary — shows whether the task is approaching or exceeded limits.
	BudgetDefined         bool                  `json:"budget_defined"`
	BudgetExceeded        *state.BudgetExceeded `json:"budget_exceeded,omitempty"`
	BudgetExceededReasons []string              `json:"budget_exceeded_reasons,omitempty"`

	// Verification summary.
	VerificationPolicy        string `json:"verification_policy,omitempty"`
	VerificationChecks        int    `json:"verification_checks"`
	VerificationPassed        bool   `json:"verification_passed"`
	VerificationPendingChecks int    `json:"verification_pending_checks,omitempty"`
	VerificationFailedChecks  int    `json:"verification_failed_checks,omitempty"`

	// Approval summary.
	ApprovalRequired bool   `json:"approval_required"`
	ApprovalDecision string `json:"approval_decision,omitempty"`
	ApprovalActor    string `json:"approval_actor,omitempty"`
	ApprovalReason   string `json:"approval_reason,omitempty"`

	// Workflow child summary.
	WorkflowRunID    string `json:"workflow_run_id,omitempty"`
	WorkflowStepID   string `json:"workflow_step_id,omitempty"`
	WorkflowStepType string `json:"workflow_step_type,omitempty"`

	// Authority / governance.
	AutonomyMode string `json:"autonomy_mode,omitempty"`
	RiskClass    string `json:"risk_class,omitempty"`
	CanAct       bool   `json:"can_act"`
	CanDelegate  bool   `json:"can_delegate"`

	// Warnings — human-readable diagnostic messages.
	Warnings []string `json:"warnings,omitempty"`
}

// BuildTaskDiagnostic constructs a diagnostic summary from a task and its runs.
func BuildTaskDiagnostic(task state.TaskSpec, runs []state.TaskRun, now time.Time) TaskDiagnostic {
	diag := TaskDiagnostic{
		Status:          string(task.Status),
		TransitionCount: len(task.Transitions),
		TotalRuns:       len(runs),
	}

	// Status timing.
	if len(task.Transitions) > 0 {
		last := task.Transitions[len(task.Transitions)-1]
		diag.StatusSince = last.At
		if last.At > 0 {
			diag.StatusAge = formatDurationApprox(now.Unix() - last.At)
		}
	}

	// Active and last run.
	if task.CurrentRunID != "" {
		diag.ActiveRun = task.CurrentRunID
		for _, run := range runs {
			if run.RunID == task.CurrentRunID {
				diag.ActiveRunStatus = string(run.Status)
				break
			}
		}
	}
	if len(runs) > 0 {
		latest := runs[0]
		for _, run := range runs[1:] {
			if run.StartedAt > latest.StartedAt {
				latest = run
			}
		}
		diag.LastRunStatus = string(latest.Status)
		diag.LastRunEndedAt = latest.EndedAt
	}

	// Budget.
	diag.BudgetDefined = !task.Budget.IsZero()
	if diag.BudgetDefined {
		// Check the most recent run's usage against the task budget.
		for _, run := range runs {
			if run.RunID == task.CurrentRunID || run.RunID == task.LastRunID {
				exceeded := task.Budget.CheckUsage(run.Usage)
				if exceeded.Any() {
					diag.BudgetExceeded = &exceeded
					diag.BudgetExceededReasons = exceeded.Reasons()
				}
				break
			}
		}
	}

	auth := task.Authority

	// Verification.
	diag.VerificationPolicy = string(task.Verification.Policy)
	diag.VerificationChecks = len(task.Verification.Checks)
	diag.VerificationPassed = task.Verification.AllRequiredPassed()
	for _, check := range task.Verification.Checks {
		switch check.Status {
		case state.VerificationStatusPassed:
		case state.VerificationStatusFailed:
			diag.VerificationFailedChecks++
		default:
			diag.VerificationPendingChecks++
		}
	}

	// Approval metadata.
	mode := auth.EffectiveAutonomyMode(state.AutonomySupervised)
	diag.ApprovalRequired = task.Status == state.TaskStatusAwaitingApproval || mode.RequiresPlanApproval() || mode.RequiresStepApproval()
	diag.ApprovalDecision = metaString(task.Meta, "approval_decision")
	diag.ApprovalActor = metaString(task.Meta, "approval_actor")
	diag.ApprovalReason = metaString(task.Meta, "approval_reason")

	// Workflow child metadata.
	diag.WorkflowRunID = metaString(task.Meta, "workflow_run_id")
	diag.WorkflowStepID = metaString(task.Meta, "workflow_step_id")
	diag.WorkflowStepType = metaString(task.Meta, "workflow_step_type")

	// Authority.
	diag.AutonomyMode = string(mode)
	diag.RiskClass = string(auth.RiskClass)
	diag.CanAct = auth.CanAct
	diag.CanDelegate = auth.CanDelegate

	// Warnings.
	diag.Warnings = buildTaskWarnings(task, runs, now)

	return diag
}

// buildTaskWarnings generates human-readable diagnostic messages.
func buildTaskWarnings(task state.TaskSpec, runs []state.TaskRun, now time.Time) []string {
	var warnings []string

	// Stuck in non-terminal state for too long.
	if len(task.Transitions) > 0 {
		last := task.Transitions[len(task.Transitions)-1]
		age := now.Unix() - last.At
		switch task.Status {
		case state.TaskStatusInProgress:
			if age > 3600 {
				warnings = append(warnings, "task has been in_progress for over 1 hour")
			}
		case state.TaskStatusBlocked:
			if age > 7200 {
				warnings = append(warnings, "task has been blocked for over 2 hours")
			}
		case state.TaskStatusAwaitingApproval:
			if age > 1800 {
				warnings = append(warnings, "task has been awaiting_approval for over 30 minutes")
			}
		case state.TaskStatusVerifying:
			if age > 1800 {
				warnings = append(warnings, "task has been verifying for over 30 minutes")
			}
		}
	}

	// Has current run but it's in a terminal state.
	if task.CurrentRunID != "" {
		for _, run := range runs {
			if run.RunID == task.CurrentRunID {
				if run.Status == state.TaskRunStatusFailed || run.Status == state.TaskRunStatusCancelled {
					warnings = append(warnings, "current_run_id points to a terminated run ("+string(run.Status)+")")
				}
				break
			}
		}
	}

	if task.CurrentRunID != "" && task.Status == state.TaskStatusAwaitingApproval {
		if runStatus := runStatusForID(runs, task.CurrentRunID); runStatus != "" && runStatus != state.TaskRunStatusAwaitingApproval && runStatus != state.TaskRunStatusBlocked {
			warnings = append(warnings, "task is awaiting approval but current run is "+string(runStatus))
		}
	}

	// Verification required but no checks defined.
	if task.Verification.Policy == state.VerificationPolicyRequired && len(task.Verification.Checks) == 0 {
		warnings = append(warnings, "verification policy is 'required' but no checks are defined")
	}
	if task.Verification.Policy == state.VerificationPolicyRequired {
		var failedRequired []string
		var pendingRequired []string
		for _, check := range task.Verification.Checks {
			if !check.Required {
				continue
			}
			checkID := strings.TrimSpace(check.CheckID)
			if checkID == "" {
				checkID = strings.TrimSpace(string(check.Type))
			}
			switch check.Status {
			case state.VerificationStatusFailed:
				failedRequired = append(failedRequired, checkID)
			case state.VerificationStatusPassed:
			default:
				pendingRequired = append(pendingRequired, checkID)
			}
		}
		if len(failedRequired) > 0 {
			warnings = append(warnings, "verification required checks failing: "+strings.Join(failedRequired, ", "))
		}
		if task.Status == state.TaskStatusCompleted && len(pendingRequired) > 0 {
			warnings = append(warnings, "task completed with pending required verification checks: "+strings.Join(pendingRequired, ", "))
		}
	}
	if task.Status == state.TaskStatusVerifying && len(task.Verification.PendingChecks()) == 0 {
		warnings = append(warnings, "task is verifying but has no pending checks")
	}

	// Budget.
	if !task.Budget.IsZero() {
		for _, run := range runs {
			if run.RunID != task.CurrentRunID && run.RunID != task.LastRunID {
				continue
			}
			if exceeded := task.Budget.CheckUsage(run.Usage); exceeded.Any() {
				warnings = append(warnings, "budget exceeded: "+strings.Join(exceeded.Reasons(), ", "))
			}
			break
		}
	}
	if task.Budget.IsZero() && task.Authority.EffectiveAutonomyMode(state.AutonomySupervised) == state.AutonomyFull {
		warnings = append(warnings, "full autonomy with no budget limits — consider adding a budget")
	}

	if decision := metaString(task.Meta, "approval_decision"); decision == string(state.TaskApprovalDecisionRejected) && task.Status != state.TaskStatusBlocked {
		warnings = append(warnings, "approval decision is rejected but task status is "+string(task.Status))
	}
	if task.Status == state.TaskStatusAwaitingApproval && !task.Authority.EffectiveAutonomyMode(state.AutonomySupervised).RequiresPlanApproval() && !task.Authority.EffectiveAutonomyMode(state.AutonomySupervised).RequiresStepApproval() {
		warnings = append(warnings, "task is awaiting approval but effective autonomy mode does not require approval")
	}
	if task.Status == state.TaskStatusAwaitingApproval && !task.Authority.CanEscalate {
		warnings = append(warnings, "task is awaiting approval but authority cannot escalate")
	}
	if task.Authority.EscalationRequired && !task.Authority.CanEscalate {
		warnings = append(warnings, "escalation is required but authority cannot escalate")
	}
	if task.Authority.CanDelegate && task.Authority.MaxDelegationDepth == 0 {
		warnings = append(warnings, "authority can delegate but max_delegation_depth is 0")
	}

	workflowRunID := metaString(task.Meta, "workflow_run_id")
	workflowStepID := metaString(task.Meta, "workflow_step_id")
	workflowStepType := metaString(task.Meta, "workflow_step_type")
	if workflowRunID != "" || workflowStepID != "" || workflowStepType != "" {
		if strings.TrimSpace(task.ParentTaskID) == "" {
			warnings = append(warnings, "workflow child task has workflow metadata but no parent_task_id")
		}
		if workflowStepID == "" {
			warnings = append(warnings, "workflow child task is missing workflow_step_id")
		}
		if workflowStepType == "" {
			warnings = append(warnings, "workflow child task is missing workflow_step_type")
		}
		if strings.TrimSpace(task.CurrentRunID) == "" && strings.TrimSpace(task.LastRunID) == "" && len(runs) == 0 {
			warnings = append(warnings, "workflow child task has no linked task run")
		}
	}

	return warnings
}

func runStatusForID(runs []state.TaskRun, runID string) state.TaskRunStatus {
	runID = strings.TrimSpace(runID)
	for _, run := range runs {
		if strings.TrimSpace(run.RunID) == runID {
			return run.Status
		}
	}
	return ""
}

// ─── tasks.summary ───────────────────────────────────────────────────────────

// TasksSummaryRequest is the input for the tasks.summary method.
type TasksSummaryRequest struct {
	GoalID string `json:"goal_id,omitempty"`
}

func (r TasksSummaryRequest) Normalize() (TasksSummaryRequest, error) {
	r.GoalID = strings.TrimSpace(r.GoalID)
	return r, nil
}

func DecodeTasksSummaryParams(params []byte) (TasksSummaryRequest, error) {
	return decodeMethodParams[TasksSummaryRequest](params)
}

// TasksSummaryResponse provides aggregate status counts.
type TasksSummaryResponse struct {
	Total        int            `json:"total"`
	ByStatus     map[string]int `json:"by_status"`
	ActiveCount  int            `json:"active_count"`
	BlockedCount int            `json:"blocked_count"`
	FailedCount  int            `json:"failed_count"`
}

// BuildTasksSummary computes aggregate status counts from a list of tasks.
func BuildTasksSummary(tasks []state.TaskSpec) TasksSummaryResponse {
	byStatus := make(map[string]int)
	var active, blocked, failed int
	for _, task := range tasks {
		s := string(task.Status)
		if s == "" {
			s = "unknown"
		}
		byStatus[s]++
		switch task.Status {
		case state.TaskStatusInProgress, state.TaskStatusReady, state.TaskStatusPlanned:
			active++
		case state.TaskStatusBlocked, state.TaskStatusAwaitingApproval:
			blocked++
		case state.TaskStatusFailed:
			failed++
		}
	}
	return TasksSummaryResponse{
		Total:        len(tasks),
		ByStatus:     byStatus,
		ActiveCount:  active,
		BlockedCount: blocked,
		FailedCount:  failed,
	}
}

// ─── Enhanced tasks.list filters ─────────────────────────────────────────────

// FilterTasks applies the TasksListRequest filters to a list of tasks.
// This is extracted from main.go's listControlTasks for testability.
func FilterTasks(tasks []state.TaskSpec, req TasksListRequest) []state.TaskSpec {
	filtered := make([]state.TaskSpec, 0, len(tasks))
	for _, task := range tasks {
		if req.Status != "" && task.Status != req.Status {
			continue
		}
		if req.GoalID != "" && strings.TrimSpace(task.GoalID) != req.GoalID {
			continue
		}
		if req.AssignedAgent != "" && strings.TrimSpace(task.AssignedAgent) != req.AssignedAgent {
			continue
		}
		if req.SessionID != "" && strings.TrimSpace(task.SessionID) != req.SessionID {
			continue
		}
		if req.ParentTaskID != "" && strings.TrimSpace(task.ParentTaskID) != req.ParentTaskID {
			continue
		}
		if req.PlanID != "" && strings.TrimSpace(task.PlanID) != req.PlanID {
			continue
		}
		if req.CreatedAfter > 0 && task.CreatedAt < req.CreatedAfter {
			continue
		}
		if req.UpdatedAfter > 0 && task.UpdatedAt < req.UpdatedAfter {
			continue
		}
		filtered = append(filtered, task)
		if req.Limit > 0 && len(filtered) >= req.Limit {
			break
		}
	}
	return filtered
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func formatDurationApprox(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return itoa(seconds) + "s"
	case seconds < 3600:
		return itoa(seconds/60) + "m"
	case seconds < 86400:
		return itoa(seconds/3600) + "h"
	default:
		return itoa(seconds/86400) + "d"
	}
}

func metaString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	idx := len(buf)
	for v > 0 {
		idx--
		buf[idx] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[idx:])
}

func errRequiredField(name string) error {
	return &requiredFieldError{field: name}
}

type requiredFieldError struct {
	field string
}

func (e *requiredFieldError) Error() string {
	return e.field + " is required"
}
