package commitments

import "time"

// Delivery is a due reminder surfaced by the heartbeat scheduler.
type Delivery struct {
	Commitment Commitment
	Channel    string
	To         string
	SessionID  string
	Text       string
}

// HeartbeatScheduler selects pending commitments for reminder delivery.
type HeartbeatScheduler struct {
	Store  *Store
	Config Config
	Now    func() time.Time
}

func (h HeartbeatScheduler) Due(sessionID string) []Delivery {
	cfg := h.Config.withDefaults()
	if !cfg.Enabled || h.Store == nil {
		return nil
	}
	now := h.now()
	remaining := cfg.DailyLimit - h.sentToday(sessionID, now)
	if remaining <= 0 {
		return nil
	}
	limit := cfg.MaxPerHeartbeat
	if limit > remaining {
		limit = remaining
	}
	var out []Delivery
	for _, c := range h.Store.List(sessionID, StatusPending) {
		if len(out) >= limit {
			break
		}
		if !isDue(c, now, cfg) {
			continue
		}
		out = append(out, Delivery{Commitment: c, Channel: c.Channel, To: c.To, SessionID: firstNonEmpty(c.DeliverySessionID, c.SessionID), Text: c.Text})
	}
	return out
}

func (h HeartbeatScheduler) MarkAttempted(id string) error {
	now := h.now()
	cfg := h.Config.withDefaults()
	h.Store.mu.Lock()
	defer h.Store.mu.Unlock()
	c, ok := h.Store.commitments[id]
	if !ok {
		return nil
	}
	c.Attempts++
	c.LastAttemptAt = now
	c.NextAttemptAt = now.Add(cfg.AttemptBackoff * time.Duration(max(1, c.Attempts)))
	c.UpdatedAt = now
	h.Store.commitments[id] = c
	return h.Store.saveLocked()
}

func (h HeartbeatScheduler) MarkSent(id string) error {
	now := h.now()
	h.Store.mu.Lock()
	defer h.Store.mu.Unlock()
	c, ok := h.Store.commitments[id]
	if !ok {
		return nil
	}
	c.Status = StatusFulfilled
	c.SentAt = now
	c.UpdatedAt = now
	h.Store.commitments[id] = c
	return h.Store.saveLocked()
}

func (h HeartbeatScheduler) sentToday(sessionID string, now time.Time) int {
	count := 0
	since := now.Add(-24 * time.Hour)
	for _, c := range h.Store.List(sessionID, StatusFulfilled) {
		if !c.SentAt.IsZero() && !c.SentAt.Before(since) {
			count++
		}
	}
	return count
}

func (h HeartbeatScheduler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func isDue(c Commitment, now time.Time, cfg Config) bool {
	if !c.NextAttemptAt.IsZero() && now.Before(c.NextAttemptAt) {
		return false
	}
	if c.DueAt.IsZero() {
		return true
	}
	return !now.Before(c.DueAt) && !now.After(c.DueAt.Add(cfg.DueWindow))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
