package protocol

const (
	ErrorCodeNotLinked      = "NOT_LINKED"
	ErrorCodeNotPaired      = "NOT_PAIRED"
	ErrorCodeAgentTimeout   = "AGENT_TIMEOUT"
	ErrorCodeInvalidRequest = "INVALID_REQUEST"
	ErrorCodeUnavailable    = "UNAVAILABLE"
)

func NewError(code string, message string, details any) *ErrorShape {
	return &ErrorShape{Code: code, Message: message, Details: details}
}
