package methods

import (
	"encoding/json"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ─── BuildTaskDiagnostic tests ───────────────────────────────────────────────

func TestBuildTaskDiagnostic_BasicFields(t *testing.T) {
	now := time.Unix(1000, 0)
	task := state.TaskSpec{
		TaskID: "t1",
		Status: state.TaskStatusInProgress,
		Transitions: []state.TaskTransition{
			{To: state.TaskStatusPending, At: 800},
			{To: state.TaskStatusInProgress, At: 900},
		},
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
			RiskClass:    state.RiskClassMedium,
			CanAct:       true,
			CanDelegate:  true,
		},
	}
	diag := BuildTaskDiagnostic(task, nil, now)
	if diag.Status != "in_progress" {
		t.Fatalf("expected in_progress, got %q", diag.Status)
	}
	if diag.TransitionCount != 2 {
		t.Fatalf("expected 2 transitions, got %d", diag.TransitionCount)
	}
	if diag.StatusSince != 900 {
		t.Fatalf("expected status_since=900, got %d", diag.StatusSince)
	}
	if diag.StatusAge != "1m" {
		t.Fatalf("expected ~1m age, got %q", diag.StatusAge)
	}
	if diag.AutonomyMode != "full" {
		t.Fatalf("expected full autonomy, got %q", diag.AutonomyMode)
	}
	if !diag.CanAct || !diag.CanDelegate {
		t.Fatal("expected can_act and can_delegate")
	}
}

func TestBuildTaskDiagnostic_RunSummary(t *testing.T) {
	now := time.Unix(2000, 0)
	task := state.TaskSpec{
		TaskID:       "t1",
		Status:       state.TaskStatusInProgress,
		CurrentRunID: "run-2",
	}
	runs := []state.TaskRun{
		{RunID: "run-1", TaskID: "t1", Status: state.TaskRunStatusCompleted, StartedAt: 100, EndedAt: 200},
		{RunID: "run-2", TaskID: "t1", Status: state.TaskRunStatusRunning, StartedAt: 300},
	}
	diag := BuildTaskDiagnostic(task, runs, now)
	if diag.TotalRuns != 2 {
		t.Fatalf("expected 2 runs, got %d", diag.TotalRuns)
	}
	if diag.ActiveRun != "run-2" {
		t.Fatalf("expected active run-2, got %q", diag.ActiveRun)
	}
	if diag.ActiveRunStatus != "running" {
		t.Fatalf("expected running, got %q", diag.ActiveRunStatus)
	}
	if diag.LastRunStatus != "running" {
		t.Fatalf("expected latest run to be running, got %q", diag.LastRunStatus)
	}
}

func TestBuildTaskDiagnostic_BudgetExceeded(t *testing.T) {
	now := time.Unix(2000, 0)
	task := state.TaskSpec{
		TaskID:       "t1",
		Status:       state.TaskStatusInProgress,
		CurrentRunID: "run-1",
		Budget: state.TaskBudget{
			MaxTotalTokens: 100,
		},
	}
	runs := []state.TaskRun{
		{RunID: "run-1", TaskID: "t1", Status: state.TaskRunStatusRunning, Usage: state.TaskUsage{TotalTokens: 200}},
	}
	diag := BuildTaskDiagnostic(task, runs, now)
	if !diag.BudgetDefined {
		t.Fatal("expected budget defined")
	}
	if diag.BudgetExceeded == nil {
		t.Fatal("expected budget exceeded")
	}
	if !diag.BudgetExceeded.Any() {
		t.Fatal("expected any exceeded")
	}
}

func TestBuildTaskDiagnostic_VerificationSummary(t *testing.T) {
	now := time.Unix(2000, 0)
	task := state.TaskSpec{
		TaskID: "t1",
		Status: state.TaskStatusVerifying,
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "c1", Type: "schema", Required: true, Status: state.VerificationStatusPassed},
				{CheckID: "c2", Type: "evidence", Required: false, Status: state.VerificationStatusFailed},
			},
		},
	}
	diag := BuildTaskDiagnostic(task, nil, now)
	if diag.VerificationPolicy != "required" {
		t.Fatalf("expected required, got %q", diag.VerificationPolicy)
	}
	if diag.VerificationChecks != 2 {
		t.Fatalf("expected 2 checks, got %d", diag.VerificationChecks)
	}
	if !diag.VerificationPassed {
		t.Fatal("expected passed — the one required check passed")
	}
}

