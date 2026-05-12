package toolloop

import (
	"testing"
)

func TestDetect_Disabled(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.Enabled = false
	r := Detect(s, "foo", nil, &cfg)
	if r.Stuck {
		t.Fatal("expected not stuck when disabled")
	}
}

func TestDetect_NoHistory(t *testing.T) {
	s := NewState()
	r := Detect(s, "foo", map[string]any{"x": 1}, nil)
	if r.Stuck {
		t.Fatal("expected not stuck with no history")
	}
}

func TestRecordCall_SlidingWindow(t *testing.T) {
	s := NewState()
	cfg := Config{Enabled: true, HistorySize: 5}
	for i := 0; i < 10; i++ {
		RecordCall(s, "t", nil, "", &cfg)
	}
	s.mu.Lock()
	n := len(s.history)
	s.mu.Unlock()
	if n != 5 {
		t.Fatalf("expected history size 5, got %d", n)
	}
}

func TestDetect_GenericRepeat_Warning(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.WarningThreshold = 3
	params := map[string]any{"q": "hello"}
	for i := 0; i < 3; i++ {
		RecordCall(s, "search", params, "", &cfg)
	}
	r := Detect(s, "search", params, &cfg)
	if !r.Stuck {
		t.Fatal("expected stuck")
	}
	if r.Level != Warning {
		t.Fatalf("expected warning, got %s", r.Level)
	}
	if r.Detector != GenericRepeat {
		t.Fatalf("expected generic_repeat, got %s", r.Detector)
	}
}

func TestDetect_NoProgressStreak_Critical(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.WarningThreshold = 3
	cfg.CriticalThreshold = 5
	params := map[string]any{"id": "abc"}
	for i := 0; i < 5; i++ {
		RecordCall(s, "command_status", params, "", &cfg)
		RecordOutcome(s, "command_status", params, "", "same result", "", &cfg)
	}
	r := Detect(s, "command_status", params, &cfg)
	if !r.Stuck {
		t.Fatal("expected stuck")
	}
	if r.Level != Critical {
		t.Fatalf("expected critical, got %s", r.Level)
	}
	if r.Detector != KnownPollNoProgress {
		t.Fatalf("expected known_poll_no_progress, got %s", r.Detector)
	}
}

func TestDetect_NoProgressStreak_Warning(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.WarningThreshold = 3
	cfg.CriticalThreshold = 6
	params := map[string]any{"action": "poll", "id": "abc"}
	for i := 0; i < 3; i++ {
		RecordCall(s, "process", params, "", &cfg)
		RecordOutcome(s, "process", params, "", "same result", "", &cfg)
	}
	r := Detect(s, "process", params, &cfg)
	if !r.Stuck {
		t.Fatal("expected stuck")
	}
	if r.Level != Warning {
		t.Fatalf("expected warning, got %s", r.Level)
	}
	if r.Detector != KnownPollNoProgress {
		t.Fatalf("expected known_poll_no_progress, got %s", r.Detector)
	}
}

func TestDetect_DifferentResults_NoLoop(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.WarningThreshold = 3
	params := map[string]any{"id": "abc"}
	for i := 0; i < 5; i++ {
		RecordCall(s, "search", params, "", &cfg)
		RecordOutcome(s, "search", params, "", "result-"+string(rune('a'+i)), "", &cfg)
	}
	r := Detect(s, "search", params, &cfg)
	// Different results each time → no no-progress streak.
	// But 5 identical arg calls for a non-poll tool → generic repeat should trigger.
	if !r.Stuck {
		t.Fatal("expected stuck from generic repeat")
	}
	if r.Detector != GenericRepeat {
		t.Fatalf("expected generic_repeat, got %s", r.Detector)
	}
}

