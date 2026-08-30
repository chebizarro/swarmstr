package main

import (
	"context"
	"fmt"
	"strings"

	acppkg "metiq/internal/acp"
	"metiq/internal/gateway/channels"
	"metiq/internal/store/state"
)

func taskFlowRoomConfig(cfg state.ConfigDoc, roomKey string) (state.NostrChannelConfig, channels.NostrRoomPolicy, bool) {
	for _, channelConfig := range cfg.NostrChannels {
		address := strings.TrimSpace(channelConfig.GroupAddress)
		if address == "" {
			address = strings.TrimSpace(channelConfig.ChannelID)
		}
		if address == "" || channels.NormalizeNostrRoomSessionKey(address) != roomKey {
			continue
		}
		return channelConfig, channels.ResolveNostrRoomPolicy(channelConfig.Config), true
	}
	return state.NostrChannelConfig{}, channels.NostrRoomPolicy{}, false
}

func taskFlowRoomChannel(registry *channels.Registry, roomKey, configuredAddress string) (channels.Channel, bool) {
	if registry == nil {
		return nil, false
	}
	if configuredAddress != "" {
		if channel, ok := registry.Get(configuredAddress); ok {
			return channel, true
		}
	}
	for _, info := range registry.List() {
		if channels.NormalizeNostrRoomSessionKey(info.ID) == roomKey {
			return registry.Get(info.ID)
		}
	}
	return nil, false
}

// routeManagedFlowTransition is the daemon TaskFlow bridge. Every persisted
// mutation enters the typed corpus. Only an explicitly announced mutation routes
// a compact message through the exact same per-room/author/task throttle used by
// generated chat-shadow suppression.
func routeManagedFlowTransition(
	ctx context.Context,
	loopControl *nostrGroupLoopControl,
	cfg state.ConfigDoc,
	registry *channels.Registry,
	author string,
	transition acppkg.FlowTransition,
) (channels.TaskFlowAnnouncementOutcome, error) {
	if loopControl == nil || loopControl.echo == nil {
		return channels.TaskFlowAnnouncementUnavailable, nil
	}
	flow := transition.Current
	summary := channels.TaskTransitionSummary{
		Author: author,
		TaskID: "flow:" + flow.FlowID,
		Status: string(flow.Status),
		Title:  flow.Goal,
	}
	roomConfig, policy, found := taskFlowRoomConfig(cfg, flow.OwnerSessionKey)
	announce := found && policy.TaskFlows && transition.Announce
	var send func(context.Context, string) error
	if announce {
		address := strings.TrimSpace(roomConfig.GroupAddress)
		if address == "" {
			address = strings.TrimSpace(roomConfig.ChannelID)
		}
		channel, ok := taskFlowRoomChannel(registry, flow.OwnerSessionKey, address)
		if ok {
			send = channel.Send
		}
	}
	announcement := fmt.Sprintf("⛭ flow %s %s: %s", flow.FlowID, transition.Action, strings.TrimSpace(flow.Goal))
	return loopControl.echo.RouteTaskFlowTransition(ctx, flow.OwnerSessionKey, summary, announce, announcement, send)
}
