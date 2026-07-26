// Package suspend implements the cooperative daemon suspend/resume machinery
// behind the OpenClaw gateway.suspend.* surface (src/gateway/server-methods/
// suspend.ts, backed by infra/gateway-suspend-coordinator.ts).
//
// The Coordinator owns a durable suspension lifecycle
//
//	idle -> preparing -> suspended -> resuming -> idle
//
// persisted to an atomic JSON ledger (mirroring internal/gateway/questions and
// internal/gateway/pluginapproval) so gateway.suspend.status survives a crash
// or restart mid-suspension. While suspended the coordinator flips a shared
// "accepting work" gate that the cooperative background dispatchers (cron
// scheduler, dreaming/promotion job, memory-compaction worker) consult before
// dispatching NEW scheduled work: pause == stop dispatching new, never
// hard-kill in-flight. In-flight interactive work (agent runs, sessions) is not
// killed — it is reported honestly via the quiesce accounting so an operator can
// wait for it to drain.
package suspend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Lifecycle states for the daemon suspend coordinator (OpenClaw parity).
const (
	StateIdle      = "idle"
	StatePreparing = "preparing"
	StateSuspended = "suspended"
	StateResuming  = "resuming"
)

const ledgerVersion = 1

// InFlight is the quiesce-accounting snapshot the coordinator reports. It mirrors
// the gateway.restart.preflight readiness inspector (in-flight agent runs +
// active sessions): this is work the daemon does NOT kill on suspend, only
// reports so an operator can wait for it to drain.
type InFlight struct {
	AgentRuns      int `json:"agentRuns"`
	ActiveSessions int `json:"sessions"`
}

// Empty reports whether no in-flight work remains (the daemon has fully
// quiesced its interactive surface).
func (f InFlight) Empty() bool { return f.AgentRuns == 0 && f.ActiveSessions == 0 }

// Record is the durable + wire representation of the current suspension state.
type Record struct {
	State        string `json:"state"`
	SuspensionID string `json:"suspensionId,omitempty"`
	SinceMs      int64  `json:"sinceMs,omitempty"`
	Reason       string `json:"reason,omitempty"`
	UpdatedAtMs  int64  `json:"updatedAtMs,omitempty"`
}

type ledgerDocument struct {
	Version int    `json:"version"`
	Record  Record `json:"record"`
}

// Coordinator owns the suspend/resume lifecycle and the shared accepting-work
// gate. It is safe for concurrent use. Coordinator methods never call out to
// worker code while holding the lock, and background workers must never hold
// their own locks across an AcceptingWork() call, so the gate introduces no lock
// ordering against the memory maintenance gate / promotion mutex.
type Coordinator struct {
	mu              sync.Mutex
	rec             Record
	seq             int64
	storagePath     string
	pausableWorkers []string
	now             func() int64
}

// NewCoordinator returns an in-memory coordinator (tests, ephemeral runtimes).
func NewCoordinator() *Coordinator {
	c, _ := NewCoordinatorAt("")
	return c
}

// NewCoordinatorAt loads (or initializes) a coordinator backed by the durable
// ledger at path. A suspension recorded by a prior process stays in effect
// (gate closed) until an explicit resume, so a crash mid-suspension recovers
// gated rather than silently accepting work.
func NewCoordinatorAt(path string) (*Coordinator, error) {
	c := &Coordinator{
		rec:         Record{State: StateIdle},
		storagePath: strings.TrimSpace(path),
		now:         func() int64 { return time.Now().UnixMilli() },
	}
	if c.storagePath == "" {
		return c, nil
	}
	raw, err := os.ReadFile(c.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read suspend ledger: %w", err)
	}
	var doc ledgerDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode suspend ledger: %w", err)
	}
	if doc.Version != ledgerVersion {
		return nil, fmt.Errorf("unsupported suspend ledger version %d", doc.Version)
	}
	c.rec = normalizeRecoveredRecord(doc.Record)
	return c, nil
}

// normalizeRecoveredRecord collapses the transient mid-operation states to a
// stable one on crash recovery — in-memory dispatch state was reset on restart,
// so the persisted gate must land on a stable value. A crash while preparing
// recovers conservatively as suspended (stay gated until an explicit resume); a
// crash while resuming (or any unrecognized/incomplete record) recovers as idle.
func normalizeRecoveredRecord(rec Record) Record {
	switch strings.TrimSpace(rec.State) {
	case StateSuspended, StatePreparing:
		if strings.TrimSpace(rec.SuspensionID) == "" {
			return Record{State: StateIdle}
		}
		rec.State = StateSuspended
		return rec
	default:
		return Record{State: StateIdle}
	}
}

