// approval.go provides plan preview, approval, rejection, and amendment
// control surfaces for operators and governance policies.
package planner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// PlanController exposes operator-facing plan lifecycle operations.
// It enforces autonomy mode requirements and records durable approval decisions.
type PlanController struct {
	mu        sync.RWMutex
	planner   *Planner
	approvals []state.PlanApproval // in-memory audit log; production wires to DocsRepository
}

// NewPlanController creates a controller wrapping the given planner.
func NewPlanController(planner *Planner) *PlanController {
	return &PlanController{planner: planner}
}

// PlanPreview is a read-only summary of a plan for operator review.
type PlanPreview struct {
	Plan            state.PlanSpec     `json:"plan"`
	ReadySteps      []state.PlanStep   `json:"ready_steps"`
	BlockedSteps    []state.PlanStep   `json:"blocked_steps"`
	CompletedSteps  []state.PlanStep   `json:"completed_steps"`
	FailedSteps     []state.PlanStep   `json:"failed_steps"`
	TotalSteps      int                `json:"total_steps"`
	ApprovalHistory []state.PlanApproval `json:"approval_history,omitempty"`
	NeedsApproval   bool               `json:"needs_approval"`
	AutonomyMode    state.AutonomyMode `json:"autonomy_mode"`
}

// Preview builds a read-only summary for operator review.
func (c *PlanController) Preview(plan state.PlanSpec, mode state.AutonomyMode) PlanPreview {
	// Normalize mode first so NeedsApproval uses the canonical value.
	if !mode.Valid() {
		mode = state.AutonomyFull
	}

	seen := make(map[string]bool, len(plan.Steps))
	var ready, blocked, completed, failed []state.PlanStep
	for _, step := range plan.Steps {
		switch step.Status {
		case state.PlanStepStatusPending:
			// Will be picked up by ReadySteps below if deps are met.
		case state.PlanStepStatusReady:
			ready = append(ready, step)
			seen[step.StepID] = true
		case state.PlanStepStatusBlocked:
			blocked = append(blocked, step)
		case state.PlanStepStatusCompleted:
			completed = append(completed, step)
		case state.PlanStepStatusFailed:
			failed = append(failed, step)
		}
	}

	// Include pending steps whose dependencies are met (deduplicated).
	for _, step := range plan.ReadySteps() {
		if !seen[step.StepID] {
			ready = append(ready, step)
			seen[step.StepID] = true
		}
	}

	// Collect approval history for this plan.
	c.mu.RLock()
	var history []state.PlanApproval
	for _, a := range c.approvals {
		if a.PlanID == plan.PlanID {
			history = append(history, a)
		}
	}
	c.mu.RUnlock()

	needsApproval := mode.RequiresPlanApproval() && plan.Status == state.PlanStatusDraft

	return PlanPreview{
		Plan:            plan,
		ReadySteps:      ready,
		BlockedSteps:    blocked,
		CompletedSteps:  completed,
		FailedSteps:     failed,
		TotalSteps:      len(plan.Steps),
		ApprovalHistory: history,
		NeedsApproval:   needsApproval,
		AutonomyMode:    mode,
	}
}

// ApproveRequest holds the parameters for approving a plan.
type ApproveRequest struct {
	Plan   state.PlanSpec
	Actor  string
	Reason string
	Mode   state.AutonomyMode
	Now    int64
}

// Approve marks a plan as approved and transitions it to active status.
// Returns the updated plan and the recorded approval decision.
func (c *PlanController) Approve(req ApproveRequest) (state.PlanSpec, state.PlanApproval, error) {
	if strings.TrimSpace(req.Actor) == "" {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("approve: actor is required")
	}

	plan := req.Plan.Normalize()
	if plan.Status != state.PlanStatusDraft && plan.Status != state.PlanStatusRevising {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("approve: plan status is %q, expected draft or revising", plan.Status)
	}

	now := req.Now
	if now <= 0 {
		now = time.Now().Unix()
	}

	approval := state.PlanApproval{
		PlanID:    plan.PlanID,
		Revision:  plan.Revision,
		Decision:  state.PlanApprovalApproved,
		Actor:     req.Actor,
		Reason:    req.Reason,
		CreatedAt: now,
	}
	c.mu.Lock()
	c.approvals = append(c.approvals, approval)
	c.mu.Unlock()

	plan.Status = state.PlanStatusActive
	plan.UpdatedAt = now
	if plan.Meta == nil {
		plan.Meta = map[string]any{}
	}
	plan.Meta["approved_by"] = req.Actor
	plan.Meta["approved_at"] = now

	return plan, approval, nil
}

// RejectRequest holds the parameters for rejecting a plan.
type RejectRequest struct {
	Plan   state.PlanSpec
	Actor  string
	Reason string
	Now    int64
}

