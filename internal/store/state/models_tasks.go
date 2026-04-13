package state

import (
	"fmt"
	"strings"
)

type GoalStatus string

const (
	GoalStatusPending   GoalStatus = "pending"
	GoalStatusActive    GoalStatus = "active"
	GoalStatusBlocked   GoalStatus = "blocked"
	GoalStatusCompleted GoalStatus = "completed"
	GoalStatusFailed    GoalStatus = "failed"
	GoalStatusCancelled GoalStatus = "cancelled"
)

func ParseGoalStatus(raw string) (GoalStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(GoalStatusPending):
		return GoalStatusPending, true
	case string(GoalStatusActive):
		return GoalStatusActive, true
	case string(GoalStatusBlocked):
		return GoalStatusBlocked, true
	case string(GoalStatusCompleted):
		return GoalStatusCompleted, true
	case string(GoalStatusFailed):
		return GoalStatusFailed, true
	case string(GoalStatusCancelled):
		return GoalStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizeGoalStatus(raw string) GoalStatus {
	status, _ := ParseGoalStatus(raw)
	return status
}

func (s GoalStatus) Valid() bool {
	_, ok := ParseGoalStatus(string(s))
	return ok
}

// TaskStatus describes the canonical lifecycle state of a task.
type TaskStatus string

const (
	TaskStatusPending          TaskStatus = "pending"
	TaskStatusPlanned          TaskStatus = "planned"
	TaskStatusReady            TaskStatus = "ready"
	TaskStatusInProgress       TaskStatus = "in_progress"
	TaskStatusBlocked          TaskStatus = "blocked"
	TaskStatusAwaitingApproval TaskStatus = "awaiting_approval"
	TaskStatusVerifying        TaskStatus = "verifying"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCancelled        TaskStatus = "cancelled"
)

func ParseTaskStatus(raw string) (TaskStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(TaskStatusPending):
		return TaskStatusPending, true
	case string(TaskStatusPlanned):
		return TaskStatusPlanned, true
	case string(TaskStatusReady):
		return TaskStatusReady, true
	case string(TaskStatusInProgress):
		return TaskStatusInProgress, true
	case string(TaskStatusBlocked):
		return TaskStatusBlocked, true
	case string(TaskStatusAwaitingApproval):
		return TaskStatusAwaitingApproval, true
	case string(TaskStatusVerifying):
		return TaskStatusVerifying, true
	case string(TaskStatusCompleted):
		return TaskStatusCompleted, true
	case string(TaskStatusFailed):
		return TaskStatusFailed, true
	case string(TaskStatusCancelled):
		return TaskStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizeTaskStatus(raw string) TaskStatus {
	status, _ := ParseTaskStatus(raw)
	return status
}

func (s TaskStatus) Valid() bool {
	_, ok := ParseTaskStatus(string(s))
	return ok
}

// TaskRunStatus describes the lifecycle state of an execution attempt for a task.
type TaskRunStatus string

const (
	TaskRunStatusQueued           TaskRunStatus = "queued"
	TaskRunStatusRunning          TaskRunStatus = "running"
	TaskRunStatusBlocked          TaskRunStatus = "blocked"
	TaskRunStatusAwaitingApproval TaskRunStatus = "awaiting_approval"
	TaskRunStatusRetrying         TaskRunStatus = "retrying"
	TaskRunStatusCompleted        TaskRunStatus = "completed"
	TaskRunStatusFailed           TaskRunStatus = "failed"
	TaskRunStatusCancelled        TaskRunStatus = "cancelled"
)

func ParseTaskRunStatus(raw string) (TaskRunStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(TaskRunStatusQueued):
		return TaskRunStatusQueued, true
	case string(TaskRunStatusRunning):
		return TaskRunStatusRunning, true
	case string(TaskRunStatusBlocked):
		return TaskRunStatusBlocked, true
	case string(TaskRunStatusAwaitingApproval):
		return TaskRunStatusAwaitingApproval, true
	case string(TaskRunStatusRetrying):
		return TaskRunStatusRetrying, true
	case string(TaskRunStatusCompleted):
		return TaskRunStatusCompleted, true
	case string(TaskRunStatusFailed):
		return TaskRunStatusFailed, true
	case string(TaskRunStatusCancelled):
		return TaskRunStatusCancelled, true
	default:
		return "", false
	}
}

func NormalizeTaskRunStatus(raw string) TaskRunStatus {
	status, _ := ParseTaskRunStatus(raw)
	return status
}

func (s TaskRunStatus) Valid() bool {
	_, ok := ParseTaskRunStatus(string(s))
	return ok
}

// TaskPriority describes scheduling or triage priority.
type TaskPriority string

const (
	TaskPriorityHigh   TaskPriority = "high"
	TaskPriorityMedium TaskPriority = "medium"
	TaskPriorityLow    TaskPriority = "low"
)

func ParseTaskPriority(raw string) (TaskPriority, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(TaskPriorityHigh):
		return TaskPriorityHigh, true
	case string(TaskPriorityMedium), "":
		return TaskPriorityMedium, true
	case string(TaskPriorityLow):
		return TaskPriorityLow, true
	default:
		return "", false
	}
}

