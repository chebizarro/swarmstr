package methods

import "fmt"

type PreconditionConflictError struct {
	Resource        string
	ExpectedVersion int
	CurrentVersion  int
	ExpectedEvent   string
	CurrentEvent    string
}

func (e *PreconditionConflictError) Error() string {
	return fmt.Sprintf(
		"precondition failed resource=%s expected_version=%d current_version=%d expected_event=%s current_event=%s",
		e.Resource,
		e.ExpectedVersion,
		e.CurrentVersion,
		e.ExpectedEvent,
		e.CurrentEvent,
	)
}

func (e *PreconditionConflictError) ErrorCode() int {
	return -32010
}

func (e *PreconditionConflictError) ErrorData() map[string]any {
	return map[string]any{
		"resource":         e.Resource,
		"expected_version": e.ExpectedVersion,
		"current_version":  e.CurrentVersion,
		"expected_event":   e.ExpectedEvent,
		"current_event":    e.CurrentEvent,
	}
}
