package main

import (
	"context"
	"log"

	pluginhooks "metiq/internal/plugins/hooks"
)

func emitPluginMessageReceived(ctx context.Context, event pluginhooks.MessageReceivedEvent) {
	if controlHookInvoker == nil {
		return
	}
	if _, err := controlHookInvoker.EmitMessageReceived(ctx, event); err != nil {
		log.Printf("message_received hook error channel=%s session=%s err=%v", event.ChannelID, event.SessionID, err)
	}
}

func applyPluginMessageSending(ctx context.Context, event pluginhooks.MessageSendingEvent) (string, bool) {
	if controlHookInvoker == nil {
		return event.Text, true
	}
	result, err := controlHookInvoker.EmitMessageSending(ctx, event)
	if err != nil {
		log.Printf("message_sending hook error channel=%s session=%s err=%v", event.ChannelID, event.SessionID, err)
	}
	if result == nil {
		return event.Text, true
	}
	if result.Reject {
		return result.Reason, false
	}
	return result.Text, true
}

func emitPluginMessageSent(ctx context.Context, event pluginhooks.MessageSentEvent) {
	if controlHookInvoker == nil {
		return
	}
	if _, err := controlHookInvoker.EmitMessageSent(ctx, event); err != nil {
		log.Printf("message_sent hook error channel=%s session=%s err=%v", event.ChannelID, event.SessionID, err)
	}
}
