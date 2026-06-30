package acp

import (
	"errors"
	"fmt"
)

const (
	AcpErrorSessionNotFound    = "session_not_found"
	AcpErrorTurnTimeout        = "turn_timeout"
	AcpErrorBackendUnavailable = "backend_unavailable"
	AcpErrorRateLimited        = "rate_limited"
	AcpErrorApprovalRoute      = "approval_route"
)

const (
	AcpCodeBackendMissing            = "ACP_BACKEND_MISSING"
	AcpCodeBackendUnavailable        = "ACP_BACKEND_UNAVAILABLE"
	AcpCodeBackendUnsupportedControl = "ACP_BACKEND_UNSUPPORTED_CONTROL"
	AcpCodeDispatchDisabled          = "ACP_DISPATCH_DISABLED"
	AcpCodeInvalidRuntimeOption      = "ACP_INVALID_RUNTIME_OPTION"
	AcpCodeSessionInitFailed         = "ACP_SESSION_INIT_FAILED"
	AcpCodeTurnFailed                = "ACP_TURN_FAILED"
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

func (e AcpError) Unwrap() error { return e.Err }

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

// NewAcpRuntimeError creates an acp-core-style typed runtime error.
func NewAcpRuntimeError(code, message string, err error) AcpError {
	return AcpError{Code: code, Message: message, Err: err}
}

// ToAcpRuntimeError maps existing ACP/sentinel errors into acp-core-style typed runtime errors.
func ToAcpRuntimeError(err error, fallbackCode, fallbackMessage string) AcpError {
	if err == nil {
		return AcpError{Code: fallbackCode, Message: fallbackMessage}
	}
	var acpErr AcpError
	if errors.As(err, &acpErr) && acpErr.Code != "" {
		return acpErr
	}
	code := fallbackCode
	message := fallbackMessage
	if errors.Is(err, ErrBackendMissing) {
		code = AcpCodeBackendMissing
		message = "ACP runtime backend not configured"
	} else if errors.Is(err, ErrBackendUnavailable) {
		code = AcpCodeBackendUnavailable
		message = "ACP runtime backend unavailable"
	} else if errors.Is(err, ErrSessionNotFound) {
		code = AcpCodeSessionInitFailed
		message = "ACP session not found"
	} else if errors.Is(err, ErrTurnActive) {
		code = AcpCodeTurnFailed
		message = "ACP turn active"
	}
	if message == "" {
		message = err.Error()
	}
	return AcpError{Code: code, Message: message, Err: err}
}
