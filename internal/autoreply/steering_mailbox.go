package autoreply

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// SteeringPriority controls drain ordering for active-run steering input.
type SteeringPriority string

const (
	// SteeringPriorityNormal is ordinary same-run steering input.
	SteeringPriorityNormal SteeringPriority = "normal"
	// SteeringPriorityUrgent is input that should be injected before normal steering
	// at the next safe model boundary.
	SteeringPriorityUrgent SteeringPriority = "urgent"
)

// SteeringMessage is additional user input staged for injection into an active
// agent run at the next safe model boundary.
type SteeringMessage struct {
	// Text is the message text to inject into the active run.
	Text string
	// EventID is the inbound event identifier used for dedupe.
	EventID string
	// SenderID is the sender's identifier (pubkey, Slack user ID, etc.).
	SenderID string
	// ChannelID is the channel/conversation identifier for channel-sourced input.
	ChannelID string
	// ThreadID is the thread identifier for threaded channel input.
	ThreadID string
	// AgentID is an optional per-turn agent override preserved for residual fallback.
	AgentID string
	// ToolProfile is an optional per-turn tool profile override preserved for residual fallback.
	ToolProfile string
	// EnabledTools is an optional per-turn tool allowlist override preserved for residual fallback.
	EnabledTools []string
	// CreatedAt is the original inbound event timestamp (unix seconds).
	CreatedAt int64
	// EnqueuedAt is when the item was added to the mailbox.
	EnqueuedAt time.Time
	// Source identifies where the steering input came from, e.g. "dm" or "channel".
	Source string
	// Priority controls drain ordering. Empty priority is treated as normal.
	Priority SteeringPriority
	// SummaryLine is a brief description used when the item is dropped under
	// QueueDropSummarize policy.
	SummaryLine string
}

// SteeringMailboxStats exposes deterministic in-memory accounting for mailbox
// outcomes. It is intentionally local; daemon metrics are wired by later beads.
type SteeringMailboxStats struct {
	Enqueued int
	Drained  int
	Deduped  int
	Dropped  int
}

// SteeringMailbox is a per-session active-run mailbox for messages that arrive
// while the agent is already working. Active loops should drain it
// non-blockingly at safe model boundaries.
type SteeringMailbox struct {
	mu           sync.Mutex
	items        []SteeringMessage
	droppedLines []string
	cap          int
	dropPolicy   QueueDropPolicy
	recentIDs    map[string]time.Time
	seenTTL      time.Duration
	stats        SteeringMailboxStats
}

// NewSteeringMailbox creates a mailbox with the given capacity and drop policy.
// cap <= 0 means unlimited (not recommended for production).
func NewSteeringMailbox(cap int, policy QueueDropPolicy) *SteeringMailbox {
	if policy == "" {
		policy = QueueDropSummarize
	}
	return &SteeringMailbox{
		cap:        cap,
		dropPolicy: policy,
		recentIDs:  make(map[string]time.Time),
		seenTTL:    10 * time.Minute,
	}
}

// Enqueue adds a steering message to the mailbox. Returns true if accepted,
// false when the message was deduped or dropped by capacity policy.
func (m *SteeringMailbox) Enqueue(msg SteeringMessage) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.pruneRecentIDs(now)
	if msg.EventID != "" {
		for _, item := range m.items {
			if item.EventID != "" && item.EventID == msg.EventID {
				m.stats.Deduped++
				return false
			}
		}
		if seenAt, ok := m.recentIDs[msg.EventID]; ok && now.Sub(seenAt) <= m.seenTTL {
			m.stats.Deduped++
			return false
		}
	}

	if m.cap > 0 && len(m.items) >= m.cap {
		switch m.dropPolicy {
		case QueueDropNewest:
			m.stats.Dropped++
			return false
		case QueueDropOldest:
			if len(m.items) > 0 {
				m.items = m.items[1:]
				m.stats.Dropped++
			}
		default: // QueueDropSummarize
			if len(m.items) > 0 {
				oldest := m.items[0]
				m.items = m.items[1:]
				line := oldest.SummaryLine
				if line == "" {
					line = truncate(oldest.Text, 80)
				}
				m.droppedLines = append(m.droppedLines, line)
				if len(m.droppedLines) > maxDroppedLines {
					m.droppedLines = m.droppedLines[len(m.droppedLines)-maxDroppedLines:]
				}
				m.stats.Dropped++
			}
		}
	}

	if msg.Priority == "" {
		msg.Priority = SteeringPriorityNormal
	}
	if msg.SummaryLine == "" {
		msg.SummaryLine = truncate(msg.Text, 80)
	}
	msg.EnqueuedAt = now
	m.items = append(m.items, msg)
	if msg.EventID != "" {
		m.recentIDs[msg.EventID] = now
	}
	m.stats.Enqueued++
	return true
}

