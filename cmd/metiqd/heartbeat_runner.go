package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/store/state"
)

type heartbeatAgentRun struct {
	AgentID          string
	SessionID        string
	Prompt           string
	PrimaryModel     string
	Runtime          agent.Runtime
	FallbackRuntimes []agent.Runtime
	RuntimeLabels    []string
	TimeoutMS        int
	Wakes            []heartbeatWakeRecord
}

type heartbeatRunner struct {
	ops       *operationsRegistry
	getConfig func() state.ConfigDoc
	now       func() time.Time
	runAgent  func(context.Context, heartbeatAgentRun) error
}

func newHeartbeatRunner(ops *operationsRegistry, getConfig func() state.ConfigDoc) *heartbeatRunner {
	return &heartbeatRunner{
		ops:       ops,
		getConfig: getConfig,
		now:       time.Now,
	}
}

func (r *heartbeatRunner) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if r == nil || r.ops == nil {
			return
		}
		status := r.ops.HeartbeatStatus()
		if status.Enabled && status.IntervalMS > 0 {
			log.Printf("heartbeat runner active interval=%s", time.Duration(status.IntervalMS)*time.Millisecond)
		} else {
			log.Printf("heartbeat runner waiting for manual wake or future schedule activation")
		}
		for {
			status, wakes, notify := r.ops.HeartbeatSnapshot()
			if ctx.Err() != nil {
				return
			}
			now := r.now()
			if hasImmediateHeartbeatWake(wakes, now) {
				r.runHeartbeatCycle(ctx)
				continue
			}
			waitFor, ok := heartbeatRunnerWaitDuration(status, wakes, now)
			if !ok {
				select {
				case <-ctx.Done():
					return
				case <-notify:
					continue
				}
			}
			timer := time.NewTimer(waitFor)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-notify:
				if !timer.Stop() {
					<-timer.C
				}
				continue
			case <-timer.C:
				r.runHeartbeatCycle(ctx)
			}
		}
	}()
	return done
}

func (r *heartbeatRunner) runHeartbeatCycle(ctx context.Context) {
	cfg := state.ConfigDoc{}
	if r.getConfig != nil {
		cfg = r.getConfig()
	}
	now := r.now()
	wakes := r.ops.ConsumeDueHeartbeatWakes(now.UnixMilli())
	trigger := heartbeatCycleTriggerSchedule
	if hasImmediateHeartbeatWake(wakes, now) {
		trigger = heartbeatCycleTriggerWake
	}
	if err := r.executeCycle(ctx, cfg, wakes, trigger); err != nil {
		log.Printf("heartbeat runner cycle error: %v", err)
	}
	r.ops.MarkHeartbeatRun(r.now().UnixMilli())
}

type heartbeatCycleTrigger string

const (
	heartbeatCycleTriggerSchedule heartbeatCycleTrigger = "schedule"
	heartbeatCycleTriggerWake     heartbeatCycleTrigger = "wake"
)

func (r *heartbeatRunner) executeCycle(ctx context.Context, cfg state.ConfigDoc, wakes []heartbeatWakeRecord, trigger heartbeatCycleTrigger) error {
	for _, agentID := range heartbeatCycleAgentIDs(cfg, wakes, trigger) {
		agentWakes := filterHeartbeatWakesForAgent(agentID, wakes)
		run, err := buildHeartbeatAgentRun(cfg, agentID, agentWakes)
		if err != nil {
			log.Printf("heartbeat runner: skip agent=%s err=%v", agentID, err)
			continue
		}
		if r.runAgent != nil {
			if err := r.runAgent(ctx, run); err != nil {
				log.Printf("heartbeat runner: agent=%s err=%v", agentID, err)
			}
			continue
		}
		if err := executeHeartbeatAgentRun(ctx, run); err != nil {
			log.Printf("heartbeat runner: agent=%s err=%v", agentID, err)
		}
	}
	return nil
}