// Reject marks a plan as rejected (cancelled) and records the decision.
func (c *PlanController) Reject(req RejectRequest) (state.PlanSpec, state.PlanApproval, error) {
	if strings.TrimSpace(req.Actor) == "" {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("reject: actor is required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("reject: reason is required")
	}

	plan := req.Plan.Normalize()
	if plan.IsTerminal() {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("reject: plan is already terminal (%s)", plan.Status)
	}

	now := req.Now
	if now <= 0 {
		now = time.Now().Unix()
	}

	approval := state.PlanApproval{
		PlanID:    plan.PlanID,
		Revision:  plan.Revision,
		Decision:  state.PlanApprovalRejected,
		Actor:     req.Actor,
		Reason:    req.Reason,
		CreatedAt: now,
	}
	c.mu.Lock()
	c.approvals = append(c.approvals, approval)
	c.mu.Unlock()

	plan.Status = state.PlanStatusCancelled
	plan.UpdatedAt = now
	if plan.Meta == nil {
		plan.Meta = map[string]any{}
	}
	plan.Meta["rejected_by"] = req.Actor
	plan.Meta["rejected_reason"] = req.Reason

	return plan, approval, nil
}

// AmendRequest holds parameters for requesting amendments to a plan.
type AmendRequest struct {
	Plan        state.PlanSpec
	Goal        state.GoalSpec
	Actor       string
	Feedback    string // operator feedback to guide revision
	SessionID   string
	Now         int64
}

// Amend triggers a plan revision based on operator feedback. The plan
// transitions through "revising" status and a new revision is generated.
func (c *PlanController) Amend(ctx context.Context, req AmendRequest) (state.PlanSpec, state.PlanApproval, error) {
	if strings.TrimSpace(req.Actor) == "" {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("amend: actor is required")
	}
	if strings.TrimSpace(req.Feedback) == "" {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("amend: feedback is required")
	}
	if c.planner == nil {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("amend: planner is nil")
	}

	plan := req.Plan.Normalize()
	if plan.IsTerminal() {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("amend: plan is already terminal (%s)", plan.Status)
	}

	now := req.Now
	if now <= 0 {
		now = time.Now().Unix()
	}

	// Record the amendment decision.
	approval := state.PlanApproval{
		PlanID:    plan.PlanID,
		Revision:  plan.Revision,
		Decision:  state.PlanApprovalAmended,
		Actor:     req.Actor,
		Reason:    req.Feedback,
		CreatedAt: now,
	}
	c.mu.Lock()
	c.approvals = append(c.approvals, approval)
	c.mu.Unlock()

	// Use the replanning path with operator feedback as context.
	newPlan, _, err := c.planner.Replan(ctx, ReplanRequest{
		CurrentPlan: plan,
		Goal:        req.Goal,
		Trigger:     ReplanTriggerManual,
		Reason:      fmt.Sprintf("Operator amendment: %s", req.Feedback),
		Context:     req.Feedback,
		SessionID:   req.SessionID,
		Actor:       req.Actor,
		Now:         now,
	})
	if err != nil {
		return state.PlanSpec{}, state.PlanApproval{}, fmt.Errorf("amend: %w", err)
	}

	// Amended plans return to draft for re-approval.
	newPlan.Status = state.PlanStatusDraft

	return newPlan, approval, nil
}

// MayCompile reports whether a plan is allowed to compile tasks given
// its current state and the autonomy mode.
func (c *PlanController) MayCompile(plan state.PlanSpec, mode state.AutonomyMode) bool {
	if plan.Status != state.PlanStatusActive {
		return false
	}
	if mode.RequiresPlanApproval() {
		c.mu.RLock()
		defer c.mu.RUnlock()
		// Check that the current revision has been approved.
		for i := len(c.approvals) - 1; i >= 0; i-- {
			a := c.approvals[i]
			if a.PlanID == plan.PlanID && a.Revision == plan.Revision && a.Decision == state.PlanApprovalApproved {
				return true
			}
		}
		return false
	}
	return true
}

// MayExecuteStep reports whether a specific plan step may proceed to
// task execution under the current autonomy mode.
func (c *PlanController) MayExecuteStep(plan state.PlanSpec, stepID string, mode state.AutonomyMode) bool {
	if !c.MayCompile(plan, mode) {
		return false
	}
	if !mode.RequiresStepApproval() {
		return true
	}
	// Under step_approval or supervised, check for a step-level approval.
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := len(c.approvals) - 1; i >= 0; i-- {
		a := c.approvals[i]
		if a.PlanID == plan.PlanID && a.Revision == plan.Revision {
			if meta, ok := a.Meta["step_id"].(string); ok && meta == stepID && a.Decision == state.PlanApprovalApproved {
				return true
			}
		}
	}
	return false
}

// ApproveStep records approval for a specific step under step_approval mode.
func (c *PlanController) ApproveStep(plan state.PlanSpec, stepID, actor, reason string, now int64) (state.PlanApproval, error) {
	if strings.TrimSpace(actor) == "" {
		return state.PlanApproval{}, fmt.Errorf("approve_step: actor is required")
	}
	if strings.TrimSpace(stepID) == "" {
		return state.PlanApproval{}, fmt.Errorf("approve_step: step_id is required")
	}
	// Verify the step exists.
	found := false
	for _, step := range plan.Steps {
		if step.StepID == stepID {
			found = true
			break
		}
	}
	if !found {
		return state.PlanApproval{}, fmt.Errorf("approve_step: step %q not found in plan", stepID)
	}

	if now <= 0 {
		now = time.Now().Unix()
	}

	approval := state.PlanApproval{
		PlanID:    plan.PlanID,
		Revision:  plan.Revision,
		Decision:  state.PlanApprovalApproved,
		Actor:     actor,
		Reason:    reason,
		CreatedAt: now,
		Meta:      map[string]any{"step_id": stepID},
	}
	c.mu.Lock()
	c.approvals = append(c.approvals, approval)
	c.mu.Unlock()
	return approval, nil
}

// Approvals returns all recorded approval decisions.
func (c *PlanController) Approvals() []state.PlanApproval {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]state.PlanApproval, len(c.approvals))
	copy(out, c.approvals)
	return out
}
