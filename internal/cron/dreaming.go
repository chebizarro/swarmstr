package cron

import (
	"context"
	"sync"
	"time"

	"metiq/internal/memory"
)

// DreamingJob wires the generic cron schedule package to the memory dreaming
// phases. Callers can tick Due/Run from an existing daemon scheduler or
// background goroutine without embedding memory-specific logic there.
type DreamingJob struct {
	Manager *memory.PromotionManager
	Config  memory.DreamingConfig
	Builder memory.DreamingNarrativeBuilder

	Schedule Schedule
	LastRun  time.Time
	Running  bool
	mu       sync.Mutex
}

// NewDreamingJob parses expr and returns a scheduled dreaming job.
func NewDreamingJob(manager *memory.PromotionManager, expr string, cfg memory.DreamingConfig, builder memory.DreamingNarrativeBuilder) (*DreamingJob, error) {
	sched, err := Parse(expr)
	if err != nil {
		return nil, err
	}
	return &DreamingJob{Manager: manager, Config: cfg, Builder: builder, Schedule: sched}, nil
}

// Due reports whether the job should run at now.
func (j *DreamingJob) Due(now time.Time) bool {
	if j == nil || j.Schedule == nil || j.Running {
		return false
	}
	return j.Schedule.Matches(now)
}

// Run executes the configured dreaming phases once.
func (j *DreamingJob) Run(ctx context.Context) (*memory.DreamingResult, error) {
	if j == nil || j.Manager == nil {
		return &memory.DreamingResult{}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	j.mu.Lock()
	if j.Running {
		j.mu.Unlock()
		return nil, nil
	}
	j.Running = true
	j.mu.Unlock()
	defer func() {
		j.mu.Lock()
		j.Running = false
		j.LastRun = time.Now()
		j.mu.Unlock()
	}()
	return memory.RunDreamingPhases(j.Manager, j.Config, j.Builder)
}
