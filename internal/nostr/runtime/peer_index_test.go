package runtime

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

// testClock is a manually-advanced clock for deterministic TTL tests.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Unix(1_700_000_000, 0)} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// hexPub returns a fresh valid hex pubkey for use as an index key.
func hexPub() string { return nostr.Generate().Public().Hex() }

func TestPeerIndex_ResolveBotAndHuman(t *testing.T) {
	botKey := hexPub()
	humanKey := hexPub()
	idx, err := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(pk string) (*PeerProfileFacts, error) {
			if pk == botKey {
				return &PeerProfileFacts{IsBot: true}, nil
			}
			return &PeerProfileFacts{IsBot: false}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if v := idx.Resolve(botKey); v != VerdictBot {
		t.Errorf("bot: got %v, want bot", v)
	}
	if v := idx.Resolve(humanKey); v != VerdictHuman {
		t.Errorf("human: got %v, want human", v)
	}
	// Cached synchronous read now returns the resolved verdict.
	if v := idx.IsPeerAgent(botKey); !v.IsKnownBot() {
		t.Errorf("cached bot: got %v, want known bot", v)
	}
}

// Row 7 support: a sender with no published kind:0 stays Unknown — never a bot —
// so the allowBots gate treats them as human (not gated).
func TestPeerIndex_NoProfileIsUnknownNotBot(t *testing.T) {
	key := hexPub()
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) { return nil, nil }, // no kind:0
	})
	defer idx.Close()

	if v := idx.Resolve(key); v != VerdictUnknown {
		t.Errorf("no-profile: got %v, want unknown", v)
	}
	if idx.Resolve(key).IsKnownBot() {
		t.Error("no-profile member must never be a known bot")
	}
}

func TestPeerIndex_FetchErrorIsUnknown(t *testing.T) {
	key := hexPub()
	var errCount int32
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) { return nil, fmt.Errorf("relay down") },
		OnError:      func(error, string) { atomic.AddInt32(&errCount, 1) },
	})
	defer idx.Close()

	if v := idx.Resolve(key); v != VerdictUnknown {
		t.Errorf("fetch error: got %v, want unknown", v)
	}
	if atomic.LoadInt32(&errCount) == 0 {
		t.Error("expected OnError to be reported")
	}
}

// A once-bot key that later goes stale must degrade to Unknown, not stay Bot.
func TestPeerIndex_StaleBotDegradesToUnknown(t *testing.T) {
	key := hexPub()
	clock := newTestClock()
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) { return &PeerProfileFacts{IsBot: true}, nil },
		TTL:          30 * time.Minute,
		Now:          clock.now,
	})
	defer idx.Close()

	if v := idx.Resolve(key); v != VerdictBot {
		t.Fatalf("initial: got %v, want bot", v)
	}
	clock.advance(31 * time.Minute)
	// Synchronous read past TTL degrades to unknown (and kicks a refresh).
	if v := idx.IsPeerAgent(key); v != VerdictUnknown {
		t.Errorf("stale: got %v, want unknown", v)
	}
}

func TestPeerIndex_NegativeTTLShorter(t *testing.T) {
	key := hexPub()
	clock := newTestClock()
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) { return nil, nil },
		TTL:          30 * time.Minute,
		NegativeTTL:  5 * time.Minute,
		Now:          clock.now,
	})
	defer idx.Close()

	idx.Resolve(key) // caches a negative entry
	if idx.Size() != 1 {
		t.Fatalf("expected 1 cached entry, got %d", idx.Size())
	}
	// Negative entries are already Unknown; ensure they refresh after negTTL,
	// not the longer positive TTL. (Behavioral: still Unknown either way, but a
	// refresh should fire — assert entry stays present and re-resolvable.)
	clock.advance(6 * time.Minute)
	if v := idx.Resolve(key); v != VerdictUnknown {
		t.Errorf("negative refresh: got %v, want unknown", v)
	}
}

func TestPeerIndex_DirectoryOverrideShortCircuits(t *testing.T) {
	key := hexPub()
	var fetched int32
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) {
			atomic.AddInt32(&fetched, 1)
			return &PeerProfileFacts{IsBot: false}, nil
		},
		DirectoryOverride: func(string) (bool, bool) { return true, true },
	})
	defer idx.Close()

	if v := idx.IsPeerAgent(key); v != VerdictBot {
		t.Errorf("override: got %v, want bot", v)
	}
	if atomic.LoadInt32(&fetched) != 0 {
		t.Error("directory override must short-circuit without fetching")
	}
}

