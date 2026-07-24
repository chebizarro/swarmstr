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
	"strconv"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	okpublish "metiq/internal/nostr/publish"
	runtime "metiq/internal/nostr/runtime"
)

// JobHandler is called for each incoming DVM job request.
// It receives the decoded input text and must return the result content or an error.
type JobHandler func(ctx context.Context, jobID string, kind int, input string) (string, error)

// JobInput is one NIP-90 input tag. Relay and Marker are optional.
type JobInput struct {
	Data   string `json:"data"`
	Type   string `json:"type"`
	Relay  string `json:"relay,omitempty"`
	Marker string `json:"marker,omitempty"`
}

// JobParam is one NIP-90 param tag.
type JobParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// JobRequest preserves all current-spec request fields for rich handlers.
type JobRequest struct {
	Event          nostr.Event
	Inputs         []JobInput
	Params         []JobParam
	Output         string
	BidMSat        int64
	ResponseRelays []string
	Encrypted      bool
}

// JobResult is the result returned by a rich NIP-90 handler.
type JobResult struct {
	Content         string
	AmountMSat      int64
	Invoice         string
	PaymentRequired bool
}

// JobRequestHandler handles the complete NIP-90 request instead of the legacy
// single-input callback.
type JobRequestHandler func(ctx context.Context, request JobRequest) (JobResult, error)

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
	// OnRequest receives all request inputs, params, relay preferences, and
	// encryption state. When set, it takes precedence over OnJob.
	OnRequest JobRequestHandler
	// MaxConcurrentJobs bounds in-flight job handlers. Defaults to 8.
	MaxConcurrentJobs int
}

// Handler manages NIP-90 DVM subscriptions and result publishing.
type Handler struct {
	opts      HandlerOpts
	keyer     nostr.Keyer
	pubkey    nostr.PubKey
	pool      *nostr.Pool
	publisher okpublish.Publisher
	ctx       context.Context
	jobSem    chan struct{}
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	subHealth *runtime.SubHealthTracker

	// Relay rebind support.
	relaysMu sync.RWMutex
	relays   []string
	rebindCh chan struct{}

	// Deduplication.
	seenMu   sync.Mutex
	seenSet  map[string]struct{}
	seenList []string
	seenCap  int
}

