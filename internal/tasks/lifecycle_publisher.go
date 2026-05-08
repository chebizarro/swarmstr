package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/events"
)

// LifecyclePublishFunc publishes one signed lifecycle event to relays. Tests can
// inject this to avoid network I/O; production normally uses a nostr.Pool.
type LifecyclePublishFunc func(ctx context.Context, relays []string, event nostr.Event) error

// LifecyclePublisherOptions configures the async kind:30316 lifecycle publisher.
type LifecyclePublisherOptions struct {
	Keyer          nostr.Keyer
	Pool           *nostr.Pool
	PublishFunc    LifecyclePublishFunc
	Relays         []string
	RelayProvider  func() []string
	Buffer         int
	PublishTimeout time.Duration
	Logf           func(format string, args ...any)
	Now            func() time.Time
}

// LifecyclePublisher bridges in-process task lifecycle events to best-effort
// Nostr kind:30316 publications without feeding relay failures back into ledger
// mutations.
type LifecyclePublisher struct {
	keyer          nostr.Keyer
	pool           *nostr.Pool
	publish        LifecyclePublishFunc
	relays         []string
	relayProvider  func() []string
	publishTimeout time.Duration
	logf           func(format string, args ...any)
	now            func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	ch     chan Event
	wg     sync.WaitGroup
}

const defaultLifecyclePublisherBuffer = 128
const defaultLifecyclePublishTimeout = 30 * time.Second

// NewLifecyclePublisher starts an async lifecycle publisher worker.
func NewLifecyclePublisher(parent context.Context, opts LifecyclePublisherOptions) (*LifecyclePublisher, error) {
	if opts.Keyer == nil {
		return nil, fmt.Errorf("lifecycle publisher: keyer is required")
	}
	if opts.Pool == nil && opts.PublishFunc == nil {
		return nil, fmt.Errorf("lifecycle publisher: pool or publish func is required")
	}
	buffer := opts.Buffer
	if buffer <= 0 {
		buffer = defaultLifecyclePublisherBuffer
	}
	publishTimeout := opts.PublishTimeout
	if publishTimeout <= 0 {
		publishTimeout = defaultLifecyclePublishTimeout
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p := &LifecyclePublisher{
		keyer:          opts.Keyer,
		pool:           opts.Pool,
		publish:        opts.PublishFunc,
		relays:         normalizeLifecycleRelays(opts.Relays),
		relayProvider:  opts.RelayProvider,
		publishTimeout: publishTimeout,
		logf:           logf,
		now:            now,
		ctx:            ctx,
		cancel:         cancel,
		ch:             make(chan Event, buffer),
	}
	p.wg.Add(1)
	go p.loop()
	return p, nil
}

// Subscribe registers the publisher with an EventEmitter.
func (p *LifecyclePublisher) Subscribe(emitter *EventEmitter) {
	if p == nil || emitter == nil {
		return
	}
	emitter.AddHandler(p.HandleEvent)
}

// HandleEvent is an EventEmitter-compatible handler. It never waits for relay
// publication; events are queued or dropped if the publisher is backpressured.
func (p *LifecyclePublisher) HandleEvent(ctx context.Context, event Event) {
	p.Enqueue(ctx, event)
}

// Enqueue attempts to queue a lifecycle event without blocking mutation callers.
func (p *LifecyclePublisher) Enqueue(ctx context.Context, event Event) bool {
	if p == nil {
		return false
	}
	if strings.TrimSpace(event.TaskID) == "" {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.ctx.Done():
		return false
	case <-ctx.Done():
		return false
	case p.ch <- event:
		return true
	default:
		p.logf("task lifecycle publisher: dropping event type=%s task=%s run=%s: queue full", event.Type, event.TaskID, event.RunID)
		return false
	}
}

// Stop cancels the publisher and waits for its worker to exit.
func (p *LifecyclePublisher) Stop() {
	if p == nil {
		return
	}
	p.cancel()
	p.wg.Wait()
}

func (p *LifecyclePublisher) loop() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case event := <-p.ch:
			p.publishEvent(event)
		}
	}
}

func (p *LifecyclePublisher) publishEvent(event Event) {
	evt, err := BuildLifecycleNostrEvent(event, p.now())
	if err != nil {
		p.logf("task lifecycle publisher: project event type=%s task=%s run=%s: %v", event.Type, event.TaskID, event.RunID, err)
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, p.publishTimeout)
	defer cancel()
	if err := p.keyer.SignEvent(ctx, &evt); err != nil {
		p.logf("task lifecycle publisher: sign task=%s run=%s: %v", event.TaskID, event.RunID, err)
		return
	}
	relays := p.currentRelays()
	if len(relays) == 0 {
		p.logf("task lifecycle publisher: no relays configured for task=%s run=%s", event.TaskID, event.RunID)
		return
	}
	publish := p.publish
	if publish == nil {
		publish = p.publishWithPool
	}
	if err := publish(ctx, relays, evt); err != nil {
		p.logf("task lifecycle publisher: publish task=%s run=%s: %v", event.TaskID, event.RunID, err)
	}
}

