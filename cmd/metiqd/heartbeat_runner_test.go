package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/store/state"
)

type stubHeartbeatRuntime struct{}

func (stubHeartbeatRuntime) ProcessTurn(context.Context, agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{Text: "ok"}, nil
}

func withHeartbeatTestGlobals(t *testing.T, rt agent.Runtime) {
	t.Helper()
	prevRuntime := controlAgentRuntime
	prevRegistry := controlAgentRegistry
	prevToolRegistry := controlToolRegistry
	controlServicesMu.Lock()
	prevServices := controlServices
	controlAgentRuntime = rt
	reg := agent.NewAgentRuntimeRegistry(rt)
	controlAgentRegistry = reg
	controlToolRegistry = nil
	controlServices = &daemonServices{
		session: sessionServices{
			agentRuntime:  rt,
			agentRegistry: reg,
		},
	}
	controlServicesMu.Unlock()
	t.Cleanup(func() {
		controlServicesMu.Lock()
		controlAgentRuntime = prevRuntime
		controlAgentRegistry = prevRegistry
		controlToolRegistry = prevToolRegistry
		controlServices = prevServices
		controlServicesMu.Unlock()
	})
}

func waitForHeartbeatRun(t *testing.T, ops *operationsRegistry) heartbeatRunnerStatus {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		status := ops.HeartbeatStatus()
		if status.LastRunMS > 0 {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for heartbeat run")
	return heartbeatRunnerStatus{}
}

func TestHeartbeatRunnerRunsManualWakeWhenPeriodicDisabled(t *testing.T) {
	withHeartbeatTestGlobals(t, stubHeartbeatRuntime{})
	ops := newOperationsRegistry()
	ops.QueueHeartbeatWake("main", "control", "check now", "now")

	runner := newHeartbeatRunner(ops, func() state.ConfigDoc {
		return state.ConfigDoc{Agent: state.AgentPolicy{DefaultModel: "gpt-4o"}}
	})
	runs := make(chan heartbeatAgentRun, 1)
	runner.runAgent = func(ctx context.Context, run heartbeatAgentRun) error {
		runs <- run
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runner.Start(ctx)
	defer func() {
		cancel()
		<-done
	}()

	select {
	case run := <-runs:
		if run.SessionID != "heartbeat:main" {
			t.Fatalf("expected heartbeat:main session, got %q", run.SessionID)
		}
		if run.PrimaryModel != "gpt-4o" {
			t.Fatalf("expected default model gpt-4o, got %q", run.PrimaryModel)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected manual wake to trigger heartbeat run")
	}

	status := waitForHeartbeatRun(t, ops)
	if status.Enabled {
		t.Fatalf("expected periodic heartbeat to remain disabled, got %#v", status)
	}
	if status.PendingWakes != 0 {
		t.Fatalf("expected wake queue to drain, got %#v", status)
	}
	if status.LastWakeMS == 0 {
		t.Fatalf("expected last wake timestamp, got %#v", status)
	}
}

func TestHeartbeatRunnerNextHeartbeatWakeWaitsForSchedule(t *testing.T) {
	withHeartbeatTestGlobals(t, stubHeartbeatRuntime{})
	ops := newOperationsRegistry()
	ops.SyncHeartbeatConfig(state.HeartbeatConfig{Enabled: true, IntervalMS: 80})
	ops.QueueHeartbeatWake("main", "control", "defer", "next-heartbeat")

	runner := newHeartbeatRunner(ops, func() state.ConfigDoc {
		return state.ConfigDoc{Agent: state.AgentPolicy{DefaultModel: "gpt-4o"}}
	})
	runs := make(chan heartbeatAgentRun, 2)
	runner.runAgent = func(ctx context.Context, run heartbeatAgentRun) error {
		runs <- run
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runner.Start(ctx)
	defer func() {
		cancel()
		<-done
	}()

	select {
	case <-runs:
		t.Fatal("next-heartbeat wake should not run immediately")
	case <-time.After(25 * time.Millisecond):
	}

	select {
	case <-runs:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected next-heartbeat wake to run on scheduled interval")
	}

	status := waitForHeartbeatRun(t, ops)
	if !status.Enabled || status.IntervalMS != 80 {
		t.Fatalf("unexpected heartbeat status after scheduled run: %#v", status)
	}
}

func TestBuildHeartbeatAgentRunUsesHeartbeatModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	withHeartbeatTestGlobals(t, stubHeartbeatRuntime{})
	cfg := state.ConfigDoc{
		Agent: state.AgentPolicy{DefaultModel: "gpt-4o"},
		Agents: state.AgentsConfig{{
			ID:    "main",
			Model: "gpt-4o",
			Heartbeat: state.AgentHeartbeatConfig{
				Model: "gpt-4o-mini",
			},
		}},
	}

	run, err := buildHeartbeatAgentRun(cfg, "main", nil)
	if err != nil {
		t.Fatalf("buildHeartbeatAgentRun: %v", err)
	}
	if run.PrimaryModel != "gpt-4o-mini" {
		t.Fatalf("expected heartbeat model override, got %q", run.PrimaryModel)
	}
	if run.Runtime == nil {
		t.Fatal("expected heartbeat runtime")
	}
	if len(run.RuntimeLabels) == 0 || run.RuntimeLabels[0] != "gpt-4o-mini" {
		t.Fatalf("unexpected runtime labels: %#v", run.RuntimeLabels)
	}
}

func TestHeartbeatRunnerAgentIDsUniqueOrder(t *testing.T) {
	ids := heartbeatRunnerAgentIDs(state.ConfigDoc{Agents: state.AgentsConfig{{ID: "main"}, {ID: "ops"}, {ID: "main"}, {ID: "  "}}})
	if len(ids) != 2 || ids[0] != "main" || ids[1] != "ops" {
		t.Fatalf("unexpected heartbeat agent ids: %#v", ids)
	}
}

func TestHeartbeatRunnerWakeTargetsOnlyRequestedAgent(t *testing.T) {
	withHeartbeatTestGlobals(t, stubHeartbeatRuntime{})
	ops := newOperationsRegistry()
	ops.QueueHeartbeatWake("main", "control", "check main only", "now")
	runner := newHeartbeatRunner(ops, func() state.ConfigDoc {
		return state.ConfigDoc{
			Agent: state.AgentPolicy{DefaultModel: "gpt-4o"},
			Agents: state.AgentsConfig{
				{ID: "main", Model: "gpt-4o"},
				{ID: "ops", Model: "gpt-4o"},
			},
		}
	})
	runs := make(chan heartbeatAgentRun, 4)
	runner.runAgent = func(ctx context.Context, run heartbeatAgentRun) error {
		runs <- run
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runner.Start(ctx)
	defer func() {
		cancel()
		<-done
	}()

	select {
	case run := <-runs:
		if run.AgentID != "main" {
			t.Fatalf("expected wake to target main only, got agent=%q", run.AgentID)
		}
		if len(run.Wakes) != 1 || run.Wakes[0].AgentID != "main" {
			t.Fatalf("unexpected wake payload: %#v", run.Wakes)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected targeted wake to trigger a run")
	}

	select {
	case run := <-runs:
		t.Fatalf("unexpected extra run for agent=%q", run.AgentID)
	case <-time.After(50 * time.Millisecond):
	}
}

type capturingHeartbeatRuntime struct {
	turns chan agent.Turn
	args  map[string]any
}

func (r *capturingHeartbeatRuntime) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	if r.turns != nil {
		r.turns <- turn
	}
	toolResult := `{"outcome":"no_change","notify":false,"summary":"No changes"}`
	if turn.Executor != nil && len(r.args) > 0 {
		value, err := turn.Executor.Execute(ctx, agent.ToolCall{ID: "hb1", Name: agent.HeartbeatResponseToolName, Args: r.args})
		if err != nil {
			return agent.TurnResult{}, err
		}
		toolResult = value
	}
	argsJSON, _ := json.Marshal(r.args)
	return agent.TurnResult{
		Text: "heartbeat structured response recorded",
		HistoryDelta: []agent.ConversationMessage{
			{Role: "assistant", ToolCalls: []agent.ToolCallRef{{ID: "hb1", Name: agent.HeartbeatResponseToolName, ArgsJSON: string(argsJSON)}}},
			{Role: "tool", ToolCallID: "hb1", Content: toolResult},
			{Role: "assistant", Content: "heartbeat structured response recorded"},
		},
	}, nil
}

func TestBuildHeartbeatRunnerPromptRequiresStructuredResponse(t *testing.T) {
	prompt := buildHeartbeatRunnerPrompt("main", nil)
	if !strings.Contains(prompt, agent.HeartbeatResponseToolName) {
		t.Fatalf("prompt does not mention %s: %s", agent.HeartbeatResponseToolName, prompt)
	}
	if !strings.Contains(prompt, "MUST") || !strings.Contains(prompt, "notify=false") {
		t.Fatalf("prompt does not require heartbeat_respond contract: %s", prompt)
	}
	if strings.Contains(prompt, "HEARTBEAT_OK") {
		t.Fatalf("prompt still contains freeform HEARTBEAT_OK contract: %s", prompt)
	}
}

func TestExecuteHeartbeatAgentRunExposesConsumesAndSchedulesStructuredResponse(t *testing.T) {
	nextCheck := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)
	rt := &capturingHeartbeatRuntime{
		turns: make(chan agent.Turn, 1),
		args: map[string]any{
			"outcome":           "progress",
			"notify":            true,
			"summary":           "Made progress",
			"notification_text": "Heartbeat: made progress",
			"priority":          "high",
			"next_check":        nextCheck,
		},
	}
	emitter := &capturingEmitter{}
	ops := newOperationsRegistry()
	svc := &daemonServices{
		emitter:       emitter,
		runtimeConfig: newRuntimeConfigStore(state.ConfigDoc{}),
		session: sessionServices{
			ops: ops,
		},
	}
	run := heartbeatAgentRun{
		AgentID:   "main",
		SessionID: "heartbeat:main",
		Prompt:    buildHeartbeatRunnerPrompt("main", nil),
		Runtime:   rt,
		TimeoutMS: 1000,
	}
	if err := svc.executeHeartbeatAgentRun(context.Background(), run); err != nil {
		t.Fatalf("executeHeartbeatAgentRun: %v", err)
	}

	select {
	case turn := <-rt.turns:
		foundDef := false
		for _, def := range turn.Tools {
			if def.Name == agent.HeartbeatResponseToolName {
				foundDef = true
				break
			}
		}
		if !foundDef {
			t.Fatalf("heartbeat_respond definition not exposed in turn tools: %#v", turn.Tools)
		}
		if turn.Executor == nil {
			t.Fatal("expected heartbeat executor to be available")
		}
	default:
		t.Fatal("runtime did not receive heartbeat turn")
	}

	events := emitter.eventsByName(gatewayws.EventCompatHeartbeat)
	if len(events) != 1 {
		t.Fatalf("expected one heartbeat event, got %d", len(events))
	}
	payload, ok := events[0].(map[string]any)
	if !ok {
		t.Fatalf("heartbeat payload type = %T", events[0])
	}
	if payload["notify"] != true || payload["text"] != "Heartbeat: made progress" || payload["outcome"] != "progress" || payload["priority"] != "high" {
		t.Fatalf("unexpected heartbeat payload: %#v", payload)
	}
	if payload["channel_data"] == nil {
		t.Fatalf("expected heartbeat channel data in payload: %#v", payload)
	}

	_, wakes, _ := ops.HeartbeatSnapshot()
	if len(wakes) != 1 {
		t.Fatalf("expected scheduled next_check wake, got %#v", wakes)
	}
	wantNext, err := time.Parse(time.RFC3339, nextCheck)
	if err != nil {
		t.Fatalf("parse nextCheck fixture: %v", err)
	}
	if wakes[0].AgentID != "main" || wakes[0].Mode != "scheduled" || wakes[0].AtMS != wantNext.UnixMilli() {
		t.Fatalf("unexpected scheduled wake: %#v", wakes[0])
	}
}

func TestExecuteHeartbeatAgentRunRequiresHeartbeatRespond(t *testing.T) {
	rt := agent.Runtime(runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "freeform ok"}, nil
	}))
	svc := &daemonServices{runtimeConfig: newRuntimeConfigStore(state.ConfigDoc{})}
	err := svc.executeHeartbeatAgentRun(context.Background(), heartbeatAgentRun{
		AgentID:   "main",
		SessionID: "heartbeat:main",
		Prompt:    buildHeartbeatRunnerPrompt("main", nil),
		Runtime:   rt,
		TimeoutMS: 1000,
	})
	if err == nil || !strings.Contains(err.Error(), agent.HeartbeatResponseToolName) {
		t.Fatalf("expected missing heartbeat_respond error, got %v", err)
	}
}

