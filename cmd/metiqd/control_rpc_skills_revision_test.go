package main

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	skillspkg "metiq/internal/skills"
	"metiq/internal/store/state"
)

const revisionSkillDraft = `---
name: revskill
description: A skill used by the requestRevision managed-run test.
---
# Revskill

Body content.
`

// setupRevisionWorkspace isolates the skills subsystem at temp dirs and stages a
// single pending proposal, returning its id.
func setupRevisionWorkspace(t *testing.T) (state.ConfigDoc, string) {
	t.Helper()
	ws := t.TempDir()
	t.Setenv("METIQ_WORKSPACE", ws)
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", t.TempDir())
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", t.TempDir())
	skillspkg.InvalidateSkillCatalogCache()

	cfg := state.ConfigDoc{}
	rec, err := skillspkg.NewProposalStore(cfg, "main").Create(skillspkg.ProposalDraftInput{
		Title:     "Revise me",
		Content:   revisionSkillDraft,
		SkillName: "revskill",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	return cfg, rec.ID
}

// wireControllerGlobals installs the fallback controller globals used by
// currentAgentRunController() when controlServices is nil, restoring them on
// cleanup. It returns the job registry and a pointer to the runtime call count.
func wireControllerGlobals(t *testing.T, rt agent.Runtime) *agentJobRegistry {
	t.Helper()
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	jobs := newAgentJobRegistry()

	prevRuntime, prevJobs, prevStore, prevSub, prevCfg := controlAgentRuntime, controlAgentJobs, controlSessionStore, controlSubagents, controlRuntimeConfig
	controlAgentRuntime = rt
	controlAgentJobs = jobs
	controlSessionStore = sessionStore
	controlSubagents = newSubagentRegistry()
	controlRuntimeConfig = newRuntimeConfigStore(state.ConfigDoc{})
	t.Cleanup(func() {
		controlAgentRuntime, controlAgentJobs, controlSessionStore, controlSubagents, controlRuntimeConfig = prevRuntime, prevJobs, prevStore, prevSub, prevCfg
	})
	return jobs
}

func TestRequestRevision_LaunchesManagedRunAndReturnsRunID(t *testing.T) {
	cfg, proposalID := setupRevisionWorkspace(t)
	var calls int32
	rt := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		atomic.AddInt32(&calls, 1)
		return agent.TurnResult{Text: "revised"}, nil
	})
	jobs := wireControllerGlobals(t, rt)

	h := controlRPCHandler{}
	req := methods.SkillsProposalsRequestRevisionRequest{
		ProposalID:   proposalID,
		Instructions: "Tighten the description.",
		SessionKey:   "sess-rev",
	}
	out, err := h.launchProposalRevisionRun(cfg, req)
	if err != nil {
		t.Fatalf("launchProposalRevisionRun: %v", err)
	}
	runID, _ := out["runId"].(string)
	if runID == "" {
		t.Fatalf("expected a runId, got %+v", out)
	}
	if _, ok := out["status"]; !ok {
		t.Fatalf("expected a status, got %+v", out)
	}
	// The run must be a real tracked managed run.
	if _, ok := jobs.Get(runID); !ok {
		t.Fatalf("run %q not tracked in job registry", runID)
	}
	if snap, ok := jobs.Wait(context.Background(), runID, 2*time.Second); !ok || snap.Status != "ok" {
		t.Fatalf("run did not complete ok: ok=%v snap=%+v", ok, snap)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("runtime invoked %d times, want 1", n)
	}
}

