package planner

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/store/state"
)

// ── TransitionDirection tests ──────────────────────────────────────────────────

func TestTransitionDirection_Tighten(t *testing.T) {
	cases := [][2]state.AutonomyMode{
		{state.AutonomyFull, state.AutonomyPlanApproval},
		{state.AutonomyFull, state.AutonomySupervised},
		{state.AutonomyPlanApproval, state.AutonomyStepApproval},
		{state.AutonomyStepApproval, state.AutonomySupervised},
	}
	for _, c := range cases {
		if got := TransitionDirection(c[0], c[1]); got != "tighten" {
			t.Errorf("%s → %s: expected tighten, got %s", c[0], c[1], got)
		}
	}
}

func TestTransitionDirection_Loosen(t *testing.T) {
	cases := [][2]state.AutonomyMode{
		{state.AutonomySupervised, state.AutonomyFull},
		{state.AutonomyStepApproval, state.AutonomyPlanApproval},
		{state.AutonomyPlanApproval, state.AutonomyFull},
	}
	for _, c := range cases {
		if got := TransitionDirection(c[0], c[1]); got != "loosen" {
			t.Errorf("%s → %s: expected loosen, got %s", c[0], c[1], got)
		}
	}
}

func TestTransitionDirection_Same(t *testing.T) {
	modes := []state.AutonomyMode{
		state.AutonomyFull, state.AutonomyPlanApproval,
		state.AutonomyStepApproval, state.AutonomySupervised,
	}
	for _, m := range modes {
		if got := TransitionDirection(m, m); got != "same" {
			t.Errorf("%s → %s: expected same, got %s", m, m, got)
		}
	}
}

// ── ApplyMode tests ────────────────────────────────────────────────────────────

func TestIsHotApplicable_TighteningIsHot(t *testing.T) {
	if !IsHotApplicable(state.AutonomyFull, state.AutonomySupervised) {
		t.Error("tightening should be hot-applicable")
	}
}

func TestIsHotApplicable_LooseningIsNotHot(t *testing.T) {
	if IsHotApplicable(state.AutonomySupervised, state.AutonomyFull) {
		t.Error("loosening should not be hot-applicable")
	}
}

func TestIsHotApplicable_SameIsHot(t *testing.T) {
	if !IsHotApplicable(state.AutonomyFull, state.AutonomyFull) {
		t.Error("same mode should be hot-applicable")
	}
}

// ── Policy tests ───────────────────────────────────────────────────────────────

func TestDefaultPolicy_AllowsTightenOnly(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	// Tighten: allowed.
	_, err := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: state.AutonomySupervised,
		Actor:   "operator",
		Reason:  "lockdown",
		Scope:   "config",
		Now:     1000,
	})
	if err != nil {
		t.Fatalf("tighten should succeed: %v", err)
	}

	// Loosen: denied.
	_, err = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomySupervised,
		NewMode: state.AutonomyFull,
		Actor:   "operator",
		Reason:  "unlock",
		Scope:   "config",
		Now:     1001,
	})
	if err == nil {
		t.Fatal("loosen should be denied by default policy")
	}
	if !strings.Contains(err.Error(), "loosening") {
		t.Errorf("error should mention loosening: %v", err)
	}
}

func TestOperatorPolicy_AllowsBothDirections(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	// Tighten.
	_, err := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: state.AutonomySupervised,
		Actor:   "admin",
		Reason:  "testing",
		Scope:   "config",
		Now:     1000,
	})
	if err != nil {
		t.Fatalf("tighten: %v", err)
	}
	// Loosen.
	_, err = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomySupervised,
		NewMode: state.AutonomyFull,
		Actor:   "admin",
		Reason:  "restore",
		Scope:   "config",
		Now:     1001,
	})
	if err != nil {
		t.Fatalf("loosen: %v", err)
	}
}

func TestTransition_RequiresReason(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	_, err := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: state.AutonomySupervised,
		Actor:   "operator",
		Reason:  "", // empty
		Scope:   "config",
		Now:     1000,
	})
	if err == nil {
		t.Fatal("should require reason")
	}
}

