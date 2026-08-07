package channels

// parity_matrix_test.go is the canonical NIP-29 parity acceptance suite: each
// test maps to a row of the implementation brief's "parity verification matrix"
// (swarmstr-zysu). Every row is a decision a reference OpenClaw + openclaw-nostr
// agent makes; Metiq must match it.
//
// Coverage map (row -> assertion here, or the covering test elsewhere):
//   1-7, 10, 11 -> TestParityMatrix_PreflightRows (channels.ResolveNostrGroupPreflight)
//   8, 9        -> TestParityMatrix_PairGuard (BotLoopProtection)
//   12          -> runtime.TestSelectIdentityBoundProfile_RejectsAuthorMismatch
//                  (identity-bound kind:0 import; selectIdentityBoundProfile is
//                  unexported, so it is asserted in its own package)
//   13          -> TestParityMatrix_Echo (EchoSuppressor)
//   14          -> agent.ApplyCommitmentGuard tests (commitment guard lives in
//                  internal/agent; wired via NostrRoomPolicy.CommitmentGuard)
//   15          -> TestParityMatrix_SenderIsBotVerdict (runtime.PeerAgentIndex)
//   16          -> TestParityMatrix_ConcurrentIdentity (identity.Establisher)
//   17          -> TestParityMatrix_QueueOverflow (RoomDispatchQueue)
//   18          -> TestParityMatrix_SeenGatingRetry (RedispatchScheduler) +
//                  TestSettleDispatch_SeenGating
//   R1 should-reply gate, reference: ocn-e2f -> TestParityMatrix_ShouldReplyGate
//   R3 ACK conversion, reference: ocn-te2 (default-on; false opts out) -> TestParityMatrix_ACKConversion
//   R4 commitment enforcement, reference: ocn-krf (pending) -> TestParityMatrix_CommitmentEnforcement
//      (asserts Metiq's independently implemented room-policy behavior)

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip29"

	runtime "metiq/internal/nostr/runtime"
	identity "metiq/internal/session/identity"
	"metiq/internal/store/state"
)

func TestParityMatrix_PreflightRows(t *testing.T) {
	bot := hexKey()
	sender := hexKey()

	// mk returns a base live input for (bot, sender) with the given tweaks.
	type mut func(*NostrPreflightInput)
	build := func(muts ...mut) NostrPreflightInput {
		in := basePreflight(bot, sender)
		for _, m := range muts {
			m(&in)
		}
		return in
	}
	requireMention := func(v bool) mut { return func(in *NostrPreflightInput) { b := v; in.RequireMention = &b } }
	mentionBot := func(in *NostrPreflightInput) { in.Meta.MentionedPubkeys = []string{bot} }
	knownBot := func(in *NostrPreflightInput) { in.SenderIsKnownBot = true; in.SenderIsPeer = true }
	allowBots := func(v NostrAllowBots) mut { return func(in *NostrPreflightInput) { in.AllowBots = v } }
	backfill := func(in *NostrPreflightInput) { in.Meta.DeliveryPhase = DeliveryPhaseBackfill }
	replyToBot := func(in *NostrPreflightInput) { in.Meta.ReplyToSenderPubkey = bot }

	cases := []struct {
		row        int
		name       string
		in         NostrPreflightInput
		wantDrop   bool
		wantReason string
	}{
		{1, "human @mention in requireMention room answers",
			build(requireMention(true), mentionBot), false, ""},
		{2, "human ambient, requireMention -> no_mention",
			build(requireMention(true)), true, DropNoMention},
		{3, "human ambient, requireMention=false -> answer raw",
			build(requireMention(false)), false, ""},
		{4, "known bot ambient, allowBots=mentions -> bot_requires_mention",
			build(requireMention(true), knownBot, allowBots(AllowBotsMentions)), true, DropBotRequiresMention},
		{5, "known bot @mention, allowBots=mentions -> answer",
			build(requireMention(true), knownBot, allowBots(AllowBotsMentions), mentionBot), false, ""},
		{6, "known bot @mention, allowBots=off -> bot_disallowed",
			build(requireMention(true), knownBot, allowBots(AllowBotsOff), mentionBot), true, DropBotDisallowed},
		{7, "unknown sender, allowBots=off -> NOT gated",
			build(requireMention(false), allowBots(AllowBotsOff)), false, ""},
		{10, "backfill bot ambient -> backfill_ambient",
			build(requireMention(false), knownBot, allowBots(AllowBotsAll), backfill), true, DropBackfillAmbient},
		{11, "reply-to-bot from human -> answer (implicit mention)",
			build(requireMention(true), replyToBot), false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ResolveNostrGroupPreflight(tc.in)
			if r.ShouldDrop != tc.wantDrop {
				t.Fatalf("row %d: ShouldDrop=%v want %v (reason=%q)", tc.row, r.ShouldDrop, tc.wantDrop, r.DropReason)
			}
			if tc.wantReason != "" && r.DropReason != tc.wantReason {
				t.Errorf("row %d: DropReason=%q want %q", tc.row, r.DropReason, tc.wantReason)
			}
		})
	}

	// Row 11 (peer half): reply-to-bot from a PEER suppresses the implicit
	// mention, so an otherwise-unmentioned message drops.
	peer := basePreflight(bot, sender)
	rm := true
	peer.RequireMention = &rm
	peer.SenderIsPeer = true
	peer.Meta.ReplyToSenderPubkey = bot
	if r := ResolveNostrGroupPreflight(peer); !r.ShouldDrop || r.DropReason != DropNoMention {
		t.Errorf("row 11 peer: reply-to-bot from peer should drop no_mention, got drop=%v reason=%q", r.ShouldDrop, r.DropReason)
	}
}

