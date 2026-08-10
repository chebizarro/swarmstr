// Package channels provides the multi-channel messaging framework for Metiq.
//
// It supports NIP-17 (private DMs, already handled by nostr/runtime) and
// NIP-29 (relay-based group chat), NIP-CAS-0007 Communikey communities, and
// NIP-CAS-0008 Concord communities.
// New channel types can be added by
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

	metricspkg "metiq/internal/metrics"
	okpublish "metiq/internal/nostr/publish"
	nostruntime "metiq/internal/nostr/runtime"
)

func parseNIP29GroupAddress(addr string) (nip29.GroupAddress, error) {
	addr = strings.TrimSpace(addr)
	parts := strings.SplitN(addr, "'", 2)
	if len(parts) != 2 {
		return nip29.GroupAddress{}, fmt.Errorf("expected relay'groupID")
	}
	relay := strings.TrimSpace(parts[0])
	groupID := strings.TrimSpace(parts[1])
	if relay == "" || groupID == "" {
		return nip29.GroupAddress{}, fmt.Errorf("relay and group ID are required")
	}
	return nip29.GroupAddress{Relay: relay, ID: groupID}, nil
}

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
	// Meta carries NIP-29 mention/thread facts extracted from the event tags,
	// consumed by the preflight decision (mentions, reply/quote, backfill).
	Meta NostrInboundMeta
	// Reply sends a reply back to the channel/sender.
	Reply func(ctx context.Context, text string) error
	// React publishes an emoji reaction targeting this inbound event (the same
	// publish path the ACK conversion and status reactions use). Nil for
	// channels without reaction support; callers must nil-check.
	React func(ctx context.Context, emoji string) error
	// ResponderTakeover marks a responder-election takeover redelivery (R2):
	// the elected responder stayed silent past the window and this successor
	// re-verifies + claims the event. Never set on transport deliveries.
	ResponderTakeover bool
	// Settle reports the dispatch outcome for delivery-confirmed seen-gating:
	// true = processed/delivered (the event stays durably seen); false =
	// retryable failure (model timeout / signer failure / unconfirmed send),
	// which triggers bounded redispatch. Nil for channels without seen-gating;
	// callers must nil-check. It should be called exactly once per dispatch.
	Settle func(deliveredOK bool)
}

// extractNIP29Meta parses NIP-29 mention/thread facts from a kind:9 event's
// tags. liveSince marks when this subscription went live; events older than it
// are treated as backfill/replay (never trip loop guards — the preflight drops
// them as ambient before any stateful record).
func extractNIP29Meta(ev nostr.Event, eventID string, liveSince nostr.Timestamp) NostrInboundMeta {
	meta := NostrInboundMeta{EventID: eventID, DeliveryPhase: "live"}
	if liveSince > 0 && ev.CreatedAt < liveSince {
		meta.DeliveryPhase = DeliveryPhaseBackfill
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 || tag[1] == "" {
			continue
		}
		switch tag[0] {
		case "p":
			meta.MentionedPubkeys = append(meta.MentionedPubkeys, tag[1])
		case "e":
			// ["e", <id>, <relay>, <marker>, <pubkey?>] (NIP-10/NIP-29).
			marker := ""
			if len(tag) >= 4 {
				marker = tag[3]
			}
			author := ""
			if len(tag) >= 5 {
				author = tag[4]
			}
			switch marker {
			case "root":
				meta.ThreadRootEventID = tag[1]
			case "reply", "":
				if author != "" {
					meta.ReplyToSenderPubkey = author
				}
				if marker == "reply" || meta.ReplyToEventID == "" {
					meta.ReplyToEventID = tag[1]
				}
				if marker == "reply" || meta.ThreadRootEventID == "" {
					// A bare e-tag (no marker) is a reply target in NIP-10.
					if meta.ThreadRootEventID == "" {
						meta.ThreadRootEventID = tag[1]
					}
				}
			}
		case "q":
			// ["q", <id>, <relay?>, <pubkey?>] — quote with optional author hint.
			if len(tag) >= 4 && tag[3] != "" {
				meta.QuoteSenderPubkey = tag[3]
			}
		}
	}
	if meta.ThreadRootEventID == "" {
		meta.ThreadRootEventID = eventID
	}
	return meta
}

