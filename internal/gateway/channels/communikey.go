package channels

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
)

// Communikey event kinds from NIP-CAS-0007. They are kept local because the
// generated cascadia-nips module is not a public dependency of metiq.
const (
	CommunikeyCommunityDefinitionKind nostr.Kind = 10222
	CommunikeyProfileListKind         nostr.Kind = 30000
	CommunikeyTargetedPublicationKind nostr.Kind = 30222
)

// CommunikeyAddress is a normalized NIP-CAS-0007 community identifier.
type CommunikeyAddress struct {
	PubKey string
	Relays []string
}

// ParseCommunikeyAddress accepts either a bare lowercase hex community pubkey
// or ncommunity://<pubkey>?relay=<ws-url>&relay=<wss-url>.
func ParseCommunikeyAddress(raw string) (CommunikeyAddress, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CommunikeyAddress{}, fmt.Errorf("community_address is required")
	}
	if !strings.HasPrefix(raw, "ncommunity://") {
		if err := validateCommunikeyPubkey(raw); err != nil {
			return CommunikeyAddress{}, fmt.Errorf("invalid community pubkey: %w", err)
		}
		return CommunikeyAddress{PubKey: raw}, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return CommunikeyAddress{}, fmt.Errorf("parse ncommunity address: %w", err)
	}
	if u.Scheme != "ncommunity" || u.User != nil || u.Path != "" || u.Fragment != "" || u.Host == "" {
		return CommunikeyAddress{}, fmt.Errorf("invalid ncommunity address")
	}
	if err := validateCommunikeyPubkey(u.Host); err != nil {
		return CommunikeyAddress{}, fmt.Errorf("invalid ncommunity pubkey: %w", err)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return CommunikeyAddress{}, fmt.Errorf("parse ncommunity query: %w", err)
	}
	relays, err := normalizeCommunikeyRelays(query["relay"])
	if err != nil {
		return CommunikeyAddress{}, err
	}
	return CommunikeyAddress{PubKey: u.Host, Relays: relays}, nil
}

func validateCommunikeyPubkey(raw string) error {
	if len(raw) != 64 || raw != strings.ToLower(raw) {
		return fmt.Errorf("must be 64-character lowercase hex")
	}
	if _, err := nostr.PubKeyFromHex(raw); err != nil {
		return fmt.Errorf("must be a 32-byte hex pubkey: %w", err)
	}
	return nil
}

