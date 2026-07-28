package tasks

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nostr "fiatjaf.com/nostr"
)

// TaskStatePublishFunc publishes one signed task or collection event.
type TaskStatePublishFunc func(context.Context, []string, nostr.Event) error

// TaskStateSubscribeFunc opens a stored+live EOSE-aware subscription.
type TaskStateSubscribeFunc func(context.Context, []string, nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{})

// FleetTaskBridgeOptions configures peer-to-peer task-state synchronization.
type FleetTaskBridgeOptions struct {
	Keyer                    nostr.Keyer
	Pool                     *nostr.Pool
	Ledger                   *Ledger
	Emitter                  *EventEmitter
	ReadRelays               []string
	WriteRelays              []string
	TrustedTaskAuthors       []string
	TrustedCollectionAuthors []string
	CollectionSources        []TaskCollectionSource
	MaxFutureSkew            time.Duration
	ClaimSettlement          time.Duration
	PublishTimeout           time.Duration
	PublishFunc              TaskStatePublishFunc
	SubscribeFunc            TaskStateSubscribeFunc
	Logf                     func(string, ...any)
	Now                      func() time.Time
}

// TaskCollectionSource identifies one exact trusted NIP-51 queue or epic.
type TaskCollectionSource struct {
	Author string
	Type   string
	ID     string
}

// FleetTaskBridge connects the existing ledger/event emitter to NIP-CAS-0006.
// It owns no daemon protocol and has no ContextVM dependency.
type FleetTaskBridge struct {
	keyer             nostr.Keyer
	pool              *nostr.Pool
	ledger            *Ledger
	emitter           *EventEmitter
	readRelays        []string
	writeRelays       []string
	publishTimeout    time.Duration
	claimSettlement   time.Duration
	collectionSources []TaskCollectionSource
	publish           TaskStatePublishFunc
	subscribe         TaskStateSubscribeFunc
	logf              func(string, ...any)
	now               func() time.Time
	merger            *TaskMerger

	ctx        context.Context
	cancel     context.CancelFunc
	taskCh     chan string
	ready      chan struct{}
	once       sync.Once
	wg         sync.WaitGroup
	correcting atomic.Bool
}

const defaultTaskStatePublishTimeout = 30 * time.Second
const defaultClaimSettlement = 10 * time.Second