// InboundReaction is a normalised inbound room reaction (kind:7), consumed by
// the responder-election takeover coordinator (R2): a reply/claim reaction on
// a contested event stands a pending takeover down.
type InboundReaction struct {
	ChannelID     string // registry key ("relay'groupID" for NIP-29)
	GroupID       string
	Relay         string
	FromPubKey    string
	Content       string
	EventID       string
	TargetEventID string
	CreatedAt     int64
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
	if ch == nil {
		return fmt.Errorf("channel is nil")
	}
	id := strings.TrimSpace(ch.ID())
	if id == "" {
		return fmt.Errorf("channel ID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.channels[id]; ok {
		return fmt.Errorf("channel %q already joined", id)
	}
	r.channels[id] = ch
	r.order = append(r.order, id)
	return nil
}

// Remove closes and removes a channel by ID.
func (r *Registry) Remove(id string) error {
	id = strings.TrimSpace(id)
	r.mu.Lock()
	ch, ok := r.channels[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("channel %q not found", id)
	}
	delete(r.channels, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	ch.Close()
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
		if ch == nil {
			continue
		}
		out = append(out, ChannelInfo{ID: ch.ID(), Type: ch.Type()})
	}
	return out
}

// CloseAll closes every registered channel.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	channels := make([]Channel, 0, len(r.order))
	for _, id := range r.order {
		if ch := r.channels[id]; ch != nil {
			channels = append(channels, ch)
		}
	}
	r.channels = make(map[string]Channel)
	r.order = nil
	r.mu.Unlock()
	for _, ch := range channels {
		ch.Close()
	}
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
	// OnReaction, when set, subscribes to the room's kind:7 reactions and is
	// called for every inbound (non-own) reaction. Used by the R2
	// responder-election takeover coordinator.
	OnReaction func(InboundReaction)
	// OnError is called for subscription errors.
	OnError func(error)
	// PendingStorePath, when set, enables durable cross-restart replay of
	// unsettled inbound events (persisted to this file). Empty disables it.
	PendingStorePath string
	// AckAsReaction converts pure-ACK replies with a known target into NIP-25
	// reactions instead of posting another kind-9 room message. Nil enables the
	// default; an explicit false opts the room out.
	AckAsReaction *bool
	// CommitmentEnforcement rewrites unbacked work promises before publish.
	// It is an explicit per-room taskflow opt-in.
	CommitmentEnforcement bool
}

// NIP29GroupChannel subscribes to a NIP-29 relay-based group (kind 9) and
// allows the agent to send messages back.
type NIP29GroupChannel struct {
	id string
	// roomKey is the normalized per-room scorecard/session key
	// (NormalizeNostrRoomSessionKey(group address)).
	roomKey               string
	gad                   nip29.GroupAddress
	hub                   *nostruntime.NostrHub // non-nil when using shared hub
	pool                  *nostr.Pool           // non-nil only in legacy (no-hub) mode
	publisher             okpublish.Publisher   // publish path; defaults to pool
	ownsPool              bool                  // true when we created the pool ourselves
	keyer                 nostr.Keyer
	ctx                   context.Context
	cancel                context.CancelFunc
	onMsg                 func(InboundMessage)
	onReaction            func(InboundReaction)
	onErr                 func(error)
	pubkey                string
	ackAsReaction         bool
	commitmentEnforcement bool

	seen       *SeenCache
	lastSeenMu sync.Mutex
	lastSeen   nostr.Timestamp
	// liveSince marks when this channel started subscribing; inbound events
	// older than it are treated as backfill/replay by the preflight.
	liveSince nostr.Timestamp
	// redispatch bounds retries for events whose dispatch failed delivery
	// (seen-gating; ocn-zi7 / ocn-8kh).
	redispatch *RedispatchScheduler
	// inflight is a NON-EXPIRING guard for events currently in the retry
	// lifecycle, so a relay redelivery cannot double-dispatch one even if its
	// (TTL'd) seen entry expires mid-retry. Cleared on terminal settlement.
	inflightMu sync.Mutex
	inflight   map[string]struct{}
	// recentIDs is a bounded ring of recent NON-OWN room event ids, used to
	// stamp the NIP-29 `previous` tag on outbound messages.
	recentMu  sync.Mutex
	recentIDs []string
	// pending durably records unsettled events for cross-restart replay (qye5);
	// nil when durable replay is not configured.
	pending *PendingEventStore
}

