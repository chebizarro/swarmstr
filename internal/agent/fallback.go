package agent

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ─── Fallback chain ──────────────────────────────────────────────────────────
//
// FallbackChain wraps multiple ChatProvider candidates and tries them in order.
// When a provider fails, it is placed on cooldown and the next candidate is
// tried. This provides resilience against transient failures, rate limits, and
// authentication errors.

// FallbackCandidate is a named ChatProvider with a model identifier.
type FallbackCandidate struct {
	Name     string // e.g. "anthropic-primary", "anthropic-backup"
	Model    string // e.g. "claude-sonnet-4-5"
	Provider ChatProvider
}

// CooldownTracker tracks which providers are on cooldown after failures.
type CooldownTracker struct {
	mu       sync.RWMutex
	cooldown map[string]time.Time // provider name → cooldown expiry
}

// NewCooldownTracker creates a new CooldownTracker.
func NewCooldownTracker() *CooldownTracker {
	return &CooldownTracker{cooldown: make(map[string]time.Time)}
}

// SetCooldown places a provider on cooldown for the given duration.
func (t *CooldownTracker) SetCooldown(name string, dur time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cooldown[name] = time.Now().Add(dur)
}

// IsOnCooldown returns true if the provider is currently on cooldown.
func (t *CooldownTracker) IsOnCooldown(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	expiry, ok := t.cooldown[name]
	if !ok {
		return false
	}
	return time.Now().Before(expiry)
}

// FallbackChain tries multiple ChatProvider candidates in order.
type FallbackChain struct {
	candidates []FallbackCandidate
	cooldown   *CooldownTracker
}

// NewFallbackChain creates a new FallbackChain with the given candidates.
func NewFallbackChain(candidates []FallbackCandidate, cooldown *CooldownTracker) *FallbackChain {
	if cooldown == nil {
		cooldown = NewCooldownTracker()
	}
	return &FallbackChain{candidates: candidates, cooldown: cooldown}
}

// PromptCacheProfile returns the primary candidate's policy. Prompt assembly
// happens before fallback selection, so fallback chains optimize layout for the
// primary provider and pass that same request shape to later candidates if needed.
func (fc *FallbackChain) PromptCacheProfile() PromptCacheProfile {
	if fc == nil || len(fc.candidates) == 0 {
		return disabledPromptCacheProfile()
	}
	if provider, ok := fc.candidates[0].Provider.(promptCacheProfileProvider); ok {
		return provider.PromptCacheProfile()
	}
	return disabledPromptCacheProfile()
}

// FallbackResult holds the result of a fallback chain execution.
type FallbackResult struct {
	Response *LLMResponse
	Provider string // name of the provider that succeeded
	Model    string
	Attempts int // total number of attempts before success
}

// Chat implements ChatProvider by trying each candidate in order.
func (fc *FallbackChain) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	if len(fc.candidates) == 0 {
		return nil, fmt.Errorf("fallback chain: no candidates configured")
	}

	// Single candidate: skip fallback logic, but still honor that candidate's
	// provider-native cache toggles.
	if len(fc.candidates) == 1 {
		candidate := fc.candidates[0]
		return candidate.Provider.Chat(ctx, messages, tools, chatOptionsForCandidate(opts, candidate.Provider))
	}

	var lastErr error
	attempts := 0

	primaryProfile := fc.PromptCacheProfile()
	for idx, candidate := range fc.candidates {
		if idx > 0 {
			warnIfFallbackPromptCacheProfileMismatch(primaryProfile, candidate)
		}
		if fc.cooldown.IsOnCooldown(candidate.Name) {
			log.Printf("fallback: skipping %s (on cooldown)", candidate.Name)
			continue
		}

		attempts++
		resp, err := candidate.Provider.Chat(ctx, messages, tools, chatOptionsForCandidate(opts, candidate.Provider))
		if err == nil {
			if attempts > 1 {
				log.Printf("fallback: succeeded with %s/%s after %d attempts", candidate.Name, candidate.Model, attempts)
			}
			return resp, nil
		}

		lastErr = err
		reason := classifyError(err)
		cooldownDur := cooldownForReason(reason)

		log.Printf("fallback: %s/%s failed (reason=%s, cooldown=%s): %v",
			candidate.Name, candidate.Model, reason, cooldownDur, err)

		if cooldownDur > 0 {
			fc.cooldown.SetCooldown(candidate.Name, cooldownDur)
		}

		// Don't try more candidates for non-retriable errors.
		if !isRetriableReason(reason) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("fallback chain: all candidates failed: %w", lastErr)
}

