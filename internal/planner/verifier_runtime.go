// verifier_runtime.go implements a generic verifier runtime that dispatches
// verification checks to type-specific executors (schema, evidence, review,
// dry-run, tool-output, custom). Executors are registered by
// VerificationCheckType and pending checks with registered executors are invoked
// concurrently while preserving deterministic result ordering.
package planner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Executor contract ───────────────────────────────────────────────────────

// CheckExecutor evaluates a single verification check against a task.
// Implementations are type-specific (schema validation, evidence lookup, etc.).
type CheckExecutor interface {
	// Type returns the VerificationCheckType this executor handles.
	Type() state.VerificationCheckType

	// Execute runs the check and returns a structured result.
	Execute(ctx context.Context, check state.VerificationCheck, task state.TaskSpec, outputs TaskOutputs) (CheckOutcome, error)
}

// TaskOutputs provides the task's outputs/artifacts for verification.
type TaskOutputs struct {
	// RawOutput is the final text output of the task.
	RawOutput string `json:"raw_output,omitempty"`
	// StructuredOutput is parsed JSON output (if applicable).
	StructuredOutput map[string]any `json:"structured_output,omitempty"`
	// Artifacts lists file/reference artifacts produced.
	Artifacts []TaskArtifact `json:"artifacts,omitempty"`
	// ToolResults maps tool call IDs to their outputs.
	ToolResults map[string]ToolCallResult `json:"tool_results,omitempty"`
}

// TaskArtifact is a named output artifact.
type TaskArtifact struct {
	Name    string         `json:"name"`
	Type    string         `json:"type,omitempty"` // "file", "reference", "data"
	Content string         `json:"content,omitempty"`
	Ref     string         `json:"ref,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// ToolCallResult captures a tool invocation's output for post-tool verification.
type ToolCallResult struct {
	ToolName string         `json:"tool_name"`
	Input    map[string]any `json:"input,omitempty"`
	Output   string         `json:"output,omitempty"`
	Error    string         `json:"error,omitempty"`
	Duration time.Duration  `json:"duration,omitempty"`
}

// CheckOutcome is the structured result of executing a single check.
type CheckOutcome struct {
	Passed   bool           `json:"passed"`
	Result   string         `json:"result"`
	Evidence string         `json:"evidence,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

// ── Verifier runtime ────────────────────────────────────────────────────────

// VerifierRuntime dispatches checks to registered executors.
type VerifierRuntime struct {
	mu        sync.RWMutex
	executors map[state.VerificationCheckType]CheckExecutor
}

// NewVerifierRuntime creates an empty runtime. Register executors before use.
func NewVerifierRuntime() *VerifierRuntime {
	return &VerifierRuntime{
		executors: make(map[state.VerificationCheckType]CheckExecutor),
	}
}

// Register adds an executor for a check type. Overwrites any previous.
func (r *VerifierRuntime) Register(executor CheckExecutor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[executor.Type()] = executor
}

// HasExecutor reports whether an executor is registered for the given type.
func (r *VerifierRuntime) HasExecutor(t state.VerificationCheckType) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.executors[t]
	return ok
}

// ── Runtime evaluation ──────────────────────────────────────────────────────

// RuntimeResult is the output of a full verification pass.
type RuntimeResult struct {
	Passed       bool                   `json:"passed"`
	CheckResults []CheckResult          `json:"check_results"`
	UpdatedSpec  state.VerificationSpec `json:"updated_spec"`
	Summary      string                 `json:"summary"`
	Duration     time.Duration          `json:"duration"`
}

// CheckResult pairs a check with its execution outcome.
type CheckResult struct {
	CheckID  string                      `json:"check_id"`
	Type     state.VerificationCheckType `json:"type"`
	Required bool                        `json:"required"`
	Outcome  CheckOutcome                `json:"outcome"`
	Error    string                      `json:"error,omitempty"`
	Pending  bool                        `json:"pending,omitempty"` // true when no executor was available
	Duration time.Duration               `json:"duration,omitempty"`
}