// replayPending re-dispatches events that were still unsettled when the process
// last stopped, giving them fresh in-process retry attempts. Each is marked seen
// first so a concurrent relay redelivery cannot double-dispatch it.
func (c *NIP29GroupChannel) replayPending() {
	if c.pending == nil || c.onMsg == nil {
		return
	}
	for _, ev := range c.pending.Pending() {
		if c.ctx != nil && c.ctx.Err() != nil {
			return
		}
		id := ev.ID.Hex()
		// Skip if the live subscription already redelivered this event this
		// session (seen.Add reports it as a duplicate); otherwise mark it seen so
		// a later relay redelivery is deduped, then replay it.
		if c.seen.Add(id) {
			continue
		}
		c.dispatchInbound(ev, id)
	}
}

func (c *NIP29GroupChannel) recordRecentEvent(id string) {
	c.recentMu.Lock()
	c.recentIDs = append(c.recentIDs, id)
	if len(c.recentIDs) > NIP29MaxPreviousRefs {
		trimmed := make([]string, NIP29MaxPreviousRefs)
		copy(trimmed, c.recentIDs[len(c.recentIDs)-NIP29MaxPreviousRefs:])
		c.recentIDs = trimmed
	}
	c.recentMu.Unlock()
}

func (c *NIP29GroupChannel) snapshotRecentIDs() []string {
	c.recentMu.Lock()
	defer c.recentMu.Unlock()
	out := make([]string, len(c.recentIDs))
	copy(out, c.recentIDs)
	return out
}

func (c *NIP29GroupChannel) markInflight(id string) {
	c.inflightMu.Lock()
	if c.inflight == nil {
		c.inflight = map[string]struct{}{}
	}
	c.inflight[id] = struct{}{}
	c.inflightMu.Unlock()
}

func (c *NIP29GroupChannel) unmarkInflight(id string) {
	c.inflightMu.Lock()
	delete(c.inflight, id)
	c.inflightMu.Unlock()
}

func (c *NIP29GroupChannel) isInflight(id string) bool {
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	_, ok := c.inflight[id]
	return ok
}

func resolveNIP29ACKAsReaction(configured *bool) bool {
	return configured == nil || *configured
}

