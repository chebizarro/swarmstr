// Package nip38 implements NIP-38 User Statuses (kind 30315).
//
// NIP-38 defines replaceable status events that let agents broadcast their
// current activity as a structured status + optional free-form note.
//
// Supported status values:
//   - "idle"       – agent is available and not processing anything
//   - "typing"     – agent is composing a response to a DM
//   - "updating"   – agent is executing a tool or background task
//   - "dnd"        – agent is busy and will not respond
//   - "offline"    – agent is shutting down or pausing
//
// Usage:
//
//	ctrl := nip38.NewHeartbeat(nip38.HeartbeatOptions{Keyer: kr, Relays: relays})
//	defer ctrl.Stop()
//	ctrl.SetStatus(ctx, nip38.StatusTyping, "working on your request…")
package nip38

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// Status values for NIP-38 kind 30315 events.
const (
	StatusIdle     = "idle"
	StatusTyping   = "typing"
	StatusUpdating = "updating"
	StatusDND      = "dnd"
	StatusOffline  = "offline"
)

// HeartbeatOptions configures the NIP-38 heartbeat controller.
type HeartbeatOptions struct {
	// Keyer is used to sign status events. Required.
	Keyer nostr.Keyer
	// Relays is the list of relay URLs to publish status events to.
	Relays []string
	// IdleInterval is how often to re-publish the idle status (default: 5min).
	IdleInterval time.Duration
	// DefaultContent is optional free-form text published with the idle status.
	DefaultContent string
	// Enabled can be set to false to skip all publishing (no-op mode).
	Enabled bool
}

// Heartbeat manages a continuous NIP-38 status feed.
type Heartbeat struct {
	opts       HeartbeatOptions
	pool       *nostr.Pool
	pubkey     nostr.PubKey
	mu         sync.Mutex
	current    string
	currentMsg string
	ticker     *time.Ticker
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewHeartbeat creates and starts a NIP-38 heartbeat controller.
// It immediately publishes an idle status and starts the refresh ticker.
func NewHeartbeat(parent context.Context, opts HeartbeatOptions) (*Heartbeat, error) {
	if !opts.Enabled {
		// Return a no-op heartbeat.
		ctx, cancel := context.WithCancel(parent)
		return &Heartbeat{opts: opts, ctx: ctx, cancel: cancel}, nil
	}
	if opts.Keyer == nil {
		return nil, fmt.Errorf("nip38: Keyer is required")
	}
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("nip38: at least one relay is required")
	}
	if opts.IdleInterval <= 0 {
		opts.IdleInterval = 5 * time.Minute
	}

	pk, err := opts.Keyer.GetPublicKey(parent)
	if err != nil {
		return nil, fmt.Errorf("nip38: get public key: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	h := &Heartbeat{
		opts:    opts,
		pool:    nostr.NewPool(nostr.PoolOptions{PenaltyBox: true}),
		pubkey:  pk,
		current: StatusIdle,
		ticker:  time.NewTicker(opts.IdleInterval),
		ctx:     ctx,
		cancel:  cancel,
	}

	// Publish initial idle status.
	h.publish(ctx, StatusIdle, opts.DefaultContent, 0)

	// Start background refresh goroutine.
	h.wg.Add(1)
	go h.loop()

	return h, nil
}

// SetStatus publishes a new status event immediately.
// expiry is an optional Unix timestamp after which the status expires (0 = no expiry).
// The publish is dispatched asynchronously so callers are never blocked by relay
// latency or failures (status events are best-effort indicators).
func (h *Heartbeat) SetStatus(ctx context.Context, status, content string, expiry int64) {
	if !h.opts.Enabled {
		return
	}
	h.mu.Lock()
	h.current = status
	h.currentMsg = content
	h.mu.Unlock()
	go h.publish(ctx, status, content, expiry)
}

// SetIdle transitions to idle status (useful when an agent turn completes).
func (h *Heartbeat) SetIdle(ctx context.Context) {
	h.SetStatus(ctx, StatusIdle, h.opts.DefaultContent, 0)
}

// SetTyping transitions to typing status (call when composing a DM reply).
func (h *Heartbeat) SetTyping(ctx context.Context, note string) {
	h.SetStatus(ctx, StatusTyping, note, 0)
}

// SetUpdating transitions to updating status (call when running tools).
func (h *Heartbeat) SetUpdating(ctx context.Context, note string) {
	h.SetStatus(ctx, StatusUpdating, note, 0)
}

// Stop publishes an offline status and shuts down the heartbeat.
func (h *Heartbeat) Stop() {
	if h.opts.Enabled {
		h.publish(h.ctx, StatusOffline, "", 0)
	}
	h.cancel()
	if h.ticker != nil {
		h.ticker.Stop()
	}
	h.pool.Close("nip38 heartbeat stopped")
	h.wg.Wait()
}

func (h *Heartbeat) loop() {
	defer h.wg.Done()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-h.ticker.C:
			h.mu.Lock()
			cur := h.current
			msg := h.currentMsg
			h.mu.Unlock()
			// On tick, re-publish current status (keeps it fresh and
			// updates the timestamp so subscribers know the agent is alive).
			h.publish(h.ctx, cur, msg, 0)
		}
	}
}

func (h *Heartbeat) publish(ctx context.Context, status, content string, expiry int64) {
	tags := nostr.Tags{
		{"d", "general"},
		{"status", status},
	}
	if expiry > 0 {
		tags = append(tags, nostr.Tag{"expiration", fmt.Sprintf("%d", expiry)})
	}

	evt := nostr.Event{
		Kind:      30315,
		Content:   content,
		CreatedAt: nostr.Now(),
		Tags:      tags,
	}

	if err := h.opts.Keyer.SignEvent(ctx, &evt); err != nil {
		log.Printf("nip38: sign status event: %v", err)
		return
	}

	published := false
	for result := range h.pool.PublishMany(ctx, h.opts.Relays, evt) {
		if result.Error == nil {
			published = true
		} else {
			log.Printf("nip38: publish to %s: %v", result.RelayURL, result.Error)
		}
	}
	if !published && len(h.opts.Relays) > 0 {
		log.Printf("nip38: status event not accepted by any relay (status=%s)", status)
	}
}
