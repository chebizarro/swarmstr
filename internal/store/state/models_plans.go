package state

import (
	"fmt"
	"strings"
)

type PlanStatus string

const (
	PlanStatusDraft     PlanStatus = "draft"
	PlanStatusActive    PlanStatus = "active"
	PlanStatusRevising  PlanStatus = "revising"
	PlanStatusCompleted PlanStatus = "completed"
	PlanStatusFailed    PlanStatus = "failed"
	PlanStatusCancelled PlanStatus = "cancelled"
)

func ParsePlanStatus(raw string) (PlanStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PlanStatusDraft):
		return PlanStatusDraft, true
	case string(PlanStatusActive):
		return PlanStatusActive, true
	case string(PlanStatusRevising):
		return PlanStatusRevising, true
	case string(PlanStatusCompleted):
		return PlanStatusCompleted, true
	case string(PlanStatusFailed):
		return PlanStatusFailed, true
	case string(PlanStatusCancelled):
		return PlanStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizePlanStatus(raw string) PlanStatus {
	status, _ := ParsePlanStatus(raw)
	return status
}

func (s PlanStatus) Valid() bool {
	_, ok := ParsePlanStatus(string(s))
	return ok
}

// PlanStepStatus describes the state of an individual plan step.
type PlanStepStatus string

const (
	PlanStepStatusPending    PlanStepStatus = "pending"
	PlanStepStatusReady      PlanStepStatus = "ready"
	PlanStepStatusBlocked    PlanStepStatus = "blocked"
	PlanStepStatusInProgress PlanStepStatus = "in_progress"
	PlanStepStatusCompleted  PlanStepStatus = "completed"
	PlanStepStatusFailed     PlanStepStatus = "failed"
	PlanStepStatusSkipped    PlanStepStatus = "skipped"
)

func ParsePlanStepStatus(raw string) (PlanStepStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PlanStepStatusPending):
		return PlanStepStatusPending, true
	case string(PlanStepStatusReady):
		return PlanStepStatusReady, true
	case string(PlanStepStatusBlocked):
		return PlanStepStatusBlocked, true
	case string(PlanStepStatusInProgress):
		return PlanStepStatusInProgress, true
	case string(PlanStepStatusCompleted):
		return PlanStepStatusCompleted, true
	case string(PlanStepStatusFailed):
		return PlanStepStatusFailed, true
	case string(PlanStepStatusSkipped):
		return PlanStepStatusSkipped, true
	default:
		return "", false
	}
}

func NormalizePlanStepStatus(raw string) PlanStepStatus {
	status, _ := ParsePlanStepStatus(raw)
	return status
}

func (s PlanStepStatus) Valid() bool {
	_, ok := ParsePlanStepStatus(string(s))
	return ok
}

// PlanApprovalDecision describes the outcome of a plan approval review.
type PlanApprovalDecision string

const (
	PlanApprovalPending  PlanApprovalDecision = "pending"
	PlanApprovalApproved PlanApprovalDecision = "approved"
	PlanApprovalRejected PlanApprovalDecision = "rejected"
	PlanApprovalAmended  PlanApprovalDecision = "amended"
)

func ParsePlanApprovalDecision(raw string) (PlanApprovalDecision, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PlanApprovalPending), "":
		return PlanApprovalPending, true
	case string(PlanApprovalApproved):
		return PlanApprovalApproved, true
	case string(PlanApprovalRejected):
		return PlanApprovalRejected, true
	case string(PlanApprovalAmended):
		return PlanApprovalAmended, true
	default:
		return "", false
	}
}

func (d PlanApprovalDecision) Valid() bool {
	_, ok := ParsePlanApprovalDecision(string(d))
	return ok
}

// PlanApproval records a durable approval or rejection decision for a plan.
type PlanApproval struct {
	PlanID    string               `json:"plan_id"`
	Revision  int                  `json:"revision"`
	Decision  PlanApprovalDecision `json:"decision"`
	Actor     string               `json:"actor"`
	Reason    string               `json:"reason,omitempty"`
	CreatedAt int64                `json:"created_at"`
	Meta      map[string]any       `json:"meta,omitempty"`
}

// AutonomyMode controls how much latitude an agent has before requiring
// operator intervention. Plan approval requirements are keyed off this.
type PlanStep struct {
	StepID       string           `json:"step_id"`
	Title        string           `json:"title"`
	Instructions string           `json:"instructions,omitempty"`
	DependsOn    []string         `json:"depends_on,omitempty"`
	Status       PlanStepStatus   `json:"status"`
	TaskID       string           `json:"task_id,omitempty"`
	Agent        string           `json:"agent,omitempty"`
	Outputs      []TaskOutputSpec `json:"outputs,omitempty"`
	Meta         map[string]any   `json:"meta,omitempty"`
}

