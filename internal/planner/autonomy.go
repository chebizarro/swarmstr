// autonomy.go provides auditable autonomy mode transitions with lifecycle
// events, authorization, and hot-apply/restart semantics.
package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Transition events ──────────────────────────────────────────────────────────

// AutonomyEvent records an auditable autonomy mode change.
type AutonomyEvent struct {
	// EventID uniquely identifies this transition event.
	EventID string `json:"event_id"`
	// OldMode is the autonomy mode before the transition.
	OldMode state.AutonomyMode `json:"old_mode"`
	// NewMode is the autonomy mode after the transition.
	NewMode state.AutonomyMode `json:"new_mode"`
	// Actor is the operator or system component that initiated the change.
	Actor string `json:"actor"`
	// Reason is a human-readable explanation for the transition.
	Reason string `json:"reason"`
	// Scope identifies what the mode change applies to (e.g. "config", "goal:g1", "task:t1").
	Scope string `json:"scope"`
	// ApplyMode describes whether the change takes effect immediately or requires restart.
	ApplyMode ApplyMode `json:"apply_mode"`
	// CreatedAt is the Unix timestamp of the transition.
	CreatedAt int64 `json:"created_at"`
	// Meta holds optional structured metadata.
	Meta map[string]any `json:"meta,omitempty"`
}

// ApplyMode describes when a mode transition takes effect.
type ApplyMode string

const (
	// ApplyHot means the mode change takes effect immediately for new actions.
	// In-flight operations complete under the old mode.
	ApplyHot ApplyMode = "hot"
	// ApplyNextRun means the mode change takes effect on the next task run.
	ApplyNextRun ApplyMode = "next_run"
	// ApplyRestart means the change requires a full restart to take effect.
	ApplyRestart ApplyMode = "restart"
)

// ── Transition rules ───────────────────────────────────────────────────────────

// TransitionPolicy governs which autonomy mode transitions are permitted
// and how they apply.
type TransitionPolicy struct {
	// AllowTighten permits transitions to more restrictive modes.
	AllowTighten bool
	// AllowLoosen permits transitions to less restrictive modes.
	AllowLoosen bool
	// RequireReason mandates a non-empty reason for any transition.
	RequireReason bool
}

// DefaultTransitionPolicy returns a reasonable default: tightening is always
// allowed (can always become more restrictive), loosening requires explicit
// permission.
func DefaultTransitionPolicy() TransitionPolicy {
	return TransitionPolicy{
		AllowTighten:  true,
		AllowLoosen:   false,
		RequireReason: true,
	}
}

// OperatorTransitionPolicy returns a policy for operator-initiated changes:
// both directions allowed, reason required.
func OperatorTransitionPolicy() TransitionPolicy {
	return TransitionPolicy{
		AllowTighten:  true,
		AllowLoosen:   true,
		RequireReason: true,
	}
}

// classifyTransition determines whether old → new is tightening, loosening, or same.
func classifyTransition(old, new state.AutonomyMode) string {
	oldRank, okOld := autonomyRank[old]
	newRank, okNew := autonomyRank[new]
	if !okOld {
		oldRank = 3 // unknown → supervised
	}
	if !okNew {
		newRank = 3
	}
	if newRank > oldRank {
		return "tighten"
	}
	if newRank < oldRank {
		return "loosen"
	}
	return "same"
}

// determineApplyMode decides whether a mode transition can be hot-applied.
// Transitions to more restrictive modes are hot-applied (safe default).
// Transitions to less restrictive modes apply on the next run.
func determineApplyMode(old, new state.AutonomyMode) ApplyMode {
	switch classifyTransition(old, new) {
	case "tighten":
		return ApplyHot // more restrictive = safe to apply immediately
	case "loosen":
		return ApplyNextRun // less restrictive = wait for next run boundary
	default:
		return ApplyHot // same mode = no-op, hot is fine
	}
}

// ── Autonomy controller ────────────────────────────────────────────────────────

// AutonomyController manages autonomy mode transitions with audit logging
// and policy enforcement.
type AutonomyController struct {
	mu     sync.RWMutex
	policy TransitionPolicy
	events []AutonomyEvent
	nextID int64
}

// NewAutonomyController creates a controller with the given transition policy.
func NewAutonomyController(policy TransitionPolicy) *AutonomyController {
	return &AutonomyController{policy: policy}
}

// TransitionRequest describes a proposed autonomy mode change.
type TransitionRequest struct {
	// OldMode is the current mode.
	OldMode state.AutonomyMode
	// NewMode is the desired mode.
	NewMode state.AutonomyMode
	// Actor identifies who is making the change.
	Actor string
	// Reason explains why the change is being made.
	Reason string
	// Scope identifies what the change applies to.
	Scope string
	// Now is the Unix timestamp (0 = use current time).
	Now int64
	// Meta holds optional metadata.
	Meta map[string]any
}

