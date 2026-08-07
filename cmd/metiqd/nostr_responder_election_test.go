package main

// nostr_responder_election_test.go — R2 wiring tests (reference: openclaw-nostr
// ocn-myl): the loop-control gate runs the deterministic responder election
// AFTER the should-reply gate on admitted ambient traffic, only the elected
// agent takes the turn, the successor's armed takeover re-delivers the event
// with a visible 🙋 claim, and mentions / off-switch bypass the election.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/gateway/channels"
	"metiq/internal/store/state"
)

// Mention detection normalizes against curve-valid pubkeys, so the fleet uses
// real generated keys; event ids stay synthetic.
var (
	electAgentSelf  = nostr.Generate().Public().Hex()
	electAgentPeer  = nostr.Generate().Public().Hex()
	electHumanAsker = nostr.Generate().Public().Hex()
	electEventID    = strings.Repeat("e", 64)
)

func electionTestLoopControl(t *testing.T, self string, directory []channels.ResponderDirectoryEntry) *nostrGroupLoopControl {
	t.Helper()
	lc := &nostrGroupLoopControl{
		ownPubkey:               self,
		shouldReplyCapabilities: []string{"deploy"},
		responderDirectory:      func() []channels.ResponderDirectoryEntry { return directory },
	}
	takeovers, err := channels.NewTakeoverCoordinator(channels.TakeoverCoordinatorOptions[nostrResponderTakeoverPayload]{
		SelfPubkey: self,
		OnTakeover: func(pending channels.TakeoverPending[nostrResponderTakeoverPayload]) {
			redelivery := pending.Payload.msg
			redelivery.ResponderTakeover = true
			redelivery.Settle = nil
			pending.Payload.deliver(redelivery)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	lc.takeovers = takeovers
	t.Cleanup(takeovers.Close)
	return lc
}

func electionTestMsg(eventID string) channels.InboundMessage {
	return channels.InboundMessage{
		ChannelID:  "relay.test'fleet-ops",
		GroupID:    "fleet-ops",
		FromPubKey: electHumanAsker,
		Text:       "can someone deploy the gateway?",
		EventID:    eventID,
		Meta:       channels.NostrInboundMeta{EventID: eventID, ThreadRootEventID: eventID, DeliveryPhase: "live"},
	}
}

func electionTestRoomCfg(config map[string]any) state.NostrChannelConfig {
	return state.NostrChannelConfig{
		Kind:         state.NostrChannelKindNIP29,
		GroupAddress: "relay.test'fleet-ops",
		AllowFrom:    []string{"*"},
		Config:       config,
	}
}

// The election runs after the should-reply gate: on a capability-matched
// ambient question, exactly one instance is admitted (identical decision on
// every instance), the successor defers with an armed takeover, and mention
// traffic bypasses the election entirely.
func TestResponderElectionGateWiring(t *testing.T) {
	directory := []channels.ResponderDirectoryEntry{
		{Pubkey: electAgentSelf, Capabilities: []string{"deploy", "gateway"}},
		{Pubkey: electAgentPeer, Capabilities: []string{"deploy"}},
	}
	cfg := electionTestRoomCfg(map[string]any{"requireMention": false})

	// Self is the stronger capability match → elected → admitted.
	lcElected := electionTestLoopControl(t, electAgentSelf, directory)
	if _, admitted := lcElected.gate(electionTestMsg(electEventID), cfg, nil); !admitted {
		t.Fatal("elected agent must take the turn")
	}

	// The weaker match defers as successor and reports the election outcome.
	lcSuccessor := electionTestLoopControl(t, electAgentPeer, directory)
	decision, admitted := lcSuccessor.gate(electionTestMsg(electEventID), cfg, nil)
	if admitted {
		t.Fatal("successor must defer the turn")
	}
	if decision.ResponderElection == nil || decision.ResponderElection.Role != channels.ResponderRoleSuccessor {
		t.Fatalf("successor decision missing election outcome: %+v", decision)
	}
	if decision.ResponderRoomKey != "nostr:room:relay.test'fleet-ops" {
		t.Fatalf("room key = %q", decision.ResponderRoomKey)
	}

	// Explicit mention bypasses the election: the deferring successor answers.
	mentioned := electionTestMsg(strings.Repeat("f", 64))
	mentioned.Meta.MentionedPubkeys = []string{electAgentPeer}
	if _, admitted := lcSuccessor.gate(mentioned, cfg, nil); !admitted {
		t.Fatal("explicit mention must bypass the election")
	}

	// Room off-switch preserves legacy dispatch (every capable agent answers).
	offCfg := electionTestRoomCfg(map[string]any{"requireMention": false, "responderElection": false})
	if _, admitted := lcSuccessor.gate(electionTestMsg(strings.Repeat("1", 64)), offCfg, nil); !admitted {
		t.Fatal("responderElection=false must preserve legacy admission")
	}
}

// The armed takeover re-delivers the contested event with the takeover marker;
// the gate then admits this agent even though it is not the elected responder,
// and the claim reaction goes out through the message's reaction path. An
// observed reaction on the contested event stands the pending takeover down.
func TestResponderElectionTakeoverRedelivery(t *testing.T) {
	directory := []channels.ResponderDirectoryEntry{
		{Pubkey: electAgentSelf, Capabilities: []string{"deploy", "gateway"}},
		{Pubkey: electAgentPeer, Capabilities: []string{"deploy"}},
	}
	cfg := electionTestRoomCfg(map[string]any{"requireMention": false})

	lc := &nostrGroupLoopControl{
		ownPubkey:               electAgentPeer,
		shouldReplyCapabilities: []string{"deploy"},
		responderDirectory:      func() []channels.ResponderDirectoryEntry { return directory },
	}
	var timerFns []func()
	takeovers, err := channels.NewTakeoverCoordinator(channels.TakeoverCoordinatorOptions[nostrResponderTakeoverPayload]{
		SelfPubkey: electAgentPeer,
		// Production redelivery shape (newNostrResponderTakeoverCoordinator).
		OnTakeover: func(pending channels.TakeoverPending[nostrResponderTakeoverPayload]) {
			redelivery := pending.Payload.msg
			redelivery.ResponderTakeover = true
			redelivery.Settle = nil
			pending.Payload.deliver(redelivery)
		},
		SetTimer:   func(fn func(), _ time.Duration) any { timerFns = append(timerFns, fn); return nil },
		ClearTimer: func(any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	lc.takeovers = takeovers
	defer takeovers.Close()

	var mu sync.Mutex
	var redelivered []channels.InboundMessage
	var reactions []string
	deliver := func(msg channels.InboundMessage) {
		// The production handler re-runs the gate under CURRENT config and
		// posts the claim before dispatching the turn — mirror that here.
		decision, admitted := lc.gate(msg, cfg, nil)
		if !admitted {
			t.Errorf("takeover redelivery must be admitted, decision=%+v", decision)
			return
		}
		if msg.ResponderTakeover {
			lc.postResponderClaim(msg)
		}
		mu.Lock()
		redelivered = append(redelivered, msg)
		mu.Unlock()
	}

	msg := electionTestMsg(electEventID)
	msg.React = func(_ context.Context, emoji string) error {
		mu.Lock()
		reactions = append(reactions, emoji)
		mu.Unlock()
		return nil
	}
	msg.Settle = func(bool) {}
	decision, admitted := lc.gate(msg, cfg, nil)
	if admitted {
		t.Fatal("successor must defer first")
	}
	lc.armResponderTakeover(decision, msg, deliver)
	if takeovers.Size() != 1 || len(timerFns) != 1 {
		t.Fatalf("takeover must be armed once (size=%d timers=%d)", takeovers.Size(), len(timerFns))
	}

	// The elected responder stays silent past the window: the timer fires and
	// the successor claims + answers.
	timerFns[0]()
	if len(redelivered) != 1 {
		t.Fatalf("redeliveries = %d, want 1", len(redelivered))
	}
	if !redelivered[0].ResponderTakeover || redelivered[0].Settle != nil {
		t.Fatalf("redelivery must carry the takeover marker and no Settle: %+v", redelivered[0])
	}
	if len(reactions) != 1 || reactions[0] != channels.NostrResponderClaimEmoji {
		t.Fatalf("claim reactions = %v, want [🙋]", reactions)
	}

	// Cancel path: a reaction on a NEW contested event (e.g. an earlier
	// successor's claim) stands the armed takeover down before it fires.
	second := electionTestMsg(strings.Repeat("2", 64))
	secondDecision, secondAdmitted := lc.gate(second, cfg, nil)
	if secondAdmitted {
		t.Fatal("successor must defer the second event")
	}
	lc.armResponderTakeover(secondDecision, second, deliver)
	if takeovers.Size() != 1 {
		t.Fatal("second takeover must be armed")
	}
	lc.observeReaction(channels.InboundReaction{
		ChannelID:     "relay.test'fleet-ops",
		FromPubKey:    strings.Repeat("c", 64),
		TargetEventID: second.EventID,
	})
	if takeovers.Size() != 0 {
		t.Fatal("an observed reaction on the contested event must cancel the takeover")
	}

	// The elected responder answering in the room also cancels (via the gate's
	// per-message observation).
	third := electionTestMsg(strings.Repeat("3", 64))
	thirdDecision, _ := lc.gate(third, cfg, nil)
	lc.armResponderTakeover(thirdDecision, third, deliver)
	electedReply := electionTestMsg(strings.Repeat("4", 64))
	electedReply.FromPubKey = electAgentSelf
	lc.gate(electedReply, cfg, nil)
	if takeovers.Size() != 0 {
		t.Fatal("the elected responder's room message must cancel the takeover")
	}
}
