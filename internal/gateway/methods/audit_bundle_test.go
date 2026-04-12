package methods

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ─── AuditExportRequest ──────────────────────────────────────────────────────

func TestAuditExportRequest_Normalize_RequiresScope(t *testing.T) {
	_, err := AuditExportRequest{}.Normalize()
	if err == nil {
		t.Fatal("expected error when neither task_id nor goal_id set")
	}
}

func TestAuditExportRequest_Normalize_RejectsBoth(t *testing.T) {
	_, err := AuditExportRequest{TaskID: "t1", GoalID: "g1"}.Normalize()
	if err == nil {
		t.Fatal("expected error when both task_id and goal_id set")
	}
}

func TestAuditExportRequest_Normalize_DefaultRunsLimit(t *testing.T) {
	req, err := AuditExportRequest{TaskID: "t1"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if req.RunsLimit != 50 {
		t.Fatalf("expected default runs_limit 50, got %d", req.RunsLimit)
	}
}

func TestAuditExportRequest_Normalize_CapsRunsLimit(t *testing.T) {
	req, err := AuditExportRequest{TaskID: "t1", RunsLimit: 9999}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if req.RunsLimit != 500 {
		t.Fatalf("expected capped runs_limit 500, got %d", req.RunsLimit)
	}
}

// ─── BuildAuditBundle ────────────────────────────────────────────────────────

func newTestTask(id, parent, goalID string, status state.TaskStatus, created, updated int64) state.TaskSpec {
	return state.TaskSpec{
		Version:      1,
		TaskID:       id,
		ParentTaskID: parent,
		GoalID:       goalID,
		Title:        "Task " + id,
		Instructions: "Do " + id,
		Status:       status,
		Priority:     state.TaskPriorityMedium,
		CreatedAt:    created,
		UpdatedAt:    updated,
		Transitions: []state.TaskTransition{
			{To: status, At: created, Actor: "test", Source: "test"},
		},
	}
}

func newTestRun(runID, taskID string, attempt int, status state.TaskRunStatus, tokens int) state.TaskRun {
	return state.TaskRun{
		Version:   1,
		RunID:     runID,
		TaskID:    taskID,
		Attempt:   attempt,
		Status:    status,
		AgentID:   "agent-1",
		StartedAt: 100,
		EndedAt:   200,
		Usage:     state.TaskUsage{TotalTokens: tokens, ToolCalls: 3},
	}
}

func TestBuildAuditBundle_BasicFields(t *testing.T) {
	tasks := []state.TaskSpec{
		newTestTask("root", "", "g1", state.TaskStatusCompleted, 100, 200),
	}
	runs := map[string][]state.TaskRun{
		"root": {newTestRun("r1", "root", 1, state.TaskRunStatusCompleted, 500)},
	}
	req := AuditExportRequest{TaskID: "root", IncludeInputs: false, IncludeMeta: false, RunsLimit: 50}
	req, _ = req.Normalize()
	now := time.Unix(300, 0)
	bundle := BuildAuditBundle(tasks, runs, req, "operator", now)

	if bundle.SchemaVersion != 1 {
		t.Fatalf("expected schema version 1, got %d", bundle.SchemaVersion)
	}
	if bundle.ExportedAt != 300 {
		t.Fatalf("expected exported_at 300, got %d", bundle.ExportedAt)
	}
	if bundle.ExportedBy != "operator" {
		t.Fatalf("expected exported_by operator, got %q", bundle.ExportedBy)
	}
	if bundle.RootTaskID != "root" {
		t.Fatalf("expected root_task_id root, got %q", bundle.RootTaskID)
	}
	if len(bundle.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(bundle.Tasks))
	}
	if bundle.Tasks[0].TaskID != "root" {
		t.Fatalf("expected task root, got %q", bundle.Tasks[0].TaskID)
	}
}

