package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip59"
)

func TestNormalizeNIP17SinceDefaultsToGiftWrapBackfillWindow(t *testing.T) {
	before := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	got := normalizeNIP17Since(0)
	after := time.Now().Add(-nip17GiftWrapBackfill).Unix()
	if got < before || got > after {
		t.Fatalf("expected default since within [%d, %d], got %d", before, after, got)
	}
}

func TestNormalizeNIP17SinceBackfillsCallerCheckpointWithoutAgeClamp(t *testing.T) {
	checkpoint := time.Now().Add(-30 * 24 * time.Hour).Unix()
	want := checkpoint - int64(nip17GiftWrapBackfill.Seconds())
	if got := normalizeNIP17Since(checkpoint); got != want {
		t.Fatalf("normalizeNIP17Since() = %d, want preserved historical checkpoint %d", got, want)
	}
}

func TestNormalizeNIP17SinceClampsToZero(t *testing.T) {
	if got := normalizeNIP17Since(60); got != 0 {
		t.Fatalf("expected non-negative clamp, got %d", got)
	}
}

func TestNIP17ValidateGiftWrapEvent(t *testing.T) {
	bus, keyer, recipient := newTestNIP17BusIdentity(t)
	evt := signedEvent(t, keyer, nostr.Event{
		Kind:      nostr.KindGiftWrap,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", recipient.Hex()}},
		Content:   "sealed-content",
	})
	if err := bus.validateGiftWrapEvent(evt, time.Now()); err != nil {
		t.Fatalf("expected valid gift wrap, got error: %v", err)
	}

	badTarget := evt
	badTarget.Tags = nostr.Tags{{"p", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}}
	if err := bus.validateGiftWrapEvent(badTarget, time.Now()); err == nil {
		t.Fatal("expected missing recipient-tag validation error")
	}

	badID := evt
	badID.Content = "mutated"
	if err := bus.validateGiftWrapEvent(badID, time.Now()); err == nil {
		t.Fatal("expected invalid id/signature validation error")
	}
}

func TestNIP17ValidateRumorEvent(t *testing.T) {
	bus, keyer, recipient := newTestNIP17BusIdentity(t)

	// Test 1: Incoming message (we are the recipient)
	rumor := unsignedRumorEvent(t, keyer, nostr.Event{
		Kind:      nostr.KindDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", recipient.Hex()}},
		Content:   "hello",
	})
	if err := bus.validateRumorEvent(rumor, time.Now()); err != nil {
		t.Fatalf("expected valid rumor where we are recipient, got error: %v", err)
	}

	// Test 2: Backup copy of our own sent message (we are the sender, not recipient)
	// This happens when we send a DM - the library gift-wraps it back to us for backup
	otherRecipient := testControlKeyer(t, "3333333333333333333333333333333333333333333333333333333333333333")
	otherPub, _ := otherRecipient.GetPublicKey(context.Background())
	selfSentRumor := unsignedRumorEvent(t, keyer, nostr.Event{
		Kind:      nostr.KindDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", otherPub.Hex()}}, // Sent to someone else
		Content:   "hello from me",
	})
	if err := bus.validateRumorEvent(selfSentRumor, time.Now()); err != nil {
		t.Fatalf("expected valid rumor where we are sender (backup copy), got error: %v", err)
	}

	// Test 3: Message not involving us at all should fail
	thirdPartyKeyer := testControlKeyer(t, "4444444444444444444444444444444444444444444444444444444444444444")
	unrelatedRumor := unsignedRumorEvent(t, thirdPartyKeyer, nostr.Event{
		Kind:      nostr.KindDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", otherPub.Hex()}}, // Between other parties
		Content:   "not for us",
	})
	if err := bus.validateRumorEvent(unrelatedRumor, time.Now()); err == nil {
		t.Fatal("expected validation error for rumor not involving us")
	}

	// Test 4: Missing p tag
	noPTag := rumor
	noPTag.Tags = nostr.Tags{}
	noPTag.ID = noPTag.GetID()
	if err := bus.validateRumorEvent(noPTag, time.Now()); err == nil {
		t.Fatal("expected missing p tag validation error")
	}

	wrongKind := rumor
	wrongKind.Kind = nostr.KindTextNote
	if err := bus.validateRumorEvent(wrongKind, time.Now()); err == nil {
		t.Fatal("expected kind validation error")
	}

	future := rumor
	future.CreatedAt = nostr.Timestamp(time.Now().Add(inboundEventMaxFutureSkew + time.Second).Unix())
	if err := bus.validateRumorEvent(future, time.Now()); err == nil {
		t.Fatal("expected future-skew validation error")
	}

	historical := rumor
	historical.CreatedAt = nostr.Timestamp(time.Now().Add(-30 * 24 * time.Hour).Unix())
	historical.ID = historical.GetID()
	if err := bus.validateRumorEvent(historical, time.Now()); err != nil {
		t.Fatalf("NIP-17 permits historical rumors, got: %v", err)
	}
}

