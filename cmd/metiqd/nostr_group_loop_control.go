package main

import (
	"log"
	"time"

	"metiq/internal/gateway/channels"
	nostruntime "metiq/internal/nostr/runtime"
	sessionidentity "metiq/internal/session/identity"
	"metiq/internal/store/state"
)

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
	ownPubkey   string
	peerIndex   *nostruntime.PeerAgentIndex
	guard       *channels.BotLoopProtection
	queue       *channels.RoomDispatchQueue
	establisher *sessionidentity.Establisher
	echo        *channels.EchoSuppressor
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
		BotPubkey:            lc.ownPubkey,
		GroupID:              msg.GroupID,
		GroupAddress:         roomCfg.GroupAddress,
		SenderPubkey:         msg.FromPubKey,
		Text:                 msg.Text,
		AgentID:              roomCfg.AgentID,
		Meta:                 msg.Meta,
		RoomAllowFrom:        roomCfg.AllowFrom,
		AccountAllowFrom:     accountAllowFrom,
		RequireMention:       roomPolicy.RequireMention,
		AllowTextCommands:    true,
		UnmentionedRoomEvent: roomPolicy.UnmentionedRoomEvent,
		AllowBots:            roomPolicy.AllowBots,
		SenderIsPeer:         knownBot,
		SenderIsKnownBot:     knownBot,
	})
	if preflight.ShouldDrop {
		log.Printf("nip29 preflight drop from=%s channel=%s reason=%s", msg.FromPubKey, msg.ChannelID, preflight.DropReason)
		return nostrGateDecision{}, false
	}
	if knownBot && lc.guard != nil {
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
		SessionID:     preflight.RoomKey,
		BodyForAgent:  body,
		SenderIsBot:   knownBot,
		EchoSuppress:  roomPolicy.EchoSuppression,
		EchoThreshold: roomPolicy.EchoThreshold,
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
