package main

import (
	"context"
	"testing"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func TestExecApprovalsEventDrivenWait(t *testing.T) {
	reg := newExecApprovalsRegistry()

	rec := reg.Request(methods.ExecApprovalRequestRequest{
		Command:   "test-event",
		TimeoutMS: 5000,
	})

	done := make(chan bool)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, err := reg.Resolve(methods.ExecApprovalResolveRequest{
			ID:       rec.ID,
			Decision: "approve",
		})
		if err != nil {
			t.Errorf("resolve error: %v", err)
		}
		done <- true
	}()

	result, resolved, err := reg.WaitForDecision(context.Background(), rec.ID, 2000)
	if err != nil {
		t.Fatalf("wait error: %v", err)
	}
	if !resolved {
		t.Fatalf("expected resolved=true, got false")
	}
	if result.Decision != "approve" {
		t.Fatalf("expected decision=approve, got %s", result.Decision)
	}

	<-done
}

func TestExecApprovalsMultipleWaiters(t *testing.T) {
	reg := newExecApprovalsRegistry()

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

	time.Sleep(50 * time.Millisecond)
	_, err := reg.Resolve(methods.ExecApprovalResolveRequest{
		ID:       rec.ID,
		Decision: "approve",
	})
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	for i := 0; i < 3; i++ {
		if !<-results {
			t.Fatalf("waiter %d did not receive resolution", i)
		}
	}
}

func TestExecApprovalsContextCancellation(t *testing.T) {
	reg := newExecApprovalsRegistry()

	rec := reg.Request(methods.ExecApprovalRequestRequest{
		Command:   "test-cancel",
		TimeoutMS: 5000,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)

	go func() {
		_, resolved, err := reg.WaitForDecision(ctx, rec.ID, 5000)
		if err != nil {
			t.Errorf("wait error: %v", err)
		}
		if resolved {
			t.Errorf("expected resolved=false due to cancellation")
		}
		done <- true
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
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
