package suspend

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestPrepareResumeStateMachine(t *testing.T) {
	c := NewCoordinator()
	if got := c.State().State; got != StateIdle {
		t.Fatalf("initial state = %q, want idle", got)
	}
	if !c.AcceptingWork() {
		t.Fatal("idle coordinator should accept work")
	}

	rec, err := c.Prepare("maintenance")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if rec.State != StateSuspended {
		t.Fatalf("prepared state = %q, want suspended", rec.State)
	}
	if rec.SuspensionID == "" {
		t.Fatal("prepare must allocate a suspension id")
	}
	if rec.SinceMs == 0 {
		t.Fatal("prepare must stamp sinceMs")
	}
	if rec.Reason != "maintenance" {
		t.Fatalf("reason = %q, want maintenance", rec.Reason)
	}
	if c.AcceptingWork() {
		t.Fatal("suspended coordinator must NOT accept work")
	}
	if !c.Suspended() {
		t.Fatal("Suspended() should be true after prepare")
	}

	resumed, err := c.Resume(rec.SuspensionID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != StateIdle {
		t.Fatalf("resumed state = %q, want idle", resumed.State)
	}
	if !c.AcceptingWork() {
		t.Fatal("resumed coordinator should accept work again")
	}
}

func TestPrepareIdempotent(t *testing.T) {
	c := NewCoordinator()
	first, err := c.Prepare("a")
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	second, err := c.Prepare("b")
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if second.SuspensionID != first.SuspensionID {
		t.Fatalf("prepare not idempotent: ids %q != %q", second.SuspensionID, first.SuspensionID)
	}
	if second.Reason != first.Reason {
		t.Fatalf("idempotent prepare changed reason: %q != %q", second.Reason, first.Reason)
	}
}

func TestResumeRejectsWhenNotSuspended(t *testing.T) {
	c := NewCoordinator()
	if _, err := c.Resume(""); err == nil {
		t.Fatal("resume on idle coordinator must be rejected")
	}
	// After a full cycle it is idle again and resume must reject.
	rec, _ := c.Prepare("x")
	if _, err := c.Resume(rec.SuspensionID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := c.Resume(rec.SuspensionID); err == nil {
		t.Fatal("second resume must be rejected (already idle)")
	}
}

func TestResumeValidatesSuspensionID(t *testing.T) {
	c := NewCoordinator()
	rec, _ := c.Prepare("x")
	if _, err := c.Resume("not-the-right-id"); err == nil {
		t.Fatal("resume with mismatched id must be rejected")
	}
	if !c.Suspended() {
		t.Fatal("rejected resume must leave the suspension intact")
	}
	if _, err := c.Resume(rec.SuspensionID); err != nil {
		t.Fatalf("resume with correct id: %v", err)
	}
	// Empty id resumes the active suspension unconditionally.
	rec2, _ := c.Prepare("y")
	_ = rec2
	if _, err := c.Resume(""); err != nil {
		t.Fatalf("resume with empty id should resume active suspension: %v", err)
	}
}

func TestDurabilitySurvivesRestartMidSuspension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suspend-ledger.json")

	c1, err := NewCoordinatorAt(path)
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	rec, err := c1.Prepare("host hibernation")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Simulate a crash/restart mid-suspension: a brand-new coordinator loads
	// the persisted ledger.
	c2, err := NewCoordinatorAt(path)
	if err != nil {
		t.Fatalf("reload coordinator: %v", err)
	}
	got := c2.State()
	if got.State != StateSuspended {
		t.Fatalf("recovered state = %q, want suspended", got.State)
	}
	if got.SuspensionID != rec.SuspensionID {
		t.Fatalf("recovered suspension id = %q, want %q", got.SuspensionID, rec.SuspensionID)
	}
	if got.Reason != "host hibernation" {
		t.Fatalf("recovered reason = %q", got.Reason)
	}
	if c2.AcceptingWork() {
		t.Fatal("recovered-suspended coordinator must stay gated (not accept work)")
	}

	// The recovered coordinator can resume normally.
	if _, err := c2.Resume(rec.SuspensionID); err != nil {
		t.Fatalf("resume after recovery: %v", err)
	}

	// A third load sees idle persisted.
	c3, err := NewCoordinatorAt(path)
	if err != nil {
		t.Fatalf("third load: %v", err)
	}
	if c3.State().State != StateIdle {
		t.Fatalf("after resume, reloaded state = %q, want idle", c3.State().State)
	}
	if !c3.AcceptingWork() {
		t.Fatal("after resume, reloaded coordinator should accept work")
	}
}

func TestRecoveryNormalizesPreparingToSuspended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suspend-ledger.json")
	c := &Coordinator{rec: Record{State: StateIdle}, storagePath: path, now: func() int64 { return 42 }}
	// Persist a raw "preparing" record (as if the daemon crashed between the two
	// persist phases of Prepare).
	if err := c.persistLocked(Record{State: StatePreparing, SuspensionID: "suspend-1-1", SinceMs: 10}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	reloaded, err := NewCoordinatorAt(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.State().State != StateSuspended {
		t.Fatalf("preparing should recover as suspended, got %q", reloaded.State().State)
	}
	if reloaded.AcceptingWork() {
		t.Fatal("recovered-preparing coordinator must stay gated")
	}
}

func TestRecoveryNormalizesResumingToIdle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suspend-ledger.json")
	c := &Coordinator{rec: Record{State: StateIdle}, storagePath: path, now: func() int64 { return 42 }}
	if err := c.persistLocked(Record{State: StateResuming, SuspensionID: "suspend-1-1", SinceMs: 10}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	reloaded, err := NewCoordinatorAt(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.State().State != StateIdle {
		t.Fatalf("resuming should recover as idle, got %q", reloaded.State().State)
	}
	if !reloaded.AcceptingWork() {
		t.Fatal("recovered-resuming coordinator should accept work")
	}
}

func TestPausableWorkerRegistry(t *testing.T) {
	c := NewCoordinator()
	c.RegisterPausableWorker("cron-scheduler")
	c.RegisterPausableWorker("dreaming-job")
	c.RegisterPausableWorker("cron-scheduler") // dup ignored
	c.RegisterPausableWorker("")               // ignored
	got := c.PausableWorkers()
	if len(got) != 2 {
		t.Fatalf("pausable workers = %v, want 2 unique", got)
	}
	if got[0] != "cron-scheduler" || got[1] != "dreaming-job" {
		t.Fatalf("pausable workers not sorted/deduped: %v", got)
	}
}

// TestGateGatesDispatch models a background dispatcher that consults the gate: a
// scheduled op is refused while suspended and accepted after resume.
func TestGateGatesDispatch(t *testing.T) {
	c := NewCoordinator()
	dispatched := 0
	tryDispatch := func() bool {
		if !c.AcceptingWork() {
			return false // deferred: dispatcher skips this tick
		}
		dispatched++
		return true
	}

	if !tryDispatch() {
		t.Fatal("dispatch should succeed while idle")
	}
	rec, _ := c.Prepare("pause")
	if tryDispatch() {
		t.Fatal("dispatch must be refused while suspended")
	}
	if dispatched != 1 {
		t.Fatalf("dispatched=%d, expected the suspended dispatch to be refused", dispatched)
	}
	if _, err := c.Resume(rec.SuspensionID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !tryDispatch() {
		t.Fatal("dispatch should succeed again after resume")
	}
	if dispatched != 2 {
		t.Fatalf("dispatched=%d, want 2 after resume", dispatched)
	}
}

func TestInteractiveAdmissionLeaseDrainsWithoutPolling(t *testing.T) {
	c := NewCoordinator()
	lease, err := c.BeginWork()
	if err != nil {
		t.Fatal(err)
	}
	if c.ActiveWork() != 1 {
		t.Fatalf("active work=%d", c.ActiveWork())
	}
	rec, err := c.Prepare("maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != StatePreparing {
		t.Fatalf("state=%q, want preparing while lease active", rec.State)
	}
	if _, err := c.BeginWork(); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("expected admission closed, got %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if rec := c.State(); rec.State != StateSuspended || c.ActiveWork() != 0 {
		t.Fatalf("after drain record=%+v active=%d", rec, c.ActiveWork())
	}
}

func TestResumeCanCancelPreparingSuspension(t *testing.T) {
	c := NewCoordinator()
	lease, err := c.BeginWork()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := c.Prepare("maintenance")
	if err != nil || rec.State != StatePreparing {
		t.Fatalf("prepare record=%+v err=%v", rec, err)
	}
	if _, err := c.Resume(rec.SuspensionID); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if c.State().State != StateIdle || !c.AcceptingWork() {
		t.Fatalf("resume did not reopen admission: %+v", c.State())
	}
}

func TestNilCoordinatorAcceptsWork(t *testing.T) {
	var c *Coordinator
	if !c.AcceptingWork() {
		t.Fatal("nil coordinator must accept work (no-op gate)")
	}
	if c.Suspended() {
		t.Fatal("nil coordinator is not suspended")
	}
	if c.State().State != StateIdle {
		t.Fatal("nil coordinator state should be idle")
	}
}

// TestConcurrentPrepareResumeNoDeadlock exercises the lock under concurrency to
// catch deadlocks / races (run with -race).
func TestConcurrentPrepareResumeNoDeadlock(t *testing.T) {
	c := NewCoordinator()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); c.Prepare("r") }()
		go func() { defer wg.Done(); c.Resume("") }()
		go func() { defer wg.Done(); _ = c.AcceptingWork(); _ = c.State() }()
	}
	wg.Wait()
	// Drive to a known terminal state.
	c.Prepare("final")
	if _, err := c.Resume(""); err != nil {
		t.Fatalf("final resume: %v", err)
	}
	if !c.AcceptingWork() {
		t.Fatal("coordinator should be idle/accepting after final resume")
	}
}