func (p *LifecyclePublisher) publishWithPool(ctx context.Context, relays []string, event nostr.Event) error {
	if p == nil || p.pool == nil {
		return fmt.Errorf("nostr pool is required")
	}
	published := false
	var lastErr error
	for result := range p.pool.PublishMany(ctx, relays, event) {
		if result.Error == nil {
			published = true
			continue
		}
		lastErr = result.Error
		p.logf("task lifecycle publisher: publish to %s: %v", result.RelayURL, result.Error)
	}
	if published {
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no relays accepted lifecycle event")
}

func (p *LifecyclePublisher) currentRelays() []string {
	if p == nil {
		return nil
	}
	if p.relayProvider != nil {
		if relays := normalizeLifecycleRelays(p.relayProvider()); len(relays) > 0 {
			return relays
		}
	}
	return append([]string{}, p.relays...)
}

// BuildLifecycleNostrEvent projects an in-process lifecycle event into the
// documented kind:30316 replaceable Nostr event shape. The event is unsigned.
func BuildLifecycleNostrEvent(event Event, createdAt time.Time) (nostr.Event, error) {
	taskID := strings.TrimSpace(event.TaskID)
	if taskID == "" {
		return nostr.Event{}, fmt.Errorf("task_id is required")
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if event.Timestamp == 0 {
		event.Timestamp = createdAt.Unix()
	}
	status := strings.TrimSpace(event.Status)
	fromStatus := metaString(event.Meta, "from")
	toStatus := metaString(event.Meta, "to")
	if status == "" {
		status = toStatus
	}
	payload := lifecyclePayload{
		EventType:  string(event.Type),
		TaskID:     taskID,
		RunID:      strings.TrimSpace(event.RunID),
		FromStatus: fromStatus,
		ToStatus:   toStatus,
		Status:     status,
		Actor:      strings.TrimSpace(event.Actor),
		Source:     strings.TrimSpace(string(event.Source)),
		Reason:     strings.TrimSpace(event.Reason),
		Timestamp:  event.Timestamp,
		Meta:       cloneLifecycleMeta(event.Meta),
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return nostr.Event{}, fmt.Errorf("marshal lifecycle payload: %w", err)
	}
	tags := nostr.Tags{
		{"d", LifecycleDTag(taskID, payload.RunID)},
		{"t", taskID},
		{"task_id", taskID},
		{"lifecycle", string(event.Type)},
	}
	if payload.RunID != "" {
		tags = append(tags, nostr.Tag{"run", payload.RunID})
	}
	if status != "" {
		tags = append(tags, nostr.Tag{"stage", status}, nostr.Tag{"status", status})
	}
	appendMetaTag := func(tagKey, metaKey string) {
		if value := metaString(event.Meta, metaKey); value != "" {
			tags = append(tags, nostr.Tag{tagKey, value})
		}
	}
	appendMetaTag("goal", "goal_id")
	appendMetaTag("session", "session_id")
	appendMetaTag("agent", "agent_id")
	if event.Source != "" {
		tags = append(tags, nostr.Tag{"source", string(event.Source)})
	}
	return nostr.Event{
		Kind:      nostr.Kind(events.KindLifecycle),
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Tags:      tags,
		Content:   string(content),
	}, nil
}

// LifecycleDTag returns the parameterized-replaceable address for a lifecycle
// event. Run events are latest-per-run; task-only events are latest-per-task.
func LifecycleDTag(taskID, runID string) string {
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return taskID
	}
	return taskID + ":" + runID
}

type lifecyclePayload struct {
	EventType  string         `json:"event_type"`
	TaskID     string         `json:"task_id"`
	RunID      string         `json:"run_id,omitempty"`
	FromStatus string         `json:"from_status,omitempty"`
	ToStatus   string         `json:"to_status,omitempty"`
	Status     string         `json:"status,omitempty"`
	Actor      string         `json:"actor,omitempty"`
	Source     string         `json:"source,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Timestamp  int64          `json:"timestamp"`
	Meta       map[string]any `json:"meta,omitempty"`
}

func normalizeLifecycleRelays(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func metaString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func cloneLifecycleMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}
