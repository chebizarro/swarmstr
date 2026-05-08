package tasks

import (
	"context"
	"strings"
	"time"

	"metiq/internal/planner"
	"metiq/internal/store/state"
)

// VerificationExecutor runs task completion verification using the planner
// verifier runtime and returns both the gate result and lifecycle events. It is
// intentionally small so daemon/runtime code can gate completion without owning
// planner implementation details.
type VerificationExecutor struct {
	Runtime *planner.VerifierRuntime
	Now     func() time.Time
}

// VerificationExecution is the complete result of one verification pass.
type VerificationExecution struct {
	RuntimeResult planner.RuntimeResult
	Gate          planner.GateResult
	Events        []planner.VerificationEvent
	Summary       planner.VerificationSummary
}

// Execute evaluates task verification against the supplied outputs.
func (e VerificationExecutor) Execute(ctx context.Context, task state.TaskSpec, run state.TaskRun, outputs planner.TaskOutputs, actor string) VerificationExecution {
	if ctx == nil {
		ctx = context.Background()
	}
	runtime := e.Runtime
	if runtime == nil {
		runtime = planner.DefaultVerifierRuntime()
	}
	if strings.TrimSpace(actor) == "" {
		actor = "task_runner"
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	unixNow := now.Unix()

	task = task.Normalize()
	run = run.Normalize()
	result := runtime.EvaluateAll(ctx, task, outputs, actor, unixNow)

	telemetry := planner.NewVerificationTelemetry(nil)
	planner.EmitRuntimeEvents(telemetry, task.TaskID, run.RunID, result, unixNow)

	gate := VerificationGateFromRuntimeResult(task, result)
	planner.EmitGateEvent(telemetry, task.TaskID, run.RunID, gate, unixNow)

	summary := planner.BuildVerificationSummary(task.TaskID, run.RunID, result.UpdatedSpec, &result, &gate)
	return VerificationExecution{
		RuntimeResult: result,
		Gate:          gate,
		Events:        telemetry.Events(),
		Summary:       summary,
	}
}

// VerificationGateFromRuntimeResult maps a verifier runtime result to the
// terminal completion gate used by task runs. Required verification failures are
// terminal failures for the current run; advisory failures are allowed because
// the verifier runtime marks advisory policy as passed.
func VerificationGateFromRuntimeResult(task state.TaskSpec, result planner.RuntimeResult) planner.GateResult {
	if result.Passed {
		return planner.GateResult{Decision: planner.GateAllow, Reason: result.Summary, UpdatedSpec: result.UpdatedSpec}
	}
	return planner.GateResult{
		Decision:     planner.GateBlock,
		Reason:       result.Summary,
		FailedChecks: verificationBlockingChecks(result),
		Suggestion:   "resolve failed verification checks before completing the task",
		UpdatedSpec:  result.UpdatedSpec,
	}
}

func verificationBlockingChecks(result planner.RuntimeResult) []string {
	out := make([]string, 0, len(result.CheckResults))
	for _, check := range result.CheckResults {
		if !check.Required {
			continue
		}
		if check.Pending || check.Error != "" || !check.Outcome.Passed {
			out = append(out, check.CheckID)
		}
	}
	return out
}
