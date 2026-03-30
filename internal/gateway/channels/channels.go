// Package channels provides the multi-channel messaging framework for Metiq.
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

	nostruntime "metiq/internal/nostr/runtime"
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
	// Hub is the shared NostrHub.  If set, the channel uses the hub's pool
	// (sharing WebSocket connections with all other channels).  If nil, a
	// dedicated pool is created (legacy behaviour).
	Hub *nostruntime.NostrHub
	// Keyer is the signing interface.  Ignored when Hub is set (hub provides keyer).
	Keyer nostr.Keyer
	// OnMessage is called for every inbound group message.
	OnMessage func(InboundMessage)
	// OnError is called for subscription errors.
	OnError func(error)
}

// NIP29GroupChannel subscribes to a NIP-29 relay-based group (kind 9) and
// allows the agent to send messages back.
type NIP29GroupChannel struct {
	id       string
	gad      nip29.GroupAddress
	hub      *nostruntime.NostrHub // non-nil when using shared hub
	pool     *nostr.Pool           // non-nil only in legacy (no-hub) mode
	ownsPool bool                  // true when we created the pool ourselves
	keyer    nostr.Keyer
	ctx      context.Context
	cancel   context.CancelFunc
	onMsg    func(InboundMessage)
	onErr    func(error)
	pubkey   string
}

// NewNIP29GroupChannel creates and starts a NIP-29 group subscription.
func NewNIP29GroupChannel(parent context.Context, opts NIP29GroupChannelOptions) (*NIP29GroupChannel, error) {
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

	// Resolve keyer and pool from hub or opts.
	var keyer nostr.Keyer
	var pool *nostr.Pool
	var hub *nostruntime.NostrHub
	ownsPool := false

	if opts.Hub != nil {
		hub = opts.Hub
		keyer = hub.Keyer()
		pool = hub.Pool()
	} else {
		if opts.Keyer == nil {
			return nil, fmt.Errorf("keyer is required (or provide Hub)")
		}
		keyer = opts.Keyer
		pool = nostr.NewPool(nostruntime.PoolOptsNIP42(keyer))
		ownsPool = true
	}

	pk, err := keyer.GetPublicKey(parent)
	if err != nil {
		return nil, fmt.Errorf("nip29: get public key from keyer: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)

	ch := &NIP29GroupChannel{
		id:       opts.GroupAddress,
		gad:      gad,
		hub:      hub,
		pool:     pool,
		ownsPool: ownsPool,
		keyer:    keyer,
		ctx:      ctx,
		cancel:   cancel,
		onMsg:    opts.OnMessage,
		onErr:    opts.OnError,
		pubkey:   pk.Hex(),
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

// Close shuts down the subscription.  Only closes the pool if we own it.
func (c *NIP29GroupChannel) Close() {
	c.cancel()
	if c.ownsPool {
		c.pool.Close("nip29 channel closed")
	}
}

// subscribeLoop listens for kind-9 messages on the group relay using
// SubscribeManyNotifyClosed for proper CLOSED signal handling.
func (c *NIP29GroupChannel) subscribeLoop(ctx context.Context) {
	seen := NewSeenCache()

	since := applyJitter(nostr.Timestamp(time.Now().Unix()), DefaultSinceJitter)
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.KindSimpleGroupChatMessage},
		Tags:  nostr.TagMap{"h": []string{c.gad.ID}},
		Since: since,
	}

	events, closedCh := c.pool.SubscribeManyNotifyClosed(
		ctx, []string{c.gad.Relay}, filter, nostr.SubscriptionOptions{},
	)

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			evIDHex := ev.ID.Hex()
			if seen.Add(evIDHex) {
				continue // duplicate
			}
			if ev.PubKey.Hex() == c.pubkey {
				continue
			}
			if c.onMsg == nil {
				continue
			}
			gad := c.gad
			senderHex := ev.PubKey.Hex()
			c.onMsg(InboundMessage{
				ChannelID:  c.id,
				GroupID:    gad.ID,
				Relay:      gad.Relay,
				FromPubKey: senderHex,
				Text:       ev.Content,
				EventID:    evIDHex,
				CreatedAt:  int64(ev.CreatedAt),
				Reply: func(ctx context.Context, text string) error {
					return c.Send(ctx, text)
				},
			})

		case rc, ok := <-closedCh:
			if !ok {
				// Avoid tight-looping on a closed channel; the events channel will
				// also close, at which point we will exit.
				closedCh = nil
				continue
			}
			if !rc.HandledAuth && c.onErr != nil {
				c.onErr(fmt.Errorf("nip29 sub closed by %s: %s", rc.Relay.URL, rc.Reason))
			}

		case <-ctx.Done():
			return
		}
	}
}

