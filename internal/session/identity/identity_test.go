package identity

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// countingStore is a fake Store that widens the read-decide-write window (via
// getDelay) so a missing keyed lock would let concurrent first-turns both
// create an identity. It also counts Put calls.
type countingStore struct {
	mu       sync.Mutex
	entries  map[string]state.SessionEntry
	getDelay time.Duration
	puts     int32
}

func newCountingStore(getDelay time.Duration) *countingStore {
	return &countingStore{entries: map[string]state.SessionEntry{}, getDelay: getDelay}
}

func (s *countingStore) Get(key string) (state.SessionEntry, bool) {
	s.mu.Lock()
	e, ok := s.entries[key]
	s.mu.Unlock()
	if s.getDelay > 0 {
		time.Sleep(s.getDelay) // widen the race window
	}
	return e, ok
}

func (s *countingStore) Put(key string, entry state.SessionEntry) error {
	atomic.AddInt32(&s.puts, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = entry
	return nil
}

// Parity row 16: concurrent first-turns for one room key -> exactly one
// transcript identity (one creation, one Put).
func TestEstablish_ConcurrentFirstTurns_SingleIdentity(t *testing.T) {
	store := newCountingStore(time.Millisecond)
	est := NewEstablisher(store)
	const n = 32
	key := "nostr:room:relay'room1"

	var wg sync.WaitGroup
	var createdCount int32
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry, created, err := est.Establish(key)
			if err != nil {
				t.Errorf("establish: %v", err)
				return
			}
			ids[i] = entry.SessionID
			if created {
				atomic.AddInt32(&createdCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if createdCount != 1 {
		t.Fatalf("expected exactly 1 creation, got %d", createdCount)
	}
	if got := atomic.LoadInt32(&store.puts); got != 1 {
		t.Fatalf("expected exactly 1 Put, got %d", got)
	}
	for i, id := range ids {
		if id != key {
			t.Fatalf("goroutine %d resolved id %q, want %q", i, id, key)
		}
	}
}

func TestEstablish_DistinctKeysIndependent(t *testing.T) {
	store := newCountingStore(0)
	est := NewEstablisher(store)
	_, c1, _ := est.Establish("nostr:room:relay'a")
	_, c2, _ := est.Establish("nostr:room:relay'b")
	if !c1 || !c2 {
		t.Fatal("distinct keys should each be created")
	}
	if got := atomic.LoadInt32(&store.puts); got != 2 {
		t.Fatalf("expected 2 Puts for 2 distinct keys, got %d", got)
	}
}

func TestEstablish_SecondCallReuses(t *testing.T) {
	store := newCountingStore(0)
	est := NewEstablisher(store)
	e1, c1, err := est.Establish("k1")
	if err != nil || !c1 {
		t.Fatalf("first establish: created=%v err=%v", c1, err)
	}
	e2, c2, err := est.Establish("k1")
	if err != nil {
		t.Fatal(err)
	}
	if c2 {
		t.Error("second establish must reuse, not create")
	}
	if e1.SessionID != e2.SessionID {
		t.Errorf("identity diverged: %q vs %q", e1.SessionID, e2.SessionID)
	}
}

func TestEstablish_Validation(t *testing.T) {
	est := NewEstablisher(newCountingStore(0))
	if _, _, err := est.Establish("   "); err == nil {
		t.Error("empty key must error")
	}
	nilEst := NewEstablisher(nil)
	if _, _, err := nilEst.Establish("k"); err == nil {
		t.Error("nil store must error")
	}
}

// Integration against the real SessionStore: deterministic SessionID == key,
// single referenced entry after concurrent establishes.
func TestEstablish_RealSessionStore(t *testing.T) {
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	est := NewEstablisher(store)
	key := "nostr:room:relay.example'room1"

	const n = 16
	var wg sync.WaitGroup
	var created int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, c, err := est.Establish(key)
			if err != nil {
				t.Errorf("establish: %v", err)
			}
			if c {
				atomic.AddInt32(&created, 1)
			}
		}()
	}
	wg.Wait()

	if created != 1 {
		t.Fatalf("expected 1 creation against real store, got %d", created)
	}
	entries := store.List()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 session entry (no orphans), got %d", len(entries))
	}
	if e, ok := entries[key]; !ok || e.SessionID != key {
		t.Fatalf("entry missing or wrong id: %+v", entries)
	}
}

// KeyedMutex enforces mutual exclusion for the same key: an unguarded counter
// increment stays correct under concurrency (also validated under -race).
func TestKeyedMutex_MutualExclusion(t *testing.T) {
	km := NewKeyedMutex()
	const goroutines = 50
	const perG = 200
	counter := 0
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				km.Do("same-key", func() { counter++ })
			}
		}()
	}
	wg.Wait()
	if counter != goroutines*perG {
		t.Fatalf("counter = %d, want %d (lost updates => broken mutex)", counter, goroutines*perG)
	}
	if tr := km.tracked(); tr != 0 {
		t.Errorf("expected all keys reclaimed, %d still tracked", tr)
	}
}

func TestKeyedMutex_DistinctKeysConcurrent(t *testing.T) {
	km := NewKeyedMutex()
	// Two distinct keys held simultaneously must not deadlock.
	unlockA := km.Lock("A")
	unlockB := km.Lock("B") // would block forever if keys shared a lock
	unlockA()
	unlockB()
	if tr := km.tracked(); tr != 0 {
		t.Errorf("expected 0 tracked after unlock, got %d", tr)
	}
}

func TestKeyedMutex_UnlockIdempotent(t *testing.T) {
	km := NewKeyedMutex()
	unlock := km.Lock("k")
	unlock()
	unlock() // second call must be a no-op, not a double-unlock panic
	if tr := km.tracked(); tr != 0 {
		t.Errorf("expected 0 tracked, got %d", tr)
	}
}
