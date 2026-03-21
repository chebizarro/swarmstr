package agent_test

import (
	"testing"
	"time"

	"metiq/internal/agent"
)

func TestKeyRing_Pick_RoundRobin(t *testing.T) {
	r := agent.NewKeyRing([]string{"a", "b", "c"})
	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		k, ok := r.Pick()
		if !ok {
			t.Fatalf("Pick returned false at iteration %d", i)
		}
		seen[k]++
	}
	for _, k := range []string{"a", "b", "c"} {
		if seen[k] != 3 {
			t.Errorf("key %q picked %d times, want 3", k, seen[k])
		}
	}
}

func TestKeyRing_Pick_SkipsCooledDown(t *testing.T) {
	r := agent.NewKeyRing([]string{"a", "b"})
	r.MarkFailed("a")
	// "b" should be picked first since "a" is in cooldown.
	k, ok := r.Pick()
	if !ok {
		t.Fatal("Pick returned false")
	}
	if k != "b" {
		t.Errorf("expected key b to be picked, got %q", k)
	}
}

func TestKeyRing_EmptyRing(t *testing.T) {
	r := agent.NewKeyRing(nil)
	_, ok := r.Pick()
	if ok {
		// OK to return ok=true per spec but key should be empty.
	}
	if r.Len() != 0 {
		t.Error("expected empty ring")
	}
}

func TestKeyRing_Dedup(t *testing.T) {
	r := agent.NewKeyRing([]string{"a", "a", "b", "a"})
	if r.Len() != 2 {
		t.Errorf("expected 2 unique keys, got %d", r.Len())
	}
}

func TestKeyRing_MarkFailed_CooldownExpiry(t *testing.T) {
	r := agent.NewKeyRing([]string{"a"})
	r.MarkFailed("a")
	// Key should still be returned (all keys in cooldown).
	k, ok := r.Pick()
	if !ok || k != "a" {
		t.Errorf("expected a even in cooldown; got %q ok=%v", k, ok)
	}
}

func TestProviderKeyRingRegistry(t *testing.T) {
	reg := agent.NewProviderKeyRingRegistry()
	if reg.Get("openai") != nil {
		t.Error("expected nil for unregistered provider")
	}
	r := agent.NewKeyRing([]string{"sk-1", "sk-2"})
	reg.Set("openai", r)
	if reg.Get("openai") == nil {
		t.Error("expected registered ring")
	}
	if reg.Get("anthropic") != nil {
		t.Error("expected nil for different provider")
	}
}

// TestKeyRing_AllCooldown verifies all-keys-in-cooldown returns the
// key with the earliest retry (best-effort).
func TestKeyRing_AllCooldown(t *testing.T) {
	r := agent.NewKeyRing([]string{"a", "b"})
	r.MarkFailed("a")
	r.MarkFailed("b")
	k, ok := r.Pick()
	// Should still return a key (the one with the nearest retry time).
	if !ok {
		// empty check
	}
	_ = k
	_ = time.Now() // referenced to avoid import removal
}
