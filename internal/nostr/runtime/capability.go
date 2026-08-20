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
	// FIPS mesh transport capability. FIPSEnabled/FIPSTransport are retained
	// as a legacy read projection; current discovery uses the structured
	// kind-37195 advert.
	FIPSEnabled   bool
	FIPSTransport string // legacy singular transport, e.g. "udp:2121"
	FIPSProtocol  string
	FIPSAdvert    *FIPSOverlayAdvert
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
	in.FIPSProtocol = strings.TrimSpace(in.FIPSProtocol)
	if in.FIPSAdvert != nil {
		advert := cloneFIPSOverlayAdvert(*in.FIPSAdvert)
		in.FIPSAdvert = &advert
	}
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
	tags := nostr.Tags{
		{"d", cap.DTag},
		{"schema", events.SchemaCascadiaAgentCapabilityV1},
	}
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
func capabilityValidationFailure(ev nostr.Event, allowedAuthors map[string]struct{}) string {
	if ev.Kind != nostr.Kind(events.CAS_AGENT_CAPABILITY) {
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
	if ev.Kind != nostr.Kind(events.CAS_AGENT_CAPABILITY) {
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
		Kind:      nostr.Kind(events.CAS_AGENT_CAPABILITY),
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

// CapabilityRegistry tracks independently replaceable kind-30317 and
// kind-37195 streams and exposes their effective merged capability.
type CapabilityRegistry struct {
	mu        sync.RWMutex
	entries   map[string]*capabilityRegistryEntry
	callbacks []CapabilityCallback
}

type capabilityRegistryEntry struct {
	base             *CapabilityAnnouncement
	fipsCreatedAt    int64
	fipsEventID      string
	fipsAnnouncement *FIPSAdvertAnnouncement
}

func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{entries: map[string]*capabilityRegistryEntry{}}
}

func (r *CapabilityRegistry) OnChange(fn CapabilityCallback) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.callbacks = append(r.callbacks, fn)
	r.mu.Unlock()
}

func effectiveCapability(entry *capabilityRegistryEntry, now time.Time) (CapabilityAnnouncement, bool) {
	if entry == nil || entry.base == nil {
		return CapabilityAnnouncement{}, false
	}
	out := cloneCapabilityAnnouncement(*entry.base)
	fips := entry.fipsAnnouncement
	if fips == nil || fips.ExpiresAt <= now.Unix() {
		return out, true
	}
	advert := cloneFIPSOverlayAdvert(fips.Advert)
	out.FIPSEnabled = true
	out.FIPSTransport = ""
	out.FIPSProtocol = fips.Protocol
	out.FIPSAdvert = &advert
	out.DMSchemes = append(out.DMSchemes, "fips")
	return normalizeCapabilityAnnouncement(out), true
}

func (r *CapabilityRegistry) Get(pubkey string) (CapabilityAnnouncement, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return effectiveCapability(r.entries[strings.TrimSpace(strings.ToLower(pubkey))], time.Now())
}

func (r *CapabilityRegistry) All() map[string]CapabilityAnnouncement {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]CapabilityAnnouncement, len(r.entries))
	now := time.Now()
	for pubkey, entry := range r.entries {
		if cap, ok := effectiveCapability(entry, now); ok {
			out[pubkey] = cap
		}
	}
	return out
}

func (r *CapabilityRegistry) Set(cap CapabilityAnnouncement) bool {
	cap = normalizeCapabilityAnnouncement(cap)
	if cap.PubKey == "" {
		return false
	}
	// Structured discovery state may only enter through SetFIPSAdvert so its
	// independent replacement ordering cannot be bypassed by callers.
	cap.FIPSProtocol = ""
	cap.FIPSAdvert = nil
	r.mu.Lock()
	if r.entries == nil {
		r.entries = map[string]*capabilityRegistryEntry{}
	}
	entry := r.entries[cap.PubKey]
	if entry == nil {
		entry = &capabilityRegistryEntry{}
		r.entries[cap.PubKey] = entry
	}
	if entry.base != nil {
		if entry.base.CreatedAt > cap.CreatedAt {
			r.mu.Unlock()
			return false
		}
		if entry.base.CreatedAt == cap.CreatedAt && strings.Compare(entry.base.EventID, cap.EventID) >= 0 {
			r.mu.Unlock()
			return false
		}
	}
	before, hadBefore := effectiveCapability(entry, time.Now())
	copyCap := cloneCapabilityAnnouncement(cap)
	entry.base = &copyCap
	after, hasAfter := effectiveCapability(entry, time.Now())
	changed := hadBefore != hasAfter || !hadBefore || !capabilitySemanticEqual(before, after)
	callbacks := append([]CapabilityCallback{}, r.callbacks...)
	r.mu.Unlock()
	if changed && hasAfter {
		for _, cb := range callbacks {
			cb(cap.PubKey, cloneCapabilityAnnouncement(after))
		}
	}
	return true
}

