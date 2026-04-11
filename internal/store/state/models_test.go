package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigDocHash_deterministic(t *testing.T) {
	doc := ConfigDoc{
		Version: 1,
		DM:      DMPolicy{Policy: "open"},
		Relays: RelayPolicy{
			Read:  []string{"wss://relay.example.com"},
			Write: []string{"wss://relay.example.com"},
		},
	}
	h1 := doc.Hash()
	h2 := doc.Hash()
	if h1 != h2 {
		t.Errorf("Hash() is not deterministic: %q != %q", h1, h2)
	}
	if h1 == "" {
		t.Error("Hash() returned empty string")
	}
	if len(h1) != 64 {
		t.Errorf("Hash() returned unexpected length %d (want 64 hex chars)", len(h1))
	}
}

func TestConfigDocHash_changesOnMutation(t *testing.T) {
	doc := ConfigDoc{Version: 1, DM: DMPolicy{Policy: "open"}}
	h1 := doc.Hash()
	doc.DM.Policy = "disabled"
	h2 := doc.Hash()
	if h1 == h2 {
		t.Error("Hash() should change when content changes")
	}
}

func TestConfigDocHash_format(t *testing.T) {
	doc := ConfigDoc{Version: 1}
	h := doc.Hash()
	if !strings.HasPrefix(h, "") {
		t.Errorf("unexpected hash format: %q", h)
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("Hash() contains non-hex character %q in %q", c, h)
			break
		}
	}
}

func TestConfigDocHash_emptyDoc(t *testing.T) {
	doc := ConfigDoc{}
	h := doc.Hash()
	if h == "" {
		t.Error("Hash() of empty ConfigDoc returned empty string")
	}
}

func TestConfigDocHash_extraFields(t *testing.T) {
	doc := ConfigDoc{
		Version: 1,
		Extra:   map[string]any{"key": "value"},
	}
	h1 := doc.Hash()
	doc.Extra["key"] = "different"
	h2 := doc.Hash()
	if h1 == h2 {
		t.Error("Hash() should change when Extra content changes")
	}
}

func TestTaskLifecycleParsers(t *testing.T) {
	if got, ok := ParseGoalStatus("ACTIVE"); !ok || got != GoalStatusActive {
		t.Fatalf("ParseGoalStatus() = %q, %v", got, ok)
	}
	if got, ok := ParseTaskStatus("In_Progress"); !ok || got != TaskStatusInProgress {
		t.Fatalf("ParseTaskStatus() = %q, %v", got, ok)
	}
	if got, ok := ParseTaskRunStatus("retrying"); !ok || got != TaskRunStatusRetrying {
		t.Fatalf("ParseTaskRunStatus() = %q, %v", got, ok)
	}
	if got, ok := ParseTaskPriority(""); !ok || got != TaskPriorityMedium {
		t.Fatalf("ParseTaskPriority(\"\") = %q, %v", got, ok)
	}
	if NormalizeTaskStatus("bogus") != "" {
		t.Fatalf("NormalizeTaskStatus should return empty for unknown values")
	}
}

func TestGoalSpecNormalizeAndValidate(t *testing.T) {
	goal := GoalSpec{
		GoalID: "goal-1",
		Title:  "Ship milestone",
	}
	norm := goal.Normalize()
	if norm.Version != 1 {
		t.Fatalf("expected version=1, got %d", norm.Version)
	}
	if norm.Status != GoalStatusPending {
		t.Fatalf("expected pending goal status, got %q", norm.Status)
	}
	if norm.Priority != TaskPriorityMedium {
		t.Fatalf("expected medium goal priority, got %q", norm.Priority)
	}
	if err := norm.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	bad := GoalSpec{GoalID: "goal-2", Title: "Broken", Status: GoalStatus("wat")}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "invalid goal status") {
		t.Fatalf("expected invalid goal status error, got %v", err)
	}
}