// NewFleetTaskBridge starts publication and stored+live subscription workers.
func NewFleetTaskBridge(parent context.Context, opts FleetTaskBridgeOptions) (*FleetTaskBridge, error) {
	if opts.Keyer == nil {
		return nil, fmt.Errorf("fleet task bridge: keyer is required")
	}
	if opts.Ledger == nil {
		return nil, fmt.Errorf("fleet task bridge: ledger is required")
	}
	if opts.Pool == nil && (opts.PublishFunc == nil || opts.SubscribeFunc == nil) {
		return nil, fmt.Errorf("fleet task bridge: pool or publish+subscribe funcs are required")
	}
	readRelays := normalizeLifecycleRelays(opts.ReadRelays)
	writeRelays := normalizeLifecycleRelays(opts.WriteRelays)
	if len(readRelays) == 0 || len(writeRelays) == 0 {
		return nil, fmt.Errorf("fleet task bridge: read and write relays are required")
	}
	if len(normalizedPubkeySet(opts.TrustedTaskAuthors)) == 0 {
		return nil, fmt.Errorf("fleet task bridge: trusted task authors are required")
	}
	if len(normalizedPubkeySet(opts.TrustedCollectionAuthors)) == 0 {
		return nil, fmt.Errorf("fleet task bridge: trusted collection authors are required")
	}
	if parent == nil {
		parent = context.Background()
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.PublishTimeout
	if timeout <= 0 {
		timeout = defaultTaskStatePublishTimeout
	}
	settlement := opts.ClaimSettlement
	if settlement <= 0 {
		settlement = defaultClaimSettlement
	}
	collectionTrust := normalizedPubkeySet(opts.TrustedCollectionAuthors)
	sources := make([]TaskCollectionSource, 0, len(opts.CollectionSources))
	seenSources := map[string]struct{}{}
	for _, source := range opts.CollectionSources {
		author := strings.ToLower(strings.TrimSpace(source.Author))
		collectionType := strings.ToLower(strings.TrimSpace(source.Type))
		id := strings.TrimSpace(source.ID)
		if _, ok := collectionTrust[author]; !ok {
			return nil, fmt.Errorf("fleet task bridge: collection source author %s is not trusted", author)
		}
		if (collectionType != "queue" && collectionType != "epic") || id == "" {
			return nil, fmt.Errorf("fleet task bridge: collection source must name queue:<id> or epic:<id>")
		}
		key := author + "|" + collectionType + ":" + id
		if _, ok := seenSources[key]; ok {
			continue
		}
		seenSources[key] = struct{}{}
		sources = append(sources, TaskCollectionSource{Author: author, Type: collectionType, ID: id})
	}
	ctx, cancel := context.WithCancel(parent)
	policy := TaskValidationPolicy{
		TrustedTaskAuthors:       append([]string(nil), opts.TrustedTaskAuthors...),
		TrustedCollectionAuthors: append([]string(nil), opts.TrustedCollectionAuthors...),
		MaxFutureSkew:            opts.MaxFutureSkew,
		Now:                      now,
	}
	bridge := &FleetTaskBridge{
		keyer: opts.Keyer, pool: opts.Pool, ledger: opts.Ledger, emitter: opts.Emitter,
		readRelays: readRelays, writeRelays: writeRelays,
		publishTimeout: timeout, claimSettlement: settlement, collectionSources: sources,
		publish: opts.PublishFunc, subscribe: opts.SubscribeFunc,
		logf: logf, now: now, merger: NewTaskMerger(policy),
		ctx: ctx, cancel: cancel, taskCh: make(chan string, 128), ready: make(chan struct{}),
	}
	if bridge.publish == nil {
		bridge.publish = bridge.publishWithPool
	}
	if bridge.subscribe == nil {
		bridge.subscribe = bridge.subscribeWithPool
	}
	if opts.Emitter != nil {
		opts.Emitter.AddHandler(bridge.HandleLifecycleEvent)
	}
	bridge.wg.Add(2)
	go bridge.publishLoop()
	go bridge.subscribeLoop()
	return bridge, nil
}

// Ready closes after the relay subscription reports EOSE for stored events.
func (b *FleetTaskBridge) Ready() <-chan struct{} {
	if b == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return b.ready
}

// Stop terminates bridge workers without closing the shared pool.
func (b *FleetTaskBridge) Stop() {
	if b == nil {
		return
	}
	b.cancel()
	b.wg.Wait()
}

// Merger exposes read-only effective task/collection access through its safe APIs.
func (b *FleetTaskBridge) Merger() *TaskMerger {
	if b == nil {
		return nil
	}
	return b.merger
}

// HandleLifecycleEvent schedules a complete snapshot for local task mutations.
func (b *FleetTaskBridge) HandleLifecycleEvent(ctx context.Context, event Event) {
	if b == nil || strings.TrimSpace(event.TaskID) == "" || !isTaskLifecycleEvent(event.Type) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-b.ctx.Done():
	case <-ctx.Done():
	case b.taskCh <- strings.TrimSpace(event.TaskID):
	}
}

func isTaskLifecycleEvent(eventType EventType) bool {
	switch eventType {
	case EventTaskCreated, EventTaskUpdated, EventTaskCompleted, EventTaskFailed, EventTaskCancelled:
		return true
	default:
		return false
	}
}

func (b *FleetTaskBridge) publishLoop() {
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		case taskID := <-b.taskCh:
			select {
			case <-b.ctx.Done():
				return
			case <-b.ready:
			}
			if _, err := b.PublishLedgerTask(b.ctx, taskID); err != nil {
				b.logf("fleet tasks: publish task=%s: %v", taskID, err)
			}
		}
	}
}

// PublishLedgerTask signs and publishes the current complete ledger snapshot.
func (b *FleetTaskBridge) PublishLedgerTask(ctx context.Context, taskID string) (string, error) {
	entry, err := b.ledger.SnapshotTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return "", err
	}
	doc, err := TaskDocumentFromLedger(entry)
	if err != nil {
		return "", err
	}
	createdAt := b.now().UTC().Truncate(time.Second)
	if doc.Status == "in_progress" && doc.ClaimedAt == "" {
		if doc.Assignee == "" {
			return "", fmt.Errorf("in_progress task %s requires an assignee claim", doc.ID)
		}
		doc.ClaimedAt = createdAt.Format(time.RFC3339)
	}
	return b.PublishTaskDocument(ctx, doc, createdAt)
}

// PublishTaskDocument publishes one full state and immediately feeds it through
// the same validator/merge path used by relay delivery.
func (b *FleetTaskBridge) PublishTaskDocument(ctx context.Context, doc TaskDocument, createdAt time.Time) (string, error) {
	event, err := BuildTaskStateEvent(doc, createdAt)
	if err != nil {
		return "", err
	}
	if err := b.keyer.SignEvent(ctx, &event); err != nil {
		return "", fmt.Errorf("sign task event: %w", err)
	}
	if _, err := ValidateTaskStateEvent(event, b.merger.policy); err != nil {
		return "", fmt.Errorf("validate signed task event: %w", err)
	}
	if err := b.publishSigned(ctx, event); err != nil {
		return event.ID.Hex(), err
	}
	if err := b.ingestTask(event); err != nil {
		return event.ID.Hex(), fmt.Errorf("ingest published task event: %w", err)
	}
	return event.ID.Hex(), nil
}

