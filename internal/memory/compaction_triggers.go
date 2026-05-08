package memory

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultMemoryCompactionWriteThreshold  = 1000
	DefaultMemoryCompactionCheckInterval   = 5 * time.Minute
	DefaultMemoryCompactionDailyHour       = 3
	DefaultMemoryCompactionRecordThreshold = 10000
	DefaultMemoryCompactionMaxAge          = 30 * 24 * time.Hour
)

type MemoryCompactionLoad struct {
	CPUPercent     float64 `json:"cpu_percent,omitempty"`
	MemoryPressure bool    `json:"memory_pressure,omitempty"`
	Reason         string  `json:"reason,omitempty"`
}

type MemoryLoadFunc func(context.Context) MemoryCompactionLoad

type MemoryCompactionPolicy struct {
	CheckInterval          time.Duration    `json:"check_interval"`
	WriteThreshold         int              `json:"write_threshold"`
	DailyHour              int              `json:"daily_hour"`
	StartupRecordThreshold int              `json:"startup_record_threshold"`
	StartupMaxAge          time.Duration    `json:"startup_max_age"`
	Now                    func() time.Time `json:"-"`
	Load                   MemoryLoadFunc   `json:"-"`
}

type MemoryCompactionState struct {
	WritesSinceCompact   int       `json:"writes_since_compact"`
	RecordCount          int       `json:"record_count"`
	LastCompactedAt      time.Time `json:"last_compacted_at,omitempty"`
	LastWriteAt          time.Time `json:"last_write_at,omitempty"`
	LastDailyCompactDate string    `json:"last_daily_compact_date,omitempty"`
	Startup              bool      `json:"startup,omitempty"`
	Now                  time.Time `json:"now,omitempty"`
}

type MemoryCompactionDecision struct {
	Due        bool                 `json:"due"`
	Reason     string               `json:"reason,omitempty"`
	Skipped    bool                 `json:"skipped,omitempty"`
	SkipReason string               `json:"skip_reason,omitempty"`
	Load       MemoryCompactionLoad `json:"load,omitempty"`
	NextCheck  time.Time            `json:"next_check,omitempty"`
}

func DefaultMemoryCompactionPolicy() MemoryCompactionPolicy {
	return MemoryCompactionPolicy{
		CheckInterval:          DefaultMemoryCompactionCheckInterval,
		WriteThreshold:         DefaultMemoryCompactionWriteThreshold,
		DailyHour:              DefaultMemoryCompactionDailyHour,
		StartupRecordThreshold: DefaultMemoryCompactionRecordThreshold,
		StartupMaxAge:          DefaultMemoryCompactionMaxAge,
		Now:                    time.Now,
		Load:                   DefaultMemoryLoadSnapshot,
	}
}

func MemoryCompactionPolicyFromMap(extra map[string]any) MemoryCompactionPolicy {
	p := DefaultMemoryCompactionPolicy()
	if extra == nil {
		return p
	}
	if d, ok := durationFromAny(extra["compaction_interval"]); ok && d > 0 {
		p.CheckInterval = d
	}
	if n, ok := intFromAny(extra["compaction_write_threshold"]); ok && n > 0 {
		p.WriteThreshold = n
	}
	if n, ok := intFromAny(extra["compaction_daily_hour"]); ok && n >= 0 && n <= 23 {
		p.DailyHour = n
	}
	if n, ok := intFromAny(extra["compaction_startup_record_threshold"]); ok && n > 0 {
		p.StartupRecordThreshold = n
	}
	if d, ok := durationFromAny(extra["compaction_startup_max_age"]); ok && d > 0 {
		p.StartupMaxAge = d
	}
	return p
}