func TestNIP17TimestampBounds(t *testing.T) {
	now := time.Now()
	if timestampTooFarFuture(now.Unix(), now, inboundEventMaxFutureSkew) {
		t.Fatal("expected current timestamp not to be future")
	}
	if !timestampTooFarFuture(now.Add(inboundEventMaxFutureSkew+time.Second).Unix(), now, inboundEventMaxFutureSkew) {
		t.Fatal("expected future timestamp to be rejected")
	}
	if timestampTooFarFuture(now.Add(inboundEventMaxFutureSkew).Unix(), now, inboundEventMaxFutureSkew) {
		t.Fatal("timestamp at exact future skew threshold should be accepted")
	}
}

func TestNIP17AcceptsCurrentSpecRumorKinds(t *testing.T) {
	bus, _, recipient := newTestNIP17BusIdentity(t)
	sender := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	targetID := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		kind    nostr.Kind
		content string
		tags    nostr.Tags
	}{
		{name: "chat", kind: nostr.KindDirectMessage, content: "hello", tags: nostr.Tags{{"p", recipient.Hex()}}},
		{name: "file", kind: nostr.KindFileMessage, content: "https://files.example/ciphertext", tags: nostr.Tags{
			{"p", recipient.Hex()}, {"file-type", "image/png"}, {"encryption-algorithm", "aes-gcm"},
			{"decryption-key", "secret"}, {"decryption-nonce", "nonce"}, {"x", targetID},
		}},
		{name: "reaction", kind: nostr.KindReaction, content: "👍", tags: nostr.Tags{{"p", recipient.Hex()}, {"e", targetID}}},
		{name: "deletion", kind: nostr.KindDeletion, content: "sent by mistake", tags: nostr.Tags{{"p", recipient.Hex()}, {"e", targetID}, {"k", "14"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rumor := unsignedRumorEvent(t, sender, nostr.Event{Kind: tt.kind, CreatedAt: nostr.Now(), Tags: tt.tags, Content: tt.content})
			if err := bus.validateRumorEvent(rumor, time.Now()); err != nil {
				t.Fatalf("validate kind %d: %v", tt.kind, err)
			}
		})
	}
}

func TestNIP17BuildRoomRumorWireFormat(t *testing.T) {
	bus, _, _ := newTestNIP17BusIdentity(t)
	first := otherNIP17PubKey(t)
	thirdKeyer := testControlKeyer(t, "3333333333333333333333333333333333333333333333333333333333333333")
	third, err := thirdKeyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("third pubkey: %v", err)
	}
	parentID := strings.Repeat("b", 64)
	rumor, recipients, err := bus.buildNIP17Rumor(nostr.KindDirectMessage, "room hello", NIP17Room{
		Participants: []NIP17Participant{
			{PubKey: first.Hex(), RelayURL: "wss://first.example"},
			{PubKey: third.Hex()},
		},
		Subject: "current topic",
	}, nostr.Tags{{"e", parentID, "wss://reply.example"}})
	if err != nil {
		t.Fatalf("build rumor: %v", err)
	}
	if rumor.Kind != nostr.KindDirectMessage || rumor.Content != "room hello" || rumor.PubKey != bus.public {
		t.Fatalf("unexpected rumor envelope: %+v", rumor)
	}
	if len(recipients) != 2 || len(rumor.Tags) != 4 {
		t.Fatalf("recipients=%v tags=%v", recipients, rumor.Tags)
	}
	wantTags := nostr.Tags{
		{"p", first.Hex(), "wss://first.example"}, {"p", third.Hex()},
		{"subject", "current topic"}, {"e", parentID, "wss://reply.example"},
	}
	for i := range wantTags {
		if strings.Join(rumor.Tags[i], "\x00") != strings.Join(wantTags[i], "\x00") {
			t.Fatalf("tag[%d] = %v, want %v", i, rumor.Tags[i], wantTags[i])
		}
	}
	if rumor.ID != rumor.GetID() || rumor.Sig != ([64]byte{}) {
		t.Fatal("rumor must have a canonical id and remain unsigned")
	}
}

