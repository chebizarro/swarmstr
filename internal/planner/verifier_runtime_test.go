package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ── Test helpers ────────────────────────────────────────────────────────────

func makeTask(checks []state.VerificationCheck, policy state.VerificationPolicy) state.TaskSpec {
	return state.TaskSpec{
		TaskID:       "task-1",
		Title:        "test task",
		Instructions: "do the thing",
		Verification: state.VerificationSpec{
			Policy: policy,
			Checks: checks,
		},
	}
}

// ── Runtime basics ──────────────────────────────────────────────────────────

func TestVerifierRuntime_NewEmpty(t *testing.T) {
	rt := NewVerifierRuntime()
	if rt.HasExecutor(state.VerificationCheckSchema) {
		t.Fatal("empty runtime should have no executors")
	}
}

func TestVerifierRuntime_Register(t *testing.T) {
	rt := NewVerifierRuntime()
	rt.Register(&SchemaCheckExecutor{})
	if !rt.HasExecutor(state.VerificationCheckSchema) {
		t.Fatal("expected schema executor registered")
	}
}

func TestVerifierRuntime_DefaultHasAllBuiltins(t *testing.T) {
	rt := DefaultVerifierRuntime()
	for _, typ := range []state.VerificationCheckType{
		state.VerificationCheckSchema,
		state.VerificationCheckEvidence,
		state.VerificationCheckTest,
		state.VerificationCheckCustom,
	} {
		if !rt.HasExecutor(typ) {
			t.Errorf("default runtime missing executor for %q", typ)
		}
	}
}

func TestVerifierRuntime_NoChecks(t *testing.T) {
	rt := DefaultVerifierRuntime()
	task := makeTask(nil, state.VerificationPolicyNone)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 0)
	if !result.Passed {
		t.Fatal("expected pass with no checks")
	}
}

// ── Schema executor ─────────────────────────────────────────────────────────

func TestSchemaExecutor_NoStructuredOutput(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has output", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with no structured output")
	}
}

func TestSchemaExecutor_EmptyOutput(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has data", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{StructuredOutput: map[string]any{}}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with empty structured output")
	}
}

func TestSchemaExecutor_HasFields_NoRequirements(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has data", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{StructuredOutput: map[string]any{"name": "Alice", "age": 30}}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}
}

func TestSchemaExecutor_RequiredFields_Present(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "s1", Type: state.VerificationCheckSchema,
			Description: "has required fields", Required: true,
			Meta: map[string]any{"required_fields": []any{"name", "email"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{StructuredOutput: map[string]any{"name": "Alice", "email": "a@b.c", "extra": true}}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}
}

func TestSchemaExecutor_RequiredFields_Missing(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "s1", Type: state.VerificationCheckSchema,
			Description: "has required fields", Required: true,
			Meta: map[string]any{"required_fields": []any{"name", "email"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{StructuredOutput: map[string]any{"name": "Alice"}}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with missing email field")
	}
	// Check that the failure identifies the missing field.
	found := false
	for _, cr := range result.CheckResults {
		if cr.CheckID == "s1" && !cr.Outcome.Passed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected s1 to fail")
	}
}

// ── Evidence executor ───────────────────────────────────────────────────────

func TestEvidenceExecutor_NoOutputOrArtifacts(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "e1", Type: state.VerificationCheckEvidence, Description: "has evidence", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with no output")
	}
}

func TestEvidenceExecutor_HasRawOutput(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "e1", Type: state.VerificationCheckEvidence, Description: "has evidence", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{RawOutput: "here is the answer"}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass with raw output, got: %s", result.Summary)
	}
}

