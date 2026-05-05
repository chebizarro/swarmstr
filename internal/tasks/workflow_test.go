package tasks

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"metiq/internal/store/state"
)

type workflowTestExecutor struct {
	errs      map[string]error
	onExecute func(step *StepRun, def *StepDefinition)
	calls     []string
	mu        sync.Mutex
}

func (e *workflowTestExecutor) ExecuteStep(ctx context.Context, run *WorkflowRun, step *StepRun, def *StepDefinition) error {
	e.mu.Lock()
	e.calls = append(e.calls, def.ID)
	e.mu.Unlock()
	if e.onExecute != nil {
		e.onExecute(step, def)
	}
	if e.errs != nil {
		return e.errs[def.ID]
	}
	return nil
}

func TestWorkflowDependencySatisfiedHonorsFailurePolicy(t *testing.T) {
	cases := []struct {
		name string
		step StepRun
		def  StepDefinition
		want bool
	}{
		{name: "completed dependency satisfies", step: StepRun{Status: StepStatusCompleted}, want: true},
		{name: "skipped dependency satisfies", step: StepRun{Status: StepStatusSkipped}, def: StepDefinition{OnFailure: "skip"}, want: true},
		{name: "failed continue dependency satisfies", step: StepRun{Status: StepStatusFailed}, def: StepDefinition{OnFailure: "continue"}, want: true},
		{name: "failed default dependency blocks", step: StepRun{Status: StepStatusFailed}, want: false},
		{name: "pending dependency blocks", step: StepRun{Status: StepStatusPending}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dependencySatisfied(&tc.step, &tc.def); got != tc.want {
				t.Fatalf("dependencySatisfied() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkflowOnFailureContinueUnblocksDependents(t *testing.T) {
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: &workflowTestExecutor{}})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}

	def := WorkflowDefinition{
		ID:   "wf-continue",
		Name: "continue workflow",
		Steps: []StepDefinition{
			{ID: "build", Name: "Build", Type: StepTypeAgentTurn, OnFailure: "continue"},
			{ID: "deploy", Name: "Deploy", Type: StepTypeAgentTurn, Dependencies: []string{"build"}},
		},
	}
	run := &WorkflowRun{Version: 1, RunID: "run-continue", WorkflowID: def.ID, WorkflowName: def.Name, Status: WorkflowStatusRunning, Steps: []StepRun{
		{StepID: "build", StepName: "Build", Type: StepTypeAgentTurn, Status: StepStatusFailed, Attempt: 1, Error: "build failed"},
		{StepID: "deploy", StepName: "Deploy", Type: StepTypeAgentTurn, Status: StepStatusPending},
	}}

	o.scheduleReadySteps(context.Background(), run, &def)

	if run.Steps[1].Status != StepStatusCompleted {
		t.Fatalf("dependent step status = %s, want completed", run.Steps[1].Status)
	}
	if run.Status == WorkflowStatusRunning {
		t.Fatal("workflow stayed running after all steps reached terminal states")
	}
}

func TestWorkflowOnFailureSkipUnblocksDependents(t *testing.T) {
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: &workflowTestExecutor{}})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}

	def := WorkflowDefinition{
		ID:   "wf-skip",
		Name: "skip workflow",
		Steps: []StepDefinition{
			{ID: "optional", Name: "Optional", Type: StepTypeAgentTurn, OnFailure: "skip"},
			{ID: "cleanup", Name: "Cleanup", Type: StepTypeAgentTurn, Dependencies: []string{"optional"}},
		},
	}
	run := &WorkflowRun{Version: 1, RunID: "run-skip", WorkflowID: def.ID, WorkflowName: def.Name, Status: WorkflowStatusRunning, Steps: []StepRun{
		{StepID: "optional", StepName: "Optional", Type: StepTypeAgentTurn, Status: StepStatusSkipped, Attempt: 1, Error: "optional failed"},
		{StepID: "cleanup", StepName: "Cleanup", Type: StepTypeAgentTurn, Status: StepStatusPending},
	}}

	o.scheduleReadySteps(context.Background(), run, &def)

	if run.Steps[1].Status != StepStatusCompleted {
		t.Fatalf("dependent step status = %s, want completed", run.Steps[1].Status)
	}
	if run.Status != WorkflowStatusCompleted {
		t.Fatalf("workflow status = %s, want completed", run.Status)
	}
}

