package main

import (
	"log"

	"metiq/internal/gateway/channels"
	nostruntime "metiq/internal/nostr/runtime"
	sessionidentity "metiq/internal/session/identity"
	"metiq/internal/store/state"
)

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
}

// gate runs the loop-control preflight + bot-loop pair guard for an inbound
// group message. It returns the room-scoped session key and whether the turn is
// admitted. Callers own the separate allow_from AUTHORIZATION check; accountAllowFrom
// is passed only so control-command authorization matches (never the bot gate).
func (lc *nostrGroupLoopControl) gate(msg channels.InboundMessage, roomCfg state.NostrChannelConfig, accountAllowFrom []string) (roomKey string, admitted bool) {
	if lc == nil {
		// No loop control configured (e.g. tests): admit with a sender-scoped key.
		return "ch:" + msg.ChannelID + ":" + msg.FromPubKey, true
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
		return "", false
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
			return "", false
		}
	}
	return preflight.RoomKey, true
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
