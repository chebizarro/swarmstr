package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
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

func recoveryStatusSnapshot() map[string]any {
	controlRecoveryMu.RLock()
	defer controlRecoveryMu.RUnlock()
	if len(controlRecoveryStatus) == 0 {
		return nil
	}
	out := make(map[string]any, len(controlRecoveryStatus))
	for k, v := range controlRecoveryStatus {
		out[k] = v
	}
	return out
}

func setRecoveryStatusField(key string, value any) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	controlRecoveryMu.Lock()
	defer controlRecoveryMu.Unlock()
	if controlRecoveryStatus == nil {
		controlRecoveryStatus = map[string]any{}
	}
	controlRecoveryStatus[key] = value
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
	notify  chan struct{}
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
	return &runtimeLogBuffer{cap: capacity, notify: make(chan struct{})}
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
	if b.notify != nil {
		close(b.notify)
	}
	b.notify = make(chan struct{})
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
	nextCursor := cursor
	for i, entry := range selected {
		line := fmt.Sprintf("%d [%s] %s", entry.TS, entry.Level, entry.Message)
		lineBytes := len(line)
		if usedBytes+lineBytes > maxBytes {
			truncated = true
			if lastProcessedIdx < 0 {
				nextCursor = entry.ID
			}
			break
		}
		usedBytes += lineBytes
		lines = append(lines, line)
		lastProcessedIdx = i
	}
	if lastProcessedIdx >= 0 && lastProcessedIdx < len(selected) {
		nextCursor = selected[lastProcessedIdx].ID
	} else if reset && len(selected) == 0 && len(b.entries) > 0 {
		nextCursor = b.entries[len(b.entries)-1].ID
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

func (b *runtimeLogBuffer) hasChangesAfterLocked(cursor int64) bool {
	if b.nextID > cursor {
		return true
	}
	return len(b.entries) > 0 && cursor < b.entries[0].ID
}

func (b *runtimeLogBuffer) snapshotNotifier(cursor int64) (bool, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hasChangesAfterLocked(cursor), b.notify
}

type channelRuntimeState struct {
	mu        sync.Mutex
	loggedOut bool
}

func newChannelRuntimeState() *channelRuntimeState {
	return &channelRuntimeState{}
}

func (c *channelRuntimeState) Status(dmBus nostruntime.DMTransport, controlBus *nostruntime.ControlRPCBus, cfg state.ConfigDoc) map[string]any {
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

// ── SubagentRegistry ─────────────────────────────────────────────────────────

const (
	maxSubagentDepth        = 5
	defaultMaxLiveSubagents = 20
)

// SubagentRecord tracks a spawned sub-session.
type SubagentRecord struct {
	RunID           string `json:"run_id"`
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	Depth           int    `json:"depth"`
	Status          string `json:"status"` // "running" | "done" | "error"
	Message         string `json:"message"`
	Result          string `json:"result,omitempty"`
	Error           string `json:"error,omitempty"`
	StartedAt       int64  `json:"started_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

// SubagentRegistry tracks spawned child sessions and their ancestry/depth.
type SubagentRegistry struct {
	mu              sync.Mutex
	records         map[string]*SubagentRecord // key: RunID
	livenessChecker *agent.SubagentLivenessChecker
}

func newSubagentRegistry() *SubagentRegistry {
	return &SubagentRegistry{
		records:         map[string]*SubagentRecord{},
		livenessChecker: agent.NewSubagentLivenessChecker(),
	}
}

func (r *SubagentRegistry) checker() *agent.SubagentLivenessChecker {
	if r == nil || r.livenessChecker == nil {
		return agent.NewSubagentLivenessChecker()
	}
	return r.livenessChecker
}

func subagentLivenessRecord(rec *SubagentRecord) agent.SubagentRunRecord {
	if rec == nil {
		return agent.SubagentRunRecord{}
	}
	return agent.SubagentRunRecord{
		StartedAt: rec.StartedAt,
		UpdatedAt: rec.UpdatedAt,
		Status:    rec.Status,
	}
}

// CleanupStale removes stale child links from the registry.
func (r *SubagentRegistry) CleanupStale(now time.Time) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	checker := r.checker()
	removed := 0
	for runID, rec := range r.records {
		if rec == nil {
			delete(r.records, runID)
			removed++
		}
	}

	keepMemo := make(map[string]bool, len(r.records))
	visiting := make(map[string]bool, len(r.records))
	var shouldKeep func(string) bool
	shouldKeep = func(runID string) bool {
		if keep, ok := keepMemo[runID]; ok {
			return keep
		}
		if visiting[runID] {
			return false
		}
		rec := r.records[runID]
		if rec == nil {
			return false
		}
		visiting[runID] = true
		keep := checker.ShouldKeepSubagentRunChildLink(subagentLivenessRecord(rec), 0, now)
		if !keep && rec.SessionID != "" {
			for childRunID, child := range r.records {
				if child == nil || child.ParentSessionID != rec.SessionID {
					continue
				}
				if shouldKeep(childRunID) {
					keep = true
					break
				}
			}
		}
		visiting[runID] = false
		keepMemo[runID] = keep
		return keep
	}

	for runID := range r.records {
		if !shouldKeep(runID) {
			delete(r.records, runID)
			removed++
		}
	}
	return removed
}

// Spawn creates a new SubagentRecord if depth/count limits allow.
// Returns the record and whether the spawn was permitted.
func (r *SubagentRegistry) Spawn(runID, sessionID, parentSessionID string, depth int, message string, maxLive int) (*SubagentRecord, bool) {
	if r == nil || depth > maxSubagentDepth {
		return nil, false
	}
	now := time.Now().UnixMilli()
	rec := &SubagentRecord{
		RunID:           runID,
		SessionID:       sessionID,
		ParentSessionID: parentSessionID,
		Depth:           depth,
		Status:          "running",
		Message:         message,
		StartedAt:       now,
		UpdatedAt:       now,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if maxLive > 0 && r.liveCountLocked() >= maxLive {
		return nil, false
	}
	r.records[runID] = rec
	return rec, true
}

func (r *SubagentRegistry) liveCountLocked() int {
	count := 0
	for _, rec := range r.records {
		if rec != nil && rec.Status == "running" {
			count++
		}
	}
	return count
}

func (r *SubagentRegistry) LiveCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.liveCountLocked()
}

func (r *SubagentRegistry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

// Finish marks a sub-session as done or errored.
func (r *SubagentRegistry) Finish(runID, result, errStr string) {
	r.mu.Lock()
	rec := r.records[runID]
	if rec != nil {
		if errStr != "" {
			rec.Status = "error"
			rec.Error = errStr
		} else {
			rec.Status = "done"
			rec.Result = result
		}
		rec.UpdatedAt = time.Now().UnixMilli()
	}
	r.mu.Unlock()
}

// Get returns the record for the given run_id, or nil.
func (r *SubagentRegistry) Get(runID string) *SubagentRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[runID]
	if rec == nil {
		return nil
	}
	copy := *rec
	return &copy
}

// DepthOf returns the depth of the session identified by parentSessionID,
// searching by sessionID field in records (for recursive depth calculation).
func (r *SubagentRegistry) DepthOf(parentSessionID string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.records {
		if rec.SessionID == parentSessionID {
			return rec.Depth
		}
	}
	return 0
}

type agentJobSnapshot struct {
	RunID          string
	SessionID      string
	Status         string
	StartedAt      int64
	EndedAt        int64
	Result         string
	Err            string
	FallbackUsed   bool
	FallbackFrom   string
	FallbackTo     string
	FallbackReason string
}

const agentJobRetention = 5 * time.Minute

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
	if r == nil {
		return
	}
	now := time.Now()
	r.mu.Lock()
	h := r.jobs[runID]
	if h == nil {
		r.cleanupFinishedLocked(now)
		r.mu.Unlock()
		return
	}
	h.mu.Lock()
	h.snapshot.EndedAt = now.UnixMilli()
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
	r.cleanupFinishedLocked(now)
	r.mu.Unlock()
}

func (r *agentJobRegistry) CleanupFinished(now time.Time) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cleanupFinishedLocked(now)
}

func (r *agentJobRegistry) cleanupFinishedLocked(now time.Time) int {
	cutoff := now.Add(-agentJobRetention).UnixMilli()
	removed := 0
	for runID, h := range r.jobs {
		if h == nil {
			delete(r.jobs, runID)
			removed++
			continue
		}
		h.mu.Lock()
		endedAt := h.snapshot.EndedAt
		status := h.snapshot.Status
		h.mu.Unlock()
		if status != "pending" && endedAt > 0 && endedAt <= cutoff {
			delete(r.jobs, runID)
			removed++
		}
	}
	return removed
}

func (r *agentJobRegistry) SetFallback(runID, from, to, reason string) {
	r.mu.Lock()
	h := r.jobs[runID]
	r.mu.Unlock()
	if h == nil {
		return
	}
	h.mu.Lock()
	h.snapshot.FallbackUsed = true
	h.snapshot.FallbackFrom = strings.TrimSpace(from)
	h.snapshot.FallbackTo = strings.TrimSpace(to)
	h.snapshot.FallbackReason = strings.TrimSpace(reason)
	h.mu.Unlock()
}

// Get returns a snapshot of the job for runID, or (zero, false) if not found.
func (r *agentJobRegistry) Get(runID string) (agentJobSnapshot, bool) {
	r.mu.Lock()
	h := r.jobs[runID]
	r.mu.Unlock()
	if h == nil {
		return agentJobSnapshot{}, false
	}
	h.mu.Lock()
	snap := h.snapshot
	h.mu.Unlock()
	return snap, true
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

type nodeInvocationEvent struct {
	Type    string         `json:"type"`
	Status  string         `json:"status,omitempty"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	UnixMS  int64          `json:"unix_ms"`
}

type nodeInvocationRecord struct {
	RunID           string                `json:"run_id"`
	NodeID          string                `json:"node_id"`
	Command         string                `json:"command"`
	Args            map[string]any        `json:"args,omitempty"`
	TimeoutMS       int                   `json:"timeout_ms"`
	Status          string                `json:"status"`
	CreatedAt       int64                 `json:"created_at"`
	UpdatedAt       int64                 `json:"updated_at"`
	Result          any                   `json:"result,omitempty"`
	Error           string                `json:"error,omitempty"`
	Events          []nodeInvocationEvent `json:"events,omitempty"`
	progressNextSeq int                   `json:"-"`
	progressPending map[int]string        `json:"-"`
}

type nodeInvocationProgressChunk struct {
	Seq   int
	Chunk string
}

type nodeInvocationProgressOutcome struct {
	Accepted  bool
	Delivered []nodeInvocationProgressChunk
	Record    nodeInvocationRecord
}

const (
	maxNodeInvocations  = 1000
	maxCronRuns         = 500
	maxPendingApprovals = 200
	maxWizardSessions   = 100
	invocationTTL       = 24 * time.Hour
	approvalTTL         = 1 * time.Hour
	wizardTTL           = 2 * time.Hour
)

type nodeInvocationRegistry struct {
	mu             sync.Mutex
	progressEmitMu sync.Mutex
	lifecycleMu    sync.RWMutex
	revokedNodes   map[string]struct{}
	runs           map[string]nodeInvocationRecord
	order          []string
}

func newNodeInvocationRegistry() *nodeInvocationRegistry {
	return &nodeInvocationRegistry{revokedNodes: map[string]struct{}{}, runs: map[string]nodeInvocationRecord{}, order: []string{}}
}

func (r *nodeInvocationRegistry) WithActiveNode(nodeID string, operation func() error) error {
	if r == nil {
		return fmt.Errorf("node invoke runtime not configured")
	}
	nodeID = strings.TrimSpace(nodeID)
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	if _, revoked := r.revokedNodes[nodeID]; nodeID != "" && revoked {
		return fmt.Errorf("node %q is no longer paired", nodeID)
	}
	return operation()
}

func (r *nodeInvocationRegistry) RevokeNode(nodeID string, cleanup func() error) error {
	if r == nil {
		return fmt.Errorf("node invoke runtime not configured")
	}
	nodeID = strings.TrimSpace(nodeID)
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if nodeID != "" {
		r.revokedNodes[nodeID] = struct{}{}
	}
	return cleanup()
}

func (r *nodeInvocationRegistry) AllowNode(nodeID string) {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	delete(r.revokedNodes, strings.TrimSpace(nodeID))
	r.lifecycleMu.Unlock()
}

// RemoveNode clears invocation state. Pair removal must call it from inside
// RevokeNode so no active-node operation can race cleanup.
func (r *nodeInvocationRegistry) RemoveNode(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.order[:0]
	for _, runID := range r.order {
		if run, ok := r.runs[runID]; ok && run.NodeID == nodeID {
			delete(r.runs, runID)
			continue
		}
		kept = append(kept, runID)
	}
	r.order = append([]string(nil), kept...)
}

func (r *nodeInvocationRegistry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	ttlMS := int64(invocationTTL.Milliseconds())
	newOrder := make([]string, 0, len(r.order))
	for _, runID := range r.order {
		rec, ok := r.runs[runID]
		if !ok {
			continue
		}
		if rec.Status == "ok" || rec.Status == "error" {
			if now-rec.UpdatedAt > ttlMS {
				delete(r.runs, runID)
				continue
			}
		}
		newOrder = append(newOrder, runID)
	}
	r.order = newOrder
	if len(r.runs) > maxNodeInvocations {
		excess := len(r.order) - maxNodeInvocations
		if excess > 0 {
			for _, runID := range r.order[:excess] {
				delete(r.runs, runID)
			}
			r.order = r.order[excess:]
		}
	}
}

func (r *nodeInvocationRegistry) Begin(req methods.NodeInvokeRequest) nodeInvocationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = fmt.Sprintf("node-run-%d", time.Now().UnixNano())
	}
	_, exists := r.runs[runID]
	rec := nodeInvocationRecord{
		RunID:           runID,
		NodeID:          req.NodeID,
		Command:         req.Command,
		Args:            req.Args,
		TimeoutMS:       req.TimeoutMS,
		Status:          "queued",
		CreatedAt:       now,
		UpdatedAt:       now,
		Events:          []nodeInvocationEvent{},
		progressPending: map[int]string{},
	}
	if !exists {
		r.order = append(r.order, runID)
	}
	r.runs[runID] = rec
	return rec
}

