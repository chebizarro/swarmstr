package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	nostruntime "swarmstr/internal/nostr/runtime"
	"swarmstr/internal/store/state"
)

type usageTracker struct {
	mu            sync.Mutex
	startedAt     time.Time
	controlCalls  int64
	dmInbound     int64
	dmOutbound    int64
	inboundRunes  int64
	outboundRunes int64
	abortedChats  int64
}

func newUsageTracker(startedAt time.Time) *usageTracker {
	return &usageTracker{startedAt: startedAt}
}

func (u *usageTracker) RecordControl() {
	u.mu.Lock()
	u.controlCalls++
	u.mu.Unlock()
}

func (u *usageTracker) RecordInbound(text string) {
	u.mu.Lock()
	u.dmInbound++
	u.inboundRunes += int64(len([]rune(text)))
	u.mu.Unlock()
}

func (u *usageTracker) RecordOutbound(text string) {
	u.mu.Lock()
	u.dmOutbound++
	u.outboundRunes += int64(len([]rune(text)))
	u.mu.Unlock()
}

func (u *usageTracker) RecordAbort(count int) {
	if count <= 0 {
		return
	}
	u.mu.Lock()
	u.abortedChats += int64(count)
	u.mu.Unlock()
}

func (u *usageTracker) Status() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return map[string]any{
		"uptime_seconds": int(time.Since(u.startedAt).Seconds()),
		"control_calls":  u.controlCalls,
		"dm_inbound":     u.dmInbound,
		"dm_outbound":    u.dmOutbound,
		"chat_aborts":    u.abortedChats,
	}
}

func (u *usageTracker) Cost() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	// Use int64 arithmetic with overflow protection
	totalRunes := u.inboundRunes + u.outboundRunes
	if totalRunes < 0 {
		// Overflow occurred, cap at max safe value
		totalRunes = 9223372036854775807 // math.MaxInt64
	}
	tokens := totalRunes / 4
	const usdPerKToken = 0.002 // synthetic local estimate for operational visibility
	totalUSD := (float64(tokens) / 1000.0) * usdPerKToken
	return map[string]any{
		"estimated_tokens": tokens,
		"total_usd":        totalUSD,
		"runes_in":         u.inboundRunes,
		"runes_out":        u.outboundRunes,
	}
}

type runtimeLogBuffer struct {
	mu      sync.Mutex
	cap     int
	nextID  int64
	entries []runtimeLogEntry
}

type runtimeLogEntry struct {
	ID      int64
	TS      int64
	Level   string
	Message string
}

func newRuntimeLogBuffer(capacity int) *runtimeLogBuffer {
	if capacity <= 0 {
		capacity = 2000
	}
	return &runtimeLogBuffer{cap: capacity}
}

func (b *runtimeLogBuffer) Append(level string, message string) {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		level = "info"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	
	// Trim before append if already at capacity to prevent unbounded growth
	if len(b.entries) >= b.cap {
		b.entries = b.entries[len(b.entries)-b.cap+1:]
	}
	
	b.nextID++
	entry := runtimeLogEntry{ID: b.nextID, TS: time.Now().UnixMilli(), Level: level, Message: message}
	b.entries = append(b.entries, entry)
}

func (b *runtimeLogBuffer) Tail(cursor int64, limit int, maxBytes int) map[string]any {
	if limit <= 0 {
		limit = 100
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	reset := false
	start := 0
	if cursor > 0 {
		start = len(b.entries)
		for i, entry := range b.entries {
			if entry.ID > cursor {
				start = i
				break
			}
		}
		if len(b.entries) > 0 && cursor < b.entries[0].ID {
			reset = true
			start = 0
		}
	}
	selected := b.entries[start:]
	if len(selected) > limit {
		selected = selected[len(selected)-limit:]
	}
	lines := make([]string, 0, len(selected))
	usedBytes := 0
	truncated := false
	lastProcessedIdx := -1
	for i, entry := range selected {
		line := fmt.Sprintf("%d [%s] %s", entry.TS, entry.Level, entry.Message)
		lineBytes := len(line)
		if usedBytes+lineBytes > maxBytes {
			truncated = true
			break
		}
		usedBytes += lineBytes
		lines = append(lines, line)
		lastProcessedIdx = i
	}
	nextCursor := cursor
	if lastProcessedIdx >= 0 && lastProcessedIdx < len(selected) {
		nextCursor = selected[lastProcessedIdx].ID
	}
	if nextCursor < 0 {
		nextCursor = 0
	}
	return map[string]any{
		"cursor":    nextCursor,
		"size":      len(b.entries),
		"lines":     lines,
		"truncated": truncated,
		"reset":     reset,
	}
}

type channelRuntimeState struct {
	mu        sync.Mutex
	loggedOut bool
}

func newChannelRuntimeState() *channelRuntimeState {
	return &channelRuntimeState{}
}

func (c *channelRuntimeState) Status(dmBus *nostruntime.DMBus, controlBus *nostruntime.ControlRPCBus, cfg state.ConfigDoc) map[string]any {
	c.mu.Lock()
	loggedOut := c.loggedOut
	c.mu.Unlock()
	dmRelays := []string{}
	controlRelays := []string{}
	if dmBus != nil {
		dmRelays = dmBus.Relays()
	}
	if controlBus != nil {
		controlRelays = controlBus.Relays()
	}
	return map[string]any{
		"channel":             "nostr",
		"connected":           !loggedOut && len(dmRelays) > 0,
		"logged_out":          loggedOut,
		"read_relays":         append([]string{}, cfg.Relays.Read...),
		"write_relays":        append([]string{}, cfg.Relays.Write...),
		"runtime_dm_relays":   dmRelays,
		"runtime_ctrl_relays": controlRelays,
	}
}

func (c *channelRuntimeState) Logout(channel string) (map[string]any, error) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		channel = "nostr"
	}
	if channel != "nostr" {
		return nil, fmt.Errorf("unsupported channel %q", channel)
	}
	c.mu.Lock()
	c.loggedOut = true
	c.mu.Unlock()
	return map[string]any{"channel": "nostr", "cleared": true, "loggedOut": true}, nil
}

