package runtime

import (
	"context"
	"encoding/json"
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

const (
	SoulFactoryRuntimeCapabilitySchema = "soulfactory-runtime-capability/v2"
	SoulFactoryRuntimeControlSchema    = "soulfactory-runtime-control/v1"
)

// SoulFactoryFeatureCapability advertises availability for a SoulFactory
// customization feature family. Status values are intentionally coarse
// (available, partial, stubbed) so controller UIs can feature-gate safely.
type SoulFactoryFeatureCapability struct {
	Name           string   `json:"name,omitempty"`
	Methods        []string `json:"methods,omitempty"`
	Status         string   `json:"status,omitempty"`
	OpenClawParity string   `json:"openclaw_parity,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

// SoulFactoryFeatureParity summarizes whether the runtime's advertised
// customization surface is at feature parity with another runtime.
type SoulFactoryFeatureParity struct {
	Runtime      string   `json:"runtime,omitempty"`
	Status       string   `json:"status,omitempty"`
	MethodParity bool     `json:"method_parity,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

// SoulFactoryCapability describes optional SoulFactory runtime-control support
// carried in the existing kind:30317 capability announcement content.
type SoulFactoryCapability struct {
	Schema            string                         `json:"schema,omitempty"`
	Runtime           string                         `json:"runtime,omitempty"`
	Methods           []string                       `json:"methods,omitempty"`
	ControlSchema     string                         `json:"control_schema,omitempty"`
	ControllerPubKeys []string                       `json:"controller_pubkeys,omitempty"`
	Features          []SoulFactoryFeatureCapability `json:"features,omitempty"`
	FeatureParity     SoulFactoryFeatureParity       `json:"feature_parity,omitempty"`
}

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
	SoulFactory       SoulFactoryCapability
	// FIPS mesh transport capability.
	FIPSEnabled   bool
	FIPSTransport string // e.g. "udp:2121"
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
	in.SoulFactory = normalizeSoulFactoryCapability(in.SoulFactory, in.Runtime)
	in.FIPSTransport = strings.TrimSpace(in.FIPSTransport)
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

func normalizeCapabilityPubKeys(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func normalizeSoulFactoryCapability(in SoulFactoryCapability, runtimeName string) SoulFactoryCapability {
	in.Schema = strings.TrimSpace(in.Schema)
	in.Runtime = strings.TrimSpace(in.Runtime)
	in.ControlSchema = strings.TrimSpace(in.ControlSchema)
	in.Methods = normalizeCapabilityStrings(in.Methods)
	in.ControllerPubKeys = normalizeCapabilityPubKeys(in.ControllerPubKeys)
	in.Features = normalizeSoulFactoryFeatureCapabilities(in.Features)
	in.FeatureParity = normalizeSoulFactoryFeatureParity(in.FeatureParity)
	if in.Schema == "" && in.ControlSchema == "" && len(in.Methods) == 0 && len(in.ControllerPubKeys) == 0 && len(in.Features) == 0 && soulFactoryFeatureParityEmpty(in.FeatureParity) {
		return SoulFactoryCapability{}
	}
	if in.Schema == "" {
		in.Schema = SoulFactoryRuntimeCapabilitySchema
	}
	if in.Runtime == "" {
		in.Runtime = strings.TrimSpace(runtimeName)
	}
	if in.ControlSchema == "" {
		in.ControlSchema = SoulFactoryRuntimeControlSchema
	}
	return in
}

func normalizeSoulFactoryFeatureCapabilities(values []SoulFactoryFeatureCapability) []SoulFactoryFeatureCapability {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]SoulFactoryFeatureCapability, len(values))
	for _, value := range values {
		value.Name = strings.ToLower(strings.TrimSpace(value.Name))
		value.Status = strings.ToLower(strings.TrimSpace(value.Status))
		value.OpenClawParity = strings.ToLower(strings.TrimSpace(value.OpenClawParity))
		value.Methods = normalizeCapabilityStrings(value.Methods)
		value.Notes = normalizeCapabilityStrings(value.Notes)
		if value.Name == "" && value.Status == "" && value.OpenClawParity == "" && len(value.Methods) == 0 && len(value.Notes) == 0 {
			continue
		}
		key := value.Name
		if key == "" {
			key = strings.Join(value.Methods, "\x00")
		}
		seen[key] = value
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]SoulFactoryFeatureCapability, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

func normalizeSoulFactoryFeatureParity(in SoulFactoryFeatureParity) SoulFactoryFeatureParity {
	in.Runtime = strings.TrimSpace(in.Runtime)
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	in.Notes = normalizeCapabilityStrings(in.Notes)
	if in.Runtime == "" && (in.Status != "" || in.MethodParity || len(in.Notes) > 0) {
		in.Runtime = "openclaw"
	}
	return in
}

func soulFactoryFeatureParityEmpty(in SoulFactoryFeatureParity) bool {
	return strings.TrimSpace(in.Runtime) == "" && strings.TrimSpace(in.Status) == "" && !in.MethodParity && len(in.Notes) == 0
}

type capabilityContent struct {
	Schema            string                         `json:"schema,omitempty"`
	Runtime           string                         `json:"runtime,omitempty"`
	Methods           []string                       `json:"methods,omitempty"`
	ControlSchema     string                         `json:"control_schema,omitempty"`
	ControllerPubKeys []string                       `json:"controller_pubkeys,omitempty"`
	Features          []SoulFactoryFeatureCapability `json:"features,omitempty"`
	FeatureParity     SoulFactoryFeatureParity       `json:"feature_parity,omitempty"`
	RelayHints        capabilityRelayHints           `json:"relay_hints,omitempty"`
}

type capabilityRelayHints struct {
	Read    []string `json:"read,omitempty"`
	Write   []string `json:"write,omitempty"`
	Control []string `json:"control,omitempty"`
}

// BuildCapabilityContent encodes optional JSON metadata for kind:30317.
func BuildCapabilityContent(cap CapabilityAnnouncement) string {
	cap = normalizeCapabilityAnnouncement(cap)
	if cap.SoulFactory.Schema == "" {
		return ""
	}
	raw, err := json.Marshal(capabilityContent{
		Schema:            cap.SoulFactory.Schema,
		Runtime:           cap.SoulFactory.Runtime,
		Methods:           cap.SoulFactory.Methods,
		ControlSchema:     cap.SoulFactory.ControlSchema,
		ControllerPubKeys: cap.SoulFactory.ControllerPubKeys,
		Features:          cap.SoulFactory.Features,
		FeatureParity:     cap.SoulFactory.FeatureParity,
		RelayHints: capabilityRelayHints{
			Read:    cap.Relays,
			Write:   cap.Relays,
			Control: cap.Relays,
		},
	})
	if err != nil {
		return ""
	}
	return string(raw)
}

func parseCapabilityContent(content string) SoulFactoryCapability {
	content = strings.TrimSpace(content)
	if content == "" {
		return SoulFactoryCapability{}
	}
	var decoded capabilityContent
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		return SoulFactoryCapability{}
	}
	return normalizeSoulFactoryCapability(SoulFactoryCapability{
		Schema:            decoded.Schema,
		Runtime:           decoded.Runtime,
		Methods:           decoded.Methods,
		ControlSchema:     decoded.ControlSchema,
		ControllerPubKeys: decoded.ControllerPubKeys,
		Features:          decoded.Features,
		FeatureParity:     decoded.FeatureParity,
	}, decoded.Runtime)
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
	if cap.FIPSEnabled {
		tags = append(tags, []string{"fips", "true"})
	}
	if cap.FIPSTransport != "" {
		tags = append(tags, []string{"fips_transport", cap.FIPSTransport})
	}
	return tags
}

// ParseCapabilityEvent decodes a kind:30317 capability event.
func capabilityValidationFailure(ev nostr.Event, allowedAuthors map[string]struct{}) string {
	if ev.Kind != nostr.Kind(events.KindCapability) {
		return fmt.Sprintf("unexpected_kind:%d", ev.Kind)
	}
	if _, ok := allowedAuthors[ev.PubKey.Hex()]; !ok {
		return "unexpected_author"
	}
	if !ev.CheckID() {
		return "invalid_id"
	}
	if !ev.VerifySignature() {
		return "invalid_signature"
	}
	if timestampTooFarFuture(int64(ev.CreatedAt), time.Now(), inboundEventMaxFutureSkew) {
		return "created_at_future"
	}
	return ""
}

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
	out.SoulFactory = parseCapabilityContent(ev.Content)
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
		case "fips":
			if strings.EqualFold(strings.TrimSpace(tag[1]), "true") {
				out.FIPSEnabled = true
			}
		case "fips_transport":
			out.FIPSTransport = strings.TrimSpace(tag[1])
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
		Content:   BuildCapabilityContent(cap),
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("publish capability: sign event: %w", err)
	}

	// Use explicit timeout to properly wait for OK responses.
	// The nostr library defaults to 7s if no deadline is set.
	pubCtx, pubCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pubCancel()

	published := false
	var lastErr error
	for result := range pool.PublishMany(pubCtx, relays, evt) {
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
		a.FIPSEnabled == b.FIPSEnabled &&
		a.FIPSTransport == b.FIPSTransport &&
		relaySliceEqual(a.DMSchemes, b.DMSchemes) &&
		relaySliceEqual(a.Tools, b.Tools) &&
		relaySliceEqual(a.ContextVMFeatures, b.ContextVMFeatures) &&
		relaySliceEqual(a.Relays, b.Relays) &&
		a.SoulFactory.Schema == b.SoulFactory.Schema &&
		a.SoulFactory.Runtime == b.SoulFactory.Runtime &&
		a.SoulFactory.ControlSchema == b.SoulFactory.ControlSchema &&
		relaySliceEqual(a.SoulFactory.Methods, b.SoulFactory.Methods) &&
		relaySliceEqual(a.SoulFactory.ControllerPubKeys, b.SoulFactory.ControllerPubKeys) &&
		soulFactoryFeatureCapabilitiesEqual(a.SoulFactory.Features, b.SoulFactory.Features) &&
		soulFactoryFeatureParityEqual(a.SoulFactory.FeatureParity, b.SoulFactory.FeatureParity)
}