// RegisterPausableWorker records that a background dispatcher consults the
// accepting-work gate, so status can report which workers are paused during a
// suspension. Call at wire time before the workers start.
func (c *Coordinator) RegisterPausableWorker(name string) {
	if c == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, existing := range c.pausableWorkers {
		if existing == name {
			return
		}
	}
	c.pausableWorkers = append(c.pausableWorkers, name)
	sort.Strings(c.pausableWorkers)
}

// PausableWorkers returns the registered gated-worker names (sorted copy).
func (c *Coordinator) PausableWorkers() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pausableWorkers) == 0 {
		return nil
	}
	out := make([]string, len(c.pausableWorkers))
	copy(out, c.pausableWorkers)
	return out
}

// AcceptingWork reports whether the daemon is accepting NEW scheduled/background
// work. It is false whenever a suspension is active (any non-idle state). A nil
// coordinator always accepts work, so callers can consult it unconditionally.
func (c *Coordinator) AcceptingWork() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rec.State == StateIdle
}

// State returns a snapshot of the current suspension record.
func (c *Coordinator) State() Record {
	if c == nil {
		return Record{State: StateIdle}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rec
}

// Suspended reports whether a suspension is currently active.
func (c *Coordinator) Suspended() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rec.State == StateSuspended
}

// Prepare begins a suspension, or returns the active one unchanged when already
// suspended (idempotent). It transitions idle -> preparing -> suspended,
// persisting at each step so a crash between steps recovers to a stable state.
// The accepting-work gate is closed for the whole preparing/suspended window.
func (c *Coordinator) Prepare(reason string) (Record, error) {
	if c == nil {
		return Record{}, fmt.Errorf("suspend coordinator unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.rec.State {
	case StateSuspended:
		return c.rec, nil // idempotent: return the active suspension
	case StateIdle:
		// proceed
	default:
		return c.rec, fmt.Errorf("cannot prepare suspension from state %q", c.rec.State)
	}
	now := c.now()
	c.seq++
	id := fmt.Sprintf("suspend-%d-%d", now, c.seq)
	// Phase 1: preparing — durably record intent (gate is already closed since
	// state != idle) before declaring the suspension complete.
	preparing := Record{
		State:        StatePreparing,
		SuspensionID: id,
		SinceMs:      now,
		Reason:       strings.TrimSpace(reason),
		UpdatedAtMs:  now,
	}
	if err := c.persistLocked(preparing); err != nil {
		return c.rec, err
	}
	c.rec = preparing
	// Phase 2: suspended — quiesce complete.
	suspended := preparing
	suspended.State = StateSuspended
	suspended.UpdatedAtMs = c.now()
	if err := c.persistLocked(suspended); err != nil {
		// Stay at preparing (recovers to suspended); surface the error.
		return c.rec, err
	}
	c.rec = suspended
	return c.rec, nil
}

// Resume clears the active suspension and re-opens the accepting-work gate. When
// suspensionID is non-empty it must match the active suspension. It transitions
// suspended -> resuming -> idle, persisting at each step. Rejects when no
// suspension is active.
func (c *Coordinator) Resume(suspensionID string) (Record, error) {
	if c == nil {
		return Record{}, fmt.Errorf("suspend coordinator unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec.State != StateSuspended {
		return c.rec, fmt.Errorf("cannot resume: no active suspension (state %q)", c.rec.State)
	}
	if id := strings.TrimSpace(suspensionID); id != "" && id != c.rec.SuspensionID {
		return c.rec, fmt.Errorf("suspension id mismatch: active %q, requested %q", c.rec.SuspensionID, id)
	}
	// Phase 1: resuming — durable intent to release the gate.
	resuming := c.rec
	resuming.State = StateResuming
	resuming.UpdatedAtMs = c.now()
	if err := c.persistLocked(resuming); err != nil {
		return c.rec, err
	}
	c.rec = resuming
	// Phase 2: idle — gate re-opened.
	idle := Record{State: StateIdle, UpdatedAtMs: c.now()}
	if err := c.persistLocked(idle); err != nil {
		return c.rec, err
	}
	c.rec = idle
	return c.rec, nil
}

func (c *Coordinator) persistLocked(rec Record) error {
	if c.storagePath == "" {
		return nil
	}
	doc := ledgerDocument{Version: ledgerVersion, Record: rec}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode suspend ledger: %w", err)
	}
	dir := filepath.Dir(c.storagePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create suspend ledger directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".suspend-ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create suspend ledger temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, c.storagePath); err != nil {
		return fmt.Errorf("replace suspend ledger: %w", err)
	}
	return nil
}
