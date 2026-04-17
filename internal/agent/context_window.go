package agent

import (
	"strings"
	"sync"
)

// ─── Context tiers ────────────────────────────────────────────────────────────

// ContextTier classifies a model's context window into a coarse tier used to
// drive budget allocation, compaction strategy, and tool/skill filtering.
type ContextTier int

const (
	// TierMicro covers models with < 8K token context windows (e.g. Phi-3-mini 4K,
	// Llama-3.2-1B 4K, Qwen2.5-0.5B 4K). These require aggressive pruning,
	// minimal skill injection, and compressed tool definitions.
	TierMicro ContextTier = iota

	// TierSmall covers models with 8K–16K token context windows (e.g. Gemma-2B 8K,
	// Mistral-7B 8K, Llama-3.2-3B 8K). These allow moderate tool/skill presence
	// with reduced compaction budgets.
	TierSmall

	// TierStandard covers models with > 16K token context windows.
	// All existing defaults (bootstrap 150K chars, 30 agentic iterations, etc.)
	// are preserved unchanged for this tier.
	TierStandard
)

// String returns the tier name.
func (t ContextTier) String() string {
	switch t {
	case TierMicro:
		return "micro"
	case TierSmall:
		return "small"
	case TierStandard:
		return "standard"
	default:
		return "unknown"
	}
}

// ─── Model context profile ───────────────────────────────────────────────────

// ModelContextProfile describes a model's context window characteristics and
// the operational limits derived from it.
type ModelContextProfile struct {
	// ContextWindowTokens is the model's total context window size in tokens.
	ContextWindowTokens int

	// ReserveOutputTokens is the number of tokens reserved for model output.
	// The effective input budget is ContextWindowTokens - ReserveOutputTokens.
	ReserveOutputTokens int

	// Tier is the coarse tier classification derived from ContextWindowTokens.
	Tier ContextTier

	// MaxAgenticIterations limits the number of tool→LLM→tool cycles in the
	// agentic loop. Smaller windows mean fewer iterations to avoid context
	// overflow from accumulated tool results.
	MaxAgenticIterations int
}

// EffectiveInputTokens returns the number of tokens available for input
// (system prompt + history + tools) after reserving output space.
func (p ModelContextProfile) EffectiveInputTokens() int {
	eff := p.ContextWindowTokens - p.ReserveOutputTokens
	if eff < 256 {
		return 256
	}
	return eff
}

// ─── Model context registry ──────────────────────────────────────────────────

type modelPatternEntry struct {
	pattern string // lowercase prefix pattern
	profile ModelContextProfile
}

var (
	registryMu sync.RWMutex
	// registry stores patterns in insertion order; last match wins for
	// duplicate patterns registered via override.
	registry []modelPatternEntry
)

// defaultStandardProfile is returned when no registered pattern matches.
var defaultStandardProfile = ModelContextProfile{
	ContextWindowTokens:  200_000,
	ReserveOutputTokens:  4_096,
	Tier:                 TierStandard,
	MaxAgenticIterations: 30,
}

// RegisterModelContextPattern registers a model context profile for a name
// pattern. The pattern is matched as a case-insensitive prefix against model
// IDs. Later registrations for the same pattern replace earlier ones.
//
// Panics if pattern is empty.
func RegisterModelContextPattern(pattern string, profile ModelContextProfile) {
	if pattern == "" {
		panic("RegisterModelContextPattern: empty pattern")
	}
	lowerPattern := strings.ToLower(pattern)

	registryMu.Lock()
	defer registryMu.Unlock()

	// Replace existing entry with same pattern if present.
	for i, entry := range registry {
		if entry.pattern == lowerPattern {
			registry[i].profile = profile
			return
		}
	}
	registry = append(registry, modelPatternEntry{pattern: lowerPattern, profile: profile})
}

// ResolveModelContext returns the ModelContextProfile for the given model ID.
// It matches registered patterns as case-insensitive prefixes, preferring the
// longest match. Returns a default TierStandard profile (200K window) when no
// pattern matches.
func ResolveModelContext(modelID string) ModelContextProfile {
	if modelID == "" {
		return defaultStandardProfile
	}
	lowerID := strings.ToLower(modelID)

	registryMu.RLock()
	defer registryMu.RUnlock()

	bestLen := 0
	bestProfile := defaultStandardProfile
	matched := false

	for _, entry := range registry {
		if strings.HasPrefix(lowerID, entry.pattern) && len(entry.pattern) > bestLen {
			bestLen = len(entry.pattern)
			bestProfile = entry.profile
			matched = true
		}
	}

	if !matched {
		return defaultStandardProfile
	}
	return bestProfile
}

// TierFromContextWindowTokens derives a ContextTier from a raw token count.
// Useful when the caller has a token count but not a registered model ID.
func TierFromContextWindowTokens(tokens int) ContextTier {
	switch {
	case tokens > 0 && tokens < 8_192:
		return TierMicro
	case tokens >= 8_192 && tokens <= 16_384:
		return TierSmall
	default:
		return TierStandard
	}
}

// ProfileFromContextWindowTokens builds a ModelContextProfile from a raw token
// count, using sensible tier-appropriate defaults.
func ProfileFromContextWindowTokens(tokens int) ModelContextProfile {
	tier := TierFromContextWindowTokens(tokens)
	p := ModelContextProfile{
		ContextWindowTokens: tokens,
		Tier:                tier,
	}
	switch tier {
	case TierMicro:
		p.ReserveOutputTokens = 512
		p.MaxAgenticIterations = 5
	case TierSmall:
		p.ReserveOutputTokens = 512
		p.MaxAgenticIterations = 10
	default:
		p.ReserveOutputTokens = 4_096
		p.MaxAgenticIterations = 30
	}
	return p
}

// ─── Built-in model registrations ─────────────────────────────────────────────

func init() {
	// Micro tier (< 8K context window)
	for _, entry := range []struct {
		pattern string
		tokens  int
	}{
		{"phi-3-mini", 4_096},
		{"phi-3", 4_096},
		{"llama-3.2-1b", 4_096},
		{"qwen2.5-0.5b", 4_096},
		{"qwen2.5-1.5b", 4_096},
		{"tinyllama", 2_048},
		{"stablelm-2", 4_096},
	} {
		RegisterModelContextPattern(entry.pattern, ModelContextProfile{
			ContextWindowTokens:  entry.tokens,
			ReserveOutputTokens:  512,
			Tier:                 TierMicro,
			MaxAgenticIterations: 5,
		})
	}

	// Small tier (8K–16K context window)
	for _, entry := range []struct {
		pattern string
		tokens  int
	}{
		{"gemma-2b", 8_192},
		{"gemma-7b", 8_192},
		{"gemma-2-2b", 8_192},
		{"llama-3.2-3b", 8_192},
		{"mistral-7b", 8_192},
		{"llama-2-7b", 4_096}, // Llama 2 only has 4K but putting as small for iterations
		{"llama-2-13b", 4_096},
		{"codellama-7b", 16_384},
		{"deepseek-coder-1.3b", 16_384},
	} {
		tier := TierSmall
		reserve := 512
		maxIter := 10
		if entry.tokens < 8_192 {
			tier = TierMicro
			maxIter = 5
		}
		RegisterModelContextPattern(entry.pattern, ModelContextProfile{
			ContextWindowTokens:  entry.tokens,
			ReserveOutputTokens:  reserve,
			Tier:                 tier,
			MaxAgenticIterations: maxIter,
		})
	}
}