func TestEvidenceExecutor_RequiredArtifacts_Present(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "e1", Type: state.VerificationCheckEvidence,
			Description: "has artifacts", Required: true,
			Meta: map[string]any{"required_artifacts": []any{"report.md", "data.json"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		Artifacts: []TaskArtifact{
			{Name: "report.md", Content: "# Report"},
			{Name: "data.json", Content: `{"key":"val"}`},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}
}

func TestEvidenceExecutor_RequiredArtifacts_Missing(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "e1", Type: state.VerificationCheckEvidence,
			Description: "has artifacts", Required: true,
			Meta: map[string]any{"required_artifacts": []any{"report.md", "data.json"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		Artifacts: []TaskArtifact{{Name: "report.md"}},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with missing data.json")
	}
}

// ── Tool output (test) executor ─────────────────────────────────────────────

func TestToolOutputExecutor_NoToolResults(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "t1", Type: state.VerificationCheckTest, Description: "tools ok", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with no tool results")
	}
}

func TestToolOutputExecutor_AllSucceeded(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "t1", Type: state.VerificationCheckTest, Description: "tools ok", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		ToolResults: map[string]ToolCallResult{
			"call-1": {ToolName: "fetch", Output: "ok"},
			"call-2": {ToolName: "publish", Output: "done"},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}
}

func TestToolOutputExecutor_SomeFailed(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "t1", Type: state.VerificationCheckTest, Description: "tools ok", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		ToolResults: map[string]ToolCallResult{
			"call-1": {ToolName: "fetch", Output: "ok"},
			"call-2": {ToolName: "publish", Error: "connection refused"},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with tool error")
	}
}

func TestToolOutputExecutor_RequiredTools_Present(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "t1", Type: state.VerificationCheckTest,
			Description: "required tools called", Required: true,
			Meta: map[string]any{"required_tools": []any{"fetch", "verify"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		ToolResults: map[string]ToolCallResult{
			"c1": {ToolName: "fetch", Output: "ok"},
			"c2": {ToolName: "verify", Output: "ok"},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}
}

func TestToolOutputExecutor_RequiredTools_Missing(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "t1", Type: state.VerificationCheckTest,
			Description: "required tools called", Required: true,
			Meta: map[string]any{"required_tools": []any{"fetch", "verify"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		ToolResults: map[string]ToolCallResult{
			"c1": {ToolName: "fetch", Output: "ok"},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with missing verify tool")
	}
}

// ── Custom executor (dry-run) ───────────────────────────────────────────────

func TestCustomExecutor_DryRun_Success(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "d1", Type: state.VerificationCheckCustom,
			Description: "dry run publish", Required: true,
			Meta: map[string]any{"evaluator": "dry_run", "tool": "nostr_publish"},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		ToolResults: map[string]ToolCallResult{
			"c1": {ToolName: "nostr_publish", Output: "simulated ok"},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}
}

func TestCustomExecutor_DryRun_Failed(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "d1", Type: state.VerificationCheckCustom,
			Description: "dry run publish", Required: true,
			Meta: map[string]any{"evaluator": "dry_run", "tool": "nostr_publish"},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		ToolResults: map[string]ToolCallResult{
			"c1": {ToolName: "nostr_publish", Error: "relay refused"},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with dry-run error")
	}
}

func TestCustomExecutor_DryRun_MissingTool(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "d1", Type: state.VerificationCheckCustom,
			Description: "dry run", Required: true,
			Meta: map[string]any{"evaluator": "dry_run", "tool": "nostr_publish"},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with missing tool")
	}
}

func TestCustomExecutor_OutputContains(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "c1", Type: state.VerificationCheckCustom,
			Description: "output has keyword", Required: true,
			Meta: map[string]any{"evaluator": "output_contains", "contains": "SUCCESS"},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)

	// Pass case.
	outputs := TaskOutputs{RawOutput: "Operation completed: SUCCESS"}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}

	// Fail case.
	outputs2 := TaskOutputs{RawOutput: "Operation failed"}
	checks2 := []state.VerificationCheck{
		{
			CheckID: "c1", Type: state.VerificationCheckCustom,
			Description: "output has keyword", Required: true,
			Meta: map[string]any{"evaluator": "output_contains", "contains": "SUCCESS"},
		},
	}
	task2 := makeTask(checks2, state.VerificationPolicyRequired)
	result2 := rt.EvaluateAll(context.Background(), task2, outputs2, "test", 200)
	if result2.Passed {
		t.Fatal("expected fail without keyword")
	}
}

func TestCustomExecutor_OutputNotEmpty(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "c1", Type: state.VerificationCheckCustom,
			Description: "has output", Required: true,
			Meta: map[string]any{"evaluator": "output_not_empty"},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{RawOutput: "hello"}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}

	// Empty.
	checks2 := []state.VerificationCheck{
		{
			CheckID: "c1", Type: state.VerificationCheckCustom,
			Description: "has output", Required: true,
			Meta: map[string]any{"evaluator": "output_not_empty"},
		},
	}
	task2 := makeTask(checks2, state.VerificationPolicyRequired)
	result2 := rt.EvaluateAll(context.Background(), task2, TaskOutputs{}, "test", 200)
	if result2.Passed {
		t.Fatal("expected fail with empty output")
	}
}

func TestCustomExecutor_UnknownEvaluator(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "c1", Type: state.VerificationCheckCustom,
			Description: "custom", Required: true,
			Meta: map[string]any{"evaluator": "nonexistent"},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected fail with unknown evaluator")
	}
}

func TestSchemaExecutor_AcceptsStringSliceMeta(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "s1", Type: state.VerificationCheckSchema,
			Description: "has required fields", Required: true,
			Meta: map[string]any{"required_fields": []string{"name", "email"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{StructuredOutput: map[string]any{"name": "Alice", "email": "a@b.c"}}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected []string metadata to pass, got: %s", result.Summary)
	}
}

func TestToolOutputExecutor_RequiredToolMustSucceed(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "t1", Type: state.VerificationCheckTest,
			Description: "required tools called", Required: true,
			Meta: map[string]any{"required_tools": []string{"fetch", "verify"}},
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		ToolResults: map[string]ToolCallResult{
			"c1": {ToolName: "fetch", Output: "ok"},
			"c2": {ToolName: "verify", Error: "assertion failed"},
		},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected required tool check to fail when the required tool call errored")
	}
	if got := result.UpdatedSpec.Checks[0].Status; got != state.VerificationStatusFailed {
		t.Fatalf("expected failed status, got %s", got)
	}
}

func TestReviewExecutor_RequiresExplicitApproval(t *testing.T) {
	rt := NewVerifierRuntime()
	rt.Register(&MetadataReviewCheckExecutor{})
	checks := []state.VerificationCheck{
		{
			CheckID: "r1", Type: state.VerificationCheckReview,
			Description: "review output", Required: true,
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "reviewer", 100)
	if result.Passed {
		t.Fatal("expected review without explicit approval to fail")
	}
	if got := result.UpdatedSpec.Checks[0].Status; got != state.VerificationStatusFailed {
		t.Fatalf("expected failed review status, got %s", got)
	}

	checks[0].Meta = map[string]any{"approved": true, "reviewer": "alice", "comment": "looks good"}
	result = rt.EvaluateAll(context.Background(), makeTask(checks, state.VerificationPolicyRequired), TaskOutputs{}, "reviewer", 200)
	if !result.Passed {
		t.Fatalf("expected explicitly approved review to pass, got: %s", result.Summary)
	}
	if result.UpdatedSpec.Checks[0].Evidence != "looks good" {
		t.Fatalf("expected review comment as evidence, got %q", result.UpdatedSpec.Checks[0].Evidence)
	}
}

// ── No executor for check type ──────────────────────────────────────────────

func TestRuntime_NoExecutor_RevertsRunningToPending(t *testing.T) {
	rt := NewVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "x1", Type: state.VerificationCheckType("external"), Description: "external verifier", Required: true, Status: state.VerificationStatusRunning},
	}
	result := rt.EvaluateAll(context.Background(), makeTask(checks, state.VerificationPolicyRequired), TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected required no-executor check to block")
	}
	if got := result.UpdatedSpec.Checks[0].Status; got != state.VerificationStatusPending {
		t.Fatalf("expected stale running status to revert to pending, got %s", got)
	}
}

