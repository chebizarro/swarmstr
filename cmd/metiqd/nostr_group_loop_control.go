package main

import (
	"context"
	"log"
	"path/filepath"
	"time"

	"metiq/internal/gateway/channels"
	metricspkg "metiq/internal/metrics"
	nostruntime "metiq/internal/nostr/runtime"
	sessionidentity "metiq/internal/session/identity"
	"metiq/internal/store/state"
)

// nostrPendingStorePath derives the durable pending-event store path for a
// NIP-29 group, under the daemon's state dir (~/.metiq/channels/<id>/pending.json).
func nostrPendingStorePath(groupAddress string) string {
	base := filepath.Dir(state.DefaultSessionStorePath())
	return filepath.Join(base, "channels", channels.SanitizeChannelPathSegment(groupAddress), "pending.json")
}

// nostrInboundDispatchAbort is watchdog stage 2: a turn silent/running past this
// is aborted so it frees the per-room dispatch lane (openclaw
// inboundDispatchAbortSeconds default).
const nostrInboundDispatchAbort = 1800 * time.Second

// settleNostrDispatch reports a NIP-29 dispatch outcome for delivery-confirmed
// seen-gating (no-op when the transport did not supply a Settle callback).
func settleNostrDispatch(msg channels.InboundMessage, deliveredOK bool) {
	if msg.Settle != nil {
		msg.Settle(deliveredOK)
	}
}

// nostrGroupLoopControl bundles the shared NIP-29 loop-control components so the
// SAME gating (allowBots / mention / backfill preflight + bot-loop pair guard),
// per-room dispatch queue, and room-scoped record-first session identity apply
// regardless of how a room was joined (startup auto-join OR the channels.join
// RPC). The bot verdict is loop-control ONLY and never affects allow_from /
// command authorization.
type nostrGroupLoopControl struct {
	ownPubkey               string
	peerIndex               *nostruntime.PeerAgentIndex
	guard                   *channels.BotLoopProtection
	queue                   *channels.RoomDispatchQueue
	establisher             *sessionidentity.Establisher
	echo                    *channels.EchoSuppressor
	shouldReplyCapabilities []string
	// responderDirectory snapshots the NIP-51 fleet directory (R2 responder
	// election): pubkey + advertised kind:30317 capability terms per member.
	// Nil/empty leaves the election inert.
	responderDirectory func() []channels.ResponderDirectoryEntry
	// isMutedPeer excludes muted fleet members from taking election turns.
	isMutedPeer func(pubkey string) bool
	// takeovers arms the R2 successor takeover timers (off-dispatch: no queue
	// slot is held while waiting for the elected responder).
	takeovers *channels.TakeoverCoordinator[nostrResponderTakeoverPayload]
	// ledgerRecorder keeps the bounded per-room event window the R5 progress
	// ledger reviews (recorded for EVERY inbound room message, admitted or
	// dropped — unanswered ambient mentions are exactly the traffic the gate
	// drops). Nil leaves the ledger inert.
	ledgerRecorder *channels.ProgressLedgerRecorder
}

// recordProgressLedgerEvent records one inbound room event into the R5 review
// window. Takeover redeliveries are skipped (the original delivery recorded).
func (lc *nostrGroupLoopControl) recordProgressLedgerEvent(roomKey string, msg channels.InboundMessage) {
	if lc == nil || lc.ledgerRecorder == nil || roomKey == "" || msg.ResponderTakeover {
		return
	}
	lc.ledgerRecorder.Record(roomKey, channels.ProgressLedgerEvent{
		EventID:          msg.EventID,
		Author:           msg.FromPubKey,
		CreatedAt:        time.Unix(msg.CreatedAt, 0).UTC(),
		Content:          msg.Text,
		MentionedPubkeys: append([]string(nil), msg.Meta.MentionedPubkeys...),
		ReplyToEventID:   msg.Meta.ReplyToEventID,
	})
}

