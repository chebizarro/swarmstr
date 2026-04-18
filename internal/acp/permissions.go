package acp

import "fmt"

// ── Permission modes ──────────���─────────────────────────────────────────────

// PermissionMode controls what operations ACP agents are allowed to perform.
type PermissionMode string

const (
	// PermissionApproveAll allows all operations without approval.
	PermissionApproveAll PermissionMode = "approve-all"
	// PermissionApproveReads allows read operations but blocks writes/executes.
	PermissionApproveReads PermissionMode = "approve-reads"
	// PermissionDenyAll denies all operations.
	PermissionDenyAll PermissionMode = "deny-all"
)

// DefaultPermissionMode is the default mode when none is specified.
const DefaultPermissionMode = PermissionApproveReads

// ValidPermissionModes is the set of valid permission modes.
var ValidPermissionModes = []PermissionMode{
	PermissionApproveAll,
	PermissionApproveReads,
	PermissionDenyAll,
}

// IsValid reports whether the mode is a recognized permission mode.
func (m PermissionMode) IsValid() bool {
	for _, v := range ValidPermissionModes {
		if m == v {
			return true
		}
	}
	return false
}

// ── Non-interactive policies ────���───────────────────────────────────────────

// NonInteractivePolicy controls behaviour when interactive approval is unavailable.
type NonInteractivePolicy string

const (
	// PolicyDeny silently denies the operation.
	PolicyDeny NonInteractivePolicy = "deny"
	// PolicyFail returns an error to the caller.
	PolicyFail NonInteractivePolicy = "fail"
)

// DefaultNonInteractivePolicy is the default policy when none is specified.
const DefaultNonInteractivePolicy = PolicyFail

// ValidNonInteractivePolicies is the set of valid non-interactive policies.
var ValidNonInteractivePolicies = []NonInteractivePolicy{PolicyDeny, PolicyFail}

// IsValid reports whether the policy is a recognized non-interactive policy.
func (p NonInteractivePolicy) IsValid() bool {
	for _, v := range ValidNonInteractivePolicies {
		if p == v {
			return true
		}
	}
	return false
}

// ── Operation kinds ─────────────────────────────────────────────────────────

// OperationKind classifies an ACP operation for permission evaluation.
type OperationKind string

const (
	// OperationRead covers read-only operations (file reads, queries).
	OperationRead OperationKind = "read"
	// OperationWrite covers state-mutating operations (file writes, edits).
	OperationWrite OperationKind = "write"
	// OperationExecute covers command execution (shell commands, tool calls).
	OperationExecute OperationKind = "execute"
)

// ── Permission decision ─────────────────────────────────────────────────────

// PermissionDecision is the result of a permission check.
type PermissionDecision struct {
	// Allowed is true if the operation is permitted.
	Allowed bool
	// Reason explains the decision.
	Reason string
}

// ── Evaluation ────────���─────────────────────────────────────────────────────

// CheckPermission evaluates whether an operation is allowed under the given mode.
//
// Decision table:
//
//	approve-all:   read→allowed, write→allowed, execute→allowed
//	approve-reads: read→allowed, write→denied,  execute→denied
//	deny-all:      read→denied,  write→denied,  execute→denied
func CheckPermission(mode PermissionMode, op OperationKind) PermissionDecision {
	switch mode {
	case PermissionApproveAll:
		return PermissionDecision{Allowed: true, Reason: "approve-all permits all operations"}
	case PermissionApproveReads:
		if op == OperationRead {
			return PermissionDecision{Allowed: true, Reason: "approve-reads permits read operations"}
		}
		return PermissionDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("approve-reads denies %s operations", op),
		}
	case PermissionDenyAll:
		return PermissionDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("deny-all denies %s operations", op),
		}
	default:
		return PermissionDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("unknown permission mode %q denies %s", mode, op),
		}
	}
}

// ApplyNonInteractivePolicy returns a decision when interactive approval is
// unavailable. With PolicyDeny the operation is silently denied; with PolicyFail
// a non-nil error is also returned.
func ApplyNonInteractivePolicy(policy NonInteractivePolicy, op OperationKind) (PermissionDecision, error) {
	decision := PermissionDecision{
		Allowed: false,
		Reason:  fmt.Sprintf("non-interactive policy %q denies %s", policy, op),
	}
	switch policy {
	case PolicyDeny:
		return decision, nil
	case PolicyFail:
		return decision, fmt.Errorf("acp: non-interactive policy %q: %s operation denied", policy, op)
	default:
		return decision, fmt.Errorf("acp: unknown non-interactive policy %q", policy)
	}
}