func TestBuildAuditBundle_Summary(t *testing.T) {
	tasks := []state.TaskSpec{
		newTestTask("root", "", "g1", state.TaskStatusCompleted, 100, 300),
		newTestTask("child1", "root", "g1", state.TaskStatusCompleted, 150, 250),
		newTestTask("child2", "root", "g1", state.TaskStatusFailed, 160, 280),
	}
	runs := map[string][]state.TaskRun{
		"root":   {newTestRun("r1", "root", 1, state.TaskRunStatusCompleted, 500)},
		"child1": {newTestRun("r2", "child1", 1, state.TaskRunStatusCompleted, 300)},
		"child2": {
			newTestRun("r3", "child2", 1, state.TaskRunStatusFailed, 200),
			newTestRun("r4", "child2", 2, state.TaskRunStatusFailed, 100),
		},
	}
	req, _ := AuditExportRequest{GoalID: "g1"}.Normalize()
	bundle := BuildAuditBundle(tasks, runs, req, "op", time.Unix(400, 0))

	s := bundle.Summary
	if s.TotalTasks != 3 {
		t.Fatalf("expected 3 tasks, got %d", s.TotalTasks)
	}
	if s.TotalRuns != 4 {
		t.Fatalf("expected 4 runs, got %d", s.TotalRuns)
	}
	if s.ByStatus["completed"] != 2 {
		t.Fatalf("expected 2 completed, got %d", s.ByStatus["completed"])
	}
	if s.ByStatus["failed"] != 1 {
		t.Fatalf("expected 1 failed, got %d", s.ByStatus["failed"])
	}
	if s.MaxDepth != 1 {
		t.Fatalf("expected max depth 1, got %d", s.MaxDepth)
	}
	if s.EarliestCreated != 100 {
		t.Fatalf("expected earliest 100, got %d", s.EarliestCreated)
	}
	if s.LatestUpdated != 300 {
		t.Fatalf("expected latest 300, got %d", s.LatestUpdated)
	}
	// Total usage: 500+300+200+100 = 1100 tokens, 3*4 = 12 tool calls.
	if s.TotalUsage.TotalTokens != 1100 {
		t.Fatalf("expected 1100 total tokens, got %d", s.TotalUsage.TotalTokens)
	}
	if s.TotalUsage.ToolCalls != 12 {
		t.Fatalf("expected 12 tool calls, got %d", s.TotalUsage.ToolCalls)
	}
}

func TestBuildAuditBundle_DepthComputation(t *testing.T) {
	tasks := []state.TaskSpec{
		newTestTask("root", "", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("c1", "root", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("c1a", "c1", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("c1b", "c1", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("c1a1", "c1a", "", state.TaskStatusCompleted, 100, 200),
	}
	req, _ := AuditExportRequest{TaskID: "root"}.Normalize()
	bundle := BuildAuditBundle(tasks, nil, req, "", time.Now())

	depthByID := map[string]int{}
	for _, at := range bundle.Tasks {
		depthByID[at.TaskID] = at.Depth
	}
	expect := map[string]int{"root": 0, "c1": 1, "c1a": 2, "c1b": 2, "c1a1": 3}
	for id, want := range expect {
		if got := depthByID[id]; got != want {
			t.Errorf("task %s: depth %d, want %d", id, got, want)
		}
	}
	if bundle.Summary.MaxDepth != 3 {
		t.Fatalf("expected max depth 3, got %d", bundle.Summary.MaxDepth)
	}
}

func TestBuildAuditBundle_RedactionDefault(t *testing.T) {
	task := newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200)
	task.Inputs = map[string]any{"secret": "key123"}
	task.Meta = map[string]any{"internal": "data"}
	tasks := []state.TaskSpec{task}

	// Default: redacted.
	req, _ := AuditExportRequest{TaskID: "t1"}.Normalize()
	bundle := BuildAuditBundle(tasks, nil, req, "", time.Now())

	if bundle.Tasks[0].Inputs != nil {
		t.Fatal("expected inputs redacted by default")
	}
	if bundle.Tasks[0].Meta != nil {
		t.Fatal("expected meta redacted by default")
	}
	if len(bundle.Redactions) != 3 {
		t.Fatalf("expected 3 redaction entries, got %d: %v", len(bundle.Redactions), bundle.Redactions)
	}
}

func TestBuildAuditBundle_RedactionIncluded(t *testing.T) {
	task := newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200)
	task.Inputs = map[string]any{"prompt": "hello"}
	task.Meta = map[string]any{"source": "api"}
	tasks := []state.TaskSpec{task}

	req, _ := AuditExportRequest{TaskID: "t1", IncludeInputs: true, IncludeMeta: true}.Normalize()
	bundle := BuildAuditBundle(tasks, nil, req, "", time.Now())

	if bundle.Tasks[0].Inputs == nil {
		t.Fatal("expected inputs included")
	}
	if bundle.Tasks[0].Meta == nil {
		t.Fatal("expected meta included")
	}
	if len(bundle.Redactions) != 0 {
		t.Fatalf("expected no redactions, got %v", bundle.Redactions)
	}
}

func TestBuildAuditBundle_VerificationEvidence(t *testing.T) {
	task := newTestTask("t1", "", "", state.TaskStatusVerifying, 100, 200)
	task.Verification = state.VerificationSpec{
		Policy: state.VerificationPolicyRequired,
		Checks: []state.VerificationCheck{
			{CheckID: "c1", Type: state.VerificationCheckSchema, Required: true, Status: state.VerificationStatusPassed},
			{CheckID: "c2", Type: state.VerificationCheckEvidence, Required: true, Status: state.VerificationStatusPassed},
		},
		VerifiedAt: 190,
		VerifiedBy: "reviewer",
	}

	req, _ := AuditExportRequest{TaskID: "t1"}.Normalize()
	bundle := BuildAuditBundle([]state.TaskSpec{task}, nil, req, "", time.Now())

	v := bundle.Tasks[0].Verification
	if v.Policy != "required" {
		t.Fatalf("expected required, got %q", v.Policy)
	}
	if len(v.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(v.Checks))
	}
	if !v.AllPassed {
		t.Fatal("expected all passed")
	}
	if v.VerifiedAt != 190 {
		t.Fatalf("expected verified_at 190, got %d", v.VerifiedAt)
	}
	if v.VerifiedBy != "reviewer" {
		t.Fatalf("expected verified_by reviewer, got %q", v.VerifiedBy)
	}
}

func TestBuildAuditBundle_AuthorityCaptured(t *testing.T) {
	task := newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200)
	task.Authority = state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassHigh,
		CanAct:       true,
		CanDelegate:  true,
	}

	req, _ := AuditExportRequest{TaskID: "t1"}.Normalize()
	bundle := BuildAuditBundle([]state.TaskSpec{task}, nil, req, "", time.Now())

	auth := bundle.Tasks[0].Authority
	if auth.AutonomyMode != "full" {
		t.Fatalf("expected full, got %q", auth.AutonomyMode)
	}
	if auth.RiskClass != "high" {
		t.Fatalf("expected high, got %q", auth.RiskClass)
	}
	if !auth.CanAct || !auth.CanDelegate {
		t.Fatal("expected can_act and can_delegate true")
	}
}

