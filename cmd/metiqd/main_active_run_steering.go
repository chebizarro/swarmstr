package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
)

type activeRunSteeringInput struct {
	SessionID    string
	Text         string
	EventID      string
	SenderID     string
	ChannelID    string
	ThreadID     string
	Source       string
	AgentID      string
	ToolProfile  string
	EnabledTools []string
	CreatedAt    int64
	Priority     autoreply.SteeringPriority
}

func enqueueActiveRunSteering(mailboxes *autoreply.SteeringMailboxRegistry, settings queueRuntimeSettings, input activeRunSteeringInput) bool {
	if mailboxes == nil {
		return false
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return false
	}
	mailbox := mailboxes.Get(sessionID)
	mailbox.Configure(settings.Cap, settings.Drop)
	msg := autoreply.SteeringMessage{
		Text:         input.Text,
		EventID:      strings.TrimSpace(input.EventID),
		SenderID:     strings.TrimSpace(input.SenderID),
		ChannelID:    strings.TrimSpace(input.ChannelID),
		ThreadID:     strings.TrimSpace(input.ThreadID),
		AgentID:      strings.TrimSpace(input.AgentID),
		ToolProfile:  strings.TrimSpace(input.ToolProfile),
		EnabledTools: append([]string(nil), input.EnabledTools...),
		CreatedAt:    input.CreatedAt,
		Source:       strings.TrimSpace(input.Source),
		Priority:     input.Priority,
		SummaryLine:  steeringSummaryLine(input),
	}
	if msg.Priority == "" {
		msg.Priority = autoreply.SteeringPriorityNormal
	}
	accepted := mailbox.Enqueue(msg)
	if accepted {
		log.Printf("active-run steering enqueued: session=%s source=%s priority=%s mailbox_len=%d", sessionID, msg.Source, msg.Priority, mailbox.Len())
	} else {
		log.Printf("active-run steering not enqueued: session=%s source=%s priority=%s", sessionID, msg.Source, msg.Priority)
	}
	return accepted
}

func handleBusySteer(mailboxes *autoreply.SteeringMailboxRegistry, _ *autoreply.SessionQueue, settings queueRuntimeSettings, input activeRunSteeringInput) bool {
	return enqueueActiveRunSteering(mailboxes, settings, input)
}

func handleBusyInterrupt(
	chatCancels *chatAbortRegistry,
	activeTools *activeToolRegistry,
	mailboxes *autoreply.SteeringMailboxRegistry,
	q *autoreply.SessionQueue,
	settings queueRuntimeSettings,
	input activeRunSteeringInput,
) (deferred bool) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return false
	}
	if activeTools == nil || activeTools.AllInterruptible(sessionID) {
		if chatCancels != nil {
			chatCancels.AbortWithCause(sessionID, agent.ErrTurnInterrupted)
		}
		clearTransientSessionSteering(mailboxes, sessionID)
		if q != nil {
			_ = q.Dequeue()
		}
		log.Printf("busy interrupt aborted active turn: session=%s", sessionID)
		return false
	}
	clearTransientSessionSteering(mailboxes, sessionID)
	if q != nil {
		_ = q.Dequeue()
	}
	input.Priority = autoreply.SteeringPriorityUrgent
	accepted := enqueueActiveRunSteering(mailboxes, settings, input)
	log.Printf("busy interrupt deferred by blocking tool: session=%s accepted=%t", sessionID, accepted)
	return true
}

func toolLifecycleSinkWithActiveTools(activeTools *activeToolRegistry, next agent.ToolLifecycleSink) agent.ToolLifecycleSink {
	if activeTools == nil {
		return next
	}
	return func(evt agent.ToolLifecycleEvent) {
		activeTools.Record(evt)
		if next != nil {
			next(evt)
		}
	}
}

func makeActiveRunSteeringDrain(mailboxes *autoreply.SteeringMailboxRegistry, sessionID string, onDrain func([]autoreply.SteeringMessage)) func(context.Context) []agent.InjectedUserInput {
	sessionID = strings.TrimSpace(sessionID)
	if mailboxes == nil || sessionID == "" {
		return nil
	}
	return func(context.Context) []agent.InjectedUserInput {
		mailbox := mailboxes.GetIfExists(sessionID)
		if mailbox == nil {
			return nil
		}
		items := mailbox.Drain()
		if len(items) == 0 {
			return nil
		}
		if onDrain != nil {
			onDrain(append([]autoreply.SteeringMessage(nil), items...))
		}
		out := make([]agent.InjectedUserInput, 0, len(items))
		for _, item := range items {
			out = append(out, agent.InjectedUserInput{Content: formatSteeringForModel(item)})
		}
		log.Printf("active-run steering drained: session=%s items=%d", sessionID, len(out))
		return out
	}
}

func drainSteeringAsPending(mailboxes *autoreply.SteeringMailboxRegistry, sessionID string) []autoreply.PendingTurn {
	if mailboxes == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	mailbox := mailboxes.GetIfExists(sessionID)
	if mailbox == nil {
		return nil
	}
	items := mailbox.Drain()
	if len(items) == 0 {
		return nil
	}
	pending := make([]autoreply.PendingTurn, 0, len(items))
	for _, item := range items {
		pending = append(pending, pendingTurnFromSteering(item))
	}
	log.Printf("active-run steering residual fallback: session=%s items=%d", strings.TrimSpace(sessionID), len(pending))
	return pending
}

func pendingTurnFromSteering(item autoreply.SteeringMessage) autoreply.PendingTurn {
	return autoreply.PendingTurn{
		Text:         item.Text,
		EventID:      item.EventID,
		SenderID:     item.SenderID,
		AgentID:      item.AgentID,
		ToolProfile:  item.ToolProfile,
		EnabledTools: append([]string(nil), item.EnabledTools...),
		CreatedAt:    item.CreatedAt,
		SummaryLine:  item.SummaryLine,
	}
}

func enqueuePendingTurns(q *autoreply.SessionQueue, pending []autoreply.PendingTurn) {
	if q == nil {
		return
	}
	for _, pt := range pending {
		q.Enqueue(pt)
	}
}

func steeringMailboxLen(mailboxes *autoreply.SteeringMailboxRegistry, sessionID string) int {
	if mailboxes == nil || strings.TrimSpace(sessionID) == "" {
		return 0
	}
	mailbox := mailboxes.GetIfExists(sessionID)
	if mailbox == nil {
		return 0
	}
	return mailbox.Len()
}

func clearTransientSessionSteering(mailboxes *autoreply.SteeringMailboxRegistry, sessionID string) {
	if mailboxes == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	mailboxes.Delete(strings.TrimSpace(sessionID))
}

func formatSteeringForModel(item autoreply.SteeringMessage) string {
	text := strings.TrimSpace(item.Text)
	source := strings.ToLower(strings.TrimSpace(item.Source))
	if source == "channel" {
		sender := strings.TrimSpace(item.SenderID)
		if sender != "" {
			return fmt.Sprintf("[Additional user input from %s while you were working]\n%s", sender, text)
		}
		return "[Additional channel input received while you were working]\n" + text
	}
	return "[Additional user input received while you were working]\n" + text
}

func steeringSummaryLine(input activeRunSteeringInput) string {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return ""
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		return text
	}
	if sender := strings.TrimSpace(input.SenderID); sender != "" {
		return fmt.Sprintf("%s/%s: %s", source, sender, text)
	}
	return fmt.Sprintf("%s: %s", source, text)
}