func heartbeatCycleAgentIDs(cfg state.ConfigDoc, wakes []heartbeatWakeRecord, trigger heartbeatCycleTrigger) []string {
	if trigger != heartbeatCycleTriggerWake {
		return heartbeatRunnerAgentIDs(cfg)
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(wakes))
	for _, wake := range wakes {
		agentID := strings.TrimSpace(wake.AgentID)
		if agentID == "" {
			agentID = "main"
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		ids = append(ids, agentID)
	}
	if len(ids) == 0 {
		return []string{"main"}
	}
	return ids
}

func filterHeartbeatWakesForAgent(agentID string, wakes []heartbeatWakeRecord) []heartbeatWakeRecord {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "main"
	}
	out := make([]heartbeatWakeRecord, 0, len(wakes))
	for _, wake := range wakes {
		target := strings.TrimSpace(wake.AgentID)
		if target == "" {
			target = "main"
		}
		if target != agentID {
			continue
		}
		out = append(out, wake)
	}
	return out
}

func heartbeatRunnerAgentIDs(cfg state.ConfigDoc) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(cfg.Agents))
	for _, agCfg := range cfg.Agents {
		agentID := strings.TrimSpace(agCfg.ID)
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		ids = append(ids, agentID)
	}
	if len(ids) == 0 {
		return []string{"main"}
	}
	return ids
}

func heartbeatSessionKey(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "main"
	}
	return "heartbeat:" + agentID
}

func heartbeatRunnerWaitDuration(status heartbeatRunnerStatus, wakes []heartbeatWakeRecord, now time.Time) (time.Duration, bool) {
	var waitFor time.Duration
	ok := false
	if status.Enabled && status.IntervalMS > 0 {
		waitFor = time.Duration(status.IntervalMS) * time.Millisecond
		if status.LastRunMS > 0 {
			elapsed := now.Sub(time.UnixMilli(status.LastRunMS))
			waitFor -= elapsed
			if waitFor < 0 {
				waitFor = 0
			}
		}
		ok = true
	}
	for _, wake := range wakes {
		if !heartbeatWakeCanScheduleIndependently(wake) {
			continue
		}
		dueAt := time.UnixMilli(wake.AtMS)
		wakeWait := dueAt.Sub(now)
		if wakeWait < 0 {
			wakeWait = 0
		}
		if !ok || wakeWait < waitFor {
			waitFor = wakeWait
			ok = true
		}
	}
	return waitFor, ok
}

func hasImmediateHeartbeatWake(wakes []heartbeatWakeRecord, now time.Time) bool {
	for _, wake := range wakes {
		if !heartbeatWakeDue(wake, now) {
			continue
		}
		if strings.ToLower(strings.TrimSpace(wake.Mode)) != "next-heartbeat" {
			return true
		}
	}
	return false
}

func heartbeatWakeCanScheduleIndependently(wake heartbeatWakeRecord) bool {
	return strings.ToLower(strings.TrimSpace(wake.Mode)) != "next-heartbeat" && wake.AtMS > 0
}

func heartbeatWakeDue(wake heartbeatWakeRecord, now time.Time) bool {
	return wake.AtMS <= 0 || !time.UnixMilli(wake.AtMS).After(now)
}

