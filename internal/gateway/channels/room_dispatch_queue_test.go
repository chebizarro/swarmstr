package channels

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoomQueue_SerializesInOrder(t *testing.T) {
	q := NewRoomDispatchQueue(RoomDispatchQueueOptions{})
	var mu sync.Mutex
	var order []int
	start := make(chan struct{})
	const n = 10
	for i := 0; i < n; i++ {
		i := i
		if !q.Enqueue("room", 9, func() {
			<-start
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
		}) {
			t.Fatalf("enqueue %d unexpectedly shed", i)
		}
	}
	close(start)
	q.Wait()
	if len(order) != n {
		t.Fatalf("ran %d, want %d", len(order), n)
	}
	for i := 0; i < n; i++ {
		if order[i] != i {
			t.Fatalf("order[%d]=%d, want %d (not serialized in enqueue order)", i, order[i], i)
		}
	}
}

func TestRoomQueue_LoadShedsNewestAtDepth(t *testing.T) {
	var drops int32
	var ovmu sync.Mutex
	var lastOv RoomOverflow
	q := NewRoomDispatchQueue(RoomDispatchQueueOptions{
		MaxDepth: 4,
		OnOverflow: func(ov RoomOverflow) {
			atomic.AddInt32(&drops, 1)
			ovmu.Lock()
			lastOv = ov
			ovmu.Unlock()
		},
	})
	block := make(chan struct{})
	for i := 0; i < 4; i++ {
		if !q.Enqueue("r", 9, func() { <-block }) {
			t.Fatalf("enqueue %d should fit within depth 4", i)
		}
	}
	// 5th exceeds depth -> load-shed newest.
	if q.Enqueue("r", 7, func() { t.Error("shed run must not execute") }) {
		t.Error("5th enqueue should be load-shed")
	}
	if got := atomic.LoadInt32(&drops); got != 1 {
		t.Errorf("drops=%d, want 1", got)
	}
	ovmu.Lock()
	ov := lastOv
	ovmu.Unlock()
	if !ov.Log || ov.EventKind != 7 || ov.Depth != 4 {
		t.Errorf("overflow report = %+v, want Log=true kind=7 depth=4", ov)
	}
	close(block)
	q.Wait()
	// A slot frees after drain.
	if !q.Enqueue("r", 9, func() {}) {
		t.Error("enqueue should fit after the room drains")
	}
	q.Wait()
}

func TestRoomQueue_OverflowThrottle(t *testing.T) {
	clock := newGuardClock()
	var logged []RoomOverflow
	var mu sync.Mutex
	q := NewRoomDispatchQueue(RoomDispatchQueueOptions{
		MaxDepth: 1,
		Now:      clock.now,
		OnOverflow: func(ov RoomOverflow) {
			if ov.Log {
				mu.Lock()
				logged = append(logged, ov)
				mu.Unlock()
			}
		},
	})
	block := make(chan struct{})
	if !q.Enqueue("r", 9, func() { <-block }) {
		t.Fatal("first enqueue should fit")
	}
	// Three drops within the window: first logs, next two are suppressed.
	q.Enqueue("r", 9, func() {})
	q.Enqueue("r", 9, func() {})
	q.Enqueue("r", 9, func() {})
	mu.Lock()
	n := len(logged)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("within window: %d logged, want 1", n)
	}
	// Advance past the window: next drop logs and folds in the 2 suppressed.
	clock.advance(61 * time.Second)
	q.Enqueue("r", 9, func() {})
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 2 {
		t.Fatalf("after window: %d logged, want 2", len(logged))
	}
	if logged[1].Suppressed != 2 {
		t.Errorf("suppressed = %d, want 2", logged[1].Suppressed)
	}
	close(block)
	q.Wait()
}

func TestRoomQueue_ClosingSkipsRun(t *testing.T) {
	var closing atomic.Bool
	closing.Store(true)
	q := NewRoomDispatchQueue(RoomDispatchQueueOptions{
		Closing: func() bool { return closing.Load() },
	})
	var ran int32
	q.Enqueue("r", 9, func() { atomic.AddInt32(&ran, 1) })
	q.Wait()
	if atomic.LoadInt32(&ran) != 0 {
		t.Error("a closing queue must skip queued runs")
	}
}

func TestRoomQueue_DistinctRoomsConcurrent(t *testing.T) {
	q := NewRoomDispatchQueue(RoomDispatchQueueOptions{})
	a := make(chan struct{})
	b := make(chan struct{})
	var started sync.WaitGroup
	started.Add(2)
	q.Enqueue("A", 9, func() { started.Done(); <-a })
	q.Enqueue("B", 9, func() { started.Done(); <-b })

	done := make(chan struct{})
	go func() { started.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("distinct rooms did not run concurrently (wrongly serialized across rooms)")
	}
	close(a)
	close(b)
	q.Wait()
}

func TestRoomQueue_DepthReturnsToZero(t *testing.T) {
	q := NewRoomDispatchQueue(RoomDispatchQueueOptions{})
	for i := 0; i < 5; i++ {
		q.Enqueue("r", 9, func() {})
	}
	q.Wait()
	if d := q.Depth("r"); d != 0 {
		t.Errorf("depth after drain = %d, want 0 (map should be reclaimed)", d)
	}
}