func TestWorkflowWaitStepBuiltIn(t *testing.T) {
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: &workflowTestExecutor{}})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	run := &WorkflowRun{Status: WorkflowStatusRunning, Steps: []StepRun{{StepID: "wait", StepName: "Wait", Type: StepTypeWait, Status: StepStatusReady}}}
	step := &run.Steps[0]
	start := time.Now()
	o.executeStep(context.Background(), run, step, &StepDefinition{ID: "wait", Name: "Wait", Type: StepTypeWait, Config: StepConfig{Duration: 20}})
	if step.Status != StepStatusCompleted {
		t.Fatalf("wait step status = %s, want completed", step.Status)
	}
	if time.Since(start) < 15*time.Millisecond {
		t.Fatalf("wait step completed too quickly")
	}
}

func TestWorkflowApprovalStepBlocks(t *testing.T) {
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: &workflowTestExecutor{}})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	run := &WorkflowRun{Status: WorkflowStatusRunning, Steps: []StepRun{{StepID: "approval", StepName: "Approval", Type: StepTypeApproval, Status: StepStatusReady}}}
	step := &run.Steps[0]
	o.executeStep(context.Background(), run, step, &StepDefinition{ID: "approval", Name: "Approval", Type: StepTypeApproval})
	if step.Status != StepStatusBlocked {
		t.Fatalf("approval step status = %s, want blocked", step.Status)
	}
	if run.Status != WorkflowStatusRunning {
		t.Fatalf("workflow status = %s, want running", run.Status)
	}
}

func TestWorkflowConditionalStepExecutesSelectedBranch(t *testing.T) {
	exec := &workflowTestExecutor{}
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: exec})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	run := &WorkflowRun{Status: WorkflowStatusRunning, Inputs: map[string]any{"use_true": false}, Steps: []StepRun{{StepID: "cond", StepName: "Cond", Type: StepTypeConditional, Status: StepStatusReady}}}
	step := &run.Steps[0]
	o.executeStep(context.Background(), run, step, &StepDefinition{ID: "cond", Name: "Cond", Type: StepTypeConditional, Condition: "use_true", Config: StepConfig{
		TrueStep:  &StepDefinition{ID: "true-step", Name: "True", Type: StepTypeAgentTurn},
		FalseStep: &StepDefinition{ID: "false-step", Name: "False", Type: StepTypeAgentTurn},
	}})
	if step.Status != StepStatusCompleted {
		t.Fatalf("conditional step status = %s, want completed", step.Status)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.calls) != 1 || exec.calls[0] != "false-step" {
		t.Fatalf("branch calls = %v, want [false-step]", exec.calls)
	}
}

func TestWorkflowParallelStepRunsAllSubsteps(t *testing.T) {
	exec := &workflowTestExecutor{}
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: exec})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	run := &WorkflowRun{Status: WorkflowStatusRunning, Steps: []StepRun{{StepID: "par", StepName: "Parallel", Type: StepTypeParallel, Status: StepStatusReady}}}
	step := &run.Steps[0]
	o.executeStep(context.Background(), run, step, &StepDefinition{ID: "par", Name: "Parallel", Type: StepTypeParallel, Config: StepConfig{Substeps: []StepDefinition{
		{ID: "a", Name: "A", Type: StepTypeAgentTurn},
		{ID: "b", Name: "B", Type: StepTypeAgentTurn},
	}}})
	if step.Status != StepStatusCompleted {
		t.Fatalf("parallel step status = %s, want completed", step.Status)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	seen := map[string]bool{}
	for _, c := range exec.calls {
		seen[c] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("parallel calls missing substeps: %v", exec.calls)
	}
}

