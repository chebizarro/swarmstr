package planner

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── Side-effect classification ───────────────────────────────────────────────

// SideEffectClass describes the replay safety of a tool or action.
type SideEffectClass string

const (
	// SideEffectPure indicates a read-only action that can be freely replayed.
	SideEffectPure SideEffectClass = "pure"
	// SideEffectRetryable indicates a write action that is naturally idempotent
	// (e.g., upserts, replaceable events). Safe to retry.
	SideEffectRetryable SideEffectClass = "retryable"
	// SideEffectSideEffectful indicates a write action that may cause duplicate
	// external effects on replay (e.g., publishing a new event, sending a DM,
	// executing a zap). Requires idempotency protection.
	SideEffectSideEffectful SideEffectClass = "side_effectful"
)

// validSideEffectClasses for validation.
var validSideEffectClasses = map[SideEffectClass]bool{
	SideEffectPure:          true,
	SideEffectRetryable:     true,
	SideEffectSideEffectful: true,
}

// ValidSideEffectClass reports whether c is a known side-effect class.
func ValidSideEffectClass(c SideEffectClass) bool {
	return validSideEffectClasses[c]
}

// ClassifySideEffect classifies a tool by its replay safety.
// This is the default heuristic — it can be overridden per-tool via
// ToolSideEffectOverrides.
func ClassifySideEffect(tool string) SideEffectClass {
	lower := strings.ToLower(tool)

	// Pure (read-only) tools.
	for _, prefix := range []string{
		"fetch", "get", "list", "search", "info", "ping", "resolve",
		"relay_list", "relay_info", "relay_ping",
		"nostr_fetch", "nostr_profile", "nostr_resolve",
		"nostr_follows", "nostr_followers", "nostr_wot",
		"nostr_relay_hints", "nostr_watch_list",
	} {
		if lower == prefix || strings.HasPrefix(lower, prefix+"_") {
			return SideEffectPure
		}
	}

	// Retryable (idempotent writes).
	// Replaceable events, profile updates, relay list updates, config updates.
	for _, keyword := range []string{
		"update_profile", "set_relay_list", "update_config",
		"set_", "upsert",
	} {
		if strings.Contains(lower, keyword) {
			return SideEffectRetryable
		}
	}

	// Side-effectful (non-idempotent writes).
	for _, keyword := range []string{
		"publish", "send", "dm", "broadcast", "zap", "pay",
		"delete", "remove", "drop", "create", "post",
		"nostr_publish", "nostr_send_dm", "nostr_zap",
		"nostr_watch", "nostr_unwatch",
	} {
		if lower == keyword || strings.Contains(lower, keyword) {
			return SideEffectSideEffectful
		}
	}

	// Unknown tools default to side-effectful for safety.
	return SideEffectSideEffectful
}

// IsSafeToReplay reports whether a tool can be replayed without risk of
// duplicate external effects.
func IsSafeToReplay(tool string) bool {
	c := ClassifySideEffect(tool)
	return c == SideEffectPure || c == SideEffectRetryable
}

// ── Idempotency keys ─────────────────────────────────────────────────────────

// IdempotencyKey is an opaque key used to deduplicate side-effectful operations.
type IdempotencyKey struct {
	Key       string `json:"key"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
	Tool      string `json:"tool"`
	Sequence  int64  `json:"sequence"`
	CreatedAt int64  `json:"created_at"`
}

// GenerateIdempotencyKey creates a deterministic idempotency key for a tool
// dispatch within a specific task run. The key is stable across restarts —
// replaying the same (runID, tool, sequence) always produces the same key.
func GenerateIdempotencyKey(taskID, runID, tool string, sequence int64) IdempotencyKey {
	raw := fmt.Sprintf("%s:%s:%s:%d", taskID, runID, tool, sequence)
	hash := sha256.Sum256([]byte(raw))
	key := fmt.Sprintf("idem-%x", hash[:16])
	return IdempotencyKey{
		Key:       key,
		TaskID:    taskID,
		RunID:     runID,
		Tool:      tool,
		Sequence:  sequence,
		CreatedAt: time.Now().Unix(),
	}
}

// ── Idempotency registry ─────────────────────────────────────────────────────

// IdempotencyOutcome records the result of a side-effectful operation keyed
// by its idempotency key.
type IdempotencyOutcome struct {
	Key         string         `json:"key"`
	Tool        string         `json:"tool"`
	Status      string         `json:"status"` // "completed", "failed", "pending"
	ResultRef   string         `json:"result_ref,omitempty"`
	CompletedAt int64          `json:"completed_at,omitempty"`
	Error       string         `json:"error,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// IdempotencyRegistry tracks executed side-effectful operations to prevent
// duplicate execution on replay.
//
// Thread safety: all public methods are safe for concurrent use.
type IdempotencyRegistry struct {
	mu       sync.RWMutex
	outcomes map[string]IdempotencyOutcome // key → outcome
}

// NewIdempotencyRegistry creates an empty registry.
func NewIdempotencyRegistry() *IdempotencyRegistry {
	return &IdempotencyRegistry{
		outcomes: make(map[string]IdempotencyOutcome),
	}
}

// Check returns the outcome for a key, or (zero, false) if not seen.
func (r *IdempotencyRegistry) Check(key string) (IdempotencyOutcome, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.outcomes[key]
	return o, ok
}

// Record stores the outcome of a side-effectful operation.
// If the key already exists, the record is NOT overwritten (first-write-wins).
// Returns true if the record was stored, false if it already existed.
func (r *IdempotencyRegistry) Record(outcome IdempotencyOutcome) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.outcomes[outcome.Key]; exists {
		return false
	}
	r.outcomes[outcome.Key] = outcome
	return true
}

