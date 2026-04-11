// Package planner provides a general-purpose task decomposition planner
// that converts goals into structured plans using an LLM provider.
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

// Planner generates structured plans from goals using an LLM provider.
type Planner struct {
	provider agent.Provider
	model    string
}

// Option configures a Planner.
type Option func(*Planner)

// WithModel sets the model identifier surfaced in plan metadata.
func WithModel(model string) Option {
	return func(p *Planner) { p.model = model }
}

// New creates a Planner that uses the given provider for LLM generation.
func New(provider agent.Provider, opts ...Option) *Planner {
	p := &Planner{provider: provider}
	for _, o := range opts {
		o(p)
	}
	return p
}

// PlanRequest holds the inputs for plan generation.
type PlanRequest struct {
	Goal        state.GoalSpec
	Context     string // optional additional context / constraints
	MaxSteps    int    // 0 = use default (10)
	SessionID   string
}

// GeneratePlan produces a structured PlanSpec from a goal.
// If the LLM output is malformed or the goal is underspecified, the returned
// plan will contain steps marked as blocked with a reason.
func (p *Planner) GeneratePlan(ctx context.Context, req PlanRequest) (state.PlanSpec, error) {
	if p.provider == nil {
		return state.PlanSpec{}, fmt.Errorf("planner: provider is nil")
	}
	goal := req.Goal.Normalize()
	if err := goal.Validate(); err != nil {
		return state.PlanSpec{}, fmt.Errorf("planner: invalid goal: %w", err)
	}

	maxSteps := req.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 10
	}

	prompt := buildPlannerPrompt(goal, req.Context, maxSteps)

	turn := agent.Turn{
		SessionID:         req.SessionID,
		TurnID:            fmt.Sprintf("planner:%s", goal.GoalID),
		UserText:          prompt,
		StaticSystemPrompt: plannerSystemPrompt,
	}

	result, err := p.provider.Generate(ctx, turn)
	if err != nil {
		return state.PlanSpec{}, fmt.Errorf("planner: provider error: %w", err)
	}

	plan, parseErr := parsePlanResponse(result.Text, goal)
	if parseErr != nil {
		// Return a degraded single-step plan that captures the raw output
		// and marks it as needing human review.
		plan = fallbackPlan(goal, result.Text, parseErr)
	}

	now := time.Now().Unix()
	plan.GoalID = goal.GoalID
	if plan.CreatedAt == 0 {
		plan.CreatedAt = now
	}
	plan.UpdatedAt = now
	if plan.Meta == nil {
		plan.Meta = map[string]any{}
	}
	if p.model != "" {
		plan.Meta["planner_model"] = p.model
	}

	plan = plan.Normalize()
	if err := plan.Validate(); err != nil {
		return state.PlanSpec{}, fmt.Errorf("planner: generated plan is invalid: %w", err)
	}
	if plan.HasCycle() {
		return state.PlanSpec{}, fmt.Errorf("planner: generated plan has cyclic dependencies")
	}
	return plan, nil
}

// plannerSystemPrompt is the system prompt used for plan generation.
const plannerSystemPrompt = `You are a task planner. Given a goal, you decompose it into concrete, actionable steps.

You MUST respond with valid JSON only — no markdown, no explanation outside the JSON.

Output schema:
{
  "plan_id": "string (unique identifier)",
  "title": "string (short plan title)",
  "steps": [
    {
      "step_id": "string (unique within the plan)",
      "title": "string (short step title)",
      "instructions": "string (what to do)",
      "depends_on": ["step_id", ...],
      "status": "pending" or "blocked",
      "agent": "string (optional: suggested agent/role)",
      "outputs": [{"name": "string", "description": "string", "format": "string"}]
    }
  ],
  "assumptions": ["string", ...],
  "risks": ["string", ...],
  "rollback_strategy": "string (optional)"
}

Rules:
- Each step must have a unique step_id.
- Dependencies reference other step_ids within the same plan.
- No circular dependencies.
- If information is missing or the goal is underspecified, add a step with status "blocked" and instructions explaining what information is needed.
- Keep steps concrete and actionable. Prefer 3-8 steps for most goals.
- Do not hallucinate implementation details you cannot verify.`

