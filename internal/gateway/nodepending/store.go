package nodepending

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Action is a queued node-side pending action.
type Action struct {
	ID             string         `json:"id"`
	NodeID         string         `json:"node_id"`
	Command        string         `json:"command"`
	Args           map[string]any `json:"args,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	EnqueuedAtMS   int64          `json:"enqueued_at_ms"`
	ExpiresAtMS    int64          `json:"expires_at_ms,omitempty"`
}

type EnqueueRequest struct {
	NodeID         string
	Command        string
	Args           map[string]any
	IdempotencyKey string
	TTLMS          int
}

type DrainRequest struct {
	NodeID   string
	MaxItems int
}

type AckRequest struct {
	NodeID string
	IDs    []string
}

type Store struct {
	mu       sync.Mutex
	byNode   map[string][]Action
	revision map[string]int64
}

func New() *Store {
	return &Store{byNode: map[string][]Action{}, revision: map[string]int64{}}
}

func (s *Store) Enqueue(req EnqueueRequest) (map[string]any, error) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("node_id is required")
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return nil, fmt.Errorf("command is required")
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.pruneLocked(nodeID, now)

	idempotency := strings.TrimSpace(req.IdempotencyKey)
	if idempotency != "" {
		for _, it := range items {
			if it.IdempotencyKey == idempotency {
				return map[string]any{
					"node_id":  nodeID,
					"revision": s.revision[nodeID],
					"queued":   it,
					"deduped":  true,
				}, nil
			}
		}
	}

	id := fmt.Sprintf("npw-%d", now)
	a := Action{
		ID:             id,
		NodeID:         nodeID,
		Command:        cmd,
		Args:           req.Args,
		IdempotencyKey: idempotency,
		EnqueuedAtMS:   now,
	}
	if req.TTLMS > 0 {
		a.ExpiresAtMS = now + int64(req.TTLMS)
	}
	items = append(items, a)
	s.byNode[nodeID] = items
	s.revision[nodeID]++
	return map[string]any{
		"node_id":  nodeID,
		"revision": s.revision[nodeID],
		"queued":   a,
		"deduped":  false,
	}, nil
}

func (s *Store) Pull(nodeID string) (map[string]any, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("node_id is required")
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.pruneLocked(nodeID, now)
	return map[string]any{
		"node_id": nodeID,
		"actions": append([]Action(nil), items...),
	}, nil
}

func (s *Store) Ack(req AckRequest) (map[string]any, error) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("node_id is required")
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.pruneLocked(nodeID, now)
	if len(req.IDs) == 0 {
		return map[string]any{
			"node_id":       nodeID,
			"acked_ids":     []string{},
			"remaining_count": len(items),
		}, nil
	}
	set := map[string]struct{}{}
	for _, id := range req.IDs {
		id = strings.TrimSpace(id)
		if id != "" {
			set[id] = struct{}{}
		}
	}
	out := make([]Action, 0, len(items))
	for _, a := range items {
		if _, ok := set[a.ID]; ok {
			continue
		}
		out = append(out, a)
	}
	s.byNode[nodeID] = out
	s.revision[nodeID]++
	acked := make([]string, 0, len(set))
	for id := range set {
		acked = append(acked, id)
	}
	return map[string]any{
		"node_id":         nodeID,
		"acked_ids":       acked,
		"remaining_count": len(out),
		"revision":        s.revision[nodeID],
	}, nil
}

func (s *Store) Drain(req DrainRequest) (map[string]any, error) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("node_id is required")
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.pruneLocked(nodeID, now)
	max := req.MaxItems
	if max <= 0 || max > len(items) {
		max = len(items)
	}
	drained := append([]Action(nil), items[:max]...)
	rest := append([]Action(nil), items[max:]...)
	s.byNode[nodeID] = rest
	s.revision[nodeID]++
	return map[string]any{
		"node_id":        nodeID,
		"actions":        drained,
		"drained_count":  len(drained),
		"remaining_count": len(rest),
		"revision":       s.revision[nodeID],
	}, nil
}

func (s *Store) pruneLocked(nodeID string, nowMS int64) []Action {
	items := s.byNode[nodeID]
	if len(items) == 0 {
		return nil
	}
	out := items[:0]
	for _, a := range items {
		if a.ExpiresAtMS > 0 && a.ExpiresAtMS <= nowMS {
			continue
		}
		out = append(out, a)
	}
	cloned := append([]Action(nil), out...)
	s.byNode[nodeID] = cloned
	return cloned
}
