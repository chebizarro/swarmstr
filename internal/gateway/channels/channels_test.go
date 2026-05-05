package channels

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip29"
)

// ─── Registry ─────────────────────────────────────────────────────────────────

type stubChannel struct {
	id      string
	closed  bool
	sendErr error
	sent    []string
}

func (s *stubChannel) ID() string   { return s.id }
func (s *stubChannel) Type() string { return "stub" }
func (s *stubChannel) Close()       { s.closed = true }
func (s *stubChannel) Send(_ context.Context, text string) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, text)
	return nil
}

func TestRegistry_addAndGet(t *testing.T) {
	r := NewRegistry()
	ch := &stubChannel{id: "ch1"}
	if err := r.Add(ch); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := r.Get("ch1")
	if !ok || got != ch {
		t.Error("expected to get the added channel")
	}
}

func TestRegistry_addDuplicateErrors(t *testing.T) {
	r := NewRegistry()
	r.Add(&stubChannel{id: "ch1"})
	err := r.Add(&stubChannel{id: "ch1"})
	if err == nil {
		t.Error("expected error for duplicate channel ID")
	}
}

func TestRegistry_remove(t *testing.T) {
	r := NewRegistry()
	ch := &stubChannel{id: "ch1"}
	r.Add(ch)
	if err := r.Remove("ch1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !ch.closed {
		t.Error("Remove should close the channel")
	}
	if _, ok := r.Get("ch1"); ok {
		t.Error("channel should be gone after remove")
	}
}

func TestRegistry_removeMissing(t *testing.T) {
	r := NewRegistry()
	if err := r.Remove("ghost"); err == nil {
		t.Error("expected error for missing channel")
	}
}

func TestRegistry_list(t *testing.T) {
	r := NewRegistry()
	r.Add(&stubChannel{id: "a"})
	r.Add(&stubChannel{id: "b"})
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	if list[0].ID != "a" || list[1].ID != "b" {
		t.Errorf("unexpected order: %v", list)
	}
}

type callbackCloseChannel struct {
	id      string
	onClose func()
}

func (c callbackCloseChannel) ID() string                         { return c.id }
func (c callbackCloseChannel) Type() string                       { return "callback" }
func (c callbackCloseChannel) Send(context.Context, string) error { return nil }
func (c callbackCloseChannel) Close() {
	if c.onClose != nil {
		c.onClose()
	}
}

func TestRegistry_removeClosesOutsideLock(t *testing.T) {
	r := NewRegistry()
	closed := make(chan struct{}, 1)
	if err := r.Add(callbackCloseChannel{id: "ch1", onClose: func() {
		_ = r.List()
		closed <- struct{}{}
	}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Remove("ch1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("close callback did not run")
	}
}

func TestRegistry_rejectsNilAndEmptyChannels(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(nil); err == nil {
		t.Fatal("expected nil channel error")
	}
	if err := r.Add(&stubChannel{}); err == nil {
		t.Fatal("expected empty channel ID error")
	}
}

func TestRegistry_closeAll(t *testing.T) {
	r := NewRegistry()
	ch1 := &stubChannel{id: "a"}
	ch2 := &stubChannel{id: "b"}
	r.Add(ch1)
	r.Add(ch2)
	r.CloseAll()
	if !ch1.closed || !ch2.closed {
		t.Error("CloseAll should close every channel")
	}
	if len(r.List()) != 0 {
		t.Error("CloseAll should clear the registry")
	}
}

func testKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("parse test key: %v", err)
	}
	return keyer.NewPlainKeySigner([32]byte(sk))
}

// ─── signer validation ────────────────────────────────────────────────────────

func TestTestKeyer_valid(t *testing.T) { _ = testKeyer(t) }

// ─── NIP29GroupChannelOptions validation ─────────────────────────────────────

func TestNewNIP29GroupChannel_missingKey(t *testing.T) {
	_, err := NewNIP29GroupChannel(context.Background(), NIP29GroupChannelOptions{
		GroupAddress: "relay.example.com'testgroup",
	})
	if err == nil {
		t.Error("expected error for missing keyer")
	}
}

func TestNewNIP29GroupChannel_missingAddress(t *testing.T) {
	_, err := NewNIP29GroupChannel(context.Background(), NIP29GroupChannelOptions{
		Keyer: testKeyer(t),
	})
	if err == nil {
		t.Error("expected error for missing group_address")
	}
}

