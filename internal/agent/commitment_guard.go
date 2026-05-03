package agent

import (
	"regexp"
	"strings"
)

// ─── Commitment Accountability Guard ──────────────────────────────────────────
//
// This module detects when an agent makes promises it doesn't back with concrete
// actions, such as saying "I'll remind you tomorrow" without actually scheduling
// a cron job, or saying "I'll check that next" without taking any tool action.
//
// Two guard mechanisms:
// 1. Planning-only detection: Agent says "I'll do X" but takes no tool action
// 2. Reminder commitment detection: Agent promises follow-up without cron_add

// ─── Regex patterns ───────────────────────────────────────────────────────────

var (
	// planningOnlyPromiseRE detects language indicating future action intent
	// without actual execution: "I'll...", "let me...", "going to...", etc.
	planningOnlyPromiseRE = regexp.MustCompile(`(?i)\b(?:i(?:'ll| will)|let me|i(?:'m| am)\s+going to|first[, ]+i(?:'ll| will)|next[, ]+i(?:'ll| will)|i can do that)\b`)

	// planningOnlyCompletionRE detects language indicating work is done,
	// which exempts the response from being flagged as planning-only.
	planningOnlyCompletionRE = regexp.MustCompile(`(?i)\b(?:done|finished|implemented|updated|fixed|changed|ran|verified|found|here(?:'s| is) what|blocked by|the blocker is)\b`)

	// reminderCommitmentRE detects promises to remind, follow up, or check back.
	reminderCommitmentRE = regexp.MustCompile(`(?i)\b(?:i\s*['']?ll|i will)\s+(?:make sure to\s+)?(?:remember|remind|ping|follow up|follow-up|check back|circle back)\b`)

	// scheduleCommitmentRE detects promises to set/create a reminder.
	scheduleCommitmentRE = regexp.MustCompile(`(?i)\b(?:i\s*['']?ll|i will)\s+(?:set|create|schedule)\s+(?:a\s+)?reminder\b`)

	// deferredActionRE detects promises to do something "later", "next", etc.
	deferredActionRE = regexp.MustCompile(`(?i)\b(?:i(?:'ll| will))\s+(?:\w+\s+){0,4}(?:later|next|soon|in a (?:bit|moment|minute|few)|after(?:wards)?|tomorrow|tonight)\b`)
)

// ─── Constants ────────────────────────────────────────────────────────────────

const (
	// UnscheduledReminderNote is appended when a reminder commitment is detected
	// but no cron_add was successfully called.
	UnscheduledReminderNote = "Note: I did not schedule a reminder in this turn, so this will not trigger automatically."

	// PlanningOnlyRetryInstruction is the prompt injected when the agent only
	// stated a plan without taking action.
	PlanningOnlyRetryInstruction = "The previous assistant turn only described the plan. Do not restate the plan. Act now: take the first concrete tool action you can. If a real blocker prevents action, reply with the exact blocker in one sentence."

	// planningOnlyMaxLength caps the text length for planning-only detection
	// to avoid false positives on long responses.
	planningOnlyMaxLength = 700
)

// ─── Turn State Tracking ──────────────────────────────────────────────────────

// CommitmentState tracks tool activity during a turn for commitment validation.
type CommitmentState struct {
	// SuccessfulCronAdds counts cron_add tool calls that completed successfully.
	SuccessfulCronAdds int

	// ToolCallCount counts all non-plan tool calls executed during the turn.
	ToolCallCount int

	// HadMutatingAction tracks whether any side-effectful tool was called.
	HadMutatingAction bool
}

// RecordToolCall updates the commitment state when a tool completes.
// toolName is the name of the tool, isError indicates failure.
func (s *CommitmentState) RecordToolCall(toolName string, isError bool) {
	s.ToolCallCount++

	// Track successful cron_add calls
	if toolName == "cron_add" && !isError {
		s.SuccessfulCronAdds++
	}

	// Track mutating actions (side effects that can't be undone)
	if isMutatingToolName(toolName) && !isError {
		s.HadMutatingAction = true
	}
}