// PublishCollection publishes a complete queue or epic list from effective heads.
func (b *FleetTaskBridge) PublishCollection(ctx context.Context, collectionType, id string, taskIDs []string) (string, error) {
	members := make([]TaskEventHead, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		head, ok := b.merger.EffectiveTask(taskID)
		if !ok {
			return "", fmt.Errorf("task %s has no effective trusted head", taskID)
		}
		members = append(members, head)
	}
	event, err := BuildTaskCollectionEvent(collectionType, id, members, b.now())
	if err != nil {
		return "", err
	}
	if err := b.keyer.SignEvent(ctx, &event); err != nil {
		return "", fmt.Errorf("sign collection event: %w", err)
	}
	if _, err := validateTaskCollection(event, b.merger.policy); err != nil {
		return "", fmt.Errorf("validate signed collection event: %w", err)
	}
	if err := b.publishSigned(ctx, event); err != nil {
		return event.ID.Hex(), err
	}
	if _, _, err := b.merger.IngestCollection(event); err != nil {
		return event.ID.Hex(), err
	}
	return event.ID.Hex(), nil
}

func (b *FleetTaskBridge) publishSigned(parent context.Context, event nostr.Event) error {
	ctx, cancel := context.WithTimeout(parent, b.publishTimeout)
	defer cancel()
	return b.publish(ctx, append([]string(nil), b.writeRelays...), event)
}

func (b *FleetTaskBridge) publishWithPool(ctx context.Context, relays []string, event nostr.Event) error {
	accepted := false
	var lastErr error
	for result := range b.pool.PublishMany(ctx, relays, event) {
		if result.Error == nil {
			accepted = true
		} else {
			lastErr = result.Error
			b.logf("fleet tasks: relay %s rejected event %s: %v", result.RelayURL, event.ID.Hex(), result.Error)
		}
	}
	if accepted {
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("event %s not accepted: %w", event.ID.Hex(), lastErr)
	}
	return fmt.Errorf("event %s not accepted by any relay", event.ID.Hex())
}

func (b *FleetTaskBridge) subscribeWithPool(ctx context.Context, relays []string, filter nostr.Filter) (<-chan nostr.RelayEvent, <-chan struct{}) {
	return b.pool.SubscribeManyNotifyEOSE(ctx, relays, filter, nostr.SubscriptionOptions{})
}

func (b *FleetTaskBridge) subscriptionFilters() []nostr.Filter {
	taskAuthors := normalizedPubkeySet(b.merger.policy.TrustedTaskAuthors)
	taskPubkeys := make([]nostr.PubKey, 0, len(taskAuthors))
	for value := range taskAuthors {
		pubkey, err := nostr.PubKeyFromHex(value)
		if err != nil {
			b.logf("fleet tasks: invalid configured task pubkey %q: %v", value, err)
			continue
		}
		taskPubkeys = append(taskPubkeys, pubkey)
	}
	sort.Slice(taskPubkeys, func(i, j int) bool { return taskPubkeys[i].Hex() < taskPubkeys[j].Hex() })
	filters := []nostr.Filter{{
		Kinds:   []nostr.Kind{nostr.Kind(30900)},
		Authors: taskPubkeys,
		Tags:    nostr.TagMap{"schema": {TaskStateSchemaV2}},
	}}
	for _, source := range b.collectionSources {
		pubkey, err := nostr.PubKeyFromHex(source.Author)
		if err != nil {
			b.logf("fleet tasks: invalid collection source pubkey %q: %v", source.Author, err)
			continue
		}
		filters = append(filters, nostr.Filter{
			Kinds:   []nostr.Kind{nostr.Kind(TaskCollectionKind)},
			Authors: []nostr.PubKey{pubkey},
			Tags:    nostr.TagMap{"d": {source.Type + ":" + source.ID}},
		})
	}
	return filters
}