func TestRequestRevision_IdempotencyReplayReturnsSameRun(t *testing.T) {
	cfg, proposalID := setupRevisionWorkspace(t)
	var calls int32
	rt := runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		atomic.AddInt32(&calls, 1)
		return agent.TurnResult{Text: "revised"}, nil
	})
	jobs := wireControllerGlobals(t, rt)

	h := controlRPCHandler{}
	req := methods.SkillsProposalsRequestRevisionRequest{
		ProposalID:     proposalID,
		Instructions:   "Tighten the description.",
		SessionKey:     "sess-rev",
		IdempotencyKey: "idem-xfny5-abc",
	}

	first, err := h.launchProposalRevisionRun(cfg, req)
	if err != nil {
		t.Fatalf("first requestRevision: %v", err)
	}
	runID1, _ := first["runId"].(string)
	if runID1 == "" {
		t.Fatalf("expected runId on first call: %+v", first)
	}
	// Let the run finish so the second call replays a tracked (finished) run.
	jobs.Wait(context.Background(), runID1, 2*time.Second)

	second, err := h.launchProposalRevisionRun(cfg, req)
	if err != nil {
		t.Fatalf("replay requestRevision: %v", err)
	}
	runID2, _ := second["runId"].(string)
	if runID2 != runID1 {
		t.Fatalf("idempotency replay returned different run: %q vs %q", runID2, runID1)
	}
	if idem, _ := second["idempotent"].(bool); !idem {
		t.Fatalf("expected idempotent=true on replay, got %+v", second)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("runtime invoked %d times across replay, want 1 (no double-launch)", n)
	}
}

func TestRequestRevision_RejectsNonPendingAndMissing(t *testing.T) {
	cfg, proposalID := setupRevisionWorkspace(t)
	wireControllerGlobals(t, runtimeFunc(func(context.Context, agent.Turn) (agent.TurnResult, error) {
		return agent.TurnResult{Text: "x"}, nil
	}))
	h := controlRPCHandler{}

	// Missing proposal -> ErrNotFound.
	if _, err := h.launchProposalRevisionRun(cfg, methods.SkillsProposalsRequestRevisionRequest{
		ProposalID: "prop_does_not_exist", Instructions: "x",
	}); err == nil {
		t.Fatalf("expected error for missing proposal")
	}

	// Reject the proposal, then requestRevision must refuse a non-pending draft.
	if _, err := skillspkg.NewProposalStore(cfg, "main").Reject(proposalID, "nope"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if _, err := h.launchProposalRevisionRun(cfg, methods.SkillsProposalsRequestRevisionRequest{
		ProposalID: proposalID, Instructions: "x",
	}); err == nil {
		t.Fatalf("expected error revising a non-pending proposal")
	}
}

func TestRequestRevision_ConcurrentReplaysLaunchOnce(t *testing.T) {
	cfg, proposalID := setupRevisionWorkspace(t)
	var calls int32
	entered := make(chan struct{}, 32)
	release := make(chan struct{})
	rt := runtimeFunc(func(ctx context.Context, _ agent.Turn) (agent.TurnResult, error) {
		atomic.AddInt32(&calls, 1)
		entered <- struct{}{}
		<-release
		return agent.TurnResult{Text: "revised"}, nil
	})
	jobs := wireControllerGlobals(t, rt)
	h := controlRPCHandler{}
	req := methods.SkillsProposalsRequestRevisionRequest{
		ProposalID:     proposalID,
		Instructions:   "Tighten wording.",
		SessionKey:     "sess-concurrent",
		IdempotencyKey: "idem-concurrent-1",
	}

	const n = 8
	runIDs := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			out, err := h.launchProposalRevisionRun(cfg, req)
			if err != nil {
				errs[i] = err
				return
			}
			runIDs[i], _ = out["runId"].(string)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d errored: %v", i, err)
		}
	}
	first := runIDs[0]
	if first == "" {
		t.Fatalf("empty runId")
	}
	for i, id := range runIDs {
		if id != first {
			t.Fatalf("call %d returned divergent runId %q (want %q)", i, id, first)
		}
	}
	<-entered // at least one managed run actually started
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("runtime launched %d times, want exactly 1 (no double-launch)", n)
	}
	close(release)
	jobs.Wait(context.Background(), first, 2*time.Second)
}