// isMutatingToolName returns true for tools that have external side effects.
func isMutatingToolName(name string) bool {
	mutating := map[string]bool{
		"cron_add":        true,
		"cron_remove":     true,
		"nostr_publish":   true,
		"nostr_dm_send":   true,
		"send_message":    true,
		"send_dm":         true,
		"social_plan_add": true,
		"bash_exec":       true,
		"file_write":      true,
		"file_edit":       true,
		"git_commit":      true,
		"git_push":        true,
	}
	return mutating[name]
}

// ─── Planning-Only Detection ──────────────────────────────────────────────────

// IsPlanningOnlyTurn returns true if the response contains promise language
// (e.g., "I'll do X") but no completion indicators and no tool calls were made.
// This suggests the agent stated a plan without actually executing it.
func IsPlanningOnlyTurn(text string, state CommitmentState) bool {
	// If tools were called, it's not planning-only
	if state.ToolCallCount > 0 {
		return false
	}

	text = strings.TrimSpace(text)

	// Skip empty or very long responses
	if len(text) == 0 || len(text) > planningOnlyMaxLength {
		return false
	}

	// Skip responses with code blocks (likely showing results)
	if strings.Contains(text, "```") {
		return false
	}

	// Must contain promise language
	if !planningOnlyPromiseRE.MatchString(text) {
		return false
	}

	// Must NOT contain completion language
	if planningOnlyCompletionRE.MatchString(text) {
		return false
	}

	return true
}

// ─── Reminder Commitment Detection ────────────────────────────────────────────

// HasUnbackedReminderCommitment returns true if the text contains a promise
// to remind/follow-up that wasn't backed by a successful cron_add call.
func HasUnbackedReminderCommitment(text string, state CommitmentState) bool {
	// If cron_add succeeded, the commitment is backed
	if state.SuccessfulCronAdds > 0 {
		return false
	}

	// Skip if the warning was already added
	if strings.Contains(strings.ToLower(text), strings.ToLower(UnscheduledReminderNote)) {
		return false
	}

	// Check for reminder/follow-up commitment patterns
	return reminderCommitmentRE.MatchString(text) || scheduleCommitmentRE.MatchString(text)
}

// HasUnbackedDeferredAction returns true if the text promises to do something
// "later" or "next" without scheduling it. Less strict than reminder detection.
func HasUnbackedDeferredAction(text string, state CommitmentState) bool {
	// If any cron was scheduled, assume the deferral is intentional
	if state.SuccessfulCronAdds > 0 {
		return false
	}

	// Only flag if no tools were called at all (pure planning response)
	if state.ToolCallCount > 0 {
		return false
	}

	return deferredActionRE.MatchString(text)
}

// ─── Response Post-Processing ─────────────────────────────────────────────────

// ApplyCommitmentGuard post-processes an agent response to add warnings
// when commitments aren't backed by concrete actions.
// Returns the (possibly modified) text and whether it was modified.
func ApplyCommitmentGuard(text string, state CommitmentState) (string, bool) {
	if HasUnbackedReminderCommitment(text, state) {
		return AppendUnscheduledReminderNote(text), true
	}
	return text, false
}

// AppendUnscheduledReminderNote adds the warning note to the response.
func AppendUnscheduledReminderNote(text string) string {
	return strings.TrimSpace(text) + "\n\n" + UnscheduledReminderNote
}

// ShouldRetryPlanningOnly returns true if the turn should be retried with
// the planning-only forcing instruction.
func ShouldRetryPlanningOnly(text string, state CommitmentState, retriesUsed int, maxRetries int) bool {
	if retriesUsed >= maxRetries {
		return false
	}
	return IsPlanningOnlyTurn(text, state)
}

// ─── Tool Trace Analysis ──────────────────────────────────────────────────────

// BuildCommitmentStateFromTraces constructs CommitmentState from tool traces.
// This allows post-turn analysis without modifying the tool executor.
func BuildCommitmentStateFromTraces(traces []ToolTrace) CommitmentState {
	state := CommitmentState{}
	for _, trace := range traces {
		isError := trace.Error != ""
		state.RecordToolCall(trace.Call.Name, isError)
	}
	return state
}

// CountSuccessfulCronAdds counts cron_add calls that succeeded in tool traces.
func CountSuccessfulCronAdds(traces []ToolTrace) int {
	count := 0
	for _, trace := range traces {
		if trace.Call.Name == "cron_add" && trace.Error == "" {
			count++
		}
	}
	return count
}