func TestRuntime_NoExecutor_LeavePending(t *testing.T) {
	rt := NewVerifierRuntime() // empty, no executors
	checks := []state.VerificationCheck{
		{CheckID: "r1", Type: state.VerificationCheckReview, Description: "needs review", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected fail — review check has no executor and is required")
	}
	// Check should remain pending in updated spec.
	for _, c := range result.UpdatedSpec.Checks {
		if c.CheckID == "r1" && c.Status != state.VerificationStatusPending {
			t.Fatalf("expected r1 to remain pending, got %s", c.Status)
		}
	}
	// CheckResult should be marked as Pending, not Error.
	for _, cr := range result.CheckResults {
		if cr.CheckID == "r1" {
			if !cr.Pending {
				t.Fatal("expected r1 check result to be marked pending")
			}
			if cr.Error != "" {
				t.Fatalf("expected no error for pending check, got %q", cr.Error)
			}
		}
	}
}

// ── Advisory policy ─────────────────────────────────────────────────────────

func TestRuntime_Advisory_FailsDoNotBlock(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has output", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyAdvisory)
	// No structured output → schema check fails, but advisory = pass.
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if !result.Passed {
		t.Fatal("advisory policy should pass even with failing checks")
	}
	if result.UpdatedSpec.VerifiedAt != 0 || result.UpdatedSpec.VerifiedBy != "" {
		t.Fatalf("advisory failure should not stamp verified state: %+v", result.UpdatedSpec)
	}
}

func TestRuntime_ClearsStaleVerifiedStateOnFailure(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{CheckID: "s1", Type: state.VerificationCheckSchema, Description: "has output", Required: true},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	task.Verification.VerifiedAt = 50
	task.Verification.VerifiedBy = "previous-verifier"
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected verification failure")
	}
	if result.UpdatedSpec.VerifiedAt != 0 || result.UpdatedSpec.VerifiedBy != "" {
		t.Fatalf("expected stale verified state to be cleared: %+v", result.UpdatedSpec)
	}
}

