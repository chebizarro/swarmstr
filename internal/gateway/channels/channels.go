// Package channels provides the multi-channel messaging framework for Swarmstr.
//
// It supports NIP-17 (private DMs, already handled by nostr/runtime) and
// NIP-29 (relay-based group chat).  New channel types can be added by
// implementing the Channel interface and registering them with Registry.
package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip29"
)

// ─── InboundMessage ───────────────────────────────────────────────────────────

// InboundMessage is a normalised inbound message from any channel.
type InboundMessage struct {
	ChannelID  string // registry key ("relay'groupID" for NIP-29)
	GroupID    string // NIP-29 group ID or "" for DM channels
	Relay      string // relay the message arrived on
	FromPubKey string
	Text       string
	EventID    string
	CreatedAt  int64
	// Reply sends a reply back to the channel/sender.
	Reply func(ctx context.Context, text string) error
}

// ─── Channel interface ────────────────────────────────────────────────────────

// Channel is the abstraction for a subscribable messaging channel.
type Channel interface {
	// ID returns the unique channel key, e.g. "relay.example.com'mygroup".
	ID() string
	// Type returns a short descriptor such as "nip29-group".
	Type() string
	// Send posts a text message to the channel.
	Send(ctx context.Context, text string) error
	// Close shuts down the subscription.
	Close()
}

// ─── Registry ─────────────────────────────────────────────────────────────────

// Registry maintains the set of currently joined channels.
type Registry struct {
	mu       sync.RWMutex
	channels map[string]Channel
	order    []string
}

// NewRegistry returns a ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{channels: make(map[string]Channel)}
}

// Add registers a channel.  Returns an error if the channel ID is already registered.
func (r *Registry) Add(ch Channel) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.channels[ch.ID()]; ok {
		return fmt.Errorf("channel %q already joined", ch.ID())
	}
	r.channels[ch.ID()] = ch
	r.order = append(r.order, ch.ID())
	return nil
}

// Remove closes and removes a channel by ID.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.channels[id]
	if !ok {
		return fmt.Errorf("channel %q not found", id)
	}
	ch.Close()
	delete(r.channels, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

// Get returns a channel by ID.
func (r *Registry) Get(id string) (Channel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.channels[id]
	return ch, ok
}

// List returns summary records for all registered channels.
func (r *Registry) List() []ChannelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ChannelInfo, 0, len(r.order))
	for _, id := range r.order {
		ch := r.channels[id]
		out = append(out, ChannelInfo{ID: ch.ID(), Type: ch.Type()})
	}
	return out
}

// CloseAll closes every registered channel.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.channels {
		ch.Close()
	}
	r.channels = make(map[string]Channel)
	r.order = nil
}

// ChannelInfo is a summary record returned by List.
type ChannelInfo struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// ─── NIP-29 Group Channel ─────────────────────────────────────────────────────

// NIP29GroupChannelOptions configure a NIP-29 group subscription.
type NIP29GroupChannelOptions struct {
	// GroupAddress is the NIP-29 group address: "<relayHost>'<groupID>".
	GroupAddress string
	// Keyer is the signing interface used for group sends and identity resolution.
	Keyer nostr.Keyer
	// OnMessage is called for every inbound group message.
	OnMessage func(InboundMessage)
	// OnError is called for subscription errors.
	OnError func(error)
}

// NIP29GroupChannel subscribes to a NIP-29 relay-based group (kind 9) and
// allows the agent to send messages back.
type NIP29GroupChannel struct {
	id      string
	gad     nip29.GroupAddress
	keyer   nostr.Keyer
	pool    *nostr.Pool
	ctx     context.Context // saved for keyer.SignEvent calls
	cancel  context.CancelFunc
	onMsg   func(InboundMessage)
	onErr   func(error)
	pubkey  string // agent's public key hex
}

