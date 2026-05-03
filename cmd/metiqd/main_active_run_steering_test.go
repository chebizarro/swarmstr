package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
)

func TestEnqueueActiveRunSteeringDrainsForModel(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}

	accepted := enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID: "sess-1",
		Text:      "please also consider the new constraint",
		EventID:   "evt-secret",
		SenderID:  "alice",
		Source:    "dm",
		CreatedAt: 10,
	})
	if !accepted {
		t.Fatal("expected steering input to enqueue")
	}

	drain := makeActiveRunSteeringDrain(mailboxes, "sess-1", nil)
	got := drain(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected one drained input, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, "Additional user input received while you were working") {
		t.Fatalf("expected DM provenance header, got %q", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "new constraint") {
		t.Fatalf("expected original text in injected input, got %q", got[0].Content)
	}
	if strings.Contains(got[0].Content, "evt-secret") {
		t.Fatalf("model-facing steering content exposed event id: %q", got[0].Content)
	}
	if again := drain(context.Background()); len(again) != 0 {
		t.Fatalf("expected mailbox to be empty after drain, got %d", len(again))
	}
}

func TestActiveRunSteeringChannelHeaderAndResidualMetadata(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}

	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID:    "sess-chan",
		Text:         "normal update",
		EventID:      "evt-normal",
		SenderID:     "bob",
		ChannelID:    "chan-1",
		ThreadID:     "thread-1",
		Source:       "channel",
		ToolProfile:  "safe",
		EnabledTools: []string{"read_file"},
		CreatedAt:    20,
		Priority:     autoreply.SteeringPriorityNormal,
	})
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID: "sess-chan",
		Text:      "urgent update",
		EventID:   "evt-urgent",
		SenderID:  "carol",
		ChannelID: "chan-1",
		Source:    "channel",
		CreatedAt: 30,
		Priority:  autoreply.SteeringPriorityUrgent,
	})

	drain := makeActiveRunSteeringDrain(mailboxes, "sess-chan", nil)
	modelInputs := drain(context.Background())
	if len(modelInputs) != 2 {
		t.Fatalf("expected two model inputs, got %d", len(modelInputs))
	}
	if !strings.Contains(modelInputs[0].Content, "from carol") || !strings.Contains(modelInputs[0].Content, "urgent update") {
		t.Fatalf("expected urgent channel steering first with sender provenance, got %q", modelInputs[0].Content)
	}

	// Re-enqueue and exercise residual fallback conversion, which must keep raw
	// text/provenance for the follow-up turn rather than model-facing headers.
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID:    "sess-chan",
		Text:         "normal update",
		EventID:      "evt-normal-2",
		SenderID:     "bob",
		ChannelID:    "chan-1",
		ThreadID:     "thread-1",
		Source:       "channel",
		ToolProfile:  "safe",
		EnabledTools: []string{"read_file"},
		CreatedAt:    20,
		Priority:     autoreply.SteeringPriorityNormal,
	})
	pending := drainSteeringAsPending(mailboxes, "sess-chan")
	if len(pending) != 1 {
		t.Fatalf("expected one residual pending turn, got %d", len(pending))
	}
	pt := pending[0]
	if pt.Text != "normal update" || pt.SenderID != "bob" || pt.EventID != "evt-normal-2" {
		t.Fatalf("residual pending turn did not preserve raw provenance: %+v", pt)
	}
	if pt.ToolProfile != "safe" || len(pt.EnabledTools) != 1 || pt.EnabledTools[0] != "read_file" {
		t.Fatalf("residual pending turn did not preserve tool constraints: %+v", pt)
	}
}

func TestClearTransientSessionSteeringDeletesMailbox(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-clear", Text: "stale", EventID: "evt-stale", Source: "dm"})
	if got := steeringMailboxLen(mailboxes, "sess-clear"); got != 1 {
		t.Fatalf("expected mailbox len 1 before cleanup, got %d", got)
	}
	clearTransientSessionSteering(mailboxes, "sess-clear")
	if got := steeringMailboxLen(mailboxes, "sess-clear"); got != 0 {
		t.Fatalf("expected mailbox len 0 after cleanup, got %d", got)
	}
}

func TestHandleBusyInterruptAbortsWhenNoToolsActive(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	q := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	ctx, release := chatCancels.Begin("sess-interrupt", context.Background())
	defer release()
	q.Enqueue(autoreply.PendingTurn{Text: "older backlog", EventID: "old-backlog"})
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-interrupt", Text: "older steering", EventID: "old-steer", Source: "dm"})

	deferred := handleBusyInterrupt(chatCancels, activeTools, mailboxes, q, settings, activeRunSteeringInput{
		SessionID: "sess-interrupt",
		Text:      "new interrupt",
		EventID:   "new-interrupt",
		Source:    "dm",
	})
	if deferred {
		t.Fatal("expected no active tools to abort immediately")
	}
	if ctx.Err() == nil {
		t.Fatal("expected active turn context to be cancelled")
	}
	if !errors.Is(context.Cause(ctx), agent.ErrTurnInterrupted) {
		t.Fatalf("expected interrupt cause, got %v", context.Cause(ctx))
	}
	if q.Len() != 0 {
		t.Fatalf("expected backlog cleared, got len=%d", q.Len())
	}
	if got := steeringMailboxLen(mailboxes, "sess-interrupt"); got != 0 {
		t.Fatalf("expected steering mailbox cleared, got len=%d", got)
	}
}

