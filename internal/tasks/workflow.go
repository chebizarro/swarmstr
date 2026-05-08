package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// WorkflowStatus describes the lifecycle state of a workflow.
type WorkflowStatus string

const (
	WorkflowStatusPending   WorkflowStatus = "pending"
	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusPaused    WorkflowStatus = "paused"
	WorkflowStatusCompleted WorkflowStatus = "completed"
	WorkflowStatusFailed    WorkflowStatus = "failed"
	WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// StepStatus describes the lifecycle state of a workflow step.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusReady     StepStatus = "ready"
	StepStatusRunning   StepStatus = "running"
	StepStatusBlocked   StepStatus = "blocked"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

// StepType describes the kind of action a step performs.
type StepType string

const (
	StepTypeAgentTurn   StepType = "agent_turn"
	StepTypeACPDispatch StepType = "acp_dispatch"
	StepTypeGatewayCall StepType = "gateway_call"
	StepTypeWait        StepType = "wait"
	StepTypeApproval    StepType = "approval"
	StepTypeParallel    StepType = "parallel"
	StepTypeConditional StepType = "conditional"
)

// WorkflowDefinition is the schema for a workflow.
type WorkflowDefinition struct {
	Version     int                   `json:"version"`
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Steps       []StepDefinition      `json:"steps"`
	Inputs      map[string]InputSpec  `json:"inputs,omitempty"`
	Outputs     map[string]OutputSpec `json:"outputs,omitempty"`
	Authority   state.TaskAuthority   `json:"authority,omitempty"`
	Budget      state.TaskBudget      `json:"budget,omitempty"`
	Timeout     int64                 `json:"timeout_ms,omitempty"`
	Meta        map[string]any        `json:"meta,omitempty"`
	CreatedAt   int64                 `json:"created_at,omitempty"`
	UpdatedAt   int64                 `json:"updated_at,omitempty"`
}

// StepDefinition defines a single step in a workflow.
type StepDefinition struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Type         StepType       `json:"type"`
	Dependencies []string       `json:"dependencies,omitempty"`
	Config       StepConfig     `json:"config"`
	Timeout      int64          `json:"timeout_ms,omitempty"`
	RetryPolicy  *RetryPolicy   `json:"retry_policy,omitempty"`
	OnFailure    string         `json:"on_failure,omitempty"` // "fail" | "continue" | "skip"
	Condition    string         `json:"condition,omitempty"`  // expression to evaluate
	Meta         map[string]any `json:"meta,omitempty"`
}

