package state

import (
	"fmt"
	"strings"
)

type VerificationStatus string

const (
	VerificationStatusPending VerificationStatus = "pending"
	VerificationStatusRunning VerificationStatus = "running"
	VerificationStatusPassed  VerificationStatus = "passed"
	VerificationStatusFailed  VerificationStatus = "failed"
	VerificationStatusSkipped VerificationStatus = "skipped"
	VerificationStatusError   VerificationStatus = "error"
)

func ParseVerificationStatus(raw string) (VerificationStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(VerificationStatusPending), "":
		return VerificationStatusPending, true
	case string(VerificationStatusRunning):
		return VerificationStatusRunning, true
	case string(VerificationStatusPassed):
		return VerificationStatusPassed, true
	case string(VerificationStatusFailed):
		return VerificationStatusFailed, true
	case string(VerificationStatusSkipped):
		return VerificationStatusSkipped, true
	case string(VerificationStatusError):
		return VerificationStatusError, true
	default:
		return "", false
	}
}

func NormalizeVerificationStatus(raw string) VerificationStatus {
	s, ok := ParseVerificationStatus(raw)
	if !ok {
		return VerificationStatusPending
	}
	return s
}

func (s VerificationStatus) Valid() bool {
	_, ok := ParseVerificationStatus(string(s))
	return ok
}

// IsTerminal reports whether the status represents a final verification outcome.
func (s VerificationStatus) IsTerminal() bool {
	switch s {
	case VerificationStatusPassed, VerificationStatusFailed, VerificationStatusSkipped, VerificationStatusError:
		return true
	}
	return false
}

// VerificationCheckType classifies verification strategies.
type VerificationCheckType string

const (
	VerificationCheckSchema   VerificationCheckType = "schema"   // JSON schema validation
	VerificationCheckEvidence VerificationCheckType = "evidence" // evidence artifact present
	VerificationCheckCustom   VerificationCheckType = "custom"   // custom evaluator
	VerificationCheckReview   VerificationCheckType = "review"   // human/agent review
	VerificationCheckTest     VerificationCheckType = "test"     // automated test pass
)

// VerificationCheck describes a single verification rule.
type VerificationCheck struct {
	CheckID     string                `json:"check_id"`
	Type        VerificationCheckType `json:"type"`
	Description string                `json:"description"`
	Required    bool                  `json:"required"`
	Status      VerificationStatus    `json:"status"`
	Result      string                `json:"result,omitempty"`
	Evidence    string                `json:"evidence,omitempty"`
	EvaluatedAt int64                 `json:"evaluated_at,omitempty"`
	EvaluatedBy string                `json:"evaluated_by,omitempty"`
	Meta        map[string]any        `json:"meta,omitempty"`
}

func (c VerificationCheck) Validate() error {
	if strings.TrimSpace(c.CheckID) == "" {
		return fmt.Errorf("check_id is required")
	}
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("check description is required")
	}
	if raw := strings.TrimSpace(string(c.Status)); raw != "" && !c.Status.Valid() {
		return fmt.Errorf("invalid check status %q", c.Status)
	}
	return nil
}

func (c VerificationCheck) Normalize() VerificationCheck {
	c.Status = NormalizeVerificationStatus(string(c.Status))
	return c
}

// VerificationPolicy controls how verification gates task completion.
type VerificationPolicy string

func ParseVerificationPolicy(raw string) (VerificationPolicy, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(VerificationPolicyRequired):
		return VerificationPolicyRequired, true
	case string(VerificationPolicyAdvisory):
		return VerificationPolicyAdvisory, true
	case string(VerificationPolicyNone), "":
		return VerificationPolicyNone, true
	default:
		return "", false
	}
}

func NormalizeVerificationPolicy(raw string) VerificationPolicy {
	p, ok := ParseVerificationPolicy(raw)
	if !ok {
		return VerificationPolicyNone
	}
	return p
}

func (p VerificationPolicy) Valid() bool {
	_, ok := ParseVerificationPolicy(string(p))
	return ok
}

// VerificationSpec is the complete verification contract for a task or run.
type VerificationSpec struct {
	Policy     VerificationPolicy  `json:"policy"`
	Checks     []VerificationCheck `json:"checks,omitempty"`
	VerifiedAt int64               `json:"verified_at,omitempty"`
	VerifiedBy string              `json:"verified_by,omitempty"`
	Meta       map[string]any      `json:"meta,omitempty"`
}

func (v VerificationSpec) Normalize() VerificationSpec {
	v.Policy = NormalizeVerificationPolicy(string(v.Policy))
	for i := range v.Checks {
		v.Checks[i] = v.Checks[i].Normalize()
	}
	return v
}

func (v VerificationSpec) Validate() error {
	if raw := strings.TrimSpace(string(v.Policy)); raw != "" && !v.Policy.Valid() {
		return fmt.Errorf("invalid verification policy %q", v.Policy)
	}
	if v.Policy == VerificationPolicyRequired && len(v.Checks) == 0 {
		return fmt.Errorf("verification policy is 'required' but no checks are defined")
	}
	checkIDs := make(map[string]bool, len(v.Checks))
	for i, check := range v.Checks {
		if err := check.Validate(); err != nil {
			return fmt.Errorf("checks[%d]: %w", i, err)
		}
		if checkIDs[check.CheckID] {
			return fmt.Errorf("duplicate check_id %q at checks[%d]", check.CheckID, i)
		}
		checkIDs[check.CheckID] = true
	}
	return nil
}

// RequiredChecks returns only the checks marked as required.
func (v VerificationSpec) RequiredChecks() []VerificationCheck {
	var out []VerificationCheck
	for _, c := range v.Checks {
		if c.Required {
			out = append(out, c)
		}
	}
	return out
}

// AllRequiredPassed reports whether all required checks have passed or been skipped.
func (v VerificationSpec) AllRequiredPassed() bool {
	for _, c := range v.Checks {
		if !c.Required {
			continue
		}
		if c.Status != VerificationStatusPassed && c.Status != VerificationStatusSkipped {
			return false
		}
	}
	return true
}

// AnyRequiredFailed reports whether any required check has failed.
func (v VerificationSpec) AnyRequiredFailed() bool {
	for _, c := range v.Checks {
		if c.Required && (c.Status == VerificationStatusFailed || c.Status == VerificationStatusError) {
			return true
		}
	}
	return false
}

// PendingChecks returns checks that haven't been evaluated yet.
func (v VerificationSpec) PendingChecks() []VerificationCheck {
	var out []VerificationCheck
	for _, c := range v.Checks {
		if c.Status == VerificationStatusPending {
			out = append(out, c)
		}
	}
	return out
}

// TaskResultRef points at a durable result, artifact, or event produced by a task run.
