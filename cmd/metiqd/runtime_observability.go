package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
)

type runtimeEventBuffer struct {
	mu      sync.Mutex
	cap     int
	nextID  int64
	entries []runtimeEventEntry
	notify  chan struct{}
}

type runtimeEventEntry struct {
	ID        int64  `json:"id"`
	TS        int64  `json:"ts_ms"`
	Event     string `json:"event"`
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	Direction string `json:"direction,omitempty"`
	Subsystem string `json:"subsystem,omitempty"`
	Source    string `json:"source,omitempty"`
	Payload   any    `json:"payload,omitempty"`
}

func newRuntimeEventBuffer(capacity int) *runtimeEventBuffer {
	if capacity <= 0 {
		capacity = 2000
	}
	return &runtimeEventBuffer{cap: capacity, notify: make(chan struct{})}
}

func (b *runtimeEventBuffer) Append(event string, payload any) {
	event = strings.TrimSpace(event)
	if event == "" {
		return
	}
	entry := runtimeEventEntry{
		TS:    time.Now().UnixMilli(),
		Event: event,
	}
	decoded := normalizeRuntimeEventPayload(payload)
	entry.Payload = decoded
	if m, ok := decoded.(map[string]any); ok {
		entry.AgentID = runtimeEventStringField(m, "agent_id")
		entry.SessionID = runtimeEventStringField(m, "session_id")
		entry.ChannelID = runtimeEventStringField(m, "channel_id")
		entry.Direction = runtimeEventStringField(m, "direction")
		entry.Subsystem = runtimeEventSubsystem(event, m)
		entry.Source = runtimeEventSource(event, m)
	} else {
		entry.Subsystem = runtimeEventSubsystem(event, nil)
		entry.Source = runtimeEventSource(event, nil)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) >= b.cap {
		b.entries = b.entries[len(b.entries)-b.cap+1:]
	}
	b.nextID++
	entry.ID = b.nextID
	b.entries = append(b.entries, entry)
	if b.notify != nil {
		close(b.notify)
	}
	b.notify = make(chan struct{})
}

