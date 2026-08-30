package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/commitments"
	"metiq/internal/gateway/channels"
	metricspkg "metiq/internal/metrics"
)

type nostrCommitmentService struct {
	store *commitments.Store
	now   func() time.Time
}

var controlCommitmentService *nostrCommitmentService

func newNostrCommitmentService(path string) (*nostrCommitmentService, error) {
	store, err := commitments.NewFileStore(path)
	if err != nil {
		return nil, err
	}
	return &nostrCommitmentService{store: store, now: time.Now}, nil
}

func (s *nostrCommitmentService) resolve(ctx context.Context, req channels.CommitmentBackingRequest) (channels.CommitmentBackingResolution, error) {
	if s == nil || s.store == nil {
		return channels.CommitmentBackingResolution{}, fmt.Errorf("commitment store unavailable")
	}
	roomKey := strings.TrimSpace(req.RoomKey)
	seen := map[string]struct{}{}
	live := make([]string, 0, len(req.References))
	for _, raw := range req.References {
		reference := strings.ToLower(strings.TrimSpace(raw))
		if reference == "" {
			continue
		}
		var valid bool
		switch {
		case strings.HasPrefix(reference, "flow:"):
			flowID := strings.TrimSpace(strings.TrimPrefix(reference, "flow:"))
			if controlACPFlowRegistry != nil && flowID != "" {
				flow, err := controlACPFlowRegistry.Get(ctx, flowID)
				valid = err == nil && flow != nil && !flow.Status.Terminal() &&
					strings.TrimSpace(flow.OwnerSessionKey) == roomKey
			}
		case strings.HasPrefix(reference, "task:"):
			taskID := strings.TrimSpace(strings.TrimPrefix(reference, "task:"))
			if controlServices != nil && controlServices.tasks.fleetTaskBridge != nil && taskID != "" {
				if view, ok := controlServices.tasks.fleetTaskBridge.FleetTaskView(taskID); ok {
					valid = !terminalFleetTaskStatus(view.Task.Status)
				}
			}
		}
		if !valid {
			continue
		}
		if _, ok := seen[reference]; ok {
			continue
		}
		seen[reference] = struct{}{}
		live = append(live, reference)
	}
	if len(live) == 0 {
		return channels.CommitmentBackingResolution{}, nil
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	before := s.store.PendingCount(roomKey)
	commitment := commitments.Commitment{
		SessionID:         roomKey,
		TurnID:            strings.TrimSpace(req.TurnID),
		Kind:              commitments.KindOpenLoop,
		Text:              strings.TrimSpace(req.Text),
		Source:            "live_backing",
		Status:            commitments.StatusPending,
		DueAt:             now.Add(30 * time.Minute),
		CreatedAt:         now,
		UpdatedAt:         now,
		Channel:           "nostr-room",
		To:                roomKey,
		DeliverySessionID: roomKey,
		Confidence:        1,
		BackingReferences: live,
	}
	if err := s.store.AddE(commitment); err != nil {
		return channels.CommitmentBackingResolution{}, err
	}
	after := s.store.PendingCount(roomKey)
	if after > before {
		metricspkg.RecordRoomSignal(roomKey, metricspkg.RoomSignalCommitmentOpened)
	}
	metricspkg.SetRoomOpenCommitments(roomKey, after)
	return channels.CommitmentBackingResolution{LiveReferences: live}, nil
}

func flowTerminalReason(status, transitionError, cancellationReason string) string {
	for _, value := range []string{transitionError, cancellationReason, status} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "flow ended"
}

func terminalFleetTaskStatus(status string) bool {
	return completedFleetTaskStatus(status) || droppedFleetTaskStatus(status)
}

func completedFleetTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "completed", "complete", "done":
		return true
	default:
		return false
	}
}

func droppedFleetTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "cancelled", "canceled", "failed", "rejected":
		return true
	default:
		return false
	}
}

func (s *nostrCommitmentService) deliverDropped(ctx context.Context, registry *channels.Registry) {
	if s == nil || s.store == nil || registry == nil {
		return
	}
	sessions := map[string]struct{}{}
	for _, status := range []commitments.Status{commitments.StatusPending, commitments.StatusExpired, commitments.StatusBroken} {
		for _, commitment := range s.store.List("", status) {
			sessions[commitment.SessionID] = struct{}{}
		}
	}
	scheduler := commitments.HeartbeatScheduler{
		Store: s.store,
		Config: commitments.Config{
			Enabled: true, DroppedCommitmentNotices: true,
			DueWindow: time.Nanosecond, MaxPerHeartbeat: 8,
		},
	}
	for sessionID := range sessions {
		for _, delivery := range scheduler.Due(sessionID) {
			if delivery.Kind != commitments.DeliveryDroppedCommitment {
				continue
			}
			channel, ok := taskFlowRoomChannel(registry, sessionID, "")
			if !ok {
				continue
			}
			if err := channel.Send(ctx, delivery.Text); err != nil {
				_ = scheduler.MarkAttempted(delivery.Commitment.ID)
				log.Printf("dropped commitment notice failed room=%s commitment=%s: %v", sessionID, delivery.Commitment.ID, err)
				continue
			}
			if err := scheduler.MarkDelivered(delivery); err != nil {
				log.Printf("dropped commitment notice acknowledgement failed room=%s commitment=%s: %v", sessionID, delivery.Commitment.ID, err)
				continue
			}
			metricspkg.SetRoomOpenCommitments(sessionID, s.store.PendingCount(sessionID))
		}
	}
}

func (s *nostrCommitmentService) startHeartbeat(ctx context.Context, registry *channels.Registry) {
	if s == nil || registry == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		s.deliverDropped(ctx, registry)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.deliverDropped(ctx, registry)
			}
		}
	}()
}

// resolveBacking closes every pending promise correlated to a task/flow handle.
func (s *nostrCommitmentService) resolveBacking(reference string, completed bool, reason string) error {
	if s == nil || s.store == nil {
		return nil
	}
	status := commitments.StatusBroken
	if completed {
		status = commitments.StatusFulfilled
	}
	changed, err := s.store.ResolveBacking(reference, status, reason, time.Now().UTC())
	if err != nil {
		return err
	}
	rooms := map[string]struct{}{}
	for _, commitment := range changed {
		rooms[commitment.SessionID] = struct{}{}
		if completed {
			metricspkg.RecordRoomSignal(commitment.SessionID, metricspkg.RoomSignalCommitmentCompleted)
		} else {
			metricspkg.RecordRoomSignal(commitment.SessionID, metricspkg.RoomSignalCommitmentDropped)
		}
		commitment.LifecycleRecorded = true
		if err := s.store.AddE(commitment); err != nil {
			return err
		}
	}
	for room := range rooms {
		metricspkg.SetRoomOpenCommitments(room, s.store.PendingCount(room))
	}
	return nil
}
