package runtime

import (
	"fmt"
	"sync"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

// An authoritative Set must not be overwritten by an older in-flight fetch that
// resolves later (peer index review P1).
func TestPeerIndex_SetNotOverwrittenByStaleFetch(t *testing.T) {
	key := hexPub()
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) {
			once.Do(func() { close(started) })
			<-release
			return &PeerProfileFacts{IsBot: false}, nil // fetch says NOT a bot
		},
	})
	defer idx.Close()

	done := make(chan struct{})
	go func() { idx.Resolve(key); close(done) }() // triggers fetch, blocks in fetch
	<-started

	idx.Set(key, PeerProfileFacts{IsBot: true}) // authoritative: IS a bot
	close(release)
	<-done

	if v := idx.IsPeerAgent(key); !v.IsKnownBot() {
		t.Fatalf("stale fetch must not overwrite authoritative Set, got %v", v)
	}
}

// An OnError callback that calls Close() must not deadlock (worker marks itself
// done before invoking the callback) (peer index review P1).
func TestPeerIndex_OnErrorCanCloseWithoutDeadlock(t *testing.T) {
	var idx *PeerAgentIndex
	closed := make(chan struct{})
	var once sync.Once
	idx, _ = NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) { return nil, fmt.Errorf("relay down") },
		OnError: func(error, string) {
			once.Do(func() {
				idx.Close()
				close(closed)
			})
		},
	})

	idx.Resolve(hexPub())

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("OnError -> Close deadlocked")
	}
}

// Equal-timestamp replaceable profiles must resolve deterministically to the
// lexicographically lower event id, independent of relay arrival order (P1).
func TestSelectIdentityBoundProfile_EqualTimestampTieBreak(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()
	a := signKind0(t, sk, `{"bot":true}`, 3000)
	b := signKind0(t, sk, `{"bot":false}`, 3000)
	if a.ID.Hex() == b.ID.Hex() {
		t.Skip("degenerate: identical ids")
	}

	best1, ok1 := selectIdentityBoundProfile([]nostr.Event{a, b}, pk)
	best2, ok2 := selectIdentityBoundProfile([]nostr.Event{b, a}, pk)
	if !ok1 || !ok2 {
		t.Fatal("expected a winner")
	}
	if best1.ID.Hex() != best2.ID.Hex() {
		t.Fatalf("tie-break non-deterministic: %s vs %s", best1.ID.Hex(), best2.ID.Hex())
	}
	lower := a
	if b.ID.Hex() < a.ID.Hex() {
		lower = b
	}
	if best1.ID.Hex() != lower.ID.Hex() {
		t.Errorf("expected lexicographically-lower id to win, got %s", best1.ID.Hex())
	}
}