// NewNIP29GroupChannel creates and starts a NIP-29 group subscription.
func NewNIP29GroupChannel(parent context.Context, opts NIP29GroupChannelOptions) (*NIP29GroupChannel, error) {
	if opts.GroupAddress == "" {
		return nil, fmt.Errorf("group_address is required (format: relay'groupID)")
	}

	gad, err := parseNIP29GroupAddress(opts.GroupAddress)
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
		pool = nostruntime.NewPoolNIP42(keyer)
		ownsPool = true
	}

	pk, err := keyer.GetPublicKey(parent)
	if err != nil {
		return nil, fmt.Errorf("nip29: get public key from keyer: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)

	ch := &NIP29GroupChannel{
		id:                    opts.GroupAddress,
		roomKey:               NormalizeNostrRoomSessionKey(opts.GroupAddress),
		gad:                   gad,
		hub:                   hub,
		pool:                  pool,
		publisher:             pool,
		ownsPool:              ownsPool,
		keyer:                 keyer,
		ctx:                   ctx,
		cancel:                cancel,
		onMsg:                 opts.OnMessage,
		onReaction:            opts.OnReaction,
		onErr:                 opts.OnError,
		pubkey:                pk.Hex(),
		ackAsReaction:         resolveNIP29ACKAsReaction(opts.AckAsReaction),
		commitmentEnforcement: opts.CommitmentEnforcement,
		seen:                  NewSeenCache(),
		liveSince:             nostr.Now(),
		redispatch:            NewRedispatchScheduler(RedispatchSchedulerOptions{}),
		inflight:              map[string]struct{}{},
	}

	if opts.PendingStorePath != "" {
		if ps, err := NewPendingEventStore(opts.PendingStorePath); err != nil {
			if ch.onErr != nil {
				ch.onErr(fmt.Errorf("nip29 pending store: %w", err))
			}
		} else {
			ch.pending = ps
			go ch.replayPending()
		}
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
	var commitmentBlocked bool
	text, commitmentBlocked = EnforceOutboundCommitment(ctx, text, c.commitmentEnforcement)
	if commitmentBlocked {
		// R7 scorecard: an unbacked work promise was rewritten at the wire.
		metricspkg.RecordRoomSignal(c.roomKey, metricspkg.RoomSignalCommitmentBlocked)
	}
	if text == "" {
		return fmt.Errorf("text must not be empty")
	}

	tags := nostr.Tags{{"h", c.gad.ID}}
	// Anchor the message to recent room events (NIP-29 `previous`, best-effort,
	// excludes the bot's own events).
	if prev := BuildPreviousEventTag(c.snapshotRecentIDs()); prev != nil {
		tags = append(tags, prev)
	}
	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupChatMessage,
		Content:   text,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
	}
	if err := c.signAndPublish(ctx, evt); err != nil {
		return err
	}
	// R7 scorecard: our own delivered room message counts toward the per-room
	// message-share window (inbound peers are counted at the loop-control gate).
	metricspkg.RecordRoomMessage(c.roomKey, c.pubkey)
	return nil
}

// sendReply posts text normally unless this room opted into ACK conversion and
// the reply target is fully known. A failed reaction publish is returned as a
// delivery failure rather than falling back to a chat message (which could
// duplicate on retry).
func (c *NIP29GroupChannel) sendReply(ctx context.Context, text, targetEventID, targetPubkey string) error {
	if c.ackAsReaction && strings.TrimSpace(targetEventID) != "" && strings.TrimSpace(targetPubkey) != "" {
		if emoji, ok := ClassifyPureACK(text); ok {
			if err := c.SendReaction(ctx, emoji, targetEventID, targetPubkey, int(nostr.KindSimpleGroupChatMessage)); err != nil {
				return err
			}
			metricspkg.OutboundACKReactions.Inc()
			metricspkg.RecordRoomSignal(c.roomKey, metricspkg.RoomSignalACKConversion)
			return nil
		}
	}
	return c.Send(ctx, text)
}

// signAndPublish signs evt and publishes it to the group relay, bounding the
// wait so a wedged relay cannot hang the send (PublishToAny returns on the first
// relay OK). Shared by Send, SendReaction, and DeleteEvent.
func (c *NIP29GroupChannel) signAndPublish(ctx context.Context, evt nostr.Event) (err error) {
	defer func() {
		outcome := "success"
		if err != nil {
			outcome = "failure"
		}
		metricspkg.RecordPublishOutcome("nip29", outcome)
	}()

	if err := c.keyer.SignEvent(ctx, &evt); err != nil {
		return fmt.Errorf("sign group event: %w", err)
	}
	publisher := c.publisher
	if publisher == nil {
		publisher = c.pool
	}
	pubCtx, cancel := context.WithTimeout(ctx, nip29PublishTimeout)
	defer cancel()
	if _, err := okpublish.PublishToAny(pubCtx, publisher, []string{c.gad.Relay}, evt); err != nil {
		return fmt.Errorf("nip29 publish to group %s: %w", c.gad, err)
	}
	return nil
}

// SendReaction publishes a NIP-29 room reaction (kind:7) to targetEventID.
// targetPubkey (the target event's author) is required by NIP-25; targetKind is
// optional (0 to omit). Reactions are immutable — to undo one, delete the
// reaction event via DeleteEvent rather than "un-reacting".
func (c *NIP29GroupChannel) SendReaction(ctx context.Context, emoji, targetEventID, targetPubkey string, targetKind int) error {
	evt, err := BuildNIP29ReactionEvent(c.gad.ID, emoji, targetEventID, targetPubkey, targetKind, c.snapshotRecentIDs())
	if err != nil {
		return err
	}
	return c.signAndPublish(ctx, evt)
}

// DeleteEvent publishes a NIP-29 delete-event (kind:9005) for targetEventID with
// an optional reason (the "unsend" capability).
func (c *NIP29GroupChannel) DeleteEvent(ctx context.Context, targetEventID, reason string) error {
	evt, err := BuildNIP29DeletionEvent(c.gad.ID, targetEventID, reason, c.snapshotRecentIDs())
	if err != nil {
		return err
	}
	return c.signAndPublish(ctx, evt)
}

// Capabilities advertises the outbound features this channel supports.
func (c *NIP29GroupChannel) Capabilities() []string {
	return []string{"reactions", "reply", "threads", "unsend"}
}

// Close shuts down the subscription.  Only closes the pool if we own it.
func (c *NIP29GroupChannel) Close() {
	c.cancel()
	if c.ownsPool {
		c.pool.Close("nip29 channel closed")
	}
}

// subscribeLoop listens for kind-9 messages on the group relay using
// SubscribeManyNotifyClosed for proper CLOSED signal handling.  When the
// underlying stream terminates, it reissues the subscription with an overlapping
// backfill window derived from the last processed event timestamp.
func (c *NIP29GroupChannel) subscribeLoop(ctx context.Context) {
	if c.seen == nil {
		c.seen = NewSeenCache()
	}

	backoff := channelReconnectInitialBackoff
	for ctx.Err() == nil {
		// The takeover coordinator (R2) also needs the room's reactions (a ✅
		// ack from the elected responder or a 🙋 claim from an earlier
		// successor stands a pending takeover down).
		kinds := []nostr.Kind{nostr.KindSimpleGroupChatMessage}
		if c.onReaction != nil {
			kinds = append(kinds, nostr.KindReaction)
		}
		filter := nostr.Filter{
			Kinds: kinds,
			Tags:  nostr.TagMap{"h": []string{c.gad.ID}},
			Since: c.subscribeSince(),
		}

		subCtx, cancelSub := context.WithCancel(ctx)
		events, closedCh := channelSubscribeManyNotifyClosed(
			subCtx, c.pool, []string{c.gad.Relay}, filter, nostr.SubscriptionOptions{},
		)

		processed := c.consumeSubscription(subCtx, events, closedCh)
		cancelSub()
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

func (c *NIP29GroupChannel) consumeSubscription(ctx context.Context, events <-chan nostr.RelayEvent, closedCh <-chan nostr.RelayClosed) (processed bool) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return processed
			}
			if c.handleEvent(ev) {
				processed = true
			}

		case rc, ok := <-closedCh:
			if !ok {
				closedCh = nil
				continue
			}
			if rc.HandledAuth {
				continue
			}
			c.reportClosed(rc)
			return processed

		case <-ctx.Done():
			return processed
		}
	}
}