func buildHeartbeatRunnerPrompt(agentID string, wakes []heartbeatWakeRecord) string {
	var b strings.Builder
	b.WriteString("Heartbeat runner check for agent \"")
	b.WriteString(strings.TrimSpace(agentID))
	b.WriteString("\".\n\n")
	b.WriteString("If queued wake events are listed below, treat them as the reason for this run. Inspect workspace/context as needed and take appropriate action. You MUST finish every heartbeat run by calling the heartbeat_respond tool exactly once. Use notify=false with outcome=no_change when nothing requires a visible alert; use notify=true and notification_text only when the user should be notified. Set next_check only when you need a follow-up heartbeat at a specific time or after a duration.")
	if len(wakes) == 0 {
		return b.String()
	}
	b.WriteString("\n\nQueued wake events:\n")
	for _, wake := range wakes {
		b.WriteString("- [")
		b.WriteString(time.UnixMilli(wake.AtMS).UTC().Format(time.RFC3339))
		b.WriteString("]")
		if source := strings.TrimSpace(wake.Source); source != "" {
			b.WriteString(" source=")
			b.WriteString(source)
		}
		if target := strings.TrimSpace(wake.AgentID); target != "" {
			b.WriteString(" agent=")
			b.WriteString(target)
		}
		if mode := strings.TrimSpace(wake.Mode); mode != "" {
			b.WriteString(" mode=")
			b.WriteString(mode)
		}
		if text := strings.TrimSpace(wake.Text); text != "" {
			b.WriteString(" text=")
			b.WriteString(text)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func heartbeatRunnerTimeoutMS(agCfg state.AgentConfig) int {
	const defaultTurnTimeoutSecs = 180
	secs := defaultTurnTimeoutSecs
	if agCfg.TurnTimeoutSecs != 0 {
		secs = agCfg.TurnTimeoutSecs
	}
	if secs <= 0 {
		secs = defaultTurnTimeoutSecs
	}
	return secs * 1000
}

func buildHeartbeatAgentRun(cfg state.ConfigDoc, agentID string, wakes []heartbeatWakeRecord) (heartbeatAgentRun, error) {
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{
			emitter:   controlWsEmitter,
			emitterMu: &controlWsEmitterMu,
			session: sessionServices{
				agentRuntime:  controlAgentRuntime,
				agentRegistry: controlAgentRegistry,
				toolRegistry:  controlToolRegistry,
				sessionRouter: controlSessionRouter,
			},
		}
	}
	return svc.buildHeartbeatAgentRun(cfg, agentID, wakes)
}

func (s *daemonServices) buildHeartbeatAgentRun(cfg state.ConfigDoc, agentID string, wakes []heartbeatWakeRecord) (heartbeatAgentRun, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "main"
	}
	run := heartbeatAgentRun{
		AgentID:   agentID,
		SessionID: heartbeatSessionKey(agentID),
		Prompt:    buildHeartbeatRunnerPrompt(agentID, wakes),
		Wakes:     append([]heartbeatWakeRecord(nil), wakes...),
	}
	if s.session.agentRegistry != nil {
		run.Runtime = s.session.agentRegistry.Get(agentID)
	}
	if run.Runtime == nil {
		run.Runtime = s.session.agentRuntime
	}
	run.PrimaryModel = strings.TrimSpace(cfg.Agent.DefaultModel)
	if agCfg, ok := resolveAgentConfigByID(cfg, agentID); ok {
		run.TimeoutMS = heartbeatRunnerTimeoutMS(agCfg)
		if heartbeatModel := resolveAuxiliaryModelForAgent(agCfg, auxiliaryModelUseCaseHeartbeat); heartbeatModel != "" {
			rt, err := buildRuntimeForAgentModel(cfg, agCfg, heartbeatModel, s.session.toolRegistry)
			if err != nil {
				return heartbeatAgentRun{}, err
			}
			run.Runtime = rt
			run.PrimaryModel = heartbeatModel
		} else if strings.TrimSpace(agCfg.Model) != "" {
			run.PrimaryModel = strings.TrimSpace(agCfg.Model)
			if run.Runtime == nil {
				rt, err := buildRuntimeForAgentModel(cfg, agCfg, agCfg.Model, s.session.toolRegistry)
				if err != nil {
					return heartbeatAgentRun{}, err
				}
				run.Runtime = rt
			}
		}
		labels := []string{run.PrimaryModel}
		fallbacks := make([]agent.Runtime, 0, len(agCfg.FallbackModels))
		seen := map[string]struct{}{}
		if run.PrimaryModel != "" {
			seen[run.PrimaryModel] = struct{}{}
		}
		for _, fbModel := range agCfg.FallbackModels {
			fbModel = strings.TrimSpace(fbModel)
			if fbModel == "" {
				continue
			}
			if _, ok := seen[fbModel]; ok {
				continue
			}
			fbRt, err := buildRuntimeForAgentModel(cfg, agCfg, fbModel, s.session.toolRegistry)
			if err != nil {
				log.Printf("heartbeat runner: skip fallback agent=%s model=%q err=%v", agentID, fbModel, err)
				continue
			}
			seen[fbModel] = struct{}{}
			fallbacks = append(fallbacks, fbRt)
			labels = append(labels, fbModel)
		}
		run.FallbackRuntimes = fallbacks
		run.RuntimeLabels = labels
	} else {
		run.TimeoutMS = heartbeatRunnerTimeoutMS(state.AgentConfig{})
		if run.PrimaryModel == "" {
			run.PrimaryModel = "primary"
		}
		run.RuntimeLabels = []string{run.PrimaryModel}
	}
	if run.Runtime == nil {
		return heartbeatAgentRun{}, fmt.Errorf("heartbeat runtime not configured for agent %s", agentID)
	}
	run.Runtime = wrapHeartbeatRuntime(run.Runtime, s.session.toolRegistry)
	for i, rt := range run.FallbackRuntimes {
		run.FallbackRuntimes[i] = wrapHeartbeatRuntime(rt, s.session.toolRegistry)
	}
	return run, nil
}

func executeHeartbeatAgentRun(ctx context.Context, run heartbeatAgentRun) error {
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{
			emitter:   controlWsEmitter,
			emitterMu: &controlWsEmitterMu,
			session: sessionServices{
				sessionRouter: controlSessionRouter,
				agentJobs:     controlAgentJobs,
			},
		}
	}
	return svc.executeHeartbeatAgentRun(ctx, run)
}

