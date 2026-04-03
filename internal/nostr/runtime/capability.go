package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

const capabilityRuntimeTag = "runtime"

func canonicalCapabilityDTag(pubkey string) string {
	return strings.TrimSpace(strings.ToLower(pubkey))
}

// CapabilityAnnouncement is the normalized kind:30317 capability descriptor.
type CapabilityAnnouncement struct {
	PubKey            string
	DTag              string
	Runtime           string
	RuntimeVersion    string
	DMSchemes         []string
	ACPVersion        int
	Tools             []string
	ContextVMFeatures []string
	Relays            []string
	EventID           string
	CreatedAt         int64
}

func normalizeCapabilityAnnouncement(in CapabilityAnnouncement) CapabilityAnnouncement {
	in.PubKey = strings.TrimSpace(strings.ToLower(in.PubKey))
	in.DTag = strings.TrimSpace(strings.ToLower(in.DTag))
	if in.DTag == "" {
		in.DTag = canonicalCapabilityDTag(in.PubKey)
	}
	in.Runtime = strings.TrimSpace(in.Runtime)
	if in.Runtime == "" {
		in.Runtime = "metiq"
	}
	in.RuntimeVersion = strings.TrimSpace(in.RuntimeVersion)
	in.DMSchemes = normalizeCapabilityStrings(in.DMSchemes)
	in.Tools = normalizeCapabilityStrings(in.Tools)
	in.ContextVMFeatures = normalizeCapabilityStrings(in.ContextVMFeatures)
	in.Relays = normalizeRelayURLs(in.Relays)
	in.EventID = strings.TrimSpace(strings.ToLower(in.EventID))
	return in
}

func NormalizeCapabilityValues(values []string) []string {
	return normalizeCapabilityStrings(values)
}

func normalizeCapabilityStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]string, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = trimmed
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

// BuildCapabilityTags encodes a capability announcement into Nostr tags.
func BuildCapabilityTags(cap CapabilityAnnouncement) nostr.Tags {
	cap = normalizeCapabilityAnnouncement(cap)
	tags := nostr.Tags{{"d", cap.DTag}}
	if cap.Runtime != "" || cap.RuntimeVersion != "" {
		tag := []string{capabilityRuntimeTag}
		if cap.Runtime != "" {
			tag = append(tag, cap.Runtime)
		}
		if cap.RuntimeVersion != "" {
			tag = append(tag, cap.RuntimeVersion)
		}
		tags = append(tags, tag)
	}
	if len(cap.DMSchemes) > 0 {
		tags = append(tags, append([]string{"dm_schemes"}, cap.DMSchemes...))
	}
	if cap.ACPVersion > 0 {
		tags = append(tags, []string{"acp_version", strconv.Itoa(cap.ACPVersion)})
	}
	if len(cap.Tools) > 0 {
		tags = append(tags, append([]string{"tools"}, cap.Tools...))
	}
	if len(cap.ContextVMFeatures) > 0 {
		tags = append(tags, append([]string{"contextvm_features"}, cap.ContextVMFeatures...))
	}
	for _, relay := range cap.Relays {
		tags = append(tags, []string{"relay", relay})
	}
	return tags
}

