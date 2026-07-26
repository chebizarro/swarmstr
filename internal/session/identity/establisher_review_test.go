package identity

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"metiq/internal/store/state"
)

// EstablishAndDo runs fn once under the same acquisition, exposing the
// created flag, reusing on subsequent calls, and propagating fn errors
// (establisher review P1: replaces the deadlock-prone Do contract).
func TestEstablish_EstablishAndDo(t *testing.T) {
	store := newCountingStore(0)
	est := NewEstablisher(store)

	var gotCreated bool
	var gotID string
	if err := est.EstablishAndDo("k1", func(entry state.SessionEntry, created bool) error {
		gotCreated = created
		gotID = entry.SessionID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !gotCreated || gotID != "k1" {
		t.Fatalf("first EstablishAndDo: created=%v id=%q", gotCreated, gotID)
	}

	var created2 bool
	if err := est.EstablishAndDo("k1", func(_ state.SessionEntry, created bool) error {
		created2 = created
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Error("second EstablishAndDo must reuse, not create")
	}

	if err := est.EstablishAndDo("k2", func(state.SessionEntry, bool) error {
		return fmt.Errorf("boom")
	}); err == nil {
		t.Error("fn error must propagate")
	}
}

// Concurrent EstablishAndDo for one key yields a single creation (record-first
// setup is covered by the same critical section as establishment).
func TestEstablish_EstablishAndDoConcurrentSingleCreation(t *testing.T) {
	store := newCountingStore(0)
	est := NewEstablisher(store)
	const n = 24
	var wg sync.WaitGroup
	var created int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = est.EstablishAndDo("k", func(_ state.SessionEntry, wasCreated bool) error {
				if wasCreated {
					atomic.AddInt32(&created, 1)
				}
				return nil
			})
		}()
	}
	wg.Wait()
	if created != 1 {
		t.Fatalf("expected exactly 1 creation, got %d", created)
	}
}