func NormalizeTaskPriority(raw string) TaskPriority {
	priority, _ := ParseTaskPriority(raw)
	return priority
}

func (p TaskPriority) Valid() bool {
	_, ok := ParseTaskPriority(string(p))
	return ok
}

// RiskClass categorizes the risk level of an operation.
type RiskClass string

const (
	RiskClassLow      RiskClass = "low"
	RiskClassMedium   RiskClass = "medium"
	RiskClassHigh     RiskClass = "high"
	RiskClassCritical RiskClass = "critical"
)

func ParseRiskClass(raw string) (RiskClass, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(RiskClassLow), "":
		return RiskClassLow, true
	case string(RiskClassMedium):
		return RiskClassMedium, true
	case string(RiskClassHigh):
		return RiskClassHigh, true
	case string(RiskClassCritical):
		return RiskClassCritical, true
	default:
		return "", false
	}
}

func NormalizeRiskClass(raw string) RiskClass {
	rc, ok := ParseRiskClass(raw)
	if !ok {
		return RiskClassLow
	}
	return rc
}

func (r RiskClass) Valid() bool {
	_, ok := ParseRiskClass(string(r))
	return ok
}

// TaskAuthority captures the authority contract attached to a goal or task.
// It defines what an agent is allowed to do and how much oversight is required.
type TaskAuthority struct {
	// AutonomyMode controls overall agent latitude (full, plan_approval, etc.).
	AutonomyMode AutonomyMode `json:"autonomy_mode,omitempty"`
	// Role is a human-readable label for the authority scope (e.g. "engineer", "reviewer").
	Role string `json:"role,omitempty"`
	// RiskClass categorizes the risk level — higher risk triggers more oversight.
	RiskClass RiskClass `json:"risk_class,omitempty"`
	// CanAct permits the agent to take actions (tool calls, writes).
	CanAct bool `json:"can_act,omitempty"`
	// CanDelegate permits spawning sub-agents or delegating sub-tasks.
	CanDelegate bool `json:"can_delegate,omitempty"`
	// CanEscalate permits the agent to escalate to a higher authority.
	CanEscalate bool `json:"can_escalate,omitempty"`
	// EscalationRequired forces all tool actions to escalation review.
	EscalationRequired bool `json:"escalation_required,omitempty"`
	// AllowedAgents restricts which agent IDs may be delegated to.
	AllowedAgents []string `json:"allowed_agents,omitempty"`
	// AllowedTools restricts which tools the agent may invoke.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DeniedTools lists tools explicitly denied regardless of other permissions.
	DeniedTools []string `json:"denied_tools,omitempty"`
	// MaxDelegationDepth caps how many levels deep delegation chains can go.
	MaxDelegationDepth int `json:"max_delegation_depth,omitempty"`

	// Deprecated: ApprovalMode is retained for backward-compatible JSON
	// deserialization of persisted state docs. Normalize() migrates it to
	// AutonomyMode. New code should use AutonomyMode directly.
	ApprovalMode string `json:"approval_mode,omitempty"`
}

