package autoreply

import (
	"strings"
	"testing"
)

func TestSteeringMailbox_DrainOrdersUrgentBeforeNormal(t *testing.T) {
	m := NewSteeringMailbox(10, QueueDropSummarize)
	mustEnqueueSteering(t, m, SteeringMessage{Text: "normal-late", EventID: "n2", CreatedAt: 30})
	mustEnqueueSteering(t, m, SteeringMessage{Text: "urgent-late", EventID: "u2", CreatedAt: 20, Priority: SteeringPriorityUrgent})
	mustEnqueueSteering(t, m, SteeringMessage{Text: "normal-early", EventID: "n1", CreatedAt: 10})
	mustEnqueueSteering(t, m, SteeringMessage{Text: "urgent-early", EventID: "u1", CreatedAt: 5, Priority: SteeringPriorityUrgent})

	items := m.Drain()
	assertSteeringTexts(t, items, []string{"urgent-early", "urgent-late", "normal-early", "normal-late"})
	if m.Len() != 0 {
		t.Fatalf("expected mailbox to be empty after drain, got %d", m.Len())
	}
	if again := m.Drain(); again != nil {
		t.Fatalf("second drain should be nil, got %#v", again)
	}
}

func TestSteeringMailbox_DedupesByEventID(t *testing.T) {
	m := NewSteeringMailbox(10, QueueDropSummarize)
	if !m.Enqueue(SteeringMessage{Text: "one", EventID: "evt-1"}) {
		t.Fatal("first enqueue should succeed")
	}
	if m.Enqueue(SteeringMessage{Text: "dup", EventID: "evt-1"}) {
		t.Fatal("duplicate event id should be dropped")
	}
	items := m.Drain()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if m.Enqueue(SteeringMessage{Text: "dup-after-drain", EventID: "evt-1"}) {
		t.Fatal("recent duplicate should still be dropped after drain")
	}
	stats := m.Stats()
	if stats.Enqueued != 1 || stats.Deduped != 2 || stats.Drained != 1 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestSteeringMailbox_DropPolicies(t *testing.T) {
	mOldest := NewSteeringMailbox(1, QueueDropOldest)
	mustEnqueueSteering(t, mOldest, SteeringMessage{Text: "a", EventID: "1"})
	mustEnqueueSteering(t, mOldest, SteeringMessage{Text: "b", EventID: "2"})
	items := mOldest.Drain()
	assertSteeringTexts(t, items, []string{"b"})
	if stats := mOldest.Stats(); stats.Dropped != 1 {
		t.Fatalf("oldest stats dropped mismatch: %#v", stats)
	}

	mNewest := NewSteeringMailbox(1, QueueDropNewest)
	mustEnqueueSteering(t, mNewest, SteeringMessage{Text: "a", EventID: "1"})
	if mNewest.Enqueue(SteeringMessage{Text: "b", EventID: "2"}) {
		t.Fatal("newest policy should drop incoming item")
	}
	items = mNewest.Drain()
	assertSteeringTexts(t, items, []string{"a"})
	if stats := mNewest.Stats(); stats.Dropped != 1 || stats.Enqueued != 1 {
		t.Fatalf("newest stats mismatch: %#v", stats)
	}
}

func TestSteeringMailbox_SummarizePrependsDroppedSummary(t *testing.T) {
	m := NewSteeringMailbox(1, QueueDropSummarize)
	mustEnqueueSteering(t, m, SteeringMessage{Text: "first message", EventID: "1", SummaryLine: "first summary"})
	mustEnqueueSteering(t, m, SteeringMessage{Text: "second message", EventID: "2"})

	items := m.Drain()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !strings.Contains(items[0].Text, "[Some steering messages were dropped while agent was busy]") {
		t.Fatalf("expected steering drop header, got %q", items[0].Text)
	}
	if !strings.Contains(items[0].Text, "first summary") || !strings.Contains(items[0].Text, "second message") {
		t.Fatalf("expected dropped summary and kept message, got %q", items[0].Text)
	}
}

func TestSteeringMailbox_ConfigureUpdatesPolicy(t *testing.T) {
	m := NewSteeringMailbox(2, QueueDropSummarize)
	m.Configure(1, QueueDropOldest)
	mustEnqueueSteering(t, m, SteeringMessage{Text: "a", EventID: "1"})
	mustEnqueueSteering(t, m, SteeringMessage{Text: "b", EventID: "2"})
	items := m.Drain()
	assertSteeringTexts(t, items, []string{"b"})
}

func TestSteeringMailboxRegistry_GetClearDelete(t *testing.T) {
	r := NewSteeringMailboxRegistry(2, QueueDropNewest)
	m1 := r.Get("session-1")
	if m1 != r.Get("session-1") {
		t.Fatal("registry should return same mailbox for same session")
	}
	mustEnqueueSteering(t, m1, SteeringMessage{Text: "one", EventID: "evt-1"})
	r.Clear("session-1")
	if m1.Len() != 0 {
		t.Fatalf("clear should empty mailbox, got %d", m1.Len())
	}
	if m1.Enqueue(SteeringMessage{Text: "duplicate", EventID: "evt-1"}) {
		t.Fatal("clear should preserve recent event IDs for dedupe")
	}

	r.Delete("session-1")
	m2 := r.Get("session-1")
	if m2 == m1 {
		t.Fatal("delete should remove mailbox object")
	}
	if !m2.Enqueue(SteeringMessage{Text: "event accepted after delete", EventID: "evt-1"}) {
		t.Fatal("delete should remove recent event ID dedupe state")
	}
}

func mustEnqueueSteering(t *testing.T, m *SteeringMailbox, msg SteeringMessage) {
	t.Helper()
	if !m.Enqueue(msg) {
		t.Fatalf("enqueue failed for %#v", msg)
	}
}

func assertSteeringTexts(t *testing.T, items []SteeringMessage, want []string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("expected %d items, got %d: %#v", len(want), len(items), items)
	}
	for i, text := range want {
		if items[i].Text != text {
			t.Fatalf("item %d text mismatch: want %q got %q (all: %#v)", i, text, items[i].Text, items)
		}
	}
}