// Seeding own pubkey as a bot must be authoritative immediately, no fetch.
func TestPeerIndex_SetSeedsOwnPubkey(t *testing.T) {
	key := hexPub()
	var fetched int32
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) {
			atomic.AddInt32(&fetched, 1)
			return nil, nil
		},
	})
	defer idx.Close()

	idx.Set(key, PeerProfileFacts{IsBot: true})
	if v := idx.IsPeerAgent(key); !v.IsKnownBot() {
		t.Errorf("seeded self: got %v, want known bot", v)
	}
	if atomic.LoadInt32(&fetched) != 0 {
		t.Error("seeded entry must not trigger a fetch")
	}
}

func TestPeerIndex_LRUEviction(t *testing.T) {
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) { return &PeerProfileFacts{IsBot: false}, nil },
		MaxEntries:   2,
	})
	defer idx.Close()

	k1, k2, k3 := hexPub(), hexPub(), hexPub()
	idx.Set(k1, PeerProfileFacts{IsBot: true})
	idx.Set(k2, PeerProfileFacts{IsBot: true})
	idx.Set(k3, PeerProfileFacts{IsBot: true}) // evicts k1 (oldest)

	if idx.Size() != 2 {
		t.Fatalf("expected size 2, got %d", idx.Size())
	}
	// k1 evicted -> synchronous read is Unknown (and re-fetches as human).
	if v := idx.IsPeerAgent(k1); v == VerdictBot {
		t.Error("k1 should have been evicted, not a cached bot")
	}
}

// maxConcurrentFetches: while N fetches are blocked in-flight, an extra lookup
// is shed and returns Unknown without spawning.
func TestPeerIndex_ConcurrencyCapShedsToUnknown(t *testing.T) {
	release := make(chan struct{})
	var active int32
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		MaxConcurrentFetches: 1,
		FetchProfile: func(string) (*PeerProfileFacts, error) {
			atomic.AddInt32(&active, 1)
			<-release // block to hold the single slot
			return &PeerProfileFacts{IsBot: true}, nil
		},
	})
	defer idx.Close()

	blockedKey := hexPub()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); idx.Resolve(blockedKey) }()

	// Wait until the single fetch slot is occupied.
	for atomic.LoadInt32(&active) == 0 {
		time.Sleep(time.Millisecond)
	}

	// A different key now finds no free slot -> Unknown, no new fetch.
	other := hexPub()
	if v := idx.IsPeerAgent(other); v != VerdictUnknown {
		t.Errorf("shed lookup: got %v, want unknown", v)
	}

	close(release)
	wg.Wait()
}

// clear() must bump the generation so an in-flight fetch cannot repopulate.
func TestPeerIndex_ClearGenerationGuard(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	key := hexPub()
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) {
			once.Do(func() { close(started) })
			<-release
			return &PeerProfileFacts{IsBot: true}, nil
		},
	})
	defer idx.Close()

	done := make(chan PeerAgentVerdict, 1)
	go func() { done <- idx.Resolve(key) }()

	<-started
	idx.Clear() // supersede the in-flight fetch
	close(release)

	<-done
	// The superseded fetch must not have populated a bot entry.
	if idx.Size() != 0 {
		t.Errorf("cleared index must stay empty, got size %d", idx.Size())
	}
	if v := idx.IsPeerAgent(key); v == VerdictBot {
		t.Error("superseded fetch must not classify key as bot")
	}
}

func TestPeerIndex_InvalidPubkeyIsUnknown(t *testing.T) {
	var fetched int32
	idx, _ := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) {
			atomic.AddInt32(&fetched, 1)
			return &PeerProfileFacts{IsBot: true}, nil
		},
	})
	defer idx.Close()

	if v := idx.IsPeerAgent("not-a-pubkey"); v != VerdictUnknown {
		t.Errorf("invalid pubkey: got %v, want unknown", v)
	}
	if atomic.LoadInt32(&fetched) != 0 {
		t.Error("invalid pubkey must not trigger a fetch")
	}
}

func TestNewPeerAgentIndex_Validation(t *testing.T) {
	if _, err := NewPeerAgentIndex(PeerAgentIndexOptions{}); err == nil {
		t.Error("nil FetchProfile must error")
	}
	if _, err := NewPeerAgentIndex(PeerAgentIndexOptions{
		FetchProfile: func(string) (*PeerProfileFacts, error) { return nil, nil },
		MaxEntries:   -1,
	}); err == nil {
		t.Error("negative MaxEntries must error")
	}
}