// MarkCompleted records a successful completion for a key.
func (r *IdempotencyRegistry) MarkCompleted(key, tool, resultRef string, at int64) bool {
	return r.Record(IdempotencyOutcome{
		Key:         key,
		Tool:        tool,
		Status:      "completed",
		ResultRef:   resultRef,
		CompletedAt: at,
	})
}

// MarkFailed records a failure for a key. Failed operations CAN be retried
// (the key is recorded but does not block future attempts with a new key).
func (r *IdempotencyRegistry) MarkFailed(key, tool, errMsg string, at int64) bool {
	return r.Record(IdempotencyOutcome{
		Key:         key,
		Tool:        tool,
		Status:      "failed",
		Error:       errMsg,
		CompletedAt: at,
	})
}

// AlreadyExecuted reports whether a key has been successfully completed.
// Failed operations are NOT considered "already executed" — they can be retried.
func (r *IdempotencyRegistry) AlreadyExecuted(key string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.outcomes[key]
	return ok && o.Status == "completed"
}

// Len returns the number of recorded outcomes.
func (r *IdempotencyRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.outcomes)
}

// Outcomes returns a snapshot of all recorded outcomes.
func (r *IdempotencyRegistry) Outcomes() []IdempotencyOutcome {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]IdempotencyOutcome, 0, len(r.outcomes))
	for _, o := range r.outcomes {
		out = append(out, o)
	}
	return out
}

// RestoreOutcomes loads previously persisted outcomes into the registry.
// Used during recovery to prevent re-execution of completed operations.
func (r *IdempotencyRegistry) RestoreOutcomes(outcomes []IdempotencyOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range outcomes {
		if _, exists := r.outcomes[o.Key]; !exists {
			r.outcomes[o.Key] = o
		}
	}
}

// ── Dispatch guard ───────────────────────────────────────────────────────────

// DispatchGuard wraps tool dispatch with idempotency protection. It checks
// whether a tool invocation is safe to execute based on its side-effect class
// and idempotency state.
type DispatchGuard struct {
	registry *IdempotencyRegistry
}

// NewDispatchGuard creates a dispatch guard backed by the given registry.
func NewDispatchGuard(registry *IdempotencyRegistry) *DispatchGuard {
	return &DispatchGuard{registry: registry}
}

// DispatchDecision describes whether a tool call should proceed.
type DispatchDecision struct {
	// Allowed is true if the tool call should execute.
	Allowed bool `json:"allowed"`
	// Reason explains the decision.
	Reason string `json:"reason"`
	// IdempotencyKey is set for side-effectful tools.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// PriorOutcome is set when the key has already been executed.
	PriorOutcome *IdempotencyOutcome `json:"prior_outcome,omitempty"`
	// SideEffectClass is the classified side-effect type.
	SideEffectClass SideEffectClass `json:"side_effect_class"`
}

// ShouldDispatch evaluates whether a tool call should proceed.
//
// For pure/retryable tools: always allowed.
// For side-effectful tools: checks the idempotency registry. If the key
// was already completed, the call is blocked to prevent duplicates.
// If the key was failed, it is allowed (retry is safe with same key).
func (g *DispatchGuard) ShouldDispatch(idemKey IdempotencyKey) DispatchDecision {
	class := ClassifySideEffect(idemKey.Tool)

	if class == SideEffectPure || class == SideEffectRetryable {
		return DispatchDecision{
			Allowed:         true,
			Reason:          fmt.Sprintf("tool %s is %s; safe to execute", idemKey.Tool, class),
			SideEffectClass: class,
		}
	}

	// Side-effectful: check registry.
	if outcome, found := g.registry.Check(idemKey.Key); found {
		if outcome.Status == "completed" {
			return DispatchDecision{
				Allowed:         false,
				Reason:          fmt.Sprintf("tool %s already completed with key %s; skipping duplicate", idemKey.Tool, idemKey.Key),
				IdempotencyKey:  idemKey.Key,
				PriorOutcome:    &outcome,
				SideEffectClass: class,
			}
		}
		// Failed → allow retry.
		return DispatchDecision{
			Allowed:         true,
			Reason:          fmt.Sprintf("tool %s previously failed with key %s; retry allowed", idemKey.Tool, idemKey.Key),
			IdempotencyKey:  idemKey.Key,
			PriorOutcome:    &outcome,
			SideEffectClass: class,
		}
	}

	// Not seen before → allow.
	return DispatchDecision{
		Allowed:         true,
		Reason:          fmt.Sprintf("tool %s is side-effectful; first execution with key %s", idemKey.Tool, idemKey.Key),
		IdempotencyKey:  idemKey.Key,
		SideEffectClass: class,
	}
}

// ── Formatting ───────────────────────────────────────────────────────────────

// FormatDispatchDecision returns a human-readable description.
func FormatDispatchDecision(d DispatchDecision) string {
	var b strings.Builder
	if d.Allowed {
		fmt.Fprintf(&b, "✅ ALLOW: %s", d.Reason)
	} else {
		fmt.Fprintf(&b, "🚫 SKIP: %s", d.Reason)
	}
	if d.IdempotencyKey != "" {
		fmt.Fprintf(&b, " [key=%s]", d.IdempotencyKey)
	}
	return b.String()
}
