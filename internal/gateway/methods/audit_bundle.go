package methods

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// ─── tasks.audit_export ──────────────────────────────────────────────────────

// AuditExportRequest is the input for the tasks.audit_export method.
type AuditExportRequest struct {
	// TaskID exports a single task and its descendants. Exactly one of TaskID or GoalID must be set.
	TaskID string `json:"task_id,omitempty"`
	// GoalID exports all tasks belonging to a goal.
	GoalID string `json:"goal_id,omitempty"`
	// IncludeInputs controls whether task inputs are included (default: false → redacted).
	IncludeInputs bool `json:"include_inputs,omitempty"`
	// IncludeMeta controls whether meta fields are included (default: false → redacted).
	IncludeMeta bool `json:"include_meta,omitempty"`
	// RunsLimit caps the number of runs returned per task (default: 50).
	RunsLimit int `json:"runs_limit,omitempty"`
}

func (r AuditExportRequest) Normalize() (AuditExportRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.GoalID = strings.TrimSpace(r.GoalID)
	if r.TaskID == "" && r.GoalID == "" {
		return r, fmt.Errorf("task_id or goal_id is required")
	}
	if r.TaskID != "" && r.GoalID != "" {
		return r, fmt.Errorf("specify task_id or goal_id, not both")
	}
	r.RunsLimit = normalizeLimit(r.RunsLimit, 50, 500)
	return r, nil
}

func DecodeAuditExportParams(params []byte) (AuditExportRequest, error) {
	return decodeMethodParams[AuditExportRequest](params)
}

// ─── Audit bundle schema ─────────────────────────────────────────────────────

// AuditBundle is the top-level export structure for a completed workflow.
type AuditBundle struct {
	// Schema version for forward-compat.
	SchemaVersion int    `json:"schema_version"`
	ExportedAt    int64  `json:"exported_at"`
	ExportedBy    string `json:"exported_by,omitempty"`

	// Scope of the export.
	GoalID string `json:"goal_id,omitempty"`
	RootTaskID string `json:"root_task_id,omitempty"`

	// Summary statistics.
	Summary AuditSummary `json:"summary"`

	// Task graph: tasks with their runs, transitions, and verification evidence.
	Tasks []AuditTask `json:"tasks"`

	// Integrity hash over the canonical JSON of the Tasks array.
	TasksHash string `json:"tasks_hash"`

	// Redaction manifest — lists which fields were stripped.
	Redactions []string `json:"redactions,omitempty"`
}

// AuditSummary provides aggregate statistics for the bundle.
type AuditSummary struct {
	TotalTasks      int            `json:"total_tasks"`
	TotalRuns       int            `json:"total_runs"`
	ByStatus        map[string]int `json:"by_status"`
	MaxDepth        int            `json:"max_depth"`
	EarliestCreated int64          `json:"earliest_created,omitempty"`
	LatestUpdated   int64          `json:"latest_updated,omitempty"`
	TotalUsage      state.TaskUsage `json:"total_usage"`
}

// AuditTask is a single task within the audit bundle.
type AuditTask struct {
	TaskID       string               `json:"task_id"`
	GoalID       string               `json:"goal_id,omitempty"`
	ParentTaskID string               `json:"parent_task_id,omitempty"`
	PlanID       string               `json:"plan_id,omitempty"`
	Title        string               `json:"title"`
	Instructions string               `json:"instructions"`
	Status       string               `json:"status"`
	Priority     string               `json:"priority,omitempty"`
	AssignedAgent string              `json:"assigned_agent,omitempty"`
	CreatedAt    int64                `json:"created_at,omitempty"`
	UpdatedAt    int64                `json:"updated_at,omitempty"`
	Depth        int                  `json:"depth"`

	// Governance snapshot.
	Authority    AuditAuthority       `json:"authority"`
	Budget       state.TaskBudget     `json:"budget,omitempty"`

	// Transitions show the full state machine history.
	Transitions  []state.TaskTransition `json:"transitions,omitempty"`

	// Verification evidence.
	Verification AuditVerification    `json:"verification,omitempty"`

	// Runs ordered by attempt.
	Runs         []AuditRun           `json:"runs,omitempty"`

	// Optional fields controlled by redaction flags.
	Inputs       map[string]any       `json:"inputs,omitempty"`
	Meta         map[string]any       `json:"meta,omitempty"`
}