// EvaluateAll runs all pending checks in the spec using registered executors.
// Checks with no registered executor are left pending (for manual evaluation).
// Checks already in a terminal status are skipped.
func (r *VerifierRuntime) EvaluateAll(ctx context.Context, task state.TaskSpec, outputs TaskOutputs, actor string, now int64) RuntimeResult {
	start := time.Now()
	spec := task.Verification.Normalize()
	if len(spec.Checks) > 0 {
		spec.Checks = append([]state.VerificationCheck(nil), spec.Checks...)
	}

	if now <= 0 {
		now = time.Now().Unix()
	}

	if spec.Policy == state.VerificationPolicyNone || len(spec.Checks) == 0 {
		spec.VerifiedAt = 0
		spec.VerifiedBy = ""
		return RuntimeResult{
			Passed:      true,
			UpdatedSpec: spec,
			Summary:     "no verification required",
			Duration:    time.Since(start),
		}
	}

	r.mu.RLock()
	executors := make(map[state.VerificationCheckType]CheckExecutor, len(r.executors))
	for k, v := range r.executors {
		executors[k] = v
	}
	r.mu.RUnlock()

	results := make([]CheckResult, len(spec.Checks))
	type evalResult struct {
		index  int
		check  state.VerificationCheck
		result CheckResult
	}
	evalCh := make(chan evalResult, len(spec.Checks))
	var wg sync.WaitGroup

	for i, check := range spec.Checks {
		if check.Status.IsTerminal() {
			cr := CheckResult{
				CheckID:  check.CheckID,
				Type:     check.Type,
				Required: check.Required,
				Outcome: CheckOutcome{
					Passed:   check.Status == state.VerificationStatusPassed || check.Status == state.VerificationStatusSkipped,
					Result:   check.Result,
					Evidence: check.Evidence,
				},
			}
			if check.Status == state.VerificationStatusError {
				cr.Error = check.Result
				if cr.Error == "" {
					cr.Error = "verification error"
				}
			}
			results[i] = cr
			continue
		}

		executor, ok := executors[check.Type]
		if !ok {
			// No executor for this type — leave pending for manual evaluation.
			if spec.Checks[i].Status == state.VerificationStatusRunning {
				spec.Checks[i].Status = state.VerificationStatusPending
			}
			results[i] = CheckResult{
				CheckID:  check.CheckID,
				Type:     check.Type,
				Required: check.Required,
				Pending:  true,
			}
			continue
		}

		wg.Add(1)
		go func(index int, check state.VerificationCheck, executor CheckExecutor) {
			defer wg.Done()
			execCheck := cloneVerificationCheck(check)
			execTask := cloneTaskSpecForVerification(task)
			execOutputs := cloneTaskOutputs(outputs)
			checkStart := time.Now()
			outcome, err := executor.Execute(ctx, execCheck, execTask, execOutputs)
			checkDuration := time.Since(checkStart)

			updated := check
			cr := CheckResult{
				CheckID:  check.CheckID,
				Type:     check.Type,
				Required: check.Required,
				Duration: checkDuration,
			}

			if err != nil {
				cr.Error = err.Error()
				cr.Outcome = CheckOutcome{Passed: false, Result: fmt.Sprintf("executor error: %s", err)}
				updated.Status = state.VerificationStatusError
				updated.Result = cr.Outcome.Result
				updated.Evidence = ""
			} else {
				cr.Outcome = outcome
				if outcome.Passed {
					updated.Status = state.VerificationStatusPassed
				} else {
					updated.Status = state.VerificationStatusFailed
				}
				updated.Result = outcome.Result
				updated.Evidence = outcome.Evidence
			}
			updated.EvaluatedAt = now
			updated.EvaluatedBy = actor
			evalCh <- evalResult{index: index, check: updated, result: cr}
		}(i, check, executor)
	}

	wg.Wait()
	close(evalCh)
	for er := range evalCh {
		spec.Checks[er.index] = er.check
		results[er.index] = er.result
	}

	// Compute aggregate pass/fail.
	passed := true
	var blocking []string
	var passCount, failCount, pendingCount int

	for _, cr := range results {
		switch {
		case cr.Pending:
			// No executor available — check remains pending for manual evaluation.
			pendingCount++
			if cr.Required {
				passed = false
				blocking = append(blocking, cr.CheckID)
			}
		case cr.Error != "":
			// Executor returned an error.
			failCount++
			if cr.Required {
				passed = false
				blocking = append(blocking, cr.CheckID)
			}
		case cr.Outcome.Passed:
			passCount++
		default:
			// Executor ran but check did not pass.
			failCount++
			if cr.Required {
				passed = false
				blocking = append(blocking, cr.CheckID)
			}
		}
	}

	if spec.Policy == state.VerificationPolicyAdvisory {
		passed = true
	}

	summary := fmt.Sprintf("%d/%d passed, %d failed, %d pending",
		passCount, len(results), failCount, pendingCount)
	if !passed {
		summary += fmt.Sprintf("; blocked by: %s", strings.Join(blocking, ", "))
	}

	if spec.AllRequiredPassed() {
		spec.VerifiedAt = now
		spec.VerifiedBy = actor
	} else {
		spec.VerifiedAt = 0
		spec.VerifiedBy = ""
	}

	return RuntimeResult{
		Passed:       passed,
		CheckResults: results,
		UpdatedSpec:  spec,
		Summary:      summary,
		Duration:     time.Since(start),
	}
}