func TestBuildAuditBundle_RunsIncluded(t *testing.T) {
	tasks := []state.TaskSpec{
		newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200),
	}
	runs := map[string][]state.TaskRun{
		"t1": {
			newTestRun("r1", "t1", 1, state.TaskRunStatusFailed, 100),
			newTestRun("r2", "t1", 2, state.TaskRunStatusCompleted, 400),
		},
	}
	req, _ := AuditExportRequest{TaskID: "t1"}.Normalize()
	bundle := BuildAuditBundle(tasks, runs, req, "", time.Now())

	if len(bundle.Tasks[0].Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(bundle.Tasks[0].Runs))
	}
	r1 := bundle.Tasks[0].Runs[0]
	if r1.RunID != "r1" || r1.Attempt != 1 || r1.Status != "failed" {
		t.Fatalf("run 0 unexpected: %+v", r1)
	}
	r2 := bundle.Tasks[0].Runs[1]
	if r2.RunID != "r2" || r2.Attempt != 2 || r2.Status != "completed" {
		t.Fatalf("run 1 unexpected: %+v", r2)
	}
}

func TestBuildAuditBundle_TasksHash(t *testing.T) {
	tasks := []state.TaskSpec{
		newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200),
	}
	req, _ := AuditExportRequest{TaskID: "t1"}.Normalize()
	bundle := BuildAuditBundle(tasks, nil, req, "", time.Unix(300, 0))

	if bundle.TasksHash == "" {
		t.Fatal("expected non-empty tasks hash")
	}
	// Verify hash is deterministic.
	bundle2 := BuildAuditBundle(tasks, nil, req, "", time.Unix(300, 0))
	if bundle.TasksHash != bundle2.TasksHash {
		t.Fatalf("expected deterministic hash: %s vs %s", bundle.TasksHash, bundle2.TasksHash)
	}

	// Verify hash matches manual computation.
	data, _ := json.Marshal(bundle.Tasks)
	h := sha256.Sum256(data)
	want := hex.EncodeToString(h[:])
	if bundle.TasksHash != want {
		t.Fatalf("hash mismatch: %s vs %s", bundle.TasksHash, want)
	}
}

func TestBuildAuditBundle_GoalScope(t *testing.T) {
	tasks := []state.TaskSpec{
		newTestTask("t1", "", "g1", state.TaskStatusCompleted, 100, 200),
		newTestTask("t2", "", "g1", state.TaskStatusCompleted, 110, 210),
	}
	req, _ := AuditExportRequest{GoalID: "g1"}.Normalize()
	bundle := BuildAuditBundle(tasks, nil, req, "", time.Now())

	if bundle.GoalID != "g1" {
		t.Fatalf("expected goal_id g1, got %q", bundle.GoalID)
	}
	if len(bundle.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(bundle.Tasks))
	}
}