func soulFactoryFeatureCapabilitiesEqual(a, b []SoulFactoryFeatureCapability) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Status != b[i].Status || a[i].OpenClawParity != b[i].OpenClawParity || !relaySliceEqual(a[i].Methods, b[i].Methods) || !relaySliceEqual(a[i].Notes, b[i].Notes) {
			return false
		}
	}
	return true
}

func soulFactoryFeatureParityEqual(a, b SoulFactoryFeatureParity) bool {
	return a.Runtime == b.Runtime && a.Status == b.Status && a.MethodParity == b.MethodParity && relaySliceEqual(a.Notes, b.Notes)
}

func cloneCapabilityAnnouncement(in CapabilityAnnouncement) CapabilityAnnouncement {
	in.DMSchemes = append([]string{}, in.DMSchemes...)
	in.Tools = append([]string{}, in.Tools...)
	in.ContextVMFeatures = append([]string{}, in.ContextVMFeatures...)
	in.Relays = append([]string{}, in.Relays...)
	in.SoulFactory.Methods = append([]string{}, in.SoulFactory.Methods...)
	in.SoulFactory.ControllerPubKeys = append([]string{}, in.SoulFactory.ControllerPubKeys...)
	in.SoulFactory.Features = cloneSoulFactoryFeatureCapabilities(in.SoulFactory.Features)
	in.SoulFactory.FeatureParity.Notes = append([]string{}, in.SoulFactory.FeatureParity.Notes...)
	return in
}