func TestNIP17RumorMetadataPreservesRoomSubjectAndReply(t *testing.T) {
	bus, _, local := newTestNIP17BusIdentity(t)
	sender := otherNIP17PubKey(t)
	thirdKeyer := testControlKeyer(t, "3333333333333333333333333333333333333333333333333333333333333333")
	third, _ := thirdKeyer.GetPublicKey(context.Background())
	parentID := strings.Repeat("c", 64)
	rumor := nostr.Event{PubKey: sender, Kind: nostr.KindDirectMessage, Tags: nostr.Tags{
		{"p", local.Hex()}, {"p", third.Hex(), "wss://third.example"},
		{"subject", "group topic"}, {"e", parentID},
	}}
	recipients, subject, replyTo, room := nip17RumorMetadata(rumor, bus.public)
	if len(recipients) != 2 || subject != "group topic" || replyTo != parentID {
		t.Fatalf("recipients=%v subject=%q replyTo=%q", recipients, subject, replyTo)
	}
	if len(room.Participants) != 2 || room.Participants[0].PubKey != sender.Hex() || room.Participants[1].PubKey != third.Hex() {
		t.Fatalf("reply room = %+v", room)
	}
	if room.Subject != "group topic" {
		t.Fatalf("reply room subject = %q", room.Subject)
	}
}

func TestNIP17DeletionBatchesSplitDifferentRumorKinds(t *testing.T) {
	messageID := strings.Repeat("d", 64)
	fileID := strings.Repeat("e", 64)
	reactionID := strings.Repeat("f", 64)
	batches, err := buildNIP17DeletionBatches([]NIP17DeletionTarget{
		{EventID: messageID, Kind: nostr.KindDirectMessage},
		{EventID: fileID, Kind: nostr.KindFileMessage},
		{EventID: reactionID, Kind: nostr.KindReaction},
		{EventID: strings.Repeat("a", 64), Kind: nostr.KindDirectMessage},
	})
	if err != nil {
		t.Fatalf("build deletion batches: %v", err)
	}
	if len(batches) != 3 {
		t.Fatalf("got %d batches, want 3", len(batches))
	}
	wantKinds := []nostr.Kind{nostr.KindDirectMessage, nostr.KindFileMessage, nostr.KindReaction}
	wantEventCounts := []int{2, 1, 1}
	for i, batch := range batches {
		if batch.kind != wantKinds[i] {
			t.Fatalf("batch[%d] kind = %d, want %d", i, batch.kind, wantKinds[i])
		}
		if len(batch.tags) != wantEventCounts[i]+1 {
			t.Fatalf("batch[%d] tags = %v", i, batch.tags)
		}
		kindTag := batch.tags[len(batch.tags)-1]
		if len(kindTag) != 2 || kindTag[0] != "k" || kindTag[1] != fmt.Sprint(batch.kind) {
			t.Fatalf("batch[%d] k tag = %v", i, kindTag)
		}
	}
}

func TestNIP17HandleRumorDeduplicatesByRumorID(t *testing.T) {
	bus, _, recipient := newTestNIP17BusIdentity(t)
	sender := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	rumor := unsignedRumorEvent(t, sender, nostr.Event{
		Kind:      nostr.KindDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", recipient.Hex()}},
		Content:   "hello from two relays",
	})
	bus.ctx = context.Background()
	bus.seenSet = map[string]struct{}{}
	bus.seenCap = 16
	bus.messageQueue = make(chan InboundDM, 2)
	bus.onMessage = func(context.Context, InboundDM) error { return nil }

	bus.handleRumor(rumor)
	bus.handleRumor(rumor)

	select {
	case msg := <-bus.messageQueue:
		if msg.EventID != rumor.ID.Hex() || msg.Text != "hello from two relays" {
			t.Fatalf("unexpected message: %+v", msg)
		}
	default:
		t.Fatal("expected first rumor delivery to enqueue message")
	}
	select {
	case msg := <-bus.messageQueue:
		t.Fatalf("duplicate rumor should not enqueue: %+v", msg)
	default:
	}
	if !bus.markSeen17(rumor.ID.Hex()) {
		t.Fatal("rumor should remain enrolled in seen-set after first delivery")
	}
}