func (b *FleetTaskBridge) subscribeLoop() {
	defer b.wg.Done()
	for {
		if b.ctx.Err() != nil {
			return
		}
		subCtx, cancel := context.WithCancel(b.ctx)
		filters := b.subscriptionFilters()
		eventsCh := make(chan nostr.RelayEvent)
		eoseCh := make(chan struct{}, len(filters))
		closedCh := make(chan struct{}, len(filters))
		for _, filter := range filters {
			sourceEvents, sourceEOSE := b.subscribe(subCtx, b.readRelays, filter)
			go func() {
				for {
					select {
					case <-subCtx.Done():
						return
					case relayEvent, ok := <-sourceEvents:
						if !ok {
							select {
							case closedCh <- struct{}{}:
							case <-subCtx.Done():
							}
							return
						}
						select {
						case eventsCh <- relayEvent:
						case <-subCtx.Done():
							return
						}
					}
				}
			}()
			go func() {
				select {
				case <-subCtx.Done():
				case <-sourceEOSE:
					select {
					case eoseCh <- struct{}{}:
					case <-subCtx.Done():
					}
				}
			}()
		}
		eoseRemaining := len(filters)
		for {
			select {
			case <-b.ctx.Done():
				cancel()
				return
			case <-eoseCh:
				if eoseRemaining > 0 {
					eoseRemaining--
					if eoseRemaining == 0 {
						b.once.Do(func() { close(b.ready) })
						b.logf("fleet tasks: EOSE — live task-state subscriptions active")
					}
				}
			case <-closedCh:
				cancel()
				timer := time.NewTimer(500 * time.Millisecond)
				select {
				case <-b.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				goto resubscribe
			case relayEvent := <-eventsCh:
				if relayEvent.Event.Kind == nostr.Kind(30900) {
					if err := b.ingestTask(relayEvent.Event); err != nil {
						b.logf("fleet tasks: ignored task event %s: %v", relayEvent.Event.ID.Hex(), err)
					}
				} else if relayEvent.Event.Kind == nostr.Kind(TaskCollectionKind) {
					if _, _, err := b.merger.IngestCollection(relayEvent.Event); err != nil {
						b.logf("fleet tasks: ignored collection event %s: %v", relayEvent.Event.ID.Hex(), err)
					}
				}
			}
		}
	resubscribe:
	}
}

func (b *FleetTaskBridge) ingestTask(event nostr.Event) error {
	head, changed, err := b.merger.IngestTask(event)
	if err != nil {
		return err
	}
	if !changed {
		b.correctLostLocalClaim(head)
		return nil
	}
	doc := head.Task
	if head.InitialClaim && head.Claim != nil {
		if doc.Metadata == nil {
			doc.Metadata = map[string]string{}
		}
		doc.Metadata[ClaimOriginIDMetaKey] = head.Claim.EventID
		doc.Metadata[ClaimOriginPubkeyMetaKey] = head.Claim.Pubkey
	}
	task, err := TaskSpecFromDocument(b.ctx, b.ledger, doc)
	if err != nil {
		return err
	}
	_, err = b.ledger.SaveTaskState(b.ctx, task, TaskSourceFleet, head.Event.ID.Hex())
	if err == nil {
		b.correctLostLocalClaim(head)
	}
	return err
}

func (b *FleetTaskBridge) correctLostLocalClaim(effective TaskEventHead) {
	if effective.Claim == nil || b.keyer == nil {
		return
	}
	// Reentrancy guard: publishing a correction re-enters the ingest path,
	// which calls back into this function. Without the guard a correction
	// that loses eventWins against a newer local head recurses unboundedly.
	if !b.correcting.CompareAndSwap(false, true) {
		return
	}
	defer b.correcting.Store(false)
	pubkey, err := b.keyer.GetPublicKey(b.ctx)
	if err != nil {
		b.logf("fleet tasks: resolve local pubkey for claim correction: %v", err)
		return
	}
	local, ok := b.merger.AuthorHead(effective.Task.ID, pubkey.Hex())
	if !ok || local.Claim == nil || sameClaim(*local.Claim, *effective.Claim) {
		return
	}
	// The correction must strictly supersede the retained local head or it is
	// discarded by the per-author eventWins replacement (a future-dated local
	// head would otherwise defeat every correction).
	correctionAt := b.now().UTC().Truncate(time.Second)
	if floor := time.Unix(int64(local.Event.CreatedAt), 0).UTC(); !correctionAt.After(floor) {
		correctionAt = floor.Add(time.Second)
	}
	skew := b.merger.policy.MaxFutureSkew
	if skew <= 0 {
		skew = 5 * time.Minute
	}
	if correctionAt.After(b.now().Add(skew)) {
		b.logf("fleet tasks: defer lost-claim correction task=%s: local head too far in the future", effective.Task.ID)
		return
	}
	correction := effective.Task
	if correction.Metadata == nil {
		correction.Metadata = map[string]string{}
	}
	correction.Assignee = effective.Claim.Assignee
	correction.ClaimedAt = time.Unix(effective.Claim.CreatedAt, 0).UTC().Format(time.RFC3339)
	correction.Metadata[ClaimOriginIDMetaKey] = effective.Claim.EventID
	correction.Metadata[ClaimOriginPubkeyMetaKey] = effective.Claim.Pubkey
	correction.UpdatedAt = correctionAt.Format(time.RFC3339Nano)
	if _, err := b.PublishTaskDocument(b.ctx, correction, correctionAt); err != nil {
		b.logf("fleet tasks: correct lost claim task=%s winner=%s: %v", effective.Task.ID, effective.Claim.EventID, err)
	}
}
