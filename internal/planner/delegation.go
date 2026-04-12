package planner

import (
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ── Delegation contract ──────────────────────────────────────────────────────

// DelegationRequest describes what a parent task wants to delegate.
type DelegationRequest struct {
	// ParentTask is the parent task that is delegating.
	ParentTask state.TaskSpec `json:"parent_task"`
	// ParentRun is the parent's current run.
	ParentRun state.TaskRun `json:"parent_run"`
	// ParentUsage is the parent's current usage for budget narrowing.
	ParentUsage state.TaskUsage `json:"parent_usage,omitempty"`
	// ChildInstructions are the instructions for the delegated task.
	ChildInstructions string `json:"child_instructions"`
	// ChildTitle is an optional explicit title (derived from instructions if empty).
	ChildTitle string `json:"child_title,omitempty"`
	// ChildAuthority overrides the child's authority. If zero, inherited from parent.
	ChildAuthority *state.TaskAuthority `json:"child_authority,omitempty"`
	// ChildBudget overrides the child's budget. If zero, inherited from parent.
	ChildBudget *state.TaskBudget `json:"child_budget,omitempty"`
	// ChildExpectedOutputs specifies expected deliverables.
	ChildExpectedOutputs []state.TaskOutputSpec `json:"child_expected_outputs,omitempty"`
	// ChildAcceptanceCriteria specifies how completion is judged.
	ChildAcceptanceCriteria []state.TaskAcceptanceCriterion `json:"child_acceptance_criteria,omitempty"`
	// ChildVerification specifies verification policy for the delegated task.
	ChildVerification *state.VerificationSpec `json:"child_verification,omitempty"`
	// AssignedAgent optionally specifies the target agent.
	AssignedAgent string `json:"assigned_agent,omitempty"`
	// DelegationDepth is the current delegation depth (0 = top-level).
	DelegationDepth int `json:"delegation_depth"`
	// Now overrides the timestamp for testing.
	Now int64 `json:"now,omitempty"`
}

// DelegationResult is the output of building a delegated task.
type DelegationResult struct {
	// Task is the fully constructed child task.
	Task state.TaskSpec `json:"task"`
	// EffectiveAuthority is the narrowed authority for the child.
	EffectiveAuthority state.TaskAuthority `json:"effective_authority"`
	// EffectiveBudget is the narrowed budget for the child.
	EffectiveBudget state.TaskBudget `json:"effective_budget"`
	// AuthorityTrace records how authority was narrowed.
	AuthorityTrace AuthorityTrace `json:"authority_trace,omitempty"`
	// Warnings contains non-fatal issues detected during construction.
	Warnings []string `json:"warnings,omitempty"`
}

// ── Build delegated task ─────────────────────────────────────────────────────

// BuildDelegatedTask constructs a fully resolved child task from a delegation
// request. It enforces:
//   - Authority is narrowed from parent (child cannot widen).
//   - Budget is narrowed to parent's remaining capacity.
//   - Delegation depth is checked against the parent's limit.
//   - TaskID, GoalID, ParentTaskID, and lineage fields are linked.
func BuildDelegatedTask(req DelegationRequest) (DelegationResult, error) {
	parent := req.ParentTask.Normalize()
	now := req.Now
	if now == 0 {
		now = time.Now().Unix()
	}

	// Validate.
	if strings.TrimSpace(req.ChildInstructions) == "" {
		return DelegationResult{}, fmt.Errorf("child_instructions is required")
	}
	if strings.TrimSpace(parent.TaskID) == "" {
		return DelegationResult{}, fmt.Errorf("parent task_id is required")
	}

	// Check delegation depth.
	maxDepth := parent.Authority.MaxDelegationDepth
	if maxDepth > 0 && req.DelegationDepth >= maxDepth {
		return DelegationResult{}, fmt.Errorf("delegation depth %d exceeds max %d",
			req.DelegationDepth, maxDepth)
	}

	// Check delegation permission.
	if !parent.Authority.CanDelegate {
		return DelegationResult{}, fmt.Errorf("parent task does not have delegation permission")
	}

	var warnings []string

	// Resolve authority: narrow parent → child.
	childAuth := parent.Authority
	if req.ChildAuthority != nil {
		childAuth = NarrowAuthority(parent.Authority, *req.ChildAuthority)
	}

	// Build authority trace.
	trace := ResolveAuthority(
		AuthorityLayer{Source: AuthSourceConfig, Label: "parent_authority", Authority: parent.Authority},
		AuthorityLayer{Source: AuthSourceDelegation, Label: "child_requested", Authority: childAuth},
	)

	// Resolve budget: narrow to remaining.
	parentBudget := parent.Budget
	remaining := parentBudget.Remaining(req.ParentUsage)
	childBudget := remaining
	if req.ChildBudget != nil {
		childBudget = remaining.Narrow(*req.ChildBudget)
	}

	// Warn if budget is already tight.
	if !remaining.IsZero() {
		if remaining.MaxTotalTokens > 0 && remaining.MaxTotalTokens < 100 {
			warnings = append(warnings, "parent budget nearly exhausted: <100 tokens remaining")
		}
		if remaining.MaxToolCalls > 0 && remaining.MaxToolCalls < 2 {
			warnings = append(warnings, "parent budget nearly exhausted: <2 tool calls remaining")
		}
	}

	// Build title.
	title := strings.TrimSpace(req.ChildTitle)
	if title == "" {
		title = deriveDelegationTitle(req.ChildInstructions)
	}

	// Build task ID.
	taskID := fmt.Sprintf("%s-del-%d", parent.TaskID, now)

	task := state.TaskSpec{
		Version:            1,
		TaskID:             taskID,
		GoalID:             parent.GoalID,
		ParentTaskID:       parent.TaskID,
		PlanID:             parent.PlanID,
		SessionID:          parent.SessionID,
		Title:              title,
		Instructions:       strings.TrimSpace(req.ChildInstructions),
		ExpectedOutputs:    req.ChildExpectedOutputs,
		AcceptanceCriteria: req.ChildAcceptanceCriteria,
		AssignedAgent:      strings.TrimSpace(req.AssignedAgent),
		Status:             state.TaskStatusPending,
		Priority:           parent.Priority,
		Authority:          childAuth,
		Budget:             childBudget,
		MemoryScope:        parent.MemoryScope,
		ToolProfile:        parent.ToolProfile,
		EnabledTools:       parent.EnabledTools,
		CreatedAt:          now,
		UpdatedAt:          now,
		Meta: map[string]any{
			"delegation_depth": req.DelegationDepth + 1,
			"parent_run_id":    req.ParentRun.RunID,
		},
	}

	if req.ChildVerification != nil {
		task.Verification = *req.ChildVerification
	}

	return DelegationResult{
		Task:               task,
		EffectiveAuthority: childAuth,
		EffectiveBudget:    childBudget,
		AuthorityTrace:     trace,
		Warnings:           warnings,
	}, nil
}

// ── Delegation validation ────────────────────────────────────────────────────

// ValidateDelegation checks whether a delegation request would succeed without
// actually building the task. Useful for pre-flight checks.
func ValidateDelegation(req DelegationRequest) error {
	parent := req.ParentTask.Normalize()

	if strings.TrimSpace(req.ChildInstructions) == "" {
		return fmt.Errorf("child_instructions is required")
	}
	if strings.TrimSpace(parent.TaskID) == "" {
		return fmt.Errorf("parent task_id is required")
	}
	if !parent.Authority.CanDelegate {
		return fmt.Errorf("parent does not have delegation permission")
	}
	maxDepth := parent.Authority.MaxDelegationDepth
	if maxDepth > 0 && req.DelegationDepth >= maxDepth {
		return fmt.Errorf("delegation depth %d exceeds max %d",
			req.DelegationDepth, maxDepth)
	}
	return nil
}

// ── Formatting ───────────────────────────────────────────────────────────────

// FormatDelegationResult returns a human-readable summary.
func FormatDelegationResult(r DelegationResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Delegated Task: %s\n", r.Task.TaskID)
	fmt.Fprintf(&b, "  Title: %s\n", r.Task.Title)
	fmt.Fprintf(&b, "  Parent: %s\n", r.Task.ParentTaskID)
	fmt.Fprintf(&b, "  Authority: mode=%s can_act=%v can_delegate=%v depth=%d\n",
		r.EffectiveAuthority.AutonomyMode, r.EffectiveAuthority.CanAct,
		r.EffectiveAuthority.CanDelegate, r.EffectiveAuthority.MaxDelegationDepth)
	if !r.EffectiveBudget.IsZero() {
		fmt.Fprintf(&b, "  Budget: tokens=%d tools=%d delegations=%d\n",
			r.EffectiveBudget.MaxTotalTokens, r.EffectiveBudget.MaxToolCalls,
			r.EffectiveBudget.MaxDelegations)
	}
	if len(r.Task.ExpectedOutputs) > 0 {
		fmt.Fprintf(&b, "  Expected outputs: %d\n", len(r.Task.ExpectedOutputs))
	}
	if len(r.Task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "  Acceptance criteria: %d\n", len(r.Task.AcceptanceCriteria))
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  ⚠️  %s\n", w)
	}
	return b.String()
}

// ── Internal helpers ─────────────────────────────────────────────────────────

func deriveDelegationTitle(instructions string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return "delegated task"
	}
	if idx := strings.IndexByte(instructions, '\n'); idx >= 0 {
		instructions = instructions[:idx]
	}
	if len(instructions) > 80 {
		instructions = instructions[:77] + "..."
	}
	return instructions
}
