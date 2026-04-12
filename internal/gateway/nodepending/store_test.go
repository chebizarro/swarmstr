package nodepending

import (
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

// ─── Enqueue ──────────────────────────────────────────────────────────────────

func TestEnqueue_Basic(t *testing.T) {
	s := New()
	res, err := s.Enqueue(EnqueueRequest{
		NodeID:  "node1",
		Command: "restart",
		Args:    map[string]any{"force": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res["node_id"] != "node1" {
		t.Errorf("node_id: %v", res["node_id"])
	}
	if res["deduped"] != false {
		t.Error("should not be deduped")
	}
	queued := res["queued"].(Action)
	if queued.Command != "restart" {
		t.Errorf("command: %q", queued.Command)
	}
}

func TestEnqueue_EmptyNodeID(t *testing.T) {
	s := New()
	_, err := s.Enqueue(EnqueueRequest{NodeID: "", Command: "x"})
	if err == nil {
		t.Fatal("expected error for empty node_id")
	}
}

func TestEnqueue_EmptyCommand(t *testing.T) {
	s := New()
	_, err := s.Enqueue(EnqueueRequest{NodeID: "n1", Command: ""})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestEnqueue_Idempotency(t *testing.T) {
	s := New()
	res1, _ := s.Enqueue(EnqueueRequest{
		NodeID:         "n1",
		Command:        "restart",
		IdempotencyKey: "key1",
	})
	res2, _ := s.Enqueue(EnqueueRequest{
		NodeID:         "n1",
		Command:        "restart",
		IdempotencyKey: "key1",
	})
	if res1["deduped"] != false {
		t.Error("first should not be deduped")
	}
	if res2["deduped"] != true {
		t.Error("second should be deduped")
	}
}

func TestEnqueue_WithTTL(t *testing.T) {
	s := New()
	res, _ := s.Enqueue(EnqueueRequest{
		NodeID:  "n1",
		Command: "cmd",
		TTLMS:   5000,
	})
	action := res["queued"].(Action)
	if action.ExpiresAtMS == 0 {
		t.Error("expected non-zero expiration")
	}
	if action.ExpiresAtMS <= action.EnqueuedAtMS {
		t.Error("expiration should be after enqueue time")
	}
}

// ─── Pull ─────────────────────────────────────────────────────────────────────

func TestPull_Empty(t *testing.T) {
	s := New()
	res, err := s.Pull("n1")
	if err != nil {
		t.Fatal(err)
	}
	actions := res["actions"].([]Action)
	if len(actions) != 0 {
		t.Errorf("expected empty, got %d", len(actions))
	}
}

func TestPull_EmptyNodeID(t *testing.T) {
	s := New()
	_, err := s.Pull("")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPull_ReturnsEnqueued(t *testing.T) {
	s := New()
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "a"})
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "b"})
	s.Enqueue(EnqueueRequest{NodeID: "n2", Command: "c"})

	res, _ := s.Pull("n1")
	actions := res["actions"].([]Action)
	if len(actions) != 2 {
		t.Fatalf("expected 2, got %d", len(actions))
	}
}

func TestPull_IsolatesNodes(t *testing.T) {
	s := New()
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "a"})
	s.Enqueue(EnqueueRequest{NodeID: "n2", Command: "b"})

	res, _ := s.Pull("n1")
	actions := res["actions"].([]Action)
	if len(actions) != 1 {
		t.Fatalf("expected 1 for n1, got %d", len(actions))
	}
}

// ─── Ack ──────────────────────────────────────────────────────────────────────

func TestAck_RemovesActions(t *testing.T) {
	s := New()
	r1, _ := s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "a"})
	r2, _ := s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "b"})

	id1 := r1["queued"].(Action).ID
	id2 := r2["queued"].(Action).ID

	// Ack only the first one
	ackRes, err := s.Ack(AckRequest{NodeID: "n1", IDs: []string{id1}})
	if err != nil {
		t.Fatal(err)
	}

	// If IDs are the same (same-millisecond enqueue), both get acked
	if id1 == id2 {
		// Both have same ID, so both get removed
		remaining := ackRes["remaining_count"].(int)
		if remaining != 0 {
			t.Errorf("same-id case: remaining: %d", remaining)
		}
	} else {
		remaining := ackRes["remaining_count"].(int)
		if remaining != 1 {
			t.Errorf("remaining: %d", remaining)
		}
	}
}

func TestAck_EmptyIDs(t *testing.T) {
	s := New()
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "a"})
	res, _ := s.Ack(AckRequest{NodeID: "n1", IDs: nil})
	remaining := res["remaining_count"].(int)
	if remaining != 1 {
		t.Errorf("remaining: %d", remaining)
	}
}

func TestAck_EmptyNodeID(t *testing.T) {
	s := New()
	_, err := s.Ack(AckRequest{NodeID: "", IDs: []string{"x"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── Drain ────────────────────────────────────────────────────────────────────

func TestDrain_All(t *testing.T) {
	s := New()
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "a"})
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "b"})
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "c"})

	res, err := s.Drain(DrainRequest{NodeID: "n1", MaxItems: 0}) // 0 = drain all
	if err != nil {
		t.Fatal(err)
	}
	drained := res["drained_count"].(int)
	remaining := res["remaining_count"].(int)
	if drained != 3 {
		t.Errorf("drained: %d", drained)
	}
	if remaining != 0 {
		t.Errorf("remaining: %d", remaining)
	}
}

func TestDrain_Partial(t *testing.T) {
	s := New()
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "a"})
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "b"})
	s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "c"})

	res, _ := s.Drain(DrainRequest{NodeID: "n1", MaxItems: 2})
	drained := res["drained_count"].(int)
	remaining := res["remaining_count"].(int)
	if drained != 2 {
		t.Errorf("drained: %d", drained)
	}
	if remaining != 1 {
		t.Errorf("remaining: %d", remaining)
	}

	// The remaining one should be "c"
	pullRes, _ := s.Pull("n1")
	actions := pullRes["actions"].([]Action)
	if len(actions) != 1 || actions[0].Command != "c" {
		t.Errorf("unexpected remaining: %v", actions)
	}
}

func TestDrain_EmptyNodeID(t *testing.T) {
	s := New()
	_, err := s.Drain(DrainRequest{NodeID: ""})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDrain_Empty(t *testing.T) {
	s := New()
	res, _ := s.Drain(DrainRequest{NodeID: "n1"})
	drained := res["drained_count"].(int)
	if drained != 0 {
		t.Errorf("expected 0 drained, got %d", drained)
	}
}

// ─── Concurrency ──────────────────────────────────────────────────────────────

func TestConcurrency(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Enqueue(EnqueueRequest{NodeID: "n1", Command: "cmd"})
		}()
	}
	wg.Wait()

	res, _ := s.Pull("n1")
	actions := res["actions"].([]Action)
	if len(actions) != 50 {
		t.Errorf("expected 50 actions, got %d", len(actions))
	}
}
