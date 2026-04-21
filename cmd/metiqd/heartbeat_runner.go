package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
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
			if hasImmediateHeartbeatWake(wakes) {
				r.runHeartbeatCycle(ctx)
				continue
			}
			waitFor, ok := heartbeatRunnerWaitDuration(status, r.now())
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
	wakes := r.ops.ConsumeHeartbeatWakes()
	trigger := heartbeatCycleTriggerSchedule
	if hasImmediateHeartbeatWake(wakes) {
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

func heartbeatRunnerWaitDuration(status heartbeatRunnerStatus, now time.Time) (time.Duration, bool) {
	if !status.Enabled || status.IntervalMS <= 0 {
		return 0, false
	}
	waitFor := time.Duration(status.IntervalMS) * time.Millisecond
	if status.LastRunMS > 0 {
		elapsed := now.Sub(time.UnixMilli(status.LastRunMS))
		waitFor -= elapsed
		if waitFor < 0 {
			waitFor = 0
		}
	}
	return waitFor, true
}

func hasImmediateHeartbeatWake(wakes []heartbeatWakeRecord) bool {
	for _, wake := range wakes {
		if strings.ToLower(strings.TrimSpace(wake.Mode)) != "next-heartbeat" {
			return true
		}
	}
	return false
}

func buildHeartbeatRunnerPrompt(agentID string, wakes []heartbeatWakeRecord) string {
	var b strings.Builder
	b.WriteString("Heartbeat runner check for agent \"")
	b.WriteString(strings.TrimSpace(agentID))
	b.WriteString("\".\n\n")
	b.WriteString("If queued wake events are listed below, treat them as the reason for this run. Inspect workspace/context as needed and take appropriate action. If nothing requires action, reply HEARTBEAT_OK.")
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
	if controlServices == nil {
		return heartbeatAgentRun{}, fmt.Errorf("daemon services not initialized")
	}
	return controlServices.buildHeartbeatAgentRun(cfg, agentID, wakes)
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
	return run, nil
}

func executeHeartbeatAgentRun(ctx context.Context, run heartbeatAgentRun) error {
	if controlServices == nil {
		return fmt.Errorf("daemon services not initialized")
	}
	return controlServices.executeHeartbeatAgentRun(ctx, run)
}

func (s *daemonServices) executeHeartbeatAgentRun(ctx context.Context, run heartbeatAgentRun) error {
	if s.session.sessionRouter != nil {
		s.session.sessionRouter.Assign(run.SessionID, run.AgentID)
	}
	result := runAgentTurnWithFallbacks(ctx, methods.AgentRequest{
		SessionID: run.SessionID,
		Message:   run.Prompt,
		TimeoutMS: run.TimeoutMS,
	}, run.Runtime, run.FallbackRuntimes, run.RuntimeLabels, s.session.memoryStore)
	return result.Err
}
