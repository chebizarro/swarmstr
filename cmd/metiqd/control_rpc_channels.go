package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/channels"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
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
		if channelState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []map[string]any{buildNostrChannelStatusRow(map[string]any{}, "channel_state_unavailable")}}}, true, nil
		}
		status := channelState.Status(dmBus, controlBus, cfg)
		return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []map[string]any{buildNostrChannelStatusRow(status, "")}}}, true, nil
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
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel runtime not configured")
		}
		ch, chErr := channels.NewNIP29GroupChannel(ctx, channels.NIP29GroupChannelOptions{
			GroupAddress: req.GroupAddress,
			Hub:          controlHub,
			Keyer:        controlKeyer,
			OnMessage: func(msg channels.InboundMessage) {
				emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
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
				turnCtx, release := chatCancels.Begin(msg.ChannelID, ctx)
				go func() {
					defer release()
					filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, msg.ChannelID, activeAgentID, rt, tools, turnToolConstraints{})
					result, turnErr := filteredRuntime.ProcessTurn(turnCtx, agent.Turn{
						SessionID:           msg.ChannelID,
						UserText:            msg.Text,
						Tools:               turnTools,
						Executor:            turnExecutor,
						ContextWindowTokens: maxContextTokensForAgent(configState.Get(), activeAgentID),
					})
					if turnErr != nil {
						log.Printf("channel agent turn error channel=%s err=%v", msg.ChannelID, turnErr)
						return
					}
					if err := msg.Reply(turnCtx, result.Text); err != nil {
						log.Printf("channel reply error channel=%s err=%v", msg.ChannelID, err)
						return
					}
					emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
						TS:        time.Now().UnixMilli(),
						ChannelID: msg.ChannelID,
						GroupID:   msg.GroupID,
						Relay:     msg.Relay,
						Direction: "outbound",
						Text:      result.Text,
					})
				}()
			},
			OnError: func(err error) {
				log.Printf("nip29 channel error channel=%s err=%v", req.GroupAddress, err)
			},
		})
		if chErr != nil {
			return nostruntime.ControlRPCResult{}, true, chErr
		}
		if err := controlChannels.Add(ch); err != nil {
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
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel runtime not configured")
		}
		if err := controlChannels.Remove(req.ChannelID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel_id": req.ChannelID}}, true, nil
	case methods.MethodChannelsList:
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []any{}}}, true, nil
		}
		list := controlChannels.List()
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
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel runtime not configured")
		}
		ch, ok := controlChannels.Get(req.ChannelID)
		if !ok {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("channel %q not found; join it first with channels.join", req.ChannelID)
		}
		if err := ch.Send(ctx, req.Text); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
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
