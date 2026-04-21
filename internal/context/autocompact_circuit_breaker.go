package context

import "sync"

// ─── Autocompact circuit breaker ──────────────────────────────────────────────
//
// Prevents runaway compaction retries on irrecoverable contexts. After N
// consecutive failures, the circuit opens and further compaction attempts are
// skipped for that session until a successful compaction resets the counter.
//
// src/ found this was wasting ~250K API calls/day globally when contexts
// had a single massive prompt exceeding the window — compaction would fail,
// the next turn would trigger it again, and the cycle repeated indefinitely.
//
// Ported from src/services/compact/autoCompact.ts.

const (
	// DefaultMaxConsecutiveFailures is the number of consecutive compaction
	// failures before the circuit breaker opens for a session.
	DefaultMaxConsecutiveFailures = 3

	// DefaultAutoCompactBufferTokens is the token buffer to keep below
	// the context window limit, giving headroom for the response.
	DefaultAutoCompactBufferTokens = 13_000
)

// AutoCompactState tracks per-session autocompact circuit breaker state.
type AutoCompactState struct {
	mu    sync.Mutex
	state map[string]*autoCompactSessionState
}

type autoCompactSessionState struct {
	ConsecutiveFailures int
}

// NewAutoCompactState creates a new circuit breaker tracker.
func NewAutoCompactState() *AutoCompactState {
	return &AutoCompactState{
		state: make(map[string]*autoCompactSessionState),
	}
}

// ShouldSkipCompaction returns true if the circuit breaker is open for this
// session (too many consecutive failures). The caller should skip compaction.
func (s *AutoCompactState) ShouldSkipCompaction(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[sessionID]
	if !ok {
		return false
	}
	return st.ConsecutiveFailures >= DefaultMaxConsecutiveFailures
}

// RecordFailure increments the consecutive failure count for a session.
// Returns the new count.
func (s *AutoCompactState) RecordFailure(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.getOrCreate(sessionID)
	st.ConsecutiveFailures++
	return st.ConsecutiveFailures
}

// RecordSuccess resets the consecutive failure count for a session.
func (s *AutoCompactState) RecordSuccess(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.getOrCreate(sessionID)
	st.ConsecutiveFailures = 0
}

// Reset removes all tracking state for a session (e.g., on session rotation).
func (s *AutoCompactState) Reset(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, sessionID)
}

// ConsecutiveFailures returns the current failure count for a session.
func (s *AutoCompactState) ConsecutiveFailures(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.state[sessionID]; ok {
		return st.ConsecutiveFailures
	}
	return 0
}

func (s *AutoCompactState) getOrCreate(sessionID string) *autoCompactSessionState {
	st, ok := s.state[sessionID]
	if !ok {
		st = &autoCompactSessionState{}
		s.state[sessionID] = st
	}
	return st
}
