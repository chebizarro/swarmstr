package channels

// PollingFallbackEnabled returns true only for an explicit allow_polling=true
// channel setting. Polling is never inferred from missing webhook/WebSocket
// configuration because event delivery must remain subscription-driven by
// default.
func PollingFallbackEnabled(cfg map[string]any) bool {
	enabled, _ := cfg["allow_polling"].(bool)
	return enabled
}