// nostrResponderTakeoverPayload is the armed takeover context: the original
// inbound message and the dispatch closure that re-verifies it under CURRENT
// config when the takeover fires.
type nostrResponderTakeoverPayload struct {
	msg     channels.InboundMessage
	deliver func(channels.InboundMessage)
}

// nostrResponderClaimTimeout bounds the best-effort 🙋 claim reaction publish.
const nostrResponderClaimTimeout = 30 * time.Second

// newNostrResponderTakeoverCoordinator builds the shared R2 takeover
// coordinator. When a takeover fires, the contested event is re-delivered
// through the normal dispatch path with the ResponderTakeover marker set so
// the gate re-verifies it under CURRENT config (mention/commands/authorization
// checks run again) before the claim + reply turn.
func newNostrResponderTakeoverCoordinator(
	ctx context.Context,
	selfPubkey string,
) (*channels.TakeoverCoordinator[nostrResponderTakeoverPayload], error) {
	return channels.NewTakeoverCoordinator(channels.TakeoverCoordinatorOptions[nostrResponderTakeoverPayload]{
		SelfPubkey: selfPubkey,
		OnTakeover: func(pending channels.TakeoverPending[nostrResponderTakeoverPayload]) {
			if ctx.Err() != nil || pending.Payload.deliver == nil {
				return
			}
			redelivery := pending.Payload.msg
			redelivery.ResponderTakeover = true
			// The original dispatch already settled (deliberate defer); the
			// takeover redelivery must not settle it again.
			redelivery.Settle = nil
			pending.Payload.deliver(redelivery)
		},
		OnError: func(err error) {
			log.Printf("nip29 responder takeover failed: %v", err)
		},
	})
}

// armResponderTakeover arms the successor takeover timer for a deferred event.
// No-op unless this agent is the immediate successor in the election order.
func (lc *nostrGroupLoopControl) armResponderTakeover(decision nostrGateDecision, msg channels.InboundMessage, deliver func(channels.InboundMessage)) {
	if lc == nil || lc.takeovers == nil || deliver == nil {
		return
	}
	election := decision.ResponderElection
	if election == nil || election.Role != channels.ResponderRoleSuccessor {
		return
	}
	if lc.takeovers.Schedule(channels.TakeoverPending[nostrResponderTakeoverPayload]{
		RoomKey:       decision.ResponderRoomKey,
		EventID:       msg.EventID,
		ElectedPubkey: election.ElectedPubkey,
		Payload:       nostrResponderTakeoverPayload{msg: msg, deliver: deliver},
	}, election.Takeover) {
		log.Printf("nip29 responder takeover armed room=%s event=%s elected=%s window=%s",
			decision.ResponderRoomKey, msg.EventID, election.ElectedPubkey, election.Takeover)
	}
}

// postResponderClaim posts the visible 🙋 claim BEFORE answering a takeover so
// later successors stand down. Best-effort: a failed claim never blocks the
// answer (mirrors the reference takeover path).
func (lc *nostrGroupLoopControl) postResponderClaim(msg channels.InboundMessage) {
	if msg.React == nil {
		log.Printf("nip29 responder claim skipped (channel has no reaction path) event=%s", msg.EventID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), nostrResponderClaimTimeout)
	defer cancel()
	if err := msg.React(ctx, channels.NostrResponderClaimEmoji); err != nil {
		log.Printf("nip29 responder claim reaction failed (continuing): %v", err)
	}
}

// observeReaction feeds a room reaction into the takeover coordinator: a ✅
// ack from the elected responder or a 🙋 claim from an earlier successor
// stands this agent's pending takeover down.
func (lc *nostrGroupLoopControl) observeReaction(reaction channels.InboundReaction) {
	if lc == nil || lc.takeovers == nil {
		return
	}
	lc.takeovers.ObserveReaction(channels.TakeoverReactionFacts{
		RoomKey:       channels.NormalizeNostrRoomSessionKey(reaction.ChannelID),
		SenderPubkey:  reaction.FromPubKey,
		TargetEventID: reaction.TargetEventID,
	})
}

