// Package runtime — profile_publisher.go implements routine kind:0 profile
// metadata publishing. It reads profile fields from the config "extra.profile"
// section, compares them against the current on-relay kind:0, and publishes
// updates when they diverge.
//
// The publisher also runs a periodic refresh (default 6h) to ensure relays
// that were unreachable on earlier publishes eventually receive the profile.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"sort"

	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// ProfilePublisherOptions configures the profile publisher.
type ProfilePublisherOptions struct {
	// Keyer is used to sign kind:0 events. Required.
	Keyer nostr.Keyer
	// Pool is the shared relay pool. If nil, a dedicated pool is created.
	Pool *nostr.Pool
	// Relays is the initial set of relay URLs to publish to.
	Relays []string
	// Profile is the initial desired profile fields (from config extra.profile).
	Profile map[string]any
	// RefreshInterval is how often to re-publish the profile even when unchanged.
	// Default: 6 hours.
	RefreshInterval time.Duration
	// OnPublished is an optional callback invoked after a successful publish.
	OnPublished func(eventID string, relayCount int)
}

// ProfilePublisher manages routine kind:0 profile metadata publishing.
type ProfilePublisher struct {
	mu        sync.Mutex
	pool      *nostr.Pool
	ownsPool  bool
	keyer     nostr.Keyer
	pubkey    nostr.PubKey
	relays    []string
	profile   map[string]any
	lastHash  string // JSON hash of last-published profile content
	interval  time.Duration
	onPublish func(string, int)
	triggerCh chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewProfilePublisher creates a profile publisher. Call Start() to begin.
func NewProfilePublisher(opts ProfilePublisherOptions) (*ProfilePublisher, error) {
	if opts.Keyer == nil {
		return nil, fmt.Errorf("profile publisher: keyer is required")
	}
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("profile publisher: at least one relay is required")
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 6 * time.Hour
	}

	pk, err := opts.Keyer.GetPublicKey(context.Background())
	if err != nil {
		return nil, fmt.Errorf("profile publisher: get public key: %w", err)
	}

	pool := opts.Pool
	ownsPool := false
	if pool == nil {
		pool = nostr.NewPool(PoolOptsNIP42(opts.Keyer))
		ownsPool = true
	}

	pp := &ProfilePublisher{
		pool:      pool,
		ownsPool:  ownsPool,
		keyer:     opts.Keyer,
		pubkey:    pk,
		relays:    dedupeRelays(opts.Relays),
		profile:   cloneProfileMap(opts.Profile),
		interval:  opts.RefreshInterval,
		onPublish: opts.OnPublished,
		triggerCh: make(chan struct{}, 1),
	}
	return pp, nil
}

// Start begins the background publisher loop.
func (pp *ProfilePublisher) Start(parent context.Context) {
	pp.ctx, pp.cancel = context.WithCancel(parent)
	pp.wg.Add(1)
	go pp.loop()
}

// Stop shuts down the publisher. Safe to call on nil receiver.
func (pp *ProfilePublisher) Stop() {
	if pp == nil {
		return
	}
	pp.cancel()
	pp.wg.Wait()
	if pp.ownsPool && pp.pool != nil {
		pp.pool.Close("profile publisher stopped")
	}
}

// UpdateProfile sets a new desired profile and triggers a publish if it changed.
func (pp *ProfilePublisher) UpdateProfile(profile map[string]any) {
	pp.mu.Lock()
	old := pp.profile
	pp.profile = cloneProfileMap(profile)
	pp.mu.Unlock()

	if !profileMapsEqual(old, profile) {
		log.Printf("profile-publisher: profile config changed, triggering publish")
		pp.TriggerPublish()
	}
}

// UpdateRelays updates the relay list for publishing.
func (pp *ProfilePublisher) UpdateRelays(relays []string) {
	pp.mu.Lock()
	pp.relays = dedupeRelays(relays)
	pp.mu.Unlock()
}

// TriggerPublish requests an immediate publish cycle.
func (pp *ProfilePublisher) TriggerPublish() {
	select {
	case pp.triggerCh <- struct{}{}:
	default:
		// already queued
	}
}