func TestNIP17BusCloseNilAndPartial(t *testing.T) {
	var nilBus *NIP17Bus
	nilBus.Close()
	(&NIP17Bus{}).Close()
}

func TestStartNIP17BusRejectsMismatchedHubPubKey(t *testing.T) {
	hubKey := newNIP04KeyerAdapter(mustSecretKey(t, "1111111111111111111111111111111111111111111111111111111111111111"))
	hub, err := NewHub(context.Background(), hubKey, nil)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	defer hub.Close()

	busKey := newNIP04KeyerAdapter(mustSecretKey(t, "2222222222222222222222222222222222222222222222222222222222222222"))
	_, err = StartNIP17Bus(context.Background(), NIP17BusOptions{
		Keyer:  busKey,
		Relays: []string{"wss://relay.example"},
		Hub:    hub,
	})
	if err == nil || err.Error() != "nip17 bus: hub pubkey does not match keyer pubkey" {
		t.Fatalf("expected hub mismatch error, got %v", err)
	}
}

func TestNIP17SendDMRequiresRecipientKind10050(t *testing.T) {
	bus, keyer, _ := newTestNIP17BusIdentity(t)
	recipientPubKey := otherNIP17PubKey(t)
	bus.kr = keyer
	bus.pool = NewPoolNIP42(keyer)
	defer bus.pool.Close("test")
	bus.relays = []string{"wss://configured-write.example"}
	bus.testLookupDMRelays = func(context.Context, nostr.PubKey) []string { return nil }

	err := bus.SendDM(context.Background(), recipientPubKey.Hex(), "hello")
	if !errors.Is(err, ErrRecipientNotNIP17Ready) {
		t.Fatalf("SendDM error = %v, want ErrRecipientNotNIP17Ready", err)
	}
}

func TestNIP17SendDMRequiresSenderKind10050ForBackupCopy(t *testing.T) {
	bus, keyer, _ := newTestNIP17BusIdentity(t)
	recipientPubKey := otherNIP17PubKey(t)
	bus.kr = keyer
	bus.pool = NewPoolNIP42(keyer)
	defer bus.pool.Close("test")
	bus.relays = []string{"wss://configured-write.example"}
	lookedUp := make([]nostr.PubKey, 0, 2)
	bus.testLookupDMRelays = func(_ context.Context, pk nostr.PubKey) []string {
		lookedUp = append(lookedUp, pk)
		if pk == bus.public {
			return nil
		}
		return []string{"wss://recipient-dm.example"}
	}

	err := bus.SendDM(context.Background(), recipientPubKey.Hex(), "hello")
	if !errors.Is(err, ErrRecipientNotNIP17Ready) {
		t.Fatalf("SendDM error = %v, want sender readiness error", err)
	}
	if len(lookedUp) != 2 || lookedUp[0] != recipientPubKey || lookedUp[1] != bus.public {
		t.Fatalf("DM relay lookups = %v, want recipient then sender", lookedUp)
	}
}

func TestNIP17SendDMUsesAdvertisedKind10050Relays(t *testing.T) {
	bus, keyer, _ := newTestNIP17BusIdentity(t)
	recipientPubKey := otherNIP17PubKey(t)
	bus.kr = keyer
	bus.pool = NewPoolNIP42(keyer)
	bus.pool.Close("test")
	bus.relays = []string{"wss://configured-write.example"}
	bus.testLookupDMRelays = func(context.Context, nostr.PubKey) []string {
		return []string{"wss://recipient-dm.example"}
	}

	err := bus.SendDM(context.Background(), recipientPubKey.Hex(), "hello")
	if errors.Is(err, ErrRecipientNotNIP17Ready) {
		t.Fatalf("SendDM should not return readiness error when recipient advertises 10050 relays: %v", err)
	}
	if err == nil {
		t.Fatal("expected publish to fail without a pool, got nil")
	}
}

