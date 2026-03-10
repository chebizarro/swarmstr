package autoreply

import (
	"strings"
	"sync"
	"time"
)

// QueueDropPolicy controls what happens when a session queue is full.
type QueueDropPolicy string

const (
	// QueueDropSummarize keeps a one-line summary of dropped items and
	// prepends it to the next collected batch prompt.
	QueueDropSummarize QueueDropPolicy = "summarize"
	// QueueDropOldest discards the oldest queued item to make room.
	QueueDropOldest QueueDropPolicy = "oldest"
	// QueueDropNewest discards the newly-arrived item (default: never enqueue when full).
	QueueDropNewest QueueDropPolicy = "newest"
)

// PendingTurn is a queued message waiting to be processed after the current
// agent turn finishes.
type PendingTurn struct {
	// Text is the combined message text (may be multiple debounced messages).
	Text string
	// EventID is the inbound event identifier (for dedup and ack reactions).
	EventID string
	// SenderID is the sender's identifier (pubkey, Slack user ID, etc.).
	SenderID string
	// EnqueuedAt is when the item was added to the queue.
	EnqueuedAt time.Time
	// SummaryLine is a brief description used when the item is dropped under
	// QueueDropSummarize policy.
	SummaryLine string
}

// SessionQueue is a per-session FIFO queue for messages that arrive while the
// agent is busy. After each turn completes, the caller should drain the queue
// and run the next pending turn.
type SessionQueue struct {
	mu           sync.Mutex
	items        []PendingTurn
	droppedLines []string // summary lines from dropped items
	cap          int
	dropPolicy   QueueDropPolicy
}

// NewSessionQueue creates a queue with the given capacity and drop policy.
// cap <= 0 means unlimited (not recommended for production).
func NewSessionQueue(cap int, policy QueueDropPolicy) *SessionQueue {
	if policy == "" {
		policy = QueueDropSummarize
	}
	return &SessionQueue{cap: cap, dropPolicy: policy}
}

// Enqueue adds a pending turn to the queue. Returns true if the item was
// accepted, false if it was dropped due to capacity limits.
func (q *SessionQueue) Enqueue(pt PendingTurn) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cap > 0 && len(q.items) >= q.cap {
		switch q.dropPolicy {
		case QueueDropNewest:
			return false
		case QueueDropOldest:
			if len(q.items) > 0 {
				dropped := q.items[0]
				q.items = q.items[1:]
				if dropped.SummaryLine != "" {
					q.droppedLines = append(q.droppedLines, dropped.SummaryLine)
				}
			}
		default: // QueueDropSummarize
			// Record a summary of the newest-but-one (keep the most recent).
			if len(q.items) > 0 {
				oldest := q.items[0]
				q.items = q.items[1:]
				line := oldest.SummaryLine
				if line == "" {
					line = truncate(oldest.Text, 80)
				}
				q.droppedLines = append(q.droppedLines, line)
			}
		}
	}

	if pt.SummaryLine == "" {
		pt.SummaryLine = truncate(pt.Text, 80)
	}
	pt.EnqueuedAt = time.Now()
	q.items = append(q.items, pt)
	return true
}

// Dequeue returns and removes all pending items. If dropped items exist their
// summaries are prepended to the first item's text as a "[Queued messages]"
// header. Returns nil if the queue is empty.
func (q *SessionQueue) Dequeue() []PendingTurn {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return nil
	}

	items := q.items
	dropped := q.droppedLines
	q.items = nil
	q.droppedLines = nil

	// If items were dropped, prepend a summary to inform the agent.
	if len(dropped) > 0 && len(items) > 0 {
		summary := "[Some messages were dropped while agent was busy]\n" + strings.Join(dropped, "\n")
		items[0].Text = summary + "\n\n" + items[0].Text
	}
	return items
}

// Len returns the number of queued items without dequeuing.
func (q *SessionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// ─── SessionQueueRegistry ─────────────────────────────────────────────────────

// SessionQueueRegistry manages per-session queues.
type SessionQueueRegistry struct {
	mu         sync.Mutex
	queues     map[string]*SessionQueue
	defaultCap int
	dropPolicy QueueDropPolicy
}

// NewSessionQueueRegistry creates a registry with given defaults.
func NewSessionQueueRegistry(defaultCap int, policy QueueDropPolicy) *SessionQueueRegistry {
	return &SessionQueueRegistry{
		queues:     make(map[string]*SessionQueue),
		defaultCap: defaultCap,
		dropPolicy: policy,
	}
}

// Get returns the queue for sessionID, creating it if needed.
func (r *SessionQueueRegistry) Get(sessionID string) *SessionQueue {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.queues[sessionID]
	if !ok {
		q = NewSessionQueue(r.defaultCap, r.dropPolicy)
		r.queues[sessionID] = q
	}
	return q
}

// Delete removes the queue for sessionID (called after drain if empty).
func (r *SessionQueueRegistry) Delete(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.queues, sessionID)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
