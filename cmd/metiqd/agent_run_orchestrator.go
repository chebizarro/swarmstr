package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/memory"
	"metiq/internal/store/state"
	cfgTimeouts "metiq/internal/timeouts"
)

type agentRunController struct {
	runtimeConfig  *runtimeConfigStore
	sessionStore   *state.SessionStore
	sessionRouter  *agent.AgentSessionRouter
	agentRegistry  *agent.AgentRuntimeRegistry
	defaultRuntime agent.Runtime
	jobs           *agentJobRegistry
	subagents      *SubagentRegistry
	emitEvent      func(string, any)
	daemonCtx      context.Context
	runWG          *sync.WaitGroup
	runMu          *sync.Mutex
	runClosed      *bool
}

func currentAgentRunController() agentRunController {
	if controlServices != nil {
		return agentRunController{
			runtimeConfig:  controlServices.runtimeConfig,
			sessionStore:   controlServices.session.sessionStore,
			sessionRouter:  controlServices.session.sessionRouter,
			agentRegistry:  controlServices.session.agentRegistry,
			defaultRuntime: controlServices.session.agentRuntime,
			jobs:           controlServices.session.agentJobs,
			subagents:      controlServices.session.subagents,
			emitEvent:      controlServices.emitWSEvent,
			daemonCtx:      controlServices.lifecycleCtx,
			runWG:          controlServices.agentRunWG,
			runMu:          controlServices.agentRunMu,
			runClosed:      controlServices.agentRunClosed,
		}
	}
	// Fallback: use package-level globals (test compatibility).
	return agentRunController{
		runtimeConfig:  controlRuntimeConfig,
		sessionStore:   controlSessionStore,
		sessionRouter:  controlSessionRouter,
		agentRegistry:  controlAgentRegistry,
		defaultRuntime: controlAgentRuntime,
		jobs:           controlAgentJobs,
		subagents:      controlSubagents,
	}
}

func (c agentRunController) runContext() context.Context {
	if c.daemonCtx != nil {
		return c.daemonCtx
	}
	return context.Background()
}

func (c agentRunController) emit(event string, payload any) {
	if c.emitEvent != nil {
		c.emitEvent(event, payload)
		return
	}
	emitControlWSEvent(event, payload)
}

func resolveInboundChannelRuntime(configuredAgentID, sessionID string) (string, agent.Runtime) {
	return currentAgentRunController().resolveInboundChannelRuntime(configuredAgentID, sessionID)
}

func (c agentRunController) resolveInboundChannelRuntime(configuredAgentID, sessionID string) (string, agent.Runtime) {
	agentID := strings.TrimSpace(configuredAgentID)
	if agentID == "" && c.sessionRouter != nil {
		agentID = strings.TrimSpace(c.sessionRouter.Get(sessionID))
	}
	if agentID == "" {
		agentID = "main"
	}
	if c.agentRegistry != nil {
		if rt := c.agentRegistry.Get(agentID); rt != nil {
			return agentID, rt
		}
	}
	return agentID, c.defaultRuntime
}

func applySessionsSpawn(ctx context.Context, req methods.SessionsSpawnRequest, cfg state.ConfigDoc, docsRepo *state.DocsRepository, memoryIndex memory.Store) (map[string]any, error) {
	return currentAgentRunController().applySessionsSpawn(ctx, req, cfg, docsRepo, memoryIndex)
}