func TestTaskSpecNormalizeAndValidate(t *testing.T) {
	task := TaskSpec{
		TaskID:       "task-1",
		GoalID:       "goal-1",
		ParentTaskID: "task-root",
		PlanID:       "plan-1",
		Title:        "Implement transport envelope",
		Instructions: "Define the canonical task payload and persist it.",
		ExpectedOutputs: []TaskOutputSpec{{
			Name:      "task-envelope",
			Format:    "json",
			SchemaRef: "schemas/task-envelope.json",
			Required:  true,
		}},
		AcceptanceCriteria: []TaskAcceptanceCriterion{{
			Type:        "schema",
			Description: "The payload validates against the schema.",
			Required:    true,
		}},
		MemoryScope: AgentMemoryScopeProject,
	}
	norm := task.Normalize()
	if norm.Version != 1 {
		t.Fatalf("expected version=1, got %d", norm.Version)
	}
	if norm.Status != TaskStatusPending {
		t.Fatalf("expected pending task status, got %q", norm.Status)
	}
	if norm.Priority != TaskPriorityMedium {
		t.Fatalf("expected medium task priority, got %q", norm.Priority)
	}
	if norm.MemoryScope != AgentMemoryScopeProject {
		t.Fatalf("expected project memory scope, got %q", norm.MemoryScope)
	}
	if err := norm.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	badScope := task
	badScope.MemoryScope = AgentMemoryScope("bogus")
	if err := badScope.Validate(); err == nil || !strings.Contains(err.Error(), "invalid memory_scope") {
		t.Fatalf("expected invalid memory_scope error, got %v", err)
	}

	badOutput := task
	badOutput.ExpectedOutputs = []TaskOutputSpec{{}}
	if err := badOutput.Validate(); err == nil || !strings.Contains(err.Error(), "expected_outputs[0].name") {
		t.Fatalf("expected invalid expected output error, got %v", err)
	}
}

func TestTaskRunNormalizeAndValidate(t *testing.T) {
	run := TaskRun{
		RunID:   "run-1",
		TaskID:  "task-1",
		GoalID:  "goal-1",
		AgentID: "builder",
	}
	norm := run.Normalize()
	if norm.Version != 1 {
		t.Fatalf("expected version=1, got %d", norm.Version)
	}
	if norm.Attempt != 1 {
		t.Fatalf("expected attempt=1, got %d", norm.Attempt)
	}
	if norm.Status != TaskRunStatusQueued {
		t.Fatalf("expected queued run status, got %q", norm.Status)
	}
	if err := norm.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	bad := run
	bad.Status = TaskRunStatus("unknown")
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "invalid run status") {
		t.Fatalf("expected invalid run status error, got %v", err)
	}
}

func TestTaskLifecycleTransitionsAndRetrySemantics(t *testing.T) {
	task := TaskSpec{TaskID: "task-1", Title: "Lifecycle", Instructions: "Drive transitions."}.Normalize()
	if err := task.ApplyTransition(TaskStatusPlanned, 100, "planner", "planner", "initial plan", nil); err != nil {
		t.Fatalf("ApplyTransition planned: %v", err)
	}
	if err := task.ApplyTransition(TaskStatusReady, 110, "planner", "planner", "ready to execute", map[string]any{"step": 1}); err != nil {
		t.Fatalf("ApplyTransition ready: %v", err)
	}
	if err := task.ApplyTransition(TaskStatusCompleted, 120, "runner", "runtime", "skip verify", nil); err == nil {
		t.Fatal("expected illegal task transition to be rejected")
	}
	if err := task.ApplyTransition(TaskStatusInProgress, 120, "runner", "runtime", "started", nil); err != nil {
		t.Fatalf("ApplyTransition in_progress: %v", err)
	}
	if err := task.ApplyTransition(TaskStatusVerifying, 130, "verifier", "runtime", "verifying output", nil); err != nil {
		t.Fatalf("ApplyTransition verifying: %v", err)
	}
	if err := task.ApplyTransition(TaskStatusCompleted, 140, "verifier", "runtime", "verified", nil); err != nil {
		t.Fatalf("ApplyTransition completed: %v", err)
	}
	if len(task.Transitions) != 5 {
		t.Fatalf("expected 5 task transitions, got %d", len(task.Transitions))
	}
	if task.UpdatedAt != 140 || task.Status != TaskStatusCompleted {
		t.Fatalf("unexpected final task state: %+v", task)
	}

	failedRun := TaskRun{RunID: "run-1", TaskID: task.TaskID, Attempt: 1}.Normalize()
	if err := failedRun.ApplyTransition(TaskRunStatusRunning, 200, "runner", "runtime", "started", nil); err != nil {
		t.Fatalf("run ApplyTransition running: %v", err)
	}
	if err := failedRun.ApplyTransition(TaskRunStatusFailed, 210, "runner", "runtime", "tool failed", nil); err != nil {
		t.Fatalf("run ApplyTransition failed: %v", err)
	}
	if failedRun.StartedAt != 200 || failedRun.EndedAt != 210 {
		t.Fatalf("unexpected failed run timing: %+v", failedRun)
	}
	second, err := NewTaskRunAttempt(task, "run-2", []TaskRun{failedRun}, 220, "retry", "runner", "runtime")
	if err != nil {
		t.Fatalf("NewTaskRunAttempt retry: %v", err)
	}
	if second.Attempt != 2 || second.Status != TaskRunStatusQueued {
		t.Fatalf("unexpected retry run: %+v", second)
	}
	if len(second.Transitions) != 1 || second.Transitions[0].To != TaskRunStatusQueued {
		t.Fatalf("expected initial queued transition, got %+v", second.Transitions)
	}
	if _, err := NewTaskRunAttempt(task, "run-2", []TaskRun{failedRun, second}, 230, "retry", "runner", "runtime"); err == nil {
		t.Fatal("expected duplicate run_id error")
	}
}

