package attach

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestStore(now *time.Time) *Store {
	return NewStore(Options{Now: func() time.Time { return *now }})
}

func TestStoreMintDefaultsAndClampsTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newTestStore(&now)

	grant, err := s.Mint("sess-1", 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if grant.SessionKey != "sess-1" || grant.Token == "" {
		t.Fatalf("unexpected grant: %+v", grant)
	}
	if got := grant.ExpiresAt.Sub(grant.IssuedAt); got != DefaultTTL {
		t.Fatalf("default ttl mismatch: %v", got)
	}

	grant, err = s.Mint("sess-1", 48*time.Hour)
	if err != nil {
		t.Fatalf("mint clamped: %v", err)
	}
	if got := grant.ExpiresAt.Sub(grant.IssuedAt); got != MaxTTL {
		t.Fatalf("ttl not clamped: %v", got)
	}

	if _, err := s.Mint("   ", 0); err == nil {
		t.Fatal("mint with blank session key succeeded")
	}
}

func TestStoreResolveExpiryAndRevoke(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newTestStore(&now)
	grant, err := s.Mint("sess-1", time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	if got, ok := s.Resolve(grant.Token); !ok || got.SessionKey != "sess-1" {
		t.Fatalf("resolve failed: %+v %v", got, ok)
	}
	if _, ok := s.Resolve("unknown"); ok {
		t.Fatal("resolved unknown token")
	}

	now = now.Add(2 * time.Minute)
	if _, ok := s.Resolve(grant.Token); ok {
		t.Fatal("resolved expired token")
	}
	if s.Revoke(grant.Token) {
		t.Fatal("revoking expired token reported a live grant")
	}

	grant, err = s.Mint("sess-2", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !s.Revoke(grant.Token) {
		t.Fatal("revoke of live grant reported false")
	}
	if s.Revoke(grant.Token) {
		t.Fatal("double revoke reported true")
	}
	if _, ok := s.Resolve(grant.Token); ok {
		t.Fatal("resolved revoked token")
	}
}

func TestStoreMintSweepsStaleGrants(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newTestStore(&now)
	if _, err := s.Mint("sess-1", time.Minute); err != nil {
		t.Fatalf("mint: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.Mint("sess-2", time.Minute); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if got := s.Count(); got != 1 {
		t.Fatalf("stale grant not swept: count=%d", got)
	}
}

func TestStoreResolvePicksCorrectGrantAmongMany(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newTestStore(&now)

	grants := make([]Grant, 0, 8)
	for i := 0; i < 8; i++ {
		grant, err := s.Mint(fmt.Sprintf("sess-%d", i), time.Minute)
		if err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
		grants = append(grants, grant)
	}
	// The constant-time scan must still resolve each token to its own grant.
	for i, grant := range grants {
		resolved, ok := s.Resolve(grant.Token)
		if !ok || resolved.SessionKey != fmt.Sprintf("sess-%d", i) {
			t.Fatalf("grant %d resolved to %+v ok=%v", i, resolved, ok)
		}
	}
	if _, ok := s.Resolve(grants[0].Token + "x"); ok {
		t.Fatal("near-miss token resolved")
	}
}

func TestStoreConcurrentMintResolveRevoke(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				grant, err := s.Mint(fmt.Sprintf("sess-%d-%d", g, i), time.Minute)
				if err != nil {
					t.Errorf("mint: %v", err)
					return
				}
				if resolved, ok := s.Resolve(grant.Token); !ok || resolved.SessionKey != grant.SessionKey {
					t.Errorf("resolve after mint failed: %+v ok=%v", resolved, ok)
					return
				}
				if i%2 == 0 {
					if !s.Revoke(grant.Token) {
						t.Errorf("revoke failed for live grant")
						return
					}
					if _, ok := s.Resolve(grant.Token); ok {
						t.Errorf("revoked grant resolved")
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