func (c agentRunController) applySessionsSpawn(ctx context.Context, req methods.SessionsSpawnRequest, cfg state.ConfigDoc, docsRepo *state.DocsRepository, memoryIndex memory.Store) (map[string]any, error) {
	if c.defaultRuntime == nil || c.jobs == nil {
		return nil, fmt.Errorf("agent runtime not configured")
	}
	if c.subagents == nil {
		return nil, fmt.Errorf("subagent registry not initialised")
	}
	if removed := c.subagents.CleanupStale(time.Now()); removed > 0 {
		log.Printf("subagent registry stale cleanup removed=%d", removed)
	}

	parentDepth := 0
	if req.ParentSessionID != "" {
		parentDepth = c.subagents.DepthOf(req.ParentSessionID)
	}
	childDepth := parentDepth + 1
	if childDepth > maxSubagentDepth {
		return nil, fmt.Errorf("subagent depth limit %d exceeded", maxSubagentDepth)
	}

	runID := fmt.Sprintf("spawn-%d", time.Now().UnixNano())
	sessionID := fmt.Sprintf("spawn-sess-%d", time.Now().UnixNano())

	rec, ok := c.subagents.Spawn(runID, sessionID, req.ParentSessionID, childDepth, req.Message)
	if !ok {
		return nil, fmt.Errorf("subagent depth limit %d exceeded", maxSubagentDepth)
	}

	var rt agent.Runtime
	if c.agentRegistry != nil && req.AgentID != "" {
		rt = c.agentRegistry.Get(req.AgentID)
	}
	if rt == nil {
		rt = c.defaultRuntime
	}
	if rt == nil {
		return nil, fmt.Errorf("agent runtime not configured")
	}

	rt = applyAgentProfileFilter(ctx, rt, sessionID, cfg, docsRepo)

	resolvedAgentID := defaultAgentID(req.AgentID)
	agentReq := methods.AgentRequest{
		SessionID:   sessionID,
		Message:     req.Message,
		Context:     req.Context,
		MemoryScope: req.MemoryScope,
		TimeoutMS:   req.TimeoutMS,
	}
	persistSessionMemoryScope(c.sessionStore, sessionID, resolvedAgentID, req.MemoryScope)
	if c.sessionStore != nil {
		se := c.sessionStore.GetOrNew(sessionID)
		se.AgentID = resolvedAgentID
		se.SpawnedBy = "sessions.spawn"
		if parentEntry, ok := c.sessionStore.Get(req.ParentSessionID); ok {
			if strings.TrimSpace(parentEntry.ActiveTaskID) != "" {
				se.ParentTaskID = strings.TrimSpace(parentEntry.ActiveTaskID)
				se.ParentRunID = strings.TrimSpace(parentEntry.ActiveRunID)
			}
		}
		if putErr := c.sessionStore.Put(sessionID, se); putErr != nil {
			log.Printf("session store put failed session=%s: %v", sessionID, putErr)
		}
	}

	jobs := c.jobs
	subagents := c.subagents
	snapshot := jobs.Begin(runID, sessionID)
	if err := c.launchManagedRun(runID, agentReq, rt, nil, nil, memoryIndex, jobs, func(final agentJobSnapshot, found bool) {
		if found && subagents != nil {
			subagents.Finish(runID, final.Result, final.Err)
		}
	}); err != nil {
		return nil, err
	}

	return methods.ApplyCompatResponseAliases(map[string]any{
		"run_id":            runID,
		"session_id":        sessionID,
		"parent_session_id": rec.ParentSessionID,
		"depth":             rec.Depth,
		"status":            "accepted",
		"accepted_at":       snapshot.StartedAt,
	}), nil
}

type agentRunFinalizer func(final agentJobSnapshot, found bool)

func (c agentRunController) launchManagedRun(runID string, req methods.AgentRequest, primary agent.Runtime, fallbacks []agent.Runtime, runtimeLabels []string, memoryIndex memory.Store, jobs *agentJobRegistry, finalizer agentRunFinalizer) error {
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("agent run id required")
	}
	if primary == nil {
		return fmt.Errorf("agent runtime not configured")
	}
	if jobs == nil {
		jobs = c.jobs
	}
	if jobs == nil {
		return fmt.Errorf("agent job registry not configured")
	}
	baseCtx := c.runContext()
	if c.runMu != nil {
		c.runMu.Lock()
	}
	var launchErr error
	if c.runClosed != nil && *c.runClosed {
		launchErr = context.Canceled
	}
	if launchErr == nil {
		launchErr = baseCtx.Err()
	}
	if launchErr != nil {
		if c.runMu != nil {
			c.runMu.Unlock()
		}
		jobs.Finish(runID, "", launchErr)
		if final, found := jobs.Get(runID); finalizer != nil {
			finalizer(final, found)
		}
		return launchErr
	}
	tracked := c.runWG != nil
	if c.runWG != nil {
		c.runWG.Add(1)
	}
	if c.runMu != nil {
		c.runMu.Unlock()
	}
	go func(ctrl agentRunController) {
		if tracked {
			defer ctrl.runWG.Done()
		}
		ctrl.executeAgentRunWithFallbacks(baseCtx, runID, req, primary, fallbacks, runtimeLabels, memoryIndex, jobs)
		if final, found := jobs.Get(runID); found {
			if finalizer != nil {
				finalizer(final, found)
			}
		} else if finalizer != nil {
			finalizer(agentJobSnapshot{}, false)
		}
	}(c)
	return nil
}

