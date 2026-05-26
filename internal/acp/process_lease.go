package acp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProcessLease tracks ownership of a backend/worker process without assuming
// that the ACP manager itself spawned it. Reapers can use Expired leases as
// cleanup candidates while backends keep renewing active leases.
type ProcessLease struct {
	LeaseID    string    `json:"lease_id"`
	PID        int       `json:"pid"`
	SessionKey string    `json:"session_key,omitempty"`
	Backend    string    `json:"backend,omitempty"`
	Owner      string    `json:"owner,omitempty"`
	AcquiredAt time.Time `json:"acquired_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (l ProcessLease) Expired(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && !now.Before(l.ExpiresAt)
}

type ProcessLeaseRegistry struct {
	mu     sync.RWMutex
	now    func() time.Time
	leases map[string]ProcessLease
}

// ProcessTerminator terminates the process associated with an expired lease.
// Tests and non-process backends can inject a fake terminator; production
// callers can use DefaultProcessTerminator.
type ProcessTerminator func(ctx context.Context, lease ProcessLease) error

// ProcessReaperOptions controls the orphan lease reaper goroutine.
type ProcessReaperOptions struct {
	// Interval controls how often expired leases are scanned. Defaults to 30s.
	Interval time.Duration
	// Terminator kills or otherwise cleans up the leased process. Defaults to
	// DefaultProcessTerminator.
	Terminator ProcessTerminator
	// OnError receives termination failures. Reaped leases are only released
	// after a successful Terminator call.
	OnError func(ProcessLease, error)
}

func NewProcessLeaseRegistry() *ProcessLeaseRegistry {
	return &ProcessLeaseRegistry{now: time.Now, leases: make(map[string]ProcessLease)}
}

func (r *ProcessLeaseRegistry) Acquire(_ context.Context, lease ProcessLease, ttl time.Duration) (ProcessLease, error) {
	lease.LeaseID = strings.TrimSpace(lease.LeaseID)
	if lease.LeaseID == "" {
		lease.LeaseID = "lease-" + strings.TrimPrefix(GenerateTaskID(), "task-")
	}
	if lease.PID <= 0 {
		return ProcessLease{}, fmt.Errorf("acp process lease: pid required")
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	now := r.now()
	lease.AcquiredAt = now
	lease.RenewedAt = now
	lease.ExpiresAt = now.Add(ttl)
	r.mu.Lock()
	r.leases[lease.LeaseID] = lease
	r.mu.Unlock()
	return lease, nil
}

func (r *ProcessLeaseRegistry) Renew(_ context.Context, leaseID string, ttl time.Duration) (ProcessLease, error) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	lease, ok := r.leases[strings.TrimSpace(leaseID)]
	if !ok {
		return ProcessLease{}, fmt.Errorf("acp process lease: lease %q not found", leaseID)
	}
	now := r.now()
	lease.RenewedAt = now
	lease.ExpiresAt = now.Add(ttl)
	r.leases[lease.LeaseID] = lease
	return lease, nil
}

func (r *ProcessLeaseRegistry) Release(_ context.Context, leaseID string) {
	r.mu.Lock()
	delete(r.leases, strings.TrimSpace(leaseID))
	r.mu.Unlock()
}

// ReapExpired terminates and releases all currently expired leases. A lease is
// removed only after the terminator succeeds, so transient kill failures remain
// visible for a later reaper pass.
func (r *ProcessLeaseRegistry) ReapExpired(ctx context.Context, terminator ProcessTerminator) ([]ProcessLease, error) {
	if r == nil {
		return nil, nil
	}
	if terminator == nil {
		terminator = DefaultProcessTerminator
	}
	expired := r.Expired(ctx)
	reaped := make([]ProcessLease, 0, len(expired))
	var firstErr error
	for _, lease := range expired {
		if ctx.Err() != nil {
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			break
		}
		live, ok := r.expiredLeaseIfUnchanged(lease)
		if !ok {
			continue
		}
		if err := terminator(ctx, live); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if r.releaseIfUnchanged(live) {
			reaped = append(reaped, live)
		}
	}
	return reaped, firstErr
}

// StartReaper launches an orphan reaper goroutine. It performs one immediate
// scan, then scans on each tick until ctx is cancelled. The returned channel is
// closed when the goroutine exits.
func (r *ProcessLeaseRegistry) StartReaper(ctx context.Context, opts ProcessReaperOptions) <-chan struct{} {
	done := make(chan struct{})
	if r == nil {
		close(done)
		return done
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	terminator := opts.Terminator
	if terminator == nil {
		terminator = DefaultProcessTerminator
	}
	go func() {
		defer close(done)
		reap := func() {
			if _, err := r.ReapExpired(ctx, terminator); err != nil && opts.OnError != nil {
				for _, lease := range r.Expired(ctx) {
					opts.OnError(lease, err)
					break
				}
			}
		}
		reap()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reap()
			}
		}
	}()
	return done
}

// DefaultProcessTerminator kills the leased PID. It intentionally does not
// wait, because the registry may track processes it did not spawn.
func DefaultProcessTerminator(ctx context.Context, lease ProcessLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lease.PID <= 0 {
		return fmt.Errorf("acp process lease: pid required")
	}
	proc, err := os.FindProcess(lease.PID)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func (r *ProcessLeaseRegistry) expiredLeaseIfUnchanged(snapshot ProcessLease) (ProcessLease, bool) {
	now := r.now()
	r.mu.RLock()
	live, ok := r.leases[snapshot.LeaseID]
	r.mu.RUnlock()
	if !ok {
		return ProcessLease{}, false
	}
	if !live.ExpiresAt.Equal(snapshot.ExpiresAt) || !live.RenewedAt.Equal(snapshot.RenewedAt) || live.PID != snapshot.PID {
		return ProcessLease{}, false
	}
	return live, live.Expired(now)
}

func (r *ProcessLeaseRegistry) releaseIfUnchanged(snapshot ProcessLease) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	live, ok := r.leases[snapshot.LeaseID]
	if !ok {
		return false
	}
	if !live.ExpiresAt.Equal(snapshot.ExpiresAt) || !live.RenewedAt.Equal(snapshot.RenewedAt) || live.PID != snapshot.PID {
		return false
	}
	delete(r.leases, snapshot.LeaseID)
	return true
}

func (r *ProcessLeaseRegistry) Expired(_ context.Context) []ProcessLease {
	now := r.now()
	r.mu.RLock()
	out := make([]ProcessLease, 0)
	for _, lease := range r.leases {
		if lease.Expired(now) {
			out = append(out, lease)
		}
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	return out
}

func (r *ProcessLeaseRegistry) List(_ context.Context) []ProcessLease {
	r.mu.RLock()
	out := make([]ProcessLease, 0, len(r.leases))
	for _, lease := range r.leases {
		out = append(out, lease)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LeaseID < out[j].LeaseID })
	return out
}

func (r *ProcessLeaseRegistry) Stats(ctx context.Context) ProcessLeaseStats {
	leases := r.List(ctx)
	expired := 0
	now := r.now()
	for _, lease := range leases {
		if lease.Expired(now) {
			expired++
		}
	}
	return ProcessLeaseStats{Total: len(leases), Expired: expired}
}

type ProcessLeaseStats struct {
	Total   int `json:"total"`
	Expired int `json:"expired"`
}