// observeEcho records room text (peer or own) into the echo ring.
func (lc *nostrGroupLoopControl) observeEcho(roomKey, text string) {
	if lc != nil && lc.echo != nil {
		lc.echo.Observe(roomKey, text)
	}
}

// isEchoReply reports whether a candidate reply substantially restates recent
// room traffic (only meaningful when the room opted into echo suppression).
func (lc *nostrGroupLoopControl) isEchoReply(roomKey, text string, threshold float64) bool {
	if lc == nil || lc.echo == nil {
		return false
	}
	return lc.echo.IsEcho(roomKey, text, threshold)
}

// observeTaskTransition feeds one validated kind-30900 fleet-task transition
// into the suppressor's task corpus (R6 chat-shadow suppression).
func (lc *nostrGroupLoopControl) observeTaskTransition(author, taskID, status, title string) {
	if lc != nil && lc.echo != nil {
		lc.echo.ObserveTaskTransition(channels.TaskTransitionSummary{Author: author, TaskID: taskID, Status: status, Title: title})
	}
}

// checkTaskEcho reports whether a candidate reply is the chat shadow of a
// recent task transition WE authored (same-author rule: lc.ownPubkey signs
// both our kind-30900 events and our chat messages).
func (lc *nostrGroupLoopControl) checkTaskEcho(roomKey, text string, threshold float64) channels.TaskEchoVerdict {
	if lc == nil || lc.echo == nil {
		return channels.TaskEchoVerdict{}
	}
	return lc.echo.CheckTaskEcho(roomKey, lc.ownPubkey, text, threshold)
}

// nostrGateDecision is the admitted-turn payload from gate.
type nostrGateDecision struct {
	// SessionID is the room-scoped transcript key.
	SessionID string
	// BodyForAgent is the message wrapped per ambientPolicy (scan/respond) and
	// prefixed with a trusted note when the sender is a definitive bot.
	BodyForAgent string
	// SenderIsBot is the definitive known-bot verdict (loop-control info only).
	SenderIsBot bool
	// EchoSuppress and EchoThreshold carry the room's opt-in echo policy so the
	// turn body can drop a reply that restates recent traffic before sending.
	EchoSuppress  bool
	EchoThreshold float64
	// TaskEchoSuppress and TaskEchoThreshold carry the room's task chat-shadow
	// policy (R6): drop a reply restating a recent same-author kind-30900
	// transition, allowing one compact throttled announcement.
	TaskEchoSuppress  bool
	TaskEchoThreshold float64
	// InboundEventKind is the classified inbound kind ("user_request" |
	// "room_event"); a generated-failure reply is suppressed for room_event
	// (ambient) traffic so the agent does not spew errors into rooms it was not
	// directly addressed in.
	InboundEventKind string
	// ResponderElection carries the R2 election outcome when the turn was
	// deferred to another agent (set only on non-admitted decisions so the
	// caller can arm the successor takeover timer). Nil = election bypassed
	// or this agent elected.
	ResponderElection *channels.NostrResponderElectionDecision
	// ResponderRoomKey is the room session key the takeover is armed under.
	ResponderRoomKey string
}

