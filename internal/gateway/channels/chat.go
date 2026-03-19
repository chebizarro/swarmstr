// NIP-C7 chat channel support.
//
// NIP-C7 defines kind:9 as a simple chat message. Replies quote the parent
// event using a `q` tag. A special convention uses a single "-" tag to identify
// the "root" chat of a given relay — an ambient, relay-wide conversation.
//
// Channel config example:
//
//	"nostr_channels": {
//	  "lobby": {
//	    "kind": "chat",
//	    "relays": ["wss://relay.example.com"],
//	    "config": {
//	      "root_tag": "-"       // subscribe to the relay's root chat
//	    }
//	  },
//	  "topic": {
//	    "kind": "chat",
//	    "relays": ["wss://relay.example.com"],
//	    "config": {
//	      "root_tag": "nostr"   // subscribe to a topic-scoped chat
//	    }
//	  }
//	}
package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"

	nostr "fiatjaf.com/nostr"

	nostruntime "swarmstr/internal/nostr/runtime"
)

// KindChat is the NIP-C7 chat message kind.
const KindChat nostr.Kind = 9

// ChatChannelOptions configure a NIP-C7 chat subscription.
type ChatChannelOptions struct {
	// Hub is the shared NostrHub.  If set, shares connections with all channels.
	Hub *nostruntime.NostrHub
	// Keyer is the signing interface.  Ignored when Hub is set.
	Keyer nostr.Keyer
	// Relays is the set of relay URLs to subscribe and publish to.
	Relays []string
	// RootTag is the tag value that identifies this chat room.
	// Use "-" for the relay's root/ambient chat.
	// Use any other string for a topic-scoped chat.
	// Empty string defaults to "-" (root chat).
	RootTag string
	// OnMessage is called for every inbound kind:9 message.
	OnMessage func(InboundMessage)
	// OnError is called for subscription errors.
	OnError func(error)
}

// ChatChannel subscribes to NIP-C7 kind:9 chat messages on one or more relays.
type ChatChannel struct {
	id       string
	rootTag  string
	keyer    nostr.Keyer
	relays   []string
	pool     *nostr.Pool
	ownsPool bool
	ctx      context.Context
	cancel   context.CancelFunc
	onMsg   func(InboundMessage)
	onErr   func(error)
	pubkey  string

	// lastEventMu protects lastEventID for quote-reply threading.
	lastEventMu sync.Mutex
	lastEventID string
}

// NewChatChannel creates and starts a NIP-C7 chat channel subscription.
func NewChatChannel(parent context.Context, opts ChatChannelOptions) (*ChatChannel, error) {
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("at least one relay is required")
	}

	rootTag := opts.RootTag
	if rootTag == "" {
		rootTag = "-"
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
		return nil, fmt.Errorf("chat: get public key: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)

	// Build a stable channel ID from relays + rootTag.
	channelID := "chat:" + rootTag
	if len(opts.Relays) > 0 {
		channelID = "chat:" + opts.Relays[0] + ":" + rootTag
	}

	ch := &ChatChannel{
		id:       channelID,
		rootTag:  rootTag,
		keyer:    keyer,
		relays:   opts.Relays,
		pool:     pool,
		ownsPool: ownsPool,
		ctx:     ctx,
		cancel:  cancel,
		onMsg:   opts.OnMessage,
		onErr:   opts.OnError,
		pubkey:  pk.Hex(),
	}

	go ch.subscribeLoop(ctx)
	return ch, nil
}

// ID implements Channel.
func (c *ChatChannel) ID() string { return c.id }

// Type implements Channel.
func (c *ChatChannel) Type() string { return "nipc7-chat" }

// Send posts a kind:9 chat message. If parentEventID is non-empty, it adds
// a `q` tag to create a threaded reply per NIP-C7.
func (c *ChatChannel) Send(ctx context.Context, text string) error {
	return c.SendWithReply(ctx, text, "")
}

// SendWithReply posts a kind:9 with an optional `q` tag for threading.
func (c *ChatChannel) SendWithReply(ctx context.Context, text, parentEventID string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text must not be empty")
	}

	tags := nostr.Tags{}

	// Add the root tag ("-" for relay root chat, or a topic tag).
	if c.rootTag != "" {
		tags = append(tags, nostr.Tag{"-", c.rootTag})
	}

	// Add quote-reply tag if replying to a specific message.
	if parentEventID != "" {
		relay := ""
		if len(c.relays) > 0 {
			relay = c.relays[0]
		}
		tags = append(tags, nostr.Tag{"q", parentEventID, relay, ""})
	}

	evt := nostr.Event{
		Kind:      KindChat,
		Content:   text,
		CreatedAt: nostr.Now(),
		Tags:      tags,
	}
	if err := c.keyer.SignEvent(ctx, &evt); err != nil {
		return fmt.Errorf("sign chat message: %w", err)
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
		return lastErr
	}
	return nil
}

// Close shuts down the subscription.
func (c *ChatChannel) Close() {
	c.cancel()
	if c.ownsPool {
		c.pool.Close("chat channel closed")
	}
}

// subscribeLoop listens for kind:9 chat messages on configured relays.
func (c *ChatChannel) subscribeLoop(ctx context.Context) {
	seen := NewSeenCache()

	since := applyJitter(nostr.Now(), DefaultSinceJitter)

	// Build filter: subscribe to kind:9 events with our root tag.
	filter := nostr.Filter{
		Kinds: []nostr.Kind{KindChat},
		Since: since,
	}

	// If the root tag is "-" (relay root chat), filter on the "-" tag.
	// For topic-scoped chats, filter on the "-" tag with the topic value.
	if c.rootTag != "" {
		filter.Tags = nostr.TagMap{"-": []string{c.rootTag}}
	}

	events, closedCh := c.pool.SubscribeManyNotifyClosed(
		ctx, c.relays, filter, nostr.SubscriptionOptions{},
	)

	for {
		select {
		case re, ok := <-events:
			if !ok {
				return
			}
			ev := re.Event
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
			if re.Relay != nil {
				relayURL = re.Relay.URL
			}

			// Track last event for default reply threading.
			c.lastEventMu.Lock()
			c.lastEventID = evIDHex
			c.lastEventMu.Unlock()

			c.onMsg(InboundMessage{
				ChannelID:  c.id,
				GroupID:    c.rootTag,
				Relay:      relayURL,
				FromPubKey: senderHex,
				Text:       ev.Content,
				EventID:    evIDHex,
				CreatedAt:  int64(ev.CreatedAt),
				Reply: func(replyCtx context.Context, replyText string) error {
					return c.SendWithReply(replyCtx, replyText, evIDHex)
				},
			})

		case rc, ok := <-closedCh:
			if !ok {
				continue
			}
			if !rc.HandledAuth && c.onErr != nil {
				c.onErr(fmt.Errorf("chat sub closed by %s: %s", rc.Relay.URL, rc.Reason))
			}

		case <-ctx.Done():
			return
		}
	}
}