// SetFIPSAdvert accepts a kind-37195 advert independently of the base
// capability stream. Expired adverts advance the high-water mark but are not
// exposed as active capability state.
func (r *CapabilityRegistry) SetFIPSAdvert(announcement FIPSAdvertAnnouncement) bool {
	announcement.PubKey = strings.ToLower(strings.TrimSpace(announcement.PubKey))
	announcement.EventID = strings.ToLower(strings.TrimSpace(announcement.EventID))
	announcement.Protocol = defaultFIPSProtocol(announcement.Protocol)
	if announcement.PubKey == "" || announcement.EventID == "" || announcement.CreatedAt <= 0 || announcement.ExpiresAt <= 0 {
		return false
	}
	advert, err := ValidateFIPSOverlayAdvert(announcement.Advert)
	if err != nil {
		return false
	}
	announcement.Advert = advert
	r.mu.Lock()
	if r.entries == nil {
		r.entries = map[string]*capabilityRegistryEntry{}
	}
	entry := r.entries[announcement.PubKey]
	if entry == nil {
		entry = &capabilityRegistryEntry{}
		r.entries[announcement.PubKey] = entry
	}
	if entry.fipsCreatedAt > announcement.CreatedAt ||
		(entry.fipsCreatedAt == announcement.CreatedAt && strings.Compare(entry.fipsEventID, announcement.EventID) >= 0) {
		r.mu.Unlock()
		return false
	}
	now := time.Now()
	before, hadBefore := effectiveCapability(entry, now)
	entry.fipsCreatedAt = announcement.CreatedAt
	entry.fipsEventID = announcement.EventID
	if announcement.ExpiresAt > now.Unix() {
		copyAnnouncement := announcement
		copyAnnouncement.Advert = cloneFIPSOverlayAdvert(announcement.Advert)
		entry.fipsAnnouncement = &copyAnnouncement
	} else {
		entry.fipsAnnouncement = nil
	}
	after, hasAfter := effectiveCapability(entry, now)
	changed := hadBefore && hasAfter && !capabilitySemanticEqual(before, after)
	callbacks := append([]CapabilityCallback{}, r.callbacks...)
	r.mu.Unlock()
	if changed {
		for _, cb := range callbacks {
			cb(announcement.PubKey, cloneCapabilityAnnouncement(after))
		}
	}
	return true
}

