package agent

import "math"

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

	// MemoryRecallMax is the character ceiling for dynamic memory recall
	// context (indexed memory + file memory + session memory combined)
	// injected as per-turn dynamic context.
	MemoryRecallMax int

	// DynamicContextMax is the character ceiling for the entire per-turn
	// dynamic context string (memory recall + runtime dynamic additions).
	DynamicContextMax int

	// MaxToolCount is the maximum number of tool definitions that should be
	// sent inline with an API request. Scales cubically with context window
	// to aggressively limit tools for small models where JSON schema
	// tokenization overhead dominates.
	MaxToolCount int

	// CompactionThreshold is the fraction of the effective window at which
	// micro-compaction is triggered in the agentic loop. Smaller windows
	// compact earlier (0.70) to preserve headroom; larger windows can wait
	// longer (0.80).
	CompactionThreshold float64

	// MicroCompactKeepRecent is the number of recent tool results to preserve
	// when micro-compaction clears old results. Scales with window size.
	MicroCompactKeepRecent int
}

// charsPerToken is the approximate character-to-token ratio used for budget
// estimation. This is conservative; real tokenizers typically yield 3.5-4.0
// chars/token for English text.
const charsPerToken = 4

// safetyMargin accounts for JSON framing, message overhead, and tokenizer
// estimation drift.
const safetyMargin = 0.80

// ─── Proportional budget allocation ──────────────────────────────────────────
//
// All budget zones scale continuously with the effective character budget.
// The formulas use lerp/clamp to produce smooth gradients:
//
//   t = clamp(contextWindowTokens / 200000, 0, 1)
//
// This normalisation factor (t) is 0.0 for tiny models and 1.0 at 200K+.
// Each zone defines its own allocation percentage and [min, max] clamps.
//
// Zone          Share     Min        Max
// ──────────────────────────────────────────────
// Bootstrap     32%       1536       150000
// Skills         7%        800        30000
// ToolDefs      13%       1500        50000
// Memory         5%        600        24000
// ToolResultPct  -        15%         30%      (of effective window)
// Compaction     -        70%         80%      (threshold)
// KeepRecent     -         1           8       (count)
// History       remainder (at least 1000)