// StepConfig contains type-specific configuration for a step.
type StepConfig struct {
	// AgentTurn
	AgentID      string   `json:"agent_id,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	ToolProfile  string   `json:"tool_profile,omitempty"`
	EnabledTools []string `json:"enabled_tools,omitempty"`

	// ACPDispatch
	PeerPubKey string `json:"peer_pubkey,omitempty"`

	// GatewayCall
	Method string         `json:"method,omitempty"`
	Params map[string]any `json:"params,omitempty"`

	// Wait
	Duration int64  `json:"duration_ms,omitempty"`
	Until    string `json:"until,omitempty"` // timestamp or expression

	// Approval
	ApprovalMessage string   `json:"approval_message,omitempty"`
	Approvers       []string `json:"approvers,omitempty"`

	// Parallel
	Substeps []StepDefinition `json:"substeps,omitempty"`

	// Conditional
	TrueStep  *StepDefinition `json:"true_step,omitempty"`
	FalseStep *StepDefinition `json:"false_step,omitempty"`
}

// RetryPolicy controls step retry behavior.
type RetryPolicy struct {
	MaxAttempts int     `json:"max_attempts"`
	DelayMS     int64   `json:"delay_ms"`
	Backoff     float64 `json:"backoff,omitempty"` // multiplier for exponential backoff
}

// InputSpec defines an expected input for a workflow.
type InputSpec struct {
	Type        string `json:"type"` // string, number, boolean, object, array
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// OutputSpec defines an expected output from a workflow.
type OutputSpec struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	FromStep    string `json:"from_step,omitempty"`
}

// WorkflowRun is an execution instance of a workflow.
type WorkflowRun struct {
	Version      int             `json:"version"`
	RunID        string          `json:"run_id"`
	WorkflowID   string          `json:"workflow_id"`
	WorkflowName string          `json:"workflow_name"`
	Status       WorkflowStatus  `json:"status"`
	Inputs       map[string]any  `json:"inputs,omitempty"`
	Outputs      map[string]any  `json:"outputs,omitempty"`
	Steps        []StepRun       `json:"steps"`
	CurrentStep  string          `json:"current_step,omitempty"`
	Usage        state.TaskUsage `json:"usage,omitempty"`
	Error        string          `json:"error,omitempty"`
	StartedAt    int64           `json:"started_at,omitempty"`
	EndedAt      int64           `json:"ended_at,omitempty"`
	CreatedAt    int64           `json:"created_at"`
	UpdatedAt    int64           `json:"updated_at"`
	Meta         map[string]any  `json:"meta,omitempty"`
}

// StepRun is an execution instance of a workflow step.
type StepRun struct {
	StepID    string         `json:"step_id"`
	StepName  string         `json:"step_name"`
	Type      StepType       `json:"type"`
	Status    StepStatus     `json:"status"`
	Attempt   int            `json:"attempt"`
	TaskID    string         `json:"task_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	Output    map[string]any `json:"output,omitempty"`
	Error     string         `json:"error,omitempty"`
	StartedAt int64          `json:"started_at,omitempty"`
	EndedAt   int64          `json:"ended_at,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// WorkflowOrchestrator manages workflow execution.
type WorkflowOrchestrator struct {
	mu          sync.RWMutex
	definitions map[string]*WorkflowDefinition
	runs        map[string]*WorkflowRun
	ledger      *Ledger
	emitter     *EventEmitter
	store       WorkflowStore
	executor    StepExecutor
}

// WorkflowStore persists workflow definitions and runs.
type WorkflowStore interface {
	LoadDefinitions(ctx context.Context) ([]*WorkflowDefinition, error)
	LoadRuns(ctx context.Context) ([]*WorkflowRun, error)
	SaveDefinition(ctx context.Context, def *WorkflowDefinition) error
	SaveRun(ctx context.Context, run *WorkflowRun) error
}

// StepExecutor executes workflow steps.
type StepExecutor interface {
	ExecuteStep(ctx context.Context, run *WorkflowRun, step *StepRun, def *StepDefinition) error
}

// OrchestratorConfig configures the workflow orchestrator.
type OrchestratorConfig struct {
	Dir      string
	Store    WorkflowStore
	Ledger   *Ledger
	Emitter  *EventEmitter
	Executor StepExecutor
}

type workflowRunRecovery struct {
	run *WorkflowRun
	def *WorkflowDefinition
}

type workflowStepReconciliation struct {
	stepID string
	task   *state.TaskSpec
	run    *state.TaskRun
}

// NewWorkflowOrchestrator creates a new workflow orchestrator.
func NewWorkflowOrchestrator(cfg OrchestratorConfig) (*WorkflowOrchestrator, error) {
	store := cfg.Store
	if store == nil {
		if cfg.Dir == "" {
			return nil, fmt.Errorf("workflow store or directory is required")
		}
		fsStore, err := NewFSWorkflowStore(cfg.Dir)
		if err != nil {
			return nil, err
		}
		store = fsStore
	}

	o := &WorkflowOrchestrator{
		definitions: make(map[string]*WorkflowDefinition),
		runs:        make(map[string]*WorkflowRun),
		ledger:      cfg.Ledger,
		emitter:     cfg.Emitter,
		store:       store,
		executor:    cfg.Executor,
	}

	// Load existing definitions and runs
	if err := o.loadDefinitions(context.Background()); err != nil {
		// Log warning but continue
	}
	if err := o.loadRuns(context.Background()); err != nil {
		// Log warning but continue
	}

	return o, nil
}

func (o *WorkflowOrchestrator) loadDefinitions(ctx context.Context) error {
	defs, err := o.store.LoadDefinitions(ctx)
	if err != nil {
		return err
	}
	for _, def := range defs {
		if def == nil || strings.TrimSpace(def.ID) == "" {
			continue
		}
		o.definitions[def.ID] = def
	}
	return nil
}

func (o *WorkflowOrchestrator) loadRuns(ctx context.Context) error {
	runs, err := o.store.LoadRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run == nil || strings.TrimSpace(run.RunID) == "" {
			continue
		}
		o.runs[run.RunID] = run
	}
	return nil
}

// RegisterDefinition adds or updates a workflow definition.
func (o *WorkflowOrchestrator) RegisterDefinition(ctx context.Context, def WorkflowDefinition) error {
	if def.ID == "" {
		return fmt.Errorf("workflow ID is required")
	}
	if def.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if len(def.Steps) == 0 {
		return fmt.Errorf("workflow must have at least one step")
	}

	// Validate step IDs are unique
	seen := make(map[string]bool)
	for _, step := range def.Steps {
		if step.ID == "" {
			return fmt.Errorf("step ID is required")
		}
		if seen[step.ID] {
			return fmt.Errorf("duplicate step ID: %s", step.ID)
		}
		seen[step.ID] = true
	}

	// Validate dependencies reference valid steps
	for _, step := range def.Steps {
		for _, dep := range step.Dependencies {
			if !seen[dep] {
				return fmt.Errorf("step %q depends on unknown step %q", step.ID, dep)
			}
		}
	}

	now := time.Now().Unix()
	if def.CreatedAt == 0 {
		def.CreatedAt = now
	}
	def.UpdatedAt = now
	if def.Version == 0 {
		def.Version = 1
	}

	o.mu.Lock()
	o.definitions[def.ID] = &def
	o.mu.Unlock()

	// Persist
	return o.saveDefinition(ctx, &def)
}

func (o *WorkflowOrchestrator) saveDefinition(ctx context.Context, def *WorkflowDefinition) error {
	return o.store.SaveDefinition(ctx, def)
}

// GetDefinition retrieves a workflow definition by ID.
func (o *WorkflowOrchestrator) GetDefinition(ctx context.Context, id string) (*WorkflowDefinition, error) {
	o.mu.RLock()
	def, ok := o.definitions[id]
	o.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("workflow %q not found", id)
	}
	return def, nil
}

// ListDefinitions returns all workflow definitions.
func (o *WorkflowOrchestrator) ListDefinitions(ctx context.Context) []*WorkflowDefinition {
	o.mu.RLock()
	defer o.mu.RUnlock()

	defs := make([]*WorkflowDefinition, 0, len(o.definitions))
	for _, def := range o.definitions {
		defs = append(defs, def)
	}
	return defs
}

// StartRun creates and starts a new workflow run.
func (o *WorkflowOrchestrator) StartRun(ctx context.Context, workflowID string, inputs map[string]any) (*WorkflowRun, error) {
	def, err := o.GetDefinition(ctx, workflowID)
	if err != nil {
		return nil, err
	}

	// Validate required inputs
	for name, spec := range def.Inputs {
		if spec.Required {
			if _, ok := inputs[name]; !ok {
				if spec.Default == nil {
					return nil, fmt.Errorf("required input %q not provided", name)
				}
				inputs[name] = spec.Default
			}
		}
	}

	now := time.Now().Unix()
	runID := generateID("wfrun")

	// Initialize step runs
	steps := make([]StepRun, len(def.Steps))
	for i, stepDef := range def.Steps {
		steps[i] = StepRun{
			StepID:   stepDef.ID,
			StepName: stepDef.Name,
			Type:     stepDef.Type,
			Status:   StepStatusPending,
			Attempt:  0,
		}
	}

	run := &WorkflowRun{
		Version:      1,
		RunID:        runID,
		WorkflowID:   workflowID,
		WorkflowName: def.Name,
		Status:       WorkflowStatusRunning,
		Inputs:       inputs,
		Steps:        steps,
		CreatedAt:    now,
		UpdatedAt:    now,
		StartedAt:    now,
	}

	o.mu.Lock()
	o.runs[runID] = run
	o.mu.Unlock()

	// Persist
	if err := o.saveRun(ctx, run); err != nil {
		// Log but don't fail
	}

	// Emit event
	if o.emitter != nil {
		o.emitter.Emit(ctx, Event{
			Type:      EventWorkflowStart,
			Timestamp: now,
			Meta: map[string]any{
				"run_id":      runID,
				"workflow_id": workflowID,
				"name":        def.Name,
			},
		})
	}

	// Schedule ready steps
	go o.scheduleReadySteps(context.Background(), run, def)

	return run, nil
}

func (o *WorkflowOrchestrator) saveRun(ctx context.Context, run *WorkflowRun) error {
	return o.store.SaveRun(ctx, run)
}

// RecoverNonTerminalRuns reconciles durable workflow runs after daemon startup
// and schedules only unfinished work. Completed child task/run state is applied
// before scheduling so recovered workflows never rerun already-finished child
// steps.
func (o *WorkflowOrchestrator) RecoverNonTerminalRuns(ctx context.Context) error {
	if o == nil {
		return fmt.Errorf("workflow orchestrator is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	recoveries := o.recoverableRuns()
	var firstErr error
	for _, rec := range recoveries {
		if rec.run == nil || rec.def == nil {
			continue
		}
		changed, err := o.reconcileChildStepState(ctx, rec.run, rec.def)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if changed {
			if saveErr := o.saveRun(ctx, rec.run); saveErr != nil && firstErr == nil {
				firstErr = saveErr
			}
		}
		if rec.run.Status == WorkflowStatusRunning {
			recoveryCtx := context.WithoutCancel(ctx)
			go o.scheduleReadySteps(recoveryCtx, rec.run, rec.def)
		}
	}
	return firstErr
}

func (o *WorkflowOrchestrator) recoverableRuns() []workflowRunRecovery {
	o.mu.RLock()
	defer o.mu.RUnlock()
	recoveries := make([]workflowRunRecovery, 0)
	for _, run := range o.runs {
		if run == nil || workflowRunTerminal(run.Status) {
			continue
		}
		def := o.definitions[run.WorkflowID]
		if def == nil {
			continue
		}
		recoveries = append(recoveries, workflowRunRecovery{run: run, def: def})
	}
	return recoveries
}

func (o *WorkflowOrchestrator) reconcileChildStepState(ctx context.Context, run *WorkflowRun, _ *WorkflowDefinition) (bool, error) {
	if o == nil || o.ledger == nil || run == nil {
		return false, nil
	}
	outcomes := make([]workflowStepReconciliation, 0, len(run.Steps))
	var firstErr error
	for i := range run.Steps {
		step := &run.Steps[i]
		if workflowStepTerminal(step.Status) {
			continue
		}
		outcome, err := o.reconcileStepFromLedger(ctx, step)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if outcome.task != nil || outcome.run != nil || step.Status == StepStatusRunning || step.Status == StepStatusReady {
			outcomes = append(outcomes, outcome)
		}
	}
	if len(outcomes) == 0 {
		return false, firstErr
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	stepIndex := make(map[string]*StepRun, len(run.Steps))
	for i := range run.Steps {
		stepIndex[run.Steps[i].StepID] = &run.Steps[i]
	}
	now := time.Now().Unix()
	changed := false
	for _, outcome := range outcomes {
		step := stepIndex[outcome.stepID]
		if step == nil || workflowStepTerminal(step.Status) {
			continue
		}
		if workflowApplyRunReconciliation(step, outcome.run) {
			changed = true
			continue
		}
		if workflowApplyTaskReconciliation(step, outcome.task) {
			changed = true
			continue
		}
		if step.Status == StepStatusRunning || step.Status == StepStatusReady {
			step.Status = StepStatusPending
			step.EndedAt = 0
			step.Error = ""
			changed = true
		}
	}
	if changed {
		run.UpdatedAt = now
		finalizeWorkflowRunIfDone(run, now)
	}
	return changed, firstErr
}

func (o *WorkflowOrchestrator) reconcileStepFromLedger(ctx context.Context, step *StepRun) (workflowStepReconciliation, error) {
	if step == nil {
		return workflowStepReconciliation{}, nil
	}
	outcome := workflowStepReconciliation{stepID: step.StepID}
	taskID := strings.TrimSpace(step.TaskID)
	runID := strings.TrimSpace(step.RunID)
	if taskID != "" {
		entry, err := o.ledger.GetTask(ctx, taskID)
		if err != nil {
			return outcome, err
		}
		if entry != nil {
			task := entry.Task.Normalize()
			outcome.task = &task
			if runID == "" {
				runID = strings.TrimSpace(task.CurrentRunID)
				if runID == "" {
					runID = strings.TrimSpace(task.LastRunID)
				}
			}
		}
	}
	if runID != "" {
		entry, err := o.ledger.GetRun(ctx, runID)
		if err != nil {
			return outcome, err
		}
		if entry != nil {
			run := entry.Run.Normalize()
			outcome.run = &run
		}
	}
	return outcome, nil
}

// GetRun retrieves a workflow run by ID.
func (o *WorkflowOrchestrator) GetRun(ctx context.Context, runID string) (*WorkflowRun, error) {
	o.mu.RLock()
	run, ok := o.runs[runID]
	o.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("workflow run %q not found", runID)
	}
	return run, nil
}

// ListRuns returns workflow runs, optionally filtered.
func (o *WorkflowOrchestrator) ListRuns(ctx context.Context, workflowID string, status WorkflowStatus) []*WorkflowRun {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var runs []*WorkflowRun
	for _, run := range o.runs {
		if workflowID != "" && run.WorkflowID != workflowID {
			continue
		}
		if status != "" && run.Status != status {
			continue
		}
		runs = append(runs, run)
	}
	return runs
}

// CancelRun cancels a running workflow.
func (o *WorkflowOrchestrator) CancelRun(ctx context.Context, runID, reason string) error {
	o.mu.Lock()
	run, ok := o.runs[runID]
	if !ok {
		o.mu.Unlock()
		return fmt.Errorf("workflow run %q not found", runID)
	}

	if run.Status != WorkflowStatusRunning && run.Status != WorkflowStatusPaused {
		o.mu.Unlock()
		return fmt.Errorf("workflow run %q is not active (status: %s)", runID, run.Status)
	}

	now := time.Now().Unix()
	run.Status = WorkflowStatusCancelled
	run.EndedAt = now
	run.UpdatedAt = now
	run.Error = reason
	o.mu.Unlock()

	// Persist
	_ = o.saveRun(ctx, run)

	return nil
}

// PauseRun pauses a running workflow.
func (o *WorkflowOrchestrator) PauseRun(ctx context.Context, runID string) error {
	o.mu.Lock()
	run, ok := o.runs[runID]
	if !ok {
		o.mu.Unlock()
		return fmt.Errorf("workflow run %q not found", runID)
	}

	if run.Status != WorkflowStatusRunning {
		o.mu.Unlock()
		return fmt.Errorf("workflow run %q is not running", runID)
	}

	run.Status = WorkflowStatusPaused
	run.UpdatedAt = time.Now().Unix()
	o.mu.Unlock()

	return o.saveRun(ctx, run)
}

// ResumeRun resumes a paused workflow.
func (o *WorkflowOrchestrator) ResumeRun(ctx context.Context, runID string) error {
	o.mu.Lock()
	run, ok := o.runs[runID]
	if !ok {
		o.mu.Unlock()
		return fmt.Errorf("workflow run %q not found", runID)
	}

	if run.Status != WorkflowStatusPaused {
		o.mu.Unlock()
		return fmt.Errorf("workflow run %q is not paused", runID)
	}

	run.Status = WorkflowStatusRunning
	run.UpdatedAt = time.Now().Unix()
	o.mu.Unlock()

	// Get definition and schedule ready steps
	def, err := o.GetDefinition(ctx, run.WorkflowID)
	if err != nil {
		return err
	}

	go o.scheduleReadySteps(context.Background(), run, def)
	return o.saveRun(ctx, run)
}

// scheduleReadySteps finds and executes steps that are ready to run.
func (o *WorkflowOrchestrator) scheduleReadySteps(ctx context.Context, run *WorkflowRun, def *WorkflowDefinition) {
	o.mu.Lock()
	if run.Status != WorkflowStatusRunning {
		o.mu.Unlock()
		return
	}

	// Build step index
	stepIndex := make(map[string]*StepRun)
	for i := range run.Steps {
		stepIndex[run.Steps[i].StepID] = &run.Steps[i]
	}

	defIndex := make(map[string]*StepDefinition)
	for i := range def.Steps {
		defIndex[def.Steps[i].ID] = &def.Steps[i]
	}

	// Find ready steps
	var readySteps []*StepRun
	for i := range run.Steps {
		step := &run.Steps[i]
		if step.Status != StepStatusPending && step.Status != StepStatusBlocked {
			continue
		}

		stepDef := defIndex[step.StepID]
		if stepDef == nil {
			continue
		}

		// Check dependencies. A dependency can be satisfied by a successful
		// completion, by a skipped upstream step, or by a failed upstream step whose
		// own failure policy explicitly allows the workflow to continue.
		ready := true
		for _, depID := range stepDef.Dependencies {
			dep := stepIndex[depID]
			depDef := defIndex[depID]
			if !dependencySatisfied(dep, depDef) {
				ready = false
				break
			}
		}

		if ready {
			step.Status = StepStatusReady
			readySteps = append(readySteps, step)
		} else if len(stepDef.Dependencies) > 0 {
			step.Status = StepStatusBlocked
		}
	}
	o.mu.Unlock()

	// Execute ready steps
	for _, step := range readySteps {
		stepDef := defIndex[step.StepID]
		if stepDef == nil {
			continue
		}

		o.executeStep(ctx, run, step, stepDef)
	}
}

func dependencySatisfied(step *StepRun, def *StepDefinition) bool {
	if step == nil {
		return false
	}
	switch step.Status {
	case StepStatusCompleted, StepStatusSkipped:
		return true
	case StepStatusFailed:
		return def != nil && def.OnFailure == "continue"
	default:
		return false
	}
}

func workflowRunTerminal(status WorkflowStatus) bool {
	switch status {
	case WorkflowStatusCompleted, WorkflowStatusFailed, WorkflowStatusCancelled:
		return true
	default:
		return false
	}
}

func workflowStepTerminal(status StepStatus) bool {
	switch status {
	case StepStatusCompleted, StepStatusFailed, StepStatusSkipped:
		return true
	default:
		return false
	}
}

func workflowApplyRunReconciliation(step *StepRun, run *state.TaskRun) bool {
	if step == nil || run == nil {
		return false
	}
	switch run.Status {
	case state.TaskRunStatusCompleted:
		step.Status = StepStatusCompleted
		step.Output = workflowStepOutputFromTaskRun(*run)
		step.Error = ""
		if step.StartedAt == 0 {
			step.StartedAt = run.StartedAt
		}
		if run.EndedAt > 0 {
			step.EndedAt = run.EndedAt
		}
		if step.Attempt == 0 {
			step.Attempt = run.Attempt
		}
		return true
	case state.TaskRunStatusFailed, state.TaskRunStatusCancelled:
		step.Status = StepStatusFailed
		step.Output = workflowStepOutputFromTaskRun(*run)
		step.Error = strings.TrimSpace(run.Error)
		if step.Error == "" {
			step.Error = fmt.Sprintf("child task run %s ended with status %s", run.RunID, run.Status)
		}
		if step.StartedAt == 0 {
			step.StartedAt = run.StartedAt
		}
		if run.EndedAt > 0 {
			step.EndedAt = run.EndedAt
		}
		if step.Attempt == 0 {
			step.Attempt = run.Attempt
		}
		return true
	default:
		return false
	}
}

func workflowApplyTaskReconciliation(step *StepRun, task *state.TaskSpec) bool {
	if step == nil || task == nil {
		return false
	}
	switch task.Status {
	case state.TaskStatusCompleted:
		step.Status = StepStatusCompleted
		step.Error = ""
		if step.Output == nil {
			step.Output = map[string]any{}
		}
		step.Output["task_id"] = task.TaskID
		step.Output["status"] = string(task.Status)
		if step.EndedAt == 0 {
			step.EndedAt = task.UpdatedAt
		}
		return true
	case state.TaskStatusFailed, state.TaskStatusCancelled:
		step.Status = StepStatusFailed
		if step.Output == nil {
			step.Output = map[string]any{}
		}
		step.Output["task_id"] = task.TaskID
		step.Output["status"] = string(task.Status)
		if step.Error == "" {
			step.Error = fmt.Sprintf("child task %s ended with status %s", task.TaskID, task.Status)
		}
		if step.EndedAt == 0 {
			step.EndedAt = task.UpdatedAt
		}
		return true
	default:
		return false
	}
}

func workflowStepOutputFromTaskRun(run state.TaskRun) map[string]any {
	out := map[string]any{
		"task_id": run.TaskID,
		"run_id":  run.RunID,
		"status":  string(run.Status),
	}
	if strings.TrimSpace(run.Error) != "" {
		out["error"] = run.Error
	}
	if run.Usage != (state.TaskUsage{}) {
		out["usage"] = run.Usage
	}
	if run.Result.Kind != "" || run.Result.ID != "" {
		out["result"] = run.Result
	}
	return out
}

func finalizeWorkflowRunIfDone(run *WorkflowRun, now int64) {
	if run == nil || run.Status != WorkflowStatusRunning {
		return
	}
	allDone := true
	allSuccess := true
	for _, step := range run.Steps {
		if !workflowStepTerminal(step.Status) {
			allDone = false
			break
		}
		if step.Status == StepStatusFailed {
			allSuccess = false
		}
	}
	if !allDone {
		return
	}
	if allSuccess {
		run.Status = WorkflowStatusCompleted
	} else {
		run.Status = WorkflowStatusFailed
	}
	run.EndedAt = now
}

var errStepBlocked = errors.New("step blocked")

func (o *WorkflowOrchestrator) executeStep(ctx context.Context, run *WorkflowRun, step *StepRun, def *StepDefinition) {
	o.mu.Lock()
	step.Status = StepStatusRunning
	step.Attempt++
	step.StartedAt = time.Now().Unix()
	run.CurrentStep = step.StepID
	run.UpdatedAt = time.Now().Unix()
	o.mu.Unlock()

	// Emit step event
	if o.emitter != nil {
		o.emitter.Emit(ctx, Event{
			Type:      EventWorkflowStep,
			Timestamp: time.Now().Unix(),
			Meta: map[string]any{
				"run_id":    run.RunID,
				"step_id":   step.StepID,
				"step_name": step.StepName,
				"status":    "running",
				"attempt":   step.Attempt,
			},
		})
	}

	var err error
	if handled, builtinErr := o.executeBuiltInStep(ctx, run, step, def); handled {
		err = builtinErr
	} else if o.executor != nil {
		err = o.executor.ExecuteStep(ctx, run, step, def)
	} else {
		err = fmt.Errorf("no step executor configured")
	}

	o.mu.Lock()
	now := time.Now().Unix()
	step.EndedAt = now
	run.UpdatedAt = now

	if err != nil {
		if errors.Is(err, errStepBlocked) {
			step.Status = StepStatusBlocked
		} else {
			step.Error = err.Error()

			// Check retry policy
			if def.RetryPolicy != nil && step.Attempt < def.RetryPolicy.MaxAttempts {
				step.Status = StepStatusPending // Will be retried
			} else if def.OnFailure == "continue" {
				step.Status = StepStatusFailed
				// Continue to next steps
			} else if def.OnFailure == "skip" {
				step.Status = StepStatusSkipped
			} else {
				step.Status = StepStatusFailed
				run.Status = WorkflowStatusFailed
				run.EndedAt = now
				run.Error = fmt.Sprintf("step %s failed: %s", step.StepID, err.Error())
			}
		}
	} else {
		step.Status = StepStatusCompleted
		step.Error = ""
	}

	accumulateWorkflowUsage(run, step)

	// Check if workflow is complete
	if run.Status == WorkflowStatusRunning {
		allDone := true
		allSuccess := true
		for _, s := range run.Steps {
			if s.Status == StepStatusPending || s.Status == StepStatusReady || s.Status == StepStatusRunning || s.Status == StepStatusBlocked {
				allDone = false
				break
			}
			if s.Status == StepStatusFailed {
				allSuccess = false
			}
		}

		if allDone {
			if allSuccess {
				run.Status = WorkflowStatusCompleted
			} else {
				run.Status = WorkflowStatusFailed
			}
			run.EndedAt = now
		}
	}
	o.mu.Unlock()

	// Persist
	_ = o.saveRun(ctx, run)

	// Emit completion event
	if o.emitter != nil {
		eventType := EventWorkflowStep
		if run.Status == WorkflowStatusCompleted {
			eventType = EventWorkflowDone
		} else if run.Status == WorkflowStatusFailed {
			eventType = EventWorkflowFailed
		}

		o.emitter.Emit(ctx, Event{
			Type:      eventType,
			Timestamp: now,
			Meta: map[string]any{
				"run_id":    run.RunID,
				"step_id":   step.StepID,
				"step_name": step.StepName,
				"status":    string(step.Status),
			},
		})
	}

	// Schedule next ready steps if workflow is still running
	if run.Status == WorkflowStatusRunning {
		def, err := o.GetDefinition(ctx, run.WorkflowID)
		if err == nil {
			go o.scheduleReadySteps(ctx, run, def)
		}
	}
}

func (o *WorkflowOrchestrator) executeBuiltInStep(ctx context.Context, run *WorkflowRun, step *StepRun, def *StepDefinition) (bool, error) {
	switch def.Type {
	case StepTypeWait:
		return true, executeWaitStep(ctx, def)
	case StepTypeApproval:
		return true, errStepBlocked
	case StepTypeConditional:
		return true, o.executeConditionalStep(ctx, run, step, def)
	case StepTypeParallel:
		return true, o.executeParallelStep(ctx, run, step, def)
	default:
		return false, nil
	}
}

func executeWaitStep(ctx context.Context, def *StepDefinition) error {
	delay, err := waitDuration(def)
	if err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func waitDuration(def *StepDefinition) (time.Duration, error) {
	var delay time.Duration
	if def.Config.Duration > 0 {
		delay = time.Duration(def.Config.Duration) * time.Millisecond
	}
	if strings.TrimSpace(def.Config.Until) == "" {
		return delay, nil
	}

	until, err := parseUntilTime(def.Config.Until)
	if err != nil {
		return 0, err
	}
	untilDelay := time.Until(until)
	if untilDelay < 0 {
		untilDelay = 0
	}
	if untilDelay > delay {
		delay = untilDelay
	}
	return delay, nil
}

func parseUntilTime(v string) (time.Time, error) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("wait.until is empty")
	}
	if unixSec, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return time.Unix(unixSec, 0), nil
	}
	t, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid wait.until %q", v)
	}
	return t, nil
}

func (o *WorkflowOrchestrator) executeConditionalStep(ctx context.Context, run *WorkflowRun, step *StepRun, def *StepDefinition) error {
	branch := def.Config.TrueStep
	if !evaluateCondition(run, def.Condition) {
		branch = def.Config.FalseStep
	}
	if branch == nil {
		return nil
	}

	branchStep := &StepRun{
		StepID:   step.StepID + ":branch",
		StepName: branch.Name,
		Type:     branch.Type,
		Status:   StepStatusRunning,
		Attempt:  1,
	}
	if handled, err := o.executeBuiltInStep(ctx, run, branchStep, branch); handled {
		if err != nil {
			return err
		}
		if branchStep.Status == StepStatusBlocked {
			return errStepBlocked
		}
		return nil
	}
	if o.executor == nil {
		return fmt.Errorf("no step executor configured")
	}
	return o.executor.ExecuteStep(ctx, run, branchStep, branch)
}

func (o *WorkflowOrchestrator) executeParallelStep(ctx context.Context, run *WorkflowRun, step *StepRun, def *StepDefinition) error {
	if len(def.Config.Substeps) == 0 {
		return nil
	}

	errCh := make(chan error, len(def.Config.Substeps))
	var wg sync.WaitGroup
	for i := range def.Config.Substeps {
		sub := def.Config.Substeps[i]
		wg.Add(1)
		go func(subDef StepDefinition) {
			defer wg.Done()
			subStep := &StepRun{
				StepID:   step.StepID + ":" + subDef.ID,
				StepName: subDef.Name,
				Type:     subDef.Type,
				Status:   StepStatusRunning,
				Attempt:  1,
			}
			if handled, err := o.executeBuiltInStep(ctx, run, subStep, &subDef); handled {
				errCh <- err
				return
			}
			if o.executor == nil {
				errCh <- fmt.Errorf("no step executor configured")
				return
			}
			errCh <- o.executor.ExecuteStep(ctx, run, subStep, &subDef)
		}(sub)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func evaluateCondition(run *WorkflowRun, condition string) bool {
	cond := strings.TrimSpace(strings.ToLower(condition))
	switch cond {
	case "", "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	if run == nil || run.Inputs == nil {
		return false
	}
	v, ok := run.Inputs[condition]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func accumulateWorkflowUsage(run *WorkflowRun, step *StepRun) {
	if run == nil || step == nil || len(step.Output) == 0 {
		return
	}
	usage, ok := stepUsageFromOutput(step.Output)
	if !ok {
		return
	}
	run.Usage.Add(usage)
}

func stepUsageFromOutput(output map[string]any) (state.TaskUsage, bool) {
	if usage, ok := decodeTaskUsage(output["usage"]); ok {
		return usage, true
	}
	// Fallback: some executors may place usage fields at the top level.
	return decodeTaskUsage(output)
}

func decodeTaskUsage(v any) (state.TaskUsage, bool) {
	switch typed := v.(type) {
	case nil:
		return state.TaskUsage{}, false
	case state.TaskUsage:
		if typed == (state.TaskUsage{}) {
			return state.TaskUsage{}, false
		}
		return typed, true
	case *state.TaskUsage:
		if typed == nil || *typed == (state.TaskUsage{}) {
			return state.TaskUsage{}, false
		}
		return *typed, true
	}

	data, err := json.Marshal(v)
	if err != nil {
		return state.TaskUsage{}, false
	}
	var usage state.TaskUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		return state.TaskUsage{}, false
	}
	if usage == (state.TaskUsage{}) {
		return state.TaskUsage{}, false
	}
	return usage, true
}

func generateID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}