func TestBuildTaskDiagnostic_Warnings_StuckInProgress(t *testing.T) {
	now := time.Unix(10000, 0)
	task := state.TaskSpec{
		TaskID: "t1",
		Status: state.TaskStatusInProgress,
		Transitions: []state.TaskTransition{
			{To: state.TaskStatusInProgress, At: 1000},
		},
	}
	diag := BuildTaskDiagnostic(task, nil, now)
	found := false
	for _, w := range diag.Warnings {
		if w == "task has been in_progress for over 1 hour" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stuck warning, got %v", diag.Warnings)
	}
}

func TestBuildTaskDiagnostic_Warnings_TerminatedCurrentRun(t *testing.T) {
	now := time.Unix(2000, 0)
	task := state.TaskSpec{
		TaskID:       "t1",
		Status:       state.TaskStatusInProgress,
		CurrentRunID: "run-1",
	}
	runs := []state.TaskRun{
		{RunID: "run-1", Status: state.TaskRunStatusFailed},
	}
	diag := BuildTaskDiagnostic(task, runs, now)
	found := false
	for _, w := range diag.Warnings {
		if w == "current_run_id points to a terminated run (failed)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected terminated run warning, got %v", diag.Warnings)
	}
}

func TestBuildTaskDiagnostic_Warnings_FullAutonomyNoBudget(t *testing.T) {
	now := time.Unix(2000, 0)
	task := state.TaskSpec{
		TaskID: "t1",
		Status: state.TaskStatusReady,
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
		},
	}
	diag := BuildTaskDiagnostic(task, nil, now)
	found := false
	for _, w := range diag.Warnings {
		if w == "full autonomy with no budget limits — consider adding a budget" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected budget warning, got %v", diag.Warnings)
	}
}

func TestBuildTaskDiagnostic_NoWarningsWhenHealthy(t *testing.T) {
	now := time.Unix(100, 0)
	task := state.TaskSpec{
		TaskID: "t1",
		Status: state.TaskStatusCompleted,
		Transitions: []state.TaskTransition{
			{To: state.TaskStatusCompleted, At: 90},
		},
		Budget: state.TaskBudget{MaxTotalTokens: 1000},
	}
	diag := BuildTaskDiagnostic(task, nil, now)
	if len(diag.Warnings) != 0 {
		t.Fatalf("expected no warnings for healthy task, got %v", diag.Warnings)
	}
}

func TestBuildTaskDiagnostic_GovernanceWarningsAndReporting(t *testing.T) {
	now := time.Unix(2000, 0)
	task := state.TaskSpec{
		TaskID:       "t1",
		Status:       state.TaskStatusAwaitingApproval,
		CurrentRunID: "run-1",
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
			CanAct:       true,
		},
		Budget: state.TaskBudget{MaxTotalTokens: 100},
		Verification: state.VerificationSpec{
			Policy: state.VerificationPolicyRequired,
			Checks: []state.VerificationCheck{
				{CheckID: "required-review", Required: true, Status: state.VerificationStatusFailed},
			},
		},
		Meta: map[string]any{
			"approval_decision": "rejected",
			"approval_actor":    "operator",
			"approval_reason":   "unsafe",
		},
	}
	runs := []state.TaskRun{{
		RunID:  "run-1",
		TaskID: "t1",
		Status: state.TaskRunStatusRunning,
		Usage:  state.TaskUsage{TotalTokens: 150},
	}}

	diag := BuildTaskDiagnostic(task, runs, now)
	if !diag.ApprovalRequired || diag.ApprovalDecision != "rejected" || diag.ApprovalActor != "operator" {
		t.Fatalf("expected approval summary, got %+v", diag)
	}
	if diag.BudgetExceeded == nil || len(diag.BudgetExceededReasons) == 0 {
		t.Fatalf("expected budget exceeded summary, got %+v", diag)
	}
	if diag.VerificationFailedChecks != 1 || diag.VerificationPassed {
		t.Fatalf("expected failed verification summary, got %+v", diag)
	}
	for _, want := range []string{
		"task is awaiting approval but current run is running",
		"verification required checks failing: required-review",
		"budget exceeded: total tokens exceeded",
		"approval decision is rejected but task status is awaiting_approval",
		"task is awaiting approval but effective autonomy mode does not require approval",
	} {
		if !hasDoctorWarning(diag.Warnings, want) {
			t.Fatalf("expected warning %q, got %v", want, diag.Warnings)
		}
	}
}