// ── Built-in executors ──────────────────────────────────────────────────────

// SchemaCheckExecutor validates structured output against a JSON schema
// pattern defined in the check's Meta["schema"] field.
type SchemaCheckExecutor struct{}

func (e *SchemaCheckExecutor) Type() state.VerificationCheckType {
	return state.VerificationCheckSchema
}

func (e *SchemaCheckExecutor) Execute(_ context.Context, check state.VerificationCheck, task state.TaskSpec, outputs TaskOutputs) (CheckOutcome, error) {
	// Schema check validates that required fields exist in structured output.
	if outputs.StructuredOutput == nil {
		return CheckOutcome{
			Passed: false,
			Result: "no structured output to validate",
		}, nil
	}

	// Extract required fields from check Meta.
	requiredFields := stringList(check.Meta["required_fields"])
	if len(requiredFields) == 0 {
		// If no specific fields required, check that output is non-empty.
		if len(outputs.StructuredOutput) == 0 {
			return CheckOutcome{
				Passed: false,
				Result: "structured output is empty",
			}, nil
		}
		return CheckOutcome{
			Passed:   true,
			Result:   fmt.Sprintf("structured output has %d fields", len(outputs.StructuredOutput)),
			Evidence: fmt.Sprintf("fields: %s", mapKeys(outputs.StructuredOutput)),
		}, nil
	}

	var missing []string
	for _, fieldName := range requiredFields {
		if _, exists := outputs.StructuredOutput[fieldName]; !exists {
			missing = append(missing, fieldName)
		}
	}

	if len(missing) > 0 {
		return CheckOutcome{
			Passed: false,
			Result: fmt.Sprintf("missing required fields: %s", strings.Join(missing, ", ")),
			Details: map[string]any{
				"missing_fields": missing,
				"total_fields":   len(outputs.StructuredOutput),
			},
		}, nil
	}

	return CheckOutcome{
		Passed:   true,
		Result:   fmt.Sprintf("all %d required fields present", len(requiredFields)),
		Evidence: fmt.Sprintf("fields: %s", mapKeys(outputs.StructuredOutput)),
	}, nil
}

// EvidenceCheckExecutor validates that the task output contains references
// to expected evidence artifacts or citations.
type EvidenceCheckExecutor struct{}

func (e *EvidenceCheckExecutor) Type() state.VerificationCheckType {
	return state.VerificationCheckEvidence
}