func TestNIP59GiftWrapUnwrapInvariants(t *testing.T) {
	sender := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	recipient := testControlKeyer(t, "3333333333333333333333333333333333333333333333333333333333333333")
	senderPub, err := sender.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("sender pubkey: %v", err)
	}
	recipientPub, err := recipient.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("recipient pubkey: %v", err)
	}

	rumor := nostr.Event{
		Kind:      nostr.KindDirectMessage,
		PubKey:    senderPub,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", recipientPub.Hex()}},
		Content:   "hello nip59",
	}
	rumor.ID = rumor.GetID()

	wrap, err := nip59.GiftWrap(
		rumor,
		recipientPub,
		func(plaintext string) (string, error) {
			return sender.Encrypt(context.Background(), plaintext, recipientPub)
		},
		func(evt *nostr.Event) error { return sender.SignEvent(context.Background(), evt) },
		nil,
	)
	if err != nil {
		t.Fatalf("GiftWrap: %v", err)
	}
	if !wrap.Tags.ContainsAny("p", []string{recipientPub.Hex()}) {
		t.Fatalf("gift wrap missing p tag for local pubkey %s: %v", recipientPub.Hex(), wrap.Tags)
	}

	sealJSON, err := recipient.Decrypt(context.Background(), wrap.Content, wrap.PubKey)
	if err != nil {
		t.Fatalf("decrypt seal: %v", err)
	}
	var seal nostr.Event
	if err := json.Unmarshal([]byte(sealJSON), &seal); err != nil {
		t.Fatalf("unmarshal seal: %v", err)
	}
	if seal.Kind != nostr.KindSeal {
		t.Fatalf("seal kind = %d, want %d", seal.Kind, nostr.KindSeal)
	}
	if len(seal.Tags) != 0 {
		t.Fatalf("seal tags = %v, want empty", seal.Tags)
	}
	if seal.PubKey != rumor.PubKey {
		t.Fatalf("seal pubkey = %s, want rumor pubkey %s", seal.PubKey.Hex(), rumor.PubKey.Hex())
	}

	rumorJSON, err := recipient.Decrypt(context.Background(), seal.Content, seal.PubKey)
	if err != nil {
		t.Fatalf("decrypt rumor: %v", err)
	}
	var innerRumor nostr.Event
	if err := json.Unmarshal([]byte(rumorJSON), &innerRumor); err != nil {
		t.Fatalf("unmarshal rumor: %v", err)
	}
	if innerRumor.Sig != ([64]byte{}) {
		t.Fatalf("rumor signature = %x, want zero unsigned signature", innerRumor.Sig)
	}

	unwrapped, err := nip59.GiftUnwrap(wrap, func(pk nostr.PubKey, ciphertext string) (string, error) {
		return recipient.Decrypt(context.Background(), ciphertext, pk)
	})
	if err != nil {
		t.Fatalf("GiftUnwrap: %v", err)
	}
	if unwrapped.PubKey != seal.PubKey || unwrapped.PubKey != rumor.PubKey {
		t.Fatalf("unwrapped pubkey = %s, seal=%s rumor=%s", unwrapped.PubKey.Hex(), seal.PubKey.Hex(), rumor.PubKey.Hex())
	}
}

func TestNIP17ReceiveLoopRestartsFromClosedGiftWrapStreamWithReplayWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type listenCall struct {
		relays []string
		since  nostr.Timestamp
		ch     chan nostr.Event
	}
	calls := make(chan listenCall, 2)
	bus := &NIP17Bus{
		ctx:          ctx,
		cancel:       cancel,
		relays:       []string{"wss://gift.example"},
		rebindCh:     make(chan struct{}, 1),
		messageQueue: make(chan InboundDM, 1),
		subHealth:    NewSubHealthTracker("nip17"),
		testListenGiftWraps: func(ctx context.Context, relays []string, since nostr.Timestamp) <-chan nostr.Event {
			ch := make(chan nostr.Event)
			calls <- listenCall{relays: append([]string{}, relays...), since: since, ch: ch}
			return ch
		},
		onError: func(error) {},
	}
	initialSince := nostr.Timestamp(time.Now().Add(-time.Hour).Unix())
	bus.wg.Add(1)
	go bus.receiveLoop(initialSince)

	first := receiveBeforeTestDeadline(t, calls, "first nip17 gift-wrap listener")
	if len(first.relays) != 1 || first.relays[0] != "wss://gift.example" {
		t.Fatalf("unexpected relays: %v", first.relays)
	}
	if first.since != initialSince {
		t.Fatalf("first since = %d, want %d", first.since, initialSince)
	}

	beforeReplay := normalizeNIP17Since(time.Now().Unix())
	close(first.ch)
	second := receiveBeforeTestDeadline(t, calls, "second nip17 gift-wrap listener")
	afterReplay := normalizeNIP17Since(time.Now().Unix())
	if int64(second.since) < beforeReplay || int64(second.since) > afterReplay {
		t.Fatalf("restart since = %d, want NIP-59 replay window within [%d,%d]", second.since, beforeReplay, afterReplay)
	}

	cancel()
	close(second.ch)
	bus.wg.Wait()
}

