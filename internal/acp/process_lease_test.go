package acp

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestProcessLeaseAcquireRenewExpire(t *testing.T) {
	now := time.Unix(100, 0)
	registry := NewProcessLeaseRegistry()
	registry.now = func() time.Time { return now }
	ctx := context.Background()

	lease, err := registry.Acquire(ctx, ProcessLease{LeaseID: "lease-a", PID: 123, SessionKey: "sess", Backend: "test"}, time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lease.AcquiredAt != now || lease.RenewedAt != now || !lease.ExpiresAt.Equal(now.Add(time.Second)) {
		t.Fatalf("unexpected acquired lease: %+v", lease)
	}
	if expired := registry.Expired(ctx); len(expired) != 0 {
		t.Fatalf("lease expired too early: %+v", expired)
	}

	now = now.Add(500 * time.Millisecond)
	renewed, err := registry.Renew(ctx, "lease-a", 2*time.Second)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if !renewed.RenewedAt.Equal(now) || !renewed.ExpiresAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("unexpected renewed lease: %+v", renewed)
	}
	now = now.Add(2 * time.Second)
	if expired := registry.Expired(ctx); len(expired) != 1 || expired[0].LeaseID != "lease-a" {
		t.Fatalf("expected expired lease-a, got %+v", expired)
	}
}

func TestProcessLeaseReapExpiredTerminatesAndCleans(t *testing.T) {
	now := time.Unix(200, 0)
	registry := NewProcessLeaseRegistry()
	registry.now = func() time.Time { return now }
	ctx := context.Background()
	_, _ = registry.Acquire(ctx, ProcessLease{LeaseID: "old", PID: 111}, time.Second)
	_, _ = registry.Acquire(ctx, ProcessLease{LeaseID: "fresh", PID: 222}, 10*time.Second)
	now = now.Add(2 * time.Second)

	var killed []int
	reaped, err := registry.ReapExpired(ctx, func(_ context.Context, lease ProcessLease) error {
		killed = append(killed, lease.PID)
		return nil
	})
	if err != nil {
		t.Fatalf("ReapExpired: %v", err)
	}
	if len(reaped) != 1 || reaped[0].LeaseID != "old" || !reflect.DeepEqual(killed, []int{111}) {
		t.Fatalf("unexpected reaped=%+v killed=%+v", reaped, killed)
	}
	leases := registry.List(ctx)
	if len(leases) != 1 || leases[0].LeaseID != "fresh" {
		t.Fatalf("unexpected remaining leases: %+v", leases)
	}
}

func TestProcessLeaseReapDoesNotReleaseRenewedSnapshot(t *testing.T) {
	now := time.Unix(250, 0)
	registry := NewProcessLeaseRegistry()
	registry.now = func() time.Time { return now }
	ctx := context.Background()
	_, _ = registry.Acquire(ctx, ProcessLease{LeaseID: "race", PID: 444}, time.Second)
	now = now.Add(2 * time.Second)

	killed := false
	reaped, err := registry.ReapExpired(ctx, func(_ context.Context, lease ProcessLease) error {
		now = now.Add(time.Millisecond)
		if _, renewErr := registry.Renew(ctx, lease.LeaseID, time.Hour); renewErr != nil {
			t.Fatalf("Renew during reap: %v", renewErr)
		}
		killed = true
		return nil
	})
	if err != nil {
		t.Fatalf("ReapExpired: %v", err)
	}
	if !killed {
		t.Fatal("expected terminator to observe expired snapshot")
	}
	if len(reaped) != 0 {
		t.Fatalf("renewed lease should not be released as reaped: %+v", reaped)
	}
	if leases := registry.List(ctx); len(leases) != 1 || leases[0].LeaseID != "race" {
		t.Fatalf("renewed lease should remain: %+v", leases)
	}
}

func TestProcessLeaseStartReaper(t *testing.T) {
	now := time.Unix(300, 0)
	registry := NewProcessLeaseRegistry()
	registry.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = registry.Acquire(ctx, ProcessLease{LeaseID: "expired", PID: 333}, time.Millisecond)
	now = now.Add(time.Second)

	reaped := make(chan string, 1)
	done := registry.StartReaper(ctx, ProcessReaperOptions{
		Interval: time.Hour,
		Terminator: func(_ context.Context, lease ProcessLease) error {
			reaped <- lease.LeaseID
			return nil
		},
	})
	select {
	case id := <-reaped:
		if id != "expired" {
			t.Fatalf("reaped %q, want expired", id)
		}
	case <-time.After(time.Second):
		t.Fatal("reaper did not reap expired lease")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reaper did not stop")
	}
}
