package acp

import (
	"errors"
	"strings"
	"testing"
)

func TestAcpErrorFormattingByCategory(t *testing.T) {
	cases := []struct {
		name string
		err  AcpError
		want string
	}{
		{"plain", AcpError{Message: "failed"}, "failed"},
		{"coded", AcpError{Code: AcpErrorSessionNotFound, Message: "missing session"}, "session_not_found: missing session"},
		{"detailed", AcpError{Code: AcpErrorTurnTimeout, Message: "turn timed out", Detail: "after 10s"}, "turn_timeout: turn timed out: after 10s"},
		{"retryable", AcpError{Code: AcpErrorRateLimited, Message: "too many sessions", Retryable: true}, "rate_limited: too many sessions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToAcpRuntimeErrorMapsCoreCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"missing", ErrBackendMissing, AcpCodeBackendMissing},
		{"unavailable", ErrBackendUnavailable, AcpCodeBackendUnavailable},
		{"session", ErrSessionNotFound, AcpCodeSessionInitFailed},
		{"fallback", errors.New("boom"), AcpCodeTurnFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToAcpRuntimeError(tc.err, AcpCodeTurnFailed, "turn failed")
			if got.Code != tc.want {
				t.Fatalf("code = %q, want %q", got.Code, tc.want)
			}
			if !errors.Is(got, tc.err) {
				t.Fatalf("mapped error does not wrap source: %+v", got)
			}
		})
	}
}

func TestAcpErrorWrappingAndTypeChecking(t *testing.T) {
	cause := errors.New("dial refused")
	err := AcpError{Code: AcpErrorBackendUnavailable, Message: "backend unavailable", Err: cause}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause was not discoverable with errors.Is")
	}
	if !errors.Is(err, AcpError{Code: AcpErrorBackendUnavailable}) {
		t.Fatal("AcpError code was not discoverable with errors.Is")
	}
	if errors.Is(err, AcpError{Code: AcpErrorRateLimited}) {
		t.Fatal("unexpected match for different ACP error code")
	}
	var typed AcpError
	if !errors.As(err, &typed) || typed.Code != AcpErrorBackendUnavailable {
		t.Fatalf("errors.As failed, typed=%+v", typed)
	}
	ptrErr := &AcpError{Code: AcpErrorRateLimited, Message: "slow down"}
	var typedPtr *AcpError
	if !errors.As(ptrErr, &typedPtr) || typedPtr.Code != AcpErrorRateLimited {
		t.Fatalf("errors.As pointer failed, typed=%+v", typedPtr)
	}
	if got := err.Error(); !strings.Contains(got, "backend_unavailable: backend unavailable") || !strings.Contains(got, "dial refused") {
		t.Fatalf("wrapped error message missing context: %q", got)
	}
}