// ParseCapabilityEvent decodes a kind:30317 capability event.
func ParseCapabilityEvent(ev *nostr.Event) (CapabilityAnnouncement, error) {
	if ev == nil {
		return CapabilityAnnouncement{}, fmt.Errorf("capability event is nil")
	}
	if ev.Kind != nostr.Kind(events.KindCapability) {
		return CapabilityAnnouncement{}, fmt.Errorf("unexpected capability kind %d", ev.Kind)
	}
	out := CapabilityAnnouncement{
		PubKey:    ev.PubKey.Hex(),
		EventID:   ev.ID.Hex(),
		CreatedAt: int64(ev.CreatedAt),
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch strings.TrimSpace(tag[0]) {
		case "d":
			out.DTag = strings.TrimSpace(tag[1])
		case capabilityRuntimeTag:
			out.Runtime = strings.TrimSpace(tag[1])
			if len(tag) >= 3 {
				out.RuntimeVersion = strings.TrimSpace(tag[2])
			}
		case "dm_schemes":
			out.DMSchemes = append(out.DMSchemes, tag[1:]...)
		case "acp_version":
			if v, err := strconv.Atoi(strings.TrimSpace(tag[1])); err == nil {
				out.ACPVersion = v
			}
		case "tools":
			out.Tools = append(out.Tools, tag[1:]...)
		case "contextvm_features":
			out.ContextVMFeatures = append(out.ContextVMFeatures, tag[1:]...)
		case "relay":
			out.Relays = append(out.Relays, strings.TrimSpace(tag[1]))
		}
	}
	out = normalizeCapabilityAnnouncement(out)
	return out, nil
}

// PublishCapability signs and publishes a replaceable kind:30317 capability event.
func PublishCapability(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, publishRelays []string, cap CapabilityAnnouncement) (string, error) {
	if pool == nil {
		return "", fmt.Errorf("publish capability: pool is required")
	}
	if keyer == nil {
		return "", fmt.Errorf("publish capability: keyer is required")
	}
	relays := normalizeRelayURLs(publishRelays)
	if len(relays) == 0 {
		return "", fmt.Errorf("publish capability: at least one relay is required")
	}
	pkCtx, pkCancel := context.WithTimeout(ctx, 10*time.Second)
	pk, err := keyer.GetPublicKey(pkCtx)
	pkCancel()
	if err != nil {
		return "", fmt.Errorf("publish capability: get public key: %w", err)
	}
	cap.PubKey = pk.Hex()
	cap = normalizeCapabilityAnnouncement(cap)
	evt := nostr.Event{
		Kind:      nostr.Kind(events.KindCapability),
		CreatedAt: nostr.Now(),
		Tags:      BuildCapabilityTags(cap),
		Content:   "",
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("publish capability: sign event: %w", err)
	}
	published := false
	var lastErr error
	for result := range pool.PublishMany(ctx, relays, evt) {
		if result.Error == nil {
			published = true
			continue
		}
		lastErr = result.Error
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relays accepted the event")
		}
		return "", fmt.Errorf("publish capability: %w", lastErr)
	}
	return evt.ID.Hex(), nil
}

// CapabilityCallback fires when a peer capability changes.
type CapabilityCallback func(pubkey string, cap CapabilityAnnouncement)

// CapabilityRegistry tracks the latest accepted capability event per pubkey.
type CapabilityRegistry struct {
	mu        sync.RWMutex
	entries   map[string]*CapabilityAnnouncement
	callbacks []CapabilityCallback
}

func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{entries: map[string]*CapabilityAnnouncement{}}
}

func (r *CapabilityRegistry) OnChange(fn CapabilityCallback) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.callbacks = append(r.callbacks, fn)
	r.mu.Unlock()
}

func (r *CapabilityRegistry) Get(pubkey string) (CapabilityAnnouncement, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[strings.TrimSpace(strings.ToLower(pubkey))]
	if !ok || entry == nil {
		return CapabilityAnnouncement{}, false
	}
	return cloneCapabilityAnnouncement(*entry), true
}

func (r *CapabilityRegistry) All() map[string]CapabilityAnnouncement {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]CapabilityAnnouncement, len(r.entries))
	for pubkey, entry := range r.entries {
		if entry == nil {
			continue
		}
		out[pubkey] = cloneCapabilityAnnouncement(*entry)
	}
	return out
}