// AuditAuthority captures the governance configuration at export time.
type AuditAuthority struct {
	AutonomyMode string `json:"autonomy_mode,omitempty"`
	RiskClass    string `json:"risk_class,omitempty"`
	CanAct       bool   `json:"can_act"`
	CanDelegate  bool   `json:"can_delegate"`
}

// AuditVerification captures verification status and individual check results.
type AuditVerification struct {
	Policy     string                    `json:"policy,omitempty"`
	Checks     []state.VerificationCheck `json:"checks,omitempty"`
	VerifiedAt int64                     `json:"verified_at,omitempty"`
	VerifiedBy string                    `json:"verified_by,omitempty"`
	AllPassed  bool                      `json:"all_passed"`
}

// AuditRun records a single execution attempt.
type AuditRun struct {
	RunID       string                    `json:"run_id"`
	Attempt     int                       `json:"attempt"`
	Status      string                    `json:"status"`
	AgentID     string                    `json:"agent_id,omitempty"`
	StartedAt   int64                     `json:"started_at,omitempty"`
	EndedAt     int64                     `json:"ended_at,omitempty"`
	Error       string                    `json:"error,omitempty"`
	Usage       state.TaskUsage           `json:"usage,omitempty"`
	Result      state.TaskResultRef       `json:"result,omitempty"`
	Transitions []state.TaskRunTransition `json:"transitions,omitempty"`
}

// ─── Bundle builder ──────────────────────────────────────────────────────────

// BuildAuditBundle constructs an AuditBundle from a set of tasks and a run-fetcher.
// The runFetcher returns runs for a given taskID, limited to runsLimit.
// Tasks should include the root and all descendants.
func BuildAuditBundle(
	tasks []state.TaskSpec,
	runsByTask map[string][]state.TaskRun,
	req AuditExportRequest,
	actor string,
	now time.Time,
) AuditBundle {
	bundle := AuditBundle{
		SchemaVersion: 1,
		ExportedAt:    now.Unix(),
		ExportedBy:    actor,
		GoalID:        req.GoalID,
	}

	// Build parent→children index for depth computation.
	childrenOf := make(map[string][]string, len(tasks))
	var roots []string
	for _, t := range tasks {
		parent := strings.TrimSpace(t.ParentTaskID)
		if parent != "" {
			childrenOf[parent] = append(childrenOf[parent], t.TaskID)
		} else {
			roots = append(roots, t.TaskID)
		}
	}

	// Compute depth via BFS from roots.
	depth := make(map[string]int, len(tasks))
	queue := make([]string, 0, len(tasks))
	for _, r := range roots {
		depth[r] = 0
		queue = append(queue, r)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range childrenOf[cur] {
			depth[child] = depth[cur] + 1
			queue = append(queue, child)
		}
	}
	// Orphans (parent not in set) get depth 0.
	for _, t := range tasks {
		if _, ok := depth[t.TaskID]; !ok {
			depth[t.TaskID] = 0
		}
	}

	if req.TaskID != "" {
		bundle.RootTaskID = req.TaskID
	} else if len(roots) == 1 {
		bundle.RootTaskID = roots[0]
	}

	// Track redactions.
	var redactions []string
	if !req.IncludeInputs {
		redactions = append(redactions, "task.inputs")
	}
	if !req.IncludeMeta {
		redactions = append(redactions, "task.meta", "run.meta")
	}
	bundle.Redactions = redactions

	// Build audit tasks.
	summary := AuditSummary{
		ByStatus: make(map[string]int),
	}
	auditTasks := make([]AuditTask, 0, len(tasks))
	for _, t := range tasks {
		at := buildAuditTask(t, runsByTask[t.TaskID], depth[t.TaskID], req)
		auditTasks = append(auditTasks, at)

		// Accumulate summary.
		s := string(t.Status)
		if s == "" {
			s = "unknown"
		}
		summary.ByStatus[s]++
		summary.TotalRuns += len(at.Runs)
		if d := depth[t.TaskID]; d > summary.MaxDepth {
			summary.MaxDepth = d
		}
		if t.CreatedAt > 0 && (summary.EarliestCreated == 0 || t.CreatedAt < summary.EarliestCreated) {
			summary.EarliestCreated = t.CreatedAt
		}
		if t.UpdatedAt > summary.LatestUpdated {
			summary.LatestUpdated = t.UpdatedAt
		}
		for _, run := range runsByTask[t.TaskID] {
			summary.TotalUsage.Add(run.Usage)
		}
	}
	summary.TotalTasks = len(auditTasks)
	bundle.Summary = summary
	bundle.Tasks = auditTasks

	// Compute integrity hash.
	bundle.TasksHash = computeTasksHash(auditTasks)

	return bundle
}