func (r *nodeInvocationRegistry) AddProgress(req methods.NodeInvokeProgressRequest) (nodeInvocationProgressOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[req.InvokeID]
	if !ok {
		return nodeInvocationProgressOutcome{}, state.ErrNotFound
	}
	if rec.NodeID != req.NodeID {
		return nodeInvocationProgressOutcome{}, fmt.Errorf("node_id mismatch")
	}
	if rec.Status == "ok" || rec.Status == "error" || rec.Status == "failed" || rec.Status == "cancelled" || rec.Status == "done" {
		return nodeInvocationProgressOutcome{Record: rec}, nil
	}
	if req.Seq < rec.progressNextSeq {
		return nodeInvocationProgressOutcome{Record: rec}, nil
	}
	if rec.progressPending == nil {
		rec.progressPending = map[int]string{}
	}
	if _, exists := rec.progressPending[req.Seq]; exists {
		return nodeInvocationProgressOutcome{Record: rec}, nil
	}
	const maxPendingProgressChunks = 128
	if req.Seq > rec.progressNextSeq && len(rec.progressPending) >= maxPendingProgressChunks {
		return nodeInvocationProgressOutcome{Record: rec}, nil
	}
	rec.progressPending[req.Seq] = req.Chunk
	rec.Status = "running"
	rec.UpdatedAt = time.Now().UnixMilli()
	outcome := nodeInvocationProgressOutcome{Accepted: true}
	for {
		chunk, exists := rec.progressPending[rec.progressNextSeq]
		if !exists {
			break
		}
		seq := rec.progressNextSeq
		delete(rec.progressPending, seq)
		rec.progressNextSeq++
		rec.Events = append(rec.Events, nodeInvocationEvent{
			Type:   "progress",
			Status: "running",
			Data:   map[string]any{"seq": seq, "chunk": chunk},
			UnixMS: rec.UpdatedAt,
		})
		outcome.Delivered = append(outcome.Delivered, nodeInvocationProgressChunk{Seq: seq, Chunk: chunk})
	}
	r.runs[req.InvokeID] = rec
	outcome.Record = rec
	return outcome, nil
}