type blockingExecutor struct {
	typ     state.VerificationCheckType
	started int32
	release chan struct{}
	once    sync.Once
}

func (e *blockingExecutor) Type() state.VerificationCheckType { return e.typ }

func (e *blockingExecutor) Execute(ctx context.Context, _ state.VerificationCheck, _ state.TaskSpec, _ TaskOutputs) (CheckOutcome, error) {
	if atomic.AddInt32(&e.started, 1) == 2 {
		e.once.Do(func() { close(e.release) })
	}
	select {
	case <-e.release:
		return CheckOutcome{Passed: true, Result: "ok"}, nil
	case <-ctx.Done():
		return CheckOutcome{}, ctx.Err()
	}
}

func TestRuntime_EvaluatesPendingExecutorChecksConcurrently(t *testing.T) {
	exec := &blockingExecutor{typ: state.VerificationCheckType("barrier"), release: make(chan struct{})}
	rt := NewVerifierRuntime()
	rt.Register(exec)
	checks := []state.VerificationCheck{
		{CheckID: "c1", Type: exec.typ, Description: "first", Required: true},
		{CheckID: "c2", Type: exec.typ, Description: "second", Required: true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	result := rt.EvaluateAll(ctx, makeTask(checks, state.VerificationPolicyRequired), TaskOutputs{}, "test", 100)
	if !result.Passed {
		t.Fatalf("expected concurrent checks to pass, got: %s", result.Summary)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("verification appears sequential or blocked; elapsed=%s", elapsed)
	}
	if got := atomic.LoadInt32(&exec.started); got != 2 {
		t.Fatalf("expected both checks to start, got %d", got)
	}
}

// ── Already-evaluated checks ────────────────────────────────────────────────

func TestRuntime_TerminalErrorPreservesErrorMarker(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "err1", Type: state.VerificationCheckSchema,
			Description: "already errored", Required: true,
			Status: state.VerificationStatusError, Result: "executor crashed",
		},
	}
	result := rt.EvaluateAll(context.Background(), makeTask(checks, state.VerificationPolicyRequired), TaskOutputs{StructuredOutput: map[string]any{"ok": true}}, "test", 100)
	if result.CheckResults[0].Error != "executor crashed" {
		t.Fatalf("expected terminal error result to preserve error, got %+v", result.CheckResults[0])
	}
	formatted := FormatRuntimeResult(result)
	if !strings.Contains(formatted, "⚠") {
		t.Fatalf("expected formatted result to show warning marker, got %q", formatted)
	}
}

func TestRuntime_SkipsTerminalChecks(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "s1", Type: state.VerificationCheckSchema,
			Description: "already passed", Required: true,
			Status: state.VerificationStatusPassed, Result: "ok",
		},
		{
			CheckID: "e1", Type: state.VerificationCheckEvidence,
			Description: "needs eval", Required: true,
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{RawOutput: "evidence here"}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Summary)
	}
	// s1 should not be re-evaluated.
	for _, cr := range result.CheckResults {
		if cr.CheckID == "s1" && cr.Duration > 0 {
			t.Fatal("already-passed check should not be re-executed")
		}
	}
}

// ── Executor error handling ─────────────────────────────────────────────────

type failingExecutor struct{}

func (e *failingExecutor) Type() state.VerificationCheckType {
	return state.VerificationCheckType("always_fail")
}