func (r *CapabilityRegistry) Set(cap CapabilityAnnouncement) bool {
	cap = normalizeCapabilityAnnouncement(cap)
	if cap.PubKey == "" {
		return false
	}
	r.mu.Lock()
	existing := r.entries[cap.PubKey]
	if existing != nil {
		if existing.CreatedAt > cap.CreatedAt {
			r.mu.Unlock()
			return false
		}
		if existing.CreatedAt == cap.CreatedAt && strings.Compare(existing.EventID, cap.EventID) >= 0 {
			r.mu.Unlock()
			return false
		}
	}
	changed := existing == nil || !capabilitySemanticEqual(*existing, cap)
	copyCap := cloneCapabilityAnnouncement(cap)
	r.entries[cap.PubKey] = &copyCap
	callbacks := append([]CapabilityCallback{}, r.callbacks...)
	r.mu.Unlock()
	if changed {
		for _, cb := range callbacks {
			cb(cap.PubKey, cloneCapabilityAnnouncement(cap))
		}
	}
	return true
}

func capabilitySemanticEqual(a, b CapabilityAnnouncement) bool {
	a = normalizeCapabilityAnnouncement(a)
	b = normalizeCapabilityAnnouncement(b)
	return a.PubKey == b.PubKey &&
		a.DTag == b.DTag &&
		a.Runtime == b.Runtime &&
		a.RuntimeVersion == b.RuntimeVersion &&
		a.ACPVersion == b.ACPVersion &&
		relaySliceEqual(a.DMSchemes, b.DMSchemes) &&
		relaySliceEqual(a.Tools, b.Tools) &&
		relaySliceEqual(a.ContextVMFeatures, b.ContextVMFeatures) &&
		relaySliceEqual(a.Relays, b.Relays)
}

func cloneCapabilityAnnouncement(in CapabilityAnnouncement) CapabilityAnnouncement {
	in.DMSchemes = append([]string{}, in.DMSchemes...)
	in.Tools = append([]string{}, in.Tools...)
	in.ContextVMFeatures = append([]string{}, in.ContextVMFeatures...)
	in.Relays = append([]string{}, in.Relays...)
	return in
}

// CapabilityMonitor keeps the local capability event published and watches
// kind:30317 updates for a dynamic fleet peer set.
type CapabilityMonitor struct {
	mu              sync.RWMutex
	pool            *nostr.Pool
	keyer           nostr.Keyer
	registry        *CapabilityRegistry
	publishRelays   []string
	subscribeRelays []string
	peers           []string
	local           CapabilityAnnouncement
	publishTimeout  time.Duration
	onPublished     func(eventID string)
	triggerCh       chan struct{}
	rebindCh        chan struct{}
}

type CapabilityMonitorOptions struct {
	Pool            *nostr.Pool
	Keyer           nostr.Keyer
	Registry        *CapabilityRegistry
	PublishRelays   []string
	SubscribeRelays []string
	Peers           []string
	Local           CapabilityAnnouncement
	PublishTimeout  time.Duration
	OnPublished     func(eventID string)
}

func NewCapabilityMonitor(opts CapabilityMonitorOptions) *CapabilityMonitor {
	publishTimeout := opts.PublishTimeout
	if publishTimeout <= 0 {
		publishTimeout = 15 * time.Second
	}
	return &CapabilityMonitor{
		pool:            opts.Pool,
		keyer:           opts.Keyer,
		registry:        opts.Registry,
		publishRelays:   normalizeRelayURLs(opts.PublishRelays),
		subscribeRelays: normalizeRelayURLs(opts.SubscribeRelays),
		peers:           normalizeCapabilityStrings(opts.Peers),
		local:           normalizeCapabilityAnnouncement(opts.Local),
		publishTimeout:  publishTimeout,
		onPublished:     opts.OnPublished,
		triggerCh:       make(chan struct{}, 1),
		rebindCh:        make(chan struct{}, 1),
	}
}

func (m *CapabilityMonitor) Start(ctx context.Context) {
	go m.runPublisher(ctx)
	go m.runSubscriber(ctx)
}