// gate runs the loop-control preflight + bot-loop pair guard for an inbound
// group message. It returns the room-scoped session key + agent-ready body and
// whether the turn is admitted. Callers own the separate allow_from
// AUTHORIZATION check; accountAllowFrom is passed only so control-command
// authorization matches (never the bot gate).
func (lc *nostrGroupLoopControl) gate(msg channels.InboundMessage, roomCfg state.NostrChannelConfig, accountAllowFrom []string) (nostrGateDecision, bool) {
	if lc == nil {
		// No loop control configured (e.g. tests): admit with a sender-scoped key
		// and the raw body.
		return nostrGateDecision{SessionID: "ch:" + msg.ChannelID + ":" + msg.FromPubKey, BodyForAgent: msg.Text}, true
	}

	verdict := nostruntime.VerdictUnknown
	if lc.peerIndex != nil {
		verdict = lc.peerIndex.IsPeerAgent(msg.FromPubKey)
	}
	knownBot := verdict == nostruntime.VerdictBot
	roomPolicy := channels.ResolveNostrRoomPolicy(roomCfg.Config)
	preflight := channels.ResolveNostrGroupPreflight(channels.NostrPreflightInput{
		BotPubkey:               lc.ownPubkey,
		GroupID:                 msg.GroupID,
		GroupAddress:            roomCfg.GroupAddress,
		SenderPubkey:            msg.FromPubKey,
		Text:                    msg.Text,
		AgentID:                 roomCfg.AgentID,
		Meta:                    msg.Meta,
		RoomAllowFrom:           roomCfg.AllowFrom,
		AccountAllowFrom:        accountAllowFrom,
		RequireMention:          roomPolicy.RequireMention,
		AllowTextCommands:       true,
		UnmentionedRoomEvent:    roomPolicy.UnmentionedRoomEvent,
		AllowBots:               roomPolicy.AllowBots,
		SenderIsPeer:            knownBot,
		SenderIsKnownBot:        knownBot,
		ShouldReplyGate:         roomPolicy.ShouldReplyGate,
		ShouldReplyAliases:      []string{"metiq"},
		ShouldReplyCapabilities: lc.shouldReplyCapabilities,
	})
	// R2: every live room message doubles as a takeover observation — the
	// elected responder answering (or anyone replying in the contested event's
	// thread) stands this agent's pending claim down, even when this message
	// is itself dropped below.
	if lc.takeovers != nil && !msg.ResponderTakeover {
		lc.takeovers.ObserveRoomMessage(channels.TakeoverRoomMessageFacts{
			RoomKey:           preflight.RoomKey,
			SenderPubkey:      msg.FromPubKey,
			ReplyToEventID:    msg.Meta.ReplyToEventID,
			ThreadRootEventID: msg.Meta.ThreadRootEventID,
		})
	}
	// R5: every live inbound room message lands in the progress-ledger window
	// (even ones the gate drops below — those are the unanswered mentions the
	// moderator review is for).
	lc.recordProgressLedgerEvent(preflight.RoomKey, msg)
	if decision := preflight.ShouldReplyGateDecision; decision != nil {
		metricspkg.RecordShouldReplyGate(string(decision.Outcome), string(decision.Reason))
		log.Printf("nip29 should-reply gate from=%s channel=%s outcome=%s reason=%s score=%d",
			msg.FromPubKey, msg.ChannelID, decision.Outcome, decision.Reason, decision.Score)
	}
	if preflight.ShouldDrop {
		log.Printf("nip29 preflight drop from=%s channel=%s reason=%s", msg.FromPubKey, msg.ChannelID, preflight.DropReason)
		return nostrGateDecision{}, false
	}
	// R2: deterministic single-responder election over the NIP-51 fleet
	// directory for admitted ambient traffic. Nil = bypass (explicit mentions,
	// replies-to-bot, commands, no fleet directory, or no other
	// capability-advertising member: the message is fully this agent's).
	var responderDirectory []channels.ResponderDirectoryEntry
	if lc.responderDirectory != nil {
		responderDirectory = lc.responderDirectory()
	}
	responderElection := channels.EvaluateNostrResponderElection(channels.NostrResponderElectionParams{
		Policy:           roomPolicy,
		SelfPubkey:       lc.ownPubkey,
		SelfCapabilities: lc.shouldReplyCapabilities,
		EventID:          msg.EventID,
		Text:             msg.Text,
		SenderPubkey:     msg.FromPubKey,
		Gate:             preflight.ShouldReplyGateDecision,
		Directory:        responderDirectory,
		IsMuted:          lc.isMutedPeer,
	})
	if msg.ResponderTakeover {
		// Takeover re-verification under CURRENT config: the event must still
		// be admissible (checked above), and this agent must still be in the
		// election order (any position may claim after the window).
		if responderElection != nil && responderElection.SelfIndex < 0 {
			return nostrGateDecision{}, false
		}
		metricspkg.RecordResponderElection("takeover")
		log.Printf("nip29 responder takeover room=%s event=%s: elected responder stayed silent past the window",
			preflight.RoomKey, msg.EventID)
	} else if responderElection != nil {
		log.Printf("nip29 responder election channel=%s room=%s event=%s role=%s elected=%s eligible=%d",
			msg.ChannelID, preflight.RoomKey, msg.EventID, responderElection.Role,
			responderElection.ElectedPubkey, responderElection.EligibleCount)
		if responderElection.Role != channels.ResponderRoleElected {
			// Only the elected agent takes the turn; the immediate successor
			// arms a takeover timer (caller-side, off-dispatch); everyone else
			// defers for this event.
			metricspkg.RecordResponderElection("deferred")
			return nostrGateDecision{
				ResponderElection: responderElection,
				ResponderRoomKey:  preflight.RoomKey,
			}, false
		}
		metricspkg.RecordResponderElection("won")
	}
	// A takeover redelivery was already recorded by the pair guard at its
	// original arrival; never double-record it.
	if knownBot && lc.guard != nil && !msg.ResponderTakeover {
		if res := lc.guard.RecordAndCheck(channels.BotLoopProtectionFacts{
			ScopeID:        lc.ownPubkey,
			ConversationID: preflight.RoomKey,
			SenderID:       msg.FromPubKey,
			ReceiverID:     lc.ownPubkey,
			Config:         roomPolicy.PairLoop,
			DefaultEnabled: true,
			EventID:        msg.EventID,
		}); res.Suppressed {
			log.Printf("nip29 bot-loop suppressed from=%s channel=%s room=%s", msg.FromPubKey, msg.ChannelID, preflight.RoomKey)
			return nostrGateDecision{}, false
		}
	}

	// Present the message per ambientPolicy (scan wraps non-mention bodies with
	// cautionary guidance; respond passes raw) and surface a trusted bot marker
	// so the agent can treat automated senders differently.
	body := channels.BuildNostrGroupBodyForAgent(msg.Text, preflight, roomPolicy.AmbientRespond)
	if knownBot {
		body = channels.NostrSenderIsBotNote + "\n\n" + body
	}
	// Feed the inbound message into the echo ring so a reply that merely
	// restates it can be suppressed (opt-in per room).
	if roomPolicy.EchoSuppression {
		lc.observeEcho(preflight.RoomKey, msg.Text)
	}
	return nostrGateDecision{
		SessionID:         preflight.RoomKey,
		BodyForAgent:      body,
		SenderIsBot:       knownBot,
		EchoSuppress:      roomPolicy.EchoSuppression,
		EchoThreshold:     roomPolicy.EchoThreshold,
		TaskEchoSuppress:  roomPolicy.TaskEchoSuppression,
		TaskEchoThreshold: roomPolicy.TaskEchoThreshold,
		InboundEventKind:  preflight.InboundEventKind,
	}, true
}

// enqueue serializes run for roomKey through the per-room dispatch queue and
// establishes the session identity record-first inside the serialized run.
// Returns false when the room is at capacity (load-shed). When no loop control
// is configured, it runs synchronously in a new goroutine (test/degraded path).
func (lc *nostrGroupLoopControl) enqueue(roomKey, eventID string, run func()) bool {
	if lc == nil || lc.queue == nil {
		go run()
		return true
	}
	return lc.queue.Enqueue(roomKey, 9, func() {
		if lc.establisher != nil {
			if _, _, err := lc.establisher.Establish(roomKey); err != nil {
				log.Printf("nip29 session identity error room=%s err=%v", roomKey, err)
			}
		}
		run()
	})
}