func TestDetect_NonPollNoProgress_DoesNotUseKnownPollDetector(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.WarningThreshold = 3
	cfg.CriticalThreshold = 5
	cfg.GlobalCircuitBreakerThreshold = 10
	params := map[string]any{"q": "hello"}
	for i := 0; i < 5; i++ {
		RecordCall(s, "search", params, "", &cfg)
		RecordOutcome(s, "search", params, "", "same result", "", &cfg)
	}
	r := Detect(s, "search", params, &cfg)
	if !r.Stuck {
		t.Fatal("expected stuck from generic repeat")
	}
	if r.Detector != GenericRepeat {
		t.Fatalf("expected generic_repeat, got %s", r.Detector)
	}
	if r.Level != Warning {
		t.Fatalf("expected warning, got %s", r.Level)
	}
}

func TestDetect_GlobalCircuitBreaker(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.HistorySize = 35
	cfg.GlobalCircuitBreakerThreshold = 10
	cfg.CriticalThreshold = 8
	cfg.WarningThreshold = 5
	params := map[string]any{"x": 1}
	for i := 0; i < 10; i++ {
		RecordCall(s, "tool", params, "", &cfg)
		RecordOutcome(s, "tool", params, "", "same", "", &cfg)
	}
	r := Detect(s, "tool", params, &cfg)
	if !r.Stuck {
		t.Fatal("expected stuck")
	}
	if r.Detector != GlobalCircuitBreaker {
		t.Fatalf("expected global_circuit_breaker, got %s", r.Detector)
	}
}

func TestDetect_PingPong_Warning(t *testing.T) {
	s := NewState()
	cfg := DefaultConfig()
	cfg.WarningThreshold = 4
	cfg.CriticalThreshold = 8
	paramsA := map[string]any{"tool": "a"}
	paramsB := map[string]any{"tool": "b"}

	// Build alternating pattern: A B A B
	for i := 0; i < 2; i++ {
		RecordCall(s, "read", paramsA, "", &cfg)
		RecordOutcome(s, "read", paramsA, "", "r1", "", &cfg)
		RecordCall(s, "write", paramsB, "", &cfg)
		RecordOutcome(s, "write", paramsB, "", "r2", "", &cfg)
	}
	// Next call would be A again → ping-pong count = 5 (4 history + 1 current)
	r := Detect(s, "read", paramsA, &cfg)
	if !r.Stuck {
		t.Fatal("expected stuck from ping-pong")
	}
	if r.Detector != PingPong {
		t.Fatalf("expected ping_pong, got %s", r.Detector)
	}
	if r.Level != Warning {
		t.Fatalf("expected warning, got %s", r.Level)
	}
}

func TestRecordOutcome_MatchesByToolCallID(t *testing.T) {
	s := NewState()
	params := map[string]any{"q": "test"}
	RecordCall(s, "search", params, "call-1", nil)
	RecordCall(s, "search", params, "call-2", nil)
	RecordOutcome(s, "search", params, "call-2", "result-2", "", nil)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history[0].ResultHash != "" {
		t.Fatal("call-1 should not have result hash")
	}
	if s.history[1].ResultHash == "" {
		t.Fatal("call-2 should have result hash")
	}
}

func TestHashToolCall_Deterministic(t *testing.T) {
	a := HashToolCall("foo", map[string]any{"b": 2, "a": 1})
	b := HashToolCall("foo", map[string]any{"a": 1, "b": 2})
	if a != b {
		t.Fatal("expected identical hashes for same params in different order")
	}
}

func TestHashToolCall_DifferentToolNames(t *testing.T) {
	a := HashToolCall("foo", map[string]any{"x": 1})
	b := HashToolCall("bar", map[string]any{"x": 1})
	if a == b {
		t.Fatal("expected different hashes for different tool names")
	}
}

func TestRegistry_GetCreateAndReuse(t *testing.T) {
	reg := NewRegistry()
	s1 := reg.Get("session-1")
	s2 := reg.Get("session-1")
	if s1 != s2 {
		t.Fatal("expected same state for same session")
	}
	s3 := reg.Get("session-2")
	if s1 == s3 {
		t.Fatal("expected different state for different session")
	}
}

func TestRegistry_Remove(t *testing.T) {
	reg := NewRegistry()
	s1 := reg.Get("s1")
	RecordCall(s1, "t", nil, "", nil)
	reg.Remove("s1")
	s2 := reg.Get("s1")
	s2.mu.Lock()
	n := len(s2.history)
	s2.mu.Unlock()
	if n != 0 {
		t.Fatal("expected empty history after remove")
	}
}