func buildPlannerPrompt(goal state.GoalSpec, extraContext string, maxSteps int) string {
	var b strings.Builder
	b.WriteString("Generate a structured plan for the following goal.\n\n")
	b.WriteString(fmt.Sprintf("Goal ID: %s\n", goal.GoalID))
	b.WriteString(fmt.Sprintf("Title: %s\n", goal.Title))
	if goal.Instructions != "" {
		b.WriteString(fmt.Sprintf("Instructions: %s\n", goal.Instructions))
	}
	if len(goal.Constraints) > 0 {
		b.WriteString(fmt.Sprintf("Constraints: %s\n", strings.Join(goal.Constraints, "; ")))
	}
	if len(goal.SuccessCriteria) > 0 {
		b.WriteString(fmt.Sprintf("Success criteria: %s\n", strings.Join(goal.SuccessCriteria, "; ")))
	}
	if goal.Priority != "" {
		b.WriteString(fmt.Sprintf("Priority: %s\n", goal.Priority))
	}
	if extraContext != "" {
		b.WriteString(fmt.Sprintf("\nAdditional context:\n%s\n", extraContext))
	}
	b.WriteString(fmt.Sprintf("\nMaximum steps: %d\n", maxSteps))
	b.WriteString("\nRespond with the JSON plan only.")
	return b.String()
}

// parsePlanResponse extracts and validates a PlanSpec from LLM text output.
func parsePlanResponse(text string, goal state.GoalSpec) (state.PlanSpec, error) {
	text = strings.TrimSpace(text)
	// Strip markdown code fences if the response is wrapped in one.
	// Only strip when the text starts with a fence (possibly after
	// non-JSON preamble) — we locate the first ``` that precedes a {.
	if idx := strings.Index(text, "```"); idx >= 0 {
		// Only strip if everything before the fence is non-JSON preamble
		// (no opening brace before the fence).
		if !strings.Contains(text[:idx], "{") {
			inner := text[idx+3:]
			if strings.HasPrefix(inner, "json") {
				inner = inner[4:]
			}
			inner = strings.TrimPrefix(inner, "\n")
			if end := strings.LastIndex(inner, "```"); end >= 0 {
				inner = inner[:end]
			}
			text = strings.TrimSpace(inner)
		}
	}

	var raw struct {
		PlanID           string          `json:"plan_id"`
		Title            string          `json:"title"`
		Steps            json.RawMessage `json:"steps"`
		Assumptions      []string        `json:"assumptions"`
		Risks            []string        `json:"risks"`
		RollbackStrategy string          `json:"rollback_strategy"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return state.PlanSpec{}, fmt.Errorf("json decode: %w", err)
	}

	var steps []state.PlanStep
	if err := json.Unmarshal(raw.Steps, &steps); err != nil {
		return state.PlanSpec{}, fmt.Errorf("steps decode: %w", err)
	}

	planID := strings.TrimSpace(raw.PlanID)
	if planID == "" {
		planID = fmt.Sprintf("plan:%s", goal.GoalID)
	}
	title := strings.TrimSpace(raw.Title)
	if title == "" {
		title = fmt.Sprintf("Plan for: %s", goal.Title)
	}

	plan := state.PlanSpec{
		Version:          1,
		PlanID:           planID,
		Title:            title,
		Revision:         1,
		Status:           state.PlanStatusDraft,
		Steps:            steps,
		Assumptions:      raw.Assumptions,
		Risks:            raw.Risks,
		RollbackStrategy: raw.RollbackStrategy,
	}
	return plan, nil
}

// fallbackPlan creates a minimal single-step plan when parsing fails.
// The step captures the raw LLM output and is marked as blocked for human review.
func fallbackPlan(goal state.GoalSpec, rawOutput string, parseErr error) state.PlanSpec {
	truncated := rawOutput
	if len(truncated) > 500 {
		truncated = truncated[:500] + "…"
	}
	return state.PlanSpec{
		Version:  1,
		PlanID:   fmt.Sprintf("plan:%s", goal.GoalID),
		Title:    fmt.Sprintf("Plan for: %s (needs review)", goal.Title),
		Revision: 1,
		Status:   state.PlanStatusDraft,
		Steps: []state.PlanStep{
			{
				StepID:       "review",
				Title:        "Review planner output",
				Instructions: fmt.Sprintf("The planner could not produce a structured plan. Parse error: %s\n\nRaw output:\n%s", parseErr, truncated),
				Status:       state.PlanStepStatusBlocked,
				Meta:         map[string]any{"parse_error": parseErr.Error()},
			},
		},
		Meta: map[string]any{"fallback": true, "parse_error": parseErr.Error()},
	}
}
