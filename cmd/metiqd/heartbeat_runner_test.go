package main

import (
	"context"
	"testing"
	"time"

	"metiq/internal/agent"
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
	t.Cleanup(func() {
		controlAgentRuntime = prevRuntime
		controlAgentRegistry = prevRegistry
		controlToolRegistry = prevToolRegistry
		controlServices = prevServices
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
