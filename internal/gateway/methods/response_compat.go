package methods

var responseFieldAliases = [][2]string{
	{"run_id", "runId"},
	{"session_id", "sessionId"},
	{"parent_session_id", "parentSessionId"},
	{"accepted_at", "acceptedAt"},
	{"started_at", "startedAt"},
	{"ended_at", "endedAt"},
	{"agent_id", "agentId"},
	{"display_name", "displayName"},
	{"aborted_count", "abortedCount"},
	{"from_entries", "fromEntries"},
	{"summary_generated", "summaryGenerated"},
	{"fallback_used", "fallbackUsed"},
	{"fallback_from", "fallbackFrom"},
	{"fallback_to", "fallbackTo"},
	{"fallback_reason", "fallbackReason"},
}

// ApplyCompatResponseAliases projects top-level response aliases needed for
// OpenClaw camelCase compatibility while preserving existing snake_case fields.
func ApplyCompatResponseAliases[T any](result T) T {
	payload, ok := any(result).(map[string]any)
	if !ok || payload == nil {
		return result
	}
	for _, pair := range responseFieldAliases {
		snake := pair[0]
		camel := pair[1]
		if value, ok := payload[snake]; ok {
			if _, exists := payload[camel]; !exists {
				payload[camel] = value
			}
			continue
		}
		if value, ok := payload[camel]; ok {
			if _, exists := payload[snake]; !exists {
				payload[snake] = value
			}
		}
	}
	return any(payload).(T)
}