// R1 dedicated should-reply admission gate, reference: openclaw-nostr ocn-e2f.
// Being named as the addressee passes, being discussed in third person drops,
// capability-matched questions pass, and the room off-switch preserves legacy admission.
func TestParityMatrix_ShouldReplyGate(t *testing.T) {
	base := ShouldReplyGateInput{
		Aliases:      []string{"Ada"},
		Capabilities: []string{"code-review"},
		Enabled:      true,
	}
	cases := []struct {
		name    string
		text    string
		outcome ShouldReplyGateOutcome
		reason  ShouldReplyGateReason
	}{
		{"named addressee", "Ada, can you review this?", ShouldReplyPass, ShouldReplyReasonDirectlyAddressed},
		{"talked about", "Is Ada reviewing this?", ShouldReplyDrop, ShouldReplyReasonTalkedAbout},
		{"capability question", "Could someone review this code?", ShouldReplyPass, ShouldReplyReasonCapabilityMatch},
		{"generic question fails quiet", "What do people think?", ShouldReplyDrop, ShouldReplyReasonAmbiguousNoModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.Text = tc.text
			got := ResolveShouldReplyGate(in, nil)
			if got.Outcome != tc.outcome || got.Reason != tc.reason {
				t.Fatalf("R1 decision = %+v, want outcome=%s reason=%s", got, tc.outcome, tc.reason)
			}
		})
	}
	policy := ResolveNostrRoomPolicy(map[string]any{"shouldReplyGate": false})
	if policy.ShouldReplyGate {
		t.Fatal("R1 shouldReplyGate=false did not preserve legacy admission")
	}
}

// Rows 8 & 9: the pair guard suppresses a runaway bot pair after the budget,
// leaves a distinct pair unaffected, and counts a redelivered event once.
func TestParityMatrix_PairGuard(t *testing.T) {
	clock := newGuardClock()
	guard := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	blp := NewBotLoopProtection(guard, 0)

	facts := func(sender, eventID string) BotLoopProtectionFacts {
		return BotLoopProtectionFacts{ScopeID: "acct", ConversationID: "room", SenderID: sender, ReceiverID: "me", DefaultEnabled: true, EventID: eventID}
	}

	// Row 8: 20 distinct events pass, the 21st trips the cooldown.
	suppressed := false
	for i := 0; i < 21; i++ {
		if blp.RecordAndCheck(facts("botA", "a"+time.Duration(i).String())).Suppressed {
			suppressed = true
		}
	}
	if !suppressed {
		t.Fatal("row 8: pair should be suppressed after exceeding the budget")
	}
	// A DIFFERENT pair in the same room is unaffected.
	if blp.RecordAndCheck(facts("botB", "b0")).Suppressed {
		t.Error("row 8: a distinct pair must not inherit the cooldown")
	}

	// Row 9: a redelivered event (same id) consumes the budget once.
	guard2 := NewPairLoopGuard(PairLoopGuardOptions{Now: clock.now})
	blp2 := NewBotLoopProtection(guard2, 0)
	for i := 0; i < 5; i++ {
		blp2.RecordAndCheck(facts("botA", "evt-1")) // same event id, redelivered
	}
	snap := guard2.Snapshot()
	if len(snap) != 1 || snap[0].RecentCount != 1 {
		t.Errorf("row 9: redelivered event must count once, snapshot=%+v", snap)
	}
}