// Start creates a Handler and begins listening for job requests.
func Start(ctx context.Context, opts HandlerOpts) (*Handler, error) {
	if opts.OnJob == nil && opts.OnRequest == nil {
		return nil, fmt.Errorf("dvm: OnJob or OnRequest handler is required")
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

	relays := make([]string, len(opts.Relays))
	copy(relays, opts.Relays)

	ctx2, cancel := context.WithCancel(ctx)
	pool := runtime.NewPoolNIP42(ks)
	h := &Handler{
		opts:      opts,
		keyer:     ks,
		pubkey:    pubkey,
		pool:      pool,
		publisher: pool,
		ctx:       ctx2,
		jobSem:    make(chan struct{}, opts.MaxConcurrentJobs),
		cancel:    cancel,
		subHealth: runtime.NewSubHealthTracker("dvm"),
		relays:    relays,
		rebindCh:  make(chan struct{}, 1),
		seenSet:   make(map[string]struct{}),
		seenCap:   10_000,
	}
	h.subHealth.RecordReconnect()

	h.wg.Add(1)
	go h.subscriptionLoop()
	return h, nil
}

// Stop shuts down the handler gracefully.
func (h *Handler) Stop() {
	h.cancel()
	h.wg.Wait()
	h.pool.Close("dvm stopped")
}

// SetRelays replaces the relay list and triggers a subscription rebind.
func (h *Handler) SetRelays(relays []string) {
	next := make([]string, 0, len(relays))
	for _, r := range relays {
		if r != "" {
			next = append(next, r)
		}
	}
	h.relaysMu.Lock()
	h.relays = next
	h.relaysMu.Unlock()
	select {
	case h.rebindCh <- struct{}{}:
	default:
	}
}

// Relays returns the currently active relay list.
func (h *Handler) Relays() []string {
	h.relaysMu.RLock()
	defer h.relaysMu.RUnlock()
	out := make([]string, len(h.relays))
	copy(out, h.relays)
	return out
}

// HealthSnapshot returns a point-in-time view of the DVM subscription's health.
func (h *Handler) HealthSnapshot() runtime.SubHealthSnapshot {
	if h.subHealth == nil {
		return runtime.SubHealthSnapshot{Label: "dvm", BoundRelays: h.Relays(), ReplayWindowMS: runtime.DVMResubscribeWindow.Milliseconds()}
	}
	return h.subHealth.Snapshot(h.Relays(), runtime.DVMResubscribeWindow)
}

func (h *Handler) currentRelays() []string {
	h.relaysMu.RLock()
	defer h.relaysMu.RUnlock()
	out := make([]string, len(h.relays))
	copy(out, h.relays)
	return out
}

func (h *Handler) subscriptionLoop() {
	defer h.wg.Done()

	backoff := runtime.SubReconnectBackoffMin
	since := runtime.ResubscribeSince(runtime.DVMResubscribeWindow)
	for {
		if h.ctx.Err() != nil {
			return
		}
		restart := h.runSubscription(since)
		if h.ctx.Err() != nil {
			return
		}
		if h.subHealth != nil {
			h.subHealth.RecordReconnect()
		}
		since = runtime.ResubscribeSince(runtime.DVMResubscribeWindow)
		if restart {
			// Deliberate rebind — restart immediately, reset backoff.
			backoff = runtime.SubReconnectBackoffMin
		} else {
			// Unexpected closure — exponential backoff before retry.
			select {
			case <-h.ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = runtime.NextBackoff(backoff, runtime.SubReconnectBackoffMax)
		}
	}
}

// runSubscription uses SubscribeManyNotifyClosed for proper CLOSED-signal
// handling instead of relying on channel close.  Returns true when the caller
// should restart immediately (rebind), false when a brief backoff is appropriate.
func (h *Handler) runSubscription(since int64) bool {
	kinds := make([]nostr.Kind, len(h.opts.AcceptedKinds))
	for i, k := range h.opts.AcceptedKinds {
		kinds[i] = nostr.Kind(k)
	}

	f := nostr.Filter{
		Kinds: kinds,
		Tags:  nostr.TagMap{"p": []string{h.pubkey.Hex()}},
		Since: nostr.Timestamp(since),
	}

	relays := h.currentRelays()
	if len(relays) == 0 {
		select {
		case <-h.ctx.Done():
			return true
		case <-h.rebindCh:
			return true
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}

	events, closedCh := h.pool.SubscribeManyNotifyClosed(
		h.ctx, relays, f, nostr.SubscriptionOptions{},
	)

	for {
		select {
		case <-h.ctx.Done():
			return true
		case <-h.rebindCh:
			return true
		case rc, ok := <-closedCh:
			if !ok {
				// Avoid tight-looping on a closed channel; the events channel will
				// also close, at which point we will restart.
				closedCh = nil
				continue
			}
			if rc.HandledAuth {
				continue // auth retry handled internally by pool
			}
			relayURL := ""
			if rc.Relay != nil {
				relayURL = rc.Relay.URL
			}
			log.Printf("dvm: subscription closed by relay=%s reason=%s; restarting", relayURL, rc.Reason)
			if h.subHealth != nil {
				h.subHealth.RecordClosed(relayURL, rc.Reason)
			}
			return false
		case re, ok := <-events:
			if !ok {
				log.Printf("dvm: event channel closed; restarting")
				return false
			}
			if !re.Event.CheckID() || !re.Event.VerifySignature() {
				continue
			}
			if !re.Event.Tags.ContainsAny("p", []string{h.pubkey.Hex()}) {
				continue
			}
			if h.markSeen(re.Event.ID.Hex()) {
				continue
			}
			if h.subHealth != nil {
				h.subHealth.RecordEvent()
			}
			select {
			case h.jobSem <- struct{}{}:
			case <-h.ctx.Done():
				return true
			}
			h.wg.Add(1)
			sourceRelay := ""
			if re.Relay != nil {
				sourceRelay = re.Relay.URL
			}
			go func(ev nostr.Event, relayURL string) {
				defer h.wg.Done()
				defer func() { <-h.jobSem }()
				h.handleJobFromRelay(h.ctx, ev, relayURL)
			}(re.Event, sourceRelay)
		}
	}
}

// markSeen returns true if the ID was already seen (duplicate).
func (h *Handler) markSeen(id string) bool {
	h.seenMu.Lock()
	defer h.seenMu.Unlock()
	if _, exists := h.seenSet[id]; exists {
		return true
	}
	h.seenSet[id] = struct{}{}
	h.seenList = append(h.seenList, id)
	if len(h.seenList) > h.seenCap {
		evict := h.seenList[0]
		h.seenList = h.seenList[1:]
		delete(h.seenSet, evict)
	}
	return false
}

func (h *Handler) handleJob(ctx context.Context, ev nostr.Event) {
	h.handleJobFromRelay(ctx, ev, "")
}

func (h *Handler) handleJobFromRelay(ctx context.Context, ev nostr.Event, sourceRelay string) {
	request, err := h.parseJobRequest(ctx, ev)
	if err != nil {
		log.Printf("dvm: parse job %s: %v", ev.ID.Hex(), err)
		return
	}
	if err := h.publishStatus(ctx, request, sourceRelay, JobResult{}, "processing", ""); err != nil {
		log.Printf("dvm: publish processing status for job %s: %v", ev.ID.Hex(), err)
	}

	// Dispatch to the job handler.
	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if h.pool != nil && jobCtx.Err() == nil {
		go h.watchCancellation(jobCtx, request, mergeRelays(request.ResponseRelays, h.currentRelays()), cancel)
	}

	var result JobResult
	if h.opts.OnRequest != nil {
		result, err = h.opts.OnRequest(jobCtx, request)
	} else {
		var content string
		content, err = h.opts.OnJob(jobCtx, ev.ID.Hex(), int(ev.Kind), legacyInput(request))
		result.Content = content
	}
	if err != nil {
		log.Printf("dvm: job %s error: %v", ev.ID.Hex(), err)
		if pubErr := h.publishStatus(ctx, request, sourceRelay, JobResult{}, "error", err.Error()); pubErr != nil {
			log.Printf("dvm: publish error status for job %s: %v", ev.ID.Hex(), pubErr)
		}
		return
	}
	if result.PaymentRequired {
		if err := h.publishStatus(ctx, request, sourceRelay, result, "payment-required", ""); err != nil {
			log.Printf("dvm: publish payment-required status for job %s: %v", ev.ID.Hex(), err)
		}
		return
	}

	// Publish result (kind:6000-6999).
	if err := h.publishResult(ctx, request, sourceRelay, result); err != nil {
		log.Printf("dvm: publish result for job %s: %v", ev.ID.Hex(), err)
		return
	}
	// Publish success status.
	if err := h.publishStatus(ctx, request, sourceRelay, JobResult{}, "success", ""); err != nil {
		log.Printf("dvm: publish success status for job %s: %v", ev.ID.Hex(), err)
	}
}

func (h *Handler) signEvent(ctx context.Context, evt *nostr.Event) error {
	return h.keyer.SignEvent(ctx, evt)
}

func (h *Handler) publishResult(ctx context.Context, request JobRequest, sourceRelay string, result JobResult) error {
	evt, err := h.buildResultEvent(ctx, request, sourceRelay, result)
	if err != nil {
		return err
	}
	return h.publishToRelays(ctx, preferredRelays(request.ResponseRelays, h.currentRelays()), evt)
}

func (h *Handler) publishStatus(ctx context.Context, request JobRequest, sourceRelay string, result JobResult, status, extraMsg string) error {
	evt, err := h.buildStatusEvent(ctx, request, sourceRelay, result, status, extraMsg)
	if err != nil {
		return err
	}
	return h.publishToRelays(ctx, preferredRelays(request.ResponseRelays, h.currentRelays()), evt)
}

func (h *Handler) buildResultEvent(ctx context.Context, request JobRequest, sourceRelay string, result JobResult) (nostr.Event, error) {
	requestJSON, err := json.Marshal(request.Event)
	if err != nil {
		return nostr.Event{}, fmt.Errorf("marshal request event: %w", err)
	}
	tags := nostr.Tags{
		eventReferenceTag(request.Event.ID.Hex(), sourceRelay),
		{"p", request.Event.PubKey.Hex()},
		{"request", string(requestJSON)},
	}
	if !request.Encrypted {
		for _, tag := range request.Event.Tags {
			if len(tag) > 0 && tag[0] == "i" {
				tags = append(tags, append(nostr.Tag(nil), tag...))
			}
		}
	}
	if result.AmountMSat > 0 {
		amount := nostr.Tag{"amount", strconv.FormatInt(result.AmountMSat, 10)}
		if result.Invoice != "" {
			amount = append(amount, result.Invoice)
		}
		tags = append(tags, amount)
	}
	content := result.Content
	if request.Encrypted {
		content, err = h.encryptNIP04(ctx, result.Content, request.Event.PubKey)
		if err != nil {
			return nostr.Event{}, fmt.Errorf("encrypt result: %w", err)
		}
		tags = append(tags, nostr.Tag{"encrypted"})
	}
	evt := nostr.Event{
		Kind:      request.Event.Kind + 1000,
		Content:   content,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
		PubKey:    h.pubkey,
	}
	if err := h.signEvent(ctx, &evt); err != nil {
		return nostr.Event{}, fmt.Errorf("sign result: %w", err)
	}
	return evt, nil
}

func (h *Handler) buildStatusEvent(ctx context.Context, request JobRequest, sourceRelay string, result JobResult, status, extraMsg string) (nostr.Event, error) {
	content := status
	if extraMsg != "" {
		content = status + ": " + extraMsg
	}
	tags := nostr.Tags{
		eventReferenceTag(request.Event.ID.Hex(), sourceRelay),
		{"p", request.Event.PubKey.Hex()},
		{"status", status},
	}
	if result.AmountMSat > 0 {
		amount := nostr.Tag{"amount", strconv.FormatInt(result.AmountMSat, 10)}
		if result.Invoice != "" {
			amount = append(amount, result.Invoice)
		}
		tags = append(tags, amount)
	}
	var err error
	if request.Encrypted && content != "" {
		content, err = h.encryptNIP04(ctx, content, request.Event.PubKey)
		if err != nil {
			return nostr.Event{}, fmt.Errorf("encrypt feedback: %w", err)
		}
		tags = append(tags, nostr.Tag{"encrypted"})
	}
	evt := nostr.Event{
		Kind:      7000,
		Content:   content,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
		PubKey:    h.pubkey,
	}
	if err := h.signEvent(ctx, &evt); err != nil {
		return nostr.Event{}, fmt.Errorf("sign status: %w", err)
	}
	return evt, nil
}

func (h *Handler) publish(ctx context.Context, evt nostr.Event) error {
	return h.publishToRelays(ctx, h.currentRelays(), evt)
}

func (h *Handler) publishToRelays(ctx context.Context, relays []string, evt nostr.Event) error {
	publisher := h.publisher
	if publisher == nil {
		publisher = h.pool
	}
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := okpublish.PublishToAny(ctx2, publisher, relays, evt); err != nil {
		return fmt.Errorf("publish kind %d: %w", evt.Kind, err)
	}
	return nil
}

func (h *Handler) parseJobRequest(ctx context.Context, ev nostr.Event) (JobRequest, error) {
	request := JobRequest{Event: ev}
	privateTags := nostr.Tags(nil)
	for _, tag := range ev.Tags {
		if len(tag) == 0 {
			continue
		}
		switch tag[0] {
		case "encrypted":
			request.Encrypted = true
		case "relays":
			request.ResponseRelays = mergeRelays(tag[1:], request.ResponseRelays)
		case "output":
			if len(tag) > 1 {
				request.Output = tag[1]
			}
		case "bid":
			if len(tag) > 1 {
				request.BidMSat, _ = strconv.ParseInt(tag[1], 10, 64)
			}
		}
	}
	if request.Encrypted {
		plaintext, err := h.decryptNIP04(ctx, ev.Content, ev.PubKey)
		if err != nil {
			return JobRequest{}, fmt.Errorf("decrypt request: %w", err)
		}
		if err := json.Unmarshal([]byte(plaintext), &privateTags); err != nil {
			return JobRequest{}, fmt.Errorf("decode encrypted request tags: %w", err)
		}
	} else {
		privateTags = ev.Tags
	}
	for _, tag := range privateTags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "i":
			input := JobInput{Data: tag[1]}
			if len(tag) > 2 {
				input.Type = tag[2]
			}
			if len(tag) > 3 {
				input.Relay = tag[3]
			}
			if len(tag) > 4 {
				input.Marker = tag[4]
			}
			request.Inputs = append(request.Inputs, input)
		case "param":
			param := JobParam{Name: tag[1]}
			if len(tag) > 2 {
				param.Value = tag[2]
			}
			request.Params = append(request.Params, param)
		}
	}
	return request, nil
}

func (h *Handler) decryptNIP04(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	decrypter, ok := h.keyer.(runtime.NIP04Decrypter)
	if !ok {
		return "", fmt.Errorf("keyer does not support NIP-04 decryption")
	}
	return decrypter.DecryptNIP04(ctx, ciphertext, sender)
}

func (h *Handler) encryptNIP04(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	encrypter, ok := h.keyer.(runtime.NIP04Encrypter)
	if !ok {
		return "", fmt.Errorf("keyer does not support NIP-04 encryption")
	}
	return encrypter.EncryptNIP04(ctx, plaintext, recipient)
}

func (h *Handler) watchCancellation(ctx context.Context, request JobRequest, relays []string, cancel context.CancelFunc) {
	filter := nostr.Filter{
		Kinds:   []nostr.Kind{5},
		Authors: []nostr.PubKey{request.Event.PubKey},
		Tags:    nostr.TagMap{"e": []string{request.Event.ID.Hex()}},
		Since:   request.Event.CreatedAt,
	}
	events, closed := h.pool.SubscribeManyNotifyClosed(ctx, relays, filter, nostr.SubscriptionOptions{})
	for {
		select {
		case <-ctx.Done():
			return
		case rc, ok := <-closed:
			if !ok {
				closed = nil
				continue
			}
			if rc.HandledAuth {
				continue
			}
			return
		case re, ok := <-events:
			if !ok {
				return
			}
			if isCancellationFor(re.Event, request.Event) {
				cancel()
				return
			}
		}
	}
}

func isCancellationFor(deletion, request nostr.Event) bool {
	return deletion.Kind == 5 &&
		deletion.PubKey == request.PubKey &&
		deletion.CheckID() &&
		deletion.VerifySignature() &&
		deletion.Tags.ContainsAny("e", []string{request.ID.Hex()})
}

func eventReferenceTag(eventID, relay string) nostr.Tag {
	tag := nostr.Tag{"e", eventID}
	if relay != "" {
		tag = append(tag, relay)
	}
	return tag
}

func mergeRelays(primary, fallback []string) []string {
	out := make([]string, 0, len(primary)+len(fallback))
	seen := make(map[string]struct{}, len(primary)+len(fallback))
	for _, relay := range append(append([]string(nil), primary...), fallback...) {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			continue
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		out = append(out, relay)
	}
	return out
}

func preferredRelays(requested, fallback []string) []string {
	if relays := mergeRelays(requested, nil); len(relays) > 0 {
		return relays
	}
	return mergeRelays(fallback, nil)
}

func legacyInput(request JobRequest) string {
	if len(request.Inputs) == 0 {
		if request.Encrypted {
			return ""
		}
		return request.Event.Content
	}
	if len(request.Inputs) == 1 {
		return request.Inputs[0].Data
	}
	encoded, _ := json.Marshal(request.Inputs)
	return string(encoded)
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

// FormatResult is a convenience for agent tools that want to publish a DVM result directly.
func FormatResult(jobID, requesterPubkey, outputType, content string) string {
	m := map[string]any{
		"job_id":         jobID,
		"requester":      requesterPubkey,
		"output_type":    outputType,
		"result_content": content,
	}
	b, _ := json.Marshal(m)
	return string(b)
}
