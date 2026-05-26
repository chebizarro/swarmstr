package acp

import "fmt"

const (
	AcpErrorSessionNotFound    = "session_not_found"
	AcpErrorTurnTimeout        = "turn_timeout"
	AcpErrorBackendUnavailable = "backend_unavailable"
	AcpErrorRateLimited        = "rate_limited"
	AcpErrorApprovalRoute      = "approval_route"
)

// AcpError is a typed error suitable for manager events and observability.
type AcpError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Detail    string `json:"detail,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	Err       error  `json:"-"`
}

func (e AcpError) Error() string {
	base := e.Message
	if base == "" && e.Err != nil {
		base = e.Err.Error()
	}
	if e.Detail != "" {
		base = fmt.Sprintf("%s: %s", base, e.Detail)
	}
	if e.Code != "" {
		base = fmt.Sprintf("%s: %s", e.Code, base)
	}
	if e.Err != nil && e.Message != "" {
		base = fmt.Sprintf("%s: %v", base, e.Err)
	}
	return base
}

func (e AcpError) Unwrap() error {
	return e.Err
}

func (e AcpError) Is(target error) bool {
	switch t := target.(type) {
	case AcpError:
		return t.Code != "" && e.Code == t.Code
	case *AcpError:
		return t != nil && t.Code != "" && e.Code == t.Code
	default:
		return false
	}
}