func (r *nodeInvocationRegistry) AddEvent(req methods.NodeEventRequest) (nodeInvocationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[req.RunID]
	if !ok {
		return nodeInvocationRecord{}, state.ErrNotFound
	}
	now := time.Now().UnixMilli()
	rec.UpdatedAt = now
	if req.NodeID != "" {
		rec.NodeID = req.NodeID
	}
	if req.Status != "" {
		rec.Status = req.Status
	}
	rec.Events = append(rec.Events, nodeInvocationEvent{Type: req.Type, Status: req.Status, Message: req.Message, Data: req.Data, UnixMS: now})
	r.runs[req.RunID] = rec
	return rec, nil
}

func (r *nodeInvocationRegistry) SetResult(req methods.NodeResultRequest) (nodeInvocationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.runs[req.RunID]
	if !ok {
		return nodeInvocationRecord{}, state.ErrNotFound
	}
	now := time.Now().UnixMilli()
	rec.UpdatedAt = now
	if req.NodeID != "" {
		rec.NodeID = req.NodeID
	}
	rec.Result = req.Result
	rec.Error = req.Error
	if req.Status != "" {
		rec.Status = req.Status
	} else if req.Error != "" {
		rec.Status = "error"
	} else {
		rec.Status = "ok"
	}
	rec.Events = append(rec.Events, nodeInvocationEvent{Type: "result", Status: rec.Status, Message: req.Error, UnixMS: now})
	rec.progressPending = nil
	r.runs[req.RunID] = rec
	return rec, nil
}

type cronJobRecord struct {
	ID       string          `json:"id"`
	Schedule string          `json:"schedule"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
	Enabled  bool            `json:"enabled"`
	Created  int64           `json:"created_at"`
	Updated  int64           `json:"updated_at"`
}

const cronScratchMaxBytes = 262144

type cronScratchRecord struct {
	Content     string `json:"content"`
	Revision    int    `json:"revision"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

type cronScratchWriteResult struct {
	OK              bool
	Scratch         *cronScratchRecord
	CurrentRevision int
}

type cronPersistedState struct {
	Version          int                          `json:"version"`
	Jobs             []cronJobRecord              `json:"jobs"`
	Scratch          map[string]cronScratchRecord `json:"scratch,omitempty"`
	ScratchRevisions map[string]int               `json:"scratchRevisions,omitempty"`
}

type cronRunRecord struct {
	RunID    string `json:"run_id"`
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	Started  int64  `json:"started_at"`
	Finished int64  `json:"finished_at"`
}

var errCronPersistenceUnavailable = errors.New("cron persistence backend unavailable")

type cronRegistry struct {
	mu               sync.Mutex
	persistMu        sync.Mutex
	jobs             map[string]cronJobRecord
	order            []string
	runsByID         map[string][]cronRunRecord
	scratch          map[string]cronScratchRecord
	scratchRevisions map[string]int
}

func newCronRegistry() *cronRegistry {
	return &cronRegistry{
		jobs:             map[string]cronJobRecord{},
		order:            []string{},
		runsByID:         map[string][]cronRunRecord{},
		scratch:          map[string]cronScratchRecord{},
		scratchRevisions: map[string]int{},
	}
}

func (r *cronRegistry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for jobID, runs := range r.runsByID {
		if len(runs) > maxCronRuns {
			r.runsByID[jobID] = runs[len(runs)-maxCronRuns:]
		}
	}
}

func (r *cronRegistry) List(limit int) []cronJobRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]cronJobRecord, 0, min(limit, len(r.order)))
	for i := len(r.order) - 1; i >= 0 && len(out) < limit; i-- {
		id := r.order[i]
		job, ok := r.jobs[id]
		if !ok {
			continue
		}
		out = append(out, job)
	}
	return out
}

func (r *cronRegistry) Add(req methods.CronAddRequest) cronJobRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = fmt.Sprintf("cron-%d", time.Now().UnixNano())
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rec := cronJobRecord{ID: id, Schedule: req.Schedule, Method: req.Method, Params: req.Params, Enabled: enabled, Created: now, Updated: now}
	if _, exists := r.jobs[id]; !exists {
		r.order = append(r.order, id)
	}
	r.jobs[id] = rec
	return rec
}

func (r *cronRegistry) Status(id string) (cronJobRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	return job, ok
}

func (r *cronRegistry) Update(req methods.CronUpdateRequest) (cronJobRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[req.ID]
	if !ok {
		return cronJobRecord{}, state.ErrNotFound
	}
	if req.Schedule != "" {
		job.Schedule = req.Schedule
	}
	if req.Method != "" {
		job.Method = req.Method
	}
	if len(req.Params) > 0 {
		job.Params = req.Params
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}
	job.Updated = time.Now().UnixMilli()
	r.jobs[req.ID] = job
	return job, nil
}