// Row 13: an echo reply that restates recent traffic is suppressed.
func TestParityMatrix_Echo(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.Observe("room", "The migration completed on all three shards")
	if !s.IsEcho("room", "the migration completed on all three shards!", 0) {
		t.Error("row 13: restated reply should be suppressed as an echo")
	}
	if s.IsEcho("room", "Starting an entirely unrelated new job now", 0) {
		t.Error("row 13: a distinct reply must not be suppressed")
	}
}

// R3 ACK conversion, reference: ocn-te2. Rooms convert targeted ACKs by
// default; ackAsReaction=false opts out, and substantive text remains chat.
func TestParityMatrix_ACKConversion(t *testing.T) {
	publisher := &channelFakePublisher{results: []nostr.PublishResult{{RelayURL: "wss://relay.test"}}}
	defaultPolicy := ResolveNostrRoomPolicy(nil)
	ch := &NIP29GroupChannel{
		gad:           nip29.GroupAddress{Relay: "wss://relay.test", ID: "room"},
		keyer:         testKeyer(t),
		publisher:     publisher,
		ackAsReaction: defaultPolicy.AckAsReaction,
	}
	if err := ch.sendReply(context.Background(), "got it!", "target-event", "target-author"); err != nil {
		t.Fatal(err)
	}
	if publisher.event.Kind != nostr.KindReaction || !outHasTag(publisher.event, "e", "target-event") {
		t.Fatalf("R3: pure ACK did not become a target reaction: %#v", publisher.event)
	}
	if err := ch.sendReply(context.Background(), "got it; I will investigate", "target-event", "target-author"); err != nil {
		t.Fatal(err)
	}
	if publisher.event.Kind != nostr.KindSimpleGroupChatMessage {
		t.Fatalf("R3: substantive reply was converted: %#v", publisher.event)
	}
	ch.ackAsReaction = ResolveNostrRoomPolicy(map[string]any{"ackAsReaction": false}).AckAsReaction
	if err := ch.sendReply(context.Background(), "ack", "target-event", "target-author"); err != nil {
		t.Fatal(err)
	}
	if publisher.event.Kind != nostr.KindSimpleGroupChatMessage {
		t.Fatalf("R3: explicit false did not opt out: %#v", publisher.event)
	}
}

// R4 commitment enforcement. openclaw-nostr ocn-krf is still pending, so
// this asserts Metiq's own explicit taskflow-room policy: an unbacked promise
// is rewritten while a same-turn task open preserves the response.
func TestParityMatrix_CommitmentEnforcement(t *testing.T) {
	publisher := &channelFakePublisher{results: []nostr.PublishResult{{RelayURL: "wss://relay.test"}}}
	ch := &NIP29GroupChannel{
		gad:                   nip29.GroupAddress{Relay: "wss://relay.test", ID: "room"},
		keyer:                 testKeyer(t),
		publisher:             publisher,
		commitmentEnforcement: ResolveNostrRoomPolicy(map[string]any{"commitmentEnforcement": true}).CommitmentEnforcement,
	}
	if err := ch.Send(context.Background(), "I'll handle the incident."); err != nil {
		t.Fatal(err)
	}
	if publisher.event.Content != CommitmentEnforcementRewrite {
		t.Fatalf("R4: unbacked commitment posted as-is: %q", publisher.event.Content)
	}
	ctx := ContextWithCommitmentBacking(context.Background(), CommitmentBacking{SuccessfulTaskFlowActions: 1})
	const backed = "I'll handle the incident."
	if err := ch.Send(ctx, backed); err != nil {
		t.Fatal(err)
	}
	if publisher.event.Content != backed {
		t.Fatalf("R4: backed commitment was rewritten: %q", publisher.event.Content)
	}
}

