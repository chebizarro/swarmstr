package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type CompletionEnvelope struct {
	IdempotencyKey      string      `json:"idempotency_key"`
	RunID               string      `json:"run_id"`
	Generation          int         `json:"generation"`
	ChildSessionKey     string      `json:"child_session_key"`
	RequesterSessionKey string      `json:"requester_session_key"`
	Outcome             *RunOutcome `json:"outcome,omitempty"`
}

type CompletionAnnouncer interface {
	AnnounceCompletion(context.Context, CompletionEnvelope) error
}

type CompletionDeliveryWorker struct {
	Registry  *Registry
	Announcer CompletionAnnouncer
	// Wake is an event-driven delivery signal. Closing or sending on it requests
	// a due-completion pass; no polling timer is used for message delivery.
	Wake        <-chan struct{}
	Lease       time.Duration
	MaxAttempts int
}

func (w *CompletionDeliveryWorker) Run(ctx context.Context) error {
	if w == nil || w.Registry == nil || w.Announcer == nil {
		return fmt.Errorf("completion delivery worker is not configured")
	}
	if err := w.DeliverDue(ctx); err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if w.Wake == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-w.Wake:
			if !ok {
				return nil
			}
			// Individual failures are persisted; a later lifecycle/reconnect event
			// wakes another due pass without polling the relay or registry.
			_ = w.DeliverDue(ctx)
		}
	}
}

func (w *CompletionDeliveryWorker) DeliverDue(ctx context.Context) error {
	now := time.Now()
	leaseDuration := w.Lease
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}
	var firstErr error
	for _, rec := range w.Registry.DueCompletions(now, 100) {
		leaseID := randomLeaseID()
		if err := w.Registry.MarkDeliveryInProgress(rec.RunID, leaseID, now.Add(leaseDuration)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		envelope := CompletionEnvelope{IdempotencyKey: fmt.Sprintf("subagent:%s:%d", rec.RunID, rec.Generation), RunID: rec.RunID, Generation: rec.Generation, ChildSessionKey: rec.ChildSessionKey, RequesterSessionKey: rec.RequesterSessionKey, Outcome: rec.Completion.Outcome}
		if err := w.Announcer.AnnounceCompletion(ctx, envelope); err != nil {
			_ = w.Registry.MarkDeliveryFailed(rec.RunID, leaseID, err, w.MaxAttempts)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := w.Registry.MarkDeliveryDelivered(rec.RunID, leaseID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func randomLeaseID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