func TestStats(t *testing.T) {
	s := NewState()
	RecordCall(s, "a", map[string]any{"x": 1}, "", nil)
	RecordCall(s, "a", map[string]any{"x": 1}, "", nil)
	RecordCall(s, "b", map[string]any{"y": 2}, "", nil)
	total, unique, freqTool, freqCount := Stats(s)
	if total != 3 {
		t.Fatalf("expected 3 total, got %d", total)
	}
	if unique != 2 {
		t.Fatalf("expected 2 unique, got %d", unique)
	}
	if freqTool != "a" || freqCount != 2 {
		t.Fatalf("expected most frequent a:2, got %s:%d", freqTool, freqCount)
	}
}

func TestState_Reset(t *testing.T) {
	s := NewState()
	RecordCall(s, "t", nil, "", nil)
	s.Reset()
	s.mu.Lock()
	n := len(s.history)
	s.mu.Unlock()
	if n != 0 {
		t.Fatal("expected empty history after reset")
	}
}

func TestObserveTextThrash_WarningSameToolPlan(t *testing.T) {
	state := NewTextThrashState()
	cfg := DefaultConfig()
	cfg.TextThrash.WarningThreshold = 2
	cfg.TextThrash.CriticalThreshold = 3
	planKey := "read:{file:a}"

	if r := ObserveTextThrash(state, "Actually, I should inspect the file first.", planKey, &cfg); r.Stuck {
		t.Fatalf("first matching response should not warn: %+v", r)
	}
	r := ObserveTextThrash(state, "Wait, I need to inspect the file first.", planKey, &cfg)
	if !r.Stuck {
		t.Fatal("expected warning on repeated self-correction with unchanged plan")
	}
	if r.Level != Warning {
		t.Fatalf("expected warning, got %s", r.Level)
	}
	if r.Detector != TextDecisionThrash {
		t.Fatalf("expected text_decision_thrash, got %s", r.Detector)
	}
	if r.Count != 2 {
		t.Fatalf("expected count 2, got %d", r.Count)
	}
	if r.WarningKey == "" {
		t.Fatal("expected warning key")
	}
}

func TestObserveTextThrash_CriticalSameToolPlan(t *testing.T) {
	state := NewTextThrashState()
	cfg := DefaultConfig()
	cfg.TextThrash.WarningThreshold = 2
	cfg.TextThrash.CriticalThreshold = 3
	planKey := "read:{file:a}"

	ObserveTextThrash(state, "Actually, I'll read it.", planKey, &cfg)
	ObserveTextThrash(state, "Wait, I'll read it.", planKey, &cfg)
	r := ObserveTextThrash(state, "Hold on, I'll read it.", planKey, &cfg)
	if !r.Stuck {
		t.Fatal("expected critical on third repeated self-correction")
	}
	if r.Level != Critical {
		t.Fatalf("expected critical, got %s", r.Level)
	}
	if r.Detector != TextDecisionThrash {
		t.Fatalf("expected text_decision_thrash, got %s", r.Detector)
	}
	if r.Count != 3 {
		t.Fatalf("expected count 3, got %d", r.Count)
	}
}

func TestObserveTextThrash_ResetOnToolPlanChange(t *testing.T) {
	state := NewTextThrashState()
	cfg := DefaultConfig()
	cfg.TextThrash.WarningThreshold = 2
	cfg.TextThrash.CriticalThreshold = 3

	ObserveTextThrash(state, "Actually, I'll read it.", "plan-a", &cfg)
	r := ObserveTextThrash(state, "Wait, I'll search instead.", "plan-b", &cfg)
	if r.Stuck {
		t.Fatalf("expected reset on changed tool plan, got %+v", r)
	}
	if state.ConsecutiveCount != 1 || state.LastToolPlanKey != "plan-b" {
		t.Fatalf("expected streak to restart on plan-b, state=%+v", state)
	}

	r = ObserveTextThrash(state, "Actually, I'll search instead.", "plan-b", &cfg)
	if !r.Stuck || r.Level != Warning || r.Count != 2 {
		t.Fatalf("expected warning after second matching plan-b response, got %+v", r)
	}
}