func (pp *ProfilePublisher) loop() {
	defer pp.wg.Done()

	// Immediate initial publish on start.
	pp.publishIfNeeded()

	ticker := time.NewTicker(pp.interval)
	defer ticker.Stop()

	for {
		select {
		case <-pp.ctx.Done():
			return
		case <-ticker.C:
			pp.publishIfNeeded()
		case <-pp.triggerCh:
			pp.publishIfNeeded()
		}
	}
}

func (pp *ProfilePublisher) publishIfNeeded() {
	pp.mu.Lock()
	desired := cloneProfileMap(pp.profile)
	relays := append([]string(nil), pp.relays...)
	pp.mu.Unlock()

	if len(desired) == 0 {
		return
	}
	if len(relays) == 0 {
		log.Printf("profile-publisher: no relays configured, skipping")
		return
	}

	// Fetch current kind:0 from relays.
	fetchCtx, fetchCancel := context.WithTimeout(pp.ctx, 15*time.Second)
	defer fetchCancel()

	f := nostr.Filter{
		Kinds:   []nostr.Kind{0},
		Authors: []nostr.PubKey{pp.pubkey},
		Limit:   1,
	}
	var best *nostr.Event
	for re := range pp.pool.FetchMany(fetchCtx, relays, f, nostr.SubscriptionOptions{}) {
		ev := re.Event
		if best == nil || ev.CreatedAt > best.CreatedAt {
			cp := ev
			best = &cp
		}
	}

	// Parse existing metadata.
	existing := make(map[string]any)
	if best != nil {
		_ = json.Unmarshal([]byte(best.Content), &existing)
	}

	// Merge desired fields into existing.
	merged := mergeProfileFields(existing, desired)
	mergedHash := profileHash(merged)

	// Skip if nothing changed and we've published this profile before.
	if mergedHash == pp.lastHash && best != nil {
		return
	}

	// Even if the merged content matches what's on the relay, re-publish
	// periodically (the ticker fires and lastHash won't match after restart).
	content, _ := json.Marshal(merged)
	evt := nostr.Event{
		Kind:      0,
		Content:   string(content),
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
	}
	if err := pp.keyer.SignEvent(pp.ctx, &evt); err != nil {
		log.Printf("profile-publisher: sign failed: %v", err)
		return
	}

	pubCtx, pubCancel := context.WithTimeout(pp.ctx, 20*time.Second)
	defer pubCancel()

	published := 0
	for result := range pp.pool.PublishMany(pubCtx, relays, evt) {
		if result.Error == nil {
			published++
		} else {
			log.Printf("profile-publisher: publish to %s: %v", result.RelayURL, result.Error)
		}
	}

	if published > 0 {
		pp.lastHash = mergedHash
		log.Printf("profile-publisher: published kind:0 to %d/%d relays (event=%s)", published, len(relays), evt.ID.Hex())
		if pp.onPublish != nil {
			pp.onPublish(evt.ID.Hex(), published)
		}
	} else {
		log.Printf("profile-publisher: kind:0 not accepted by any relay (%d attempted)", len(relays))
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// mergeProfileFields merges desired fields into existing, returning the merged
// result. Fields present in desired override existing. Fields in existing that
// are not in desired are preserved (so the agent doesn't clobber fields set by
// external tools or UI).
func mergeProfileFields(existing, desired map[string]any) map[string]any {
	merged := make(map[string]any, len(existing)+len(desired))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range desired {
		merged[k] = v
	}
	return merged
}

// profileHash returns a stable string representation of the profile for
// comparison. Keys are sorted to ensure determinism.
func profileHash(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	// Use JSON with sorted keys for a stable hash.
	b, _ := json.Marshal(sortedMapForHash(m))
	return string(b)
}

func sortedMapForHash(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		if sub, ok := v.(map[string]any); ok {
			v = sortedMapForHash(sub)
		}
		out[k] = v
	}
	return out
}

// profileMapsEqual compares two profile maps for semantic equality.
func profileMapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	return reflect.DeepEqual(sortedMapForHash(a), sortedMapForHash(b))
}

// cloneProfileMap returns a shallow clone of a profile map.
func cloneProfileMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}



// ExtractProfileFromExtra reads profile fields from the config Extra section.
// It looks for Extra["profile"] as a map, returning its fields.
// Returns nil if no profile data is configured.
func ExtractProfileFromExtra(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	profileRaw, ok := extra["profile"].(map[string]any)
	if !ok || len(profileRaw) == 0 {
		return nil
	}
	return cloneProfileMap(profileRaw)
}