func (r *cronRegistry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.jobs[id]; !ok {
		return state.ErrNotFound
	}
	delete(r.jobs, id)
	for idx, item := range r.order {
		if item == id {
			r.order = append(r.order[:idx], r.order[idx+1:]...)
			break
		}
	}
	delete(r.runsByID, id)
	delete(r.scratch, id)
	delete(r.scratchRevisions, id)
	return nil
}

func (r *cronRegistry) Scratch(id string) (*cronScratchRecord, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.jobs[id]; !ok {
		return nil, 0, state.ErrNotFound
	}
	currentRevision := r.scratchRevisions[id]
	record, ok := r.scratch[id]
	if !ok {
		return nil, currentRevision, nil
	}
	out := record
	return &out, currentRevision, nil
}

func (r *cronRegistry) SetScratch(id string, content *string, expectedRevision *int) (cronScratchWriteResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.jobs[id]; !ok {
		return cronScratchWriteResult{}, state.ErrNotFound
	}
	currentRevision := r.scratchRevisions[id]
	if expectedRevision != nil && *expectedRevision != currentRevision {
		return cronScratchWriteResult{OK: false, CurrentRevision: currentRevision}, nil
	}
	if content != nil && len([]byte(*content)) > cronScratchMaxBytes {
		return cronScratchWriteResult{}, fmt.Errorf("scratch content exceeds %d bytes", cronScratchMaxBytes)
	}
	nextRevision := currentRevision + 1
	r.scratchRevisions[id] = nextRevision
	if content == nil {
		delete(r.scratch, id)
		return cronScratchWriteResult{OK: true, CurrentRevision: nextRevision}, nil
	}
	record := cronScratchRecord{Content: *content, Revision: nextRevision, UpdatedAtMS: time.Now().UnixMilli()}
	r.scratch[id] = record
	out := record
	return cronScratchWriteResult{OK: true, Scratch: &out, CurrentRevision: nextRevision}, nil
}

func (r *cronRegistry) Run(id string) (cronRunRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.jobs[id]; !ok {
		return cronRunRecord{}, state.ErrNotFound
	}
	now := time.Now().UnixMilli()
	run := cronRunRecord{RunID: fmt.Sprintf("cron-run-%d", time.Now().UnixNano()), JobID: id, Status: "ok", Started: now, Finished: now}
	r.runsByID[id] = append(r.runsByID[id], run)
	return run, nil
}

// RecordRun appends a run result for the given job ID.
// Used by the cron scheduler to record actual execution outcomes.
func (r *cronRegistry) RecordRun(id, status string, durationMS int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	run := cronRunRecord{
		RunID:    fmt.Sprintf("cron-run-%d", time.Now().UnixNano()),
		JobID:    id,
		Status:   status,
		Started:  now - durationMS,
		Finished: now,
	}
	r.runsByID[id] = append(r.runsByID[id], run)
}

func (r *cronRegistry) Runs(id string, limit int) []cronRunRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if id != "" {
		runs := r.runsByID[id]
		if len(runs) > limit {
			return append([]cronRunRecord{}, runs[len(runs)-limit:]...)
		}
		return append([]cronRunRecord{}, runs...)
	}
	all := make([]cronRunRecord, 0)
	for _, runs := range r.runsByID {
		all = append(all, runs...)
		if len(all) > limit {
			break
		}
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

// MutatePersisted stages a cron mutation against an isolated snapshot, writes
// that snapshot durably, then publishes it as live state. A failed write leaves
// the live registry (including scratch CAS revisions) unchanged.
func (r *cronRegistry) MutatePersisted(ctx context.Context, repo *state.DocsRepository, mutate func(*cronRegistry) (bool, error)) error {
	if r == nil {
		return fmt.Errorf("cron runtime not configured")
	}
	r.persistMu.Lock()
	defer r.persistMu.Unlock()

	r.mu.Lock()
	staged := newCronRegistry()
	staged.order = append(staged.order, r.order...)
	for id, job := range r.jobs {
		job.Params = append(json.RawMessage(nil), job.Params...)
		staged.jobs[id] = job
	}
	for id, scratch := range r.scratch {
		staged.scratch[id] = scratch
	}
	for id, revision := range r.scratchRevisions {
		staged.scratchRevisions[id] = revision
	}
	r.mu.Unlock()

	changed, err := mutate(staged)
	if err != nil || !changed {
		return err
	}
	if err := staged.Save(ctx, repo); err != nil {
		return err
	}

	r.mu.Lock()
	for id := range r.jobs {
		if _, exists := staged.jobs[id]; !exists {
			delete(r.runsByID, id)
		}
	}
	r.jobs = staged.jobs
	r.order = staged.order
	r.scratch = staged.scratch
	r.scratchRevisions = staged.scratchRevisions
	r.mu.Unlock()
	return nil
}

// Save persists cron jobs and their scratch documents to the DocsRepository so
// they survive daemon restarts. Runs are intentionally not persisted.
func (r *cronRegistry) Save(ctx context.Context, repo *state.DocsRepository) error {
	if repo == nil {
		return fmt.Errorf("%w: docs repository is nil", errCronPersistenceUnavailable)
	}
	r.mu.Lock()
	persisted := cronPersistedState{
		Version:          2,
		Jobs:             make([]cronJobRecord, 0, len(r.jobs)),
		Scratch:          make(map[string]cronScratchRecord, len(r.scratch)),
		ScratchRevisions: make(map[string]int, len(r.scratchRevisions)),
	}
	for _, id := range r.order {
		if job, ok := r.jobs[id]; ok {
			persisted.Jobs = append(persisted.Jobs, job)
		}
	}
	for id, scratch := range r.scratch {
		persisted.Scratch[id] = scratch
	}
	for id, revision := range r.scratchRevisions {
		persisted.ScratchRevisions[id] = revision
	}
	r.mu.Unlock()

	raw, err := json.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("marshal cron state: %w", err)
	}
	_, err = repo.PutCronJobs(ctx, raw)
	return err
}

