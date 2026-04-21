package main

import (
	"log"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	gatewayws "metiq/internal/gateway/ws"
	mcppkg "metiq/internal/mcp"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// MCP lifecycle payloads
// ---------------------------------------------------------------------------

func buildMCPLifecyclePayload(change mcppkg.StateChange, ts int64) gatewayws.MCPLifecyclePayload {
	return buildMCPLifecyclePayloadForTelemetry(mcppkg.TelemetryServer{
		Name:              change.Server.Name,
		State:             string(change.Server.State),
		Healthy:           !change.Removed && change.Server.State == mcppkg.ConnectionStateConnected,
		Enabled:           change.Server.Enabled,
		RuntimePresent:    !change.Removed,
		Source:            change.Server.Source,
		Precedence:        change.Server.Precedence,
		Signature:         change.Server.Signature,
		Transport:         change.Server.Transport,
		Command:           change.Server.Command,
		URL:               change.Server.URL,
		Capabilities:      change.Server.Capabilities,
		ToolCount:         change.Server.ToolCount,
		LastError:         change.Server.LastError,
		ReconnectAttempts: change.Server.ReconnectAttempts,
		LastAttemptAtMS:   change.Server.LastAttemptAtMS,
		LastConnectedAtMS: change.Server.LastConnectedAtMS,
		LastFailedAtMS:    change.Server.LastFailedAtMS,
		UpdatedAtMS:       change.Server.UpdatedAtMS,
	}, string(change.PreviousState), change.Reason, change.Removed, ts)
}

func buildMCPLifecyclePayloadForTelemetry(server mcppkg.TelemetryServer, previousState, reason string, removed bool, ts int64) gatewayws.MCPLifecyclePayload {
	caps := map[string]bool{}
	if server.Capabilities.Tools {
		caps["tools"] = true
	}
	if server.Capabilities.Resources {
		caps["resources"] = true
	}
	if server.Capabilities.Prompts {
		caps["prompts"] = true
	}
	if server.Capabilities.Logging {
		caps["logging"] = true
	}
	if len(caps) == 0 {
		caps = nil
	}
	return gatewayws.MCPLifecyclePayload{
		TS:                ts,
		Name:              server.Name,
		State:             server.State,
		PreviousState:     previousState,
		Reason:            reason,
		Removed:           removed,
		Healthy:           server.Healthy,
		Enabled:           server.Enabled,
		RuntimePresent:    server.RuntimePresent,
		Source:            string(server.Source),
		Precedence:        server.Precedence,
		Signature:         server.Signature,
		Transport:         server.Transport,
		Command:           server.Command,
		URL:               server.URL,
		ToolCount:         server.ToolCount,
		LastError:         server.LastError,
		ReconnectAttempts: server.ReconnectAttempts,
		LastAttemptAtMS:   server.LastAttemptAtMS,
		LastConnectedAtMS: server.LastConnectedAtMS,
		LastFailedAtMS:    server.LastFailedAtMS,
		UpdatedAtMS:       server.UpdatedAtMS,
		PolicyStatus:      string(server.PolicyStatus),
		PolicyReason:      string(server.PolicyReason),
		Capabilities:      caps,
	}
}

// ---------------------------------------------------------------------------
// filteredMCPLifecycleTracker — deduplicating MCP lifecycle event emitter
// ---------------------------------------------------------------------------

type filteredMCPLifecycleTracker struct {
	mu   sync.Mutex
	last map[string]mcppkg.TelemetryServer
}

func newFilteredMCPLifecycleTracker() *filteredMCPLifecycleTracker {
	return &filteredMCPLifecycleTracker{last: map[string]mcppkg.TelemetryServer{}}
}

func (t *filteredMCPLifecycleTracker) Emit(emitter gatewayws.EventEmitter, resolved mcppkg.Config, reason string, ts int64) {
	if t == nil || emitter == nil {
		return
	}
	current := map[string]mcppkg.TelemetryServer{}
	for _, server := range mcppkg.BuildTelemetrySnapshot(resolved, mcppkg.ManagerSnapshot{}).Servers {
		if server.PolicyStatus == "" {
			continue
		}
		current[server.Name] = server
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for name, previous := range t.last {
		if _, ok := current[name]; ok {
			continue
		}
		if _, ok := resolved.Servers[name]; ok {
			continue
		}
		if _, ok := resolved.DisabledServers[name]; ok {
			disabled := previous
			disabled.State = string(mcppkg.ConnectionStateDisabled)
			disabled.Healthy = false
			disabled.RuntimePresent = false
			disabled.PolicyStatus = ""
			disabled.PolicyReason = ""
			emitter.Emit(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayloadForTelemetry(disabled, previous.State, reason+".disabled", false, ts))
			continue
		}
		emitter.Emit(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayloadForTelemetry(previous, previous.State, reason+".removed", true, ts))
	}
	for _, server := range current {
		emitter.Emit(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayloadForTelemetry(server, "", reason, false, ts))
	}
	t.last = current
}

// ---------------------------------------------------------------------------
// MCP telemetry snapshot
// ---------------------------------------------------------------------------

func currentMCPTelemetry(cfg state.ConfigDoc, mgr *mcppkg.Manager) mcppkg.TelemetrySnapshot {
	runtime := mcppkg.ManagerSnapshot{}
	if mgr != nil {
		runtime = mgr.Snapshot()
	}
	return mcppkg.BuildTelemetrySnapshot(mcppkg.ResolveConfigDoc(cfg), runtime)
}

// ---------------------------------------------------------------------------
// Turn telemetry
// ---------------------------------------------------------------------------

func buildTurnTelemetry(turnID string, startedAt, endedAt time.Time, result agent.TurnResult, turnErr error, fallbackUsed bool, fallbackFrom, fallbackTo, fallbackReason string) agent.TurnTelemetry {
	telemetry := agent.TurnTelemetry{
		TurnID:         strings.TrimSpace(turnID),
		StartedAtMS:    startedAt.UnixMilli(),
		EndedAtMS:      endedAt.UnixMilli(),
		DurationMS:     endedAt.Sub(startedAt).Milliseconds(),
		FallbackUsed:   fallbackUsed,
		FallbackFrom:   strings.TrimSpace(fallbackFrom),
		FallbackTo:     strings.TrimSpace(fallbackTo),
		FallbackReason: strings.TrimSpace(fallbackReason),
	}
	if meta, ok := agent.BuildTurnResultMetadata(result, turnErr); ok {
		telemetry.Outcome = meta.Outcome
		telemetry.StopReason = meta.StopReason
		telemetry.Usage = meta.Usage
	}
	if turnErr != nil {
		telemetry.Error = truncateRunes(strings.TrimSpace(turnErr.Error()), 200)
	}
	telemetry.LoopBlocked = telemetry.StopReason == agent.TurnStopReasonLoopBlocked
	return telemetry
}

func turnResultMetadataPtr(result agent.TurnResult, turnErr error) *agent.TurnResultMetadata {
	meta, ok := agent.BuildTurnResultMetadata(result, turnErr)
	if !ok {
		return nil
	}
	return &meta
}

func persistTurnTelemetry(sessionStore *state.SessionStore, sessionID string, telemetry agent.TurnTelemetry) {
	if sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if err := sessionStore.RecordTurn(sessionID, state.TurnTelemetry{
		TurnID:         telemetry.TurnID,
		StartedAtMS:    telemetry.StartedAtMS,
		EndedAtMS:      telemetry.EndedAtMS,
		DurationMS:     telemetry.DurationMS,
		Outcome:        string(telemetry.Outcome),
		StopReason:     string(telemetry.StopReason),
		LoopBlocked:    telemetry.LoopBlocked,
		Error:          telemetry.Error,
		FallbackUsed:   telemetry.FallbackUsed,
		FallbackFrom:   telemetry.FallbackFrom,
		FallbackTo:     telemetry.FallbackTo,
		FallbackReason: telemetry.FallbackReason,
		InputTokens:    telemetry.Usage.InputTokens,
		OutputTokens:   telemetry.Usage.OutputTokens,
	}); err != nil {
		log.Printf("session store turn telemetry failed session=%s: %v", sessionID, err)
	}
}

func emitTurnTelemetry(emitter gatewayws.EventEmitter, agentID, sessionID string, telemetry agent.TurnTelemetry) {
	if emitter == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	emitter.Emit(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, sessionID, telemetry))
}

func turnTelemetryPayload(agentID, sessionID string, telemetry agent.TurnTelemetry) gatewayws.TurnResultPayload {
	return gatewayws.TurnResultPayload{
		TS:             telemetry.EndedAtMS,
		AgentID:        agentID,
		SessionID:      sessionID,
		TurnID:         telemetry.TurnID,
		StartedAtMS:    telemetry.StartedAtMS,
		EndedAtMS:      telemetry.EndedAtMS,
		DurationMS:     telemetry.DurationMS,
		Outcome:        string(telemetry.Outcome),
		StopReason:     string(telemetry.StopReason),
		LoopBlocked:    telemetry.LoopBlocked,
		Error:          telemetry.Error,
		FallbackUsed:   telemetry.FallbackUsed,
		FallbackFrom:   telemetry.FallbackFrom,
		FallbackTo:     telemetry.FallbackTo,
		FallbackReason: telemetry.FallbackReason,
		InputTokens:    telemetry.Usage.InputTokens,
		OutputTokens:   telemetry.Usage.OutputTokens,
		GoalID:         telemetry.Trace.GoalID,
		TaskID:         telemetry.Trace.TaskID,
		RunID:          telemetry.Trace.RunID,
		StepID:         telemetry.Trace.StepID,
		ParentTaskID:   telemetry.Trace.ParentTaskID,
		ParentRunID:    telemetry.Trace.ParentRunID,
	}
}

// ---------------------------------------------------------------------------
// Tool lifecycle telemetry
// ---------------------------------------------------------------------------

func toolLifecycleEmitter(emitter gatewayws.EventEmitter, agentID string) agent.ToolLifecycleSink {
	if emitter == nil {
		return nil
	}
	return func(evt agent.ToolLifecycleEvent) {
		payload := gatewayws.ToolLifecyclePayload{
			TS:           evt.TS,
			AgentID:      agentID,
			SessionID:    evt.SessionID,
			TurnID:       evt.TurnID,
			ToolCallID:   evt.ToolCallID,
			ToolName:     evt.ToolName,
			Result:       evt.Result,
			Error:        evt.Error,
			Data:         projectToolLifecycleData(evt.Data),
			GoalID:       evt.Trace.GoalID,
			TaskID:       evt.Trace.TaskID,
			RunID:        evt.Trace.RunID,
			StepID:       evt.Trace.StepID,
			ParentTaskID: evt.Trace.ParentTaskID,
			ParentRunID:  evt.Trace.ParentRunID,
		}
		switch evt.Type {
		case agent.ToolLifecycleEventStart:
			emitter.Emit(gatewayws.EventToolStart, payload)
		case agent.ToolLifecycleEventProgress:
			emitter.Emit(gatewayws.EventToolProgress, payload)
		case agent.ToolLifecycleEventResult:
			emitter.Emit(gatewayws.EventToolResult, payload)
		case agent.ToolLifecycleEventError:
			emitter.Emit(gatewayws.EventToolError, payload)
		}
	}
}

func projectToolLifecycleData(data any) any {
	switch decision := data.(type) {
	case agent.ToolSchedulerDecision:
		return gatewayws.ToolSchedulerDecisionPayload{
			Kind:             gatewayws.ToolDecisionKind(decision.Kind),
			Mode:             decision.Mode,
			BatchIndex:       decision.BatchIndex,
			BatchCount:       decision.BatchCount,
			BatchSize:        decision.BatchSize,
			BatchPosition:    decision.BatchPosition,
			ConcurrencySafe:  decision.ConcurrencySafe,
			ConcurrencyLimit: decision.ConcurrencyLimit,
		}
	case agent.ToolLoopDecision:
		return gatewayws.ToolLoopDecisionPayload{
			Kind:           gatewayws.ToolDecisionKind(decision.Kind),
			Blocked:        decision.Blocked,
			Level:          decision.Level,
			Detector:       decision.Detector,
			Count:          decision.Count,
			WarningKey:     decision.WarningKey,
			PairedToolName: decision.PairedToolName,
			Message:        decision.Message,
		}
	default:
		return data
	}
}