// Normalize sets canonical defaults for zero-value authority fields.
// It also migrates the deprecated ApprovalMode field into AutonomyMode
// for backward compatibility with persisted state docs.
func (a TaskAuthority) Normalize() TaskAuthority {
	// Migrate legacy ApprovalMode → AutonomyMode when the new field is empty.
	if a.AutonomyMode == "" && a.ApprovalMode != "" {
		a.AutonomyMode = migrateApprovalMode(a.ApprovalMode)
		a.ApprovalMode = "" // clear deprecated field after migration
	}
	if a.AutonomyMode != "" {
		a.AutonomyMode = NormalizeAutonomyMode(string(a.AutonomyMode))
	}
	if a.RiskClass != "" {
		a.RiskClass = NormalizeRiskClass(string(a.RiskClass))
	}
	return a
}

// migrateApprovalMode maps legacy approval_mode strings to AutonomyMode values.
func migrateApprovalMode(legacy string) AutonomyMode {
	switch strings.ToLower(strings.TrimSpace(legacy)) {
	case "act_with_approval", "approval", "plan_approval":
		return AutonomyPlanApproval
	case "step_approval":
		return AutonomyStepApproval
	case "supervised", "observe_only", "recommend_only":
		return AutonomySupervised
	case "autonomous", "full", "bounded_autonomous", "fully_autonomous":
		return AutonomyFull
	default:
		// Unknown legacy value — default to plan_approval for safety
		// (more restrictive than full, less restrictive than supervised).
		return AutonomyPlanApproval
	}
}

// Validate checks that authority fields contain valid values.
func (a TaskAuthority) Validate() error {
	if a.AutonomyMode != "" && !a.AutonomyMode.Valid() {
		return fmt.Errorf("invalid autonomy_mode %q", a.AutonomyMode)
	}
	if a.RiskClass != "" && !a.RiskClass.Valid() {
		return fmt.Errorf("invalid risk_class %q", a.RiskClass)
	}
	if a.MaxDelegationDepth < 0 {
		return fmt.Errorf("max_delegation_depth must be >= 0")
	}
	return nil
}

// EffectiveAutonomyMode returns the authority's autonomy mode, or falls back
// to the given default when the authority doesn't specify one.
func (a TaskAuthority) EffectiveAutonomyMode(defaultMode AutonomyMode) AutonomyMode {
	if a.AutonomyMode != "" {
		return a.AutonomyMode
	}
	return defaultMode
}

// MayUseTool reports whether this authority permits the given tool name.
func (a TaskAuthority) MayUseTool(tool string) bool {
	for _, denied := range a.DeniedTools {
		if denied == tool {
			return false
		}
	}
	if len(a.AllowedTools) == 0 {
		return true // no allowlist means all tools permitted
	}
	for _, allowed := range a.AllowedTools {
		if allowed == tool {
			return true
		}
	}
	return false
}

// MayDelegateTo reports whether this authority permits delegation to the given agent.
func (a TaskAuthority) MayDelegateTo(agentID string) bool {
	if !a.CanDelegate {
		return false
	}
	if len(a.AllowedAgents) == 0 {
		return true // no restriction
	}
	for _, allowed := range a.AllowedAgents {
		if allowed == agentID {
			return true
		}
	}
	return false
}