func TestNewNIP29GroupChannel_badAddress(t *testing.T) {
	_, err := NewNIP29GroupChannel(context.Background(), NIP29GroupChannelOptions{
		Keyer:        testKeyer(t),
		GroupAddress: "noSlashInAddress",
	})
	if err == nil {
		t.Error("expected parse error for malformed group_address")
	}
}

// ─── channel reconnect handling ───────────────────────────────────────────────

type scriptedSubscription struct {
	events chan nostr.RelayEvent
	closed chan nostr.RelayClosed
}

type subscriptionCall struct {
	relays []string
	filter nostr.Filter
}

type scriptedSubscriber struct {
	calls   chan subscriptionCall
	streams chan scriptedSubscription
}

func newScriptedSubscriber() *scriptedSubscriber {
	return &scriptedSubscriber{
		calls:   make(chan subscriptionCall, 8),
		streams: make(chan scriptedSubscription, 8),
	}
}

func newScriptedSubscription() scriptedSubscription {
	return scriptedSubscription{
		events: make(chan nostr.RelayEvent, 8),
		closed: make(chan nostr.RelayClosed, 8),
	}
}

func (s *scriptedSubscriber) subscribe(ctx context.Context, _ *nostr.Pool, relays []string, filter nostr.Filter, _ nostr.SubscriptionOptions) (<-chan nostr.RelayEvent, <-chan nostr.RelayClosed) {
	call := subscriptionCall{
		relays: append([]string(nil), relays...),
		filter: filter,
	}
	select {
	case s.calls <- call:
	case <-ctx.Done():
		return closedEventStream(), closedRelayClosedStream()
	}

	select {
	case stream := <-s.streams:
		return stream.events, stream.closed
	case <-ctx.Done():
		return closedEventStream(), closedRelayClosedStream()
	}
}

func closedEventStream() <-chan nostr.RelayEvent {
	ch := make(chan nostr.RelayEvent)
	close(ch)
	return ch
}

func closedRelayClosedStream() <-chan nostr.RelayClosed {
	ch := make(chan nostr.RelayClosed)
	close(ch)
	return ch
}

type reconnectGate struct {
	waits    chan time.Duration
	releases chan struct{}
}

func newReconnectGate() *reconnectGate {
	return &reconnectGate{
		waits:    make(chan time.Duration, 8),
		releases: make(chan struct{}, 8),
	}
}

func (g *reconnectGate) wait(ctx context.Context, delay time.Duration) bool {
	select {
	case g.waits <- delay:
	case <-ctx.Done():
		return false
	}
	select {
	case <-g.releases:
		return true
	case <-ctx.Done():
		return false
	}
}

func withScriptedChannelSubscriptions(t *testing.T, subscriber *scriptedSubscriber, gate *reconnectGate) {
	t.Helper()
	origSubscribe := channelSubscribeManyNotifyClosed
	origDelay := channelReconnectDelay
	origInitialBackoff := channelReconnectInitialBackoff
	origMaxBackoff := channelReconnectMaxBackoff

	channelSubscribeManyNotifyClosed = subscriber.subscribe
	channelReconnectDelay = gate.wait
	channelReconnectInitialBackoff = time.Millisecond
	channelReconnectMaxBackoff = 4 * time.Millisecond

	t.Cleanup(func() {
		channelSubscribeManyNotifyClosed = origSubscribe
		channelReconnectDelay = origDelay
		channelReconnectInitialBackoff = origInitialBackoff
		channelReconnectMaxBackoff = origMaxBackoff
	})
}

func waitSubscriptionCall(t *testing.T, subscriber *scriptedSubscriber) subscriptionCall {
	t.Helper()
	select {
	case call := <-subscriber.calls:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscription call")
		return subscriptionCall{}
	}
}

func waitReconnectDelay(t *testing.T, gate *reconnectGate) time.Duration {
	t.Helper()
	select {
	case delay := <-gate.waits:
		return delay
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect delay")
		return 0
	}
}

func releaseReconnect(gate *reconnectGate) {
	gate.releases <- struct{}{}
}

func waitInboundMessage(t *testing.T, messages <-chan InboundMessage) InboundMessage {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound message")
		return InboundMessage{}
	}
}

func waitChannelError(t *testing.T, errs <-chan error) error {
	t.Helper()
	select {
	case err := <-errs:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel error")
		return nil
	}
}