func TestExecuteHeartbeatAgentRunRejectsMalformedNextCheck(t *testing.T) {
	rt := &capturingHeartbeatRuntime{
		turns: make(chan agent.Turn, 1),
		args: map[string]any{
			"outcome":    "progress",
			"notify":     false,
			"summary":    "Need another check",
			"next_check": "PT1H30",
		},
	}
	ops := newOperationsRegistry()
	svc := &daemonServices{
		runtimeConfig: newRuntimeConfigStore(state.ConfigDoc{}),
		session:       sessionServices{ops: ops},
	}
	err := svc.executeHeartbeatAgentRun(context.Background(), heartbeatAgentRun{
		AgentID:   "main",
		SessionID: "heartbeat:main",
		Prompt:    buildHeartbeatRunnerPrompt("main", nil),
		Runtime:   rt,
		TimeoutMS: 1000,
	})
	if err == nil || !strings.Contains(err.Error(), "next_check") {
		t.Fatalf("expected malformed next_check error, got %v", err)
	}
	_, wakes, _ := ops.HeartbeatSnapshot()
	if len(wakes) != 0 {
		t.Fatalf("malformed next_check should not enqueue wakes, got %#v", wakes)
	}
}

func TestExtractHeartbeatResponseRequiresExactlyOneCall(t *testing.T) {
	args := `{"outcome":"no_change","notify":false,"summary":"No changes"}`
	_, err := extractHeartbeatResponseFromTurnResult(agent.TurnResult{HistoryDelta: []agent.ConversationMessage{
		{Role: "assistant", ToolCalls: []agent.ToolCallRef{
			{ID: "hb1", Name: agent.HeartbeatResponseToolName, ArgsJSON: args},
			{ID: "hb2", Name: agent.HeartbeatResponseToolName, ArgsJSON: args},
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("expected exactly-once error, got %v", err)
	}
}

func TestHeartbeatRunnerWaitsForScheduledWake(t *testing.T) {
	now := time.UnixMilli(1_000)
	future := heartbeatWakeRecord{AtMS: now.Add(250 * time.Millisecond).UnixMilli(), Mode: "scheduled"}
	if wait, ok := heartbeatRunnerWaitDuration(heartbeatRunnerStatus{}, []heartbeatWakeRecord{future}, now); !ok || wait != 250*time.Millisecond {
		t.Fatalf("wait = %v ok=%v, want 250ms true", wait, ok)
	}
	if hasImmediateHeartbeatWake([]heartbeatWakeRecord{future}, now) {
		t.Fatal("future scheduled wake should not run immediately")
	}
	if !hasImmediateHeartbeatWake([]heartbeatWakeRecord{future}, now.Add(250*time.Millisecond)) {
		t.Fatal("due scheduled wake should run immediately")
	}
}