func (c *NIP29GroupChannel) handleEvent(ev nostr.RelayEvent) bool {
	if ev.Kind == nostr.KindReaction {
		return c.handleReactionEvent(ev)
	}
	if !validChannelEvent(ev.Event, nostr.KindSimpleGroupChatMessage, "h", c.gad.ID) {
		return false
	}
	evIDHex := ev.ID.Hex()
	// Skip events that already exhausted their bounded retries this process, or
	// are currently mid-retry (both process-local; a restart clears them and
	// relay redelivery retries best-effort within its replay window).
	if c.redispatch != nil && c.redispatch.GaveUp(evIDHex) {
		return false
	}
	if c.isInflight(evIDHex) {
		return false
	}
	if c.seen.Add(evIDHex) {
		return false // recently seen
	}
	c.recordLastSeen(ev.CreatedAt)
	if ev.PubKey.Hex() == c.pubkey {
		return true
	}
	// Track non-own room events for the outbound `previous` tag.
	c.recordRecentEvent(evIDHex)
	if c.onMsg == nil {
		return true
	}
	c.dispatchInbound(ev.Event, evIDHex)
	return true
}

// handleReactionEvent forwards a room reaction to the OnReaction observer
// (R2 takeover cancellation). Reactions never enter the message dispatch,
// redispatch, or `previous`-tag paths.
func (c *NIP29GroupChannel) handleReactionEvent(ev nostr.RelayEvent) bool {
	if c.onReaction == nil {
		return false
	}
	if !validChannelEvent(ev.Event, nostr.KindReaction, "h", c.gad.ID) {
		return false
	}
	evIDHex := ev.ID.Hex()
	if c.seen.Add(evIDHex) {
		return false // recently seen
	}
	if ev.PubKey.Hex() == c.pubkey {
		return true
	}
	target := ""
	// NIP-25: the last "e" tag is the reacted-to event.
	for _, tag := range ev.Tags {
		if len(tag) >= 2 && tag[0] == "e" && tag[1] != "" {
			target = tag[1]
		}
	}
	if target == "" {
		return true
	}
	c.onReaction(InboundReaction{
		ChannelID:     c.id,
		GroupID:       c.gad.ID,
		Relay:         c.gad.Relay,
		FromPubKey:    ev.PubKey.Hex(),
		Content:       ev.Content,
		EventID:       evIDHex,
		TargetEventID: target,
		CreatedAt:     int64(ev.CreatedAt),
	})
	return true
}