// NewNIP29GroupChannel creates and starts a NIP-29 group subscription.
func NewNIP29GroupChannel(parent context.Context, opts NIP29GroupChannelOptions) (*NIP29GroupChannel, error) {
	if opts.Keyer == nil {
		return nil, fmt.Errorf("keyer is required")
	}
	if opts.GroupAddress == "" {
		return nil, fmt.Errorf("group_address is required (format: relay'groupID)")
	}

	gad, err := nip29.ParseGroupAddress(opts.GroupAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid group_address %q: %w", opts.GroupAddress, err)
	}
	if !gad.IsValid() {
		return nil, fmt.Errorf("invalid group_address %q: relay and group ID are required", opts.GroupAddress)
	}

	pk, err := opts.Keyer.GetPublicKey(parent)
	if err != nil {
		return nil, fmt.Errorf("nip29: get public key from keyer: %w", err)
	}
	pubkey := pk.Hex()

	pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})
	ctx, cancel := context.WithCancel(parent)

	ch := &NIP29GroupChannel{
		id:     opts.GroupAddress,
		gad:    gad,
		keyer:  opts.Keyer,
		pool:   pool,
		ctx:    ctx,
		cancel: cancel,
		onMsg:  opts.OnMessage,
		onErr:  opts.OnError,
		pubkey: pubkey,
	}

	go ch.subscribeLoop(ctx)
	return ch, nil
}

// ID implements Channel.
func (c *NIP29GroupChannel) ID() string { return c.id }

// Type implements Channel.
func (c *NIP29GroupChannel) Type() string { return "nip29-group" }

// Send posts a kind-9 message to the group relay.
func (c *NIP29GroupChannel) Send(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text must not be empty")
	}

	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupChatMessage,
		Content:   text,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"h", c.gad.ID},
		},
	}
	if err := c.keyer.SignEvent(ctx, &evt); err != nil {
		return fmt.Errorf("sign group message: %w", err)
	}

	relay, err := c.pool.EnsureRelay(c.gad.Relay)
	if err != nil {
		return fmt.Errorf("connect to relay %s: %w", c.gad.Relay, err)
	}
	if err := relay.Publish(ctx, evt); err != nil {
		return fmt.Errorf("publish to group %s: %w", c.gad, err)
	}
	return nil
}

// Close shuts down the subscription.
func (c *NIP29GroupChannel) Close() {
	c.cancel()
	c.pool.Close("nip29 channel closed")
}

// subscribeLoop listens for kind-9 messages on the group relay.
func (c *NIP29GroupChannel) subscribeLoop(ctx context.Context) {
	since := nostr.Timestamp(time.Now().Unix())
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.KindSimpleGroupChatMessage},
		Tags:  nostr.TagMap{"h": []string{c.gad.ID}},
		Since: since,
	}

	sub := c.pool.SubscribeMany(ctx, []string{c.gad.Relay}, filter, nostr.SubscriptionOptions{})

	for ev := range sub {
		// Skip our own messages.
		if ev.PubKey.Hex() == c.pubkey {
			continue
		}
		if c.onMsg == nil {
			continue
		}
		gad := c.gad
		senderHex := ev.PubKey.Hex()
		evIDHex := ev.ID.Hex()
		relay := c.gad.Relay
		c.onMsg(InboundMessage{
			ChannelID:  c.id,
			GroupID:    gad.ID,
			Relay:      relay,
			FromPubKey: senderHex,
			Text:       ev.Content,
			EventID:    evIDHex,
			CreatedAt:  int64(ev.CreatedAt),
			Reply: func(ctx context.Context, text string) error {
				return c.Send(ctx, text)
			},
		})
	}
}

// ─── NIP-28 Public Channel ────────────────────────────────────────────────────

// NIP28PublicChannelOptions configure a NIP-28 public channel subscription.
type NIP28PublicChannelOptions struct {
	// ChannelID is the event ID of the kind-40 channel-creation event.
	ChannelID string
	// Keyer is the signing interface used for channel sends and identity resolution.
	Keyer nostr.Keyer
	// Relays is the list of relay URLs to connect to.
	Relays []string
	// OnMessage is called for every inbound kind-42 message.
	OnMessage func(InboundMessage)
	// OnError is called for subscription errors (optional).
	OnError func(error)
}