func TestObserveTextThrash_ResetOnNonCorrectionText(t *testing.T) {
	state := NewTextThrashState()
	cfg := DefaultConfig()
	cfg.TextThrash.WarningThreshold = 2
	cfg.TextThrash.CriticalThreshold = 3
	planKey := "plan-a"

	ObserveTextThrash(state, "Actually, I'll read it.", planKey, &cfg)
	r := ObserveTextThrash(state, "I will read the file now.", planKey, &cfg)
	if r.Stuck {
		t.Fatalf("expected non-correction text to be ignored, got %+v", r)
	}
	if state.ConsecutiveCount != 0 || state.LastToolPlanKey != "" {
		t.Fatalf("expected non-correction text to reset state, state=%+v", state)
	}

	r = ObserveTextThrash(state, "Actually, I'll read it.", planKey, &cfg)
	if r.Stuck {
		t.Fatalf("expected streak to restart after reset, got %+v", r)
	}
	if state.ConsecutiveCount != 1 {
		t.Fatalf("expected restarted streak count 1, got %d", state.ConsecutiveCount)
	}
}

func TestObserveTextThrash_PatternMatching(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TextThrash.WarningThreshold = 1
	cfg.TextThrash.CriticalThreshold = 3
	planKey := "plan-a"

	matches := []string{
		"Actually, I should inspect this first.",
		"  > **Wait,** let me inspect this first.",
		"Hold on: let me inspect this first.",
		"On second thought, I should inspect this first.",
		"Let me try again with the same call.",
		"Let me correct that with the same call.",
		"I should instead inspect this first.",
	}
	for _, text := range matches {
		state := NewTextThrashState()
		r := ObserveTextThrash(state, text, planKey, &cfg)
		if !r.Stuck || r.Level != Warning {
			t.Fatalf("expected %q to match self-correction marker, got %+v", text, r)
		}
	}

	nonMatches := []string{
		"Waiting for the command to finish.",
		"Here is the plan. Actually, I should inspect this first.",
		"The actual result is ready.",
	}
	for _, text := range nonMatches {
		state := NewTextThrashState()
		r := ObserveTextThrash(state, text, planKey, &cfg)
		if r.Stuck {
			t.Fatalf("expected %q not to match self-correction marker, got %+v", text, r)
		}
	}
}

func TestObserveTextThrash_ConfigNormalization(t *testing.T) {
	rc := resolveConfig(&Config{
		Enabled: true,
		TextThrash: TextThrashConfig{
			Enabled:           true,
			WarningThreshold:  0,
			CriticalThreshold: 1,
			PrefixWindowChars: 0,
		},
	})
	if rc.TextThrash.WarningThreshold != defaultTextThrashWarningThreshold {
		t.Fatalf("expected default warning threshold %d, got %d", defaultTextThrashWarningThreshold, rc.TextThrash.WarningThreshold)
	}
	if rc.TextThrash.CriticalThreshold != defaultTextThrashWarningThreshold+1 {
		t.Fatalf("expected critical threshold adjusted above warning, got %d", rc.TextThrash.CriticalThreshold)
	}
	if rc.TextThrash.PrefixWindowChars != defaultTextThrashPrefixWindowChars {
		t.Fatalf("expected default prefix window %d, got %d", defaultTextThrashPrefixWindowChars, rc.TextThrash.PrefixWindowChars)
	}

	rc = resolveConfig(&Config{Enabled: true})
	if !rc.TextThrash.Enabled {
		t.Fatal("expected zero nested text-thrash config to resolve to enabled defaults")
	}
	if rc.TextThrash.WarningThreshold != defaultTextThrashWarningThreshold || rc.TextThrash.CriticalThreshold != defaultTextThrashCriticalThreshold {
		t.Fatalf("expected default text-thrash thresholds, got %+v", rc.TextThrash)
	}
}
