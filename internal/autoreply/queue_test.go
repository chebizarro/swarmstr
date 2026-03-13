package autoreply

import "testing"

func TestSessionQueue_DedupesByEventID(t *testing.T) {
	q := NewSessionQueue(10, QueueDropSummarize)
	if !q.Enqueue(PendingTurn{Text: "one", EventID: "evt-1"}) {
		t.Fatal("first enqueue should succeed")
	}
	if q.Enqueue(PendingTurn{Text: "dup", EventID: "evt-1"}) {
		t.Fatal("duplicate event id should be dropped")
	}
	items := q.Dequeue()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if q.Enqueue(PendingTurn{Text: "dup-after-drain", EventID: "evt-1"}) {
		t.Fatal("recent duplicate should still be dropped after drain")
	}
}

func TestSessionQueue_DropPolicies(t *testing.T) {
	qOldest := NewSessionQueue(1, QueueDropOldest)
	_ = qOldest.Enqueue(PendingTurn{Text: "a", EventID: "1"})
	_ = qOldest.Enqueue(PendingTurn{Text: "b", EventID: "2"})
	items := qOldest.Dequeue()
	if len(items) != 1 || items[0].Text != "b" {
		t.Fatalf("oldest policy mismatch: %#v", items)
	}

	qNewest := NewSessionQueue(1, QueueDropNewest)
	_ = qNewest.Enqueue(PendingTurn{Text: "a", EventID: "1"})
	if qNewest.Enqueue(PendingTurn{Text: "b", EventID: "2"}) {
		t.Fatal("newest policy should drop incoming item")
	}
	items = qNewest.Dequeue()
	if len(items) != 1 || items[0].Text != "a" {
		t.Fatalf("newest policy mismatch: %#v", items)
	}
}

func TestSessionQueue_ConfigureUpdatesPolicy(t *testing.T) {
	q := NewSessionQueue(2, QueueDropSummarize)
	q.Configure(1, QueueDropOldest)
	_ = q.Enqueue(PendingTurn{Text: "a", EventID: "1"})
	_ = q.Enqueue(PendingTurn{Text: "b", EventID: "2"})
	items := q.Dequeue()
	if len(items) != 1 || items[0].Text != "b" {
		t.Fatalf("configure policy mismatch: %#v", items)
	}
}
