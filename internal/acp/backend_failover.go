package acp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var transientBackendErrorPattern = regexp.MustCompile(`(?i)\b(?:unavailable|rate[-\s]?limit(?:ed|ing)?|quota|exhausted|temporar(?:y|ily)|overloaded)\b`)

// BackendAttempt captures one failed backend candidate and whether externally
// visible turn output made retrying on another backend unsafe.
type BackendAttempt struct {
	Backend   string `json:"backend"`
	Error     string `json:"error"`
	Code      string `json:"code"`
	SawOutput bool   `json:"saw_output"`
	Cause     error  `json:"-"`
}

// BackendFailoverError retains every attempted backend when the candidate plan
// is exhausted or an attempt is not safe to retry.
type BackendFailoverError struct {
	Attempts []BackendAttempt `json:"attempts"`
}

func (e BackendFailoverError) Error() string {
	if len(e.Attempts) == 0 {
		return "ACP backend failover exhausted without an attempt"
	}
	parts := make([]string, 0, len(e.Attempts))
	for _, attempt := range e.Attempts {
		backend := strings.TrimSpace(attempt.Backend)
		if backend == "" {
			backend = "<auto>"
		}
		parts = append(parts, fmt.Sprintf("%s [%s]: %s", backend, attempt.Code, attempt.Error))
	}
	return "ACP backend attempts failed: " + strings.Join(parts, "; ")
}

func (e BackendFailoverError) Unwrap() error {
	if len(e.Attempts) == 0 {
		return nil
	}
	return e.Attempts[len(e.Attempts)-1].Cause
}

func resolveBackendCandidatePlan(configuredPrimary, resolvedPrimary string, fallbacks []string) []string {
	primary := normalizeBackendID(configuredPrimary)
	if primary == "" {
		primary = normalizeBackendID(resolvedPrimary)
	}
	candidates := make([]string, 0, len(fallbacks)+1)
	seen := make(map[string]struct{}, len(fallbacks)+1)
	appendCandidate := func(candidate string) {
		candidate = normalizeBackendID(candidate)
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}
	appendCandidate(primary)
	for _, fallback := range fallbacks {
		if strings.TrimSpace(fallback) != "" {
			appendCandidate(fallback)
		}
	}
	return candidates
}

func backendAttempt(backend string, err error, fallbackCode, fallbackMessage string, sawOutput bool) BackendAttempt {
	acpErr := ToAcpRuntimeError(err, fallbackCode, fallbackMessage)
	return BackendAttempt{
		Backend:   firstNonEmpty(normalizeBackendID(backend), "<auto>"),
		Error:     acpErr.Error(),
		Code:      acpErr.Code,
		SawOutput: sawOutput,
		Cause:     acpErr,
	}
}

func isFailoverWorthyBackendAttempt(attempt BackendAttempt) bool {
	if attempt.SawOutput || errors.Is(attempt.Cause, context.Canceled) || errors.Is(attempt.Cause, context.DeadlineExceeded) {
		return false
	}
	switch attempt.Code {
	case AcpCodeTurnFailed, AcpCodeSessionInitFailed, AcpCodeBackendUnavailable:
	default:
		return false
	}
	return transientBackendErrorPattern.MatchString(attempt.Error)
}

func turnEventsSawOutput(events []RuntimeEvent) bool {
	for _, event := range events {
		if event.Kind == EventTextDelta || event.Kind == EventToolCall || event.Kind == EventApprovalRequest {
			return true
		}
	}
	return false
}
