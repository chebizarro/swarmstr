package protocol

import "fmt"

const (
	MinProtocolVersion     = 1
	CurrentProtocolVersion = 3
)

func NegotiateProtocol(minProtocol, maxProtocol int) (int, error) {
	if minProtocol < MinProtocolVersion || maxProtocol < MinProtocolVersion {
		return 0, fmt.Errorf("invalid protocol range")
	}
	if minProtocol > maxProtocol {
		return 0, fmt.Errorf("invalid protocol range")
	}
	if maxProtocol < MinProtocolVersion || minProtocol > CurrentProtocolVersion {
		return 0, fmt.Errorf("unsupported protocol range min=%d max=%d supported=[%d,%d]", minProtocol, maxProtocol, MinProtocolVersion, CurrentProtocolVersion)
	}
	negotiated := maxProtocol
	if negotiated > CurrentProtocolVersion {
		negotiated = CurrentProtocolVersion
	}
	if negotiated < MinProtocolVersion {
		negotiated = MinProtocolVersion
	}
	return negotiated, nil
}