func (s PlanStep) Normalize() PlanStep {
	if !s.Status.Valid() {
		s.Status = PlanStepStatusPending
	}
	return s
}

func (s PlanStep) Validate() error {
	if strings.TrimSpace(s.StepID) == "" {
		return fmt.Errorf("step_id is required")
	}
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("step title is required")
	}
	if raw := strings.TrimSpace(string(s.Status)); raw != "" && !s.Status.Valid() {
		return fmt.Errorf("invalid step status %q", s.Status)
	}
	for i, out := range s.Outputs {
		if strings.TrimSpace(out.Name) == "" {
			return fmt.Errorf("step %q outputs[%d].name is required", s.StepID, i)
		}
	}
	return nil
}

// PlanSpec is the canonical persisted representation of a task decomposition plan.
type PlanSpec struct {
	Version          int            `json:"version"`
	PlanID           string         `json:"plan_id"`
	GoalID           string         `json:"goal_id,omitempty"`
	Title            string         `json:"title"`
	Revision         int            `json:"revision"`
	Status           PlanStatus     `json:"status"`
	Steps            []PlanStep     `json:"steps"`
	Assumptions      []string       `json:"assumptions,omitempty"`
	Risks            []string       `json:"risks,omitempty"`
	RollbackStrategy string         `json:"rollback_strategy,omitempty"`
	CreatedAt        int64          `json:"created_at,omitempty"`
	UpdatedAt        int64          `json:"updated_at,omitempty"`
	Meta             map[string]any `json:"meta,omitempty"`
}

func (p PlanSpec) Normalize() PlanSpec {
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Revision <= 0 {
		p.Revision = 1
	}
	if !p.Status.Valid() {
		p.Status = PlanStatusDraft
	}
	for i := range p.Steps {
		p.Steps[i] = p.Steps[i].Normalize()
	}
	return p
}

func (p PlanSpec) Validate() error {
	if strings.TrimSpace(p.PlanID) == "" {
		return fmt.Errorf("plan_id is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("plan title is required")
	}
	if raw := strings.TrimSpace(string(p.Status)); raw != "" && !p.Status.Valid() {
		return fmt.Errorf("invalid plan status %q", p.Status)
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("plan must have at least one step")
	}
	stepIDs := make(map[string]bool, len(p.Steps))
	for i, step := range p.Steps {
		if err := step.Validate(); err != nil {
			return fmt.Errorf("steps[%d]: %w", i, err)
		}
		if stepIDs[step.StepID] {
			return fmt.Errorf("duplicate step_id %q at steps[%d]", step.StepID, i)
		}
		stepIDs[step.StepID] = true
	}
	// Validate dependency references.
	for i, step := range p.Steps {
		for _, dep := range step.DependsOn {
			if !stepIDs[dep] {
				return fmt.Errorf("steps[%d] depends_on unknown step_id %q", i, dep)
			}
			if dep == step.StepID {
				return fmt.Errorf("steps[%d] depends on itself", i)
			}
		}
	}
	return nil
}

// HasCycle reports whether the step dependency graph contains a cycle.
func (p PlanSpec) HasCycle() bool {
	adj := make(map[string][]string, len(p.Steps))
	for _, step := range p.Steps {
		adj[step.StepID] = step.DependsOn
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(p.Steps))
	var dfs func(string) bool
	dfs = func(id string) bool {
		color[id] = gray
		for _, dep := range adj[id] {
			switch color[dep] {
			case gray:
				return true
			case white:
				if dfs(dep) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for _, step := range p.Steps {
		if color[step.StepID] == white {
			if dfs(step.StepID) {
				return true
			}
		}
	}
	return false
}

// ReadySteps returns steps whose status is pending and whose dependencies
// are all completed or skipped.
func (p PlanSpec) ReadySteps() []PlanStep {
	statusByID := make(map[string]PlanStepStatus, len(p.Steps))
	for _, step := range p.Steps {
		statusByID[step.StepID] = step.Status
	}
	var ready []PlanStep
	for _, step := range p.Steps {
		if step.Status != PlanStepStatusPending {
			continue
		}
		allDone := true
		for _, dep := range step.DependsOn {
			ds := statusByID[dep]
			if ds != PlanStepStatusCompleted && ds != PlanStepStatusSkipped {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, step)
		}
	}
	return ready
}

// IsTerminal reports whether the plan is in a terminal state.
func (p PlanSpec) IsTerminal() bool {
	switch p.Status {
	case PlanStatusCompleted, PlanStatusFailed, PlanStatusCancelled:
		return true
	}
	return false
}

// ── Workflow journal schemas ──────────────────────────────────────────────────

// WorkflowJournalDoc is the persisted representation of a task run's execution
// journal. It is stored as a replaceable state doc keyed by run ID.
// On each append the full doc is re-persisted; the entry list is bounded by
// the runtime (see WorkflowJournal.maxEntries).