func (s *daemonServices) executeHeartbeatAgentRun(ctx context.Context, run heartbeatAgentRun) error {
	if s.session.sessionRouter != nil {
		s.session.sessionRouter.Assign(run.SessionID, run.AgentID)
	}
	runtime := wrapHeartbeatRuntime(run.Runtime, s.session.toolRegistry)
	fallbacks := append([]agent.Runtime(nil), run.FallbackRuntimes...)
	for i, rt := range fallbacks {
		fallbacks[i] = wrapHeartbeatRuntime(rt, s.session.toolRegistry)
	}
	controller := agentRunController{
		runtimeConfig:  s.runtimeConfig,
		sessionStore:   s.session.sessionStore,
		sessionRouter:  s.session.sessionRouter,
		agentRegistry:  s.session.agentRegistry,
		defaultRuntime: s.session.agentRuntime,
		jobs:           s.session.agentJobs,
		subagents:      s.session.subagents,
		emitEvent:      s.emitWSEvent,
		daemonCtx:      s.lifecycleCtx,
		runWG:          s.agentRunWG,
		runMu:          s.agentRunMu,
		runClosed:      s.agentRunClosed,
	}
	result := controller.runAgentTurnWithFallbacks(ctx, methods.AgentRequest{
		SessionID: run.SessionID,
		Message:   run.Prompt,
		TimeoutMS: run.TimeoutMS,
	}, runtime, fallbacks, run.RuntimeLabels, s.session.memoryStore)
	if result.Err != nil {
		return result.Err
	}
	if result.Result == nil {
		return fmt.Errorf("heartbeat run returned no result")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.consumeHeartbeatRunResult(run, *result.Result)
}

type heartbeatRuntimeWrapper struct {
	base  agent.Runtime
	tools agent.ToolExecutor
}

func wrapHeartbeatRuntime(base agent.Runtime, tools *agent.ToolRegistry) agent.Runtime {
	if base == nil {
		return nil
	}
	if _, ok := base.(*heartbeatRuntimeWrapper); ok {
		return base
	}
	var baseTools agent.ToolExecutor
	if tools != nil {
		baseTools = tools
	}
	return &heartbeatRuntimeWrapper{base: base, tools: heartbeatToolExecutor{base: baseTools}}
}

func (r *heartbeatRuntimeWrapper) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	turn.Tools = mergeHeartbeatToolDefinition(turn.Tools, agent.ToolDefinitions(r.tools))
	if turn.Executor != nil {
		turn.Executor = heartbeatToolExecutor{base: turn.Executor}
	} else {
		turn.Executor = r.tools
	}
	return r.base.ProcessTurn(ctx, turn)
}

