package identity

import (
	"fmt"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// Store is the minimal session-store surface the Establisher needs. The
// production *state.SessionStore satisfies it; tests can substitute a fake.
type Store interface {
	Get(key string) (state.SessionEntry, bool)
	Put(key string, entry state.SessionEntry) error
}

// Establisher provides atomic, record-first session-identity establishment. ALL
// inbound paths for a room (message, reaction, membership, DM) must resolve
// identity through the SAME Establisher so concurrent first-turns cannot mint
// divergent transcript identities.
type Establisher struct {
	km    *KeyedMutex
	store Store
	now   func() time.Time
}

// NewEstablisher wraps a session store with a keyed critical section.
func NewEstablisher(store Store) *Establisher {
	return &Establisher{
		km:    NewKeyedMutex(),
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

// withClock is a test hook for a deterministic clock.
func (e *Establisher) withClock(now func() time.Time) *Establisher {
	e.now = now
	return e
}

// Establish atomically ensures a session entry (transcript identity) exists for
// key BEFORE the turn runs, and returns it along with whether it was newly
// created. The read-decide-write runs inside the per-key critical section, so
// two concurrent first-turns for the same key resolve to a SINGLE identity: the
// first creates it, the second observes and reuses it.
func (e *Establisher) Establish(key string) (state.SessionEntry, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return state.SessionEntry{}, false, fmt.Errorf("establish session identity: key is required")
	}
	if e.store == nil {
		return state.SessionEntry{}, false, fmt.Errorf("establish session identity: store is required")
	}

	unlock := e.km.Lock(key)
	defer unlock()
	return e.establishLocked(key)
}

// establishLocked performs the read-decide-write. The caller MUST hold the
// per-key critical section for key.
func (e *Establisher) establishLocked(key string) (state.SessionEntry, bool, error) {
	if entry, ok := e.store.Get(key); ok {
		return entry, false, nil
	}
	now := e.now()
	entry := state.SessionEntry{SessionID: key, CreatedAt: now, UpdatedAt: now}
	if err := e.store.Put(key, entry); err != nil {
		return state.SessionEntry{}, false, fmt.Errorf("establish session identity: persist %q: %w", key, err)
	}
	// Re-read so the caller sees any store-side normalization applied by Put.
	if persisted, ok := e.store.Get(key); ok {
		return persisted, true, nil
	}
	return entry, true, nil
}

// EstablishAndDo establishes identity and runs fn under the SAME per-key
// critical section (a single lock acquisition), so first-turn setup that must
// not race with a concurrent first-turn (transcript graph init, memory scope)
// is covered atomically together with identity establishment. fn receives the
// established entry and whether it was newly created.
func (e *Establisher) EstablishAndDo(key string, fn func(entry state.SessionEntry, created bool) error) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("establish session identity: key is required")
	}
	if e.store == nil {
		return fmt.Errorf("establish session identity: store is required")
	}

	unlock := e.km.Lock(key)
	defer unlock()

	entry, created, err := e.establishLocked(key)
	if err != nil {
		return err
	}
	if fn == nil {
		return nil
	}
	return fn(entry, created)
}