func normalizeCommunikeyRelays(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, relay := range raw {
		relay = strings.TrimSpace(relay)
		u, err := url.Parse(relay)
		if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil || u.Fragment != "" || strings.Contains(relay, "'") {
			return nil, fmt.Errorf("invalid community relay URL %q", relay)
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		out = append(out, relay)
	}
	return out, nil
}

func mergeCommunikeyRelays(explicit, hinted []string) ([]string, error) {
	combined := append(append([]string(nil), explicit...), hinted...)
	return normalizeCommunikeyRelays(combined)
}

func formatCommunikeyAddress(pubkey string, relays []string) string {
	u := &url.URL{Scheme: "ncommunity", Host: pubkey}
	q := url.Values{}
	for _, relay := range relays {
		q.Add("relay", relay)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type communikeyAssignment struct {
	kind    nostr.Kind
	subtype string
}

type communikeySection struct {
	name             string
	profileListID    string
	profileListRelay string
	assignments      []communikeyAssignment
}

type communikeyDefinition struct {
	createdAt nostr.Timestamp
	eventID   string
	relays    []string
	sections  map[communikeyAssignment]communikeySection
}

type communikeyACL struct {
	createdAt nostr.Timestamp
	eventID   string
	members   map[string]struct{}
}

type communikeyAuthorization struct {
	mu         sync.RWMutex
	community  string
	definition *communikeyDefinition
	acls       map[string]communikeyACL
}

func newCommunikeyAuthorization(community string) *communikeyAuthorization {
	return &communikeyAuthorization{community: community, acls: map[string]communikeyACL{}}
}

// allowed returns both the permission decision and whether enough authoritative
// state has arrived to make it. Callers can retry events while ready is false.
func (a *communikeyAuthorization) allowed(pubkey string, kind nostr.Kind, subtype string) (allowed, ready bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.definition == nil {
		return false, false
	}
	section, ok := a.definition.sections[communikeyAssignment{kind: kind, subtype: subtype}]
	if !ok {
		return false, true
	}
	acl, ok := a.acls[section.profileListID]
	if !ok {
		return false, false
	}
	_, allowed = acl.members[pubkey]
	return allowed, true
}

func (a *communikeyAuthorization) acceptDefinition(ev nostr.Event) error {
	definition, err := parseCommunikeyDefinition(ev, a.community)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.definition != nil && !newerCommunikeyEvent(definition.createdAt, definition.eventID, a.definition.createdAt, a.definition.eventID) {
		return nil
	}
	a.definition = &definition
	return nil
}

func (a *communikeyAuthorization) acceptProfileList(ev nostr.Event) error {
	d, acl, err := parseCommunikeyProfileList(ev, a.community)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if current, ok := a.acls[d]; ok && !newerCommunikeyEvent(acl.createdAt, acl.eventID, current.createdAt, current.eventID) {
		return nil
	}
	a.acls[d] = acl
	return nil
}

func newerCommunikeyEvent(nextTime nostr.Timestamp, nextID string, currentTime nostr.Timestamp, currentID string) bool {
	return nextTime > currentTime || (nextTime == currentTime && (currentID == "" || nextID < currentID))
}

func parseCommunikeyDefinition(ev nostr.Event, community string) (communikeyDefinition, error) {
	if ev.Kind != CommunikeyCommunityDefinitionKind || ev.PubKey.Hex() != community {
		return communikeyDefinition{}, fmt.Errorf("definition must be kind %d authored by the community", CommunikeyCommunityDefinitionKind)
	}
	definition := communikeyDefinition{
		createdAt: ev.CreatedAt,
		eventID:   ev.ID.Hex(),
		sections:  map[communikeyAssignment]communikeySection{},
	}
	seenNames := map[string]struct{}{}
	zeroOrOne := map[string]int{}
	relayCount := 0
	var current *communikeySection
	sectionsEnded := false

	flush := func() error {
		if current == nil {
			return nil
		}
		if len(current.assignments) == 0 {
			return fmt.Errorf("community section %q has no kind assignments", current.name)
		}
		if current.profileListID == "" {
			return fmt.Errorf("community section %q has no profile-list coordinate", current.name)
		}
		for _, assignment := range current.assignments {
			if _, exists := definition.sections[assignment]; exists {
				return fmt.Errorf("duplicate community kind/subtype assignment (%d, %q)", assignment.kind, assignment.subtype)
			}
			definition.sections[assignment] = *current
		}
		current = nil
		return nil
	}

	for _, tag := range ev.Tags {
		if len(tag) == 0 {
			continue
		}
		if len(tag) < 2 {
			switch tag[0] {
			case "r", "blossom", "grasp", "mint", "tos", "location", "g", "description", "content", "k", "a", "badge":
				return communikeyDefinition{}, fmt.Errorf("malformed community %s tag", tag[0])
			}
			continue
		}
		switch tag[0] {
		case "tos", "location", "g", "description":
			zeroOrOne[tag[0]]++
			if zeroOrOne[tag[0]] > 1 {
				return communikeyDefinition{}, fmt.Errorf("community definition has multiple %s tags", tag[0])
			}
		}
		switch tag[0] {
		case "r":
			relays, err := normalizeCommunikeyRelays([]string{tag[1]})
			if err != nil {
				return communikeyDefinition{}, fmt.Errorf("community definition relay: %w", err)
			}
			definition.relays = append(definition.relays, relays...)
			relayCount++
			if current != nil {
				if err := flush(); err != nil {
					return communikeyDefinition{}, err
				}
				sectionsEnded = true
			}
		case "content":
			if sectionsEnded {
				return communikeyDefinition{}, fmt.Errorf("community content sections are not contiguous")
			}
			if err := flush(); err != nil {
				return communikeyDefinition{}, err
			}
			name := strings.TrimSpace(tag[1])
			if name == "" {
				return communikeyDefinition{}, fmt.Errorf("community section name is required")
			}
			if _, exists := seenNames[name]; exists {
				return communikeyDefinition{}, fmt.Errorf("duplicate community section %q", name)
			}
			seenNames[name] = struct{}{}
			current = &communikeySection{name: name}
		case "k":
			if current == nil || sectionsEnded {
				return communikeyDefinition{}, fmt.Errorf("community k tag must follow a content tag")
			}
			kind, err := strconv.Atoi(tag[1])
			if err != nil || kind < 0 || kind > 65535 {
				return communikeyDefinition{}, fmt.Errorf("invalid community section kind %q", tag[1])
			}
			subtype := ""
			if len(tag) >= 3 {
				subtype = tag[2]
			}
			current.assignments = append(current.assignments, communikeyAssignment{kind: nostr.Kind(kind), subtype: subtype})
		case "a":
			if current == nil || sectionsEnded {
				return communikeyDefinition{}, fmt.Errorf("community a tag must follow a content tag")
			}
			if current.profileListID != "" {
				return communikeyDefinition{}, fmt.Errorf("community section %q has multiple profile lists", current.name)
			}
			parts := strings.SplitN(tag[1], ":", 3)
			if len(parts) != 3 || parts[0] != strconv.Itoa(int(CommunikeyProfileListKind)) || parts[1] != community || strings.TrimSpace(parts[2]) == "" {
				return communikeyDefinition{}, fmt.Errorf("community section %q profile list must be authored by the community", current.name)
			}
			if len(tag) >= 3 && tag[2] != "" {
				if _, err := normalizeCommunikeyRelays([]string{tag[2]}); err != nil {
					return communikeyDefinition{}, fmt.Errorf("community section %q profile-list relay: %w", current.name, err)
				}
				current.profileListRelay = tag[2]
			}
			current.profileListID = parts[2]
		case "badge":
			if current == nil || sectionsEnded {
				return communikeyDefinition{}, fmt.Errorf("community badge tag must follow a content tag")
			}
			parts := strings.SplitN(tag[1], ":", 3)
			if len(parts) != 3 || parts[0] != "30009" || strings.TrimSpace(parts[2]) == "" || validateCommunikeyPubkey(parts[1]) != nil {
				return communikeyDefinition{}, fmt.Errorf("invalid badge coordinate in community section %q", current.name)
			}
		default:
			if current != nil {
				if err := flush(); err != nil {
					return communikeyDefinition{}, err
				}
				sectionsEnded = true
			}
		}
	}
	if err := flush(); err != nil {
		return communikeyDefinition{}, err
	}
	if relayCount == 0 {
		return communikeyDefinition{}, fmt.Errorf("community definition requires at least one relay")
	}
	if len(definition.sections) == 0 {
		return communikeyDefinition{}, fmt.Errorf("community definition requires at least one content section")
	}
	return definition, nil
}

func parseCommunikeyProfileList(ev nostr.Event, community string) (string, communikeyACL, error) {
	if ev.Kind != CommunikeyProfileListKind || ev.PubKey.Hex() != community {
		return "", communikeyACL{}, fmt.Errorf("profile list must be kind %d authored by the community", CommunikeyProfileListKind)
	}
	d := ""
	members := map[string]struct{}{}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			if d != "" || strings.TrimSpace(tag[1]) == "" {
				return "", communikeyACL{}, fmt.Errorf("profile list requires exactly one non-empty d tag")
			}
			d = tag[1]
		case "p":
			if err := validateCommunikeyPubkey(tag[1]); err != nil {
				return "", communikeyACL{}, fmt.Errorf("invalid profile-list member: %w", err)
			}
			members[tag[1]] = struct{}{}
		}
	}
	if d == "" {
		return "", communikeyACL{}, fmt.Errorf("profile list requires exactly one non-empty d tag")
	}
	return d, communikeyACL{createdAt: ev.CreatedAt, eventID: ev.ID.Hex(), members: members}, nil
}

// CommunikeyTarget identifies a validated targeted-publication record.
type CommunikeyTarget struct {
	Event        nostr.Event
	D            string
	OriginalKind nostr.Kind
	Reference    nostr.Tag
}

type communikeyEventVersion struct {
	createdAt nostr.Timestamp
	eventID   string
}

func parseCommunikeyTarget(ev nostr.Event, community string) (CommunikeyTarget, error) {
	if ev.Kind != CommunikeyTargetedPublicationKind || ev.Content != "" {
		return CommunikeyTarget{}, fmt.Errorf("targeted publication must be kind %d with empty content", CommunikeyTargetedPublicationKind)
	}
	dCount, kCount := 0, 0
	dValue := ""
	communityCount := 0
	communities := map[string]struct{}{}
	var reference nostr.Tag
	var originalKind nostr.Kind
	for i, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			if strings.TrimSpace(tag[1]) == "" {
				return CommunikeyTarget{}, fmt.Errorf("targeted publication d tag is empty")
			}
			dValue = tag[1]
			dCount++
		case "e", "a":
			if strings.TrimSpace(tag[1]) == "" || reference != nil {
				return CommunikeyTarget{}, fmt.Errorf("targeted publication requires exactly one e or a reference")
			}
			reference = append(nostr.Tag(nil), tag...)
		case "k":
			kind, err := strconv.Atoi(tag[1])
			if err != nil || kind < 0 || kind > 65535 {
				return CommunikeyTarget{}, fmt.Errorf("invalid targeted publication kind %q", tag[1])
			}
			originalKind = nostr.Kind(kind)
			kCount++
		case "p":
			if err := validateCommunikeyPubkey(tag[1]); err != nil {
				return CommunikeyTarget{}, fmt.Errorf("invalid targeted community: %w", err)
			}
			if _, duplicate := communities[tag[1]]; duplicate {
				return CommunikeyTarget{}, fmt.Errorf("duplicate targeted community")
			}
			communities[tag[1]] = struct{}{}
			if tag[1] == community {
				communityCount++
			}
			if i+1 < len(ev.Tags) && len(ev.Tags[i+1]) >= 2 && ev.Tags[i+1][0] == "r" {
				if _, err := normalizeCommunikeyRelays([]string{ev.Tags[i+1][1]}); err != nil {
					return CommunikeyTarget{}, fmt.Errorf("targeted community relay hint: %w", err)
				}
			}
		}
	}
	if dCount != 1 || kCount != 1 || reference == nil || len(communities) < 1 || len(communities) > 12 || communityCount != 1 {
		return CommunikeyTarget{}, fmt.Errorf("invalid targeted publication shape")
	}
	if originalKind == nostr.KindSimpleGroupChatMessage || originalKind == nostr.Kind(11) {
		return CommunikeyTarget{}, fmt.Errorf("kinds 9 and 11 are community-exclusive and cannot be targeted")
	}
	author := ev.PubKey.Hex()
	if reference[0] == "a" {
		parts := strings.SplitN(reference[1], ":", 3)
		if len(parts) != 3 {
			return CommunikeyTarget{}, fmt.Errorf("invalid targeted address reference")
		}
		addressKind, err := strconv.Atoi(parts[0])
		if err != nil || addressKind != int(originalKind) || parts[1] != author || strings.TrimSpace(parts[2]) == "" {
			return CommunikeyTarget{}, fmt.Errorf("targeted address reference does not match publisher and k tag")
		}
	} else {
		if len(reference[1]) != 64 || reference[1] != strings.ToLower(reference[1]) {
			return CommunikeyTarget{}, fmt.Errorf("targeted event reference must be a lowercase event ID")
		}
		if _, err := nostr.IDFromHex(reference[1]); err != nil {
			return CommunikeyTarget{}, fmt.Errorf("invalid targeted event reference: %w", err)
		}
		if len(reference) >= 4 && reference[3] != "" && reference[3] != author {
			return CommunikeyTarget{}, fmt.Errorf("targeted event author hint does not match publisher")
		}
	}
	return CommunikeyTarget{Event: ev, D: dValue, OriginalKind: originalKind, Reference: reference}, nil
}

