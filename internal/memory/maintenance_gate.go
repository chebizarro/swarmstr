package memory

// maintenance_gate.go — confirmation-token gate for destructive memory
// maintenance ops (swarmstr-qc53.4, related swarmstr-s4wl).
//
// Destructive, per-scope operations (resetDreamDiary, resetGroundedShortTerm)
// must not fire on a bare call. The caller first issues the op WITHOUT a
// confirmation token and receives the deterministic token for that
// (operation, scope) pair; it then re-issues the op echoing that token. A
// missing token yields a non-destructive preview; a mismatched token is
// rejected outright. The token binds to the exact scope so a token minted for
// one agent/workspace cannot authorize a reset of another.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// MaintenanceConfirmToken returns the deterministic acknowledgement a caller
// must echo to run a destructive maintenance op against the given scope.
func MaintenanceConfirmToken(operation, scope string) string {
	operation = strings.TrimSpace(operation)
	scope = strings.TrimSpace(scope)
	sum := sha256.Sum256([]byte(operation + "\x00" + scope))
	return fmt.Sprintf("%s-%s", operation, hex.EncodeToString(sum[:6]))
}

// MaintenanceConfirmation is the outcome of evaluating a confirmation token.
type MaintenanceConfirmation struct {
	// Operation and Scope echo the checked op/scope.
	Operation string `json:"operation"`
	Scope     string `json:"scope"`
	// ConfirmToken is the token the caller must echo to proceed.
	ConfirmToken string `json:"confirm_token"`
	// Confirmed is true only when the provided token matched.
	Confirmed bool `json:"confirmed"`
	// Required is true when no token was provided (preview mode).
	Required bool `json:"confirmation_required"`
}

// ErrMaintenanceConfirmationMismatch is returned when a caller supplies a
// non-empty confirmation token that does not match the expected value.
var ErrMaintenanceConfirmationMismatch = fmt.Errorf("memory maintenance confirmation token mismatch")

// EvaluateMaintenanceConfirmation checks a provided token against the expected
// value for (operation, scope):
//
//   - provided == "":       Required=true, Confirmed=false, no error (preview).
//   - provided == expected: Confirmed=true, no error (proceed).
//   - otherwise:            error ErrMaintenanceConfirmationMismatch.
func EvaluateMaintenanceConfirmation(operation, scope, provided string) (MaintenanceConfirmation, error) {
	expected := MaintenanceConfirmToken(operation, scope)
	out := MaintenanceConfirmation{
		Operation:    strings.TrimSpace(operation),
		Scope:        strings.TrimSpace(scope),
		ConfirmToken: expected,
	}
	provided = strings.TrimSpace(provided)
	switch {
	case provided == "":
		out.Required = true
		return out, nil
	case provided == expected:
		out.Confirmed = true
		return out, nil
	default:
		return out, ErrMaintenanceConfirmationMismatch
	}
}