func TestBuildTaskDiagnostic_WorkflowChildInconsistency(t *testing.T) {
	task := state.TaskSpec{
		TaskID:       "child",
		Status:       state.TaskStatusReady,
		Title:        "Workflow child",
		Instructions: "run step",
		Meta: map[string]any{
			"workflow_run_id":    "wf-run",
			"workflow_step_type": "gateway_call",
		},
	}
	diag := BuildTaskDiagnostic(task, nil, time.Unix(2000, 0))
	if diag.WorkflowRunID != "wf-run" || diag.WorkflowStepType != "gateway_call" {
		t.Fatalf("expected workflow summary, got %+v", diag)
	}
	for _, want := range []string{
		"workflow child task has workflow metadata but no parent_task_id",
		"workflow child task is missing workflow_step_id",
		"workflow child task has no linked task run",
	} {
		if !hasDoctorWarning(diag.Warnings, want) {
			t.Fatalf("expected workflow warning %q, got %v", want, diag.Warnings)
		}
	}
}

// ─── BuildTasksSummary tests ─────────────────────────────────────────────────

func TestBuildTasksSummary_BasicCounts(t *testing.T) {
	tasks := []state.TaskSpec{
		{TaskID: "1", Status: state.TaskStatusInProgress},
		{TaskID: "2", Status: state.TaskStatusInProgress},
		{TaskID: "3", Status: state.TaskStatusBlocked},
		{TaskID: "4", Status: state.TaskStatusFailed},
		{TaskID: "5", Status: state.TaskStatusCompleted},
		{TaskID: "6", Status: state.TaskStatusReady},
	}
	summary := BuildTasksSummary(tasks)
	if summary.Total != 6 {
		t.Fatalf("expected total=6, got %d", summary.Total)
	}
	if summary.ActiveCount != 3 {
		t.Fatalf("expected active=3 (in_progress*2 + ready), got %d", summary.ActiveCount)
	}
	if summary.BlockedCount != 1 {
		t.Fatalf("expected blocked=1, got %d", summary.BlockedCount)
	}
	if summary.FailedCount != 1 {
		t.Fatalf("expected failed=1, got %d", summary.FailedCount)
	}
	if summary.ByStatus["in_progress"] != 2 {
		t.Fatalf("expected by_status[in_progress]=2, got %d", summary.ByStatus["in_progress"])
	}
}

func TestBuildTasksSummary_Empty(t *testing.T) {
	summary := BuildTasksSummary(nil)
	if summary.Total != 0 {
		t.Fatal("expected total=0")
	}
	if len(summary.ByStatus) != 0 {
		t.Fatal("expected empty by_status")
	}
}

// ─── FilterTasks tests ───────────────────────────────────────────────────────

func TestFilterTasks_ByParentTaskID(t *testing.T) {
	tasks := []state.TaskSpec{
		{TaskID: "1", ParentTaskID: "parent-a"},
		{TaskID: "2", ParentTaskID: "parent-b"},
		{TaskID: "3", ParentTaskID: "parent-a"},
	}
	filtered := FilterTasks(tasks, TasksListRequest{ParentTaskID: "parent-a"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results, got %d", len(filtered))
	}
}

func TestFilterTasks_ByPlanID(t *testing.T) {
	tasks := []state.TaskSpec{
		{TaskID: "1", PlanID: "plan-x"},
		{TaskID: "2", PlanID: "plan-y"},
	}
	filtered := FilterTasks(tasks, TasksListRequest{PlanID: "plan-x"})
	if len(filtered) != 1 || filtered[0].TaskID != "1" {
		t.Fatalf("expected task 1, got %+v", filtered)
	}
}

func TestFilterTasks_ByCreatedAfter(t *testing.T) {
	tasks := []state.TaskSpec{
		{TaskID: "1", CreatedAt: 100},
		{TaskID: "2", CreatedAt: 200},
		{TaskID: "3", CreatedAt: 300},
	}
	filtered := FilterTasks(tasks, TasksListRequest{CreatedAfter: 150})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results (created_at >= 150), got %d", len(filtered))
	}
}

func TestFilterTasks_ByUpdatedAfter(t *testing.T) {
	tasks := []state.TaskSpec{
		{TaskID: "1", UpdatedAt: 100},
		{TaskID: "2", UpdatedAt: 200},
	}
	filtered := FilterTasks(tasks, TasksListRequest{UpdatedAfter: 150})
	if len(filtered) != 1 || filtered[0].TaskID != "2" {
		t.Fatalf("expected task 2, got %+v", filtered)
	}
}

