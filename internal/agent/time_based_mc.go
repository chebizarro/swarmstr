package agent

import "time"

// ─── Time-based micro-compaction ──────────────────────────────────────────────
//
// When the gap since the last assistant message exceeds a threshold, the
// server-side prompt cache has almost certainly expired, so the full prefix
// will be rewritten anyway. Clearing old tool results before the request
// shrinks what gets rewritten — the clearing is "free".
//
// Runs BEFORE the first API call in RunAgenticLoop, so the shrunk prompt is
// what actually gets sent. Running after the first miss would only help
// subsequent turns.
//
// Ported from src/services/compact/timeBasedMCConfig.ts.

// TimeBasedMCConfig controls the time-gap microcompact trigger.
type TimeBasedMCConfig struct {
	// Enabled is the master switch. When false, time-based microcompact is a no-op.
	Enabled bool

	// GapThresholdMinutes triggers when (now − last assistant timestamp) exceeds
	// this value. 60 is the safe choice: the server's 1h cache TTL is guaranteed
	// expired for all users, so we never force a miss that wouldn't have happened.
	GapThresholdMinutes int

	// KeepRecent is the number of most-recent compactable tool results to
	// preserve when the time-gap trigger fires. Takes priority over the budget's
	// MicroCompactKeepRecent.
	KeepRecent int
}

// DefaultTimeBasedMCConfig provides safe defaults matching the src/ pattern.
var DefaultTimeBasedMCConfig = TimeBasedMCConfig{
	Enabled:             true,
	GapThresholdMinutes: 60,
	KeepRecent:          5,
}

// ShouldTimeBasedMicrocompact returns true if the gap since the last assistant
// message exceeds the threshold. When true, the caller should run microcompact
// with the config's KeepRecent value — more aggressively than the normal
// budget-driven KeepRecent, since the prompt cache has expired anyway.
func ShouldTimeBasedMicrocompact(config TimeBasedMCConfig, lastAssistantTime time.Time) bool {
	if !config.Enabled {
		return false
	}
	if lastAssistantTime.IsZero() {
		return false
	}
	threshold := config.GapThresholdMinutes
	if threshold <= 0 {
		threshold = DefaultTimeBasedMCConfig.GapThresholdMinutes
	}
	return time.Since(lastAssistantTime) > time.Duration(threshold)*time.Minute
}

// TimeBasedMicrocompact runs an aggressive micro-compaction pass when the
// time-gap trigger fires. Returns the compacted messages and the number of
// tool results cleared, or the original messages unchanged if nothing was done.
func TimeBasedMicrocompact(messages []LLMMessage, config TimeBasedMCConfig, lastAssistantTime time.Time) MicroCompactResult {
	if !ShouldTimeBasedMicrocompact(config, lastAssistantTime) {
		return MicroCompactResult{Messages: messages}
	}

	keepRecent := config.KeepRecent
	if keepRecent <= 0 {
		keepRecent = DefaultTimeBasedMCConfig.KeepRecent
	}

	return MicroCompactMessages(messages, MicroCompactOptions{
		KeepRecent: keepRecent,
		// No TargetChars — clear all eligible old results since cache is expired.
	})
}
