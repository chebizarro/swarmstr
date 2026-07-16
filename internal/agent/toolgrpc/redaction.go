package toolgrpc

import "metiq/internal/agent"

const redactedValue = agent.RedactedToolValue

// Redactor is retained as the gRPC-facing alias for the centralized tool-data
// redactor used by traces, hooks, and tool-boundary results.
type Redactor = agent.ToolRedactor

func NewRedactor() Redactor { return agent.NewToolRedactor() }