func NormalizeMemoryCompactionPolicy(p MemoryCompactionPolicy) MemoryCompactionPolicy {
	if p.CheckInterval <= 0 {
		p.CheckInterval = DefaultMemoryCompactionCheckInterval
	}
	if p.WriteThreshold <= 0 {
		p.WriteThreshold = DefaultMemoryCompactionWriteThreshold
	}
	if p.DailyHour < 0 || p.DailyHour > 23 {
		p.DailyHour = DefaultMemoryCompactionDailyHour
	}
	if p.StartupRecordThreshold <= 0 {
		p.StartupRecordThreshold = DefaultMemoryCompactionRecordThreshold
	}
	if p.StartupMaxAge <= 0 {
		p.StartupMaxAge = DefaultMemoryCompactionMaxAge
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Load == nil {
		p.Load = DefaultMemoryLoadSnapshot
	}
	return p
}

func ShouldCompactMemory(ctx context.Context, state MemoryCompactionState, policy MemoryCompactionPolicy) MemoryCompactionDecision {
	policy = NormalizeMemoryCompactionPolicy(policy)
	now := state.Now
	if now.IsZero() {
		now = policy.Now()
	}
	if now.IsZero() {
		now = time.Now()
	}
	decision := MemoryCompactionDecision{NextCheck: now.Add(policy.CheckInterval)}
	if state.Startup {
		if state.RecordCount > policy.StartupRecordThreshold {
			decision.Due = true
			decision.Reason = "startup_record_threshold"
		} else if state.RecordCount > 0 && (state.LastCompactedAt.IsZero() || now.Sub(state.LastCompactedAt) > policy.StartupMaxAge) {
			decision.Due = true
			decision.Reason = "startup_age_threshold"
		}
	}
	if !decision.Due && state.WritesSinceCompact >= policy.WriteThreshold {
		decision.Due = true
		decision.Reason = "write_threshold"
	}
	if !decision.Due && state.WritesSinceCompact > 0 && !state.LastWriteAt.IsZero() {
		localNow := now.Local()
		writeDay := state.LastWriteAt.Local().Format("2006-01-02")
		if writeDay == localNow.Format("2006-01-02") && localNow.Hour() >= policy.DailyHour && state.LastDailyCompactDate != writeDay {
			decision.Due = true
			decision.Reason = "daily_3am"
		}
	}
	if !decision.Due {
		return decision
	}
	load := policy.Load(ctx)
	decision.Load = load
	if load.CPUPercent > 80 {
		decision.Skipped = true
		decision.SkipReason = fmt.Sprintf("cpu %.1f%% above 80%%", load.CPUPercent)
		return decision
	}
	if load.MemoryPressure {
		decision.Skipped = true
		if strings.TrimSpace(load.Reason) != "" {
			decision.SkipReason = "memory pressure: " + load.Reason
		} else {
			decision.SkipReason = "memory pressure"
		}
	}
	return decision
}

func DefaultMemoryLoadSnapshot(ctx context.Context) MemoryCompactionLoad {
	_ = ctx
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	load := MemoryCompactionLoad{}
	if ms.Sys > 0 {
		ratio := float64(ms.Alloc) / float64(ms.Sys)
		if ratio > 0.85 && ms.Sys > 256*1024*1024 {
			load.MemoryPressure = true
			load.Reason = fmt.Sprintf("heap_alloc_sys_ratio=%.2f", ratio)
		}
	}
	return load
}

func MemoryCompactionStateForStore(ctx context.Context, store Store) (MemoryCompactionState, error) {
	if typed, ok := any(store).(interface {
		MemoryCompactionState(context.Context) (MemoryCompactionState, error)
	}); ok {
		return typed.MemoryCompactionState(ctx)
	}
	if store == nil {
		return MemoryCompactionState{}, fmt.Errorf("memory store is nil")
	}
	return MemoryCompactionState{RecordCount: store.Count()}, nil
}

func CompactMemoryRecords(ctx context.Context, store Store, cfg CompactionConfig) (CompactionResult, error) {
	if typed, ok := any(store).(interface {
		CompactMemoryRecords(context.Context, CompactionConfig) (CompactionResult, error)
	}); ok {
		return typed.CompactMemoryRecords(ctx, cfg)
	}
	if store == nil {
		return CompactionResult{}, fmt.Errorf("memory store is nil")
	}
	removed := store.Compact(0)
	return CompactionResult{Expired: removed}, nil
}

func CompactMemoryIfDue(ctx context.Context, store Store, policy MemoryCompactionPolicy, startup bool) (MemoryCompactionDecision, CompactionResult, bool, error) {
	state, err := MemoryCompactionStateForStore(ctx, store)
	if err != nil {
		return MemoryCompactionDecision{}, CompactionResult{}, false, err
	}
	state.Startup = startup
	decision := ShouldCompactMemory(ctx, state, policy)
	if !decision.Due || decision.Skipped {
		return decision, CompactionResult{}, false, nil
	}
	cfg := CompactionConfig{Now: state.Now, Reason: decision.Reason}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now().UTC()
	}
	result, err := CompactMemoryRecords(ctx, store, cfg)
	return decision, result, err == nil, err
}

func durationFromAny(v any) (time.Duration, bool) {
	switch t := v.(type) {
	case time.Duration:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		if d, err := time.ParseDuration(s); err == nil {
			return d, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return time.Duration(f * float64(time.Minute)), true
		}
	case float64:
		return time.Duration(t * float64(time.Minute)), true
	case float32:
		return time.Duration(float64(t) * float64(time.Minute)), true
	case int:
		return time.Duration(t) * time.Minute, true
	case int64:
		return time.Duration(t) * time.Minute, true
	}
	return 0, false
}

func intFromAny(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case float32:
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}