func isRetryableAgentError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "context length") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "model_not_found")
}

func executeAgentRun(runID string, req methods.AgentRequest, runtime agent.Runtime, memoryIndex memory.Store, jobs *agentJobRegistry) {
	currentAgentRunController().executeAgentRun(runID, req, runtime, memoryIndex, jobs)
}

func (c agentRunController) executeAgentRun(runID string, req methods.AgentRequest, runtime agent.Runtime, memoryIndex memory.Store, jobs *agentJobRegistry) {
	c.executeAgentRunWithFallbacks(c.runContext(), runID, req, runtime, nil, nil, memoryIndex, jobs)
}

type agentRunAttemptResult struct {
	Result         *agent.TurnResult
	Err            error
	FallbackUsed   bool
	FallbackFrom   string
	FallbackTo     string
	FallbackReason string
}

func runAgentTurnWithFallbacks(baseCtx context.Context, req methods.AgentRequest, primary agent.Runtime, fallbacks []agent.Runtime, runtimeLabels []string, memoryIndex memory.Store) agentRunAttemptResult {
	return currentAgentRunController().runAgentTurnWithFallbacks(baseCtx, req, primary, fallbacks, runtimeLabels, memoryIndex)
}

func (c agentRunController) runAgentTurnWithFallbacks(baseCtx context.Context, req methods.AgentRequest, primary agent.Runtime, fallbacks []agent.Runtime, runtimeLabels []string, memoryIndex memory.Store) agentRunAttemptResult {
	if primary == nil {
		return agentRunAttemptResult{Err: fmt.Errorf("agent runtime not configured")}
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = cfgTimeouts.SubagentDefault(c.runtimeConfig.Get().Timeouts)
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()

	agentID := ""
	if c.sessionRouter != nil {
		agentID = c.sessionRouter.Get(req.SessionID)
	}
	if agentID == "" {
		agentID = "main"
	}

	c.emit(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
		TS:      time.Now().UnixMilli(),
		AgentID: agentID,
		Status:  "thinking",
		Session: req.SessionID,
	})
	defer func() {
		c.emit(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
			TS:      time.Now().UnixMilli(),
			AgentID: agentID,
			Status:  "idle",
			Session: req.SessionID,
		})
	}()

	runtimesToTry := append([]agent.Runtime{primary}, fallbacks...)
	cfg := state.ConfigDoc{}
	if c.runtimeConfig != nil {
		cfg = c.runtimeConfig.Get()
	}
	scopeCtx := resolveMemoryScopeContext(ctx, cfg, nil, c.sessionStore, req.SessionID, agentID, req.MemoryScope)
	persistSessionMemoryScope(c.sessionStore, req.SessionID, agentID, scopeCtx.Scope)
	ctx = contextWithMemoryScope(ctx, scopeCtx)
	prepared := buildAgentRunTurn(ctx, req, memoryIndex, scopeCtx, workspaceDirForAgent(cfg, agentID), c.sessionStore)
	prepared = applyPromptEnvelopeToPreparedTurn(prepared, turnPromptBuilderParams{Config: cfg, SessionID: req.SessionID, AgentID: agentID, Channel: "nostr", StaticSystemPrompt: prepared.Turn.StaticSystemPrompt, Context: prepared.Turn.Context, Tools: prepared.Turn.Tools})
	turn := prepared.Turn
	var result *agent.TurnResult
	var lastErr error
	attempt := agentRunAttemptResult{}
	turnStartedAt := time.Now()
	for i, rt := range runtimesToTry {
		if rt == nil {
			continue
		}
		var r agent.TurnResult
		r, lastErr = rt.ProcessTurn(ctx, turn)
		if lastErr == nil {
			if i > 0 {
				attempt.FallbackUsed = true
				attempt.FallbackFrom = runtimeLabelAt(runtimeLabels, i-1)
				attempt.FallbackTo = runtimeLabelAt(runtimeLabels, i)
			}
			result = &r

			// ── Commitment Guard: detect unbacked promises ──────────────────
			// Build commitment state from tool traces and apply the guard to
			// warn users when the agent makes promises without concrete actions.
			commitState := agent.BuildCommitmentStateFromTraces(result.ToolTraces)
			if guardedText, modified := agent.ApplyCommitmentGuard(result.Text, commitState); modified {
				result.Text = guardedText
			}
			break
		}
		if i < len(runtimesToTry)-1 && isRetryableAgentError(lastErr) {
			log.Printf("executeAgentRun session=%s fallback attempt %d/%d err=%v", req.SessionID, i+1, len(runtimesToTry)-1, lastErr)
			if attempt.FallbackReason == "" {
				attempt.FallbackReason = strings.TrimSpace(lastErr.Error())
			}
			continue
		}
		break
	}

	if lastErr != nil {
		turnTelemetry := buildTurnTelemetry("", turnStartedAt, time.Now(), agent.TurnResult{}, lastErr, attempt.FallbackUsed, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
		persistTurnTelemetry(c.sessionStore, req.SessionID, turnTelemetry)
		c.emit(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, req.SessionID, turnTelemetry))
		attempt.Err = lastErr
		return attempt
	}
	if result == nil {
		err := fmt.Errorf("all runtimes returned nil result")
		turnTelemetry := buildTurnTelemetry("", turnStartedAt, time.Now(), agent.TurnResult{}, err, attempt.FallbackUsed, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
		persistTurnTelemetry(c.sessionStore, req.SessionID, turnTelemetry)
		c.emit(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, req.SessionID, turnTelemetry))
		attempt.Err = err
		return attempt
	}
	commitMemoryRecallArtifacts(c.sessionStore, req.SessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
	if c.sessionStore != nil {
		se := c.sessionStore.GetOrNew(req.SessionID)
		if attempt.FallbackUsed {
			se.FallbackFrom = attempt.FallbackFrom
			se.FallbackTo = attempt.FallbackTo
			se.FallbackReason = truncateRunes(attempt.FallbackReason, 200)
			se.FallbackAt = time.Now().UnixMilli()
		} else {
			se.FallbackFrom = ""
			se.FallbackTo = ""
			se.FallbackReason = ""
			se.FallbackAt = 0
		}
		if putErr := c.sessionStore.Put(req.SessionID, se); putErr != nil {
			log.Printf("session store put failed session=%s: %v", req.SessionID, putErr)
		}
	}
	turnTelemetry := buildTurnTelemetry("", turnStartedAt, time.Now(), *result, nil, attempt.FallbackUsed, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
	persistTurnTelemetry(c.sessionStore, req.SessionID, turnTelemetry)
	c.emit(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, req.SessionID, turnTelemetry))
	attempt.Result = result
	return attempt
}