// DefaultAuthority returns a reasonable default authority for the given autonomy mode.
func DefaultAuthority(mode AutonomyMode) TaskAuthority {
	switch mode {
	case AutonomySupervised:
		return TaskAuthority{
			AutonomyMode:       AutonomySupervised,
			CanAct:             false,
			CanDelegate:        false,
			CanEscalate:        true,
			EscalationRequired: true,
			RiskClass:          RiskClassHigh,
		}
	case AutonomyStepApproval:
		return TaskAuthority{
			AutonomyMode:       AutonomyStepApproval,
			CanAct:             true,
			CanDelegate:        false,
			CanEscalate:        true,
			RiskClass:          RiskClassMedium,
			MaxDelegationDepth: 1,
		}
	case AutonomyPlanApproval:
		return TaskAuthority{
			AutonomyMode:       AutonomyPlanApproval,
			CanAct:             true,
			CanDelegate:        true,
			CanEscalate:        true,
			RiskClass:          RiskClassMedium,
			MaxDelegationDepth: 2,
		}
	case AutonomyFull:
		return TaskAuthority{
			AutonomyMode:       AutonomyFull,
			CanAct:             true,
			CanDelegate:        true,
			CanEscalate:        true,
			RiskClass:          RiskClassLow,
			MaxDelegationDepth: 3,
		}
	default:
		return DefaultAuthority(AutonomyFull)
	}
}

// TaskBudget captures budget guardrails that downstream runtime layers enforce.
// Zero values mean "unlimited" for that dimension.
type TaskBudget struct {
	MaxPromptTokens     int   `json:"max_prompt_tokens,omitempty"`
	MaxCompletionTokens int   `json:"max_completion_tokens,omitempty"`
	MaxTotalTokens      int   `json:"max_total_tokens,omitempty"`
	MaxRuntimeMS        int64 `json:"max_runtime_ms,omitempty"`
	MaxToolCalls        int   `json:"max_tool_calls,omitempty"`
	MaxDelegations      int   `json:"max_delegations,omitempty"`
	MaxCostMicrosUSD    int64 `json:"max_cost_micros_usd,omitempty"`
}

// IsZero reports whether no budget limits have been set.
func (b TaskBudget) IsZero() bool {
	return b.MaxPromptTokens == 0 &&
		b.MaxCompletionTokens == 0 &&
		b.MaxTotalTokens == 0 &&
		b.MaxRuntimeMS == 0 &&
		b.MaxToolCalls == 0 &&
		b.MaxDelegations == 0 &&
		b.MaxCostMicrosUSD == 0
}

// Validate checks that budget values are non-negative and internally consistent.
func (b TaskBudget) Validate() error {
	if b.MaxPromptTokens < 0 {
		return fmt.Errorf("max_prompt_tokens must be >= 0")
	}
	if b.MaxCompletionTokens < 0 {
		return fmt.Errorf("max_completion_tokens must be >= 0")
	}
	if b.MaxTotalTokens < 0 {
		return fmt.Errorf("max_total_tokens must be >= 0")
	}
	if b.MaxRuntimeMS < 0 {
		return fmt.Errorf("max_runtime_ms must be >= 0")
	}
	if b.MaxToolCalls < 0 {
		return fmt.Errorf("max_tool_calls must be >= 0")
	}
	if b.MaxDelegations < 0 {
		return fmt.Errorf("max_delegations must be >= 0")
	}
	if b.MaxCostMicrosUSD < 0 {
		return fmt.Errorf("max_cost_micros_usd must be >= 0")
	}
	// If both component and total token limits are set, total must be >= sum.
	if b.MaxTotalTokens > 0 && b.MaxPromptTokens > 0 && b.MaxCompletionTokens > 0 {
		if b.MaxTotalTokens < b.MaxPromptTokens+b.MaxCompletionTokens {
			return fmt.Errorf("max_total_tokens (%d) < max_prompt_tokens (%d) + max_completion_tokens (%d)",
				b.MaxTotalTokens, b.MaxPromptTokens, b.MaxCompletionTokens)
		}
	}
	return nil
}