// Row 15: the peer-agent verdict (which drives ctx.SenderIsBot) is a definitive
// bot for a kind:0 bot:true profile, human for a non-bot profile, and unknown
// (never a bot) when no profile is published.
func TestParityMatrix_SenderIsBotVerdict(t *testing.T) {
	botKey := hexKey()
	humanKey := hexKey()
	noProfileKey := hexKey()
	idx, _ := runtime.NewPeerAgentIndex(runtime.PeerAgentIndexOptions{
		FetchProfile: func(pk string) (*runtime.PeerProfileFacts, error) {
			switch pk {
			case botKey:
				return &runtime.PeerProfileFacts{IsBot: true}, nil
			case humanKey:
				return &runtime.PeerProfileFacts{IsBot: false}, nil
			default:
				return nil, nil // no kind:0 published
			}
		},
	})
	defer idx.Close()

	if v := idx.Resolve(botKey); v != runtime.VerdictBot {
		t.Errorf("row 15: bot profile -> %v, want bot", v)
	}
	if v := idx.Resolve(humanKey); v != runtime.VerdictHuman {
		t.Errorf("row 15: human profile -> %v, want human", v)
	}
	if v := idx.Resolve(noProfileKey); v != runtime.VerdictUnknown {
		t.Errorf("row 15: no profile -> %v, want unknown", v)
	}
}

// Row 16: concurrent first-turns for one room key resolve to a single identity.
func TestParityMatrix_ConcurrentIdentity(t *testing.T) {
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	est := identity.NewEstablisher(store)
	const n = 16
	var wg sync.WaitGroup
	var created int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, c, _ := est.Establish("nostr:room:relay'r1"); c {
				atomic.AddInt32(&created, 1)
			}
		}()
	}
	wg.Wait()
	if created != 1 || len(store.List()) != 1 {
		t.Errorf("row 16: concurrent first-turns must yield one transcript (created=%d entries=%d)", created, len(store.List()))
	}
}

// Row 17: queue overflow load-sheds the newest event and reports it (not retried).
func TestParityMatrix_QueueOverflow(t *testing.T) {
	var dropped int32
	q := NewRoomDispatchQueue(RoomDispatchQueueOptions{
		MaxDepth:   2,
		OnOverflow: func(RoomOverflow) { atomic.AddInt32(&dropped, 1) },
	})
	block := make(chan struct{})
	for i := 0; i < 2; i++ {
		q.Enqueue("room", 9, func() { <-block })
	}
	if q.Enqueue("room", 9, func() { t.Error("shed event must not run") }) {
		t.Error("row 17: overflow enqueue should be load-shed")
	}
	if atomic.LoadInt32(&dropped) != 1 {
		t.Errorf("row 17: overflow should report exactly one drop, got %d", dropped)
	}
	close(block)
	q.Wait()
}

// Row 18: an unconfirmed dispatch is retried on a bounded 30s/2m/5m backoff,
// capped at 3 attempts per process.
func TestParityMatrix_SeenGatingRetry(t *testing.T) {
	sched := &capturingScheduler{}
	r := NewRedispatchScheduler(RedispatchSchedulerOptions{Schedule: sched.schedule})
	key := "room|evt"
	for i, want := range DefaultRedispatchBackoffs {
		d, ok := r.Schedule(key, func() {})
		if !ok || d != want {
			t.Fatalf("row 18: attempt %d = (%v,%v), want (%v,true)", i+1, d, ok, want)
		}
	}
	if _, ok := r.Schedule(key, func() {}); ok || !r.GaveUp(key) {
		t.Error("row 18: retries must be capped at 3 per process")
	}
}