func cloneSoulFactoryFeatureCapabilities(in []SoulFactoryFeatureCapability) []SoulFactoryFeatureCapability {
	if len(in) == 0 {
		return nil
	}
	out := make([]SoulFactoryFeatureCapability, len(in))
	for i, feature := range in {
		out[i] = feature
		out[i].Methods = append([]string{}, feature.Methods...)
		out[i].Notes = append([]string{}, feature.Notes...)
	}
	return out
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
	started         bool
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
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
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
		allowedAuthors := make(map[string]struct{}, len(authors))
		for _, author := range authors {
			allowedAuthors[author.Hex()] = struct{}{}
		}
		subCtx, cancel := context.WithCancel(ctx)
		eventsCh, eoseCh := m.pool.SubscribeManyNotifyEOSE(subCtx, relays, nostr.Filter{
			Kinds:   []nostr.Kind{nostr.Kind(events.KindCapability)},
			Authors: authors,
			Tags:    nostr.TagMap{"d": dTags},
		}, nostr.SubscriptionOptions{})
		// eoseCh is nil'd after EOSE to prevent busy-loop (closed channels return immediately).
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
				eoseCh = nil // prevent busy-loop: closed channel returns immediately
				log.Printf("capability-sync: EOSE — watching %d peer capability streams", len(authors))
			case re, ok := <-eventsCh:
				if !ok {
					cancel()
					break restartLoop
				}
				if reason := capabilityValidationFailure(re.Event, allowedAuthors); reason != "" {
					continue
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