// ─── Error classification ────────────────────────────────────────────────────
//
// Comprehensive error classification adapted from picoclaw/OpenClaw (~40 patterns).
// Classifies provider errors into failover reasons for fallback decisions.

type failoverReason string

const (
	reasonCanceled   failoverReason = "canceled"
	reasonAuth       failoverReason = "auth"
	reasonRateLimit  failoverReason = "rate_limit"
	reasonBilling    failoverReason = "billing"
	reasonOverloaded failoverReason = "overloaded"
	reasonTimeout    failoverReason = "timeout"
	reasonFormat     failoverReason = "format"
	reasonUnknown    failoverReason = "unknown"
)

// errorPattern defines a single pattern (string or regex) for error classification.
type errorPattern struct {
	substring string
	regex     *regexp.Regexp
}

func errSubstr(s string) errorPattern { return errorPattern{substring: s} }
func errRxp(r string) errorPattern    { return errorPattern{regex: regexp.MustCompile("(?i)" + r)} }

// Error patterns organized by failover reason.
var (
	rateLimitPatterns = []errorPattern{
		errRxp(`rate[_ ]limit`),
		errSubstr("too many requests"),
		errSubstr("429"),
		errSubstr("exceeded your current quota"),
		errRxp(`exceeded.*quota`),
		errRxp(`resource has been exhausted`),
		errRxp(`resource.*exhausted`),
		errSubstr("resource_exhausted"),
		errSubstr("quota exceeded"),
		errSubstr("usage limit"),
	}

	overloadedPatterns = []errorPattern{
		errRxp(`overloaded_error`),
		errRxp(`"type"\s*:\s*"overloaded_error"`),
		errSubstr("overloaded"),
	}

	timeoutPatterns = []errorPattern{
		errSubstr("timeout"),
		errSubstr("timed out"),
		errSubstr("deadline exceeded"),
		errSubstr("context deadline exceeded"),
	}

	billingPatterns = []errorPattern{
		errRxp(`\b402\b`),
		errSubstr("payment required"),
		errSubstr("insufficient credits"),
		errSubstr("credit balance"),
		errSubstr("plans & billing"),
		errSubstr("insufficient balance"),
	}

	authPatterns = []errorPattern{
		errRxp(`invalid[_ ]?api[_ ]?key`),
		errSubstr("incorrect api key"),
		errSubstr("invalid token"),
		errSubstr("authentication"),
		errSubstr("re-authenticate"),
		errSubstr("oauth token refresh failed"),
		errSubstr("unauthorized"),
		errSubstr("forbidden"),
		errSubstr("access denied"),
		errSubstr("expired"),
		errSubstr("token has expired"),
		errRxp(`\b401\b`),
		errRxp(`\b403\b`),
		errSubstr("no credentials found"),
		errSubstr("no api key found"),
	}

	formatPatterns = []errorPattern{
		errSubstr("string should match pattern"),
		errSubstr("tool_use.id"),
		errSubstr("tool_use_id"),
		errSubstr("invalid request format"),
	}

	// HTTP status code extraction patterns.
	httpStatusPatterns = []*regexp.Regexp{
		regexp.MustCompile(`status[:\s]+(\d{3})`),
		regexp.MustCompile(`http[/\s]+\d*\.?\d*\s+(\d{3})`),
		regexp.MustCompile(`\b([3-5]\d{2})\b`),
	}

	// Transient HTTP status codes that map to timeout.
	transientStatusCodes = map[int]bool{
		500: true, 502: true, 503: true,
		521: true, 522: true, 523: true, 524: true,
		529: true,
	}
)

