package agent

// ─── Context budget ───────────────────────────────────────────────────────────

// ContextBudget allocates the effective context window across the major
// content-injection zones. All values are in characters (approximately
// tokens × 4). A 20% safety margin is applied to account for JSON overhead,
// token-estimation drift, and message framing.
type ContextBudget struct {
	// Profile is the model context profile this budget was derived from.
	Profile ModelContextProfile

	// EffectiveChars is the total character budget available for input after
	// reserving output tokens and applying the safety margin.
	// Computed as: (ContextWindowTokens - ReserveOutputTokens) × 4 × 0.80
	EffectiveChars int

	// SystemPromptMax is the maximum characters for the combined system prompt
	// (bootstrap + provider prompt + dynamic additions).
	SystemPromptMax int

	// BootstrapFileMax is the per-file character ceiling for bootstrap files
	// (SOUL.md, IDENTITY.md, USER.md, AGENTS.md).
	BootstrapFileMax int

	// BootstrapTotalMax is the total character ceiling for all bootstrap files
	// combined.
	BootstrapTotalMax int

	// SkillsTotalMax is the total character ceiling for injected skill
	// descriptions in the system prompt.
	SkillsTotalMax int

	// SkillsMaxCount is the maximum number of skills injected into the prompt.
	SkillsMaxCount int

	// ToolDefsMax is the character ceiling for serialized tool definitions
	// (JSON schemas sent as function-calling definitions).
	ToolDefsMax int

	// HistoryMax is the character budget for conversation history messages.
	HistoryMax int

	// ToolResultSharePct is the fraction of the effective window that
	// GuardToolResultMessages uses for individual tool result truncation.
	ToolResultSharePct float64

	// SessionMemoryMax is the character ceiling for session memory content
	// injected into the system prompt.
	SessionMemoryMax int
}

// charsPerToken is the approximate character-to-token ratio used for budget
// estimation. This is conservative; real tokenizers typically yield 3.5-4.0
// chars/token for English text.
const charsPerToken = 4

// safetyMargin accounts for JSON framing, message overhead, and tokenizer
// estimation drift.
const safetyMargin = 0.80

// ComputeContextBudget derives a ContextBudget from a ModelContextProfile.
// The allocation strategy scales each zone proportionally to the effective
// window, with tier-specific floors and caps to ensure usability at every tier.
func ComputeContextBudget(profile ModelContextProfile) ContextBudget {
	effectiveTokens := profile.EffectiveInputTokens()
	effectiveChars := int(float64(effectiveTokens) * charsPerToken * safetyMargin)
	if effectiveChars < 1024 {
		effectiveChars = 1024
	}

	b := ContextBudget{
		Profile:        profile,
		EffectiveChars: effectiveChars,
	}

	switch profile.Tier {
	case TierMicro:
		b.BootstrapFileMax = 1_500
		b.BootstrapTotalMax = 4_000
		b.SkillsTotalMax = 800
		b.SkillsMaxCount = 3
		b.ToolDefsMax = 1_500
		b.ToolResultSharePct = 0.15
		b.SessionMemoryMax = 600

		// History gets whatever is left after fixed allocations
		fixedUse := b.BootstrapTotalMax + b.SkillsTotalMax + b.ToolDefsMax + b.SessionMemoryMax
		b.HistoryMax = effectiveChars - fixedUse
		if b.HistoryMax < 1_000 {
			b.HistoryMax = 1_000
		}

	case TierSmall:
		b.BootstrapFileMax = 5_000
		b.BootstrapTotalMax = 15_000
		b.SkillsTotalMax = 3_500
		b.SkillsMaxCount = 10
		b.ToolDefsMax = 4_000
		b.ToolResultSharePct = 0.20
		b.SessionMemoryMax = 3_000

		fixedUse := b.BootstrapTotalMax + b.SkillsTotalMax + b.ToolDefsMax + b.SessionMemoryMax
		b.HistoryMax = effectiveChars - fixedUse
		if b.HistoryMax < 4_000 {
			b.HistoryMax = 4_000
		}

	default: // TierStandard
		b.BootstrapFileMax = clampInt(effectiveChars*25/100, 5_000, 20_000)
		b.BootstrapTotalMax = clampInt(effectiveChars*25/100, 15_000, 150_000)
		b.SkillsTotalMax = clampInt(effectiveChars*5/100, 3_500, 30_000)
		b.SkillsMaxCount = 150
		b.ToolDefsMax = clampInt(effectiveChars*10/100, 4_000, 50_000)
		b.ToolResultSharePct = 0.30
		b.SessionMemoryMax = clampInt(effectiveChars*4/100, 3_000, 24_000)

		fixedUse := b.BootstrapTotalMax + b.SkillsTotalMax + b.ToolDefsMax + b.SessionMemoryMax
		b.HistoryMax = effectiveChars - fixedUse
		if b.HistoryMax < 10_000 {
			b.HistoryMax = 10_000
		}
	}

	// System prompt max is bootstrap + skills + session memory.
	b.SystemPromptMax = b.BootstrapTotalMax + b.SkillsTotalMax + b.SessionMemoryMax

	return b
}

// ComputeContextBudgetForTokens is a convenience that builds a profile from a
// raw token count and then computes the budget.
func ComputeContextBudgetForTokens(contextWindowTokens int) ContextBudget {
	return ComputeContextBudget(ProfileFromContextWindowTokens(contextWindowTokens))
}

// ─── Budget helpers ───────────────────────────────────────────────────────────

// clampInt returns v clamped to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
