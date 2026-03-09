package autoreply

import (
	"sync"
	"time"
)

// SessionTurns serialises agent invocations on a per-session basis.
//
// At most one agent turn runs at a time for each session ID.  If a second
// message arrives while a turn is still in progress, TryAcquire returns
// (nil, false) so the caller can reply immediately with a "busy" message
// instead of kicking off a concurrent agent run.
//
// The implementation uses a per-session *sync.Mutex stored in a sync.Map.
// Go 1.18+ sync.Mutex.TryLock is required.
type SessionTurns struct {
	locks sync.Map // map[string]*sync.Mutex
	known sync.Map // map[string]*sessionRecord — sessions registered via Track()
}

// NewSessionTurns creates an empty SessionTurns registry.
func NewSessionTurns() *SessionTurns {
	return &SessionTurns{}
}

// sessionRecord tracks a spawned session.
type sessionRecord struct {
	id        string
	agentID   string
	createdAt time.Time
}

// Track registers a session ID as known to the daemon.
// This is idempotent and safe for concurrent use.
func (s *SessionTurns) Track(sessionID, agentID string) {
	s.known.Store(sessionID, &sessionRecord{
		id:        sessionID,
		agentID:   agentID,
		createdAt: time.Now(),
	})
}

// KnownSessions returns all session IDs that have been passed to Track().
func (s *SessionTurns) KnownSessions() []map[string]any {
	var out []map[string]any
	s.known.Range(func(_, v any) bool {
		r := v.(*sessionRecord)
		out = append(out, map[string]any{
			"session_id": r.id,
			"agent_id":   r.agentID,
			"created_at": r.createdAt.Unix(),
		})
		return true
	})
	return out
}

// IsKnown reports whether a session has been registered via Track().
func (s *SessionTurns) IsKnown(sessionID string) bool {
	_, ok := s.known.Load(sessionID)
	return ok
}

// TryAcquire attempts to take the processing slot for sessionID.
//
//   - If acquired: returns (release func, true).  The caller MUST call
//     release() when the turn is finished (typically via defer).
//   - If busy:     returns (nil, false).  The caller should not start
//     a new agent turn and may reply to the user with a "busy" notice.
func (s *SessionTurns) TryAcquire(sessionID string) (release func(), acquired bool) {
	v, _ := s.locks.LoadOrStore(sessionID, &sync.Mutex{})
	m := v.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	return func() { m.Unlock() }, true
}
