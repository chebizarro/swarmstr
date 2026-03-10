// Package dvm implements a NIP-90 Data Vending Machine handler.
//
// The handler subscribes to kind:5000-5999 job request events addressed to the
// agent pubkey (via #p tag), dispatches each request as an agent turn, and
// publishes kind:6000-6999 results + kind:7000 status events.
package dvm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

// JobHandler is called for each incoming DVM job request.
// It receives the decoded input text and must return the result content or an error.
type JobHandler func(ctx context.Context, jobID string, kind int, input string) (string, error)

// HandlerOpts configures the DVM handler.
type HandlerOpts struct {
	// Keyer is the signing interface used to publish statuses and results.
	Keyer nostr.Keyer
	// Relays is the list of relays to subscribe to and publish on.
	Relays []string
	// AcceptedKinds is the set of request kinds to handle (5000-5999).
	// Defaults to {5000} if empty.
	AcceptedKinds []int
	// OnJob is called for each accepted job request.
	OnJob JobHandler
	// MaxConcurrentJobs bounds in-flight job handlers. Defaults to 8.
	MaxConcurrentJobs int
}

// Handler manages NIP-90 DVM subscriptions and result publishing.
type Handler struct {
	opts   HandlerOpts
	keyer  nostr.Keyer
	pubkey nostr.PubKey
	pool   *nostr.Pool
	ctx    context.Context // saved for keyer.SignEvent calls
	jobSem chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Start creates a Handler and begins listening for job requests.
func Start(ctx context.Context, opts HandlerOpts) (*Handler, error) {
	if opts.OnJob == nil {
		return nil, fmt.Errorf("dvm: OnJob handler is required")
	}
	if opts.Keyer == nil {
		return nil, fmt.Errorf("dvm: keyer is required")
	}
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("dvm: Relays must be non-empty")
	}

	ks := opts.Keyer
	pk, err := ks.GetPublicKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("dvm: get public key from keyer: %w", err)
	}
	pubkey := pk

	if len(opts.AcceptedKinds) == 0 {
		opts.AcceptedKinds = []int{5000}
	}
	if opts.MaxConcurrentJobs <= 0 {
		opts.MaxConcurrentJobs = 8
	}

	ctx2, cancel := context.WithCancel(ctx)
	h := &Handler{
		opts:   opts,
		keyer:  ks,
		pubkey: pubkey,
		pool:   nostr.NewPool(nostr.PoolOptions{}),
		ctx:    ctx2,
		jobSem: make(chan struct{}, opts.MaxConcurrentJobs),
		cancel: cancel,
	}

	h.wg.Add(1)
	go h.run(ctx2)
	return h, nil
}

// Stop shuts down the handler gracefully.
func (h *Handler) Stop() {
	h.cancel()
	h.wg.Wait()
	h.pool.Close("dvm stopped")
}

func (h *Handler) run(ctx context.Context) {
	defer h.wg.Done()

	kinds := make([]nostr.Kind, len(h.opts.AcceptedKinds))
	for i, k := range h.opts.AcceptedKinds {
		kinds[i] = nostr.Kind(k)
	}

	f := nostr.Filter{
		Kinds: kinds,
		Tags:  nostr.TagMap{"p": []string{h.pubkey.Hex()}},
	}

	sub := h.pool.SubscribeMany(ctx, h.opts.Relays, f, nostr.SubscriptionOptions{})

	for {
		select {
		case <-ctx.Done():
			return
		case re, ok := <-sub:
			if !ok {
				return
			}
			select {
			case h.jobSem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			h.wg.Add(1)
			go func(ev nostr.Event) {
				defer h.wg.Done()
				defer func() { <-h.jobSem }()
				h.handleJob(ctx, ev)
			}(re.Event)
		}
	}
}

func (h *Handler) handleJob(ctx context.Context, ev nostr.Event) {
	jobID := ev.ID.Hex()
	reqKind := int(ev.Kind)
	resultKind := reqKind + 1000 // 5000 → 6000, 5001 → 6001, etc.

	// Publish processing status (kind:7000).
	h.publishStatus(ctx, jobID, ev.PubKey.Hex(), "processing", "")

	// Extract input from "i" tags: ["i", content, type].
	input := extractInput(ev)

	// Dispatch to the job handler.
	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	result, err := h.opts.OnJob(jobCtx, jobID, reqKind, input)
	if err != nil {
		log.Printf("dvm: job %s error: %v", jobID, err)
		h.publishStatus(ctx, jobID, ev.PubKey.Hex(), "error", err.Error())
		return
	}

	// Publish result (kind:6000-6999).
	h.publishResult(ctx, jobID, ev.PubKey.Hex(), resultKind, result)
	// Publish success status.
	h.publishStatus(ctx, jobID, ev.PubKey.Hex(), "success", "")
}

func (h *Handler) signEvent(ctx context.Context, evt *nostr.Event) error {
	return h.keyer.SignEvent(ctx, evt)
}

func (h *Handler) publishResult(ctx context.Context, jobID, requesterPubkey string, kind int, content string) {
	evt := nostr.Event{
		Kind:      nostr.Kind(kind),
		Content:   content,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"e", jobID},
			{"p", requesterPubkey},
			{"request", jobID},
		},
	}
	evt.PubKey = h.pubkey
	if err := h.signEvent(ctx, &evt); err != nil {
		log.Printf("dvm: sign result: %v", err)
		return
	}
	h.publish(ctx, evt)
}

func (h *Handler) publishStatus(ctx context.Context, jobID, requesterPubkey, status, extraMsg string) {
	content := status
	if extraMsg != "" {
		content = status + ": " + extraMsg
	}
	evt := nostr.Event{
		Kind:      7000,
		Content:   content,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"e", jobID},
			{"p", requesterPubkey},
			{"status", status},
		},
	}
	evt.PubKey = h.pubkey
	if err := h.signEvent(ctx, &evt); err != nil {
		log.Printf("dvm: sign status: %v", err)
		return
	}
	h.publish(ctx, evt)
}

func (h *Handler) publish(ctx context.Context, evt nostr.Event) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, relayURL := range h.opts.Relays {
		r, err := h.pool.EnsureRelay(relayURL)
		if err != nil {
			continue
		}
		if err := r.Publish(ctx2, evt); err != nil {
			log.Printf("dvm: publish to %s: %v", relayURL, err)
		}
	}
}

// extractInput pulls the first "i" tag content from a job request event.
func extractInput(ev nostr.Event) string {
	for _, tag := range ev.Tags {
		if len(tag) >= 2 && tag[0] == "i" {
			return tag[1]
		}
	}
	// Fall back to event content.
	return ev.Content
}

// PublishJobID is a convenience for agent tools that want to publish a DVM result directly.
func FormatResult(jobID, requesterPubkey, outputType, content string) string {
	m := map[string]any{
		"job_id":          jobID,
		"requester":       requesterPubkey,
		"output_type":     outputType,
		"result_content":  content,
	}
	b, _ := json.Marshal(m)
	return string(b)
}
