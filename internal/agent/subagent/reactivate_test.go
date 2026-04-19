package subagent

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ReactivateCompletedSession
// ---------------------------------------------------------------------------

func TestReactivate_EndedRunIsReplaced(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:               "run-prev",
		ChildSessionKey:     "agent:main:subagent:follow",
		RequesterSessionKey: "agent:main:main",
		RequesterDisplayKey: "main",
		Task:                "follow up task",
		Cleanup:             "keep",
		Label:               "follow-up",
		RunTimeoutSeconds:   60,
		CreatedAt:           1000,
		StartedAt:           1001,
		EndedAt:             2000,
		Outcome:             &RunOutcome{Status: "ok"},
	})

	result, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "agent:main:subagent:follow",
		RunID:      "run-next",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if !result.Reactivated {
		t.Fatal("expected reactivated=true")
	}
	if result.PreviousRunID != "run-prev" {
		t.Errorf("previousRunID = %q, want run-prev", result.PreviousRunID)
	}
	if result.NewRunID != "run-next" {
		t.Errorf("newRunID = %q, want run-next", result.NewRunID)
	}

	// Old run should be removed.
	if r.Get("run-prev") != nil {
		t.Error("old run should be deleted")
	}

	// New run should exist with preserved context.
	next := r.Get("run-next")
	if next == nil {
		t.Fatal("new run not found")
	}
	if next.ChildSessionKey != "agent:main:subagent:follow" {
		t.Errorf("child session key not preserved: %q", next.ChildSessionKey)
	}
	if next.RequesterSessionKey != "agent:main:main" {
		t.Errorf("requester not preserved: %q", next.RequesterSessionKey)
	}
	if next.Task != "follow up task" {
		t.Errorf("task not preserved: %q", next.Task)
	}
	if next.Cleanup != "keep" {
		t.Errorf("cleanup not preserved: %q", next.Cleanup)
	}
	if next.Label != "follow-up" {
		t.Errorf("label not preserved: %q", next.Label)
	}
	if next.RunTimeoutSeconds != 60 {
		t.Errorf("timeout not preserved: %d", next.RunTimeoutSeconds)
	}
	if next.EndedAt != 0 {
		t.Error("new run should not be ended")
	}
	if next.Outcome != nil {
		t.Error("new run should have nil outcome")
	}
	if next.StartedAt == 0 {
		t.Error("new run should have startedAt set")
	}
	if next.CreatedAt != 1000 {
		t.Errorf("createdAt should be preserved from source: %d", next.CreatedAt)
	}
}

func TestReactivate_ActiveRunIsNotReactivated(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-active",
		ChildSessionKey: "child-active",
		Task:            "task",
		Cleanup:         "keep",
		CreatedAt:       100,
	})

	result, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "child-active",
		RunID:      "run-next-active",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if result.Reactivated {
		t.Error("should not reactivate an active run")
	}
}

func TestReactivate_NoRunForSessionKey(t *testing.T) {
	r := NewRegistry()
	result, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "nonexistent",
		RunID:      "run-new",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if result.Reactivated {
		t.Error("should not reactivate when no runs exist")
	}
}

func TestReactivate_RequiresSessionKey(t *testing.T) {
	r := NewRegistry()
	_, err := r.ReactivateCompletedSession(ReactivateInput{RunID: "run-1"})
	if err == nil || !strings.Contains(err.Error(), "session_key") {
		t.Fatalf("expected session_key error, got: %v", err)
	}
}

func TestReactivate_RequiresRunID(t *testing.T) {
	r := NewRegistry()
	_, err := r.ReactivateCompletedSession(ReactivateInput{SessionKey: "child-1"})
	if err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("expected run_id error, got: %v", err)
	}
}

func TestReactivate_TimeoutOverride(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:             "run-to",
		ChildSessionKey:   "child-to",
		Task:              "task",
		Cleanup:           "keep",
		RunTimeoutSeconds: 30,
		CreatedAt:         100,
		EndedAt:           200,
		Outcome:           &RunOutcome{Status: "timeout"},
	})

	_, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey:        "child-to",
		RunID:             "run-to-next",
		RunTimeoutSeconds: 120,
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	next := r.Get("run-to-next")
	if next.RunTimeoutSeconds != 120 {
		t.Errorf("timeout = %d, want 120", next.RunTimeoutSeconds)
	}
}

