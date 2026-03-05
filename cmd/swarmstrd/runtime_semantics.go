package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"swarmstr/internal/gateway/methods"
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

type nodeInvocationEvent struct {
	Type    string         `json:"type"`
	Status  string         `json:"status,omitempty"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	UnixMS  int64          `json:"unix_ms"`
}

type nodeInvocationRecord struct {
	RunID     string                `json:"run_id"`
	NodeID    string                `json:"node_id"`
	Command   string                `json:"command"`
	Args      map[string]any        `json:"args,omitempty"`
	TimeoutMS int                   `json:"timeout_ms"`
	Status    string                `json:"status"`
	CreatedAt int64                 `json:"created_at"`
	UpdatedAt int64                 `json:"updated_at"`
	Result    any                   `json:"result,omitempty"`
	Error     string                `json:"error,omitempty"`
	Events    []nodeInvocationEvent `json:"events,omitempty"`
}

const (
	maxNodeInvocations = 1000
	maxCronRuns        = 500
	maxPendingApprovals = 200
	maxWizardSessions  = 100
	invocationTTL      = 24 * time.Hour
	approvalTTL        = 1 * time.Hour
	wizardTTL          = 2 * time.Hour
)

type nodeInvocationRegistry struct {
	mu    sync.Mutex
	runs  map[string]nodeInvocationRecord
	order []string
}

func newNodeInvocationRegistry() *nodeInvocationRegistry {
	return &nodeInvocationRegistry{runs: map[string]nodeInvocationRecord{}, order: []string{}}
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
		RunID:     runID,
		NodeID:    req.NodeID,
		Command:   req.Command,
		Args:      req.Args,
		TimeoutMS: req.TimeoutMS,
		Status:    "queued",
		CreatedAt: now,
		UpdatedAt: now,
		Events:    []nodeInvocationEvent{},
	}
	if !exists {
		r.order = append(r.order, runID)
	}
	r.runs[runID] = rec
	return rec
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

type cronRunRecord struct {
	RunID    string `json:"run_id"`
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	Started  int64  `json:"started_at"`
	Finished int64  `json:"finished_at"`
}

type cronRegistry struct {
	mu       sync.Mutex
	jobs     map[string]cronJobRecord
	order    []string
	runsByID map[string][]cronRunRecord
}

func newCronRegistry() *cronRegistry {
	return &cronRegistry{jobs: map[string]cronJobRecord{}, order: []string{}, runsByID: map[string][]cronRunRecord{}}
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
	return nil
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

type execApprovalPendingRecord struct {
	ID         string         `json:"id"`
	NodeID     string         `json:"node_id,omitempty"`
	Command    string         `json:"command"`
	Args       map[string]any `json:"args,omitempty"`
	TimeoutMS  int            `json:"timeout_ms"`
	Status     string         `json:"status"`
	Decision   string         `json:"decision,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Requested  int64          `json:"requested_at"`
	ResolvedAt int64          `json:"resolved_at,omitempty"`
	ExpiresAt  int64          `json:"expires_at,omitempty"`
}

type execApprovalsRegistry struct {
	mu        sync.Mutex
	global    map[string]any
	perNode   map[string]map[string]any
	pending   map[string]execApprovalPendingRecord
	pendingID int64
	watchers  map[string][]chan execApprovalPendingRecord
}

func newExecApprovalsRegistry() *execApprovalsRegistry {
	return &execApprovalsRegistry{
		global:   map[string]any{},
		perNode:  map[string]map[string]any{},
		pending:  map[string]execApprovalPendingRecord{},
		watchers: map[string][]chan execApprovalPendingRecord{},
	}
}