// dispatchInbound builds the inbound context and invokes the handler. It is
// called for the initial delivery, re-invoked by the bounded redispatch
// scheduler on a delivery failure, and by the durable pending-event replay on
// startup. It takes a plain nostr.Event so a persisted event can be replayed.
func (c *NIP29GroupChannel) dispatchInbound(ev nostr.Event, evIDHex string) {
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
		Meta:       extractNIP29Meta(ev, evIDHex, c.liveSince),
		Reply: func(ctx context.Context, text string) error {
			return c.sendReply(ctx, text, evIDHex, senderHex)
		},
		React: func(ctx context.Context, emoji string) error {
			return c.SendReaction(ctx, emoji, evIDHex, senderHex, int(nostr.KindSimpleGroupChatMessage))
		},
		Settle: func(deliveredOK bool) {
			c.settleDispatch(ev, evIDHex, deliveredOK)
		},
	})
}

// settleDispatch applies delivery-confirmed seen-gating. On success the event
// stays seen and its in-flight/retry state is cleared. On a retryable failure
// the event is actively re-dispatched on a bounded backoff (30s/2m/5m, 3
// attempts); it stays seen AND in-flight across retries so a relay redelivery
// cannot double-dispatch it. After give-up the event is marked GaveUp (checked
// in handleEvent) which blocks reprocessing for the rest of this process; a
// restart clears the in-memory state and retries best-effort within the relay
// replay window. A pending-event store, when configured, persists the unsettled
// event so a crash/restart mid-retry replays it (durable at-least-once).
func (c *NIP29GroupChannel) settleDispatch(ev nostr.Event, evIDHex string, deliveredOK bool) {
	if c.redispatch == nil {
		return
	}
	if deliveredOK {
		c.unmarkInflight(evIDHex)
		c.redispatch.Succeeded(evIDHex)
		if c.pending != nil {
			_ = c.pending.Remove(evIDHex)
		}
		return
	}
	// Retryable failure: durably persist the unsettled event (survives restart),
	// protect the retry window (non-expiring), and schedule a bounded, backed-off
	// re-dispatch. The event stays seen throughout so a relay redelivery cannot
	// double-dispatch it. After give-up, redispatch marks the event GaveUp
	// (checked in handleEvent), which blocks reprocessing for the rest of this
	// process; the event remains persisted so a restart retries it.
	if c.pending != nil {
		_ = c.pending.Add(evIDHex, ev)
	}
	c.markInflight(evIDHex)
	if _, scheduled := c.redispatch.Schedule(evIDHex, func() {
		if c.ctx == nil || c.ctx.Err() == nil {
			c.dispatchInbound(ev, evIDHex)
		}
	}); !scheduled {
		c.unmarkInflight(evIDHex)
	}
}

func (c *NIP29GroupChannel) subscribeSince() nostr.Timestamp {
	c.lastSeenMu.Lock()
	lastSeen := c.lastSeen
	c.lastSeenMu.Unlock()
	if lastSeen == 0 {
		lastSeen = nostr.Now()
	}
	return applyJitter(lastSeen, DefaultSinceJitter)
}

func (c *NIP29GroupChannel) recordLastSeen(ts nostr.Timestamp) {
	c.lastSeenMu.Lock()
	defer c.lastSeenMu.Unlock()
	if ts > c.lastSeen {
		c.lastSeen = ts
	}
}