// Transition validates and records an autonomy mode change.
// Returns the resulting event and any validation error.
func (c *AutonomyController) Transition(req TransitionRequest) (AutonomyEvent, error) {
	if strings.TrimSpace(req.Actor) == "" {
		return AutonomyEvent{}, fmt.Errorf("transition: actor is required")
	}
	if !req.OldMode.Valid() && req.OldMode != "" {
		return AutonomyEvent{}, fmt.Errorf("transition: invalid old_mode %q", req.OldMode)
	}
	if !req.NewMode.Valid() {
		return AutonomyEvent{}, fmt.Errorf("transition: invalid new_mode %q", req.NewMode)
	}
	if c.policy.RequireReason && strings.TrimSpace(req.Reason) == "" {
		return AutonomyEvent{}, fmt.Errorf("transition: reason is required by policy")
	}

	direction := classifyTransition(req.OldMode, req.NewMode)
	switch direction {
	case "tighten":
		if !c.policy.AllowTighten {
			return AutonomyEvent{}, fmt.Errorf("transition: tightening (%s → %s) not permitted by policy",
				req.OldMode, req.NewMode)
		}
	case "loosen":
		if !c.policy.AllowLoosen {
			return AutonomyEvent{}, fmt.Errorf("transition: loosening (%s → %s) not permitted by policy",
				req.OldMode, req.NewMode)
		}
	case "same":
		// No-op transitions are always allowed.
	}

	now := req.Now
	if now <= 0 {
		now = time.Now().Unix()
	}

	c.mu.Lock()
	c.nextID++
	eventID := fmt.Sprintf("aut-%d", c.nextID)
	event := AutonomyEvent{
		EventID:   eventID,
		OldMode:   req.OldMode,
		NewMode:   req.NewMode,
		Actor:     req.Actor,
		Reason:    req.Reason,
		Scope:     req.Scope,
		ApplyMode: determineApplyMode(req.OldMode, req.NewMode),
		CreatedAt: now,
		Meta:      req.Meta,
	}
	c.events = append(c.events, event)
	c.mu.Unlock()

	return event, nil
}

// Events returns all recorded transition events.
func (c *AutonomyController) Events() []AutonomyEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]AutonomyEvent, len(c.events))
	copy(out, c.events)
	return out
}

// EventsForScope returns transition events filtered by scope.
func (c *AutonomyController) EventsForScope(scope string) []AutonomyEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []AutonomyEvent
	for _, e := range c.events {
		if e.Scope == scope {
			out = append(out, e)
		}
	}
	return out
}

// CurrentMode returns the effective mode for a scope by replaying events.
// If no events exist for the scope, returns the given default.
func (c *AutonomyController) CurrentMode(scope string, defaultMode state.AutonomyMode) state.AutonomyMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	mode := defaultMode
	for _, e := range c.events {
		if e.Scope == scope {
			mode = e.NewMode
		}
	}
	return mode
}

// ── Inspect helpers ────────────────────────────────────────────────────────────

// InspectMode returns a human-readable summary of the current autonomy
// mode for the given scope, including recent transition history.
func (c *AutonomyController) InspectMode(scope string, defaultMode state.AutonomyMode) string {
	mode := c.CurrentMode(scope, defaultMode)
	events := c.EventsForScope(scope)

	var b strings.Builder
	fmt.Fprintf(&b, "Scope: %s\n", scope)
	fmt.Fprintf(&b, "Current mode: %s\n", mode)
	if len(events) > 0 {
		fmt.Fprintf(&b, "Transition history (%d events):\n", len(events))
		// Show last 5 events.
		start := 0
		if len(events) > 5 {
			start = len(events) - 5
		}
		for _, e := range events[start:] {
			fmt.Fprintf(&b, "  [%s] %s → %s by %s (%s): %s\n",
				e.EventID, e.OldMode, e.NewMode, e.Actor, e.ApplyMode, e.Reason)
		}
	} else {
		b.WriteString("No transitions recorded (using default)\n")
	}
	return b.String()
}

// TransitionDirection returns "tighten", "loosen", or "same" for a
// proposed mode change. Exported for operator surfaces.
func TransitionDirection(old, new state.AutonomyMode) string {
	return classifyTransition(old, new)
}

// IsHotApplicable returns whether a mode transition can be applied
// immediately without waiting for a run boundary.
func IsHotApplicable(old, new state.AutonomyMode) bool {
	return determineApplyMode(old, new) == ApplyHot
}