// PruneExpiredFIPS removes expired effective adverts while retaining their
// replacement high-water marks. It returns the number of effective changes.
func (r *CapabilityRegistry) PruneExpiredFIPS(now time.Time) int {
	type notification struct {
		pubkey string
		cap    CapabilityAnnouncement
	}
	r.mu.Lock()
	var notifications []notification
	for pubkey, entry := range r.entries {
		if entry == nil || entry.fipsAnnouncement == nil || entry.fipsAnnouncement.ExpiresAt > now.Unix() {
			continue
		}
		before, hadBefore := effectiveCapability(entry, time.Unix(entry.fipsAnnouncement.ExpiresAt-1, 0))
		entry.fipsAnnouncement = nil
		after, hasAfter := effectiveCapability(entry, now)
		if hadBefore && hasAfter && !capabilitySemanticEqual(before, after) {
			notifications = append(notifications, notification{pubkey: pubkey, cap: after})
		}
	}
	callbacks := append([]CapabilityCallback{}, r.callbacks...)
	r.mu.Unlock()
	for _, notification := range notifications {
		for _, cb := range callbacks {
			cb(notification.pubkey, cloneCapabilityAnnouncement(notification.cap))
		}
	}
	return len(notifications)
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
		a.FIPSProtocol == b.FIPSProtocol &&
		fipsOverlayAdvertEqual(a.FIPSAdvert, b.FIPSAdvert) &&
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

func fipsOverlayAdvertEqual(a, b *FIPSOverlayAdvert) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Identifier != b.Identifier || a.Version != b.Version || len(a.Endpoints) != len(b.Endpoints) {
		return false
	}
	for i := range a.Endpoints {
		if a.Endpoints[i] != b.Endpoints[i] {
			return false
		}
	}
	return relaySliceEqual(a.SignalRelays, b.SignalRelays) && relaySliceEqual(a.STUNServers, b.STUNServers)
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
	if in.FIPSAdvert != nil {
		advert := cloneFIPSOverlayAdvert(*in.FIPSAdvert)
		in.FIPSAdvert = &advert
	}
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
	mu                    sync.RWMutex
	pool                  *nostr.Pool
	keyer                 nostr.Keyer
	registry              *CapabilityRegistry
	publishRelays         []string
	subscribeRelays       []string
	peers                 []string
	local                 CapabilityAnnouncement
	publishTimeout        time.Duration
	fipsProtocol          string
	fipsAdvertTTL         time.Duration
	fipsAdvertRefresh     time.Duration
	onPublished           func(eventID string)
	onFIPSAdvertPublished func(eventID string)
	publishLocalOverride  func(context.Context)
	triggerCh             chan struct{}
	rebindCh              chan struct{}
	fipsRebindCh          chan struct{}
	started               bool
}

type CapabilityMonitorOptions struct {
	Pool                  *nostr.Pool
	Keyer                 nostr.Keyer
	Registry              *CapabilityRegistry
	PublishRelays         []string
	SubscribeRelays       []string
	Peers                 []string
	Local                 CapabilityAnnouncement
	PublishTimeout        time.Duration
	FIPSProtocol          string
	FIPSAdvertTTL         time.Duration
	FIPSAdvertRefresh     time.Duration
	OnPublished           func(eventID string)
	OnFIPSAdvertPublished func(eventID string)
}