func executeAgentRunWithFallbacks(baseCtx context.Context, runID string, req methods.AgentRequest, primary agent.Runtime, fallbacks []agent.Runtime, runtimeLabels []string, memoryIndex memory.Store, jobs *agentJobRegistry) {
	currentAgentRunController().executeAgentRunWithFallbacks(baseCtx, runID, req, primary, fallbacks, runtimeLabels, memoryIndex, jobs)
}

func (c agentRunController) executeAgentRunWithFallbacks(baseCtx context.Context, runID string, req methods.AgentRequest, primary agent.Runtime, fallbacks []agent.Runtime, runtimeLabels []string, memoryIndex memory.Store, jobs *agentJobRegistry) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in executeAgentRun runID=%s panic=%v", runID, r)
			if jobs != nil {
				jobs.Finish(runID, "", fmt.Errorf("agent runtime panic: %v", r))
			}
		}
	}()

	if jobs == nil {
		jobs = c.jobs
	}
	if jobs == nil {
		return
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	attempt := c.runAgentTurnWithFallbacks(baseCtx, req, primary, fallbacks, runtimeLabels, memoryIndex)
	if attempt.FallbackUsed {
		jobs.SetFallback(runID, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
	}
	if attempt.Err != nil {
		jobs.Finish(runID, "", attempt.Err)
		return
	}
	if attempt.Result == nil {
		jobs.Finish(runID, "", fmt.Errorf("all runtimes returned nil result"))
		return
	}
	jobs.Finish(runID, attempt.Result.Text, nil)
}

func runtimeLabelAt(labels []string, idx int) string {
	if idx < 0 || idx >= len(labels) {
		if idx == 0 {
			return "primary"
		}
		return fmt.Sprintf("fallback-%d", idx)
	}
	if strings.TrimSpace(labels[idx]) == "" {
		if idx == 0 {
			return "primary"
		}
		return fmt.Sprintf("fallback-%d", idx)
	}
	return strings.TrimSpace(labels[idx])
}