func TestHandleBusyInterruptAbortsWhenAllActiveToolsCancelable(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	ctx, release := chatCancels.Begin("sess-cancel", context.Background())
	defer release()
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-cancel",
		ToolCallID: "tool-1",
		ToolName:   "cancelable",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorCancel,
		},
	})

	deferred := handleBusyInterrupt(chatCancels, activeTools, nil, nil, settings, activeRunSteeringInput{SessionID: "sess-cancel", Text: "interrupt"})
	if deferred {
		t.Fatal("expected all-cancelable tools to abort immediately")
	}
	if ctx.Err() == nil {
		t.Fatal("expected active turn context to be cancelled")
	}
}

func TestHandleBusyInterruptDefersWhenAnyActiveToolBlocks(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	q := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	ctx, release := chatCancels.Begin("sess-block", context.Background())
	defer release()
	q.Enqueue(autoreply.PendingTurn{Text: "older backlog", EventID: "old-backlog"})
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-block", Text: "older steering", EventID: "old-steer", Source: "dm"})
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-block",
		ToolCallID: "tool-block",
		ToolName:   "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	})
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-block",
		ToolCallID: "tool-cancel",
		ToolName:   "cancelable",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorCancel,
		},
	})

	deferred := handleBusyInterrupt(chatCancels, activeTools, mailboxes, q, settings, activeRunSteeringInput{
		SessionID: "sess-block",
		Text:      "newest interrupt",
		EventID:   "new-interrupt",
		Source:    "dm",
	})
	if !deferred {
		t.Fatal("expected blocking tool to defer interrupt into urgent steering")
	}
	if ctx.Err() != nil {
		t.Fatalf("expected active turn to continue, got ctx err %v", ctx.Err())
	}
	if q.Len() != 0 {
		t.Fatalf("expected backlog cleared before urgent steering, got len=%d", q.Len())
	}
	pending := drainSteeringAsPending(mailboxes, "sess-block")
	if len(pending) != 1 {
		t.Fatalf("expected only newest urgent interrupt in mailbox, got %d", len(pending))
	}
	if pending[0].Text != "newest interrupt" || pending[0].EventID != "new-interrupt" {
		t.Fatalf("unexpected pending interrupt: %+v", pending[0])
	}
}

func TestActiveToolRegistryResultClearsBlockingTool(t *testing.T) {
	activeTools := newActiveToolRegistry()
	start := agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-tools",
		ToolCallID: "tool-block",
		ToolName:   "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	}
	activeTools.Record(start)
	if activeTools.AllInterruptible("sess-tools") {
		t.Fatal("expected blocking tool to make session non-interruptible")
	}
	start.Type = agent.ToolLifecycleEventResult
	activeTools.Record(start)
	if !activeTools.AllInterruptible("sess-tools") {
		t.Fatal("expected session interruptible after tool result")
	}
}

func TestActiveToolRegistryCountsProviderEmptyFallbackKeys(t *testing.T) {
	activeTools := newActiveToolRegistry()
	start := agent.ToolLifecycleEvent{
		Type:      agent.ToolLifecycleEventStart,
		SessionID: "sess-empty-id",
		TurnID:    "turn-1",
		ToolName:  "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	}
	activeTools.Record(start)
	activeTools.Record(start)
	if activeTools.AllInterruptible("sess-empty-id") {
		t.Fatal("expected duplicate empty-id blocking calls to be tracked")
	}
	start.Type = agent.ToolLifecycleEventResult
	activeTools.Record(start)
	if activeTools.AllInterruptible("sess-empty-id") {
		t.Fatal("expected one remaining empty-id blocking call after first result")
	}
	activeTools.Record(start)
	if !activeTools.AllInterruptible("sess-empty-id") {
		t.Fatal("expected session interruptible after both empty-id calls complete")
	}
}

func TestOperatorAbortUnconditionalWithBlockingToolActive(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	ctx, release := chatCancels.Begin("sess-operator", context.Background())
	defer release()
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-operator",
		ToolCallID: "tool-block",
		ToolName:   "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	})
	if activeTools.AllInterruptible("sess-operator") {
		t.Fatal("expected blocking tool to be active for regression setup")
	}
	if !chatCancels.Abort("sess-operator") {
		t.Fatal("expected operator abort to cancel in-flight session")
	}
	if ctx.Err() == nil {
		t.Fatal("expected operator abort to cancel even with blocking tool active")
	}
}