func (c *channelRuntimeState) IsLoggedOut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loggedOut
}

type agentJobSnapshot struct {
	RunID     string
	SessionID string
	Status    string
	StartedAt int64
	EndedAt   int64
	Result    string
	Err       string
}

type agentJobRegistry struct {
	mu   sync.Mutex
	jobs map[string]*agentJobHandle
}

type agentJobHandle struct {
	mu       sync.Mutex
	snapshot agentJobSnapshot
	done     chan struct{}
	closed   bool
}

func newAgentJobRegistry() *agentJobRegistry {
	return &agentJobRegistry{jobs: map[string]*agentJobHandle{}}
}

func (r *agentJobRegistry) Begin(runID string, sessionID string) agentJobSnapshot {
	now := time.Now().UnixMilli()
	h := &agentJobHandle{snapshot: agentJobSnapshot{RunID: runID, SessionID: sessionID, Status: "pending", StartedAt: now}, done: make(chan struct{})}
	r.mu.Lock()
	r.jobs[runID] = h
	r.mu.Unlock()
	return h.snapshot
}

func (r *agentJobRegistry) Finish(runID string, result string, err error) {
	r.mu.Lock()
	h := r.jobs[runID]
	if h == nil {
		r.mu.Unlock()
		return
	}
	h.mu.Lock()
	h.snapshot.EndedAt = time.Now().UnixMilli()
	if err != nil {
		h.snapshot.Status = "error"
		h.snapshot.Err = strings.TrimSpace(err.Error())
	} else {
		h.snapshot.Status = "ok"
		h.snapshot.Result = strings.TrimSpace(result)
	}
	if !h.closed {
		close(h.done)
		h.closed = true
	}
	h.mu.Unlock()
	r.mu.Unlock()
	
	// Schedule cleanup after 5 minutes to prevent memory leak
	go func() {
		time.Sleep(5 * time.Minute)
		r.mu.Lock()
		delete(r.jobs, runID)
		r.mu.Unlock()
	}()
}

func (r *agentJobRegistry) Wait(ctx context.Context, runID string, timeout time.Duration) (agentJobSnapshot, bool) {
	r.mu.Lock()
	h := r.jobs[runID]
	if h == nil {
		r.mu.Unlock()
		return agentJobSnapshot{}, false
	}
	h.mu.Lock()
	snap := h.snapshot
	h.mu.Unlock()
	done := h.done
	if snap.Status != "pending" {
		r.mu.Unlock()
		return snap, true
	}
	r.mu.Unlock()

	if timeout <= 0 {
		return snap, true
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-done:
		r.mu.Lock()
		h2 := r.jobs[runID]
		if h2 == nil {
			r.mu.Unlock()
			return agentJobSnapshot{}, false
		}
		h2.mu.Lock()
		result := h2.snapshot
		h2.mu.Unlock()
		r.mu.Unlock()
		return result, true
	case <-waitCtx.Done():
		r.mu.Lock()
		h2 := r.jobs[runID]
		if h2 == nil {
			r.mu.Unlock()
			return agentJobSnapshot{}, false
		}
		h2.mu.Lock()
		result := h2.snapshot
		h2.mu.Unlock()
		r.mu.Unlock()
		return result, true
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