// Narrow returns a new budget that is the tighter of b and child for each
// dimension. This implements the inheritance rule: a child task's budget can
// only be equal to or stricter than its parent's. Zero (unlimited) in either
// input yields the other's limit.
func (b TaskBudget) Narrow(child TaskBudget) TaskBudget {
	return TaskBudget{
		MaxPromptTokens:     narrowInt(b.MaxPromptTokens, child.MaxPromptTokens),
		MaxCompletionTokens: narrowInt(b.MaxCompletionTokens, child.MaxCompletionTokens),
		MaxTotalTokens:      narrowInt(b.MaxTotalTokens, child.MaxTotalTokens),
		MaxRuntimeMS:        narrowInt64(b.MaxRuntimeMS, child.MaxRuntimeMS),
		MaxToolCalls:        narrowInt(b.MaxToolCalls, child.MaxToolCalls),
		MaxDelegations:      narrowInt(b.MaxDelegations, child.MaxDelegations),
		MaxCostMicrosUSD:    narrowInt64(b.MaxCostMicrosUSD, child.MaxCostMicrosUSD),
	}
}

// narrowInt returns the tighter of two limits. Zero means unlimited.
func narrowInt(parent, child int) int {
	if parent == 0 {
		return child
	}
	if child == 0 {
		return parent
	}
	if child < parent {
		return child
	}
	return parent
}

func narrowInt64(parent, child int64) int64 {
	if parent == 0 {
		return child
	}
	if child == 0 {
		return parent
	}
	if child < parent {
		return child
	}
	return parent
}

// TaskUsage captures measured runtime consumption for a task run.
type TaskUsage struct {
	PromptTokens     int   `json:"prompt_tokens,omitempty"`
	CompletionTokens int   `json:"completion_tokens,omitempty"`
	TotalTokens      int   `json:"total_tokens,omitempty"`
	WallClockMS      int64 `json:"wall_clock_ms,omitempty"`
	ToolCalls        int   `json:"tool_calls,omitempty"`
	Delegations      int   `json:"delegations,omitempty"`
	CostMicrosUSD    int64 `json:"cost_micros_usd,omitempty"`
}

// Add accumulates usage from another measurement.
func (u *TaskUsage) Add(other TaskUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.WallClockMS += other.WallClockMS
	u.ToolCalls += other.ToolCalls
	u.Delegations += other.Delegations
	u.CostMicrosUSD += other.CostMicrosUSD
}

// BudgetExceeded describes which budget dimensions have been exceeded.
type BudgetExceeded struct {
	PromptTokens     bool `json:"prompt_tokens,omitempty"`
	CompletionTokens bool `json:"completion_tokens,omitempty"`
	TotalTokens      bool `json:"total_tokens,omitempty"`
	RuntimeMS        bool `json:"runtime_ms,omitempty"`
	ToolCalls        bool `json:"tool_calls,omitempty"`
	Delegations      bool `json:"delegations,omitempty"`
	CostMicrosUSD    bool `json:"cost_micros_usd,omitempty"`
}

// Any reports whether any budget dimension has been exceeded.
func (e BudgetExceeded) Any() bool {
	return e.PromptTokens || e.CompletionTokens || e.TotalTokens ||
		e.RuntimeMS || e.ToolCalls || e.Delegations || e.CostMicrosUSD
}

// Reasons returns human-readable descriptions of exceeded dimensions.
func (e BudgetExceeded) Reasons() []string {
	var reasons []string
	if e.PromptTokens {
		reasons = append(reasons, "prompt tokens exceeded")
	}
	if e.CompletionTokens {
		reasons = append(reasons, "completion tokens exceeded")
	}
	if e.TotalTokens {
		reasons = append(reasons, "total tokens exceeded")
	}
	if e.RuntimeMS {
		reasons = append(reasons, "runtime exceeded")
	}
	if e.ToolCalls {
		reasons = append(reasons, "tool calls exceeded")
	}
	if e.Delegations {
		reasons = append(reasons, "delegations exceeded")
	}
	if e.CostMicrosUSD {
		reasons = append(reasons, "cost exceeded")
	}
	return reasons
}

