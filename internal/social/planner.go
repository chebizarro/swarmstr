// Package social provides social media planning scaffolding for agents.
//
// A Planner manages a set of named plans (post, follow, engage) with
// cadence scheduling, rate limiting, and execution history.  Plans are
// persisted as memory entries so the agent can query what it has done
// and avoid duplicating actions.
//
// The planner is intentionally thin: it stores plans and history, enforces
// rate limits, and provides query helpers.  Actual execution (posting,
// following) is delegated to the agent via existing nostr tools.
package social

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// PlanType classifies a social plan.
type PlanType string

const (
	PlanPost   PlanType = "post"
	PlanFollow PlanType = "follow"
	PlanEngage PlanType = "engage"
)

// Plan describes a recurring social action.
type Plan struct {
	ID           string   `json:"id"`
	Type         PlanType `json:"type"`
	Label        string   `json:"label"`
	Schedule     string   `json:"schedule"`      // cron expression
	Instructions string   `json:"instructions"`   // what the agent should do
	Tags         []string `json:"tags,omitempty"` // e.g. ["nostr", "dev"]
	CreatedAt    int64    `json:"created_at"`
	Enabled      bool     `json:"enabled"`
}

// HistoryEntry records a single execution of a social action.
type HistoryEntry struct {
	PlanID    string   `json:"plan_id"`
	Type      PlanType `json:"type"`
	Label     string   `json:"label"`
	Action    string   `json:"action"`    // short description of what was done
	EventID   string   `json:"event_id"`  // nostr event ID if applicable
	Unix      int64    `json:"unix"`
	SessionID string   `json:"session_id,omitempty"`
}

// RateLimitConfig controls how many actions of each type can be taken
// within a sliding window.
type RateLimitConfig struct {
	PostsPerDay    int `json:"posts_per_day"`    // default 10
	FollowsPerDay  int `json:"follows_per_day"`  // default 20
	EngagesPerDay  int `json:"engages_per_day"`  // default 30
}

// DefaultRateLimitConfig returns conservative defaults.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		PostsPerDay:   10,
		FollowsPerDay: 20,
		EngagesPerDay: 30,
	}
}

// Planner manages social plans and execution history.
type Planner struct {
	mu      sync.RWMutex
	plans   map[string]*Plan
	history []HistoryEntry
	limits  RateLimitConfig
}

// NewPlanner creates a Planner with the given rate limits.
func NewPlanner(limits RateLimitConfig) *Planner {
	if limits.PostsPerDay <= 0 {
		limits.PostsPerDay = 10
	}
	if limits.FollowsPerDay <= 0 {
		limits.FollowsPerDay = 20
	}
	if limits.EngagesPerDay <= 0 {
		limits.EngagesPerDay = 30
	}
	return &Planner{
		plans:   map[string]*Plan{},
		limits:  limits,
	}
}