// ComputeContextBudget derives a ContextBudget from a ModelContextProfile.
// The allocation strategy scales each zone proportionally to the effective
// window using continuous lerp/clamp math — no tier switches.
func ComputeContextBudget(profile ModelContextProfile) ContextBudget {
	effectiveTokens := profile.EffectiveInputTokens()
	effectiveChars := int(float64(effectiveTokens) * charsPerToken * safetyMargin)
	if effectiveChars < 1024 {
		effectiveChars = 1024
	}

	// Normalisation factor: 0.0 at 0 tokens, 1.0 at 200K tokens.
	t := clampF(float64(profile.ContextWindowTokens)/200_000.0, 0, 1)

	b := ContextBudget{
		Profile:        profile,
		EffectiveChars: effectiveChars,
	}

	// ── Zone allocations ────────────────────────────────────────────────────
	b.BootstrapTotalMax = clampInt(effectiveChars*32/100, 1_536, 150_000)
	b.BootstrapFileMax = clampInt(b.BootstrapTotalMax/3, 512, 20_000)
	b.SkillsTotalMax = clampInt(effectiveChars*7/100, 800, 30_000)
	// Tool definitions are JSON schemas that tokenize at ~2.5 chars/token
	// (not 4 like prose). Apply a 5/8 correction factor to prevent
	// overallocation that causes context overflow on small models.
	b.ToolDefsMax = clampInt(effectiveChars*13/100*5/8, 1_000, 50_000)
	b.SessionMemoryMax = clampInt(effectiveChars*5/100, 600, 24_000)
	b.MemoryRecallMax = clampInt(effectiveChars*8/100, 800, 40_000)
	b.DynamicContextMax = clampInt(effectiveChars*12/100, 1_200, 60_000)

	// Skills count scales linearly: 3 at t=0, 150 at t=1.
	b.SkillsMaxCount = clampInt(int(lerp(3, 150, t)), 3, 150)

	// MaxToolCount scales cubically: small models get tight limits because
	// JSON schemas tokenize poorly (~2.5 chars/token). t³ curve gives:
	// ~17 at 65K, ~59 at 128K, ~200 at 200K+.
	t3 := t * t * t
	b.MaxToolCount = clampInt(int(math.Round(lerp(10, 200, t3))), 10, 200)

	// Tool result share: 15% for tiny models, 30% for 200K+.
	b.ToolResultSharePct = clampF(lerp(0.15, 0.30, t), 0.15, 0.30)

	// Compaction threshold: compact earlier for small models.
	b.CompactionThreshold = clampF(lerp(0.70, 0.80, t), 0.70, 0.80)

	// Keep-recent count for micro-compaction.
	b.MicroCompactKeepRecent = clampInt(int(math.Round(lerp(1, 8, t))), 1, 8)

	// History gets whatever remains after fixed-zone allocations.
	fixedUse := b.BootstrapTotalMax + b.SkillsTotalMax + b.ToolDefsMax + b.SessionMemoryMax + b.MemoryRecallMax
	b.HistoryMax = effectiveChars - fixedUse
	if b.HistoryMax < 1_000 {
		b.HistoryMax = 1_000
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

// CompressionPressure returns a 0.0–1.0 gradient indicating how aggressively
// tool definitions and skill prompts should be compressed. 0.0 means no
// compression needed; 1.0 means maximum compression.
//
// pressure = 1 - clamp(budgetChars / estimatedTotalChars, 0, 1)
func CompressionPressure(budgetChars, estimatedTotalChars int) float64 {
	if estimatedTotalChars <= 0 || budgetChars >= estimatedTotalChars {
		return 0
	}
	return clampF(1.0-float64(budgetChars)/float64(estimatedTotalChars), 0, 1)
}

// ─── Budget enforcement ──────────────────────────────────────────────────────

// EnforceSystemPromptBudget truncates the system prompt to SystemPromptMax
// characters if it exceeds the budget. Returns the (possibly truncated) prompt
// and true if truncation occurred.
func EnforceSystemPromptBudget(prompt string, budget ContextBudget) (string, bool) {
	if budget.SystemPromptMax <= 0 || len(prompt) <= budget.SystemPromptMax {
		return prompt, false
	}
	return truncateUTF8(prompt, budget.SystemPromptMax) + "\n\n⚠️ [System prompt truncated to fit context budget]", true
}

// EnforceDynamicContextBudget truncates the dynamic context to DynamicContextMax
// characters if it exceeds the budget.
func EnforceDynamicContextBudget(ctx string, budget ContextBudget) (string, bool) {
	if budget.DynamicContextMax <= 0 || len(ctx) <= budget.DynamicContextMax {
		return ctx, false
	}
	return truncateUTF8(ctx, budget.DynamicContextMax) + "\n\n⚠️ [Dynamic context truncated to fit budget]", true
}

// EnforceMemoryRecallBudget truncates memory recall content to MemoryRecallMax.
func EnforceMemoryRecallBudget(recall string, budget ContextBudget) (string, bool) {
	if budget.MemoryRecallMax <= 0 || len(recall) <= budget.MemoryRecallMax {
		return recall, false
	}
	return truncateUTF8(recall, budget.MemoryRecallMax) + "\n\n⚠️ [Memory recall truncated to fit budget]", true
}

// BudgetUtilization returns the percentage (0-100) of a budget zone in use.
func BudgetUtilization(used, max int) int {
	if max <= 0 {
		return 0
	}
	pct := used * 100 / max
	if pct > 100 {
		return 100
	}
	return pct
}

// truncateUTF8 truncates s to at most maxBytes, respecting UTF-8 boundaries.
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes to find a valid UTF-8 boundary.
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	if cut == 0 {
		return ""
	}
	return s[:cut]
}

// SessionMemoryBudgetRunes returns the SessionMemoryMax as a rune count
// (approximate, assuming ~1 byte per rune for English text; safe for truncation).
func (b ContextBudget) SessionMemoryBudgetRunes() int {
	if b.SessionMemoryMax <= 0 {
		return 1600 // fallback to legacy default
	}
	// Runes are roughly 1:1 with chars for Latin text; use 90% for safety.
	return b.SessionMemoryMax * 9 / 10
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

// clampF returns v clamped to [lo, hi].
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// lerp performs linear interpolation between a and b by t ∈ [0, 1].
func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}
