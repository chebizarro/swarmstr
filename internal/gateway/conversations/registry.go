// Package conversations implements the gateway conversations.* surface: a
// registry of durable external conversation addresses observed on channel
// transports, operation-id dedupe for outbound sends, and pending-turn reply
// correlation for conversations.turn.
//
// Metiq deviations from the OpenClaw reference: the registry is process-local
// (no durable delivery store yet), so operation replay covers one daemon
// lifetime and turn results report correlationPersisted=false.
package conversations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Conversation kinds mirroring the OpenClaw wire enum.
const (
	KindDirect  = "direct"
	KindGroup   = "group"
	KindChannel = "channel"
)

// Conversation is one durable external conversation address.
type Conversation struct {
	ConversationRef string `json:"conversationRef"`
	Channel         string `json:"channel"`
	AccountID       string `json:"accountId"`
	Kind            string `json:"kind"`
	Target          string `json:"target"`
	ThreadID        string `json:"threadId,omitempty"`
	Label           string `json:"label,omitempty"`
	FirstSeenAt     int64  `json:"firstSeenAt"`
	LastSeenAt      int64  `json:"lastSeenAt"`
}

// BuildRef derives the stable conversation reference for a transport address.
// The shape matches OpenClaw's "conv_" + 32 hex chars contract.
func BuildRef(channel, accountID, kind, target, threadID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.ToLower(strings.TrimSpace(channel)),
		strings.TrimSpace(accountID),
		strings.TrimSpace(kind),
		strings.TrimSpace(target),
		strings.TrimSpace(threadID),
	}, "\x00")))
	return "conv_" + hex.EncodeToString(sum[:])[:32]
}

// Reply is one correlated inbound reply consumed by a pending turn.
type Reply struct {
	ConversationRef string `json:"conversationRef"`
	MessageID       string `json:"messageId"`
	ReplyToID       string `json:"replyToId,omitempty"`
	ThreadID        string `json:"threadId,omitempty"`
	Text            string `json:"text"`
	Timestamp       int64  `json:"timestamp"`
}

// PendingTurn is one registered conversations.turn waiter.
type PendingTurn struct {
	registry        *Registry
	key             string
	conversationRef string
	deadline        time.Time
	replyCh         chan Reply
	cancelCh        chan struct{}
	once            sync.Once
}

// Wait blocks until a correlated reply arrives, the turn deadline passes, the
// turn is cancelled, or ctx ends. replied is true only for a real reply.
func (t *PendingTurn) Wait(ctx context.Context) (reply Reply, replied bool, err error) {
	timer := time.NewTimer(time.Until(t.deadline))
	defer timer.Stop()
	defer t.registry.removeTurn(t.key, t)
	select {
	case reply = <-t.replyCh:
		return reply, true, nil
	case <-t.cancelCh:
		return Reply{}, false, fmt.Errorf("conversation turn was cancelled")
	case <-timer.C:
		// A reply may have been consumed by NotifyInbound in the instant the
		// deadline fired. Prefer delivering it over reporting a timeout so the
		// correlated message is never silently dropped.
		select {
		case reply = <-t.replyCh:
			return reply, true, nil
		default:
			return Reply{}, false, nil
		}
	case <-ctx.Done():
		return Reply{}, false, ctx.Err()
	}
}

// operationRecord tracks one send/turn operation id for replay protection.
type operationRecord struct {
	identity string
	result   map[string]any
	done     bool
	at       time.Time
}

const operationTTL = time.Hour

// Registry holds observed conversations, operation dedupe state, and pending
// turn waiters. All methods are safe for concurrent use.
type Registry struct {
	mu            sync.Mutex
	conversations map[string]*Conversation
	operations    map[string]*operationRecord
	turns         map[string]*PendingTurn
	turnsByRef    map[string][]*PendingTurn
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		conversations: map[string]*Conversation{},
		operations:    map[string]*operationRecord{},
		turns:         map[string]*PendingTurn{},
		turnsByRef:    map[string][]*PendingTurn{},
	}
}

