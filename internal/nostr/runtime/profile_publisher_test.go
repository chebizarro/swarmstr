package runtime

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func testProfileKeyer() nostr.Keyer {
	sk := nostr.Generate()
	kr := keyer.NewPlainKeySigner(sk)
	return &kr
}

func TestNewProfilePublisher_NilKeyer(t *testing.T) {
	_, err := NewProfilePublisher(ProfilePublisherOptions{
		Relays:  []string{"wss://relay.example"},
		Profile: map[string]any{"name": "test"},
	})
	if err == nil {
		t.Fatal("expected error for nil keyer")
	}
}

func TestNewProfilePublisher_NoRelays(t *testing.T) {
	_, err := NewProfilePublisher(ProfilePublisherOptions{
		Keyer:   testProfileKeyer(),
		Profile: map[string]any{"name": "test"},
	})
	if err == nil {
		t.Fatal("expected error for no relays")
	}
}

func TestNewProfilePublisher_DefaultRefreshInterval(t *testing.T) {
	pp, err := NewProfilePublisher(ProfilePublisherOptions{
		Keyer:   testProfileKeyer(),
		Relays:  []string{"wss://relay.example"},
		Profile: map[string]any{"name": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pp.interval != 6*time.Hour {
		t.Errorf("expected 6h default interval, got %v", pp.interval)
	}
	// Clean up pool
	if pp.ownsPool && pp.pool != nil {
		pp.pool.Close("test cleanup")
	}
}

func TestNewProfilePublisher_CustomRefreshInterval(t *testing.T) {
	pp, err := NewProfilePublisher(ProfilePublisherOptions{
		Keyer:           testProfileKeyer(),
		Relays:          []string{"wss://relay.example"},
		Profile:         map[string]any{"name": "test"},
		RefreshInterval: 1 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pp.interval != 1*time.Hour {
		t.Errorf("expected 1h interval, got %v", pp.interval)
	}
	if pp.ownsPool && pp.pool != nil {
		pp.pool.Close("test cleanup")
	}
}

func TestProfileMapsEqual(t *testing.T) {
	tests := []struct {
		name  string
		a, b  map[string]any
		equal bool
	}{
		{"both nil", nil, nil, true},
		{"empty vs empty", map[string]any{}, map[string]any{}, true},
		{"same fields", map[string]any{"name": "test", "about": "hi"}, map[string]any{"name": "test", "about": "hi"}, true},
		{"different values", map[string]any{"name": "a"}, map[string]any{"name": "b"}, false},
		{"extra field", map[string]any{"name": "a"}, map[string]any{"name": "a", "about": "hi"}, false},
		{"nil vs empty", nil, map[string]any{}, true}, // both are "no data"
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := profileMapsEqual(tt.a, tt.b)
			if got != tt.equal {
				t.Errorf("profileMapsEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.equal)
			}
		})
	}
}

func TestMergeProfileFields(t *testing.T) {
	existing := map[string]any{"name": "old", "about": "existing about", "lud16": "user@ln.com"}
	desired := map[string]any{"name": "new", "picture": "https://pic.com/a.jpg"}

	merged := mergeProfileFields(existing, desired)

	if merged["name"] != "new" {
		t.Errorf("name should be overridden: got %v", merged["name"])
	}
	if merged["about"] != "existing about" {
		t.Errorf("about should be preserved: got %v", merged["about"])
	}
	if merged["lud16"] != "user@ln.com" {
		t.Errorf("lud16 should be preserved: got %v", merged["lud16"])
	}
	if merged["picture"] != "https://pic.com/a.jpg" {
		t.Errorf("picture should be added: got %v", merged["picture"])
	}
}

func TestProfileHash_Deterministic(t *testing.T) {
	m1 := map[string]any{"b": "2", "a": "1", "c": "3"}
	m2 := map[string]any{"a": "1", "c": "3", "b": "2"}

	h1 := profileHash(m1)
	h2 := profileHash(m2)
	if h1 != h2 {
		t.Errorf("hash should be deterministic regardless of insertion order: %s != %s", h1, h2)
	}
}

func TestProfileHash_Empty(t *testing.T) {
	if profileHash(nil) != "" {
		t.Error("nil map should produce empty hash")
	}
	if profileHash(map[string]any{}) != "" {
		t.Error("empty map should produce empty hash")
	}
}

func TestCloneProfileMap(t *testing.T) {
	original := map[string]any{"name": "test", "about": "hello"}
	clone := cloneProfileMap(original)

	if clone["name"] != "test" || clone["about"] != "hello" {
		t.Error("clone should have same values")
	}

	// Mutation of clone should not affect original.
	clone["name"] = "modified"
	if original["name"] != "test" {
		t.Error("modifying clone should not affect original")
	}
}

func TestCloneProfileMap_Nil(t *testing.T) {
	if cloneProfileMap(nil) != nil {
		t.Error("nil input should return nil")
	}
}

func TestDedupRelays_Profile(t *testing.T) {
	relays := []string{
		"wss://relay1.example",
		"wss://relay2.example",
		"wss://relay2.example",
		"  wss://relay3.example  ",
		"",
	}
	deduped := dedupeRelays(relays)
	if len(deduped) != 3 {
		t.Errorf("expected 3 unique relays, got %d: %v", len(deduped), deduped)
	}
}

func TestExtractProfileFromExtra(t *testing.T) {
	tests := []struct {
		name     string
		extra    map[string]any
		expected map[string]any
	}{
		{"nil extra", nil, nil},
		{"no profile key", map[string]any{"other": "data"}, nil},
		{"profile not a map", map[string]any{"profile": "string"}, nil},
		{"empty profile", map[string]any{"profile": map[string]any{}}, nil},
		{
			"valid profile",
			map[string]any{"profile": map[string]any{"name": "Agent", "about": "I'm an agent"}},
			map[string]any{"name": "Agent", "about": "I'm an agent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractProfileFromExtra(tt.extra)
			if tt.expected == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if !profileMapsEqual(got, tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestExtractProfileFromExtra_ReturnsClone(t *testing.T) {
	extra := map[string]any{"profile": map[string]any{"name": "original"}}
	extracted := ExtractProfileFromExtra(extra)
	extracted["name"] = "modified"

	// Original should not be affected.
	profile := extra["profile"].(map[string]any)
	if profile["name"] != "original" {
		t.Error("ExtractProfileFromExtra should return a clone")
	}
}

func TestUpdateProfile_TriggersOnChange(t *testing.T) {
	pp, err := NewProfilePublisher(ProfilePublisherOptions{
		Keyer:   testProfileKeyer(),
		Relays:  []string{"wss://relay.example"},
		Profile: map[string]any{"name": "original"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if pp.ownsPool && pp.pool != nil {
			pp.pool.Close("test cleanup")
		}
	}()

	// Update with different profile should trigger.
	pp.UpdateProfile(map[string]any{"name": "updated"})
	select {
	case <-pp.triggerCh:
		// good — trigger fired
	default:
		t.Error("UpdateProfile with changed data should trigger publish")
	}
}

func TestUpdateProfile_NoTriggerOnSame(t *testing.T) {
	pp, err := NewProfilePublisher(ProfilePublisherOptions{
		Keyer:   testProfileKeyer(),
		Relays:  []string{"wss://relay.example"},
		Profile: map[string]any{"name": "original"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if pp.ownsPool && pp.pool != nil {
			pp.pool.Close("test cleanup")
		}
	}()

	// Drain any pending triggers.
	select {
	case <-pp.triggerCh:
	default:
	}

	// Update with same profile should not trigger.
	pp.UpdateProfile(map[string]any{"name": "original"})
	select {
	case <-pp.triggerCh:
		t.Error("UpdateProfile with same data should not trigger publish")
	default:
		// good — no trigger
	}
}

func TestStop_NilReceiver(t *testing.T) {
	var pp *ProfilePublisher
	pp.Stop() // should not panic
}

func TestStop_CleanShutdown(t *testing.T) {
	pp, err := NewProfilePublisher(ProfilePublisherOptions{
		Keyer:   testProfileKeyer(),
		Relays:  []string{"wss://relay.example"},
		Profile: map[string]any{"name": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pp.Start(ctx)
	cancel()

	// Stop should return without hanging.
	done := make(chan struct{})
	go func() {
		pp.Stop()
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5s")
	}
}

func TestUpdateRelays(t *testing.T) {
	pp, err := NewProfilePublisher(ProfilePublisherOptions{
		Keyer:   testProfileKeyer(),
		Relays:  []string{"wss://relay1.example"},
		Profile: map[string]any{"name": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if pp.ownsPool && pp.pool != nil {
			pp.pool.Close("test cleanup")
		}
	}()

	pp.UpdateRelays([]string{"wss://relay2.example", "wss://relay3.example"})

	pp.mu.Lock()
	relays := pp.relays
	pp.mu.Unlock()

	if len(relays) != 2 {
		t.Errorf("expected 2 relays after update, got %d", len(relays))
	}
}