func buildAuditTask(t state.TaskSpec, runs []state.TaskRun, depth int, req AuditExportRequest) AuditTask {
	at := AuditTask{
		TaskID:        t.TaskID,
		GoalID:        t.GoalID,
		ParentTaskID:  t.ParentTaskID,
		PlanID:        t.PlanID,
		Title:         t.Title,
		Instructions:  t.Instructions,
		Status:        string(t.Status),
		Priority:      string(t.Priority),
		AssignedAgent: t.AssignedAgent,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		Depth:         depth,
		Authority: AuditAuthority{
			AutonomyMode: string(t.Authority.EffectiveAutonomyMode(state.AutonomySupervised)),
			RiskClass:    string(t.Authority.RiskClass),
			CanAct:       t.Authority.CanAct,
			CanDelegate:  t.Authority.CanDelegate,
		},
		Budget:      t.Budget,
		Transitions: t.Transitions,
		Verification: AuditVerification{
			Policy:     string(t.Verification.Policy),
			Checks:     t.Verification.Checks,
			VerifiedAt: t.Verification.VerifiedAt,
			VerifiedBy: t.Verification.VerifiedBy,
			AllPassed:  t.Verification.AllRequiredPassed(),
		},
	}

	// Include or redact optional fields.
	if req.IncludeInputs && len(t.Inputs) > 0 {
		at.Inputs = t.Inputs
	}
	if req.IncludeMeta && len(t.Meta) > 0 {
		at.Meta = t.Meta
	}

	// Build audit runs.
	at.Runs = make([]AuditRun, 0, len(runs))
	for _, run := range runs {
		ar := AuditRun{
			RunID:       run.RunID,
			Attempt:     run.Attempt,
			Status:      string(run.Status),
			AgentID:     run.AgentID,
			StartedAt:   run.StartedAt,
			EndedAt:     run.EndedAt,
			Error:       run.Error,
			Usage:       run.Usage,
			Result:      run.Result,
			Transitions: run.Transitions,
		}
		at.Runs = append(at.Runs, ar)
	}
	return at
}

// computeTasksHash computes a SHA-256 hash over the tasks array.
// Because AuditTask may contain map[string]any fields (Inputs, Meta) whose
// JSON marshal ordering is non-deterministic, the hash is a transport-integrity
// checksum rather than a reproducible fingerprint. Re-exporting the same data
// may yield a different hash if map iteration order changes.
func computeTasksHash(tasks []AuditTask) string {
	data, err := json.Marshal(tasks)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ─── Descendant collection ───────────────────────────────────────────────────

// CollectDescendants returns the root task plus all transitive children from allTasks.
func CollectDescendants(rootID string, allTasks []state.TaskSpec) []state.TaskSpec {
	childrenOf := make(map[string][]state.TaskSpec, len(allTasks))
	var root state.TaskSpec
	var found bool
	for _, t := range allTasks {
		if t.TaskID == rootID {
			root = t
			found = true
		}
		parent := strings.TrimSpace(t.ParentTaskID)
		if parent != "" {
			childrenOf[parent] = append(childrenOf[parent], t)
		}
	}
	if !found {
		return nil
	}
	result := []state.TaskSpec{root}
	queue := []string{rootID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range childrenOf[cur] {
			result = append(result, child)
			queue = append(queue, child.TaskID)
		}
	}
	return result
}