// chatOptionsForCandidate keeps the primary prompt layout/option envelope but
// reapplies provider-native cache toggles for the candidate being called.
func chatOptionsForCandidate(opts ChatOptions, provider ChatProvider) ChatOptions {
	candidateOpts := opts
	if profileProvider, ok := provider.(promptCacheProfileProvider); ok {
		profile := profileProvider.PromptCacheProfile()
		candidateOpts.CacheSystem = profile.UseAnthropicCacheControl
		candidateOpts.CacheTools = profile.UseAnthropicCacheControl
	}
	return candidateOpts
}

func warnIfFallbackPromptCacheProfileMismatch(primary PromptCacheProfile, candidate FallbackCandidate) {
	profileProvider, ok := candidate.Provider.(promptCacheProfileProvider)
	if !ok {
		return
	}
	candidateProfile := profileProvider.PromptCacheProfile()
	if candidateProfile == primary {
		return
	}
	log.Printf("fallback: %s/%s prompt-cache profile differs from primary; using primary prompt layout with candidate-native cache flags", candidate.Name, candidate.Model)
}

// classifyError classifies an error into a failover reason.
// Context.Canceled returns reasonUnknown (non-retriable by convention).
// Context.DeadlineExceeded returns reasonTimeout.
func classifyError(err error) failoverReason {
	if err == nil {
		return reasonUnknown
	}

	// Context cancellation — user abort, don't fallback.
	if err == context.Canceled {
		return reasonCanceled
	}

	// Context deadline exceeded — treat as timeout.
	if err == context.DeadlineExceeded {
		return reasonTimeout
	}

	msg := strings.ToLower(err.Error())

	// Try HTTP status code extraction first.
	if status := extractHTTPStatus(msg); status > 0 {
		if r := classifyByStatus(status); r != "" {
			return r
		}
	}

	// Message pattern matching (priority order).
	return classifyByMessage(msg)
}

// classifyByStatus maps HTTP status codes to failover reasons.
func classifyByStatus(status int) failoverReason {
	switch {
	case status == 401 || status == 403:
		return reasonAuth
	case status == 402:
		return reasonBilling
	case status == 408:
		return reasonTimeout
	case status == 429:
		return reasonRateLimit
	case status == 400:
		return reasonFormat
	case transientStatusCodes[status]:
		return reasonTimeout
	}
	return ""
}

// classifyByMessage matches error messages against patterns (priority order).
func classifyByMessage(msg string) failoverReason {
	if matchesAnyPattern(msg, rateLimitPatterns) {
		return reasonRateLimit
	}
	if matchesAnyPattern(msg, overloadedPatterns) {
		return reasonOverloaded
	}
	if matchesAnyPattern(msg, billingPatterns) {
		return reasonBilling
	}
	if matchesAnyPattern(msg, timeoutPatterns) {
		return reasonTimeout
	}
	if matchesAnyPattern(msg, authPatterns) {
		return reasonAuth
	}
	if matchesAnyPattern(msg, formatPatterns) {
		return reasonFormat
	}
	return reasonUnknown
}

// extractHTTPStatus extracts an HTTP status code from an error message.
func extractHTTPStatus(msg string) int {
	for _, p := range httpStatusPatterns {
		if m := p.FindStringSubmatch(msg); len(m) > 1 {
			return parseStatusDigits(m[1])
		}
	}
	return 0
}

// matchesAnyPattern checks if msg matches any of the patterns.
func matchesAnyPattern(msg string, patterns []errorPattern) bool {
	for _, p := range patterns {
		if p.regex != nil {
			if p.regex.MatchString(msg) {
				return true
			}
		} else if p.substring != "" {
			if strings.Contains(msg, p.substring) {
				return true
			}
		}
	}
	return false
}

// parseStatusDigits converts a string of digits to an int.
func parseStatusDigits(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func cooldownForReason(reason failoverReason) time.Duration {
	switch reason {
	case reasonAuth:
		return 5 * time.Minute
	case reasonBilling:
		return 10 * time.Minute
	case reasonRateLimit:
		return 30 * time.Second
	case reasonOverloaded:
		return 60 * time.Second
	case reasonTimeout:
		return 10 * time.Second
	case reasonFormat:
		return 0 // format errors won't fix themselves
	default:
		return 15 * time.Second
	}
}

func isRetriableReason(reason failoverReason) bool {
	return reason != reasonFormat && reason != reasonCanceled
}