// CommunikeyChannelOptions configure a Communikeys community subscription.
type CommunikeyChannelOptions struct {
	CommunityAddress string
	Relays           []string
	Hub              *nostruntime.NostrHub
	Keyer            nostr.Keyer
	OnMessage        func(InboundMessage)
	OnTarget         func(CommunikeyTarget)
	OnError          func(error)
	PendingStorePath string
	AckAsReaction    *bool
}

// CommunikeyChannel keeps NIP-29 kind-9 transport while adding the client-side
// definition/profile-list authorization and targeted-publication plane from
// NIP-CAS-0007.
type CommunikeyChannel struct {
	id           string
	community    string
	relays       []string
	pool         *nostr.Pool
	ownsPool     bool
	chat         *NIP29GroupChannel
	auth         *communikeyAuthorization
	onTarget     func(CommunikeyTarget)
	onErr        func(error)
	seenMeta     *SeenCache
	pendingMu    sync.Mutex
	pending      map[string]CommunikeyTarget
	targetMu     sync.Mutex
	targetLatest map[string]communikeyEventVersion
	ctx          context.Context
	cancel       context.CancelFunc
	pubkey       string
	closeOnce    sync.Once
	metadataWG   sync.WaitGroup
}

// NewCommunikeyChannel creates the authorization/distribution subscriptions and
// a NIP-29-compatible kind-9 chat transport scoped by h=<community pubkey>.
func NewCommunikeyChannel(parent context.Context, opts CommunikeyChannelOptions) (*CommunikeyChannel, error) {
	address, err := ParseCommunikeyAddress(opts.CommunityAddress)
	if err != nil {
		return nil, err
	}
	relays, err := mergeCommunikeyRelays(opts.Relays, address.Relays)
	if err != nil {
		return nil, err
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("at least one relay is required for communikey channel")
	}

	var keyer nostr.Keyer
	var pool *nostr.Pool
	ownsPool := false
	if opts.Hub != nil {
		keyer = opts.Hub.Keyer()
		pool = opts.Hub.Pool()
	} else {
		if opts.Keyer == nil {
			return nil, fmt.Errorf("keyer is required (or provide Hub)")
		}
		keyer = opts.Keyer
		pool = nostruntime.NewPoolNIP42(keyer)
		ownsPool = true
	}
	pk, err := keyer.GetPublicKey(parent)
	if err != nil {
		if ownsPool {
			pool.Close("communikey initialization failed")
		}
		return nil, fmt.Errorf("communikey: get public key from keyer: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	c := &CommunikeyChannel{
		id:           formatCommunikeyAddress(address.PubKey, relays),
		community:    address.PubKey,
		relays:       relays,
		pool:         pool,
		ownsPool:     ownsPool,
		auth:         newCommunikeyAuthorization(address.PubKey),
		onTarget:     opts.OnTarget,
		onErr:        opts.OnError,
		seenMeta:     NewSeenCache(),
		pending:      map[string]CommunikeyTarget{},
		targetLatest: map[string]communikeyEventVersion{},
		ctx:          ctx,
		cancel:       cancel,
		pubkey:       pk.Hex(),
	}

	chat, err := NewNIP29GroupChannel(ctx, NIP29GroupChannelOptions{
		GroupAddress:     relays[0] + "'" + address.PubKey,
		Hub:              opts.Hub,
		Keyer:            keyer,
		PendingStorePath: opts.PendingStorePath,
		AckAsReaction:    opts.AckAsReaction,
		OnMessage: func(msg InboundMessage) {
			c.handleChatMessage(msg, opts.OnMessage)
		},
		OnError: opts.OnError,
	})
	if err != nil {
		cancel()
		if ownsPool {
			pool.Close("communikey initialization failed")
		}
		return nil, err
	}
	c.chat = chat

	communityKey, _ := nostr.PubKeyFromHex(address.PubKey)
	c.startMetadataSubscription(ctx, "definition", nostr.Filter{
		Kinds:   []nostr.Kind{CommunikeyCommunityDefinitionKind},
		Authors: []nostr.PubKey{communityKey},
	}, c.handleDefinition)
	c.startMetadataSubscription(ctx, "profile-list", nostr.Filter{
		Kinds:   []nostr.Kind{CommunikeyProfileListKind},
		Authors: []nostr.PubKey{communityKey},
	}, c.handleProfileList)
	c.startMetadataSubscription(ctx, "targeted-publication", nostr.Filter{
		Kinds: []nostr.Kind{CommunikeyTargetedPublicationKind},
		Tags:  nostr.TagMap{"p": []string{address.PubKey}},
	}, c.handleTarget)
	return c, nil
}

func (c *CommunikeyChannel) ID() string   { return c.id }
func (c *CommunikeyChannel) Type() string { return "communikey" }

func (c *CommunikeyChannel) handleChatMessage(msg InboundMessage, onMessage func(InboundMessage)) {
	allowed, ready := c.auth.allowed(msg.FromPubKey, nostr.KindSimpleGroupChatMessage, "")
	if !allowed {
		if msg.Settle != nil {
			msg.Settle(ready)
		}
		return
	}
	msg.ChannelID = c.id
	msg.GroupID = c.community
	// Preserve the target-aware NIP-29 reply closure so ACK conversion still
	// has the inbound event ID/author, while enforcing the current Communikey
	// ACL before either a kind-9 message or kind-7 reaction is published.
	transportReply := msg.Reply
	msg.Reply = func(replyCtx context.Context, text string) error {
		if err := c.authorizeChatPublish(); err != nil {
			return err
		}
		if transportReply != nil {
			return transportReply(replyCtx, text)
		}
		return c.chat.Send(replyCtx, text)
	}
	if onMessage == nil {
		if msg.Settle != nil {
			msg.Settle(true)
		}
		return
	}
	onMessage(msg)
}

func (c *CommunikeyChannel) authorizeChatPublish() error {
	if c.ctx != nil && c.ctx.Err() != nil {
		return fmt.Errorf("communikey channel is closed")
	}
	allowed, ready := c.auth.allowed(c.pubkey, nostr.KindSimpleGroupChatMessage, "")
	if !ready {
		return fmt.Errorf("communikey authorization state is not ready")
	}
	if !allowed {
		return fmt.Errorf("communikey publisher %s is not authorized for kind 9", c.pubkey)
	}
	return nil
}

// Send publishes an authorized kind-9 message through the NIP-29 transport.
func (c *CommunikeyChannel) Send(ctx context.Context, text string) error {
	if err := c.authorizeChatPublish(); err != nil {
		return err
	}
	return c.chat.Send(ctx, text)
}

func (c *CommunikeyChannel) Close() {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.chat != nil {
			c.chat.Close()
		}
		c.metadataWG.Wait()
		if c.ownsPool && c.pool != nil {
			c.pool.Close("communikey channel closed")
		}
	})
}