func TestTransition_RequiresActor(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	_, err := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: state.AutonomySupervised,
		Actor:   "",
		Reason:  "test",
		Scope:   "config",
		Now:     1000,
	})
	if err == nil {
		t.Fatal("should require actor")
	}
}

func TestTransition_InvalidNewMode(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	_, err := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: "yolo",
		Actor:   "admin",
		Reason:  "test",
		Now:     1000,
	})
	if err == nil {
		t.Fatal("should reject invalid new_mode")
	}
}

func TestTransition_InvalidOldMode(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	_, err := ctrl.Transition(TransitionRequest{
		OldMode: "bogus",
		NewMode: state.AutonomyFull,
		Actor:   "admin",
		Reason:  "test",
		Now:     1000,
	})
	if err == nil {
		t.Fatal("should reject invalid old_mode")
	}
}

func TestTransition_SameModeAllowed(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	event, err := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: state.AutonomyFull,
		Actor:   "system",
		Reason:  "refresh",
		Scope:   "config",
		Now:     1000,
	})
	if err != nil {
		t.Fatalf("same-mode transition should succeed: %v", err)
	}
	if event.ApplyMode != ApplyHot {
		t.Errorf("same-mode should be hot, got %s", event.ApplyMode)
	}
}

// ── Event recording tests ──────────────────────────────────────────────────────

func TestEvents_AuditTrail(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	_, _ = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: state.AutonomyPlanApproval,
		Actor:   "admin",
		Reason:  "step 1",
		Scope:   "goal:g1",
		Now:     1000,
	})
	_, _ = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyPlanApproval,
		NewMode: state.AutonomySupervised,
		Actor:   "admin",
		Reason:  "step 2",
		Scope:   "goal:g1",
		Now:     1001,
	})
	events := ctrl.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventID == events[1].EventID {
		t.Error("event IDs should be unique")
	}
}

func TestEventsForScope_FiltersCorrectly(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	_, _ = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull, NewMode: state.AutonomyPlanApproval,
		Actor: "a", Reason: "r", Scope: "goal:g1", Now: 1000,
	})
	_, _ = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull, NewMode: state.AutonomySupervised,
		Actor: "a", Reason: "r", Scope: "task:t1", Now: 1001,
	})
	g1Events := ctrl.EventsForScope("goal:g1")
	if len(g1Events) != 1 {
		t.Errorf("expected 1 event for goal:g1, got %d", len(g1Events))
	}
	t1Events := ctrl.EventsForScope("task:t1")
	if len(t1Events) != 1 {
		t.Errorf("expected 1 event for task:t1, got %d", len(t1Events))
	}
	noEvents := ctrl.EventsForScope("nonexistent")
	if len(noEvents) != 0 {
		t.Errorf("expected 0 events for nonexistent, got %d", len(noEvents))
	}
}

func TestCurrentMode_DefaultWhenNoEvents(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	mode := ctrl.CurrentMode("config", state.AutonomyFull)
	if mode != state.AutonomyFull {
		t.Errorf("expected default full, got %s", mode)
	}
}

func TestCurrentMode_ReflectsLatestTransition(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	_, _ = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull, NewMode: state.AutonomyPlanApproval,
		Actor: "a", Reason: "r", Scope: "config", Now: 1000,
	})
	_, _ = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyPlanApproval, NewMode: state.AutonomySupervised,
		Actor: "a", Reason: "r", Scope: "config", Now: 1001,
	})
	mode := ctrl.CurrentMode("config", state.AutonomyFull)
	if mode != state.AutonomySupervised {
		t.Errorf("expected supervised after two transitions, got %s", mode)
	}
}

// ── Inspect tests ──────────────────────────────────────────────────────────────

func TestInspectMode_IncludesHistory(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	_, _ = ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull, NewMode: state.AutonomyPlanApproval,
		Actor: "admin", Reason: "testing", Scope: "config", Now: 1000,
	})
	output := ctrl.InspectMode("config", state.AutonomyFull)
	if !strings.Contains(output, "plan_approval") {
		t.Error("should show current mode")
	}
	if !strings.Contains(output, "admin") {
		t.Error("should show actor")
	}
	if !strings.Contains(output, "testing") {
		t.Error("should show reason")
	}
}