func (e *failingExecutor) Execute(_ context.Context, _ state.VerificationCheck, _ state.TaskSpec, _ TaskOutputs) (CheckOutcome, error) {
	return CheckOutcome{}, fmt.Errorf("executor crashed")
}

func TestRuntime_ExecutorError(t *testing.T) {
	rt := NewVerifierRuntime()
	rt.Register(&failingExecutor{})
	checks := []state.VerificationCheck{
		{
			CheckID: "f1", Type: state.VerificationCheckType("always_fail"),
			Description: "will error", Required: true,
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	result := rt.EvaluateAll(context.Background(), task, TaskOutputs{}, "test", 100)
	if result.Passed {
		t.Fatal("expected fail on executor error")
	}
	// Check should be marked as error.
	for _, c := range result.UpdatedSpec.Checks {
		if c.CheckID == "f1" && c.Status != state.VerificationStatusError {
			t.Fatalf("expected error status, got %s", c.Status)
		}
	}
}

// ── Multi-check mixed results ───────────────────────────────────────────────

func TestRuntime_MixedChecks(t *testing.T) {
	rt := DefaultVerifierRuntime()
	checks := []state.VerificationCheck{
		{
			CheckID: "s1", Type: state.VerificationCheckSchema,
			Description: "has fields", Required: true,
			Meta: map[string]any{"required_fields": []any{"name"}},
		},
		{
			CheckID: "e1", Type: state.VerificationCheckEvidence,
			Description: "has artifacts", Required: false, // optional
		},
		{
			CheckID: "r1", Type: state.VerificationCheckReview,
			Description: "needs review", Required: true, // no executor = stays pending
		},
	}
	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		StructuredOutput: map[string]any{"name": "Alice"},
	}
	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	// s1 passes, e1 fails (no artifacts, but optional), r1 pending (required) → blocks
	if result.Passed {
		t.Fatal("expected fail because r1 (required review) is pending")
	}
}

// ── Formatting ──────────────────────────────────────────────────────────────

func TestFormatRuntimeResult(t *testing.T) {
	result := RuntimeResult{
		Passed:   true,
		Summary:  "2/2 passed, 0 failed, 0 pending",
		Duration: 50 * time.Millisecond,
		CheckResults: []CheckResult{
			{CheckID: "s1", Type: state.VerificationCheckSchema, Outcome: CheckOutcome{Passed: true, Result: "ok"}, Duration: 10 * time.Millisecond},
			{CheckID: "e1", Type: state.VerificationCheckEvidence, Outcome: CheckOutcome{Passed: true, Result: "ok"}, Duration: 5 * time.Millisecond},
		},
	}
	s := FormatRuntimeResult(result)
	if s == "" {
		t.Fatal("expected non-empty format")
	}
}

// ── JSON round-trips ────────────────────────────────────────────────────────

func TestCheckOutcome_JSON(t *testing.T) {
	o := CheckOutcome{
		Passed: true, Result: "all good",
		Evidence: "proof", Details: map[string]any{"count": 5},
	}
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var o2 CheckOutcome
	if err := json.Unmarshal(data, &o2); err != nil {
		t.Fatal(err)
	}
	if !o2.Passed || o2.Result != "all good" {
		t.Fatalf("round-trip mismatch: %+v", o2)
	}
}

func TestRuntimeResult_JSON(t *testing.T) {
	r := RuntimeResult{
		Passed:   false,
		Summary:  "1/2 passed",
		Duration: 100 * time.Millisecond,
		CheckResults: []CheckResult{
			{CheckID: "s1", Outcome: CheckOutcome{Passed: true}},
			{CheckID: "e1", Outcome: CheckOutcome{Passed: false}},
		},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var r2 RuntimeResult
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatal(err)
	}
	if r2.Passed || len(r2.CheckResults) != 2 {
		t.Fatalf("round-trip mismatch: %+v", r2)
	}
}

func TestTaskOutputs_JSON(t *testing.T) {
	o := TaskOutputs{
		RawOutput:        "hello",
		StructuredOutput: map[string]any{"key": "val"},
		Artifacts:        []TaskArtifact{{Name: "report.md", Content: "# Report"}},
		ToolResults: map[string]ToolCallResult{
			"c1": {ToolName: "fetch", Output: "ok"},
		},
	}
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var o2 TaskOutputs
	if err := json.Unmarshal(data, &o2); err != nil {
		t.Fatal(err)
	}
	if o2.RawOutput != "hello" || len(o2.Artifacts) != 1 {
		t.Fatalf("round-trip mismatch: %+v", o2)
	}
}

// ── End-to-end ──────────────────────────────────────────────────────────────

func TestEndToEnd_FullVerificationPipeline(t *testing.T) {
	rt := DefaultVerifierRuntime()

	checks := []state.VerificationCheck{
		{
			CheckID: "schema-1", Type: state.VerificationCheckSchema,
			Description: "output has name and status", Required: true,
			Meta: map[string]any{"required_fields": []any{"name", "status"}},
		},
		{
			CheckID: "evidence-1", Type: state.VerificationCheckEvidence,
			Description: "report artifact present", Required: true,
			Meta: map[string]any{"required_artifacts": []any{"report.md"}},
		},
		{
			CheckID: "tool-1", Type: state.VerificationCheckTest,
			Description: "all tools succeeded", Required: true,
		},
		{
			CheckID: "dry-run-1", Type: state.VerificationCheckCustom,
			Description: "dry-run publish ok", Required: false,
			Meta: map[string]any{"evaluator": "dry_run", "tool": "nostr_publish"},
		},
	}

	task := makeTask(checks, state.VerificationPolicyRequired)
	outputs := TaskOutputs{
		RawOutput: "Task completed successfully",
		StructuredOutput: map[string]any{
			"name":   "analysis",
			"status": "complete",
			"extra":  42,
		},
		Artifacts: []TaskArtifact{
			{Name: "report.md", Type: "file", Content: "# Analysis Report\n\nFindings..."},
		},
		ToolResults: map[string]ToolCallResult{
			"c1": {ToolName: "nostr_fetch", Output: "fetched 10 events"},
			"c2": {ToolName: "nostr_publish", Output: "published event abc123"},
		},
	}

	result := rt.EvaluateAll(context.Background(), task, outputs, "verifier-agent", 12345)

	if !result.Passed {
		t.Fatalf("expected full pipeline to pass, got: %s", result.Summary)
	}

	if len(result.CheckResults) != 4 {
		t.Fatalf("expected 4 check results, got %d", len(result.CheckResults))
	}

	// Verify all checks passed.
	for _, cr := range result.CheckResults {
		if !cr.Outcome.Passed {
			t.Errorf("check %s failed: %s", cr.CheckID, cr.Outcome.Result)
		}
	}

	// Verify spec was updated.
	if result.UpdatedSpec.VerifiedAt != 12345 {
		t.Errorf("expected VerifiedAt=12345, got %d", result.UpdatedSpec.VerifiedAt)
	}
	if result.UpdatedSpec.VerifiedBy != "verifier-agent" {
		t.Errorf("expected VerifiedBy=verifier-agent, got %s", result.UpdatedSpec.VerifiedBy)
	}

	// Format output should work.
	s := FormatRuntimeResult(result)
	if s == "" {
		t.Fatal("expected non-empty formatted result")
	}
}

func TestEndToEnd_PartialFailure_BlocksCompletion(t *testing.T) {
	rt := DefaultVerifierRuntime()

	checks := []state.VerificationCheck{
		{
			CheckID: "s1", Type: state.VerificationCheckSchema,
			Description: "has required fields", Required: true,
			Meta: map[string]any{"required_fields": []any{"result"}},
		},
		{
			CheckID: "e1", Type: state.VerificationCheckEvidence,
			Description: "has artifacts", Required: true,
			Meta: map[string]any{"required_artifacts": []any{"proof.txt"}},
		},
	}

	task := makeTask(checks, state.VerificationPolicyRequired)
	// Schema passes but evidence fails.
	outputs := TaskOutputs{
		StructuredOutput: map[string]any{"result": "ok"},
		Artifacts:        nil, // missing proof.txt
	}

	result := rt.EvaluateAll(context.Background(), task, outputs, "test", 100)
	if result.Passed {
		t.Fatal("expected fail — evidence check should block")
	}

	// Verify specific check outcomes.
	for _, cr := range result.CheckResults {
		switch cr.CheckID {
		case "s1":
			if !cr.Outcome.Passed {
				t.Fatal("schema check should pass")
			}
		case "e1":
			if cr.Outcome.Passed {
				t.Fatal("evidence check should fail")
			}
		}
	}
}