// AddPlan registers a new plan.  Returns an error if the ID is already taken.
func (p *Planner) AddPlan(plan Plan) error {
	if strings.TrimSpace(plan.ID) == "" {
		return fmt.Errorf("social: plan ID is required")
	}
	if plan.Type == "" {
		return fmt.Errorf("social: plan type is required (post, follow, engage)")
	}
	switch plan.Type {
	case PlanPost, PlanFollow, PlanEngage:
		// ok
	default:
		return fmt.Errorf("social: unsupported plan type %q", plan.Type)
	}
	if plan.Schedule == "" {
		return fmt.Errorf("social: plan schedule is required")
	}
	if plan.Instructions == "" {
		return fmt.Errorf("social: plan instructions are required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.plans[plan.ID]; exists {
		return fmt.Errorf("social: plan %q already exists", plan.ID)
	}
	if plan.CreatedAt == 0 {
		plan.CreatedAt = time.Now().Unix()
	}
	plan.Enabled = true
	p.plans[plan.ID] = &plan
	return nil
}

// RemovePlan removes a plan by ID.  Returns true if it existed.
func (p *Planner) RemovePlan(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.plans[id]
	delete(p.plans, id)
	return ok
}

// GetPlan returns a copy of the plan with the given ID, or nil.
func (p *Planner) GetPlan(id string) *Plan {
	p.mu.RLock()
	defer p.mu.RUnlock()
	plan, ok := p.plans[id]
	if !ok {
		return nil
	}
	cp := *plan
	return &cp
}

// ListPlans returns all plans, sorted by creation time.
func (p *Planner) ListPlans() []Plan {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Plan, 0, len(p.plans))
	for _, pl := range p.plans {
		out = append(out, *pl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// RecordAction logs a completed social action and returns an error if the
// rate limit for that action type has been exceeded in the last 24 hours.
func (p *Planner) RecordAction(entry HistoryEntry) error {
	if entry.Unix == 0 {
		entry.Unix = time.Now().Unix()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check rate limit.
	dayAgo := time.Now().Add(-24 * time.Hour).Unix()
	count := 0
	for _, h := range p.history {
		if h.Type == entry.Type && h.Unix >= dayAgo {
			count++
		}
	}
	limit := p.dailyLimit(entry.Type)
	if count >= limit {
		return fmt.Errorf("social: rate limit exceeded for %s (%d/%d in 24h)", entry.Type, count, limit)
	}

	p.history = append(p.history, entry)
	return nil
}

// RecentHistory returns the most recent history entries, newest first.
func (p *Planner) RecentHistory(limit int) []HistoryEntry {
	if limit <= 0 {
		limit = 20
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := len(p.history)
	if n > limit {
		n = limit
	}
	out := make([]HistoryEntry, n)
	for i := 0; i < n; i++ {
		out[i] = p.history[len(p.history)-1-i]
	}
	return out
}

// HistoryByType returns recent entries of a specific type.
func (p *Planner) HistoryByType(typ PlanType, limit int) []HistoryEntry {
	if limit <= 0 {
		limit = 20
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]HistoryEntry, 0, limit)
	for i := len(p.history) - 1; i >= 0 && len(out) < limit; i-- {
		if p.history[i].Type == typ {
			out = append(out, p.history[i])
		}
	}
	return out
}

// DailyUsage returns how many actions of each type have been taken in the
// last 24 hours alongside their limits.
func (p *Planner) DailyUsage() map[PlanType][2]int {
	dayAgo := time.Now().Add(-24 * time.Hour).Unix()
	p.mu.RLock()
	defer p.mu.RUnlock()
	counts := map[PlanType]int{}
	for _, h := range p.history {
		if h.Unix >= dayAgo {
			counts[h.Type]++
		}
	}
	return map[PlanType][2]int{
		PlanPost:   {counts[PlanPost], p.limits.PostsPerDay},
		PlanFollow: {counts[PlanFollow], p.limits.FollowsPerDay},
		PlanEngage: {counts[PlanEngage], p.limits.EngagesPerDay},
	}
}

// RemainingToday returns how many more actions of the given type are allowed today.
func (p *Planner) RemainingToday(typ PlanType) int {
	dayAgo := time.Now().Add(-24 * time.Hour).Unix()
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, h := range p.history {
		if h.Type == typ && h.Unix >= dayAgo {
			count++
		}
	}
	limit := p.dailyLimit(typ)
	if count >= limit {
		return 0
	}
	return limit - count
}

func (p *Planner) dailyLimit(typ PlanType) int {
	switch typ {
	case PlanPost:
		return p.limits.PostsPerDay
	case PlanFollow:
		return p.limits.FollowsPerDay
	case PlanEngage:
		return p.limits.EngagesPerDay
	default:
		return 10
	}
}

// MarshalPlans serializes all plans to JSON for persistence.
func (p *Planner) MarshalPlans() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	plans := make([]Plan, 0, len(p.plans))
	for _, pl := range p.plans {
		plans = append(plans, *pl)
	}
	return json.Marshal(plans)
}

// LoadPlans deserializes plans from JSON.
func (p *Planner) LoadPlans(data []byte) error {
	var plans []Plan
	if err := json.Unmarshal(data, &plans); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.plans == nil {
		p.plans = map[string]*Plan{}
	}
	for i := range plans {
		if strings.TrimSpace(plans[i].ID) == "" {
			continue
		}
		switch plans[i].Type {
		case PlanPost, PlanFollow, PlanEngage:
			// ok
		default:
			continue
		}
		p.plans[plans[i].ID] = &plans[i]
	}
	return nil
}

// MarshalHistory serializes execution history to JSON.
func (p *Planner) MarshalHistory() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return json.Marshal(p.history)
}

// LoadHistory deserializes execution history from JSON.
func (p *Planner) LoadHistory(data []byte) error {
	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.history = entries
	return nil
}

// CompactHistory removes entries older than the given duration.
func (p *Planner) CompactHistory(maxAge time.Duration) int {
	cutoff := time.Now().Add(-maxAge).Unix()
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := make([]HistoryEntry, 0, len(p.history))
	removed := 0
	for _, h := range p.history {
		if h.Unix >= cutoff {
			kept = append(kept, h)
		} else {
			removed++
		}
	}
	p.history = kept
	return removed
}
