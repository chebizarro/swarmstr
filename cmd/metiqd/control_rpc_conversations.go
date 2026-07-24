package main

import (
	"context"
	"fmt"
	"time"

	"metiq/internal/gateway/channels"
	conversationspkg "metiq/internal/gateway/conversations"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/plugins/sdk"
)

// Conversation RPC handlers (WS-A/A7.5). Delivery reuses the daemon's channel
// send internals: extension channel accounts send through their live
// sdk.ChannelHandle (media replies via the shared dispatchChannelMediaReply
// helper) and joined NIP-29 group channels send through the channel registry.
// The conversation registry itself is process-local; see the package comment
// in internal/gateway/conversations for the recorded deviations.

const nostrConversationChannel = "nostr"

func (h controlRPCHandler) conversationRegistry() (*conversationspkg.Registry, error) {
	if h.deps.conversations == nil {
		return nil, fmt.Errorf("conversations surface is not available")
	}
	return h.deps.conversations, nil
}

// nostrGroupConversation presents one joined NIP-29 group channel as a
// conversation address.
func nostrGroupConversation(info channels.ChannelInfo) conversationspkg.Conversation {
	return conversationspkg.Conversation{
		ConversationRef: conversationspkg.BuildRef(nostrConversationChannel, "default", conversationspkg.KindGroup, info.ID, ""),
		Channel:         nostrConversationChannel,
		AccountID:       "default",
		Kind:            conversationspkg.KindGroup,
		Target:          info.ID,
		Label:           info.ID,
	}
}

// resolveConversationRef resolves a conversationRef against observed
// conversations first, then against currently joined NIP-29 group channels.
func (h controlRPCHandler) resolveConversationRef(registry *conversationspkg.Registry, ref string) (conversationspkg.Conversation, bool) {
	if record, ok := registry.Resolve(ref); ok {
		return record, true
	}
	if h.deps.channels != nil {
		for _, info := range h.deps.channels.List() {
			candidate := nostrGroupConversation(info)
			if candidate.ConversationRef == ref {
				return candidate, true
			}
		}
	}
	return conversationspkg.Conversation{}, false
}

// deliverConversationMessage sends one outbound message to the conversation's
// transport and reports how it was delivered ("text", "media", "audio").
func (h controlRPCHandler) deliverConversationMessage(ctx context.Context, record conversationspkg.Conversation, message string) (string, error) {
	if record.Channel == nostrConversationChannel {
		if h.deps.channels == nil {
			return "", fmt.Errorf("channel runtime not configured")
		}
		ch, ok := h.deps.channels.Get(record.Target)
		if !ok {
			return "", fmt.Errorf("conversation channel %q is no longer joined", record.Target)
		}
		if err := ch.Send(ctx, message); err != nil {
			return "", err
		}
		emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
			TS:        time.Now().UnixMilli(),
			ChannelID: record.Target,
			Direction: "outbound",
			Text:      message,
		})
		return "text", nil
	}

	if h.deps.channelAccounts == nil {
		return "", fmt.Errorf("channel account runtime not configured")
	}
	conn, running := h.deps.channelAccounts.Get(record.Channel, record.AccountID)
	if !running || conn.Handle == nil {
		return "", fmt.Errorf("channel account %s/%s is not running", record.Channel, record.AccountID)
	}
	return deliverExtensionConversationMessage(ctx, conn.Handle, conn.RawHandle, record, message, h.deps.logBuffer)
}

// deliverExtensionConversationMessage sends one message through a live
// extension channel account handle. Media output markers are routed through
// the shared outbound media dispatch helper (dispatchChannelMediaReply); when
// no media path succeeds the text fallback goes through Handle.Send with the
// conversation target as the channel reply target.
func deliverExtensionConversationMessage(ctx context.Context, handle channels.Channel, rawHandle sdk.ChannelHandle, record conversationspkg.Conversation, message string, logs *runtimeLogBuffer) (string, error) {
	sendCtx := sdk.WithChannelReplyTarget(ctx, record.Target)
	outboundText := message
	if mediaPath, isMedia := extractMediaOutputPath(message); isMedia {
		dispatch := dispatchChannelMediaReply(sendCtx, rawHandle, record.Target, mediaPath)
		if dispatch.err != nil && logs != nil {
			logs.Append("warn", fmt.Sprintf("conversation media send error account=%s target=%s path=%s err=%v", record.AccountID, record.Target, mediaPath, dispatch.err))
		}
		if dispatch.sent {
			emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
				TS:        time.Now().UnixMilli(),
				ChannelID: record.AccountID,
				Direction: "outbound",
				Text:      "[" + dispatch.method + "]",
			})
			return dispatch.method, nil
		}
		outboundText = mediaReplyFallbackText(mediaPath)
	}
	if err := handle.Send(sendCtx, outboundText); err != nil {
		return "", err
	}
	emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
		TS:        time.Now().UnixMilli(),
		ChannelID: record.AccountID,
		Direction: "outbound",
		Text:      outboundText,
	})
	return "text", nil
}

