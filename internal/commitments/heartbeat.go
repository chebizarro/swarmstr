package commitments

import (
	"strings"
	"time"

	metricspkg "metiq/internal/metrics"
)

type DeliveryKind string

const (
	DeliveryReminder          DeliveryKind = "reminder"
	DeliveryDroppedCommitment DeliveryKind = "dropped_commitment"
)

// Delivery is a reminder or dropped-commitment notice surfaced by the heartbeat
// scheduler for the channel delivery pipeline.
type Delivery struct {
	Kind       DeliveryKind
	Commitment Commitment
	Channel    string
	To         string
	SessionID  string
	Text       string
	DropReason string
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
	out := make([]Delivery, 0, cfg.MaxPerHeartbeat)

	// Dropped notices bypass the reminder daily limit: silently losing the
	// convention notice would defeat its purpose. They remain bounded by the
	// per-heartbeat delivery cap and repeat until MarkDelivered succeeds.
	if cfg.DroppedCommitmentNotices {
		for _, c := range h.Store.List(sessionID, StatusExpired) {
			if len(out) >= cfg.MaxPerHeartbeat {
				return out
			}
			if !c.DroppedNoticeAt.IsZero() {
				continue
			}
			out = append(out, droppedDelivery(c, firstNonEmpty(c.BrokenReason, "expired before completion")))
		}
		for _, c := range h.Store.List(sessionID, StatusPending) {
			if len(out) >= cfg.MaxPerHeartbeat {
				return out
			}
			if reason := droppedReason(c, now, cfg); reason != "" {
				out = append(out, droppedDelivery(c, reason))
			}
		}
	}

	remaining := cfg.DailyLimit - h.sentToday(sessionID, now)
	if remaining <= 0 {
		return out
	}
	limit := cfg.MaxPerHeartbeat - len(out)
	if limit > remaining {
		limit = remaining
	}
	if limit <= 0 {
		return out
	}
	for _, c := range h.Store.List(sessionID, StatusPending) {
		if len(out) >= cfg.MaxPerHeartbeat {
			break
		}
		if cfg.DroppedCommitmentNotices && droppedReason(c, now, cfg) != "" {
			continue
		}
		if !isDue(c, now, cfg) {
			continue
		}
		out = append(out, Delivery{
			Kind:       DeliveryReminder,
			Commitment: c,
			Channel:    c.Channel,
			To:         c.To,
			SessionID:  firstNonEmpty(c.DeliverySessionID, c.SessionID),
			Text:       c.Text,
		})
		limit--
		if limit == 0 {
			break
		}
	}
	return out
}

func droppedDelivery(c Commitment, reason string) Delivery {
	return Delivery{
		Kind:       DeliveryDroppedCommitment,
		Commitment: c,
		Channel:    c.Channel,
		To:         c.To,
		SessionID:  firstNonEmpty(c.DeliverySessionID, c.SessionID),
		Text:       DroppedCommitmentNotice(c, reason),
		DropReason: reason,
	}
}

// DroppedCommitmentNotice formats the visible one-line Cohen & Levesque
// convention notice. The commitment text is whitespace-normalized and bounded.
func DroppedCommitmentNotice(c Commitment, reason string) string {
	text := normalizeWhitespace(c.Text)
	if text == "" {
		text = c.ID
	}
	runes := []rune(text)
	if len(runes) > 120 {
		text = string(runes[:117]) + "..."
	}
	reason = strings.TrimSpace(strings.TrimSuffix(reason, "."))
	if reason == "" {
		reason = "expired before completion"
	}
	return "Dropped commitment: " + text + " — " + reason + "."
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

// MarkDelivered records successful delivery of either a reminder or a dropped
// notice. Dropped notices transition pending records to expired and persist a
// sent marker so the room is notified exactly once after successful delivery.
func (h HeartbeatScheduler) MarkDelivered(delivery Delivery) error {
	now := h.now()
	h.Store.mu.Lock()
	defer h.Store.mu.Unlock()
	c, ok := h.Store.commitments[delivery.Commitment.ID]
	if !ok {
		return nil
	}
	if delivery.Kind == DeliveryDroppedCommitment {
		c.Status = StatusExpired
		c.BrokenReason = firstNonEmpty(delivery.DropReason, c.BrokenReason, "expired before completion")
		c.DroppedNoticeAt = now
		// R7 scorecard: one commitment dropped (notice delivered) for this room.
		metricspkg.RecordRoomSignal(firstNonEmpty(delivery.SessionID, c.SessionID), metricspkg.RoomSignalCommitmentDropped)
	} else {
		c.Status = StatusFulfilled
	}
	c.SentAt = now
	c.UpdatedAt = now
	h.Store.commitments[c.ID] = c
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

func droppedReason(c Commitment, now time.Time, cfg Config) string {
	if c.Status == StatusExpired {
		return firstNonEmpty(c.BrokenReason, "expired before completion")
	}
	if !c.DueAt.IsZero() && now.After(c.DueAt.Add(cfg.DueWindow)) {
		return "expired before completion"
	}
	if cfg.MaxDeliveryAttempts > 0 && c.Attempts >= cfg.MaxDeliveryAttempts {
		return "delivery attempts exhausted"
	}
	return ""
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
