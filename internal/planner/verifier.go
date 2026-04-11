// verifier.go enforces verification contracts on task completion.
// It evaluates VerificationSpec checks, records results, and gates
// terminal task status transitions based on verification policy.
package planner

import (
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// VerificationResult summarizes the outcome of evaluating a task's verification spec.
type VerificationResult struct {
	// Passed is true when the task may proceed to completed status.
	Passed bool `json:"passed"`

	// Blocking lists required checks that prevent completion.
	Blocking []string `json:"blocking,omitempty"`

	// Summary is a human-readable explanation.
	Summary string `json:"summary"`

	// UpdatedSpec is the verification spec with check statuses updated.
	UpdatedSpec state.VerificationSpec `json:"updated_spec"`
}

// CheckEvaluator is called for each check during evaluation. It receives the
// check and task, and returns (passed bool, result string, evidence string, err).
// If nil, checks remain in their current status (manual evaluation assumed).
type CheckEvaluator func(check state.VerificationCheck, task state.TaskSpec) (passed bool, result string, evidence string, err error)

// Verifier evaluates verification specs and gates task completion.
type Verifier struct {
	evaluator CheckEvaluator
}

// NewVerifier creates a verifier. If evaluator is nil, checks must be
// resolved externally (e.g. by a reviewer agent or operator).
func NewVerifier(evaluator CheckEvaluator) *Verifier {
	return &Verifier{evaluator: evaluator}
}

// Evaluate runs all pending checks in a task's verification spec and returns
// the aggregate result. Checks already in a terminal status are not re-evaluated.
func (v *Verifier) Evaluate(task state.TaskSpec, actor string, now int64) VerificationResult {
	spec := task.Verification.Normalize()

	if now <= 0 {
		now = time.Now().Unix()
	}

	if spec.Policy == state.VerificationPolicyNone || len(spec.Checks) == 0 {
		return VerificationResult{
			Passed:      true,
			Summary:     "no verification required",
			UpdatedSpec: spec,
		}
	}

	for i, check := range spec.Checks {
		if check.Status.IsTerminal() {
			continue // already evaluated
		}
		if v.evaluator == nil {
			continue // no auto-evaluator; leave as pending
		}

		passed, result, evidence, err := v.evaluator(check, task)
		if err != nil {
			spec.Checks[i].Status = state.VerificationStatusError
			spec.Checks[i].Result = fmt.Sprintf("evaluator error: %s", err)
		} else if passed {
			spec.Checks[i].Status = state.VerificationStatusPassed
			spec.Checks[i].Result = result
		} else {
			spec.Checks[i].Status = state.VerificationStatusFailed
			spec.Checks[i].Result = result
		}
		spec.Checks[i].Evidence = evidence
		spec.Checks[i].EvaluatedAt = now
		spec.Checks[i].EvaluatedBy = actor
	}

	result := v.summarize(spec)
	// Set overall verification timestamp when all checks are terminal.
	if result.Passed && spec.VerifiedAt == 0 {
		result.UpdatedSpec.VerifiedAt = now
		result.UpdatedSpec.VerifiedBy = actor
	}
	return result
}

// MayComplete reports whether a task is allowed to transition to completed
// status given its current verification state. This is the gate function.
func (v *Verifier) MayComplete(task state.TaskSpec) VerificationResult {
	spec := task.Verification.Normalize()

	if spec.Policy == state.VerificationPolicyNone || len(spec.Checks) == 0 {
		return VerificationResult{
			Passed:      true,
			Summary:     "no verification required",
			UpdatedSpec: spec,
		}
	}

	return v.summarize(spec)
}

// RecordCheckResult manually records the outcome of a single check
// (e.g. from a reviewer agent or operator). Returns the updated spec.
func RecordCheckResult(spec state.VerificationSpec, checkID string, passed bool, result, evidence, actor string, now int64) (state.VerificationSpec, error) {
	spec = spec.Normalize()
	if strings.TrimSpace(checkID) == "" {
		return spec, fmt.Errorf("check_id is required")
	}
	if now <= 0 {
		now = time.Now().Unix()
	}

	found := false
	for i, check := range spec.Checks {
		if check.CheckID == checkID {
			found = true
			if passed {
				spec.Checks[i].Status = state.VerificationStatusPassed
			} else {
				spec.Checks[i].Status = state.VerificationStatusFailed
			}
			spec.Checks[i].Result = result
			spec.Checks[i].Evidence = evidence
			spec.Checks[i].EvaluatedAt = now
			spec.Checks[i].EvaluatedBy = actor
			break
		}
	}
	if !found {
		return spec, fmt.Errorf("check_id %q not found in verification spec", checkID)
	}
	return spec, nil
}

// BuildFromAcceptanceCriteria creates a VerificationSpec from a task's
// acceptance criteria, converting each criterion into a verification check.
func BuildFromAcceptanceCriteria(criteria []state.TaskAcceptanceCriterion, policy state.VerificationPolicy) state.VerificationSpec {
	if len(criteria) == 0 {
		return state.VerificationSpec{Policy: state.VerificationPolicyNone}
	}

	checks := make([]state.VerificationCheck, len(criteria))
	for i, c := range criteria {
		checkType := state.VerificationCheckReview
		if c.Type != "" {
			checkType = state.VerificationCheckType(c.Type)
		}
		checks[i] = state.VerificationCheck{
			CheckID:     fmt.Sprintf("ac-%d", i+1),
			Type:        checkType,
			Description: c.Description,
			Required:    c.Required,
			Status:      state.VerificationStatusPending,
		}
	}

	return state.VerificationSpec{
		Policy: policy,
		Checks: checks,
	}
}

// ValidateCompletionGate checks whether a task's verification spec allows
// completion. Returns an error describing what's blocking if it doesn't.
func ValidateCompletionGate(task state.TaskSpec) error {
	spec := task.Verification.Normalize()

	switch spec.Policy {
	case state.VerificationPolicyNone:
		return nil
	case state.VerificationPolicyAdvisory:
		// Advisory: warn but don't block. Return nil.
		return nil
	case state.VerificationPolicyRequired:
		// Must have checks defined for verifiable tasks.
		if len(spec.Checks) == 0 {
			return fmt.Errorf("verification policy is 'required' but no checks are defined")
		}

		var blocking []string
		for _, check := range spec.Checks {
			if !check.Required {
				continue
			}
			switch check.Status {
			case state.VerificationStatusPassed, state.VerificationStatusSkipped:
				// OK
			case state.VerificationStatusPending:
				blocking = append(blocking, fmt.Sprintf("%s: not yet evaluated", check.CheckID))
			case state.VerificationStatusRunning:
				blocking = append(blocking, fmt.Sprintf("%s: still running", check.CheckID))
			case state.VerificationStatusFailed:
				blocking = append(blocking, fmt.Sprintf("%s: failed — %s", check.CheckID, check.Result))
			case state.VerificationStatusError:
				blocking = append(blocking, fmt.Sprintf("%s: error — %s", check.CheckID, check.Result))
			}
		}

		if len(blocking) > 0 {
			return fmt.Errorf("verification blocks completion: %s", strings.Join(blocking, "; "))
		}
		return nil
	default:
		return fmt.Errorf("unknown verification policy %q", spec.Policy)
	}
	return nil
}

// summarize computes the aggregate verification result from check statuses.
func (v *Verifier) summarize(spec state.VerificationSpec) VerificationResult {
	var blocking []string
	var passed, failed, pending, total int

	for _, check := range spec.Checks {
		total++
		switch check.Status {
		case state.VerificationStatusPassed, state.VerificationStatusSkipped:
			passed++
		case state.VerificationStatusFailed, state.VerificationStatusError:
			failed++
			if check.Required {
				blocking = append(blocking, check.CheckID)
			}
		default:
			pending++
			if check.Required {
				blocking = append(blocking, check.CheckID)
			}
		}
	}

	allPassed := len(blocking) == 0
	if spec.Policy == state.VerificationPolicyAdvisory {
		allPassed = true // advisory never blocks
	}

	summary := fmt.Sprintf("%d/%d passed, %d failed, %d pending", passed, total, failed, pending)
	if !allPassed {
		summary += fmt.Sprintf("; blocked by: %s", strings.Join(blocking, ", "))
	}

	return VerificationResult{
		Passed:      allPassed,
		Blocking:    blocking,
		Summary:     summary,
		UpdatedSpec: spec,
	}
}