func TestTaskModelsJSONRoundTrip(t *testing.T) {
	original := struct {
		Goal GoalSpec `json:"goal"`
		Task TaskSpec `json:"task"`
		Run  TaskRun  `json:"run"`
	}{
		Goal: GoalSpec{
			Version:         1,
			GoalID:          "goal-42",
			Title:           "Ship canonical autonomy task model",
			Instructions:    "Define the persistence schema for goals, tasks, and runs.",
			RequestedBy:     "operator",
			SessionID:       "session-1",
			Status:          GoalStatusActive,
			Priority:        TaskPriorityHigh,
			Constraints:     []string{"nostr-native", "backward-compatible"},
			SuccessCriteria: []string{"schemas merged", "tests passing"},
			Authority: TaskAuthority{
				Role:               "director",
				ApprovalMode:       "act_with_approval",
				CanAct:             true,
				CanDelegate:        true,
				CanEscalate:        true,
				EscalationRequired: false,
				RiskClass:          "medium",
				AllowedAgents:      []string{"builder", "reviewer"},
				MaxDelegationDepth: 2,
			},
			Budget: TaskBudget{
				MaxPromptTokens:     32000,
				MaxCompletionTokens: 8000,
				MaxTotalTokens:      40000,
				MaxRuntimeMS:        120000,
				MaxToolCalls:        24,
				MaxDelegations:      4,
				MaxCostMicrosUSD:    250000,
			},
			CreatedAt: 111,
			UpdatedAt: 222,
			Meta:      map[string]any{"source": "qrx.1.1"},
		},
		Task: TaskSpec{
			Version:       1,
			TaskID:        "task-42.1",
			GoalID:        "goal-42",
			ParentTaskID:  "task-42",
			PlanID:        "plan-42",
			SessionID:     "session-1",
			Title:         "Model persistence structs",
			Instructions:  "Add GoalSpec, TaskSpec, and TaskRun to internal/store/state.",
			Inputs:        map[string]any{"package": "internal/store/state"},
			Dependencies:  []string{"task-42.bootstrap"},
			AssignedAgent: "builder",
			CurrentRunID:  "run-42.1",
			LastRunID:     "run-42.1",
			Status:        TaskStatusReady,
			Priority:      TaskPriorityHigh,
			Authority: TaskAuthority{
				Role:         "builder",
				CanAct:       true,
				CanDelegate:  false,
				CanEscalate:  true,
				ApprovalMode: "act_with_approval",
			},
			MemoryScope:  AgentMemoryScopeProject,
			ToolProfile:  "coding",
			EnabledTools: []string{"read_file", "apply_edits"},
			Budget: TaskBudget{
				MaxTotalTokens:   12000,
				MaxRuntimeMS:     60000,
				MaxToolCalls:     8,
				MaxDelegations:   0,
				MaxCostMicrosUSD: 75000,
			},
			ExpectedOutputs: []TaskOutputSpec{{
				Name:        "go-types",
				Description: "Go structs and helper methods",
				Format:      "go",
				SchemaRef:   "internal/store/state/models.go",
				Required:    true,
			}},
			AcceptanceCriteria: []TaskAcceptanceCriterion{{
				Type:        "test",
				Description: "go test ./internal/store/state passes",
				Required:    true,
			}},
			CreatedAt: 333,
			UpdatedAt: 444,
			Transitions: []TaskTransition{{From: TaskStatusPending, To: TaskStatusReady, At: 444, Actor: "planner", Source: "planner", Reason: "ready"}},
			Meta:        map[string]any{"epic": "swarmstr-qrx.1"},
		},
		Run: TaskRun{
			Version:       1,
			RunID:         "run-42.1",
			TaskID:        "task-42.1",
			GoalID:        "goal-42",
			ParentRunID:   "run-42",
			SessionID:     "session-1",
			AgentID:       "builder",
			Attempt:       1,
			Status:        TaskRunStatusRunning,
			StartedAt:     555,
			EndedAt:       666,
			Trigger:       "manual",
			CheckpointRef: "checkpoint:run-42.1:step-2",
			Result: TaskResultRef{
				Kind: "event",
				ID:   "event-123",
				URI:  "nostr:event-123",
				Hash: "deadbeef",
			},
			Usage: TaskUsage{
				PromptTokens:     1234,
				CompletionTokens: 432,
				TotalTokens:      1666,
				WallClockMS:      8123,
				ToolCalls:        3,
				Delegations:      1,
				CostMicrosUSD:    15000,
			},
			Transitions: []TaskRunTransition{{From: TaskRunStatusQueued, To: TaskRunStatusRunning, At: 555, Actor: "runner", Source: "runtime", Reason: "started"}},
			Meta:        map[string]any{"trace_id": "trace-1"},
		},
	}

	blob, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded struct {
		Goal GoalSpec `json:"goal"`
		Task TaskSpec `json:"task"`
		Run  TaskRun  `json:"run"`
	}
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Goal.GoalID != original.Goal.GoalID || decoded.Task.TaskID != original.Task.TaskID || decoded.Run.RunID != original.Run.RunID {
		t.Fatalf("round-trip lost identifiers: got goal=%q task=%q run=%q", decoded.Goal.GoalID, decoded.Task.TaskID, decoded.Run.RunID)
	}
	if decoded.Task.MemoryScope != AgentMemoryScopeProject {
		t.Fatalf("expected project memory scope after round-trip, got %q", decoded.Task.MemoryScope)
	}
	if decoded.Run.Result.URI != "nostr:event-123" {
		t.Fatalf("expected result URI to survive round-trip, got %q", decoded.Run.Result.URI)
	}
}