func (b *runtimeEventBuffer) Tail(cursor int64, limit int, maxBytes int, filters toolbuiltin.RuntimeObserveFilters) map[string]any {
	if limit <= 0 {
		limit = 100
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	eventNames := runtimeObserveEventNames(filters.Events)

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

	filtered := make([]runtimeEventEntry, 0, len(b.entries))
	for _, entry := range b.entries[start:] {
		if !runtimeEventMatchesFilters(entry, eventNames, filters) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	events := make([]map[string]any, 0, len(filtered))
	usedBytes := 0
	truncated := false
	nextCursor := cursor
	for _, entry := range filtered {
		item := map[string]any{
			"id":      entry.ID,
			"ts_ms":   entry.TS,
			"event":   entry.Event,
			"payload": entry.Payload,
		}
		if entry.AgentID != "" {
			item["agent_id"] = entry.AgentID
		}
		if entry.SessionID != "" {
			item["session_id"] = entry.SessionID
		}
		if entry.ChannelID != "" {
			item["channel_id"] = entry.ChannelID
		}
		if entry.Direction != "" {
			item["direction"] = entry.Direction
		}
		if entry.Subsystem != "" {
			item["subsystem"] = entry.Subsystem
		}
		if entry.Source != "" {
			item["source"] = entry.Source
		}
		raw, _ := json.Marshal(item)
		if usedBytes+len(raw) > maxBytes {
			truncated = true
			if len(events) == 0 {
				nextCursor = entry.ID
			}
			break
		}
		usedBytes += len(raw)
		nextCursor = entry.ID
		events = append(events, item)
	}
	if reset && len(events) == 0 && len(filtered) == 0 && len(b.entries) > 0 {
		nextCursor = b.entries[len(b.entries)-1].ID
	}
	if nextCursor < 0 {
		nextCursor = 0
	}
	return map[string]any{
		"cursor":    nextCursor,
		"size":      len(b.entries),
		"events":    events,
		"truncated": truncated,
		"reset":     reset,
	}
}

func (b *runtimeEventBuffer) hasChangesAfterLocked(cursor int64, filters toolbuiltin.RuntimeObserveFilters) bool {
	if len(b.entries) > 0 && cursor < b.entries[0].ID {
		return true
	}
	eventNames := runtimeObserveEventNames(filters.Events)
	for _, entry := range b.entries {
		if entry.ID <= cursor {
			continue
		}
		if runtimeEventMatchesFilters(entry, eventNames, filters) {
			return true
		}
	}
	return false
}

func (b *runtimeEventBuffer) snapshotNotifier(cursor int64, filters toolbuiltin.RuntimeObserveFilters) (bool, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hasChangesAfterLocked(cursor, filters), b.notify
}

type observedEventEmitter struct {
	inner  gatewayws.EventEmitter
	buffer *runtimeEventBuffer
}

func newObservedEventEmitter(inner gatewayws.EventEmitter, buffer *runtimeEventBuffer) gatewayws.EventEmitter {
	if inner == nil {
		inner = gatewayws.NoopEmitter{}
	}
	return observedEventEmitter{inner: inner, buffer: buffer}
}

func (e observedEventEmitter) Emit(event string, payload any) {
	if e.buffer != nil {
		e.buffer.Append(event, payload)
	}
	e.inner.Emit(event, payload)
}

func normalizeRuntimeEventPayload(payload any) any {
	if payload == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("%v", payload)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return string(raw)
	}
	return decoded
}

func runtimeEventStringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func runtimeEventSubsystem(event string, payload map[string]any) string {
	if subsystem := runtimeEventStringField(payload, "subsystem"); subsystem != "" {
		return subsystem
	}
	switch {
	case strings.HasPrefix(event, "relay."):
		return "relay"
	case strings.HasPrefix(event, "dm."):
		return "dm"
	case strings.HasPrefix(event, "tool."):
		return "tool"
	case strings.HasPrefix(event, "turn."):
		return "session"
	case strings.HasPrefix(event, "chat."):
		return "chat"
	case strings.HasPrefix(event, "channel."):
		return "channel"
	case strings.HasPrefix(event, "config."):
		return "config"
	case strings.HasPrefix(event, "mcp."):
		return "mcp"
	case strings.HasPrefix(event, "agent."):
		return "agent"
	case strings.HasPrefix(event, "cron."):
		return "cron"
	case strings.HasPrefix(event, "voice."):
		return "voice"
	case strings.HasPrefix(event, "update."):
		return "update"
	case strings.HasPrefix(event, "plugin."):
		return "plugin"
	case strings.HasPrefix(event, "node."):
		return "node"
	case strings.HasPrefix(event, "device."):
		return "device"
	case strings.HasPrefix(event, "exec."):
		return "exec"
	case strings.HasPrefix(event, "canvas."):
		return "canvas"
	default:
		return ""
	}
}

func runtimeEventSource(event string, payload map[string]any) string {
	if source := runtimeEventStringField(payload, "source"); source != "" {
		return source
	}
	direction := runtimeEventStringField(payload, "direction")
	switch event {
	case gatewayws.EventChatMessage, gatewayws.EventChannelMessage:
		switch direction {
		case "inbound":
			return "inbound"
		case "outbound":
			return "reply"
		}
	case gatewayws.EventChatChunk:
		return "stream"
	}
	return ""
}

func runtimeObserveEventNames(events []string) map[string]struct{} {
	eventNames := map[string]struct{}{}
	for _, name := range events {
		name = strings.TrimSpace(name)
		if name != "" {
			eventNames[name] = struct{}{}
		}
	}
	return eventNames
}

func runtimeEventMatchesFilters(entry runtimeEventEntry, eventNames map[string]struct{}, filters toolbuiltin.RuntimeObserveFilters) bool {
	if len(eventNames) > 0 {
		if _, ok := eventNames[entry.Event]; !ok {
			return false
		}
	}
	if filters.AgentID != "" && entry.AgentID != filters.AgentID {
		return false
	}
	if filters.SessionID != "" && entry.SessionID != filters.SessionID {
		return false
	}
	if filters.ChannelID != "" && entry.ChannelID != filters.ChannelID {
		return false
	}
	if filters.Direction != "" && entry.Direction != filters.Direction {
		return false
	}
	if filters.Subsystem != "" && entry.Subsystem != filters.Subsystem {
		return false
	}
	if filters.Source != "" && entry.Source != filters.Source {
		return false
	}
	return true
}

type runtimeEventEmitterFunc func(string, any)

func (f runtimeEventEmitterFunc) Emit(event string, payload any) {
	if f != nil {
		f(event, payload)
	}
}

type runtimeShutdownEmitter struct {
	once sync.Once
	emit func(string, any)
}

func newRuntimeShutdownEmitter(emit func(string, any)) *runtimeShutdownEmitter {
	if emit == nil {
		emit = func(string, any) {}
	}
	return &runtimeShutdownEmitter{emit: emit}
}

func (e *runtimeShutdownEmitter) Emit(reason string) {
	if e == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	e.once.Do(func() {
		e.emit(gatewayws.EventShutdown, gatewayws.ShutdownPayload{
			TS:     time.Now().UnixMilli(),
			Reason: reason,
		})
	})
}

func startRuntimeHeartbeatLoop(ctx context.Context, startedAt time.Time, version string, tickInterval time.Duration, shutdown *runtimeShutdownEmitter) <-chan struct{} {
	if tickInterval <= 0 {
		tickInterval = 30 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		emitControlWSEvent(gatewayws.EventHealth, gatewayws.HealthPayload{
			TS: time.Now().UnixMilli(),
			OK: true,
		})
		emitter := runtimeEventEmitterFunc(emitControlWSEvent)
		for {
			select {
			case <-ctx.Done():
				shutdown.Emit("daemon stopping")
				return
			case <-ticker.C:
				gatewayws.EmitTick(emitter, startedAt, version)
			}
		}
	}()
	return done
}

type dmHealthReporter interface {
	HealthSnapshot() nostruntime.SubHealthSnapshot
}

type dmHealthObserver struct {
	emit func(string, any)
	last map[string]nostruntime.SubHealthSnapshot
}

func newDMHealthObserver(emitter gatewayws.EventEmitter) *dmHealthObserver {
	if emitter == nil {
		emitter = gatewayws.NoopEmitter{}
	}
	return newDMHealthObserverFunc(emitter.Emit)
}

func newDMHealthObserverFunc(emit func(string, any)) *dmHealthObserver {
	if emit == nil {
		emit = func(string, any) {}
	}
	return &dmHealthObserver{emit: emit, last: map[string]nostruntime.SubHealthSnapshot{}}
}

func (o *dmHealthObserver) EmitStartup(reporters ...dmHealthReporter) {
	for _, reporter := range reporters {
		o.emitSnapshot("startup", reporter.HealthSnapshot())
	}
}

func (o *dmHealthObserver) EmitTick(reporters ...dmHealthReporter) {
	for _, reporter := range reporters {
		snap := reporter.HealthSnapshot()
		source := "periodic"
		if prev, ok := o.last[snap.Label]; !ok {
			source = "startup"
		} else if !dmHealthSnapshotsEqual(prev, snap) {
			source = "change"
		}
		o.emitSnapshot(source, snap)
	}
}

func (o *dmHealthObserver) emitSnapshot(source string, snap nostruntime.SubHealthSnapshot) {
	if o == nil {
		return
	}
	o.last[snap.Label] = snap
	o.emit(gatewayws.EventDMHealth, gatewayws.DMHealthPayload{
		TS:               time.Now().UnixMilli(),
		Label:            snap.Label,
		BoundRelays:      append([]string(nil), snap.BoundRelays...),
		LastEventAt:      runtimeObserveUnixMillis(snap.LastEventAt),
		LastReconnectAt:  runtimeObserveUnixMillis(snap.LastReconnectAt),
		LastClosedReason: strings.TrimSpace(snap.LastClosedReason),
		ReplayWindowMS:   snap.ReplayWindowMS,
		EventCount:       snap.EventCount,
		ReconnectCount:   snap.ReconnectCount,
		Healthy:          dmHealthSnapshotHealthy(snap),
		Source:           source,
	})
}

func dmHealthSnapshotHealthy(snap nostruntime.SubHealthSnapshot) bool {
	if len(snap.BoundRelays) == 0 {
		return false
	}
	return strings.TrimSpace(snap.LastClosedReason) == ""
}

func dmHealthSnapshotsEqual(a, b nostruntime.SubHealthSnapshot) bool {
	if a.Label != b.Label ||
		a.LastClosedReason != b.LastClosedReason ||
		a.ReplayWindowMS != b.ReplayWindowMS ||
		a.ReconnectCount != b.ReconnectCount ||
		!a.LastReconnectAt.Equal(b.LastReconnectAt) ||
		len(a.BoundRelays) != len(b.BoundRelays) {
		return false
	}
	for i := range a.BoundRelays {
		if a.BoundRelays[i] != b.BoundRelays[i] {
			return false
		}
	}
	return true
}

func runtimeObserveUnixMillis(ts time.Time) int64 {
	if ts.IsZero() {
		return 0
	}
	return ts.UnixMilli()
}

func observeRuntimeActivity(ctx context.Context, eventBuffer *runtimeEventBuffer, logBuffer *runtimeLogBuffer, req toolbuiltin.RuntimeObserveRequest) (map[string]any, error) {
	started := time.Now()
	timeout := time.Duration(req.WaitTimeoutMS) * time.Millisecond
	deadline := started.Add(timeout)

	for {
		out := map[string]any{}
		haveData := false
		if req.IncludeEvents {
			section := eventBuffer.Tail(req.EventCursor, req.EventLimit, req.MaxBytes, req.Filters)
			out["events"] = section
			haveData = haveData || runtimeObserveHasEventData(section)
		}
		if req.IncludeLogs {
			section := logBuffer.Tail(req.LogCursor, req.LogLimit, req.MaxBytes)
			out["logs"] = section
			haveData = haveData || runtimeObserveHasLogData(section)
		}
		out["waited_ms"] = time.Since(started).Milliseconds()
		if haveData || timeout <= 0 {
			out["timed_out"] = false
			return out, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			out["timed_out"] = true
			return out, nil
		}
		if err := waitForRuntimeObserveChange(ctx, eventBuffer, logBuffer, req, remaining); err != nil {
			return nil, err
		}
	}
}

func waitForRuntimeObserveChange(ctx context.Context, eventBuffer *runtimeEventBuffer, logBuffer *runtimeLogBuffer, req toolbuiltin.RuntimeObserveRequest, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		var (
			eventCh <-chan struct{}
			logCh   <-chan struct{}
		)
		if req.IncludeEvents {
			if changed, ch := eventBuffer.snapshotNotifier(req.EventCursor, req.Filters); changed {
				return nil
			} else {
				eventCh = ch
			}
		}
		if req.IncludeLogs {
			if changed, ch := logBuffer.snapshotNotifier(req.LogCursor); changed {
				return nil
			} else {
				logCh = ch
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-eventCh:
			continue
		case <-logCh:
			continue
		}
	}
}

func runtimeObserveHasEventData(section map[string]any) bool {
	if runtimeObserveHasSectionStateChange(section) {
		return true
	}
	if section == nil {
		return false
	}
	switch v := section["events"].(type) {
	case []map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	default:
		return false
	}
}

func runtimeObserveHasLogData(section map[string]any) bool {
	if runtimeObserveHasSectionStateChange(section) {
		return true
	}
	if section == nil {
		return false
	}
	switch v := section["lines"].(type) {
	case []string:
		return len(v) > 0
	case []any:
		return len(v) > 0
	default:
		return false
	}
}

func runtimeObserveHasSectionStateChange(section map[string]any) bool {
	if section == nil {
		return false
	}
	reset, _ := section["reset"].(bool)
	if reset {
		return true
	}
	truncated, _ := section["truncated"].(bool)
	return truncated
}

func runtimeObserveToolRequest(req methods.RuntimeObserveRequest) toolbuiltin.RuntimeObserveRequest {
	includeEvents := true
	if req.IncludeEvents != nil {
		includeEvents = *req.IncludeEvents
	}
	includeLogs := true
	if req.IncludeLogs != nil {
		includeLogs = *req.IncludeLogs
	}
	return toolbuiltin.RuntimeObserveRequest{
		IncludeEvents: includeEvents,
		IncludeLogs:   includeLogs,
		EventCursor:   req.EventCursor,
		LogCursor:     req.LogCursor,
		EventLimit:    req.EventLimit,
		LogLimit:      req.LogLimit,
		MaxBytes:      req.MaxBytes,
		WaitTimeoutMS: req.WaitTimeoutMS,
		Filters: toolbuiltin.RuntimeObserveFilters{
			Events:    append([]string(nil), req.Events...),
			AgentID:   req.AgentID,
			SessionID: req.SessionID,
			ChannelID: req.ChannelID,
			Direction: req.Direction,
			Subsystem: req.Subsystem,
			Source:    req.Source,
		},
	}
}