// CheckUsage compares measured usage against a budget and returns which
// dimensions are exceeded. Zero budget values mean unlimited.
func (b TaskBudget) CheckUsage(usage TaskUsage) BudgetExceeded {
	return BudgetExceeded{
		PromptTokens:     b.MaxPromptTokens > 0 && usage.PromptTokens > b.MaxPromptTokens,
		CompletionTokens: b.MaxCompletionTokens > 0 && usage.CompletionTokens > b.MaxCompletionTokens,
		TotalTokens:      b.MaxTotalTokens > 0 && usage.TotalTokens > b.MaxTotalTokens,
		RuntimeMS:        b.MaxRuntimeMS > 0 && usage.WallClockMS > b.MaxRuntimeMS,
		ToolCalls:        b.MaxToolCalls > 0 && usage.ToolCalls > b.MaxToolCalls,
		Delegations:      b.MaxDelegations > 0 && usage.Delegations > b.MaxDelegations,
		CostMicrosUSD:    b.MaxCostMicrosUSD > 0 && usage.CostMicrosUSD > b.MaxCostMicrosUSD,
	}
}

// Remaining returns a budget representing the unused capacity given current usage.
// Dimensions with no limit (zero) remain zero (unlimited) in the result.
func (b TaskBudget) Remaining(usage TaskUsage) TaskBudget {
	remaining := func(limit, used int) int {
		if limit == 0 {
			return 0
		}
		r := limit - used
		if r < 0 {
			return 0
		}
		return r
	}
	remaining64 := func(limit, used int64) int64 {
		if limit == 0 {
			return 0
		}
		r := limit - used
		if r < 0 {
			return 0
		}
		return r
	}
	return TaskBudget{
		MaxPromptTokens:     remaining(b.MaxPromptTokens, usage.PromptTokens),
		MaxCompletionTokens: remaining(b.MaxCompletionTokens, usage.CompletionTokens),
		MaxTotalTokens:      remaining(b.MaxTotalTokens, usage.TotalTokens),
		MaxRuntimeMS:        remaining64(b.MaxRuntimeMS, usage.WallClockMS),
		MaxToolCalls:        remaining(b.MaxToolCalls, usage.ToolCalls),
		MaxDelegations:      remaining(b.MaxDelegations, usage.Delegations),
		MaxCostMicrosUSD:    remaining64(b.MaxCostMicrosUSD, usage.CostMicrosUSD),
	}
}

// TaskOutputSpec describes an expected output contract for a task.
type TaskOutputSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
	SchemaRef   string `json:"schema_ref,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// TaskAcceptanceCriterion describes how task completion should be judged.
type TaskAcceptanceCriterion struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

// ── Verification schemas ──────────────────────────────────────────────────��────

// VerificationStatus describes the lifecycle of a verification check.
type TaskResultRef struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
	URI  string `json:"uri,omitempty"`
	Hash string `json:"hash,omitempty"`
}

// GoalSpec is the canonical persisted representation of a user or system goal.
type GoalSpec struct {
	Version         int            `json:"version"`
	GoalID          string         `json:"goal_id"`
	Title           string         `json:"title"`
	Instructions    string         `json:"instructions,omitempty"`
	RequestedBy     string         `json:"requested_by,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	Status          GoalStatus     `json:"status"`
	Priority        TaskPriority   `json:"priority,omitempty"`
	Constraints     []string       `json:"constraints,omitempty"`
	SuccessCriteria []string       `json:"success_criteria,omitempty"`
	Authority       TaskAuthority  `json:"authority,omitempty"`
	Budget          TaskBudget     `json:"budget,omitempty"`
	CreatedAt       int64          `json:"created_at,omitempty"`
	UpdatedAt       int64          `json:"updated_at,omitempty"`
	Meta            map[string]any `json:"meta,omitempty"`
}

func (g GoalSpec) Normalize() GoalSpec {
	if g.Version == 0 {
		g.Version = 1
	}
	if !g.Status.Valid() {
		g.Status = GoalStatusPending
	}
	if strings.TrimSpace(string(g.Priority)) == "" {
		g.Priority = TaskPriorityMedium
	} else if !g.Priority.Valid() {
		g.Priority = TaskPriorityMedium
	}
	return g
}

