// Package channels — room_dispatch_queue.go ports the openclaw-nostr per-room
// dispatch queue (nostr-bus-nip29.ts enqueueRoomDispatch; ocn-2l3/ocn-bf9/
// ocn-gz8). Concurrent inbound paths (kind:9 message, kind:7 reaction,
// 9000/9001 membership) plus reconnect backfill bursts hit the same room. This
// queue SERIALIZES turns per room so multiple senders on one room-scoped
// session cannot race on history/persistence, and BOUNDS the backlog (depth 32,
// load-shed the newest event) so a slow turn cannot retain unbounded closures.
package channels

import (
	"sync"
	"time"
)

// DefaultRoomDispatchQueueDepth bounds outstanding (queued + running) dispatches
// per room. The worst-case wait for the Nth queued event is N × the dispatch
// timeout, so the queue must be bounded.
const DefaultRoomDispatchQueueDepth = 32

// roomOverflowLogWindow throttles the human-facing overflow log so a saturated
// room cannot spam one line per dropped event.
const roomOverflowLogWindow = 60 * time.Second

// RoomOverflow describes a load-shed (dropped) event.
type RoomOverflow struct {
	RoomKey   string
	Depth     int
	EventKind int
	// Log is true when the throttle window allows a human-facing line.
	Log bool
	// Suppressed is the number of drops folded into the previous window (only
	// meaningful when Log is true).
	Suppressed int
}

// RoomDispatchQueueOptions configure a RoomDispatchQueue.
type RoomDispatchQueueOptions struct {
	// MaxDepth bounds outstanding dispatches per room. Default 32.
	MaxDepth int
	// OnOverflow is called for every load-shed event: the caller should ALWAYS
	// emit the dispatch.queue_dropped counter and log the line only when
	// RoomOverflow.Log is true. Overflow is deliberate load shedding, not a
	// fault — the dropped event stays seen and is NOT auto-retried.
	OnOverflow func(RoomOverflow)
	// Closing returns true once the bus is shutting down; queued dispatches that
	// have not started yet then skip their run (no new turns after close).
	Closing func() bool
	// Now is an injectable clock for the overflow throttle. Default time.Now.
	Now func() time.Time
}

type overflowState struct {
	windowStart time.Time
	suppressed  int
}

// RoomDispatchQueue serializes and bounds per-room dispatches. Safe for
// concurrent use.
type RoomDispatchQueue struct {
	mu       sync.Mutex
	tails    map[string]chan struct{} // roomKey -> channel closed when the current tail completes
	depths   map[string]int           // roomKey -> outstanding (queued + running)
	overflow map[string]*overflowState
	maxDepth int
	onOver   func(RoomOverflow)
	closing  func() bool
	now      func() time.Time
	wg       sync.WaitGroup
}

// NewRoomDispatchQueue constructs a per-room dispatch queue.
func NewRoomDispatchQueue(opts RoomDispatchQueueOptions) *RoomDispatchQueue {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultRoomDispatchQueueDepth
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &RoomDispatchQueue{
		tails:    make(map[string]chan struct{}),
		depths:   make(map[string]int),
		overflow: make(map[string]*overflowState),
		maxDepth: opts.MaxDepth,
		onOver:   opts.OnOverflow,
		closing:  opts.Closing,
		now:      opts.Now,
	}
}

// Enqueue schedules run to execute after all prior dispatches for roomKey have
// completed (serialized). Returns false and load-sheds the newest event when the
// room is at capacity. run executes on a background goroutine.
func (q *RoomDispatchQueue) Enqueue(roomKey string, eventKind int, run func()) bool {
	q.mu.Lock()
	depth := q.depths[roomKey]
	if depth >= q.maxDepth {
		ov := q.recordOverflowLocked(roomKey, depth, eventKind)
		q.mu.Unlock()
		if q.onOver != nil {
			q.onOver(ov)
		}
		return false
	}
	q.depths[roomKey] = depth + 1
	prev := q.tails[roomKey]
	done := make(chan struct{})
	q.tails[roomKey] = done
	q.wg.Add(1)
	q.mu.Unlock()

	go func() {
		defer q.wg.Done()
		defer close(done)
		// Serialize: wait for the previous dispatch on this room to finish.
		if prev != nil {
			<-prev
		}
		if q.closing == nil || !q.closing() {
			run()
		}
		q.mu.Lock()
		if n := q.depths[roomKey]; n <= 1 {
			delete(q.depths, roomKey)
		} else {
			q.depths[roomKey] = n - 1
		}
		if q.tails[roomKey] == done {
			delete(q.tails, roomKey)
		}
		q.mu.Unlock()
	}()
	return true
}

// recordOverflowLocked computes the throttled overflow report. Caller holds q.mu.
func (q *RoomDispatchQueue) recordOverflowLocked(roomKey string, depth, eventKind int) RoomOverflow {
	now := q.now()
	st := q.overflow[roomKey]
	if st != nil && now.Sub(st.windowStart) < roomOverflowLogWindow {
		st.suppressed++
		return RoomOverflow{RoomKey: roomKey, Depth: depth, EventKind: eventKind, Log: false}
	}
	suppressed := 0
	if st != nil {
		suppressed = st.suppressed
	}
	q.overflow[roomKey] = &overflowState{windowStart: now, suppressed: 0}
	return RoomOverflow{RoomKey: roomKey, Depth: depth, EventKind: eventKind, Log: true, Suppressed: suppressed}
}

// Depth returns the current outstanding dispatch count for roomKey (diagnostics).
func (q *RoomDispatchQueue) Depth(roomKey string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.depths[roomKey]
}

// Wait blocks until all outstanding dispatches finish (tests/shutdown drain).
func (q *RoomDispatchQueue) Wait() { q.wg.Wait() }
