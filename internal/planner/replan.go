// replan.go implements failure-driven replanning, revision history,
// and replan trigger detection for the planning engine.
package planner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ReplanTrigger describes why a plan needs replanning.
type ReplanTrigger string

const (
	ReplanTriggerStepFailed  ReplanTrigger = "step_failed"
	ReplanTriggerStepBlocked ReplanTrigger = "step_blocked"
	ReplanTriggerPlanFailed  ReplanTrigger = "plan_failed"
	ReplanTriggerManual      ReplanTrigger = "manual"
)

// ReplanPolicy controls whether replanning is automatic or requires approval.
type ReplanPolicy string

const (
	ReplanPolicyAuto     ReplanPolicy = "auto"     // replan automatically on trigger
	ReplanPolicyApproval ReplanPolicy = "approval"  // queue for operator approval
	ReplanPolicyDisabled ReplanPolicy = "disabled"  // never replan
)

// ReplanRequest holds inputs for generating a plan revision.
type ReplanRequest struct {
	CurrentPlan state.PlanSpec
	Goal        state.GoalSpec
	Trigger     ReplanTrigger
	Reason      string            // human/system explanation
	Evidence    map[string]string // step_id → failure/block reason
	Context     string            // additional context for the planner
	SessionID   string
	Actor       string
	Now         int64
}

// PlanRevision captures what changed between two plan versions.
type PlanRevision struct {
	PlanID      string            `json:"plan_id"`
	FromVersion int               `json:"from_revision"`
	ToVersion   int               `json:"to_revision"`
	Trigger     ReplanTrigger     `json:"trigger"`
	Reason      string            `json:"reason,omitempty"`
	Actor       string            `json:"actor,omitempty"`
	CreatedAt   int64             `json:"created_at"`
	StepsAdded  []string          `json:"steps_added,omitempty"`
	StepsRemoved []string         `json:"steps_removed,omitempty"`
	StepsChanged []string         `json:"steps_changed,omitempty"`
	Evidence    map[string]string `json:"evidence,omitempty"`
	Meta        map[string]any    `json:"meta,omitempty"`
}

// NeedsReplan examines a plan and returns a trigger if replanning is warranted.
// Returns ("", false) when the plan is healthy or already terminal.
func NeedsReplan(plan state.PlanSpec) (ReplanTrigger, bool) {
	if plan.IsTerminal() && plan.Status != state.PlanStatusFailed {
		return "", false
	}
	if plan.Status == state.PlanStatusFailed {
		return ReplanTriggerPlanFailed, true
	}

	hasBlocked := false
	hasFailed := false
	for _, step := range plan.Steps {
		switch step.Status {
		case state.PlanStepStatusFailed:
			hasFailed = true
		case state.PlanStepStatusBlocked:
			hasBlocked = true
		}
	}

	if hasFailed {
		return ReplanTriggerStepFailed, true
	}
	if hasBlocked {
		// Only trigger if blocked steps are preventing further progress.
		ready := plan.ReadySteps()
		if len(ready) == 0 {
			return ReplanTriggerStepBlocked, true
		}
	}
	return "", false
}

// Replan generates a revised plan based on failure context.
// It preserves the prior plan's identity, increments the revision, and
// carries forward completed steps while regenerating failed/blocked ones.
func (p *Planner) Replan(ctx context.Context, req ReplanRequest) (state.PlanSpec, PlanRevision, error) {
	if p.provider == nil {
		return state.PlanSpec{}, PlanRevision{}, fmt.Errorf("replan: provider is nil")
	}

	current := req.CurrentPlan.Normalize()
	goal := req.Goal.Normalize()

	now := req.Now
	if now <= 0 {
		now = time.Now().Unix()
	}

	// Build failure context for the planner.
	failureContext := buildReplanContext(current, req)

	// Generate a new plan using the planner with failure context.
	newPlan, err := p.GeneratePlan(ctx, PlanRequest{
		Goal:      goal,
		Context:   failureContext,
		MaxSteps:  len(current.Steps) + 3, // allow some expansion
		SessionID: req.SessionID,
	})
	if err != nil {
		return state.PlanSpec{}, PlanRevision{}, fmt.Errorf("replan: %w", err)
	}

	// Preserve plan identity and bump revision.
	newPlan.PlanID = current.PlanID
	newPlan.GoalID = current.GoalID
	newPlan.Revision = current.Revision + 1
	newPlan.Status = state.PlanStatusActive
	newPlan.CreatedAt = current.CreatedAt
	newPlan.UpdatedAt = now

	// Carry forward completed steps from the current plan.
	newPlan = carryForwardCompleted(current, newPlan)

	newPlan = newPlan.Normalize()
	if err := newPlan.Validate(); err != nil {
		return state.PlanSpec{}, PlanRevision{}, fmt.Errorf("replan: revised plan invalid: %w", err)
	}
	if newPlan.HasCycle() {
		return state.PlanSpec{}, PlanRevision{}, fmt.Errorf("replan: revised plan has cyclic dependencies")
	}

	// Compute revision diff.
	revision := DiffPlans(current, newPlan, req.Trigger, req.Reason, req.Actor, now)
	revision.Evidence = req.Evidence

	return newPlan, revision, nil
}

