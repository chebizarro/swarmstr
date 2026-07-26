// Package identity provides atomic per-session-key identity establishment
// (record-first dispatch) for inbound channel turns.
//
// Background (swarmstr-f3g5, openclaw-nostr nostr-orphan-transcripts
// investigation): several concurrent inbound paths hit the same room session
// (kind:9 message, kind:7 reaction, 9000/9001 membership, DMs) plus reconnect
// backfill bursts. If session-identity establishment reads the store, decides
// an id, then writes non-atomically, two concurrent first-turns for the same
// key can mint divergent transcript identities (orphans). This package closes
// that window with a keyed critical section so identity is established ONCE,
// before the turn runs.
package identity

import "sync"

// KeyedMutex provides per-key exclusive locking. Distinct keys proceed
// concurrently; the same key serializes. Unused keys are reclaimed via
// reference counting so the internal map does not grow without bound on rooms
// that see many unique session keys.
type KeyedMutex struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

type refLock struct {
	mu   sync.Mutex
	refs int
}

// NewKeyedMutex returns a ready-to-use KeyedMutex.
func NewKeyedMutex() *KeyedMutex {
	return &KeyedMutex{locks: make(map[string]*refLock)}
}

// Lock acquires the exclusive lock for key and returns a function that releases
// it. Always call the returned unlock exactly once (defer it).
func (k *KeyedMutex) Lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*refLock)
	}
	rl, ok := k.locks[key]
	if !ok {
		rl = &refLock{}
		k.locks[key] = rl
	}
	rl.refs++
	k.mu.Unlock()

	rl.mu.Lock()

	var once sync.Once
	return func() {
		once.Do(func() {
			rl.mu.Unlock()
			k.mu.Lock()
			rl.refs--
			if rl.refs == 0 {
				delete(k.locks, key)
			}
			k.mu.Unlock()
		})
	}
}

// Do runs fn while holding the exclusive lock for key.
func (k *KeyedMutex) Do(key string, fn func()) {
	unlock := k.Lock(key)
	defer unlock()
	fn()
}

// tracked reports how many keys currently have live locks (diagnostics/tests).
func (k *KeyedMutex) tracked() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.locks)
}