func testRelayEvent(eventID string, kind nostr.Kind, createdAt int64, content string) nostr.RelayEvent {
	tags := nostr.Tags{}
	switch kind {
	case nostr.KindSimpleGroupChatMessage:
		tags = append(tags, nostr.Tag{"h", "group"})
	case nostr.KindChannelMessage:
		tags = append(tags, nostr.Tag{"e", "channel-root", "", "root"})
	}
	sk, err := nostr.SecretKeyFromHex(strings.Repeat("3", 64))
	if err != nil {
		panic(err)
	}
	evt := nostr.Event{
		Kind:      kind,
		CreatedAt: nostr.Timestamp(createdAt),
		Content:   content,
		Tags:      tags,
	}
	if err := evt.Sign([32]byte(sk)); err != nil {
		panic(err)
	}
	if eventID == "bad-id" {
		evt.ID = nostr.MustIDFromHex(strings.Repeat("f", 64))
	}
	return nostr.RelayEvent{
		Event: evt,
		Relay: &nostr.Relay{URL: "wss://relay.test"},
	}
}

func TestNIP29GroupChannel_rejectsInvalidOrWrongGroupEvents(t *testing.T) {
	var delivered atomic.Int32
	ch := &NIP29GroupChannel{
		id:     "relay.test'group",
		gad:    nip29.GroupAddress{Relay: "wss://relay.test", ID: "group"},
		pubkey: strings.Repeat("1", 64),
		seen:   NewSeenCache(),
		onMsg:  func(InboundMessage) { delivered.Add(1) },
	}

	badID := testRelayEvent("bad-id", nostr.KindSimpleGroupChatMessage, 1000, "bad id")
	if ch.handleEvent(badID) {
		t.Fatal("bad event ID should not be processed")
	}
	wrongGroup := testRelayEvent("", nostr.KindSimpleGroupChatMessage, 1001, "wrong group")
	wrongGroup.Tags = nostr.Tags{{"h", "other-group"}}
	sk, err := nostr.SecretKeyFromHex(strings.Repeat("3", 64))
	if err != nil {
		t.Fatalf("parse test secret: %v", err)
	}
	if err := wrongGroup.Sign([32]byte(sk)); err != nil {
		t.Fatalf("sign wrong-group event: %v", err)
	}
	if ch.handleEvent(wrongGroup) {
		t.Fatal("wrong group tag should not be processed")
	}
	if delivered.Load() != 0 {
		t.Fatalf("delivered invalid events = %d, want 0", delivered.Load())
	}
}

func TestNIP29GroupChannel_reconnectsAfterStreamClosureAndDedupesReplay(t *testing.T) {
	subscriber := newScriptedSubscriber()
	gate := newReconnectGate()
	withScriptedChannelSubscriptions(t, subscriber, gate)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	messages := make(chan InboundMessage, 4)
	var delivered atomic.Int32
	ch := &NIP29GroupChannel{
		id:     "relay.test'group",
		gad:    nip29.GroupAddress{Relay: "wss://relay.test", ID: "group"},
		pubkey: strings.Repeat("1", 64),
		seen:   NewSeenCache(),
		onMsg: func(msg InboundMessage) {
			delivered.Add(1)
			messages <- msg
		},
	}

	go ch.subscribeLoop(ctx)
	firstCall := waitSubscriptionCall(t, subscriber)
	if len(firstCall.relays) != 1 || firstCall.relays[0] != "wss://relay.test" {
		t.Fatalf("unexpected relays: %v", firstCall.relays)
	}
	firstStream := newScriptedSubscription()
	subscriber.streams <- firstStream

	event := testRelayEvent(strings.Repeat("a", 64), nostr.KindSimpleGroupChatMessage, 1000, "hello")
	firstStream.events <- event
	msg := waitInboundMessage(t, messages)
	if msg.EventID != event.ID.Hex() || msg.Text != "hello" {
		t.Fatalf("unexpected message: %+v", msg)
	}

	close(firstStream.events)
	if delay := waitReconnectDelay(t, gate); delay != time.Millisecond {
		t.Fatalf("unexpected reconnect delay: %s", delay)
	}
	releaseReconnect(gate)

	secondCall := waitSubscriptionCall(t, subscriber)
	if want := applyJitter(nostr.Timestamp(1000), DefaultSinceJitter); secondCall.filter.Since != want {
		t.Fatalf("reconnect since = %d, want %d", secondCall.filter.Since, want)
	}
	secondStream := newScriptedSubscription()
	subscriber.streams <- secondStream
	secondStream.events <- event // replay from reconnect overlap
	close(secondStream.events)
	_ = waitReconnectDelay(t, gate)

	if got := delivered.Load(); got != 1 {
		t.Fatalf("delivered messages = %d, want 1", got)
	}
}