func (g GoalSpec) Validate() error {
	if strings.TrimSpace(g.GoalID) == "" {
		return fmt.Errorf("goal_id is required")
	}
	if strings.TrimSpace(g.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if raw := strings.TrimSpace(string(g.Status)); raw != "" && !g.Status.Valid() {
		return fmt.Errorf("invalid goal status %q", g.Status)
	}
	if raw := strings.TrimSpace(string(g.Priority)); raw != "" && !g.Priority.Valid() {
		return fmt.Errorf("invalid goal priority %q", g.Priority)
	}
	return nil
}

// TaskSpec is the canonical persisted representation of a unit of work.
type TaskSpec struct {
	Version            int                       `json:"version"`
	TaskID             string                    `json:"task_id"`
	GoalID             string                    `json:"goal_id,omitempty"`
	ParentTaskID       string                    `json:"parent_task_id,omitempty"`
	PlanID             string                    `json:"plan_id,omitempty"`
	SessionID          string                    `json:"session_id,omitempty"`
	Title              string                    `json:"title"`
	Instructions       string                    `json:"instructions"`
	Inputs             map[string]any            `json:"inputs,omitempty"`
	ExpectedOutputs    []TaskOutputSpec          `json:"expected_outputs,omitempty"`
	AcceptanceCriteria []TaskAcceptanceCriterion `json:"acceptance_criteria,omitempty"`
	Dependencies       []string                  `json:"dependencies,omitempty"`
	AssignedAgent      string                    `json:"assigned_agent,omitempty"`
	CurrentRunID       string                    `json:"current_run_id,omitempty"`
	LastRunID          string                    `json:"last_run_id,omitempty"`
	Status             TaskStatus                `json:"status"`
	Priority           TaskPriority              `json:"priority,omitempty"`
	Authority          TaskAuthority             `json:"authority,omitempty"`
	MemoryScope        AgentMemoryScope          `json:"memory_scope,omitempty"`
	ToolProfile        string                    `json:"tool_profile,omitempty"`
	EnabledTools       []string                  `json:"enabled_tools,omitempty"`
	Budget             TaskBudget                `json:"budget,omitempty"`
	Verification       VerificationSpec          `json:"verification,omitempty"`
	CreatedAt          int64                     `json:"created_at,omitempty"`
	UpdatedAt          int64                     `json:"updated_at,omitempty"`
	Transitions        []TaskTransition          `json:"transitions,omitempty"`
	Meta               map[string]any            `json:"meta,omitempty"`
}

func (t TaskSpec) Normalize() TaskSpec {
	if t.Version == 0 {
		t.Version = 1
	}
	if !t.Status.Valid() {
		t.Status = TaskStatusPending
	}
	if strings.TrimSpace(string(t.Priority)) == "" {
		t.Priority = TaskPriorityMedium
	} else if !t.Priority.Valid() {
		t.Priority = TaskPriorityMedium
	}
	if t.MemoryScope != "" {
		t.MemoryScope = NormalizeAgentMemoryScope(string(t.MemoryScope))
	}
	return t
}

func (t TaskSpec) Validate() error {
	if strings.TrimSpace(t.TaskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(t.Instructions) == "" {
		return fmt.Errorf("instructions are required")
	}
	if raw := strings.TrimSpace(string(t.Status)); raw != "" && !t.Status.Valid() {
		return fmt.Errorf("invalid task status %q", t.Status)
	}
	if raw := strings.TrimSpace(string(t.Priority)); raw != "" && !t.Priority.Valid() {
		return fmt.Errorf("invalid task priority %q", t.Priority)
	}
	norm := t.Normalize()
	if norm.MemoryScope == "" && t.MemoryScope != "" {
		return fmt.Errorf("invalid memory_scope %q", t.MemoryScope)
	}
	for i, output := range t.ExpectedOutputs {
		if strings.TrimSpace(output.Name) == "" {
			return fmt.Errorf("expected_outputs[%d].name is required", i)
		}
	}
	for i, criterion := range t.AcceptanceCriteria {
		if strings.TrimSpace(criterion.Description) == "" {
			return fmt.Errorf("acceptance_criteria[%d].description is required", i)
		}
	}
	return nil
}

// TaskRun is the canonical persisted representation of a task execution attempt.
type TaskRun struct {
	Version       int                 `json:"version"`
	RunID         string              `json:"run_id"`
	TaskID        string              `json:"task_id"`
	GoalID        string              `json:"goal_id,omitempty"`
	ParentRunID   string              `json:"parent_run_id,omitempty"`
	SessionID     string              `json:"session_id,omitempty"`
	AgentID       string              `json:"agent_id,omitempty"`
	Attempt       int                 `json:"attempt"`
	Status        TaskRunStatus       `json:"status"`
	StartedAt     int64               `json:"started_at,omitempty"`
	EndedAt       int64               `json:"ended_at,omitempty"`
	Trigger       string              `json:"trigger,omitempty"`
	CheckpointRef string              `json:"checkpoint_ref,omitempty"`
	Result        TaskResultRef       `json:"result,omitempty"`
	Error         string              `json:"error,omitempty"`
	Usage         TaskUsage           `json:"usage,omitempty"`
	Verification  VerificationSpec    `json:"verification,omitempty"`
	Transitions   []TaskRunTransition `json:"transitions,omitempty"`
	Meta          map[string]any      `json:"meta,omitempty"`
}

func (r TaskRun) Normalize() TaskRun {
	if r.Version == 0 {
		r.Version = 1
	}
	if r.Attempt <= 0 {
		r.Attempt = 1
	}
	if !r.Status.Valid() {
		r.Status = TaskRunStatusQueued
	}
	return r
}

func (r TaskRun) Validate() error {
	if strings.TrimSpace(r.RunID) == "" {
		return fmt.Errorf("run_id is required")
	}
	if strings.TrimSpace(r.TaskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if raw := strings.TrimSpace(string(r.Status)); raw != "" && !r.Status.Valid() {
		return fmt.Errorf("invalid run status %q", r.Status)
	}
	if r.Attempt < 0 {
		return fmt.Errorf("attempt must be >= 0")
	}
	return nil
}

// ── Plan schemas ───────────────────────────────────────────────────────────────

// PlanStatus describes the lifecycle state of a plan.
type AutonomyMode string

const (
	// AutonomyFull allows the agent to plan, execute, and complete tasks
	// without operator approval.
	AutonomyFull AutonomyMode = "full"

	// AutonomyPlanApproval requires operator approval of plans before
	// task compilation and execution begin. Execution is autonomous.
	AutonomyPlanApproval AutonomyMode = "plan_approval"

	// AutonomyStepApproval requires approval before each plan step is
	// compiled into a task.
	AutonomyStepApproval AutonomyMode = "step_approval"

	// AutonomySupervised requires approval of plans and individual tool
	// calls within task execution. Most restrictive mode.
	AutonomySupervised AutonomyMode = "supervised"
)

func ParseAutonomyMode(raw string) (AutonomyMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(AutonomyFull), "":
		return AutonomyFull, true
	case string(AutonomyPlanApproval):
		return AutonomyPlanApproval, true
	case string(AutonomyStepApproval):
		return AutonomyStepApproval, true
	case string(AutonomySupervised):
		return AutonomySupervised, true
	default:
		return "", false
	}
}

func NormalizeAutonomyMode(raw string) AutonomyMode {
	mode, ok := ParseAutonomyMode(raw)
	if !ok {
		return AutonomyFull
	}
	return mode
}

func (m AutonomyMode) Valid() bool {
	_, ok := ParseAutonomyMode(string(m))
	return ok
}

// RequiresPlanApproval reports whether this mode requires plan-level
// approval before execution begins.
func (m AutonomyMode) RequiresPlanApproval() bool {
	switch m {
	case AutonomyPlanApproval, AutonomyStepApproval, AutonomySupervised:
		return true
	}
	return false
}

// RequiresStepApproval reports whether this mode requires per-step
// approval before task compilation.
func (m AutonomyMode) RequiresStepApproval() bool {
	switch m {
	case AutonomyStepApproval, AutonomySupervised:
		return true
	}
	return false
}

// PlanStep describes one unit of work inside a plan.