func (e *EvidenceCheckExecutor) Execute(_ context.Context, check state.VerificationCheck, _ state.TaskSpec, outputs TaskOutputs) (CheckOutcome, error) {
	// Evidence check: verify that required artifacts exist.
	requiredArtifacts := stringList(check.Meta["required_artifacts"])

	if len(requiredArtifacts) == 0 {
		// No specific artifacts required — just check that some evidence exists.
		if len(outputs.Artifacts) == 0 && outputs.RawOutput == "" {
			return CheckOutcome{
				Passed: false,
				Result: "no output or artifacts to verify",
			}, nil
		}
		return CheckOutcome{
			Passed:   true,
			Result:   fmt.Sprintf("output present with %d artifacts", len(outputs.Artifacts)),
			Evidence: outputs.RawOutput,
		}, nil
	}

	// Check that each required artifact is present.
	artifactNames := make(map[string]bool, len(outputs.Artifacts))
	for _, a := range outputs.Artifacts {
		artifactNames[a.Name] = true
	}

	var missing []string
	for _, name := range requiredArtifacts {
		if !artifactNames[name] {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return CheckOutcome{
			Passed: false,
			Result: fmt.Sprintf("missing required artifacts: %s", strings.Join(missing, ", ")),
			Details: map[string]any{
				"missing_artifacts": missing,
				"present_artifacts": artifactMapKeys(outputs.Artifacts),
			},
		}, nil
	}

	return CheckOutcome{
		Passed:   true,
		Result:   fmt.Sprintf("all %d required artifacts present", len(requiredArtifacts)),
		Evidence: fmt.Sprintf("artifacts: %s", artifactMapKeys(outputs.Artifacts)),
	}, nil
}

// ToolOutputCheckExecutor validates that specific tool calls produced expected outputs.
type ToolOutputCheckExecutor struct{}

func (e *ToolOutputCheckExecutor) Type() state.VerificationCheckType {
	return state.VerificationCheckTest
}

func (e *ToolOutputCheckExecutor) Execute(_ context.Context, check state.VerificationCheck, _ state.TaskSpec, outputs TaskOutputs) (CheckOutcome, error) {
	if len(outputs.ToolResults) == 0 {
		return CheckOutcome{
			Passed: false,
			Result: "no tool results to verify",
		}, nil
	}

	// Check for required tool calls.
	requiredTools := stringList(check.Meta["required_tools"])
	if len(requiredTools) == 0 {
		// No specific tools required — check that all tools succeeded.
		var failures []string
		for callID, result := range outputs.ToolResults {
			if result.Error != "" {
				failures = append(failures, fmt.Sprintf("%s(%s): %s", result.ToolName, callID, result.Error))
			}
		}
		sort.Strings(failures)
		if len(failures) > 0 {
			return CheckOutcome{
				Passed:  false,
				Result:  fmt.Sprintf("%d tool calls failed", len(failures)),
				Details: map[string]any{"failures": failures},
			}, nil
		}
		return CheckOutcome{
			Passed: true,
			Result: fmt.Sprintf("all %d tool calls succeeded", len(outputs.ToolResults)),
		}, nil
	}

	// Verify required tools were called and that at least one call per required
	// tool succeeded. A failed call to a required tool cannot satisfy the check.
	calledTools := make(map[string]bool)
	succeededTools := make(map[string]bool)
	for _, r := range outputs.ToolResults {
		calledTools[r.ToolName] = true
		if r.Error == "" {
			succeededTools[r.ToolName] = true
		}
	}

	var missing, failed []string
	for _, name := range requiredTools {
		if !calledTools[name] {
			missing = append(missing, name)
			continue
		}
		if !succeededTools[name] {
			failed = append(failed, name)
		}
	}

	if len(missing) > 0 || len(failed) > 0 {
		details := make(map[string]any)
		var parts []string
		if len(missing) > 0 {
			details["missing_tools"] = missing
			parts = append(parts, fmt.Sprintf("not called: %s", strings.Join(missing, ", ")))
		}
		if len(failed) > 0 {
			details["failed_tools"] = failed
			parts = append(parts, fmt.Sprintf("called but failed: %s", strings.Join(failed, ", ")))
		}
		return CheckOutcome{
			Passed:  false,
			Result:  fmt.Sprintf("required tools did not succeed (%s)", strings.Join(parts, "; ")),
			Details: details,
		}, nil
	}

	return CheckOutcome{
		Passed: true,
		Result: fmt.Sprintf("all %d required tools succeeded", len(requiredTools)),
	}, nil
}

// MetadataReviewCheckExecutor evaluates explicit human/agent review decisions
// recorded in check metadata. It never infers approval from ordinary task
// output: a review check passes only when Meta["approved"] or Meta["accepted"]
// is true. Use NewReviewCheckExecutor in reviewer.go when live review pipeline
// execution is required instead.
type MetadataReviewCheckExecutor struct{}

func (e *MetadataReviewCheckExecutor) Type() state.VerificationCheckType {
	return state.VerificationCheckReview
}

func (e *MetadataReviewCheckExecutor) Execute(_ context.Context, check state.VerificationCheck, _ state.TaskSpec, _ TaskOutputs) (CheckOutcome, error) {
	approved, ok := boolMeta(check.Meta, "approved")
	if !ok {
		approved, ok = boolMeta(check.Meta, "accepted")
	}
	reviewer, _ := check.Meta["reviewer"].(string)
	if reviewer == "" {
		reviewer, _ = check.Meta["reviewed_by"].(string)
	}
	comment, _ := check.Meta["comment"].(string)
	if comment == "" {
		comment, _ = check.Meta["reason"].(string)
	}

	if !ok {
		return CheckOutcome{
			Passed: false,
			Result: "review check requires explicit approved=true metadata",
		}, nil
	}
	if !approved {
		return CheckOutcome{
			Passed:   false,
			Result:   "review was not approved",
			Evidence: comment,
			Details:  map[string]any{"reviewer": reviewer},
		}, nil
	}

	return CheckOutcome{
		Passed:   true,
		Result:   "review approved",
		Evidence: comment,
		Details:  map[string]any{"reviewer": reviewer},
	}, nil
}

// DryRunCheckExecutor evaluates whether a side-effectful action can proceed
// by checking dry-run metadata in the check's Meta.
type DryRunCheckExecutor struct{}

func (e *DryRunCheckExecutor) Type() state.VerificationCheckType {
	return state.VerificationCheckCustom
}

func (e *DryRunCheckExecutor) Execute(_ context.Context, check state.VerificationCheck, _ state.TaskSpec, outputs TaskOutputs) (CheckOutcome, error) {
	// Custom checks use Meta["evaluator"] to select behavior.
	evaluator, _ := check.Meta["evaluator"].(string)

	switch evaluator {
	case "dry_run":
		return evaluateDryRun(check, outputs)
	case "output_contains":
		return evaluateOutputContains(check, outputs)
	case "output_not_empty":
		return evaluateOutputNotEmpty(outputs)
	default:
		return CheckOutcome{
			Passed: false,
			Result: fmt.Sprintf("unknown custom evaluator %q", evaluator),
		}, nil
	}
}

func evaluateDryRun(check state.VerificationCheck, outputs TaskOutputs) (CheckOutcome, error) {
	// Dry-run: check that a simulated tool call didn't produce errors.
	dryRunTool, _ := check.Meta["tool"].(string)
	if dryRunTool == "" {
		return CheckOutcome{Passed: false, Result: "dry_run check missing 'tool' in meta"}, nil
	}

	for _, result := range outputs.ToolResults {
		if result.ToolName == dryRunTool {
			if result.Error != "" {
				return CheckOutcome{
					Passed: false,
					Result: fmt.Sprintf("dry-run of %s failed: %s", dryRunTool, result.Error),
				}, nil
			}
			return CheckOutcome{
				Passed:   true,
				Result:   fmt.Sprintf("dry-run of %s succeeded", dryRunTool),
				Evidence: result.Output,
			}, nil
		}
	}

	return CheckOutcome{
		Passed: false,
		Result: fmt.Sprintf("dry-run tool %s was not called", dryRunTool),
	}, nil
}

func evaluateOutputContains(check state.VerificationCheck, outputs TaskOutputs) (CheckOutcome, error) {
	expected, _ := check.Meta["contains"].(string)
	if expected == "" {
		return CheckOutcome{Passed: false, Result: "output_contains check missing 'contains' in meta"}, nil
	}

	if strings.Contains(outputs.RawOutput, expected) {
		return CheckOutcome{
			Passed: true,
			Result: fmt.Sprintf("output contains %q", expected),
		}, nil
	}

	return CheckOutcome{
		Passed: false,
		Result: fmt.Sprintf("output does not contain %q", expected),
	}, nil
}

func evaluateOutputNotEmpty(outputs TaskOutputs) (CheckOutcome, error) {
	if strings.TrimSpace(outputs.RawOutput) != "" || len(outputs.StructuredOutput) > 0 {
		return CheckOutcome{Passed: true, Result: "output is non-empty"}, nil
	}
	return CheckOutcome{Passed: false, Result: "output is empty"}, nil
}

// ── DefaultVerifierRuntime ──────────────────────────────────────────────────

// DefaultVerifierRuntime returns a runtime with all built-in executors registered.
func DefaultVerifierRuntime() *VerifierRuntime {
	rt := NewVerifierRuntime()
	rt.Register(&SchemaCheckExecutor{})
	rt.Register(&EvidenceCheckExecutor{})
	rt.Register(&ToolOutputCheckExecutor{})
	rt.Register(&DryRunCheckExecutor{})
	return rt
}

// ── Formatting ──────────────────────────────────────────────────────────────

// FormatRuntimeResult returns a human-readable verification summary.
func FormatRuntimeResult(r RuntimeResult) string {
	var b strings.Builder
	status := "PASSED"
	if !r.Passed {
		status = "FAILED"
	}
	fmt.Fprintf(&b, "Verification: %s (%s) — %s\n", status, r.Duration.Round(time.Millisecond), r.Summary)
	for _, cr := range r.CheckResults {
		marker := "✓"
		if !cr.Outcome.Passed {
			marker = "✗"
		}
		if cr.Error != "" {
			marker = "⚠"
		}
		fmt.Fprintf(&b, "  %s [%s] %s (%s", marker, cr.Type, cr.CheckID, cr.Outcome.Result)
		if cr.Duration > 0 {
			fmt.Fprintf(&b, ", %s", cr.Duration.Round(time.Millisecond))
		}
		fmt.Fprintf(&b, ")\n")
		if cr.Error != "" {
			fmt.Fprintf(&b, "      error: %s\n", cr.Error)
		}
	}
	return b.String()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func mapKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func artifactMapKeys(artifacts []TaskArtifact) string {
	names := make([]string, len(artifacts))
	for i, a := range artifacts {
		names[i] = a.Name
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func stringList(raw any) []string {
	switch v := raw.(type) {
	case nil:
		return nil
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		return []string{v}
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func cloneTaskSpecForVerification(task state.TaskSpec) state.TaskSpec {
	task.Inputs = cloneAnyMap(task.Inputs)
	task.ExpectedOutputs = append([]state.TaskOutputSpec(nil), task.ExpectedOutputs...)
	task.AcceptanceCriteria = append([]state.TaskAcceptanceCriterion(nil), task.AcceptanceCriteria...)
	task.Dependencies = append([]string(nil), task.Dependencies...)
	task.EnabledTools = append([]string(nil), task.EnabledTools...)
	task.Transitions = append([]state.TaskTransition(nil), task.Transitions...)
	task.Meta = cloneAnyMap(task.Meta)
	task.Authority.AllowedAgents = append([]string(nil), task.Authority.AllowedAgents...)
	task.Authority.AllowedTools = append([]string(nil), task.Authority.AllowedTools...)
	task.Authority.DeniedTools = append([]string(nil), task.Authority.DeniedTools...)
	task.Verification = cloneVerificationSpec(task.Verification)
	return task
}

func cloneTaskOutputs(outputs TaskOutputs) TaskOutputs {
	out := TaskOutputs{
		RawOutput:        outputs.RawOutput,
		StructuredOutput: cloneAnyMap(outputs.StructuredOutput),
	}
	if len(outputs.Artifacts) > 0 {
		out.Artifacts = make([]TaskArtifact, len(outputs.Artifacts))
		for i, artifact := range outputs.Artifacts {
			artifact.Meta = cloneAnyMap(artifact.Meta)
			out.Artifacts[i] = artifact
		}
	}
	if len(outputs.ToolResults) > 0 {
		out.ToolResults = make(map[string]ToolCallResult, len(outputs.ToolResults))
		for id, result := range outputs.ToolResults {
			result.Input = cloneAnyMap(result.Input)
			out.ToolResults[id] = result
		}
	}
	return out
}

func cloneVerificationSpec(spec state.VerificationSpec) state.VerificationSpec {
	spec.Meta = cloneAnyMap(spec.Meta)
	if len(spec.Checks) > 0 {
		checks := spec.Checks
		spec.Checks = make([]state.VerificationCheck, len(checks))
		for i, check := range checks {
			spec.Checks[i] = cloneVerificationCheck(check)
		}
	}
	return spec
}

func cloneVerificationCheck(check state.VerificationCheck) state.VerificationCheck {
	check.Meta = cloneAnyMap(check.Meta)
	return check
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func boolMeta(meta map[string]any, key string) (bool, bool) {
	if len(meta) == 0 {
		return false, false
	}
	raw, ok := meta[key]
	if !ok {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "approved", "accepted", "pass", "passed":
			return true, true
		case "false", "no", "rejected", "denied", "fail", "failed":
			return false, true
		}
	}
	return false, false
}
