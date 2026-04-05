package channels

import (
	"context"
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
)

// RelayFilterEvent is a raw Nostr event delivered by a receive-only relay
// filter subscription.
type RelayFilterEvent struct {
	ChannelID  string
	Relay      string
	FromPubKey string
	Event      nostr.Event
}

// RelayFilterChannelOptions configure a receive-only relay-filter
// subscription.
type RelayFilterChannelOptions struct {
	ID      string
	Hub     *nostruntime.NostrHub
	Keyer   nostr.Keyer
	Relays  []string
	Filter  nostr.Filter
	OnEvent func(RelayFilterEvent)
	OnError func(error)
}

// RelayFilterChannel subscribes to an arbitrary Nostr filter and forwards raw
// matching events to an application callback.
type RelayFilterChannel struct {
	id       string
	filter   nostr.Filter
	keyer    nostr.Keyer
	relays   []string
	pool     *nostr.Pool
	ownsPool bool
	cancel   context.CancelFunc
	onEvent  func(RelayFilterEvent)
	onErr    func(error)
	pubkey   string
}

func NewRelayFilterChannel(parent context.Context, opts RelayFilterChannelOptions) (*RelayFilterChannel, error) {
	if strings.TrimSpace(opts.ID) == "" {
		return nil, fmt.Errorf("relay-filter channel id is required")
	}
	relays := sanitizeRelayFilterRelays(opts.Relays)
	if len(relays) == 0 {
		return nil, fmt.Errorf("at least one relay is required for relay-filter channel")
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
		return nil, fmt.Errorf("relay-filter: get public key: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	filter := cloneNostrFilter(opts.Filter)
	if filter.Since > 0 {
		filter.Since = applyJitter(filter.Since, DefaultSinceJitter)
	} else {
		filter.Since = applyJitter(nostr.Now(), DefaultSinceJitter)
	}
	ch := &RelayFilterChannel{
		id:       strings.TrimSpace(opts.ID),
		filter:   filter,
		keyer:    keyer,
		relays:   relays,
		pool:     pool,
		ownsPool: ownsPool,
		cancel:   cancel,
		onEvent:  opts.OnEvent,
		onErr:    opts.OnError,
		pubkey:   pk.Hex(),
	}
	go ch.subscribeLoop(ctx)
	return ch, nil
}

func (c *RelayFilterChannel) ID() string { return c.id }

func (c *RelayFilterChannel) Type() string { return "relay-filter" }

func (c *RelayFilterChannel) Send(context.Context, string) error {
	return fmt.Errorf("relay-filter channels are receive-only")
}

func (c *RelayFilterChannel) Close() {
	c.cancel()
	if c.ownsPool {
		c.pool.Close("relay-filter channel closed")
	}
}

func (c *RelayFilterChannel) subscribeLoop(ctx context.Context) {
	seen := NewSeenCache()
	events, closedCh := c.pool.SubscribeManyNotifyClosed(
		ctx, c.relays, c.filter, nostr.SubscriptionOptions{},
	)
	for {
		select {
		case re, ok := <-events:
			if !ok {
				return
			}
			ev := re.Event
			if !isVerifiedRelayFilterEvent(ev) {
				continue
			}
			evIDHex := ev.ID.Hex()
			if seen.Add(evIDHex) {
				continue
			}
			if ev.PubKey.Hex() == c.pubkey {
				continue
			}
			if c.onEvent == nil {
				continue
			}
			relayURL := ""
			if re.Relay != nil {
				relayURL = re.Relay.URL
			}
			c.onEvent(RelayFilterEvent{
				ChannelID:  c.id,
				Relay:      relayURL,
				FromPubKey: ev.PubKey.Hex(),
				Event:      ev,
			})

		case rc, ok := <-closedCh:
			if !ok {
				closedCh = nil
				continue
			}
			if c.onErr != nil {
				if err := relayFilterClosedError(rc); err != nil {
					c.onErr(err)
				}
			}

		case <-ctx.Done():
			return
		}
	}
}

func sanitizeRelayFilterRelays(relays []string) []string {
	out := make([]string, 0, len(relays))
	seen := make(map[string]struct{}, len(relays))
	for _, relay := range relays {
		cleaned := strings.TrimSpace(relay)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func isVerifiedRelayFilterEvent(ev nostr.Event) bool {
	return ev.CheckID() && ev.VerifySignature()
}

func relayFilterClosedError(rc nostr.RelayClosed) error {
	if rc.HandledAuth {
		return nil
	}
	relayURL := "<unknown relay>"
	if rc.Relay != nil && strings.TrimSpace(rc.Relay.URL) != "" {
		relayURL = rc.Relay.URL
	}
	return fmt.Errorf("relay-filter sub closed by %s: %s", relayURL, rc.Reason)
}

func cloneNostrFilter(in nostr.Filter) nostr.Filter {
	out := in
	if len(in.Kinds) > 0 {
		out.Kinds = append([]nostr.Kind(nil), in.Kinds...)
	}
	if len(in.Authors) > 0 {
		out.Authors = append([]nostr.PubKey(nil), in.Authors...)
	}
	if len(in.IDs) > 0 {
		out.IDs = append([]nostr.ID(nil), in.IDs...)
	}
	if len(in.Tags) > 0 {
		out.Tags = nostr.TagMap{}
		for key, values := range in.Tags {
			out.Tags[key] = append([]string(nil), values...)
		}
	}
	return out
}
