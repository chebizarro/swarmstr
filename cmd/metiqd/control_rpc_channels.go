package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/channels"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	pluginhooks "metiq/internal/plugins/hooks"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleChannelRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	dmBus := h.deps.dmBus
	controlBus := h.deps.controlBus
	chatCancels := h.deps.chatCancels
	channelState := h.deps.channelState
	docsRepo := h.deps.docsRepo
	configState := h.deps.configState
	tools := h.deps.tools

	switch method {
	case methods.MethodChannelsStatus:
		req, err := methods.DecodeChannelsStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := map[string]any{}
		if channelState == nil {
			result["channels"] = []map[string]any{buildNostrChannelStatusRow(map[string]any{}, "channel_state_unavailable")}
		} else {
			status := channelState.Status(dmBus, controlBus, cfg)
			result["channels"] = []map[string]any{buildNostrChannelStatusRow(status, "")}
		}
		if h.deps.channelAccounts != nil {
			result["channel_accounts"] = h.deps.channelAccounts.List()
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodChannelsStart, methods.MethodChannelsStop:
		req, err := methods.DecodeChannelsLifecycleParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.channelAccounts == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel account runtime not configured")
		}
		var snapshot channels.AccountSnapshot
		if method == methods.MethodChannelsStart {
			snapshot, err = h.deps.channelAccounts.Start(ctx, req.Channel, req.AccountID)
		} else {
			snapshot, err = h.deps.channelAccounts.Stop(ctx, req.Channel, req.AccountID)
		}
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := map[string]any{"channel": snapshot.Channel, "account_id": snapshot.AccountID}
		if method == methods.MethodChannelsStart {
			result["started"] = snapshot.Running
		} else {
			result["stopped"] = !snapshot.Running
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodChannelsPairingList:
		req, err := methods.DecodeChannelsPairingListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.channelPairing == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel pairing runtime not configured")
		}
		if req.Channel != "" {
			if _, err := channels.ResolveConfiguredChannelAccount(req.Channel, req.AccountID); err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
		}
		pending, err := h.deps.channelPairing.List(req.Channel, req.AccountID, time.Now())
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		accounts := []map[string]any{}
		for _, account := range channels.ConfiguredChannelAccounts() {
			if req.Channel != "" && account.Provider != req.Channel {
				continue
			}
			if req.AccountID != "" && account.ID != req.AccountID {
				continue
			}
			resolved, resolveErr := channels.ResolveConfiguredChannelAccount(account.Provider, account.ID)
			if resolveErr != nil {
				continue
			}
			dmPolicy, _ := resolved.Config["dm_policy"].(string)
			if !strings.EqualFold(strings.TrimSpace(dmPolicy), "pairing") {
				continue
			}
			accounts = append(accounts, map[string]any{"channel": account.Provider, "account_id": account.ID})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"accounts": accounts, "requests": pending}}, true, nil
	case methods.MethodChannelsPairingApprove, methods.MethodChannelsPairingDismiss:
		req, err := methods.DecodeChannelsPairingResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.channelPairing == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel pairing runtime not configured")
		}
		var pairing channels.PairingRequest
		if method == methods.MethodChannelsPairingApprove {
			if h.deps.approvePairing == nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel pairing persistence not configured")
			}
			pairing, err = h.deps.channelPairing.Approve(req.RequestID, func(observed channels.PairingRequest) error {
				if observed.Channel != req.Channel || observed.AccountID != req.AccountID {
					return fmt.Errorf("pairing request does not belong to channel account")
				}
				return h.deps.approvePairing(ctx, observed)
			})
		} else {
			observed, ok := h.deps.channelPairing.Get(req.RequestID)
			if !ok {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("pending DM access request not found")
			}
			if observed.Channel != req.Channel || observed.AccountID != req.AccountID {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("pairing request does not belong to channel account")
			}
			pairing, err = h.deps.channelPairing.Dismiss(req.RequestID)
		}
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := map[string]any{"request_id": pairing.RequestID, "channel": pairing.Channel, "account_id": pairing.AccountID, "sender_id": pairing.SenderID}
		resolution := "dismissed"
		if method == methods.MethodChannelsPairingApprove {
			result["approved"] = true
			resolution = "approved"
		} else {
			result["dismissed"] = true
		}
		emitControlWSEvent(gatewayws.EventChannelPairingResolved, map[string]any{"ts_ms": time.Now().UnixMilli(), "request_id": pairing.RequestID, "channel": pairing.Channel, "account_id": pairing.AccountID, "sender_id": pairing.SenderID, "resolution": resolution})
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodChannelsLogout:
		req, err := methods.DecodeChannelsLogoutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if channelState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel": req.Channel}}, true, nil
		}
		res, err := channelState.Logout(req.Channel)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: res}, true, nil
	case methods.MethodChannelsJoin:
		req, err := methods.DecodeChannelsJoinParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.channels == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel runtime not configured")
		}
		ch, chErr := channels.NewNIP29GroupChannel(ctx, channels.NIP29GroupChannelOptions{
			GroupAddress: req.GroupAddress,
			Hub:          h.deps.nostrHub,
			Keyer:        h.deps.keyer,
			OnMessage: func(msg channels.InboundMessage) {
				// Same loop-control gate as auto-join so a manually-joined room is
				// gated identically (swarmstr-nfl4). An ad-hoc join has no
				// configured room policy, so defaults apply (requireMention,
				// allowBots="mentions"). Returns the room-scoped session key.
				roomCfg := state.NostrChannelConfig{Kind: state.NostrChannelKindNIP29, GroupAddress: req.GroupAddress}
				decision, admitted := controlNostrLoopControl.gate(msg, roomCfg, configState.Get().DM.AllowFrom)
				if !admitted {
					settleNostrDispatch(msg, true) // deliberate gate drop; terminal
					return
				}
				sessionID := decision.SessionID
				emitPluginMessageReceived(ctx, pluginhooks.MessageReceivedEvent{ChannelID: msg.ChannelID, SenderID: msg.FromPubKey, Text: msg.Text, EventID: msg.EventID, SessionID: sessionID})
				controlServices.emitWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
					TS:        time.Now().UnixMilli(),
					ChannelID: msg.ChannelID,
					GroupID:   msg.GroupID,
					Relay:     msg.Relay,
					Direction: "inbound",
					From:      msg.FromPubKey,
					Text:      msg.Text,
					EventID:   msg.EventID,
				})
				activeAgentID, rt := resolveInboundChannelRuntime("", msg.ChannelID)
				if !controlNostrLoopControl.enqueue(sessionID, msg.EventID, func() {
					delivered := false
					defer func() { settleNostrDispatch(msg, delivered) }()
					turnCtx, release := chatCancels.Begin(sessionID, ctx)
					defer release()
					// Watchdog stage 2: abort a hung turn so it frees the room lane.
					turnCtx, abortCancel := context.WithTimeout(turnCtx, nostrInboundDispatchAbort)
					defer abortCancel()
					sessionStore := h.deps.sessionStore
					filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, rt, tools, turnToolConstraints{})
					scopeCtx := resolveMemoryScopeContext(turnCtx, configState.Get(), docsRepo, sessionStore, sessionID, activeAgentID, "")
					turnCtx = contextWithMemoryScope(turnCtx, scopeCtx)
					result, turnErr := filteredRuntime.ProcessTurn(turnCtx, agent.Turn{
						SessionID:           sessionID,
						UserText:            decision.BodyForAgent,
						Tools:               turnTools,
						Executor:            turnExecutor,
						ContextWindowTokens: maxContextTokensForAgent(configState.Get(), activeAgentID),
						HookInvoker:         controlHookInvoker,
					})
					if turnErr != nil {
						log.Printf("channel agent turn error channel=%s err=%v", msg.ChannelID, turnErr)
						return
					}
					replyText, sendOK := applyPluginMessageSending(turnCtx, pluginhooks.MessageSendingEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: result.Text, SessionID: sessionID, AgentID: activeAgentID})
					if !sendOK {
						delivered = true // deliberate no-send, not a failure
						return
					}
					if decision.EchoSuppress && controlNostrLoopControl.isEchoReply(sessionID, replyText, decision.EchoThreshold) {
						log.Printf("nip29 echo suppressed reply room=%s", sessionID)
						delivered = true // deliberate suppression, not a failure
						return
					}
					controlNostrLoopControl.observeEcho(sessionID, replyText)
					if err := msg.Reply(turnCtx, replyText); err != nil {
						emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: false, Error: err.Error()})
						log.Printf("channel reply error channel=%s err=%v", msg.ChannelID, err)
						return
					}
					emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: true})
					controlServices.emitWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
						TS:        time.Now().UnixMilli(),
						ChannelID: msg.ChannelID,
						GroupID:   msg.GroupID,
						Relay:     msg.Relay,
						Direction: "outbound",
						Text:      replyText,
					})
					delivered = true
				}) {
					// Load-shed: room at capacity; event stays seen, not retried.
					settleNostrDispatch(msg, true)
					return
				}
			},
			OnError: func(err error) {
				log.Printf("nip29 channel error channel=%s err=%v", req.GroupAddress, err)
			},
		})
		if chErr != nil {
			return nostruntime.ControlRPCResult{}, true, chErr
		}
		if err := h.deps.channels.Add(ch); err != nil {
			ch.Close()
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":         true,
			"channel_id": ch.ID(),
			"type":       ch.Type(),
		}}, true, nil
	case methods.MethodChannelsLeave:
		req, err := methods.DecodeChannelsLeaveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.channels == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel runtime not configured")
		}
		if err := h.deps.channels.Remove(req.ChannelID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel_id": req.ChannelID}}, true, nil
	case methods.MethodChannelsList:
		if h.deps.channels == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []any{}}}, true, nil
		}
		list := h.deps.channels.List()
		return nostruntime.ControlRPCResult{Result: map[string]any{"channels": list, "count": len(list)}}, true, nil
	case methods.MethodChannelsSend:
		req, err := methods.DecodeChannelsSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.channels == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel runtime not configured")
		}
		ch, ok := h.deps.channels.Get(req.ChannelID)
		if !ok {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel %q not found; join it first with channels.join", req.ChannelID)
		}
		if err := ch.Send(ctx, req.Text); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		controlServices.emitWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
			TS:        time.Now().UnixMilli(),
			ChannelID: req.ChannelID,
			Direction: "outbound",
			Text:      req.Text,
		})
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel_id": req.ChannelID}}, true, nil
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