// Load restores cron jobs and scratch documents from the DocsRepository. Legacy
// job-array payloads remain supported so upgrades preserve existing schedules.
func (r *cronRegistry) Load(ctx context.Context, repo *state.DocsRepository) error {
	if repo == nil {
		return nil // no-op when store is unavailable (e.g. tests)
	}
	raw, err := repo.GetCronJobs(ctx)
	if err != nil {
		return fmt.Errorf("get cron jobs: %w", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil // nothing persisted yet
	}

	persisted := cronPersistedState{Version: 1}
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(raw, &persisted.Jobs); err != nil {
			return fmt.Errorf("unmarshal legacy cron jobs: %w", err)
		}
	} else if err := json.Unmarshal(raw, &persisted); err != nil {
		return fmt.Errorf("unmarshal cron state: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range persisted.Jobs {
		if _, exists := r.jobs[job.ID]; !exists {
			r.order = append(r.order, job.ID)
		}
		r.jobs[job.ID] = job
	}
	if r.scratch == nil {
		r.scratch = make(map[string]cronScratchRecord)
	}
	if r.scratchRevisions == nil {
		r.scratchRevisions = make(map[string]int)
	}
	for id, scratch := range persisted.Scratch {
		if _, exists := r.jobs[id]; !exists {
			continue
		}
		r.scratch[id] = scratch
		if scratch.Revision > r.scratchRevisions[id] {
			r.scratchRevisions[id] = scratch.Revision
		}
	}
	for id, revision := range persisted.ScratchRevisions {
		if _, exists := r.jobs[id]; exists && revision > r.scratchRevisions[id] {
			r.scratchRevisions[id] = revision
		}
	}
	return nil
}

type execApprovalPendingRecord struct {
	ID                   string         `json:"id"`
	Kind                 string         `json:"kind"`
	Presentation         map[string]any `json:"presentation,omitempty"`
	NodeID               string         `json:"node_id,omitempty"`
	AgentID              *string        `json:"agent_id,omitempty"`
	SessionKey           *string        `json:"session_key,omitempty"`
	Command              string         `json:"command"`
	CommandArgv          []string       `json:"command_argv,omitempty"`
	Args                 map[string]any `json:"args,omitempty"`
	CWD                  *string        `json:"cwd,omitempty"`
	Host                 *string        `json:"host,omitempty"`
	AnalysisWarnings     []string       `json:"analysis_warnings,omitempty"`
	AnalysisSummary      string         `json:"analysis_summary,omitempty"`
	AnalysisSignature    string         `json:"analysis_signature,omitempty"`
	AllowAlwaysAvailable bool           `json:"allow_always_available,omitempty"`
	AllowAlwaysReason    string         `json:"allow_always_reason,omitempty"`
	ApprovalMode         string         `json:"approval_mode,omitempty"`
	TimeoutMS            int            `json:"timeout_ms"`
	Status               string         `json:"status"`
	Decision             string         `json:"decision,omitempty"`
	Reason               string         `json:"reason,omitempty"`
	Requested            int64          `json:"requested_at"`
	ResolvedAt           int64          `json:"resolved_at,omitempty"`
	ExpiresAt            int64          `json:"expires_at,omitempty"`
}

type execApprovalsRegistry struct {
	mu               sync.Mutex
	global           map[string]any
	perNode          map[string]map[string]any
	pending          map[string]execApprovalPendingRecord
	storagePath      string
	pendingID        int64
	watchers         map[string][]chan execApprovalPendingRecord
	onWaitRegistered func(id string)
}

func newExecApprovalsRegistry() *execApprovalsRegistry {
	registry, _ := newExecApprovalsRegistryAt("")
	return registry
}

func (r *execApprovalsRegistry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	ttlMS := int64(approvalTTL.Milliseconds())
	next := cloneExecApprovalRecords(r.pending)
	notifications := map[string]execApprovalPendingRecord{}
	changed := false
	for id, rec := range next {
		if rec.Status == "resolved" && now-rec.ResolvedAt > ttlMS {
			delete(next, id)
			changed = true
			continue
		}
		if rec.Status == "pending" && rec.ExpiresAt > 0 && now >= rec.ExpiresAt {
			rec.Status = "resolved"
			rec.Decision = "deny"
			rec.Reason = "approval expired"
			rec.ResolvedAt = now
			next[id] = rec
			notifications[id] = rec
			changed = true
		}
	}
	if len(next) > maxPendingApprovals {
		oldest := make([]execApprovalPendingRecord, 0, len(next))
		for _, rec := range next {
			oldest = append(oldest, rec)
		}
		sort.Slice(oldest, func(i, j int) bool {
			if oldest[i].Requested == oldest[j].Requested {
				return oldest[i].ID < oldest[j].ID
			}
			return oldest[i].Requested < oldest[j].Requested
		})
		for i := 0; i < len(oldest)-maxPendingApprovals; i++ {
			delete(next, oldest[i].ID)
			changed = true
		}
	}
	if !changed || r.persistApprovalsLocked(next) != nil {
		return
	}
	r.pending = next
	for id, rec := range notifications {
		r.notifyWatchers(id, rec)
	}
}

func (r *execApprovalsRegistry) GetGlobal() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneMapAny(r.global)
}

func (r *execApprovalsRegistry) SetGlobal(next map[string]any) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.global = cloneMapAny(next)
	return cloneMapAny(r.global)
}

func (r *execApprovalsRegistry) GetNode(nodeID string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	approvals := r.perNode[nodeID]
	return cloneMapAny(approvals)
}

func (r *execApprovalsRegistry) SetNode(nodeID string, next map[string]any) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.perNode[nodeID] = cloneMapAny(next)
	return cloneMapAny(r.perNode[nodeID])
}

func (r *execApprovalsRegistry) Request(req methods.ExecApprovalRequestRequest) execApprovalPendingRecord {
	rec, _ := r.RequestDurable(req)
	return rec
}

func (r *execApprovalsRegistry) Resolve(req methods.ExecApprovalResolveRequest) (execApprovalPendingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.pending[req.ID]
	if !ok {
		return execApprovalPendingRecord{}, state.ErrNotFound
	}
	if rec.Status != "pending" {
		return execApprovalPendingRecord{}, fmt.Errorf("approval %q is already resolved", req.ID)
	}
	rec.Decision = req.Decision
	rec.Reason = req.Reason
	rec.Status = "resolved"
	rec.ResolvedAt = time.Now().UnixMilli()
	next := cloneExecApprovalRecords(r.pending)
	next[req.ID] = rec
	if err := r.persistApprovalsLocked(next); err != nil {
		return execApprovalPendingRecord{}, err
	}
	r.pending = next
	r.notifyWatchers(req.ID, rec)
	return cloneExecApprovalRecord(rec), nil
}

func (r *execApprovalsRegistry) GetPending(id string) (execApprovalPendingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.pending[id]
	if !ok || rec.Status != "pending" || (rec.ExpiresAt > 0 && time.Now().UnixMilli() >= rec.ExpiresAt) {
		return execApprovalPendingRecord{}, state.ErrNotFound
	}
	return cloneExecApprovalRecord(rec), nil
}

func (r *execApprovalsRegistry) ListPending() []execApprovalPendingRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	out := make([]execApprovalPendingRecord, 0, len(r.pending))
	for _, rec := range r.pending {
		if rec.Status != "pending" || (rec.ExpiresAt > 0 && now >= rec.ExpiresAt) {
			continue
		}
		out = append(out, cloneExecApprovalRecord(rec))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Requested == out[j].Requested {
			return out[i].ID < out[j].ID
		}
		return out[i].Requested < out[j].Requested
	})
	return out
}

func cloneExecApprovalRecord(rec execApprovalPendingRecord) execApprovalPendingRecord {
	out := rec
	out.CommandArgv = append([]string(nil), rec.CommandArgv...)
	out.Args = cloneMapAny(rec.Args)
	out.Presentation = cloneMapAny(rec.Presentation)
	out.AnalysisWarnings = append([]string(nil), rec.AnalysisWarnings...)
	if rec.CWD != nil {
		cwd := *rec.CWD
		out.CWD = &cwd
	}
	if rec.AgentID != nil {
		agentID := *rec.AgentID
		out.AgentID = &agentID
	}
	if rec.SessionKey != nil {
		sessionKey := *rec.SessionKey
		out.SessionKey = &sessionKey
	}
	if rec.Host != nil {
		host := *rec.Host
		out.Host = &host
	}
	return out
}