// DiffPlans computes a PlanRevision showing what changed between two plan versions.
func DiffPlans(old, new state.PlanSpec, trigger ReplanTrigger, reason, actor string, at int64) PlanRevision {
	oldSteps := make(map[string]state.PlanStep, len(old.Steps))
	for _, s := range old.Steps {
		oldSteps[s.StepID] = s
	}
	newSteps := make(map[string]state.PlanStep, len(new.Steps))
	for _, s := range new.Steps {
		newSteps[s.StepID] = s
	}

	var added, removed, changed []string
	for id := range newSteps {
		if _, ok := oldSteps[id]; !ok {
			added = append(added, id)
		}
	}
	for id := range oldSteps {
		if _, ok := newSteps[id]; !ok {
			removed = append(removed, id)
		}
	}
	for id, newStep := range newSteps {
		if oldStep, ok := oldSteps[id]; ok {
			if stepDiffers(oldStep, newStep) {
				changed = append(changed, id)
			}
		}
	}

	return PlanRevision{
		PlanID:       new.PlanID,
		FromVersion:  old.Revision,
		ToVersion:    new.Revision,
		Trigger:      trigger,
		Reason:       reason,
		Actor:        actor,
		CreatedAt:    at,
		StepsAdded:   added,
		StepsRemoved: removed,
		StepsChanged: changed,
	}
}

// stepDiffers returns true if two steps with the same ID have meaningful differences.
func stepDiffers(a, b state.PlanStep) bool {
	if a.Title != b.Title || a.Instructions != b.Instructions || a.Status != b.Status {
		return true
	}
	if a.Agent != b.Agent || a.TaskID != b.TaskID {
		return true
	}
	if len(a.DependsOn) != len(b.DependsOn) {
		return true
	}
	depSet := make(map[string]bool, len(a.DependsOn))
	for _, d := range a.DependsOn {
		depSet[d] = true
	}
	for _, d := range b.DependsOn {
		if !depSet[d] {
			return true
		}
	}
	return false
}

// carryForwardCompleted preserves completed/skipped steps from the old plan
// into the new plan. If the new plan already has a step with the same ID,
// it's replaced with the completed version.
func carryForwardCompleted(old, new state.PlanSpec) state.PlanSpec {
	completedSteps := make(map[string]state.PlanStep)
	for _, step := range old.Steps {
		if step.Status == state.PlanStepStatusCompleted || step.Status == state.PlanStepStatusSkipped {
			completedSteps[step.StepID] = step
		}
	}
	if len(completedSteps) == 0 {
		return new
	}

	newStepIDs := make(map[string]bool, len(new.Steps))
	for i, step := range new.Steps {
		newStepIDs[step.StepID] = true
		if completed, ok := completedSteps[step.StepID]; ok {
			// Replace the new step with the completed one to preserve task linkage.
			new.Steps[i] = completed
		}
	}

	// Append any completed steps that aren't in the new plan at all.
	for id, step := range completedSteps {
		if !newStepIDs[id] {
			new.Steps = append(new.Steps, step)
		}
	}

	return new
}

// buildReplanContext assembles the failure context for the replanning prompt.
func buildReplanContext(plan state.PlanSpec, req ReplanRequest) string {
	var b strings.Builder
	b.WriteString("This is a REPLAN request. A previous plan has encountered issues.\n\n")
	b.WriteString(fmt.Sprintf("Trigger: %s\n", req.Trigger))
	if req.Reason != "" {
		b.WriteString(fmt.Sprintf("Reason: %s\n", req.Reason))
	}

	b.WriteString("\n--- Previous plan state ---\n")
	b.WriteString(fmt.Sprintf("Plan: %s (revision %d, status: %s)\n", plan.PlanID, plan.Revision, plan.Status))
	for _, step := range plan.Steps {
		b.WriteString(fmt.Sprintf("  [%s] %s — %s", step.Status, step.StepID, step.Title))
		if evidence, ok := req.Evidence[step.StepID]; ok {
			b.WriteString(fmt.Sprintf(" (failure: %s)", evidence))
		}
		b.WriteString("\n")
	}

	if len(plan.Assumptions) > 0 {
		b.WriteString("\nOriginal assumptions:\n")
		for _, a := range plan.Assumptions {
			b.WriteString(fmt.Sprintf("  - %s\n", a))
		}
	}

	b.WriteString("\n--- Instructions ---\n")
	b.WriteString("Generate a revised plan that:\n")
	b.WriteString("- Preserves step IDs for completed steps where possible.\n")
	b.WriteString("- Replaces or restructures failed/blocked steps.\n")
	b.WriteString("- Updates assumptions based on what was learned.\n")
	b.WriteString("- Adds new steps only when necessary.\n")

	if req.Context != "" {
		b.WriteString(fmt.Sprintf("\nAdditional context:\n%s\n", req.Context))
	}

	return b.String()
}