func (c *CommunikeyChannel) startMetadataSubscription(ctx context.Context, label string, filter nostr.Filter, handle func(nostr.RelayEvent) bool) {
	c.metadataWG.Add(1)
	go func() {
		defer c.metadataWG.Done()
		c.subscribeMetadata(ctx, label, filter, handle)
	}()
}

func (c *CommunikeyChannel) subscribeMetadata(ctx context.Context, label string, filter nostr.Filter, handle func(nostr.RelayEvent) bool) {
	backoff := channelReconnectInitialBackoff
	for ctx.Err() == nil {
		subCtx, cancel := context.WithCancel(ctx)
		events, closed := channelSubscribeManyNotifyClosed(subCtx, c.pool, c.relays, filter, nostr.SubscriptionOptions{Label: "communikey-" + label})
		processed := false
		consume := true
		for consume {
			select {
			case ev, ok := <-events:
				if !ok {
					consume = false
					continue
				}
				if handle(ev) {
					processed = true
				}
			case rc, ok := <-closed:
				if !ok {
					closed = nil
					continue
				}
				if ctx.Err() == nil && !rc.HandledAuth && c.onErr != nil {
					c.onErr(formatChannelClosed("communikey "+label, rc))
				}
				if !rc.HandledAuth {
					consume = false
				}
			case <-subCtx.Done():
				consume = false
			}
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		if processed {
			backoff = channelReconnectInitialBackoff
		}
		if !channelReconnectDelay(ctx, backoff) {
			return
		}
		backoff = nextChannelReconnectBackoff(backoff)
	}
}

func (c *CommunikeyChannel) validMetadataEvent(ev nostr.Event, kind nostr.Kind, tagName, tagValue string) bool {
	if !validChannelEvent(ev, kind, tagName, tagValue) {
		return false
	}
	id := ev.ID.Hex()
	return !c.seenMeta.Add(id)
}

func (c *CommunikeyChannel) handleDefinition(re nostr.RelayEvent) bool {
	if c.ctx != nil && c.ctx.Err() != nil {
		return false
	}
	if !c.validMetadataEvent(re.Event, CommunikeyCommunityDefinitionKind, "", "") || re.PubKey.Hex() != c.community {
		return false
	}
	if err := c.auth.acceptDefinition(re.Event); err != nil {
		if c.onErr != nil {
			c.onErr(fmt.Errorf("communikey definition: %w", err))
		}
		return false
	}
	c.flushPendingTargets()
	return true
}

func (c *CommunikeyChannel) handleProfileList(re nostr.RelayEvent) bool {
	if c.ctx != nil && c.ctx.Err() != nil {
		return false
	}
	if !c.validMetadataEvent(re.Event, CommunikeyProfileListKind, "", "") || re.PubKey.Hex() != c.community {
		return false
	}
	if err := c.auth.acceptProfileList(re.Event); err != nil {
		if c.onErr != nil {
			c.onErr(fmt.Errorf("communikey profile list: %w", err))
		}
		return false
	}
	c.flushPendingTargets()
	return true
}

func (c *CommunikeyChannel) handleTarget(re nostr.RelayEvent) bool {
	if c.ctx != nil && c.ctx.Err() != nil {
		return false
	}
	// Do not mark a valid target seen until its definition/ACL state is ready;
	// metadata subscriptions race on startup and authorization must fail closed
	// without permanently losing an otherwise valid target.
	if !validChannelEvent(re.Event, CommunikeyTargetedPublicationKind, "p", c.community) {
		return false
	}
	target, err := parseCommunikeyTarget(re.Event, c.community)
	if err != nil {
		if c.onErr != nil {
			c.onErr(fmt.Errorf("communikey targeted publication: %w", err))
		}
		return false
	}
	return c.processTarget(target)
}

func (c *CommunikeyChannel) processTarget(target CommunikeyTarget) bool {
	if !c.acceptTargetVersion(target) {
		return false
	}
	id := target.Event.ID.Hex()
	allowed, ready := c.auth.allowed(target.Event.PubKey.Hex(), target.OriginalKind, "")
	if !ready {
		c.pendingMu.Lock()
		if len(c.pending) >= 256 {
			c.pendingMu.Unlock()
			c.seenMeta.Add(id)
			if c.onErr != nil && (c.ctx == nil || c.ctx.Err() == nil) {
				c.onErr(fmt.Errorf("communikey pending target limit reached"))
			}
			return true
		}
		c.pending[id] = target
		c.pendingMu.Unlock()
		// Close the race where definition/ACL readiness changes between the
		// first authorization check and insertion into the pending map.
		allowed, ready = c.auth.allowed(target.Event.PubKey.Hex(), target.OriginalKind, "")
		if !ready {
			return false
		}
	}
	if c.seenMeta.Add(id) {
		return false
	}
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
	if allowed && c.onTarget != nil && (c.ctx == nil || c.ctx.Err() == nil) {
		c.onTarget(target)
	}
	return true
}

func (c *CommunikeyChannel) acceptTargetVersion(target CommunikeyTarget) bool {
	coordinate := target.Event.PubKey.Hex() + "\x00" + target.D
	next := communikeyEventVersion{createdAt: target.Event.CreatedAt, eventID: target.Event.ID.Hex()}
	c.targetMu.Lock()
	if c.targetLatest == nil {
		c.targetLatest = map[string]communikeyEventVersion{}
	}
	current, exists := c.targetLatest[coordinate]
	if exists && current.eventID == next.eventID {
		c.targetMu.Unlock()
		return true
	}
	if exists && !newerCommunikeyEvent(next.createdAt, next.eventID, current.createdAt, current.eventID) {
		c.targetMu.Unlock()
		return false
	}
	if !exists && len(c.targetLatest) >= 4096 {
		c.targetMu.Unlock()
		return false
	}
	c.targetLatest[coordinate] = next
	c.targetMu.Unlock()
	if exists {
		c.pendingMu.Lock()
		delete(c.pending, current.eventID)
		c.pendingMu.Unlock()
	}
	return true
}

func (c *CommunikeyChannel) flushPendingTargets() {
	c.pendingMu.Lock()
	pending := make([]CommunikeyTarget, 0, len(c.pending))
	for _, target := range c.pending {
		pending = append(pending, target)
	}
	c.pendingMu.Unlock()
	for _, target := range pending {
		c.processTarget(target)
	}
}