func (h controlRPCHandler) handleConversationsList(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeConversationsListParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.conversationRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	// Joined NIP-29 groups are always live conversation targets; observe them
	// so refs stay resolvable for send/turn.
	if h.deps.channels != nil {
		now := time.Now().UnixMilli()
		for _, info := range h.deps.channels.List() {
			registry.Observe(nostrGroupConversation(info), now)
		}
	}
	out := registry.List(req.Channel, req.Query, req.Limit)
	return nostruntime.ControlRPCResult{Result: map[string]any{"conversations": out}}, true, nil
}

func newConversationMessageID() string {
	return fmt.Sprintf("msg-%d", time.Now().UnixNano())
}

func (h controlRPCHandler) handleConversationsSend(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeConversationsSendParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.conversationRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	opKey := "conversations.send\x00" + req.AgentID + "\x00" + req.OperationID
	identity := conversationspkg.OperationIdentity(req.AgentID, req.SourceSessionKey, req.ConversationRef, req.Message)
	cached, err := registry.BeginOperation(opKey, identity, time.Now())
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("conversation send %s: %w", req.OperationID, err)
	}
	if cached != nil {
		return nostruntime.ControlRPCResult{Result: cached}, true, nil
	}
	record, ok := h.resolveConversationRef(registry, req.ConversationRef)
	if !ok {
		registry.ReleaseOperation(opKey)
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("conversation not found: %s (use conversations.list)", req.ConversationRef)
	}
	if _, err := h.deliverConversationMessage(ctx, record, req.Message); err != nil {
		registry.ReleaseOperation(opKey)
		return nostruntime.ControlRPCResult{}, true, err
	}
	result := map[string]any{
		"status":          "sent",
		"conversationRef": record.ConversationRef,
		"channel":         record.Channel,
		"messageId":       newConversationMessageID(),
	}
	registry.CompleteOperation(opKey, result, time.Now())
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleConversationsTurn(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeConversationsTurnParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.conversationRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	opKey := "conversations.turn\x00" + req.AgentID + "\x00" + req.TurnID
	identity := conversationspkg.OperationIdentity(req.AgentID, req.SourceSessionKey, req.ConversationRef, req.Message, fmt.Sprint(req.TimeoutMS))
	cached, err := registry.BeginOperation(opKey, identity, time.Now())
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("conversation turn %s: %w", req.TurnID, err)
	}
	if cached != nil {
		return nostruntime.ControlRPCResult{Result: cached}, true, nil
	}
	record, ok := h.resolveConversationRef(registry, req.ConversationRef)
	if !ok {
		registry.ReleaseOperation(opKey)
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("conversation not found: %s (use conversations.list)", req.ConversationRef)
	}
	// Correlation exists before recipient-visible I/O; a fast peer may reply
	// while the transport send is still resolving.
	turn, err := registry.RegisterTurn(req.AgentID, req.TurnID, record.ConversationRef, time.Duration(req.TimeoutMS)*time.Millisecond)
	if err != nil {
		registry.ReleaseOperation(opKey)
		return nostruntime.ControlRPCResult{}, true, err
	}
	messageID := newConversationMessageID()
	if _, err := h.deliverConversationMessage(ctx, record, req.Message); err != nil {
		registry.CancelTurn(req.AgentID, req.TurnID)
		registry.ReleaseOperation(opKey)
		return nostruntime.ControlRPCResult{}, true, err
	}
	reply, replied, waitErr := turn.Wait(ctx)
	base := map[string]any{
		"conversationRef": record.ConversationRef,
		"channel":         record.Channel,
		"messageId":       messageID,
		// Metiq deviation: reply correlation is process-local, not durable.
		"correlationPersisted": false,
	}
	var result map[string]any
	switch {
	case replied:
		result = base
		result["status"] = "replied"
		replyPayload := map[string]any{
			"conversationRef": reply.ConversationRef,
			"messageId":       reply.MessageID,
			"text":            reply.Text,
			"timestamp":       reply.Timestamp,
		}
		if reply.ThreadID != "" {
			replyPayload["threadId"] = reply.ThreadID
		}
		if reply.ReplyToID != "" {
			replyPayload["replyToId"] = reply.ReplyToID
		}
		result["reply"] = replyPayload
	case waitErr == nil:
		result = base
		result["status"] = "timeout"
	default:
		if ctx.Err() != nil {
			registry.ReleaseOperation(opKey)
			return nostruntime.ControlRPCResult{}, true, waitErr
		}
		result = base
		result["status"] = "unknown"
		result["error"] = waitErr.Error()
	}
	registry.CompleteOperation(opKey, result, time.Now())
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleConversationsTurnCancel(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeConversationsTurnCancelParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.conversationRegistry()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"cancelled": registry.CancelTurn(req.AgentID, req.TurnID)}}, true, nil
}
