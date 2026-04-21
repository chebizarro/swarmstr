package context

import "testing"

func TestAutoCompactState_InitiallyOpen(t *testing.T) {
	s := NewAutoCompactState()
	if s.ShouldSkipCompaction("sess1") {
		t.Error("expected circuit to be closed initially")
	}
}

func TestAutoCompactState_OpensAfterMaxFailures(t *testing.T) {
	s := NewAutoCompactState()
	for i := 0; i < DefaultMaxConsecutiveFailures; i++ {
		s.RecordFailure("sess1")
	}
	if !s.ShouldSkipCompaction("sess1") {
		t.Error("expected circuit to open after max failures")
	}
}

func TestAutoCompactState_SuccessResetsCircuit(t *testing.T) {
	s := NewAutoCompactState()
	for i := 0; i < DefaultMaxConsecutiveFailures; i++ {
		s.RecordFailure("sess1")
	}
	if !s.ShouldSkipCompaction("sess1") {
		t.Fatal("expected circuit to be open")
	}
	s.RecordSuccess("sess1")
	if s.ShouldSkipCompaction("sess1") {
		t.Error("expected circuit to close after success")
	}
}

func TestAutoCompactState_SessionIsolation(t *testing.T) {
	s := NewAutoCompactState()
	for i := 0; i < DefaultMaxConsecutiveFailures; i++ {
		s.RecordFailure("sess1")
	}
	if s.ShouldSkipCompaction("sess2") {
		t.Error("circuit breaker should be per-session")
	}
}

func TestAutoCompactState_ResetClearsState(t *testing.T) {
	s := NewAutoCompactState()
	for i := 0; i < DefaultMaxConsecutiveFailures; i++ {
		s.RecordFailure("sess1")
	}
	s.Reset("sess1")
	if s.ShouldSkipCompaction("sess1") {
		t.Error("expected circuit to close after reset")
	}
	if s.ConsecutiveFailures("sess1") != 0 {
		t.Error("expected 0 failures after reset")
	}
}

func TestAutoCompactState_FailureCountTracking(t *testing.T) {
	s := NewAutoCompactState()
	if s.ConsecutiveFailures("sess1") != 0 {
		t.Error("expected 0 for unknown session")
	}
	count := s.RecordFailure("sess1")
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
	count = s.RecordFailure("sess1")
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}
}

func TestAutoCompactState_NotOpenBeforeMax(t *testing.T) {
	s := NewAutoCompactState()
	for i := 0; i < DefaultMaxConsecutiveFailures-1; i++ {
		s.RecordFailure("sess1")
	}
	if s.ShouldSkipCompaction("sess1") {
		t.Error("should not be open before reaching max failures")
	}
}