func (r *execApprovalsRegistry) WaitForDecision(ctx context.Context, id string, timeoutMS int) (execApprovalPendingRecord, bool, error) {
	r.mu.Lock()
	rec, ok := r.pending[id]
	if !ok {
		r.mu.Unlock()
		return execApprovalPendingRecord{}, false, state.ErrNotFound
	}
	if rec.Status == "resolved" {
		r.mu.Unlock()
		return rec, true, nil
	}
	ch := make(chan execApprovalPendingRecord, 1)
	r.watchers[id] = append(r.watchers[id], ch)
	onWaitRegistered := r.onWaitRegistered
	r.mu.Unlock()

	if onWaitRegistered != nil {
		onWaitRegistered(id)
	}

	defer func() {
		r.mu.Lock()
		r.removeWatcher(id, ch)
		r.mu.Unlock()
		close(ch)
	}()

	timeout := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timeout.Stop()
	var expiryTimer *time.Timer
	var expiry <-chan time.Time
	if rec.ExpiresAt > 0 {
		untilExpiry := time.Until(time.UnixMilli(rec.ExpiresAt))
		if untilExpiry <= 0 {
			return r.terminalizePending(id, "approval expired")
		}
		expiryTimer = time.NewTimer(untilExpiry)
		expiry = expiryTimer.C
		defer expiryTimer.Stop()
	}

	select {
	case <-ctx.Done():
		current, err := r.GetApproval(id)
		return current, current.Status == "resolved", err
	case <-timeout.C:
		current, err := r.GetApproval(id)
		return current, current.Status == "resolved", err
	case <-expiry:
		return r.terminalizePending(id, "approval expired")
	case updated := <-ch:
		return updated, updated.Status == "resolved", nil
	}
}

func (r *execApprovalsRegistry) notifyWatchers(id string, rec execApprovalPendingRecord) {
	for _, ch := range r.watchers[id] {
		select {
		case ch <- rec:
		default:
		}
	}
	delete(r.watchers, id)
}

func (r *execApprovalsRegistry) removeWatcher(id string, ch chan execApprovalPendingRecord) {
	watchers := r.watchers[id]
	for i, watcher := range watchers {
		if watcher == ch {
			r.watchers[id] = append(watchers[:i], watchers[i+1:]...)
			if len(r.watchers[id]) == 0 {
				delete(r.watchers, id)
			}
			break
		}
	}
}

// wizardStep describes a single UI step in the onboarding wizard.
type wizardStep struct {
	// ID is the machine-readable key this step collects (also used as Input map key).
	ID string `json:"id"`
	// Type is "text", "choice", "confirm", or "info".
	Type string `json:"type"`
	// Prompt is the human-readable question/instruction.
	Prompt string `json:"prompt"`
	// Options lists selectable values for "choice" steps.
	Options []string `json:"options,omitempty"`
	// Default is the pre-filled default value.
	Default string `json:"default,omitempty"`
	// Required marks steps whose input must be non-empty before advancing.
	Required bool `json:"required,omitempty"`
	// Secret masks the input display (e.g. for API keys / nsec).
	Secret bool `json:"secret,omitempty"`
}

type wizardSessionRecord struct {
	SessionID string         `json:"session_id"`
	Mode      string         `json:"mode"`
	Status    string         `json:"status"`
	Error     string         `json:"error,omitempty"`
	Step      int            `json:"step"`
	Input     map[string]any `json:"input,omitempty"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`
}

type wizardRegistry struct {
	mu       sync.Mutex
	sessions map[string]wizardSessionRecord
	// onComplete is called after the wizard reaches the final step.
	// The caller may use it to persist wizard results to config.
	onComplete func(rec wizardSessionRecord)
}

func newWizardRegistry() *wizardRegistry {
	return &wizardRegistry{sessions: map[string]wizardSessionRecord{}}
}

// computeSteps returns the ordered step list for the given wizard mode and
// already-collected input (so conditional steps can be included/excluded).
func computeWizardSteps(mode string, input map[string]any) []wizardStep {
	switch mode {
	case "quick":
		// Minimal setup: choose provider + API key.
		steps := []wizardStep{
			{ID: "provider", Type: "choice", Prompt: "Select your AI provider", Options: []string{"anthropic", "openai", "ollama", "google"}, Default: "anthropic"},
		}
		provider, _ := input["provider"].(string)
		if provider != "ollama" {
			steps = append(steps, wizardStep{ID: "api_key", Type: "text", Prompt: "Enter your API key", Required: true, Secret: true})
		}
		steps = append(steps, wizardStep{ID: "confirm", Type: "confirm", Prompt: "Apply these settings?"})
		return steps

	default: // "onboarding" or any unknown mode → full setup
		// Step 0: Nostr key choice.
		steps := []wizardStep{
			{ID: "nostr_key_action", Type: "choice", Prompt: "How do you want to set up your Nostr identity?", Options: []string{"generate", "import"}, Default: "generate"},
		}
		// Step 1: nsec entry only if importing.
		keyAction, _ := input["nostr_key_action"].(string)
		if keyAction == "import" {
			steps = append(steps, wizardStep{ID: "nsec", Type: "text", Prompt: "Enter your nsec (private key, starts with nsec1…)", Required: true, Secret: true})
		}
		// Step 2: Relay URLs.
		steps = append(steps, wizardStep{
			ID: "relays", Type: "text",
			Prompt: "Enter relay URLs (comma-separated)",
		})
		// Step 3: Agent display name.
		steps = append(steps, wizardStep{ID: "agent_name", Type: "text", Prompt: "Agent display name", Default: "metiq"})
		// Step 4: AI provider.
		steps = append(steps, wizardStep{ID: "provider", Type: "choice", Prompt: "Select your AI provider", Options: []string{"anthropic", "openai", "ollama", "google"}, Default: "anthropic"})
		// Step 5: API key (skip for ollama).
		provider, _ := input["provider"].(string)
		if provider != "ollama" {
			steps = append(steps, wizardStep{ID: "api_key", Type: "text", Prompt: "Enter your API key", Required: true, Secret: true})
		}
		// Step 6: Workspace directory.
		defaultWorkspace := workspace.ResolveWorkspaceDir(state.ConfigDoc{}, "")
		steps = append(steps, wizardStep{ID: "workspace_dir", Type: "text", Prompt: "Workspace directory", Default: defaultWorkspace})
		// Final: confirm.
		steps = append(steps, wizardStep{ID: "confirm", Type: "confirm", Prompt: "Apply these settings and start metiq?"})
		return steps
	}
}

// currentWizardStep returns the step definition for the given step index,
// or nil if the wizard is complete.
func currentWizardStep(rec wizardSessionRecord) *wizardStep {
	steps := computeWizardSteps(rec.Mode, rec.Input)
	if rec.Step >= len(steps) {
		return nil
	}
	s := steps[rec.Step]
	return &s
}

func (r *wizardRegistry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	ttlMS := int64(wizardTTL.Milliseconds())
	for id, rec := range r.sessions {
		if (rec.Status == "done" || rec.Status == "cancelled") && now-rec.UpdatedAt > ttlMS {
			delete(r.sessions, id)
		}
	}
	if len(r.sessions) > maxWizardSessions {
		oldest := make([]wizardSessionRecord, 0, len(r.sessions))
		for _, rec := range r.sessions {
			oldest = append(oldest, rec)
		}
		sort.Slice(oldest, func(i, j int) bool {
			return oldest[i].UpdatedAt < oldest[j].UpdatedAt
		})
		excess := len(oldest) - maxWizardSessions
		for i := 0; i < excess; i++ {
			delete(r.sessions, oldest[i].SessionID)
		}
	}
}