func TestBuildAuditBundle_EmptyTasks(t *testing.T) {
	req, _ := AuditExportRequest{GoalID: "g1"}.Normalize()
	bundle := BuildAuditBundle(nil, nil, req, "", time.Now())
	if bundle.Summary.TotalTasks != 0 {
		t.Fatalf("expected 0 tasks, got %d", bundle.Summary.TotalTasks)
	}
	if len(bundle.Tasks) != 0 {
		t.Fatalf("expected empty tasks, got %d", len(bundle.Tasks))
	}
}

func TestBuildAuditBundle_TransitionsPreserved(t *testing.T) {
	task := newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200)
	task.Transitions = []state.TaskTransition{
		{To: state.TaskStatusPending, At: 100, Actor: "user", Source: "api", Reason: "created"},
		{From: state.TaskStatusPending, To: state.TaskStatusInProgress, At: 110, Actor: "scheduler", Source: "planner"},
		{From: state.TaskStatusInProgress, To: state.TaskStatusCompleted, At: 200, Actor: "agent", Source: "runtime"},
	}
	req, _ := AuditExportRequest{TaskID: "t1"}.Normalize()
	bundle := BuildAuditBundle([]state.TaskSpec{task}, nil, req, "", time.Now())

	if len(bundle.Tasks[0].Transitions) != 3 {
		t.Fatalf("expected 3 transitions, got %d", len(bundle.Tasks[0].Transitions))
	}
}

// ─── CollectDescendants ──────────────────────────────────────────────────────

func TestCollectDescendants_FullTree(t *testing.T) {
	all := []state.TaskSpec{
		newTestTask("root", "", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("c1", "root", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("c2", "root", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("c1a", "c1", "", state.TaskStatusCompleted, 100, 200),
		newTestTask("other", "", "", state.TaskStatusCompleted, 100, 200),
	}
	result := CollectDescendants("root", all)
	ids := make(map[string]bool)
	for _, t := range result {
		ids[t.TaskID] = true
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 tasks (root + 3 descendants), got %d", len(result))
	}
	for _, id := range []string{"root", "c1", "c2", "c1a"} {
		if !ids[id] {
			t.Errorf("missing %s", id)
		}
	}
	if ids["other"] {
		t.Error("should not include unrelated task 'other'")
	}
}

func TestCollectDescendants_NotFound(t *testing.T) {
	all := []state.TaskSpec{
		newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200),
	}
	result := CollectDescendants("nonexistent", all)
	if result != nil {
		t.Fatalf("expected nil, got %d tasks", len(result))
	}
}

func TestCollectDescendants_SingleNode(t *testing.T) {
	all := []state.TaskSpec{
		newTestTask("t1", "", "", state.TaskStatusCompleted, 100, 200),
	}
	result := CollectDescendants("t1", all)
	if len(result) != 1 {
		t.Fatalf("expected 1 task, got %d", len(result))
	}
}

// ─── JSON shape ──────────────────────────────────────────────────────────────

func TestAuditBundle_JSONShape(t *testing.T) {
	tasks := []state.TaskSpec{
		newTestTask("t1", "", "g1", state.TaskStatusCompleted, 100, 200),
	}
	runs := map[string][]state.TaskRun{
		"t1": {newTestRun("r1", "t1", 1, state.TaskRunStatusCompleted, 500)},
	}
	req, _ := AuditExportRequest{TaskID: "t1"}.Normalize()
	bundle := BuildAuditBundle(tasks, runs, req, "op", time.Unix(300, 0))

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	// Check top-level keys present.
	for _, key := range []string{"schema_version", "exported_at", "exported_by", "root_task_id", "summary", "tasks", "tasks_hash", "redactions"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}

	// Check tasks array has expected shape.
	tasksArr, ok := raw["tasks"].([]any)
	if !ok || len(tasksArr) != 1 {
		t.Fatalf("expected tasks array with 1 element")
	}
	taskObj, ok := tasksArr[0].(map[string]any)
	if !ok {
		t.Fatal("expected task object")
	}
	for _, key := range []string{"task_id", "title", "status", "depth", "authority", "verification", "transitions", "runs"} {
		if _, ok := taskObj[key]; !ok {
			t.Errorf("missing task key %q", key)
		}
	}
}