func TestReactivate_TimeoutPreservedWhenZero(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:             "run-tp",
		ChildSessionKey:   "child-tp",
		Task:              "task",
		Cleanup:           "keep",
		RunTimeoutSeconds: 45,
		CreatedAt:         100,
		EndedAt:           200,
		Outcome:           &RunOutcome{Status: "ok"},
	})

	_, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "child-tp",
		RunID:      "run-tp-next",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	next := r.Get("run-tp-next")
	if next.RunTimeoutSeconds != 45 {
		t.Errorf("timeout = %d, want 45 (preserved)", next.RunTimeoutSeconds)
	}
}

func TestReactivate_PicksLatestEndedRun(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:               "run-old-ended",
		ChildSessionKey:     "child-multi",
		RequesterSessionKey: "parent",
		Task:                "old task",
		Cleanup:             "keep",
		CreatedAt:           100,
		EndedAt:             200,
		Outcome:             &RunOutcome{Status: "ok"},
	})
	_ = r.Register(SubagentRunRecord{
		RunID:               "run-new-ended",
		ChildSessionKey:     "child-multi",
		RequesterSessionKey: "parent",
		Task:                "new task",
		Cleanup:             "keep",
		CreatedAt:           300,
		EndedAt:             400,
		Outcome:             &RunOutcome{Status: "timeout"},
	})

	result, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "child-multi",
		RunID:      "run-replacement",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if !result.Reactivated {
		t.Fatal("expected reactivated=true")
	}
	if result.PreviousRunID != "run-new-ended" {
		t.Errorf("should replace the latest ended, got %q", result.PreviousRunID)
	}

	next := r.Get("run-replacement")
	if next == nil {
		t.Fatal("new run not found")
	}
	if next.Task != "new task" {
		t.Errorf("should inherit from latest ended, task = %q", next.Task)
	}
}

func TestReactivate_ClearsOutcomeAndSuppressAnnounce(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:            "run-clear",
		ChildSessionKey:  "child-clear",
		Task:             "task",
		Cleanup:          "keep",
		CreatedAt:        100,
		EndedAt:          200,
		Outcome:          &RunOutcome{Status: "error", Error: "something broke"},
		SuppressAnnounce: "steer-restart",
	})

	_, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "child-clear",
		RunID:      "run-clear-next",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	next := r.Get("run-clear-next")
	if next.Outcome != nil {
		t.Error("outcome should be cleared")
	}
	if next.SuppressAnnounce != "" {
		t.Error("suppressAnnounce should be cleared")
	}
	if next.EndedAt != 0 {
		t.Error("endedAt should be cleared")
	}
}

func TestReactivate_RegistryCountsUpdated(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-cnt",
		ChildSessionKey: "child-cnt",
		Task:            "task",
		Cleanup:         "keep",
		CreatedAt:       100,
		EndedAt:         200,
		Outcome:         &RunOutcome{Status: "ok"},
	})

	if r.CountActive() != 0 {
		t.Errorf("before reactivation: active = %d, want 0", r.CountActive())
	}

	_, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "child-cnt",
		RunID:      "run-cnt-next",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if r.CountActive() != 1 {
		t.Errorf("after reactivation: active = %d, want 1", r.CountActive())
	}
	// Old run deleted, new run added → same total count.
	if r.Len() != 1 {
		t.Errorf("after reactivation: len = %d, want 1", r.Len())
	}
}

func TestReactivate_SameRunIDReplacement(t *testing.T) {
	// Edge case: reactivate with the same run ID (overwrites in place).
	r := NewRegistry()
	_ = r.Register(SubagentRunRecord{
		RunID:           "run-same",
		ChildSessionKey: "child-same",
		Task:            "task",
		Cleanup:         "keep",
		CreatedAt:       100,
		EndedAt:         200,
		Outcome:         &RunOutcome{Status: "ok"},
	})

	result, err := r.ReactivateCompletedSession(ReactivateInput{
		SessionKey: "child-same",
		RunID:      "run-same",
	})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if !result.Reactivated {
		t.Fatal("expected reactivated=true")
	}
	if result.PreviousRunID != "run-same" || result.NewRunID != "run-same" {
		t.Error("expected same run ID")
	}
	next := r.Get("run-same")
	if next == nil {
		t.Fatal("expected run")
	}
	if next.EndedAt != 0 {
		t.Error("should be active after reactivation")
	}
	if r.Len() != 1 {
		t.Errorf("len = %d, want 1", r.Len())
	}
}