// Start creates a new wizard session and returns the first step.
func (r *wizardRegistry) Start(req methods.WizardStartRequest) (wizardSessionRecord, *wizardStep) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	sessionID := fmt.Sprintf("wizard-%d", time.Now().UnixNano())
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "onboarding"
	}
	rec := wizardSessionRecord{
		SessionID: sessionID,
		Mode:      mode,
		Status:    "running",
		Step:      0,
		Input:     map[string]any{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.sessions[sessionID] = rec
	step := currentWizardStep(rec)
	return rec, step
}

// Next advances the wizard session by one step, validates input, and returns
// the next step definition (or nil if the wizard is complete).
func (r *wizardRegistry) Next(req methods.WizardNextRequest) (wizardSessionRecord, *wizardStep, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.sessions[req.ID]
	if !ok {
		return wizardSessionRecord{}, nil, false, state.ErrNotFound
	}
	if rec.Status != "running" {
		// Already done or cancelled — return the final state.
		return rec, nil, rec.Status == "done", nil
	}

	// Get the current step definition before advancing.
	steps := computeWizardSteps(rec.Mode, rec.Input)
	if rec.Step >= len(steps) {
		// Shouldn't happen; treat as done.
		rec.Status = "done"
		rec.UpdatedAt = time.Now().UnixMilli()
		r.sessions[req.ID] = rec
		return rec, nil, true, nil
	}
	curStep := steps[rec.Step]

	// Merge supplied input into session input.
	if len(req.Input) > 0 {
		for k, v := range req.Input {
			rec.Input[k] = v
		}
	}

	// Validate required fields.
	if curStep.Required {
		val, _ := rec.Input[curStep.ID].(string)
		if strings.TrimSpace(val) == "" {
			// Return the same step with an error message.
			return rec, &curStep, false, fmt.Errorf("%s is required", curStep.Prompt)
		}
	}

	rec.Step++
	rec.UpdatedAt = time.Now().UnixMilli()

	// Re-compute steps with updated input (handles conditional steps).
	steps = computeWizardSteps(rec.Mode, rec.Input)
	done := rec.Step >= len(steps)
	if done {
		rec.Status = "done"
	}
	r.sessions[req.ID] = rec

	var nextStep *wizardStep
	if !done {
		s := steps[rec.Step]
		nextStep = &s
	}

	if done && r.onComplete != nil {
		go r.onComplete(rec)
	}

	return rec, nextStep, done, nil
}

func (r *wizardRegistry) Cancel(req methods.WizardCancelRequest) (wizardSessionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.sessions[req.ID]
	if !ok {
		return wizardSessionRecord{}, state.ErrNotFound
	}
	rec.Status = "cancelled"
	rec.UpdatedAt = time.Now().UnixMilli()
	r.sessions[req.ID] = rec
	return rec, nil
}

func (r *wizardRegistry) Status(req methods.WizardStatusRequest) (wizardSessionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.ID == "" {
		// Return the most-recently-updated active session, if any.
		var latest *wizardSessionRecord
		for _, rec := range r.sessions {
			rec := rec
			if rec.Status == "running" {
				if latest == nil || rec.UpdatedAt > latest.UpdatedAt {
					latest = &rec
				}
			}
		}
		if latest == nil {
			return wizardSessionRecord{}, state.ErrNotFound
		}
		return *latest, nil
	}
	rec, ok := r.sessions[req.ID]
	if !ok {
		return wizardSessionRecord{}, state.ErrNotFound
	}
	return rec, nil
}

type heartbeatWakeRecord struct {
	AtMS    int64
	AgentID string
	Source  string
	Text    string
	Mode    string
}

type heartbeatRunnerStatus struct {
	Enabled      bool
	IntervalMS   int
	LastRunMS    int64
	LastWakeMS   int64
	PendingWakes int
}

type operationsRegistry struct {
	mu                        sync.Mutex
	talkMode                  string
	voicewake                 []string
	ttsEnabled                bool
	ttsProvider               string
	ttsPersona                string
	heartbeatRunnerEnabled    bool
	heartbeatRunnerIntervalMS int
	lastHeartbeatRunMS        int64
	lastHeartbeatWakeMS       int64
	heartbeatNotify           chan struct{}
	pendingHeartbeatWakes     []heartbeatWakeRecord
	lastUpdateCheckMS         int64
	systemPresence            map[string]map[string]any
	systemEvents              []map[string]any
}

func newOperationsRegistry() *operationsRegistry {
	now := time.Now().UnixMilli()
	return &operationsRegistry{
		talkMode:                  "disabled",
		voicewake:                 []string{"openclaw", "metiq"},
		ttsEnabled:                false,
		ttsProvider:               "openai",
		heartbeatRunnerEnabled:    false,
		heartbeatRunnerIntervalMS: 0,
		heartbeatNotify:           make(chan struct{}),
		lastUpdateCheckMS:         now,
		systemPresence:            map[string]map[string]any{},
		systemEvents:              []map[string]any{},
	}
}

func (r *operationsRegistry) SetTalkMode(mode string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.talkMode = mode
	return r.talkMode
}

func (r *operationsRegistry) TalkMode() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.talkMode
}

func (r *operationsRegistry) Voicewake() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.voicewake...)
}

func (r *operationsRegistry) SetVoicewake(triggers []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.voicewake = append([]string{}, triggers...)
	return append([]string{}, r.voicewake...)
}

func (r *operationsRegistry) TTSStatus() (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ttsEnabled, r.ttsProvider
}

func (r *operationsRegistry) SetTTSEnabled(enabled bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ttsEnabled = enabled
	return r.ttsEnabled
}

// TTSPersona returns the active tts persona id (empty when unset).
func (r *operationsRegistry) TTSPersona() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ttsPersona
}

// SetTTSPersona persists the active tts persona id (empty clears it).
func (r *operationsRegistry) SetTTSPersona(persona string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ttsPersona = strings.TrimSpace(persona)
	return r.ttsPersona
}

func (r *operationsRegistry) SetTTSProvider(provider string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ttsProvider = strings.TrimSpace(provider)
	if r.ttsProvider == "" {
		r.ttsProvider = "openai"
	}
	validProviders := map[string]bool{"openai": true, "kokoro": true, "elevenlabs": true, "edge": true}
	if !validProviders[r.ttsProvider] {
		r.ttsProvider = "openai"
	}
	return r.ttsProvider
}

func (r *operationsRegistry) notifyHeartbeatLocked() {
	if r.heartbeatNotify != nil {
		close(r.heartbeatNotify)
	}
	r.heartbeatNotify = make(chan struct{})
}

func (r *operationsRegistry) heartbeatStatusLocked() heartbeatRunnerStatus {
	return heartbeatRunnerStatus{
		Enabled:      r.heartbeatRunnerEnabled,
		IntervalMS:   r.heartbeatRunnerIntervalMS,
		LastRunMS:    r.lastHeartbeatRunMS,
		LastWakeMS:   r.lastHeartbeatWakeMS,
		PendingWakes: len(r.pendingHeartbeatWakes),
	}
}