func TestNIP17EOSEChannelCanBeDisabledAfterFirstSignal(t *testing.T) {
	eoseCh := make(chan struct{})
	close(eoseCh)

	select {
	case <-eoseCh:
		eoseCh = nil
	default:
		t.Fatal("expected closed EOSE channel to be readable")
	}

	select {
	case <-eoseCh:
		t.Fatal("disabled EOSE channel should not fire again")
	default:
	}
}

func newTestNIP17BusIdentity(t *testing.T) (*NIP17Bus, nostr.Keyer, nostr.PubKey) {
	t.Helper()
	sk, err := ParseSecretKey("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("ParseSecretKey: %v", err)
	}
	keyer := newNIP04KeyerAdapter(sk)
	pub, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	return &NIP17Bus{public: pub}, keyer, pub
}

func otherNIP17PubKey(t *testing.T) nostr.PubKey {
	t.Helper()
	keyer := testControlKeyer(t, "2222222222222222222222222222222222222222222222222222222222222222")
	pubkey, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	return pubkey
}

func signedEvent(t *testing.T, keyer nostr.Keyer, evt nostr.Event) nostr.Event {
	t.Helper()
	if err := keyer.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("SignEvent: %v", err)
	}
	return evt
}

func unsignedRumorEvent(t *testing.T, keyer nostr.Keyer, evt nostr.Event) nostr.Event {
	t.Helper()
	pub, err := keyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	evt.PubKey = pub
	evt.ID = evt.GetID()
	return evt
}

// TestNIP17SendDMAcceptsLongMessages verifies that NIP-17 outbound messages
// are not artificially limited by the NIP-04 size constraint.
func TestNIP17SendDMAcceptsLongMessages(t *testing.T) {
	bus, keyer, _ := newTestNIP17BusIdentity(t)
	recipientPubKey := otherNIP17PubKey(t)
	bus.kr = keyer
	// Note: We don't set up a pool, so the actual publish will fail.
	// We're only testing that validation doesn't reject long messages.

	// Create a message longer than the old NIP-04 limit (2800 chars)
	longText := ""
	for i := 0; i < 3000; i++ {
		longText += "x"
	}

	// This should NOT fail with "dm text exceeds 2800 characters"
	// It may fail later in the publish path, but not at validation
	err := bus.SendDM(context.Background(), recipientPubKey.Hex(), longText)
	// We expect failure because we haven't set up relays, but the error
	// should NOT be about message length
	if err != nil && err.Error() == "dm text exceeds 2800 characters" {
		t.Fatalf("NIP-17 SendDM should not enforce NIP-04 size limit, got: %v", err)
	}
}

// TestNIP17SendDMRejectsEmptyMessages verifies that empty/whitespace-only
// messages are still rejected.
func TestNIP17SendDMRejectsEmptyMessages(t *testing.T) {
	bus, keyer, _ := newTestNIP17BusIdentity(t)
	recipientPubKey := otherNIP17PubKey(t)
	bus.kr = keyer

	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"tabs only", "\t\t"},
		{"newlines only", "\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bus.SendDM(context.Background(), recipientPubKey.Hex(), tt.input)
			if err == nil || err.Error() != "dm text is empty" {
				t.Fatalf("expected 'dm text is empty' error, got: %v", err)
			}
		})
	}
}
