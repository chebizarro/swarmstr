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
	// AgentID is an optional per-turn agent override.
	AgentID string
	// ToolProfile is an optional per-turn tool profile override.
	ToolProfile string
	// EnabledTools is an optional per-turn tool allowlist override.
	EnabledTools []string
	// CreatedAt is the original inbound event timestamp (unix seconds).
	CreatedAt int64
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
	recentIDs    map[string]time.Time
	seenTTL      time.Duration
}

const maxDroppedLines = 100 // Limit dropped line history to prevent unbounded growth

// NewSessionQueue creates a queue with the given capacity and drop policy.
// cap <= 0 means unlimited (not recommended for production).
func NewSessionQueue(cap int, policy QueueDropPolicy) *SessionQueue {
	if policy == "" {
		policy = QueueDropSummarize
	}
	return &SessionQueue{
		cap:        cap,
		dropPolicy: policy,
		recentIDs:  make(map[string]time.Time),
		seenTTL:    10 * time.Minute,
	}
}

// Enqueue adds a pending turn to the queue. Returns true if the item was
// accepted, false if it was dropped due to capacity limits.
func (q *SessionQueue) Enqueue(pt PendingTurn) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	q.pruneRecentIDs(now)
	if pt.EventID != "" {
		for _, item := range q.items {
			if item.EventID != "" && item.EventID == pt.EventID {
				return false
			}
		}
		if seenAt, ok := q.recentIDs[pt.EventID]; ok && now.Sub(seenAt) <= q.seenTTL {
			return false
		}
	}

	if q.cap > 0 && len(q.items) >= q.cap {
		switch q.dropPolicy {
		case QueueDropNewest:
			return false
		case QueueDropOldest:
			if len(q.items) > 0 {
				q.items = q.items[1:]
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
				// Limit dropped lines history to prevent unbounded growth
				if len(q.droppedLines) > maxDroppedLines {
					q.droppedLines = q.droppedLines[len(q.droppedLines)-maxDroppedLines:]
				}
			}
		}
	}

	if pt.SummaryLine == "" {
		pt.SummaryLine = truncate(pt.Text, 80)
	}
	pt.EnqueuedAt = now
	q.items = append(q.items, pt)
	if pt.EventID != "" {
		q.recentIDs[pt.EventID] = now
	}
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
	// Note: EventIDs are already tracked in recentIDs from Enqueue() time.
	// We don't update timestamps here to avoid extending the TTL window.

	// If items were dropped, prepend a summary to inform the agent.
	if len(dropped) > 0 && len(items) > 0 {
		summary := "[Some messages were dropped while agent was busy]\n" + strings.Join(dropped, "\n")
		items[0].Text = summary + "\n\n" + items[0].Text
	}
	return items
}

// Configure updates queue capacity and drop policy.
// cap <= 0 means unlimited; empty policy preserves the current policy.
func (q *SessionQueue) Configure(cap int, policy QueueDropPolicy) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cap = cap
	if policy != "" {
		q.dropPolicy = policy
	}
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

func (q *SessionQueue) pruneRecentIDs(now time.Time) {
	if q.seenTTL <= 0 {
		return
	}
	for id, ts := range q.recentIDs {
		if now.Sub(ts) > q.seenTTL {
			delete(q.recentIDs, id)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