// Observe upserts a conversation address, refreshing lastSeenAt and label.
// Returns the stored record.
func (r *Registry) Observe(c Conversation, at int64) Conversation {
	c.Channel = strings.ToLower(strings.TrimSpace(c.Channel))
	c.AccountID = strings.TrimSpace(c.AccountID)
	c.Target = strings.TrimSpace(c.Target)
	if c.Channel == "" || c.Target == "" {
		return Conversation{}
	}
	if c.AccountID == "" {
		c.AccountID = "default"
	}
	if c.Kind == "" {
		c.Kind = KindDirect
	}
	if c.ConversationRef == "" {
		c.ConversationRef = BuildRef(c.Channel, c.AccountID, c.Kind, c.Target, c.ThreadID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.conversations[c.ConversationRef]
	if !ok {
		c.FirstSeenAt = at
		c.LastSeenAt = at
		stored := c
		r.conversations[c.ConversationRef] = &stored
		return stored
	}
	existing.LastSeenAt = at
	if c.Label != "" {
		existing.Label = c.Label
	}
	return *existing
}

// Resolve returns the conversation for ref when known.
func (r *Registry) Resolve(ref string) (Conversation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.conversations[strings.TrimSpace(ref)]; ok {
		return *c, true
	}
	return Conversation{}, false
}

func matchesQuery(c Conversation, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	terms := []string{query}
	if strings.HasPrefix(query, "@") {
		terms = append(terms, query[1:])
	}
	values := []string{strings.ToLower(c.ConversationRef), strings.ToLower(c.Target), strings.ToLower(c.Label)}
	for _, term := range terms {
		if term == "" {
			continue
		}
		for _, value := range values {
			if value != "" && strings.Contains(value, term) {
				return true
			}
		}
	}
	return false
}

// List returns conversations sorted by lastSeenAt (newest first), optionally
// filtered by channel and free-text query, capped at limit.
func (r *Registry) List(channel, query string, limit int) []Conversation {
	channel = strings.ToLower(strings.TrimSpace(channel))
	r.mu.Lock()
	out := make([]Conversation, 0, len(r.conversations))
	for _, c := range r.conversations {
		if channel != "" && c.Channel != channel {
			continue
		}
		if !matchesQuery(*c, query) {
			continue
		}
		out = append(out, *c)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeenAt != out[j].LastSeenAt {
			return out[i].LastSeenAt > out[j].LastSeenAt
		}
		return out[i].ConversationRef < out[j].ConversationRef
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// OperationIdentity hashes the request payload facts that must stay stable
// for an operation id to be replayed.
func OperationIdentity(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// BeginOperation claims an operation id for execution.
//   - cached=true: the operation already completed with the same identity;
//     return the stored result without re-sending.
//   - err non-nil: the id was used with different input or is still running.
//   - otherwise the claim is held; the caller must CompleteOperation or
//     ReleaseOperation.
func (r *Registry) BeginOperation(key, identity string, now time.Time) (cached map[string]any, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for candidate, record := range r.operations {
		if now.Sub(record.at) >= operationTTL {
			delete(r.operations, candidate)
		}
	}
	if record, ok := r.operations[key]; ok {
		if record.identity != identity {
			return nil, fmt.Errorf("operation was already used with different input")
		}
		if !record.done {
			return nil, fmt.Errorf("operation is still in progress")
		}
		record.at = now
		return record.result, nil
	}
	r.operations[key] = &operationRecord{identity: identity, at: now}
	return nil, nil
}

// CompleteOperation stores the durable result for replays of the same id.
func (r *Registry) CompleteOperation(key string, result map[string]any, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record, ok := r.operations[key]; ok {
		record.result = result
		record.done = true
		record.at = now
	}
}

// ReleaseOperation withdraws an in-flight claim after a retryable failure so
// the same operation id can be retried.
func (r *Registry) ReleaseOperation(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record, ok := r.operations[key]; ok && !record.done {
		delete(r.operations, key)
	}
}

func turnKey(agentID, turnID string) string {
	return agentID + "\x00" + turnID
}

// RegisterTurn registers a reply waiter for conversationRef. Only one waiter
// may exist per (agentID, turnID).
func (r *Registry) RegisterTurn(agentID, turnID, conversationRef string, timeout time.Duration) (*PendingTurn, error) {
	key := turnKey(agentID, turnID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.turns[key]; ok {
		return nil, fmt.Errorf("conversation turn %s is already pending", turnID)
	}
	turn := &PendingTurn{
		registry:        r,
		key:             key,
		conversationRef: conversationRef,
		deadline:        time.Now().Add(timeout),
		replyCh:         make(chan Reply, 1),
		cancelCh:        make(chan struct{}),
	}
	r.turns[key] = turn
	r.turnsByRef[conversationRef] = append(r.turnsByRef[conversationRef], turn)
	return turn, nil
}

func (r *Registry) removeTurn(key string, turn *PendingTurn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.turns[key]; ok && current == turn {
		delete(r.turns, key)
	}
	waiters := r.turnsByRef[turn.conversationRef]
	next := waiters[:0]
	for _, candidate := range waiters {
		if candidate != turn {
			next = append(next, candidate)
		}
	}
	if len(next) == 0 {
		delete(r.turnsByRef, turn.conversationRef)
	} else {
		r.turnsByRef[turn.conversationRef] = next
	}
}

// CancelTurn cancels a pending turn waiter. Returns true when a waiter was
// still pending.
func (r *Registry) CancelTurn(agentID, turnID string) bool {
	key := turnKey(agentID, turnID)
	r.mu.Lock()
	turn, ok := r.turns[key]
	r.mu.Unlock()
	if !ok {
		return false
	}
	turn.once.Do(func() { close(turn.cancelCh) })
	return true
}

// NotifyInbound offers an inbound message to the oldest pending turn waiter
// for its conversation. Returns true when a waiter consumed the reply; the
// caller should then suppress ordinary inbound processing for that message.
func (r *Registry) NotifyInbound(conversationRef string, reply Reply) bool {
	r.mu.Lock()
	waiters := append([]*PendingTurn(nil), r.turnsByRef[conversationRef]...)
	r.mu.Unlock()
	reply.ConversationRef = conversationRef
	for _, turn := range waiters {
		select {
		case turn.replyCh <- reply:
			return true
		default:
		}
	}
	return false
}

// HasPendingTurn reports whether any turn is waiting on conversationRef.
func (r *Registry) HasPendingTurn(conversationRef string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.turnsByRef[conversationRef]) > 0
}