func TestWorkflowAccumulatesUsageFromStepOutput(t *testing.T) {
	exec := &workflowTestExecutor{onExecute: func(step *StepRun, def *StepDefinition) {
		step.Output = map[string]any{
			"usage": map[string]any{
				"total_tokens":   11,
				"tool_calls":     2,
				"cost_micros_usd": int64(50),
			},
		}
	}}
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: exec})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	run := &WorkflowRun{Status: WorkflowStatusRunning, Steps: []StepRun{{StepID: "s1", StepName: "S1", Type: StepTypeAgentTurn, Status: StepStatusReady}}}
	step := &run.Steps[0]
	o.executeStep(context.Background(), run, step, &StepDefinition{ID: "s1", Name: "S1", Type: StepTypeAgentTurn})

	if run.Usage.TotalTokens != 11 || run.Usage.ToolCalls != 2 || run.Usage.CostMicrosUSD != 50 {
		t.Fatalf("unexpected run usage after first step: %+v", run.Usage)
	}

	step.Status = StepStatusReady
	o.executeStep(context.Background(), run, step, &StepDefinition{ID: "s1", Name: "S1", Type: StepTypeAgentTurn})
	if run.Usage.TotalTokens != 22 || run.Usage.ToolCalls != 4 || run.Usage.CostMicrosUSD != 100 {
		t.Fatalf("unexpected run usage after second step: %+v", run.Usage)
	}
}

func TestStepUsageFromOutputSupportsTopLevelUsageFields(t *testing.T) {
	usage, ok := stepUsageFromOutput(map[string]any{
		"prompt_tokens":     3,
		"completion_tokens": 4,
		"total_tokens":      7,
	})
	if !ok {
		t.Fatal("expected usage to be decoded")
	}
	if usage != (state.TaskUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}) {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestWorkflowScheduleMarksBlockedUntilDependenciesResolve(t *testing.T) {
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: &workflowTestExecutor{}})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	def := WorkflowDefinition{ID: "wf-blocked", Name: "blocked", Steps: []StepDefinition{
		{ID: "a", Name: "A", Type: StepTypeAgentTurn},
		{ID: "b", Name: "B", Type: StepTypeAgentTurn, Dependencies: []string{"a"}},
	}}
	run := &WorkflowRun{Version: 1, RunID: "run", WorkflowID: def.ID, WorkflowName: def.Name, Status: WorkflowStatusRunning, Steps: []StepRun{
		{StepID: "a", StepName: "A", Type: StepTypeAgentTurn, Status: StepStatusRunning},
		{StepID: "b", StepName: "B", Type: StepTypeAgentTurn, Status: StepStatusPending},
	}}
	o.scheduleReadySteps(context.Background(), run, &def)
	if run.Steps[1].Status != StepStatusBlocked {
		t.Fatalf("dependent step status = %s, want blocked", run.Steps[1].Status)
	}

	run.Steps[0].Status = StepStatusCompleted
	o.scheduleReadySteps(context.Background(), run, &def)
	if run.Steps[1].Status != StepStatusCompleted {
		t.Fatalf("dependent step status = %s, want completed after dependency completes", run.Steps[1].Status)
	}
}

func TestWorkflowParallelStepPropagatesSubstepFailure(t *testing.T) {
	exec := &workflowTestExecutor{errs: map[string]error{"b": fmt.Errorf("boom")}}
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Dir: t.TempDir(), Executor: exec})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	run := &WorkflowRun{Status: WorkflowStatusRunning, Steps: []StepRun{{StepID: "par", StepName: "Parallel", Type: StepTypeParallel, Status: StepStatusReady}}}
	step := &run.Steps[0]
	o.executeStep(context.Background(), run, step, &StepDefinition{ID: "par", Name: "Parallel", Type: StepTypeParallel, Config: StepConfig{Substeps: []StepDefinition{{ID: "a", Name: "A", Type: StepTypeAgentTurn}, {ID: "b", Name: "B", Type: StepTypeAgentTurn}}}})
	if step.Status != StepStatusFailed {
		t.Fatalf("parallel step status = %s, want failed", step.Status)
	}
}