func (r *execApprovalsRegistry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	ttlMS := int64(approvalTTL.Milliseconds())
	for id, rec := range r.pending {
		if rec.Status == "resolved" && now-rec.ResolvedAt > ttlMS {
			delete(r.pending, id)
		} else if rec.ExpiresAt > 0 && now > rec.ExpiresAt {
			delete(r.pending, id)
		}
	}
	if len(r.pending) > maxPendingApprovals {
		oldest := make([]string, 0, len(r.pending))
		for id := range r.pending {
			oldest = append(oldest, id)
		}
		excess := len(oldest) - maxPendingApprovals
		for i := 0; i < excess; i++ {
			delete(r.pending, oldest[i])
		}
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingID++
	now := time.Now().UnixMilli()
	id := fmt.Sprintf("approval-%d-%d", now, r.pendingID)
	rec := execApprovalPendingRecord{
		ID:        id,
		NodeID:    req.NodeID,
		Command:   req.Command,
		Args:      req.Args,
		TimeoutMS: req.TimeoutMS,
		Status:    "pending",
		Requested: now,
		ExpiresAt: now + int64(req.TimeoutMS),
	}
	r.pending[id] = rec
	return rec
}

func (r *execApprovalsRegistry) Resolve(req methods.ExecApprovalResolveRequest) (execApprovalPendingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.pending[req.ID]
	if !ok {
		return execApprovalPendingRecord{}, state.ErrNotFound
	}
	rec.Decision = req.Decision
	rec.Reason = req.Reason
	rec.Status = "resolved"
	rec.ResolvedAt = time.Now().UnixMilli()
	r.pending[req.ID] = rec
	r.notifyWatchers(req.ID, rec)
	return rec, nil
}

func (r *execApprovalsRegistry) GetPending(id string) (execApprovalPendingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.pending[id]
	if !ok {
		return execApprovalPendingRecord{}, state.ErrNotFound
	}
	return rec, nil
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
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.removeWatcher(id, ch)
		r.mu.Unlock()
		close(ch)
	}()

	timeout := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timeout.Stop()

	expireTicker := time.NewTicker(1 * time.Second)
	defer expireTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			rec, _ := r.pending[id]
			r.mu.Unlock()
			return rec, false, nil
		case <-timeout.C:
			r.mu.Lock()
			rec, _ := r.pending[id]
			r.mu.Unlock()
			return rec, false, nil
		case updated := <-ch:
			if updated.Status == "resolved" {
				return updated, true, nil
			}
		case <-expireTicker.C:
			r.mu.Lock()
			rec, ok := r.pending[id]
			if ok && rec.ExpiresAt > 0 && time.Now().UnixMilli() > rec.ExpiresAt {
				r.mu.Unlock()
				return rec, false, nil
			}
			r.mu.Unlock()
		}
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
}

func newWizardRegistry() *wizardRegistry {
	return &wizardRegistry{sessions: map[string]wizardSessionRecord{}}
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

func (r *wizardRegistry) Start(req methods.WizardStartRequest) wizardSessionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	sessionID := fmt.Sprintf("wizard-%d", time.Now().UnixNano())
	rec := wizardSessionRecord{SessionID: sessionID, Mode: req.Mode, Status: "running", Step: 0, Input: map[string]any{}, CreatedAt: now, UpdatedAt: now}
	r.sessions[sessionID] = rec
	return rec
}

func (r *wizardRegistry) Next(req methods.WizardNextRequest) (wizardSessionRecord, map[string]any, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.sessions[req.ID]
	if !ok {
		return wizardSessionRecord{}, nil, false, state.ErrNotFound
	}
	if rec.Status != "running" {
		return rec, nil, true, nil
	}
	if len(req.Input) > 0 {
		for k, v := range req.Input {
			rec.Input[k] = v
		}
	}
	rec.Step++
	rec.UpdatedAt = time.Now().UnixMilli()
	done := rec.Step >= 1
	if done {
		rec.Status = "done"
		r.sessions[req.ID] = rec
		return rec, nil, true, nil
	}
	step := map[string]any{"id": "mode", "type": "choice", "prompt": "Select mode", "options": []string{"local", "remote"}}
	r.sessions[req.ID] = rec
	return rec, step, false, nil
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
	rec, ok := r.sessions[req.ID]
	if !ok {
		return wizardSessionRecord{}, state.ErrNotFound
	}
	return rec, nil
}

type operationsRegistry struct {
	mu          sync.Mutex
	talkMode    string
	voicewake   []string
	ttsEnabled  bool
	ttsProvider string
}

func newOperationsRegistry() *operationsRegistry {
	return &operationsRegistry{talkMode: "disabled", voicewake: []string{"openclaw", "swarmstr"}, ttsEnabled: false, ttsProvider: "openai"}
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

func (r *operationsRegistry) SetTTSProvider(provider string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ttsProvider = strings.TrimSpace(provider)
	if r.ttsProvider == "" {
		r.ttsProvider = "openai"
	}
	validProviders := map[string]bool{"openai": true, "elevenlabs": true, "edge": true}
	if !validProviders[r.ttsProvider] {
		r.ttsProvider = "openai"
	}
	return r.ttsProvider
}

func cloneMapAny(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