// NIP28PublicChannel subscribes to a NIP-28 public channel (kind 42) and
// allows the agent to post replies.
type NIP28PublicChannel struct {
	id        string
	channelID string
	keyer     nostr.Keyer
	relays    []string
	pool      *nostr.Pool
	cancel    context.CancelFunc
	onMsg     func(InboundMessage)
	onErr     func(error)
	pubkey    string
}

// NewNIP28PublicChannel creates and starts a NIP-28 public channel subscription.
func NewNIP28PublicChannel(parent context.Context, opts NIP28PublicChannelOptions) (*NIP28PublicChannel, error) {
	if opts.Keyer == nil {
		return nil, fmt.Errorf("keyer is required")
	}
	if opts.ChannelID == "" {
		return nil, fmt.Errorf("channel_id is required")
	}
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("at least one relay is required for nip28 channel")
	}

	pk, err := opts.Keyer.GetPublicKey(parent)
	if err != nil {
		return nil, fmt.Errorf("nip28: get public key from keyer: %w", err)
	}
	pubkey := pk.Hex()

	pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})
	ctx, cancel := context.WithCancel(parent)

	ch := &NIP28PublicChannel{
		id:        opts.ChannelID,
		channelID: opts.ChannelID,
		keyer:     opts.Keyer,
		relays:    opts.Relays,
		pool:      pool,
		cancel:    cancel,
		onMsg:     opts.OnMessage,
		onErr:     opts.OnError,
		pubkey:    pubkey,
	}

	go ch.subscribeLoop(ctx)
	return ch, nil
}

// ID implements Channel.
func (c *NIP28PublicChannel) ID() string { return "nip28:" + c.channelID }

// Type implements Channel.
func (c *NIP28PublicChannel) Type() string { return "nip28-public" }

// Send posts a kind-42 message to the channel.
func (c *NIP28PublicChannel) Send(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text must not be empty")
	}

	evt := nostr.Event{
		Kind:      nostr.KindChannelMessage,
		Content:   text,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"e", c.channelID, "", "root"},
		},
	}
	if err := c.keyer.SignEvent(ctx, &evt); err != nil {
		return fmt.Errorf("sign channel message: %w", err)
	}

	var lastErr error
	for _, relay := range c.relays {
		r, connErr := c.pool.EnsureRelay(relay)
		if connErr != nil {
			lastErr = connErr
			continue
		}
		if pubErr := r.Publish(ctx, evt); pubErr != nil {
			lastErr = pubErr
			continue
		}
		return nil // published to at least one relay
	}
	if lastErr != nil {
		return fmt.Errorf("nip28 send failed on all relays: %w", lastErr)
	}
	return fmt.Errorf("no relays available")
}

// Close shuts down the subscription.
func (c *NIP28PublicChannel) Close() {
	c.cancel()
	c.pool.Close("nip28 channel closed")
}

// subscribeLoop listens for kind-42 messages on the configured relays.
func (c *NIP28PublicChannel) subscribeLoop(ctx context.Context) {
	since := nostr.Timestamp(time.Now().Unix())
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.KindChannelMessage},
		Tags:  nostr.TagMap{"e": []string{c.channelID}},
		Since: since,
	}

	sub := c.pool.SubscribeMany(ctx, c.relays, filter, nostr.SubscriptionOptions{})
	for ev := range sub {
		if ev.PubKey.Hex() == c.pubkey {
			continue // skip our own messages
		}
		if c.onMsg == nil {
			continue
		}
		channelID := c.channelID
		senderHex := ev.PubKey.Hex()
		evIDHex := ev.ID.Hex()
		c.onMsg(InboundMessage{
			ChannelID:  c.ID(),
			GroupID:    channelID,
			FromPubKey: senderHex,
			Text:       ev.Content,
			EventID:    evIDHex,
			CreatedAt:  int64(ev.CreatedAt),
			Reply: func(replyCtx context.Context, text string) error {
				return c.Send(replyCtx, text)
			},
		})
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

