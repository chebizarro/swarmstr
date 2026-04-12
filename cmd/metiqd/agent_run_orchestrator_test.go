package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func newTestAgentRunController(t *testing.T) (agentRunController, *state.SessionStore, *agentJobRegistry, *SubagentRegistry) {
	t.Helper()
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	jobs := newAgentJobRegistry()
	subagents := newSubagentRegistry()
	ctrl := agentRunController{
		sessionStore:   sessionStore,
		defaultRuntime: runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) { return agent.TurnResult{Text: "ok"}, nil }),
		jobs:           jobs,
		subagents:      subagents,
		emitEvent:      func(string, any) {},
	}
	return ctrl, sessionStore, jobs, subagents
}

func TestAgentRunControllerExecuteAgentRunWithFallbacks_PersistsFallbackState(t *testing.T) {
	ctrl, ss, jobs, _ := newTestAgentRunController(t)
	runID := "run-fallback"
	req := methods.AgentRequest{SessionID: "session-fallback", Message: "hello", TimeoutMS: 1000}
	_ = jobs.Begin(runID, req.SessionID)

	primary := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{}, fmt.Errorf("429 rate limit")
	})
	fallback := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "ok"}, nil
	})

	ctrl.executeAgentRunWithFallbacks(runID, req, primary, []agent.Runtime{fallback}, []string{"claude-sonnet", "claude-haiku"}, nil, jobs)

	se, ok := ss.Get(req.SessionID)
	if !ok {
		t.Fatal("session not found")
	}
	if se.FallbackFrom != "claude-sonnet" || se.FallbackTo != "claude-haiku" {
		t.Fatalf("fallback fields not persisted: %+v", se)
	}
	if strings.TrimSpace(se.FallbackReason) == "" {
		t.Fatalf("fallback reason should be captured: %+v", se)
	}
	snap, ok := jobs.Get(runID)
	if !ok {
		t.Fatal("job snapshot missing")
	}
	if !snap.FallbackUsed || snap.FallbackTo != "claude-haiku" {
		t.Fatalf("job fallback snapshot mismatch: %+v", snap)
	}
}

func TestAgentRunControllerExecuteAgentRunWithFallbacks_ClearsFallbackStateOnPrimarySuccess(t *testing.T) {
	ctrl, ss, jobs, _ := newTestAgentRunController(t)
	req := methods.AgentRequest{SessionID: "session-primary", Message: "hello", TimeoutMS: 1000}
	seed := ss.GetOrNew(req.SessionID)
	seed.FallbackFrom = "x"
	seed.FallbackTo = "y"
	seed.FallbackReason = "z"
	seed.FallbackAt = 123
	if err := ss.Put(req.SessionID, seed); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	runID := "run-primary"
	_ = jobs.Begin(runID, req.SessionID)
	primary := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "ok"}, nil
	})

	ctrl.executeAgentRunWithFallbacks(runID, req, primary, nil, []string{"claude-sonnet"}, nil, jobs)

	se, ok := ss.Get(req.SessionID)
	if !ok {
		t.Fatal("session not found")
	}
	if se.FallbackFrom != "" || se.FallbackTo != "" || se.FallbackReason != "" || se.FallbackAt != 0 {
		t.Fatalf("fallback fields should be cleared: %+v", se)
	}
}

func TestAgentRunControllerApplySessionsSpawn_InheritsParentTaskLinkage(t *testing.T) {
	ctrl, sessionStore, jobs, _ := newTestAgentRunController(t)
	if err := sessionStore.Put("parent-session", state.SessionEntry{
		SessionID:    "parent-session",
		AgentID:      "planner",
		ActiveTaskID: "task-parent",
		ActiveRunID:  "run-parent",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed parent session: %v", err)
	}

	resp, err := ctrl.applySessionsSpawn(context.Background(), methods.SessionsSpawnRequest{
		ParentSessionID: "parent-session",
		AgentID:         "worker",
		Message:         "do the child task",
		TimeoutMS:       500,
	}, state.ConfigDoc{}, nil, nil)
	if err != nil {
		t.Fatalf("applySessionsSpawn: %v", err)
	}
	if runID, _ := resp["run_id"].(string); runID != "" {
		jobs.Wait(context.Background(), runID, 5*time.Second)
	}
	sessionID, _ := resp["session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatalf("expected spawned session id, got %#v", resp)
	}
	entry, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatalf("expected spawned session entry for %s", sessionID)
	}
	if entry.ParentTaskID != "task-parent" || entry.ParentRunID != "run-parent" {
		t.Fatalf("spawned session linkage = %+v", entry)
	}
	if entry.SpawnedBy != "sessions.spawn" {
		t.Fatalf("spawned session spawned_by = %q, want sessions.spawn", entry.SpawnedBy)
	}
}

func TestAgentRunControllerApplySessionsSpawn_UsesCapturedDependencies(t *testing.T) {
	ctrl, sessionStore, jobs, subagents := newTestAgentRunController(t)
	blocked := make(chan struct{})
	ctrl.defaultRuntime = runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		<-blocked
		return agent.TurnResult{Text: "ok"}, nil
	})
	if err := sessionStore.Put("parent-session", state.SessionEntry{
		SessionID: "parent-session",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed parent session: %v", err)
	}

	prevJobs := controlAgentJobs
	prevSubagents := controlSubagents
	prevSessionStore := controlSessionStore
	defer func() {
		controlAgentJobs = prevJobs
		controlSubagents = prevSubagents
		controlSessionStore = prevSessionStore
	}()

	altJobs := newAgentJobRegistry()
	altSubagents := newSubagentRegistry()
	altStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "alt-sessions.json"))
	if err != nil {
		t.Fatalf("new alt session store: %v", err)
	}

	resp, err := ctrl.applySessionsSpawn(context.Background(), methods.SessionsSpawnRequest{
		ParentSessionID: "parent-session",
		AgentID:         "worker",
		Message:         "do the child task",
		TimeoutMS:       500,
	}, state.ConfigDoc{}, nil, nil)
	if err != nil {
		t.Fatalf("applySessionsSpawn: %v", err)
	}

	controlAgentJobs = altJobs
	controlSubagents = altSubagents
	controlSessionStore = altStore
	close(blocked)

	runID, _ := resp["run_id"].(string)
	if strings.TrimSpace(runID) == "" {
		t.Fatalf("expected run id, got %#v", resp)
	}
	if _, ok := jobs.Wait(context.Background(), runID, 5*time.Second); !ok {
		t.Fatalf("spawned job did not finish in original registry")
	}
	if _, ok := altJobs.Get(runID); ok {
		t.Fatalf("job should not be tracked in alternate registry")
	}
	rec := subagents.Get(runID)
	if rec == nil {
		t.Fatalf("expected subagent record in original registry")
	}
	if rec.Status != "done" || rec.Result != "ok" {
		t.Fatalf("unexpected original subagent record: %+v", rec)
	}
	if altSubagents.Get(runID) != nil {
		t.Fatalf("subagent record should not be tracked in alternate registry")
	}
}