func TestFilterTasks_CombinedFilters(t *testing.T) {
	tasks := []state.TaskSpec{
		{TaskID: "1", Status: state.TaskStatusInProgress, GoalID: "g1", ParentTaskID: "p1", CreatedAt: 200},
		{TaskID: "2", Status: state.TaskStatusInProgress, GoalID: "g1", ParentTaskID: "p1", CreatedAt: 100},
		{TaskID: "3", Status: state.TaskStatusCompleted, GoalID: "g1", ParentTaskID: "p1", CreatedAt: 200},
	}
	filtered := FilterTasks(tasks, TasksListRequest{
		Status:       state.TaskStatusInProgress,
		GoalID:       "g1",
		ParentTaskID: "p1",
		CreatedAfter: 150,
	})
	if len(filtered) != 1 || filtered[0].TaskID != "1" {
		t.Fatalf("expected task 1, got %+v", filtered)
	}
}

func TestFilterTasks_LimitRespected(t *testing.T) {
	tasks := make([]state.TaskSpec, 10)
	for i := range tasks {
		tasks[i] = state.TaskSpec{TaskID: itoa(int64(i))}
	}
	filtered := FilterTasks(tasks, TasksListRequest{Limit: 3})
	if len(filtered) != 3 {
		t.Fatalf("expected 3 results with limit, got %d", len(filtered))
	}
}

// ─── Request decode/normalize tests ──────────────────────────────────────────

func TestTasksDoctorRequest_Normalize_RequiresTaskID(t *testing.T) {
	req := TasksDoctorRequest{}
	_, err := req.Normalize()
	if err == nil {
		t.Fatal("expected error for empty task_id")
	}
}

func TestTasksDoctorRequest_Normalize_DefaultsRunsLimit(t *testing.T) {
	req := TasksDoctorRequest{TaskID: "t1"}
	norm, err := req.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if norm.RunsLimit != 20 {
		t.Fatalf("expected default runs_limit=20, got %d", norm.RunsLimit)
	}
}

func TestTasksSummaryRequest_Normalize(t *testing.T) {
	req := TasksSummaryRequest{GoalID: "  g1  "}
	norm, _ := req.Normalize()
	if norm.GoalID != "g1" {
		t.Fatalf("expected trimmed goal_id, got %q", norm.GoalID)
	}
}

func TestTasksListRequest_Normalize_NewFields(t *testing.T) {
	req := TasksListRequest{
		ParentTaskID: "  p1  ",
		PlanID:       "  plan-1  ",
	}
	norm, err := req.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if norm.ParentTaskID != "p1" {
		t.Fatalf("expected trimmed parent_task_id, got %q", norm.ParentTaskID)
	}
	if norm.PlanID != "plan-1" {
		t.Fatalf("expected trimmed plan_id, got %q", norm.PlanID)
	}
}

// ─── JSON shape tests ────────────────────────────────────────────────────────

func TestTasksDoctorResponse_JSONShape(t *testing.T) {
	resp := TasksDoctorResponse{
		Task: state.TaskSpec{TaskID: "t1", Status: state.TaskStatusInProgress},
		Doctor: TaskDiagnostic{
			Status:          "in_progress",
			TransitionCount: 2,
			TotalRuns:       1,
			BudgetDefined:   false,
			CanAct:          true,
			Warnings:        []string{"test warning"},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["doctor"]; !ok {
		t.Fatal("expected 'doctor' field in JSON")
	}
	doc := m["doctor"].(map[string]any)
	if doc["status"] != "in_progress" {
		t.Fatalf("expected status in doctor, got %v", doc)
	}
	warnings := doc["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", warnings)
	}
}

func TestTasksSummaryResponse_JSONShape(t *testing.T) {
	resp := TasksSummaryResponse{
		Total:        10,
		ByStatus:     map[string]int{"in_progress": 3, "completed": 7},
		ActiveCount:  3,
		BlockedCount: 0,
		FailedCount:  0,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["total"].(float64) != 10 {
		t.Fatalf("expected total=10, got %v", m["total"])
	}
	bs := m["by_status"].(map[string]any)
	if bs["in_progress"].(float64) != 3 {
		t.Fatalf("expected in_progress=3, got %v", bs)
	}
}

// ─── formatDurationApprox tests ──────────────────────────────────────────────

func TestFormatDurationApprox(t *testing.T) {
	cases := []struct {
		seconds int64
		want    string
	}{
		{0, "0s"},
		{30, "30s"},
		{90, "1m"},
		{3600, "1h"},
		{86400, "1d"},
		{172800, "2d"},
		{-5, "0s"},
	}
	for _, tc := range cases {
		got := formatDurationApprox(tc.seconds)
		if got != tc.want {
			t.Errorf("formatDurationApprox(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func hasDoctorWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}