func (m *CapabilityMonitor) UpdatePublishRelays(relays []string) {
	m.mu.Lock()
	m.publishRelays = normalizeRelayURLs(relays)
	m.mu.Unlock()
}

func (m *CapabilityMonitor) UpdateSubscribeRelays(relays []string) {
	m.mu.Lock()
	m.subscribeRelays = normalizeRelayURLs(relays)
	m.mu.Unlock()
	m.requestRebind()
}

func (m *CapabilityMonitor) UpdatePeers(pubkeys []string) {
	m.mu.Lock()
	m.peers = normalizeCapabilityStrings(pubkeys)
	m.mu.Unlock()
	m.requestRebind()
}

func (m *CapabilityMonitor) UpdateLocal(cap CapabilityAnnouncement) {
	m.mu.Lock()
	m.local = normalizeCapabilityAnnouncement(cap)
	m.mu.Unlock()
}

func (m *CapabilityMonitor) TriggerPublish() {
	select {
	case m.triggerCh <- struct{}{}:
	default:
	}
}

func (m *CapabilityMonitor) requestRebind() {
	select {
	case m.rebindCh <- struct{}{}:
	default:
	}
}

func (m *CapabilityMonitor) runPublisher(ctx context.Context) {
	m.publishLocal(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.triggerCh:
			m.publishLocal(ctx)
		}
	}
}

func (m *CapabilityMonitor) publishLocal(parent context.Context) {
	m.mu.RLock()
	pool := m.pool
	keyer := m.keyer
	relays := append([]string{}, m.publishRelays...)
	local := cloneCapabilityAnnouncement(m.local)
	timeout := m.publishTimeout
	onPublished := m.onPublished
	m.mu.RUnlock()
	if pool == nil || keyer == nil || len(relays) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	eventID, err := PublishCapability(ctx, pool, keyer, relays, local)
	if err != nil {
		log.Printf("capability-sync: publish failed: %v", err)
		return
	}
	if onPublished != nil {
		onPublished(eventID)
	}
}

func (m *CapabilityMonitor) runSubscriber(ctx context.Context) {
	for {
		relays, authors, dTags := m.snapshotSubscriptionConfig()
		if len(relays) == 0 || len(authors) == 0 || len(dTags) == 0 || m.pool == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.rebindCh:
				continue
			}
		}
		subCtx, cancel := context.WithCancel(ctx)
		eventsCh, eoseCh := m.pool.SubscribeManyNotifyEOSE(subCtx, relays, nostr.Filter{
			Kinds:   []nostr.Kind{nostr.Kind(events.KindCapability)},
			Authors: authors,
			Tags:    nostr.TagMap{"d": dTags},
		}, nostr.SubscriptionOptions{})
		eoseDone := false
	restartLoop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				return
			case <-m.rebindCh:
				cancel()
				break restartLoop
			case <-eoseCh:
				if !eoseDone {
					eoseDone = true
					log.Printf("capability-sync: EOSE — watching %d peer capability streams", len(authors))
				}
			case re, ok := <-eventsCh:
				if !ok {
					cancel()
					break restartLoop
				}
				cap, err := ParseCapabilityEvent(&re.Event)
				if err != nil {
					continue
				}
				if cap.DTag != canonicalCapabilityDTag(cap.PubKey) {
					continue
				}
				if m.registry != nil {
					m.registry.Set(cap)
				}
			}
		}
	}
}

func (m *CapabilityMonitor) snapshotSubscriptionConfig() ([]string, []nostr.PubKey, []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	relays := append([]string{}, m.subscribeRelays...)
	authors := make([]nostr.PubKey, 0, len(m.peers))
	dTags := make([]string, 0, len(m.peers))
	for _, raw := range m.peers {
		pk, err := ParsePubKey(raw)
		if err != nil {
			continue
		}
		authors = append(authors, pk)
		dTags = append(dTags, canonicalCapabilityDTag(pk.Hex()))
	}
	return relays, authors, dTags
}
