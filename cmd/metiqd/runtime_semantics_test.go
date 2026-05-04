package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func TestExecApprovalsEventDrivenWait(t *testing.T) {
	reg := newExecApprovalsRegistry()
	waitRegistered := make(chan struct{}, 1)
	reg.onWaitRegistered = func(string) {
		waitRegistered <- struct{}{}
	}

	rec := reg.Request(methods.ExecApprovalRequestRequest{
		Command:   "test-event",
		TimeoutMS: 5000,
	})

	done := make(chan struct{}, 1)
	go func() {
		result, resolved, err := reg.WaitForDecision(context.Background(), rec.ID, 2000)
		if err != nil {
			t.Errorf("wait error: %v", err)
		}
		if !resolved {
			t.Errorf("expected resolved=true, got false")
		}
		if result.Decision != "approve" {
			t.Errorf("expected decision=approve, got %s", result.Decision)
		}
		done <- struct{}{}
	}()

	select {
	case <-waitRegistered:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not register")
	}

	_, err := reg.Resolve(methods.ExecApprovalResolveRequest{
		ID:       rec.ID,
		Decision: "approve",
	})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not complete")
	}
}

func TestExecApprovalsMultipleWaiters(t *testing.T) {
	reg := newExecApprovalsRegistry()
	waitRegistered := make(chan struct{}, 3)
	reg.onWaitRegistered = func(string) {
		waitRegistered <- struct{}{}
	}

	rec := reg.Request(methods.ExecApprovalRequestRequest{
		Command:   "test-multi",
		TimeoutMS: 5000,
	})

	results := make(chan bool, 3)

	for i := 0; i < 3; i++ {
		go func() {
			result, resolved, err := reg.WaitForDecision(context.Background(), rec.ID, 2000)
			if err != nil || !resolved || result.Decision != "approve" {
				results <- false
			} else {
				results <- true
			}
		}()
	}

	for i := 0; i < 3; i++ {
		select {
		case <-waitRegistered:
		case <-time.After(2 * time.Second):
			t.Fatalf("waiter %d did not register", i)
		}
	}

	_, err := reg.Resolve(methods.ExecApprovalResolveRequest{
		ID:       rec.ID,
		Decision: "approve",
	})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	for i := 0; i < 3; i++ {
		select {
		case ok := <-results:
			if !ok {
				t.Fatalf("waiter %d did not receive resolution", i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("waiter %d did not complete", i)
		}
	}
}

func TestExecApprovalsContextCancellation(t *testing.T) {
	reg := newExecApprovalsRegistry()
	waitRegistered := make(chan struct{}, 1)
	reg.onWaitRegistered = func(string) {
		waitRegistered <- struct{}{}
	}

	rec := reg.Request(methods.ExecApprovalRequestRequest{
		Command:   "test-cancel",
		TimeoutMS: 5000,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 1)

	go func() {
		_, resolved, err := reg.WaitForDecision(ctx, rec.ID, 5000)
		if err != nil {
			t.Errorf("wait error: %v", err)
		}
		if resolved {
			t.Errorf("expected resolved=false due to cancellation")
		}
		done <- struct{}{}
	}()

	select {
	case <-waitRegistered:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not register")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not complete after cancellation")
	}
}

func TestExecApprovalsTimeout(t *testing.T) {
	reg := newExecApprovalsRegistry()

	rec := reg.Request(methods.ExecApprovalRequestRequest{
		Command:   "test-timeout",
		TimeoutMS: 5000,
	})

	_, resolved, err := reg.WaitForDecision(context.Background(), rec.ID, 100)
	if err != nil {
		t.Fatalf("wait error: %v", err)
	}
	if resolved {
		t.Fatalf("expected resolved=false due to timeout")
	}
}

func TestCronRegistrySaveNilRepoReturnsError(t *testing.T) {
	reg := newCronRegistry()
	reg.Add(methods.CronAddRequest{ID: "job-1", Schedule: "* * * * *", Method: "noop"})
	err := reg.Save(context.Background(), nil)
	if err == nil {
		t.Fatal("expected nil repo save to return error")
	}
	if !errors.Is(err, errCronPersistenceUnavailable) {
		t.Fatalf("expected errCronPersistenceUnavailable, got: %v", err)
	}
}

func TestCronRegistryLoadNilRepoNoOp(t *testing.T) {
	reg := newCronRegistry()
	job := reg.Add(methods.CronAddRequest{ID: "job-1", Schedule: "* * * * *", Method: "noop"})

	if err := reg.Load(context.Background(), nil); err != nil {
		t.Fatalf("expected nil repo load to no-op, got error: %v", err)
	}

	loaded, ok := reg.Status(job.ID)
	if !ok {
		t.Fatalf("expected existing job %q to remain after nil load", job.ID)
	}
	if loaded.Method != "noop" {
		t.Fatalf("expected job method to remain unchanged, got %q", loaded.Method)
	}
}

func TestHandleOpsRPCCronAddSurfacesPersistenceFailure(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{
		cronJobs:    newCronRegistry(),
		configState: newRuntimeConfigStore(state.ConfigDoc{}),
	})

	_, handled, err := h.handleOpsRPC(
		context.Background(),
		nostruntime.ControlRPCInbound{
			Method: methods.MethodCronAdd,
			Params: json.RawMessage(`{"id":"c1","schedule":"* * * * *","method":"status.get"}`),
		},
		methods.MethodCronAdd,
		state.ConfigDoc{},
	)
	if !handled {
		t.Fatal("expected cron.add to be handled")
	}
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if !errors.Is(err, errCronPersistenceUnavailable) {
		t.Fatalf("expected errCronPersistenceUnavailable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cron.add persist") {
		t.Fatalf("expected wrapped cron.add persist context, got: %v", err)
	}
}

func TestOperationsRegistryHeartbeatState(t *testing.T) {
	reg := newOperationsRegistry()
	enabled := true
	status := reg.SetHeartbeats(&enabled, 30000)
	if !status.Enabled || status.IntervalMS != 30000 {
		t.Fatalf("unexpected heartbeat status after set: %#v", status)
	}
	status = reg.QueueHeartbeatWake("main", "control", "wake", "now")
	if status.PendingWakes != 1 || status.LastWakeMS == 0 {
		t.Fatalf("unexpected heartbeat wake status: %#v", status)
	}
	wakes := reg.ConsumeHeartbeatWakes()
	if len(wakes) != 1 || wakes[0].AgentID != "main" || wakes[0].Source != "control" || wakes[0].Text != "wake" {
		t.Fatalf("unexpected consumed wakes: %#v", wakes)
	}
	status = reg.MarkHeartbeatRun(1234)
	if status.LastRunMS != 1234 || status.PendingWakes != 0 {
		t.Fatalf("unexpected heartbeat status after run: %#v", status)
	}
}

func TestTTSProviderValidation(t *testing.T) {
	reg := newOperationsRegistry()

	provider := reg.SetTTSProvider("openai")
	if provider != "openai" {
		t.Fatalf("expected openai, got %s", provider)
	}

	provider = reg.SetTTSProvider("elevenlabs")
	if provider != "elevenlabs" {
		t.Fatalf("expected elevenlabs, got %s", provider)
	}

	provider = reg.SetTTSProvider("edge")
	if provider != "edge" {
		t.Fatalf("expected edge, got %s", provider)
	}

	provider = reg.SetTTSProvider("invalid-provider")
	if provider != "openai" {
		t.Fatalf("expected invalid provider to default to openai, got %s", provider)
	}

	provider = reg.SetTTSProvider("")
	if provider != "openai" {
		t.Fatalf("expected empty provider to default to openai, got %s", provider)
	}
}

func TestSkillsBinsNilSafety(t *testing.T) {
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"skills": map[string]any{
				"entries": map[string]any{
					"test-skill": map[string]any{
						"name":    "Test Skill",
						"enabled": true,
						"status":  "active",
					},
					"test-skill-2": map[string]any{
						"name":   "Test Skill 2",
						"status": "inactive",
					},
					"test-skill-3": map[string]any{
						"enabled": nil,
					},
				},
			},
		},
	}

	result := applySkillsBins(cfg)
	bins, ok := result["bins"].([]string)
	if !ok {
		t.Fatalf("expected bins []string, got %T", result["bins"])
	}

	// Config-only entries should not leak bins; only final resolved catalog skills count.
	for _, want := range []string{"test-skill", "test-skill-2", "test-skill-3"} {
		for _, got := range bins {
			if got == want {
				t.Errorf("did not expect config-only bin %q in result, got bins: %v", want, bins)
			}
		}
	}
}
