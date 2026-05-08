package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMemoryOutboxRetryBackoffTerminalFailureAndCompaction(t *testing.T) {
	b := newUnifiedTestSQLiteBackend(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	id, err := b.EnqueueMemoryOutboxEvent(ctx, "rec-1", "nostr:30321", map[string]any{"id": "rec-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	events, err := b.DueMemoryOutboxEvents(ctx, now, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != id {
		t.Fatalf("expected newly enqueued event to be due, got %#v", events)
	}
	for attempt, backoff := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		at := now.Add(time.Duration(attempt) * time.Minute)
		if err := b.MarkMemoryOutboxAttempt(ctx, id, errors.New("relay rejected"), at); err != nil {
			t.Fatal(err)
		}
		due, err := b.DueMemoryOutboxEvents(ctx, at.Add(backoff-time.Second), 10, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(due) != 0 {
			t.Fatalf("attempt %d due before backoff elapsed: %#v", attempt+1, due)
		}
		due, err = b.DueMemoryOutboxEvents(ctx, at.Add(backoff), 10, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(due) != 1 || due[0].Attempts != attempt+1 {
			t.Fatalf("attempt %d expected due after %s, got %#v", attempt+1, backoff, due)
		}
	}

	oldID, err := b.EnqueueMemoryOutboxEvent(ctx, "rec-old", "nostr:30321", "{}", now.Add(-8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.MarkMemoryOutboxAttempt(ctx, oldID, errors.New("relay offline"), now); err != nil {
		t.Fatal(err)
	}
	forced, err := b.DueMemoryOutboxEvents(ctx, now, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	foundFailed := false
	for _, ev := range forced {
		if ev.ID == oldID && ev.PublishFailed {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("expected old event to be terminal publish_failed, got %#v", forced)
	}
	stats, err := b.MemoryOutboxStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.PublishFailures == 0 || stats.OutboxDepth == 0 || stats.RetryCounts == nil {
		t.Fatalf("expected stats to expose depth/failures/retries, got %#v", stats)
	}
	removed, err := b.CompactMemoryOutbox(ctx, now.Add(31*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 30-day failed compaction to remove old failed event, removed=%d", removed)
	}
}

func TestMemoryOutboxForceRepublishAndHealthDepthWarning(t *testing.T) {
	b := newUnifiedTestSQLiteBackend(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 101; i++ {
		if _, err := b.EnqueueMemoryOutboxEvent(ctx, fmt.Sprintf("rec-%d", i), "nostr:30321", "{}", now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	failedID, err := b.EnqueueMemoryOutboxEvent(ctx, "rec-failed", "nostr:30321", "{}", now.Add(-8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.MarkMemoryOutboxAttempt(ctx, failedID, errors.New("old failure"), now); err != nil {
		t.Fatal(err)
	}
	due, err := b.DueMemoryOutboxEvents(ctx, now, 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("future outbox events should not be due without force, got %d", len(due))
	}
	reset, err := b.ForceRepublishMemoryOutbox(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if reset != 1 {
		t.Fatalf("force republish reset=%d, want only failed event", reset)
	}
	due, err = b.DueMemoryOutboxEvents(ctx, now, 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != failedID {
		t.Fatalf("force republish should make only failed event due, got %#v", due)
	}
	health, err := b.MemoryHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if health.IssueCounts["outbox_depth_high"] != 102 || health.Index["outbox_depth"] != 102 {
		t.Fatalf("expected outbox health depth warning, got %#v", health)
	}
}
