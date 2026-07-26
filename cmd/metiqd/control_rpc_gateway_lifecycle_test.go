package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"metiq/internal/gateway/methods"
	suspendpkg "metiq/internal/gateway/suspend"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func lifecycleCall(t *testing.T, h controlRPCHandler, method, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleGatewayLifecycleRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled by gateway lifecycle dispatch", method)
	}
	return result, err
}

func TestGatewayRestartPreflightReadiness(t *testing.T) {
	// Empty daemon: no in-flight runs, no sessions -> ready.
	h := newControlRPCHandler(controlRPCDeps{})
	res, err := lifecycleCall(t, h, methods.MethodGatewayRestartPreflight, `{}`)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ready"] != true || payload["inFlightRuns"].(int) != 0 || payload["activeSessions"].(int) != 0 {
		t.Fatalf("expected ready empty preflight, got %+v", payload)
	}

	// A pending run + an active session -> not ready, counts reflected.
	jobs := newAgentJobRegistry()
	jobs.Begin("run-1", "session-1")
	sessions, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Put("session-1", state.SessionEntry{SessionID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	h = newControlRPCHandler(controlRPCDeps{agentJobs: jobs, sessionStore: sessions})
	res, err = lifecycleCall(t, h, methods.MethodGatewayRestartPreflight, `{}`)
	if err != nil {
		t.Fatalf("preflight2: %v", err)
	}
	payload = res.Result.(map[string]any)
	if payload["ready"] != false || payload["inFlightRuns"].(int) != 1 || payload["activeSessions"].(int) != 1 {
		t.Fatalf("expected busy preflight, got %+v", payload)
	}
}

func TestGatewayRestartRequestSchedules(t *testing.T) {
	// No restart channel wired -> honest error.
	h := newControlRPCHandler(controlRPCDeps{})
	if _, err := lifecycleCall(t, h, methods.MethodGatewayRestartRequest, `{}`); err == nil {
		t.Fatal("expected error when restart scheduler is unavailable")
	}

	restartCh := make(chan int, 1)
	h = newControlRPCHandler(controlRPCDeps{restartCh: restartCh})
	res, err := lifecycleCall(t, h, methods.MethodGatewayRestartRequest, `{"reason":"config change","delayMs":250}`)
	if err != nil {
		t.Fatalf("restart.request: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["scheduled"] != true || payload["delayMs"].(int) != 250 || payload["reason"] != "config change" {
		t.Fatalf("unexpected restart.request result: %+v", payload)
	}
	select {
	case got := <-restartCh:
		if got != 250 {
			t.Fatalf("expected delay 250 sent to scheduler, got %d", got)
		}
	default:
		t.Fatal("expected a restart signal on the channel")
	}

	// Negative delay is rejected at the schema layer.
	if _, err := lifecycleCall(t, h, methods.MethodGatewayRestartRequest, `{"delayMs":-5}`); err == nil {
		t.Fatal("expected error for negative delay")
	}
}

func TestGatewaySuspendSurfaceUnavailableWhenUnwired(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	for _, method := range []string{
		methods.MethodGatewaySuspendPrepare,
		methods.MethodGatewaySuspendStatus,
		methods.MethodGatewaySuspendResume,
	} {
		if _, err := lifecycleCall(t, h, method, `{}`); err == nil {
			t.Fatalf("expected %s to report the suspend coordinator unavailable when unwired", method)
		}
	}
}

func TestGatewaySuspendPrepareStatusResumeLifecycle(t *testing.T) {
	coord := suspendpkg.NewCoordinator()
	coord.RegisterPausableWorker("cron-scheduler")
	coord.RegisterPausableWorker("dreaming-promotion-job")

	// A pending agent run + active session are reported as in-flight (not killed).
	jobs := newAgentJobRegistry()
	jobs.Begin("run-1", "session-1")
	sessions, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Put("session-1", state.SessionEntry{SessionID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	h := newControlRPCHandler(controlRPCDeps{suspendCoordinator: coord, agentJobs: jobs, sessionStore: sessions})

	// status before any suspension -> idle, accepting work.
	res, err := lifecycleCall(t, h, methods.MethodGatewaySuspendStatus, `{}`)
	if err != nil {
		t.Fatalf("status idle: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["state"] != suspendpkg.StateIdle {
		t.Fatalf("expected idle state, got %+v", payload)
	}
	if !coord.AcceptingWork() {
		t.Fatal("coordinator should accept work before prepare")
	}

	// prepare -> suspended, gate closed, in-flight reported, pausedWorkers listed.
	res, err = lifecycleCall(t, h, methods.MethodGatewaySuspendPrepare, `{"reason":"host hibernation"}`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	payload = res.Result.(map[string]any)
	if payload["state"] != suspendpkg.StateSuspended {
		t.Fatalf("expected suspended state, got %+v", payload)
	}
	suspensionID, _ := payload["suspensionId"].(string)
	if suspensionID == "" {
		t.Fatal("prepare must return a suspensionId")
	}
	inFlight := payload["inFlight"].(map[string]any)
	if inFlight["agentRuns"].(int) != 1 || inFlight["sessions"].(int) != 1 {
		t.Fatalf("expected in-flight run+session reported, got %+v", inFlight)
	}
	if payload["quiesced"] != false {
		t.Fatal("quiesced must be false while an agent run is still in flight")
	}
	pausedWorkers := payload["pausedWorkers"].([]string)
	if len(pausedWorkers) != 2 {
		t.Fatalf("expected 2 paused workers, got %+v", pausedWorkers)
	}
	if coord.AcceptingWork() {
		t.Fatal("gate must be closed while suspended (background dispatch paused)")
	}

	// prepare again -> idempotent, same suspensionId.
	res, err = lifecycleCall(t, h, methods.MethodGatewaySuspendPrepare, `{"reason":"second"}`)
	if err != nil {
		t.Fatalf("prepare idempotent: %v", err)
	}
	if res.Result.(map[string]any)["suspensionId"] != suspensionID {
		t.Fatal("prepare must be idempotent (same suspensionId)")
	}

	// resume with a wrong id -> rejected, still suspended.
	if _, err := lifecycleCall(t, h, methods.MethodGatewaySuspendResume, `{"suspensionId":"wrong-id"}`); err == nil {
		t.Fatal("resume with mismatched id must be rejected")
	}
	if !coord.Suspended() {
		t.Fatal("rejected resume must leave the suspension active")
	}

	// resume with the correct id -> idle, gate re-opened.
	res, err = lifecycleCall(t, h, methods.MethodGatewaySuspendResume, `{"suspensionId":"`+suspensionID+`"}`)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Result.(map[string]any)["state"] != suspendpkg.StateIdle {
		t.Fatalf("expected idle after resume, got %+v", res.Result)
	}
	if !coord.AcceptingWork() {
		t.Fatal("gate must re-open after resume (background dispatch resumes)")
	}

	// resume again -> rejected (not suspended).
	if _, err := lifecycleCall(t, h, methods.MethodGatewaySuspendResume, `{}`); err == nil {
		t.Fatal("resume when not suspended must be rejected")
	}
}