func (r *operationsRegistry) HeartbeatStatus() heartbeatRunnerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.heartbeatStatusLocked()
}

func (r *operationsRegistry) SyncHeartbeatConfig(cfg state.HeartbeatConfig) heartbeatRunnerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.heartbeatRunnerEnabled = cfg.Enabled
	if cfg.IntervalMS >= 0 {
		r.heartbeatRunnerIntervalMS = cfg.IntervalMS
	}
	r.notifyHeartbeatLocked()
	return r.heartbeatStatusLocked()
}

func (r *operationsRegistry) SetHeartbeats(enabled *bool, intervalMS int) heartbeatRunnerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if enabled != nil {
		r.heartbeatRunnerEnabled = *enabled
	}
	if intervalMS >= 0 {
		r.heartbeatRunnerIntervalMS = intervalMS
	}
	r.notifyHeartbeatLocked()
	return r.heartbeatStatusLocked()
}

func (r *operationsRegistry) QueueHeartbeatWake(agentID, source, text, mode string) heartbeatRunnerStatus {
	return r.QueueHeartbeatWakeAt(agentID, source, text, mode, time.Now().UnixMilli())
}

func (r *operationsRegistry) QueueHeartbeatWakeAt(agentID, source, text, mode string, atMS int64) heartbeatRunnerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	if atMS <= 0 {
		atMS = now
	}
	r.lastHeartbeatWakeMS = now
	r.pendingHeartbeatWakes = append(r.pendingHeartbeatWakes, heartbeatWakeRecord{
		AtMS:    atMS,
		AgentID: strings.TrimSpace(agentID),
		Source:  strings.TrimSpace(source),
		Text:    strings.TrimSpace(text),
		Mode:    strings.ToLower(strings.TrimSpace(mode)),
	})
	r.notifyHeartbeatLocked()
	return r.heartbeatStatusLocked()
}

func (r *operationsRegistry) HeartbeatSnapshot() (heartbeatRunnerStatus, []heartbeatWakeRecord, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	wakes := append([]heartbeatWakeRecord(nil), r.pendingHeartbeatWakes...)
	return r.heartbeatStatusLocked(), wakes, r.heartbeatNotify
}

func (r *operationsRegistry) ConsumeHeartbeatWakes() []heartbeatWakeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pendingHeartbeatWakes) == 0 {
		return nil
	}
	wakes := append([]heartbeatWakeRecord(nil), r.pendingHeartbeatWakes...)
	r.pendingHeartbeatWakes = nil
	return wakes
}

func (r *operationsRegistry) ConsumeDueHeartbeatWakes(nowMS int64) []heartbeatWakeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pendingHeartbeatWakes) == 0 {
		return nil
	}
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	due := make([]heartbeatWakeRecord, 0, len(r.pendingHeartbeatWakes))
	pending := make([]heartbeatWakeRecord, 0, len(r.pendingHeartbeatWakes))
	for _, wake := range r.pendingHeartbeatWakes {
		if wake.AtMS <= 0 || wake.AtMS <= nowMS {
			due = append(due, wake)
			continue
		}
		pending = append(pending, wake)
	}
	r.pendingHeartbeatWakes = pending
	return due
}

func (r *operationsRegistry) MarkHeartbeatRun(tsMS int64) heartbeatRunnerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if tsMS <= 0 {
		tsMS = time.Now().UnixMilli()
	}
	r.lastHeartbeatRunMS = tsMS
	return r.heartbeatStatusLocked()
}

func (r *operationsRegistry) RecordUpdateCheck() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastUpdateCheckMS = time.Now().UnixMilli()
	return r.lastUpdateCheckMS
}

func (r *operationsRegistry) LastUpdateCheck() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastUpdateCheckMS
}

func (r *operationsRegistry) ListSystemPresence() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, 0, len(r.systemPresence))
	for _, rec := range r.systemPresence {
		out = append(out, cloneMapAny(rec))
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["updated_at_ms"].(int64)
		b, _ := out[j]["updated_at_ms"].(int64)
		return a > b
	})
	return out
}

func (r *operationsRegistry) RecordSystemEvent(req methods.SystemEventRequest) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	key := strings.TrimSpace(req.DeviceID)
	if key == "" {
		key = strings.TrimSpace(req.InstanceID)
	}
	if key == "" {
		key = "default"
	}
	rec, ok := r.systemPresence[key]
	if !ok {
		rec = map[string]any{}
	}
	rec["key"] = key
	rec["text"] = req.Text
	rec["deviceId"] = req.DeviceID
	rec["instanceId"] = req.InstanceID
	rec["host"] = req.Host
	rec["ip"] = req.IP
	rec["mode"] = req.Mode
	rec["version"] = req.Version
	rec["platform"] = req.Platform
	rec["deviceFamily"] = req.DeviceFamily
	rec["modelIdentifier"] = req.ModelIdentifier
	rec["lastInputSeconds"] = req.LastInputSeconds
	rec["reason"] = req.Reason
	rec["roles"] = append([]string{}, req.Roles...)
	rec["scopes"] = append([]string{}, req.Scopes...)
	rec["tags"] = append([]string{}, req.Tags...)
	rec["updated_at_ms"] = now
	r.systemPresence[key] = rec
	event := map[string]any{"text": req.Text, "key": key, "ts": now}
	r.systemEvents = append(r.systemEvents, event)
	if len(r.systemEvents) > 200 {
		r.systemEvents = r.systemEvents[len(r.systemEvents)-200:]
	}
	return cloneMapAny(rec)
}

func cloneMapAny(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneRuntimeAny(v)
	}
	return out
}

func cloneRuntimeAny(in any) any {
	if in == nil {
		return nil
	}
	return cloneRuntimeReflectValue(reflect.ValueOf(in)).Interface()
}

func cloneRuntimeReflectValue(in reflect.Value) reflect.Value {
	if !in.IsValid() {
		return in
	}
	switch in.Kind() {
	case reflect.Interface:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		cloned := cloneRuntimeReflectValue(in.Elem())
		if cloned.Type().AssignableTo(in.Type()) {
			return cloned
		}
		out := reflect.New(in.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Pointer:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		out := reflect.New(in.Type().Elem())
		out.Elem().Set(cloneRuntimeReflectValue(in.Elem()))
		return out
	case reflect.Map:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		out := reflect.MakeMapWithSize(in.Type(), in.Len())
		iter := in.MapRange()
		for iter.Next() {
			out.SetMapIndex(cloneRuntimeReflectValue(iter.Key()), cloneRuntimeReflectValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if in.IsNil() {
			return reflect.Zero(in.Type())
		}
		out := reflect.MakeSlice(in.Type(), in.Len(), in.Len())
		for i := 0; i < in.Len(); i++ {
			out.Index(i).Set(cloneRuntimeReflectValue(in.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(in.Type()).Elem()
		for i := 0; i < in.Len(); i++ {
			out.Index(i).Set(cloneRuntimeReflectValue(in.Index(i)))
		}
		return out
	default:
		return in
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
