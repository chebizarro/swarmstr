package protocol

import (
	"errors"
	"fmt"
)

const (
	MinProtocolVersion     = 1
	CurrentProtocolVersion = 4
)

var (
	ErrInvalidProtocolRange     = errors.New("invalid protocol range")
	ErrUnsupportedProtocolRange = errors.New("unsupported protocol range")
)

func NegotiateProtocol(minProtocol, maxProtocol int) (int, error) {
	if minProtocol <= 0 || maxProtocol <= 0 || minProtocol > maxProtocol {
		return 0, fmt.Errorf("%w min=%d max=%d", ErrInvalidProtocolRange, minProtocol, maxProtocol)
	}
	if maxProtocol < MinProtocolVersion || minProtocol > CurrentProtocolVersion {
		return 0, fmt.Errorf("%w min=%d max=%d supported=[%d,%d]", ErrUnsupportedProtocolRange, minProtocol, maxProtocol, MinProtocolVersion, CurrentProtocolVersion)
	}
	negotiated := maxProtocol
	if negotiated > CurrentProtocolVersion {
		negotiated = CurrentProtocolVersion
	}
	return negotiated, nil
}
