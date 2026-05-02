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

// normalizeModelID strips provider prefixes (e.g. "lemmy-local/") and file
// extensions (.gguf, .ggml) from a model ID so that registry patterns can
// match the bare model name regardless of how it was qualified.
func normalizeModelID(id string) string {
	// Strip provider prefix: take everything after the last '/'.
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	// Strip common quantization file extensions.
	for _, ext := range []string{".gguf", ".ggml", ".bin"} {
		if strings.HasSuffix(id, ext) {
			id = id[:len(id)-len(ext)]
			break
		}
	}
	return id
}

// resolveAgainstRegistry tries to find the longest matching pattern in the
// registry for the given (already-lowercased) candidate string.
func resolveAgainstRegistry(candidate string) (ModelContextProfile, bool) {
	bestLen := 0
	bestProfile := defaultStandardProfile
	matched := false

	for _, entry := range registry {
		if strings.HasPrefix(candidate, entry.pattern) && len(entry.pattern) > bestLen {
			bestLen = len(entry.pattern)
			bestProfile = entry.profile
			matched = true
		}
	}
	return bestProfile, matched
}

// ResolveModelContext returns the ModelContextProfile for the given model ID.
// It matches registered patterns as case-insensitive prefixes, preferring the
// longest match. Provider prefixes (e.g. "lemmy-local/") and file extensions
// (.gguf, .ggml) are stripped before matching so that qualified model IDs
// resolve correctly. Returns a default TierStandard profile (200K window)
// when no pattern matches.
func ResolveModelContext(modelID string) ModelContextProfile {
	if modelID == "" {
		return defaultStandardProfile
	}
	lowerID := strings.ToLower(modelID)

	registryMu.RLock()
	defer registryMu.RUnlock()

	// Try the raw (lowercased) ID first.
	if profile, ok := resolveAgainstRegistry(lowerID); ok {
		return profile
	}

	// Try the normalized form (provider prefix and extension stripped).
	normalized := normalizeModelID(lowerID)
	if normalized != lowerID {
		if profile, ok := resolveAgainstRegistry(normalized); ok {
			return profile
		}
	}

	return defaultStandardProfile
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
// count, using continuous proportional scaling for iterations and reserves.
func ProfileFromContextWindowTokens(tokens int) ModelContextProfile {
	if tokens <= 0 {
		tokens = 200_000
	}
	tier := TierFromContextWindowTokens(tokens)

	// MaxAgenticIterations: clamp(tokens/6000, 5, 30)
	maxIter := tokens / 6_000
	if maxIter < 5 {
		maxIter = 5
	}
	if maxIter > 30 {
		maxIter = 30
	}

	// ReserveOutputTokens: scale from 512 (tiny) to 4096 (200K+)
	t := float64(tokens) / 200_000.0
	if t > 1 {
		t = 1
	}
	reserve := int(lerp(512, 4_096, t))

	return ModelContextProfile{
		ContextWindowTokens:  tokens,
		Tier:                 tier,
		ReserveOutputTokens:  reserve,
		MaxAgenticIterations: maxIter,
	}
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
		// Gemma family (2B/7B/9B variants)
		{"gemma-2b", 8_192},
		{"gemma-7b", 8_192},
		{"gemma-9b", 8_192},
		{"gemma-2-2b", 8_192},
		{"gemma-2-9b", 8_192},
		{"gemma-2-27b", 8_192},
		{"gemma-3", 8_192},
		{"gemma-4", 8_192},
		{"google/gemma", 8_192},
		{"google_gemma", 8_192},
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