// ─── NIP-28 Public Channel ────────────────────────────────────────────────────

// NIP28PublicChannelOptions configure a NIP-28 public channel subscription.
type NIP28PublicChannelOptions struct {
	// ChannelID is the event ID of the kind-40 channel-creation event.
	ChannelID string
	// Hub is the shared NostrHub.  If set, shares connections with all channels.
	Hub *nostruntime.NostrHub
	// Keyer is the signing interface.  Ignored when Hub is set.
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
	ownsPool  bool
	cancel    context.CancelFunc
	onMsg     func(InboundMessage)
	onErr     func(error)
	pubkey    string
}

// NewNIP28PublicChannel creates and starts a NIP-28 public channel subscription.
func NewNIP28PublicChannel(parent context.Context, opts NIP28PublicChannelOptions) (*NIP28PublicChannel, error) {
	if opts.ChannelID == "" {
		return nil, fmt.Errorf("channel_id is required")
	}
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("at least one relay is required for nip28 channel")
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
		pool = nostr.NewPool(nostruntime.PoolOptsNIP42(keyer))
		ownsPool = true
	}

	pk, err := keyer.GetPublicKey(parent)
	if err != nil {
		return nil, fmt.Errorf("nip28: get public key from keyer: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)

	ch := &NIP28PublicChannel{
		id:        opts.ChannelID,
		channelID: opts.ChannelID,
		keyer:     keyer,
		relays:    opts.Relays,
		pool:      pool,
		ownsPool:  ownsPool,
		cancel:    cancel,
		onMsg:     opts.OnMessage,
		onErr:     opts.OnError,
		pubkey:    pk.Hex(),
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
	published := false
	for result := range c.pool.PublishMany(ctx, c.relays, evt) {
		if result.Error == nil {
			published = true
		} else {
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted publish")
		}
		return fmt.Errorf("nip28 send: %w", lastErr)
	}
	return nil
}

// Close shuts down the subscription.
func (c *NIP28PublicChannel) Close() {
	c.cancel()
	if c.ownsPool {
		c.pool.Close("nip28 channel closed")
	}
}

// subscribeLoop listens for kind-42 messages on the configured relays.
func (c *NIP28PublicChannel) subscribeLoop(ctx context.Context) {
	seen := NewSeenCache()

	since := applyJitter(nostr.Timestamp(time.Now().Unix()), DefaultSinceJitter)
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.KindChannelMessage},
		Tags:  nostr.TagMap{"e": []string{c.channelID}},
		Since: since,
	}

	events, closedCh := c.pool.SubscribeManyNotifyClosed(
		ctx, c.relays, filter, nostr.SubscriptionOptions{},
	)

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			evIDHex := ev.ID.Hex()
			if seen.Add(evIDHex) {
				continue // duplicate
			}
			if ev.PubKey.Hex() == c.pubkey {
				continue
			}
			if c.onMsg == nil {
				continue
			}
			senderHex := ev.PubKey.Hex()
			relayURL := ""
			if ev.Relay != nil {
				relayURL = ev.Relay.URL
			}
			c.onMsg(InboundMessage{
				ChannelID:  c.ID(),
				GroupID:    c.channelID,
				Relay:      relayURL,
				FromPubKey: senderHex,
				Text:       ev.Content,
				EventID:    evIDHex,
				CreatedAt:  int64(ev.CreatedAt),
				Reply: func(replyCtx context.Context, text string) error {
					return c.Send(replyCtx, text)
				},
			})

		case rc, ok := <-closedCh:
			if !ok {
				// Avoid tight-looping on a closed channel; the events channel will
				// also close, at which point we will exit.
				closedCh = nil
				continue
			}
			if !rc.HandledAuth && c.onErr != nil {
				c.onErr(fmt.Errorf("nip28 sub closed by %s: %s", rc.Relay.URL, rc.Reason))
			}

		case <-ctx.Done():
			return
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────
