package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

type runtimeFunc func(context.Context, agent.Turn) (agent.TurnResult, error)

func (f runtimeFunc) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return f(ctx, turn)
}

func TestExecuteAgentRunWithFallbacks_PersistsFallbackState(t *testing.T) {
	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	prevSessionStore := controlSessionStore
	controlSessionStore = ss
	defer func() { controlSessionStore = prevSessionStore }()

	runID := "run-fallback"
	req := methods.AgentRequest{SessionID: "session-fallback", Message: "hello", TimeoutMS: 1000}
	jobs := newAgentJobRegistry()
	_ = jobs.Begin(runID, req.SessionID)

	primary := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{}, fmt.Errorf("429 rate limit")
	})
	fallback := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "ok"}, nil
	})

	executeAgentRunWithFallbacks(runID, req, primary, []agent.Runtime{fallback}, []string{"claude-sonnet", "claude-haiku"}, nil, jobs)

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

func TestExecuteAgentRunWithFallbacks_ClearsFallbackStateOnPrimarySuccess(t *testing.T) {
	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	prevSessionStore := controlSessionStore
	controlSessionStore = ss
	defer func() { controlSessionStore = prevSessionStore }()

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
	jobs := newAgentJobRegistry()
	_ = jobs.Begin(runID, req.SessionID)
	primary := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "ok"}, nil
	})

	executeAgentRunWithFallbacks(runID, req, primary, nil, []string{"claude-sonnet"}, nil, jobs)

	se, ok := ss.Get(req.SessionID)
	if !ok {
		t.Fatal("session not found")
	}
	if se.FallbackFrom != "" || se.FallbackTo != "" || se.FallbackReason != "" || se.FallbackAt != 0 {
		t.Fatalf("fallback fields should be cleared: %+v", se)
	}
}

func TestRenderResponseWithUsage_Modes(t *testing.T) {
	base := "answer"
	usage := agent.TurnUsage{InputTokens: 10, OutputTokens: 5}
	se := &state.SessionEntry{ResponseUsage: "tokens"}
	out := renderResponseWithUsage(base, usage, se)
	if !strings.Contains(out, "in=10 out=5 total=15") {
		t.Fatalf("tokens mode footer missing: %q", out)
	}
	se.ResponseUsage = "off"
	if got := renderResponseWithUsage(base, usage, se); got != base {
		t.Fatalf("off mode should not append footer: %q", got)
	}
	se.ResponseUsage = "full"
	se.TotalTokens = 100
	full := renderResponseWithUsage(base, usage, se)
	if !strings.Contains(full, "session_total=115") {
		t.Fatalf("full mode footer missing session total: %q", full)
	}
}
