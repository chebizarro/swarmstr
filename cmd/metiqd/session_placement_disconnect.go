package main

import (
	"context"
	"time"

	"metiq/internal/gateway/sessioncoord"
)

const (
	disconnectReclaimAttempts = 3
	disconnectReclaimTimeout  = 5 * time.Second
	disconnectReclaimBackoff  = 100 * time.Millisecond
)

// reclaimDisconnectedSessionPlacements detaches placement cleanup from the
// connection lifecycle and retries transient persistence failures. The
// coordinator generation and owner checks make each retry idempotent and keep a
// stale disconnect from reclaiming a newer placement.
func reclaimDisconnectedSessionPlacements(ctx context.Context, coordinator *sessioncoord.Service, connectionID string) []error {
	if coordinator == nil || connectionID == "" {
		return nil
	}
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	var errs []error
	for attempt := 0; attempt < disconnectReclaimAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(baseCtx, disconnectReclaimTimeout)
		errs = coordinator.ReclaimConnection(attemptCtx, connectionID)
		cancel()
		if len(errs) == 0 {
			return nil
		}
		if attempt+1 < disconnectReclaimAttempts {
			time.Sleep(disconnectReclaimBackoff << attempt)
		}
	}
	return errs
}