func NewCapabilityMonitor(opts CapabilityMonitorOptions) *CapabilityMonitor {
	publishTimeout := opts.PublishTimeout
	if publishTimeout <= 0 {
		publishTimeout = 15 * time.Second
	}
	protocol := opts.FIPSProtocol
	if strings.TrimSpace(protocol) == "" {
		protocol = opts.Local.FIPSProtocol
	}
	protocol = defaultFIPSProtocol(protocol)
	advertTTL := opts.FIPSAdvertTTL
	if advertTTL <= 0 {
		advertTTL = DefaultFIPSOverlayAdvertTTL
	}
	advertRefresh := opts.FIPSAdvertRefresh
	if advertRefresh <= 0 || advertRefresh >= advertTTL {
		advertRefresh = advertTTL / 2
	}
	if advertRefresh <= 0 {
		advertRefresh = time.Second
	}
	return &CapabilityMonitor{
		pool:                  opts.Pool,
		keyer:                 opts.Keyer,
		registry:              opts.Registry,
		publishRelays:         normalizeRelayURLs(opts.PublishRelays),
		subscribeRelays:       normalizeRelayURLs(opts.SubscribeRelays),
		peers:                 normalizeCapabilityStrings(opts.Peers),
		local:                 normalizeCapabilityAnnouncement(opts.Local),
		publishTimeout:        publishTimeout,
		fipsProtocol:          protocol,
		fipsAdvertTTL:         advertTTL,
		fipsAdvertRefresh:     advertRefresh,
		onPublished:           opts.OnPublished,
		onFIPSAdvertPublished: opts.OnFIPSAdvertPublished,
		triggerCh:             make(chan struct{}, 1),
		rebindCh:              make(chan struct{}, 1),
		fipsRebindCh:          make(chan struct{}, 1),
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
	go m.runFIPSSubscriber(ctx)
	go m.runFIPSExpiry(ctx)
}

func (m *CapabilityMonitor) UpdatePublishRelays(relays []string) {
	m.mu.Lock()
	m.publishRelays = normalizeRelayURLs(relays)
	m.mu.Unlock()
	m.TriggerPublish()
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
	select {
	case m.fipsRebindCh <- struct{}{}:
	default:
	}
}

func (m *CapabilityMonitor) runPublisher(ctx context.Context) {
	publishLocal := m.publishLocal
	m.mu.RLock()
	if m.publishLocalOverride != nil {
		publishLocal = m.publishLocalOverride
	}
	m.mu.RUnlock()
	publishLocal(ctx)
	m.mu.RLock()
	refresh := m.fipsAdvertRefresh
	m.mu.RUnlock()
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.triggerCh:
			publishLocal(ctx)
		case <-ticker.C:
			m.publishLocalFIPS(ctx)
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
	if pool != nil && keyer != nil && len(relays) > 0 {
		ctx, cancel := context.WithTimeout(parent, timeout)
		eventID, err := PublishCapability(ctx, pool, keyer, relays, local)
		cancel()
		if err != nil {
			log.Printf("capability-sync: publish failed: %v", err)
		} else if onPublished != nil {
			onPublished(eventID)
		}
	}
	m.publishLocalFIPS(parent)
}

func (m *CapabilityMonitor) publishLocalFIPS(parent context.Context) {
	m.mu.RLock()
	pool := m.pool
	keyer := m.keyer
	relays := append([]string{}, m.publishRelays...)
	local := cloneCapabilityAnnouncement(m.local)
	timeout := m.publishTimeout
	protocol := m.fipsProtocol
	ttl := m.fipsAdvertTTL
	onPublished := m.onFIPSAdvertPublished
	m.mu.RUnlock()
	if pool == nil || keyer == nil || len(relays) == 0 || local.FIPSAdvert == nil {
		return
	}
	if local.FIPSProtocol != "" {
		protocol = local.FIPSProtocol
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	eventID, err := PublishFIPSAdvert(ctx, pool, keyer, relays, protocol, ttl, *local.FIPSAdvert)
	cancel()
	if err != nil {
		log.Printf("capability-sync: publish FIPS advert failed: %v", err)
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
			Kinds:   []nostr.Kind{nostr.Kind(events.CAS_AGENT_CAPABILITY)},
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

func (m *CapabilityMonitor) runFIPSSubscriber(ctx context.Context) {
	for {
		relays, authors := m.snapshotFIPSSubscriptionConfig()
		if len(relays) == 0 || len(authors) == 0 || m.pool == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.fipsRebindCh:
				continue
			}
		}
		allowedAuthors := make(map[string]struct{}, len(authors))
		for _, author := range authors {
			allowedAuthors[author.Hex()] = struct{}{}
		}
		m.mu.RLock()
		protocol := m.fipsProtocol
		registry := m.registry
		m.mu.RUnlock()
		subCtx, cancel := context.WithCancel(ctx)
		eventsCh, eoseCh := m.pool.SubscribeManyNotifyEOSE(subCtx, relays, nostr.Filter{
			Kinds:   []nostr.Kind{nostr.Kind(FIPSOverlayAdvertKind)},
			Authors: authors,
			Tags:    nostr.TagMap{"d": []string{FIPSOverlayAdvertIdentifier}},
		}, nostr.SubscriptionOptions{})
	restartLoop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				return
			case <-m.fipsRebindCh:
				cancel()
				break restartLoop
			case <-eoseCh:
				eoseCh = nil
				log.Printf("capability-sync: EOSE — watching %d peer FIPS advert streams", len(authors))
			case re, ok := <-eventsCh:
				if !ok {
					cancel()
					break restartLoop
				}
				now := time.Now()
				if reason := fipsAdvertValidationFailure(re.Event, allowedAuthors, now); reason != "" {
					continue
				}
				announcement, err := ParseFIPSAdvertEvent(&re.Event, protocol, now)
				if err != nil {
					continue
				}
				if registry != nil {
					registry.SetFIPSAdvert(announcement)
				}
			}
		}
	}
}

func (m *CapabilityMonitor) runFIPSExpiry(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.mu.RLock()
			registry := m.registry
			m.mu.RUnlock()
			if registry != nil {
				registry.PruneExpiredFIPS(now)
			}
		}
	}
}

func (m *CapabilityMonitor) snapshotFIPSSubscriptionConfig() ([]string, []nostr.PubKey) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	relays := append([]string{}, m.subscribeRelays...)
	authors := make([]nostr.PubKey, 0, len(m.peers))
	for _, raw := range m.peers {
		pk, err := ParsePubKey(raw)
		if err == nil {
			authors = append(authors, pk)
		}
	}
	return relays, authors
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