func TestInspectMode_NoEventsShowsDefault(t *testing.T) {
	ctrl := NewAutonomyController(DefaultTransitionPolicy())
	output := ctrl.InspectMode("config", state.AutonomyFull)
	if !strings.Contains(output, "No transitions") {
		t.Error("should note no transitions recorded")
	}
	if !strings.Contains(output, "full") {
		t.Error("should show default mode")
	}
}

// ── Event apply mode in events ─────────────────────────────────────────────────

func TestTransition_TightenEventIsHot(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	event, _ := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull, NewMode: state.AutonomySupervised,
		Actor: "a", Reason: "r", Scope: "config", Now: 1000,
	})
	if event.ApplyMode != ApplyHot {
		t.Errorf("tightening should be hot, got %s", event.ApplyMode)
	}
}

func TestTransition_LoosenEventIsNextRun(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	event, _ := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomySupervised, NewMode: state.AutonomyFull,
		Actor: "a", Reason: "r", Scope: "config", Now: 1000,
	})
	if event.ApplyMode != ApplyNextRun {
		t.Errorf("loosening should be next_run, got %s", event.ApplyMode)
	}
}

// ── Concurrency test ───────────────────────────────────────────────────────────

func TestAutonomyController_ConcurrentAccess(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = ctrl.Transition(TransitionRequest{
				OldMode: state.AutonomyFull,
				NewMode: state.AutonomyPlanApproval,
				Actor:   "worker",
				Reason:  "concurrent",
				Scope:   "config",
				Now:     int64(1000 + i),
			})
			_ = ctrl.CurrentMode("config", state.AutonomyFull)
			_ = ctrl.Events()
			_ = ctrl.EventsForScope("config")
		}(i)
	}
	wg.Wait()
	events := ctrl.Events()
	if len(events) != 50 {
		t.Errorf("expected 50 events, got %d", len(events))
	}
}

// ── JSON round-trip ────────────────────────────────────────────────────────────

func TestAutonomyEvent_JSONRoundTrip(t *testing.T) {
	event := AutonomyEvent{
		EventID:   "aut-1",
		OldMode:   state.AutonomyFull,
		NewMode:   state.AutonomySupervised,
		Actor:     "admin",
		Reason:    "lockdown",
		Scope:     "config",
		ApplyMode: ApplyHot,
		CreatedAt: 1000,
		Meta:      map[string]any{"source": "control"},
	}
	blob, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded AutonomyEvent
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.EventID != event.EventID || decoded.OldMode != event.OldMode ||
		decoded.NewMode != event.NewMode || decoded.Actor != event.Actor ||
		decoded.ApplyMode != event.ApplyMode {
		t.Errorf("round-trip mismatch: got %+v", decoded)
	}
}

// ── Policy with RequireReason=false ────────────────────────────────────────────

func TestTransition_NoReasonWhenPolicyAllows(t *testing.T) {
	policy := TransitionPolicy{
		AllowTighten:  true,
		AllowLoosen:   true,
		RequireReason: false,
	}
	ctrl := NewAutonomyController(policy)
	_, err := ctrl.Transition(TransitionRequest{
		OldMode: state.AutonomyFull,
		NewMode: state.AutonomySupervised,
		Actor:   "system",
		Reason:  "",
		Scope:   "config",
		Now:     1000,
	})
	if err != nil {
		t.Fatalf("should allow empty reason: %v", err)
	}
}

// ── Empty old mode (initial setup) ─────────────────────────────────────────────

func TestTransition_EmptyOldMode(t *testing.T) {
	ctrl := NewAutonomyController(OperatorTransitionPolicy())
	event, err := ctrl.Transition(TransitionRequest{
		OldMode: "",
		NewMode: state.AutonomyFull,
		Actor:   "system",
		Reason:  "initial setup",
		Scope:   "config",
		Now:     1000,
	})
	if err != nil {
		t.Fatalf("empty old mode should be allowed: %v", err)
	}
	if event.OldMode != "" {
		t.Errorf("should preserve empty old mode, got %q", event.OldMode)
	}
}
