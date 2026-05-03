package main

import (
	"context"
	"strings"
	"testing"

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