func TestNIP28PublicChannel_nonAuthClosedTriggersReconnectAndDedupesReplay(t *testing.T) {
	subscriber := newScriptedSubscriber()
	gate := newReconnectGate()
	withScriptedChannelSubscriptions(t, subscriber, gate)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	messages := make(chan InboundMessage, 4)
	errs := make(chan error, 4)
	var delivered atomic.Int32
	ch := &NIP28PublicChannel{
		channelID: "channel-root",
		relays:    []string{"wss://relay.one", "wss://relay.two"},
		pubkey:    strings.Repeat("1", 64),
		seen:      NewSeenCache(),
		onMsg: func(msg InboundMessage) {
			delivered.Add(1)
			messages <- msg
		},
		onErr: func(err error) { errs <- err },
	}

	go ch.subscribeLoop(ctx)
	firstCall := waitSubscriptionCall(t, subscriber)
	if len(firstCall.relays) != 2 {
		t.Fatalf("unexpected relays: %v", firstCall.relays)
	}
	firstStream := newScriptedSubscription()
	subscriber.streams <- firstStream

	event := testRelayEvent(strings.Repeat("b", 64), nostr.KindChannelMessage, 2000, "before closed")
	firstStream.events <- event
	msg := waitInboundMessage(t, messages)
	if msg.EventID != event.ID.Hex() || msg.Relay != "wss://relay.test" {
		t.Fatalf("unexpected initial message: %+v", msg)
	}

	firstStream.closed <- nostr.RelayClosed{Reason: "rate-limited", Relay: &nostr.Relay{URL: "wss://relay.one"}}
	if err := waitChannelError(t, errs); err == nil || !strings.Contains(err.Error(), "rate-limited") {
		t.Fatalf("expected CLOSED reason to be surfaced, got %v", err)
	}
	_ = waitReconnectDelay(t, gate)
	releaseReconnect(gate)

	secondCall := waitSubscriptionCall(t, subscriber)
	if want := applyJitter(nostr.Timestamp(2000), DefaultSinceJitter); secondCall.filter.Since != want {
		t.Fatalf("reconnect since = %d, want %d", secondCall.filter.Since, want)
	}
	secondStream := newScriptedSubscription()
	subscriber.streams <- secondStream
	secondStream.events <- event // replay from reconnect overlap
	close(secondStream.events)
	_ = waitReconnectDelay(t, gate)

	if got := delivered.Load(); got != 1 {
		t.Fatalf("delivered messages = %d, want 1", got)
	}
}

func TestNIP28PublicChannel_handledAuthClosedIsNotReportedAsError(t *testing.T) {
	errs := make(chan error, 1)
	ch := &NIP28PublicChannel{onErr: func(err error) { errs <- err }}
	ch.reportClosed(nostr.RelayClosed{
		Reason:      "auth-required: challenge accepted",
		Relay:       &nostr.Relay{URL: "wss://relay.one"},
		HandledAuth: true,
	})

	select {
	case err := <-errs:
		t.Fatalf("handled auth CLOSED should not be reported as error, got %v", err)
	default:
	}
}

func TestNextChannelReconnectBackoff(t *testing.T) {
	origInitialBackoff := channelReconnectInitialBackoff
	origMaxBackoff := channelReconnectMaxBackoff
	channelReconnectInitialBackoff = time.Millisecond
	channelReconnectMaxBackoff = 4 * time.Millisecond
	t.Cleanup(func() {
		channelReconnectInitialBackoff = origInitialBackoff
		channelReconnectMaxBackoff = origMaxBackoff
	})

	if got := nextChannelReconnectBackoff(0); got != time.Millisecond {
		t.Fatalf("zero backoff = %s, want %s", got, time.Millisecond)
	}
	if got := nextChannelReconnectBackoff(time.Millisecond); got != 2*time.Millisecond {
		t.Fatalf("doubled backoff = %s, want %s", got, 2*time.Millisecond)
	}
	if got := nextChannelReconnectBackoff(4 * time.Millisecond); got != 4*time.Millisecond {
		t.Fatalf("capped backoff = %s, want %s", got, 4*time.Millisecond)
	}
}