// Drain returns and removes all staged steering messages. Items are ordered by
// priority, then event creation time, then enqueue time. If dropped items exist,
// their summaries are prepended to the first drained item's text as a steering
// header. Drain is non-blocking and returns nil when the mailbox is empty.
func (m *SteeringMailbox) Drain() []SteeringMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.items) == 0 {
		return nil
	}

	items := append([]SteeringMessage(nil), m.items...)
	dropped := append([]string(nil), m.droppedLines...)
	m.items = nil
	m.droppedLines = nil

	sort.SliceStable(items, func(i, j int) bool {
		pi, pj := steeringPriorityRank(items[i].Priority), steeringPriorityRank(items[j].Priority)
		if pi != pj {
			return pi < pj
		}
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt < items[j].CreatedAt
		}
		return items[i].EnqueuedAt.Before(items[j].EnqueuedAt)
	})

	if len(dropped) > 0 {
		summary := "[Some steering messages were dropped while agent was busy]\n" + strings.Join(dropped, "\n")
		items[0].Text = summary + "\n\n" + items[0].Text
	}
	m.stats.Drained += len(items)
	return items
}

// Clear removes currently staged steering messages and dropped summaries while
// preserving recent event IDs for dedupe during the TTL window.
func (m *SteeringMailbox) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = nil
	m.droppedLines = nil
}

// Configure updates mailbox capacity and drop policy.
// cap <= 0 means unlimited; empty policy preserves the current policy.
func (m *SteeringMailbox) Configure(cap int, policy QueueDropPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cap = cap
	if policy != "" {
		m.dropPolicy = policy
	}
}

// Len returns the number of staged steering messages without draining.
func (m *SteeringMailbox) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// Stats returns a snapshot of local mailbox accounting.
func (m *SteeringMailbox) Stats() SteeringMailboxStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// ─── SteeringMailboxRegistry ──────────────────────────────────────────────────

// SteeringMailboxRegistry manages per-session active-run steering mailboxes.
type SteeringMailboxRegistry struct {
	mu         sync.Mutex
	mailboxes  map[string]*SteeringMailbox
	defaultCap int
	dropPolicy QueueDropPolicy
}

// NewSteeringMailboxRegistry creates a registry with given defaults.
func NewSteeringMailboxRegistry(defaultCap int, policy QueueDropPolicy) *SteeringMailboxRegistry {
	return &SteeringMailboxRegistry{
		mailboxes:  make(map[string]*SteeringMailbox),
		defaultCap: defaultCap,
		dropPolicy: policy,
	}
}

// Get returns the mailbox for sessionID, creating it if needed.
func (r *SteeringMailboxRegistry) Get(sessionID string) *SteeringMailbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	mailbox, ok := r.mailboxes[sessionID]
	if !ok {
		mailbox = NewSteeringMailbox(r.defaultCap, r.dropPolicy)
		r.mailboxes[sessionID] = mailbox
	}
	return mailbox
}

// GetIfExists returns the existing mailbox for sessionID without allocating one.
func (r *SteeringMailboxRegistry) GetIfExists(sessionID string) *SteeringMailbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mailboxes[sessionID]
}

// Delete removes the mailbox for sessionID.
func (r *SteeringMailboxRegistry) Delete(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.mailboxes, sessionID)
}

// Clear removes staged steering state for sessionID without deleting the
// mailbox object. It is a no-op when no mailbox exists.
func (r *SteeringMailboxRegistry) Clear(sessionID string) {
	r.mu.Lock()
	mailbox := r.mailboxes[sessionID]
	r.mu.Unlock()
	if mailbox != nil {
		mailbox.Clear()
	}
}

func (m *SteeringMailbox) pruneRecentIDs(now time.Time) {
	if m.seenTTL <= 0 {
		return
	}
	for id, ts := range m.recentIDs {
		if now.Sub(ts) > m.seenTTL {
			delete(m.recentIDs, id)
		}
	}
}

func steeringPriorityRank(priority SteeringPriority) int {
	switch priority {
	case SteeringPriorityUrgent:
		return 0
	default:
		return 1
	}
}