func (c *NIP29GroupChannel) reportClosed(rc nostr.RelayClosed) {
	if c.onErr == nil || rc.HandledAuth {
		return
	}
	c.onErr(formatChannelClosed("nip29", rc))
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

	seen       *SeenCache
	lastSeenMu sync.Mutex
	lastSeen   nostr.Timestamp
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
		pool = nostruntime.NewPoolNIP42(keyer)
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
		seen:      NewSeenCache(),
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
	if c.seen == nil {
		c.seen = NewSeenCache()
	}

	backoff := channelReconnectInitialBackoff
	for ctx.Err() == nil {
		filter := nostr.Filter{
			Kinds: []nostr.Kind{nostr.KindChannelMessage},
			Tags:  nostr.TagMap{"e": []string{c.channelID}},
			Since: c.subscribeSince(),
		}

		subCtx, cancelSub := context.WithCancel(ctx)
		events, closedCh := channelSubscribeManyNotifyClosed(
			subCtx, c.pool, c.relays, filter, nostr.SubscriptionOptions{},
		)

		processed := c.consumeSubscription(subCtx, events, closedCh)
		cancelSub()
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

func (c *NIP28PublicChannel) consumeSubscription(ctx context.Context, events <-chan nostr.RelayEvent, closedCh <-chan nostr.RelayClosed) (processed bool) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return processed
			}
			if c.handleEvent(ev) {
				processed = true
			}

		case rc, ok := <-closedCh:
			if !ok {
				closedCh = nil
				continue
			}
			if rc.HandledAuth {
				continue
			}
			c.reportClosed(rc)
			return processed

		case <-ctx.Done():
			return processed
		}
	}
}

func (c *NIP28PublicChannel) handleEvent(ev nostr.RelayEvent) bool {
	if !validChannelEvent(ev.Event, nostr.KindChannelMessage, "e", c.channelID) {
		return false
	}
	evIDHex := ev.ID.Hex()
	if c.seen.Add(evIDHex) {
		return false // duplicate
	}
	c.recordLastSeen(ev.CreatedAt)
	if ev.PubKey.Hex() == c.pubkey {
		return true
	}
	if c.onMsg == nil {
		return true
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
	return true
}

func (c *NIP28PublicChannel) subscribeSince() nostr.Timestamp {
	c.lastSeenMu.Lock()
	lastSeen := c.lastSeen
	c.lastSeenMu.Unlock()
	if lastSeen == 0 {
		lastSeen = nostr.Now()
	}
	return applyJitter(lastSeen, DefaultSinceJitter)
}

func (c *NIP28PublicChannel) recordLastSeen(ts nostr.Timestamp) {
	c.lastSeenMu.Lock()
	defer c.lastSeenMu.Unlock()
	if ts > c.lastSeen {
		c.lastSeen = ts
	}
}

func (c *NIP28PublicChannel) reportClosed(rc nostr.RelayClosed) {
	if c.onErr == nil || rc.HandledAuth {
		return
	}
	c.onErr(formatChannelClosed("nip28", rc))
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type channelSubscribeFunc func(context.Context, *nostr.Pool, []string, nostr.Filter, nostr.SubscriptionOptions) (<-chan nostr.RelayEvent, <-chan nostr.RelayClosed)

var channelSubscribeManyNotifyClosed channelSubscribeFunc = func(ctx context.Context, pool *nostr.Pool, relays []string, filter nostr.Filter, opts nostr.SubscriptionOptions) (<-chan nostr.RelayEvent, <-chan nostr.RelayClosed) {
	return pool.SubscribeManyNotifyClosed(ctx, relays, filter, opts)
}

var (
	channelReconnectInitialBackoff = 250 * time.Millisecond
	channelReconnectMaxBackoff     = 30 * time.Second
	channelReconnectDelay          = waitChannelReconnectDelay
)

func waitChannelReconnectDelay(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func nextChannelReconnectBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return channelReconnectInitialBackoff
	}
	next := current * 2
	if next > channelReconnectMaxBackoff {
		return channelReconnectMaxBackoff
	}
	return next
}

func validChannelEvent(ev nostr.Event, kind nostr.Kind, tagName, tagValue string) bool {
	if ev.Kind != kind {
		return false
	}
	if tagName != "" && tagValue != "" && ev.Tags.FindWithValue(tagName, tagValue) == nil {
		return false
	}
	if !ev.CheckID() || !ev.VerifySignature() {
		return false
	}
	if ev.CreatedAt > nostr.Timestamp(time.Now().Add(10*time.Minute).Unix()) {
		return false
	}
	return true
}

func formatChannelClosed(kind string, rc nostr.RelayClosed) error {
	relayURL := "<unknown>"
	if rc.Relay != nil && rc.Relay.URL != "" {
		relayURL = rc.Relay.URL
	}
	return fmt.Errorf("%s sub closed by %s: %s", kind, relayURL, rc.Reason)
}
