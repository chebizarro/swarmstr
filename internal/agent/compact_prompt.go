package agent

import "fmt"

// ─── Compaction prompts for small context windows ─────────────────────────────
//
// When the SmallWindowEngine or auto-compaction triggers LLM-based compaction,
// the compaction prompt itself must fit within the small model's context. These
// functions return tier-appropriate prompts of varying verbosity.

// SelectCompactSystemPrompt returns a compaction system prompt sized for the
// given tier:
//   - TierMicro: ~80 tokens, 3 sections (TASK/STATE/FACTS)
//   - TierSmall: ~200 tokens, 5 sections (adds FILES/ERRORS)
//   - TierStandard: full 9-section template matching src/services/compact/prompt.ts
func SelectCompactSystemPrompt(tier ContextTier) string {
	switch tier {
	case TierMicro:
		return compactSystemPromptMicro
	case TierSmall:
		return compactSystemPromptSmall
	default:
		return compactSystemPromptStandard
	}
}

// SelectCompactUserPrompt formats the user message for a compaction call.
// The transcript is a plain-text rendering of recent conversation history.
// maxTranscriptChars limits how much history is included; 0 means no limit.
func SelectCompactUserPrompt(tier ContextTier, transcript string, maxTranscriptChars int) string {
	if maxTranscriptChars > 0 && len(transcript) > maxTranscriptChars {
		transcript = transcript[:maxTranscriptChars] + "\n\n[...transcript truncated...]"
	}

	switch tier {
	case TierMicro:
		return fmt.Sprintf("Summarize this conversation:\n\n%s", transcript)
	case TierSmall:
		return fmt.Sprintf("Summarize this conversation concisely. Focus on what matters for continuing the work:\n\n%s", transcript)
	default:
		return fmt.Sprintf("Create a detailed summary of this conversation that preserves all context needed to continue seamlessly:\n\n%s", transcript)
	}
}

// CompactOutputMaxTokens returns the recommended max_tokens for a compaction
// LLM call at the given tier.
func CompactOutputMaxTokens(tier ContextTier) int {
	switch tier {
	case TierMicro:
		return 256
	case TierSmall:
		return 512
	default:
		return 2048
	}
}

// ─── Budget-driven variants ──────────────────────────────────────────────────
//
// These functions derive compaction parameters from a ContextBudget, avoiding
// hard tier switches. The budget's EffectiveChars and Profile.ContextWindowTokens
// drive smooth transitions.

// SelectCompactSystemPromptForBudget returns a compaction system prompt sized
// for the budget's effective window. Uses the same prompt templates but selects
// based on effective character budget rather than tier enum.
//
//   - < 6000 chars: micro prompt (~80 tokens)
//   - < 25000 chars: small prompt (~200 tokens)
//   - ≥ 25000 chars: full standard prompt
func SelectCompactSystemPromptForBudget(budget ContextBudget) string {
	switch {
	case budget.EffectiveChars < 6_000:
		return compactSystemPromptMicro
	case budget.EffectiveChars < 25_000:
		return compactSystemPromptSmall
	default:
		return compactSystemPromptStandard
	}
}

// SelectCompactUserPromptForBudget formats the user message for a compaction
// call, with transcript limits derived from the budget.
func SelectCompactUserPromptForBudget(budget ContextBudget, transcript string) string {
	// Allow transcript up to 60% of effective chars for the compaction input.
	maxTranscript := budget.EffectiveChars * 60 / 100
	if maxTranscript < 500 {
		maxTranscript = 500
	}
	if len(transcript) > maxTranscript {
		transcript = transcript[:maxTranscript] + "\n\n[...transcript truncated...]"
	}

	switch {
	case budget.EffectiveChars < 6_000:
		return fmt.Sprintf("Summarize this conversation:\n\n%s", transcript)
	case budget.EffectiveChars < 25_000:
		return fmt.Sprintf("Summarize this conversation concisely. Focus on what matters for continuing the work:\n\n%s", transcript)
	default:
		return fmt.Sprintf("Create a detailed summary of this conversation that preserves all context needed to continue seamlessly:\n\n%s", transcript)
	}
}

// CompactOutputMaxTokensForBudget returns the recommended max_tokens for a
// compaction call, scaled proportionally with the context window.
// Ranges from 256 (tiny) to 2048 (large).
func CompactOutputMaxTokensForBudget(budget ContextBudget) int {
	t := clampF(float64(budget.Profile.ContextWindowTokens)/200_000.0, 0, 1)
	return clampInt(int(lerp(256, 2048, t)), 256, 2048)
}

// ─── Prompt templates ─────────────────────────────────────────────────────────

const compactSystemPromptMicro = `Summarize this conversation in exactly 3 sections. Be extremely concise.

TASK: What was asked (1-2 sentences max)
STATE: Current status and next action (1-2 sentences max)
FACTS: Key file paths, error messages, decisions (max 5 bullet points)

Return only the 3 labeled sections. No preamble.`

const compactSystemPromptSmall = `Summarize this conversation concisely in 5 sections:

TASK: What was the user's request (1-2 sentences)
STATE: Current status - what's done, what's pending, next step (2-3 sentences)
FILES: Key files mentioned or modified (paths only, max 8 items)
ERRORS: Last error encountered and its resolution, if any (1-2 sentences, skip if none)
FACTS: Important decisions, values, constraints (max 8 bullet points)

Return only the labeled sections. No preamble or analysis.`

const compactSystemPromptStandard = `You are a conversation summarizer. Create a detailed summary that preserves all context needed to continue the conversation seamlessly.

Structure your summary in these sections:

PRIMARY REQUEST: What the user originally asked for and the high-level goal.
TECHNICAL CONCEPTS: Key technical terms, APIs, frameworks, or patterns discussed.
FILES AND CODE: Specific files, functions, classes, or code patterns referenced or modified.
ERRORS AND FIXES: Any errors encountered and how they were resolved.
PROBLEM SOLVING: Approaches tried, what worked and what didn't, and why.
USER MESSAGES: Important clarifications, preferences, or constraints the user expressed.
PENDING TASKS: Work items that remain incomplete or were deferred.
CURRENT WORK: What was being actively worked on when the conversation was interrupted.
NEXT STEP: The immediate next action that should be taken to continue.

For each section, include specific details — file paths, function names, error messages, command outputs. A future assistant reading this summary should be able to pick up exactly where this conversation left off.

If a section has no relevant content, omit it rather than writing "N/A".`