// ── Plan model tests ──────────────────────────────────────────────────────────

func TestPlanStatusParsing(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want PlanStatus
		ok   bool
	}{
		{"draft", PlanStatusDraft, true},
		{"ACTIVE", PlanStatusActive, true},
		{" Revising ", PlanStatusRevising, true},
		{"completed", PlanStatusCompleted, true},
		{"failed", PlanStatusFailed, true},
		{"cancelled", PlanStatusCancelled, true},
		{"bogus", "", false},
		{"", "", false},
	} {
		got, ok := ParsePlanStatus(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParsePlanStatus(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPlanStepStatusParsing(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want PlanStepStatus
		ok   bool
	}{
		{"pending", PlanStepStatusPending, true},
		{"ready", PlanStepStatusReady, true},
		{"in_progress", PlanStepStatusInProgress, true},
		{"completed", PlanStepStatusCompleted, true},
		{"failed", PlanStepStatusFailed, true},
		{"skipped", PlanStepStatusSkipped, true},
		{"nope", "", false},
	} {
		got, ok := ParsePlanStepStatus(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParsePlanStepStatus(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPlanSpecNormalize(t *testing.T) {
	p := PlanSpec{
		PlanID: "plan-1",
		Title:  "Test plan",
		Steps: []PlanStep{
			{StepID: "s1", Title: "Step 1"},
		},
	}
	norm := p.Normalize()
	if norm.Version != 1 {
		t.Fatalf("expected version=1, got %d", norm.Version)
	}
	if norm.Revision != 1 {
		t.Fatalf("expected revision=1, got %d", norm.Revision)
	}
	if norm.Status != PlanStatusDraft {
		t.Fatalf("expected status=draft, got %q", norm.Status)
	}
	if norm.Steps[0].Status != PlanStepStatusPending {
		t.Fatalf("expected step status=pending, got %q", norm.Steps[0].Status)
	}
}

func TestPlanSpecValidation(t *testing.T) {
	base := func() PlanSpec {
		return PlanSpec{
			PlanID: "plan-1",
			Title:  "Valid plan",
			Status: PlanStatusDraft,
			Steps: []PlanStep{
				{StepID: "s1", Title: "Step 1", Status: PlanStepStatusPending},
			},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid plan should not error: %v", err)
	}

	// Missing plan_id.
	p := base()
	p.PlanID = ""
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for missing plan_id")
	}

	// No steps.
	p = base()
	p.Steps = nil
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for empty steps")
	}

	// Duplicate step IDs.
	p = base()
	p.Steps = append(p.Steps, PlanStep{StepID: "s1", Title: "Dupe"})
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for duplicate step_id")
	}

	// Unknown dependency.
	p = base()
	p.Steps[0].DependsOn = []string{"nonexistent"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for unknown dependency")
	}

	// Self-dependency.
	p = base()
	p.Steps[0].DependsOn = []string{"s1"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for self-dependency")
	}

	// Invalid plan status.
	p = base()
	p.Status = "bogus"
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for invalid plan status")
	}
}

func TestPlanSpecHasCycle(t *testing.T) {
	// No cycle: s1 -> s2 -> s3.
	noCycle := PlanSpec{
		PlanID: "p1",
		Title:  "No cycle",
		Steps: []PlanStep{
			{StepID: "s1", Title: "A"},
			{StepID: "s2", Title: "B", DependsOn: []string{"s1"}},
			{StepID: "s3", Title: "C", DependsOn: []string{"s2"}},
		},
	}
	if noCycle.HasCycle() {
		t.Fatal("expected no cycle")
	}

	// Cycle: s1 -> s2 -> s3 -> s1.
	withCycle := PlanSpec{
		PlanID: "p2",
		Title:  "Has cycle",
		Steps: []PlanStep{
			{StepID: "s1", Title: "A", DependsOn: []string{"s3"}},
			{StepID: "s2", Title: "B", DependsOn: []string{"s1"}},
			{StepID: "s3", Title: "C", DependsOn: []string{"s2"}},
		},
	}
	if !withCycle.HasCycle() {
		t.Fatal("expected cycle")
	}
}

func TestPlanSpecReadySteps(t *testing.T) {
	plan := PlanSpec{
		PlanID: "p1",
		Title:  "Ready test",
		Steps: []PlanStep{
			{StepID: "s1", Title: "A", Status: PlanStepStatusCompleted},
			{StepID: "s2", Title: "B", Status: PlanStepStatusPending, DependsOn: []string{"s1"}},
			{StepID: "s3", Title: "C", Status: PlanStepStatusPending, DependsOn: []string{"s2"}},
			{StepID: "s4", Title: "D", Status: PlanStepStatusPending},
		},
	}
	ready := plan.ReadySteps()
	ids := make(map[string]bool)
	for _, s := range ready {
		ids[s.StepID] = true
	}
	if !ids["s2"] {
		t.Fatal("s2 should be ready (s1 completed)")
	}
	if !ids["s4"] {
		t.Fatal("s4 should be ready (no deps)")
	}
	if ids["s3"] {
		t.Fatal("s3 should NOT be ready (s2 still pending)")
	}
}

func TestPlanSpecIsTerminal(t *testing.T) {
	for _, tc := range []struct {
		status   PlanStatus
		terminal bool
	}{
		{PlanStatusDraft, false},
		{PlanStatusActive, false},
		{PlanStatusRevising, false},
		{PlanStatusCompleted, true},
		{PlanStatusFailed, true},
		{PlanStatusCancelled, true},
	} {
		p := PlanSpec{Status: tc.status}
		if p.IsTerminal() != tc.terminal {
			t.Errorf("PlanSpec{Status:%q}.IsTerminal() = %v, want %v", tc.status, !tc.terminal, tc.terminal)
		}
	}
}

func TestPlanSpecJSONRoundTrip(t *testing.T) {
	original := PlanSpec{
		Version:          1,
		PlanID:           "plan-abc",
		GoalID:           "goal-1",
		Title:            "Decompose goal",
		Revision:         2,
		Status:           PlanStatusActive,
		Assumptions:      []string{"API is stable"},
		Risks:            []string{"Rate limits"},
		RollbackStrategy: "Revert all tasks",
		CreatedAt:        1000,
		UpdatedAt:        2000,
		Steps: []PlanStep{
			{StepID: "s1", Title: "Fetch data", Status: PlanStepStatusCompleted, Outputs: []TaskOutputSpec{{Name: "data", Format: "json"}}},
			{StepID: "s2", Title: "Process", Status: PlanStepStatusPending, DependsOn: []string{"s1"}, Agent: "worker-1"},
		},
		Meta: map[string]any{"source": "planner"},
	}
	blob, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded PlanSpec
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.PlanID != original.PlanID {
		t.Fatalf("plan_id mismatch: %q vs %q", decoded.PlanID, original.PlanID)
	}
	if decoded.Revision != 2 {
		t.Fatalf("revision mismatch: %d", decoded.Revision)
	}
	if len(decoded.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(decoded.Steps))
	}
	if decoded.Steps[1].DependsOn[0] != "s1" {
		t.Fatalf("dependency lost in round-trip")
	}
	if decoded.Steps[0].Outputs[0].Name != "data" {
		t.Fatalf("output lost in round-trip")
	}
}
