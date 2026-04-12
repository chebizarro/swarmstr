package agent

// TraceContext carries stable task/run/step correlation IDs across runtime,
// tool lifecycle, WS event, and delegation boundaries. Every event surface
// that already carries SessionID/TurnID should also carry these IDs so
// operators can reconstruct a workflow end-to-end.
//
// Zero-value fields mean "not in a task context" — callers must not infer
// meaning from empty strings beyond absence.
type TraceContext struct {
	// GoalID is the top-level goal this work contributes to.
	GoalID string `json:"goal_id,omitempty"`
	// TaskID is the canonical task identifier from state.TaskSpec.
	TaskID string `json:"task_id,omitempty"`
	// RunID is the canonical run attempt identifier from state.TaskRun.
	RunID string `json:"run_id,omitempty"`
	// StepID is an optional sub-step within a run (e.g. plan step, tool batch).
	StepID string `json:"step_id,omitempty"`
	// ParentTaskID links to the delegating parent task (empty when root).
	ParentTaskID string `json:"parent_task_id,omitempty"`
	// ParentRunID links to the delegating parent run (empty when root).
	ParentRunID string `json:"parent_run_id,omitempty"`
}

// IsZero returns true when no correlation IDs have been set.
func (tc TraceContext) IsZero() bool {
	return tc.GoalID == "" && tc.TaskID == "" && tc.RunID == "" &&
		tc.StepID == "" && tc.ParentTaskID == "" && tc.ParentRunID == ""
}

// WithStep returns a copy with a new StepID, preserving all other fields.
func (tc TraceContext) WithStep(stepID string) TraceContext {
	tc.StepID = stepID
	return tc
}

// Child returns a new TraceContext for a delegated sub-task. The current
// task/run become the parent, and the child's GoalID is inherited.
func (tc TraceContext) Child(childTaskID, childRunID string) TraceContext {
	return TraceContext{
		GoalID:       tc.GoalID,
		TaskID:       childTaskID,
		RunID:        childRunID,
		ParentTaskID: tc.TaskID,
		ParentRunID:  tc.RunID,
	}
}