type heartbeatToolExecutor struct {
	base agent.ToolExecutor
}

func (e heartbeatToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (string, error) {
	if call.Name == agent.HeartbeatResponseToolName {
		response, err := agent.ParseHeartbeatResponse(call.Args)
		if err != nil {
			return "", err
		}
		payload, err := response.ToJSON()
		if err != nil {
			return "", err
		}
		return string(payload), nil
	}
	if e.base == nil {
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
	return e.base.Execute(ctx, call)
}

func (e heartbeatToolExecutor) Definitions() []agent.ToolDefinition {
	return mergeHeartbeatToolDefinition(nil, agent.ToolDefinitions(e.base))
}

func (e heartbeatToolExecutor) SnapshotToolExecutor() agent.ToolExecutor {
	return heartbeatToolExecutor{base: agent.SnapshotToolExecutor(e.base)}
}

func (e heartbeatToolExecutor) EffectiveTraits(call agent.ToolCall) (agent.ToolTraits, bool) {
	if call.Name == agent.HeartbeatResponseToolName {
		return agent.ToolTraits{ConcurrencySafe: true, ReadOnly: true}, true
	}
	provider, ok := e.base.(interface {
		EffectiveTraits(agent.ToolCall) (agent.ToolTraits, bool)
	})
	if !ok {
		return agent.ToolTraits{}, false
	}
	return provider.EffectiveTraits(call)
}

func mergeHeartbeatToolDefinition(existing []agent.ToolDefinition, base []agent.ToolDefinition) []agent.ToolDefinition {
	defs := append([]agent.ToolDefinition(nil), existing...)
	seen := map[string]struct{}{}
	for _, def := range defs {
		seen[def.Name] = struct{}{}
	}
	for _, def := range base {
		if _, ok := seen[def.Name]; ok {
			continue
		}
		seen[def.Name] = struct{}{}
		defs = append(defs, def)
	}
	if _, ok := seen[agent.HeartbeatResponseToolName]; !ok {
		defs = append(defs, agent.HeartbeatResponseToolDefinition())
	}
	return defs
}

func (s *daemonServices) consumeHeartbeatRunResult(run heartbeatAgentRun, result agent.TurnResult) error {
	response, err := extractHeartbeatResponseFromTurnResult(result)
	if err != nil {
		return err
	}
	payload := agent.CreateHeartbeatResponsePayload(response)
	if s.session.ops != nil && strings.TrimSpace(response.NextCheck) != "" {
		now := time.Now()
		next, err := heartbeatNextCheckTime(response, now)
		if err != nil {
			return fmt.Errorf("invalid heartbeat next_check: %w", err)
		}
		s.session.ops.QueueHeartbeatWakeAt(run.AgentID, "heartbeat_respond", response.Summary, "scheduled", next.UnixMilli())
	}
	if s != nil {
		s.emitWSEvent(gatewayws.EventCompatHeartbeat, map[string]any{
			"ts_ms":        time.Now().UnixMilli(),
			"agent_id":     run.AgentID,
			"session_id":   run.SessionID,
			"outcome":      string(response.Outcome),
			"notify":       response.Notify,
			"priority":     string(response.Priority),
			"summary":      response.Summary,
			"reason":       response.Reason,
			"next_check":   response.NextCheck,
			"text":         payload.Text,
			"channel_data": payload.ChannelData,
		})
	}
	return nil
}

func extractHeartbeatResponseFromTurnResult(result agent.TurnResult) (*agent.HeartbeatResponse, error) {
	heartbeatTraces := make([]agent.ToolTrace, 0, 1)
	for _, trace := range result.ToolTraces {
		if trace.Call.Name == agent.HeartbeatResponseToolName {
			heartbeatTraces = append(heartbeatTraces, trace)
		}
	}
	if len(heartbeatTraces) > 0 {
		if len(heartbeatTraces) != 1 {
			return nil, fmt.Errorf("heartbeat run must call %s exactly once, got %d", agent.HeartbeatResponseToolName, len(heartbeatTraces))
		}
		trace := heartbeatTraces[0]
		if trace.Error != "" {
			return nil, fmt.Errorf("heartbeat_respond failed: %s", trace.Error)
		}
		return agent.ParseHeartbeatResponse(trace.Call.Args)
	}

	heartbeatCalls := make([]agent.ToolCallRef, 0, 1)
	for _, msg := range result.HistoryDelta {
		if msg.Role != "assistant" {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.Name == agent.HeartbeatResponseToolName {
				heartbeatCalls = append(heartbeatCalls, call)
			}
		}
	}
	if len(heartbeatCalls) == 0 {
		return nil, fmt.Errorf("heartbeat run did not call %s", agent.HeartbeatResponseToolName)
	}
	if len(heartbeatCalls) != 1 {
		return nil, fmt.Errorf("heartbeat run must call %s exactly once, got %d", agent.HeartbeatResponseToolName, len(heartbeatCalls))
	}
	call := heartbeatCalls[0]
	if strings.TrimSpace(call.ID) == "" {
		return nil, fmt.Errorf("heartbeat_respond tool call missing id")
	}
	matchingResults := make([]agent.ConversationMessage, 0, 1)
	for _, msg := range result.HistoryDelta {
		if msg.Role == "tool" && msg.ToolCallID == call.ID {
			matchingResults = append(matchingResults, msg)
		}
	}
	if len(matchingResults) != 1 {
		return nil, fmt.Errorf("heartbeat_respond must have exactly one tool result, got %d", len(matchingResults))
	}
	content := strings.TrimSpace(matchingResults[0].Content)
	if strings.HasPrefix(strings.ToLower(content), "error:") {
		return nil, fmt.Errorf("heartbeat_respond failed: %s", strings.TrimSpace(content[len("error:"):]))
	}
	if content != "" {
		if response, err := agent.ParseHeartbeatResponse(content); err == nil {
			return response, nil
		}
	}
	args, err := heartbeatToolCallArgs(call)
	if err != nil {
		return nil, err
	}
	return agent.ParseHeartbeatResponse(args)
}

func heartbeatToolCallArgs(call agent.ToolCallRef) (map[string]any, error) {
	if strings.TrimSpace(call.ArgsJSON) == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
		return nil, fmt.Errorf("parse heartbeat_respond args: %w", err)
	}
	return args, nil
}

func heartbeatNextCheckTime(response *agent.HeartbeatResponse, now time.Time) (time.Time, error) {
	if response == nil || strings.TrimSpace(response.NextCheck) == "" {
		return time.Time{}, fmt.Errorf("no next_check specified")
	}
	if d, err := response.GetNextCheckDuration(); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("next_check duration must be positive")
		}
		return now.Add(d), nil
	}
	if t, err := response.GetNextCheckTime(); err == nil {
		if !t.After(now) {
			return time.Time{}, fmt.Errorf("next_check time must be in the future")
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unsupported next_check format: %s", response.NextCheck)
}
