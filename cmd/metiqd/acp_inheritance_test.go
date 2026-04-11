package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	ctxengine "metiq/internal/context"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const testACPSenderPubKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type capturingProvider struct {
	lastTurn agent.Turn
	result   agent.ProviderResult
	generate func(context.Context, agent.Turn) (agent.ProviderResult, error)
}

func (p *capturingProvider) Generate(ctx context.Context, turn agent.Turn) (agent.ProviderResult, error) {
	p.lastTurn = turn
	if p.generate != nil {
		return p.generate(ctx, turn)
	}
	if p.result.Text == "" && p.result.Usage.InputTokens == 0 && p.result.Usage.OutputTokens == 0 && len(p.result.HistoryDelta) == 0 && p.result.Outcome == "" && p.result.StopReason == "" {
		return agent.ProviderResult{Text: "ok"}, nil
	}
	return p.result, nil
}

type stubContextEngine struct {
	assembled ctxengine.AssembleResult
}

type filterableRuntime struct {
	allowed map[string]bool
}

func (r *filterableRuntime) ProcessTurn(context.Context, agent.Turn) (agent.TurnResult, error) {
	return agent.TurnResult{Text: "ok"}, nil
}

func (r *filterableRuntime) Filtered(allowed map[string]bool) agent.Runtime {
	return &filterableRuntime{allowed: allowed}
}

func (s *stubContextEngine) Ingest(context.Context, string, ctxengine.Message) (ctxengine.IngestResult, error) {
	return ctxengine.IngestResult{}, nil
}

func (s *stubContextEngine) Assemble(context.Context, string, int) (ctxengine.AssembleResult, error) {
	return s.assembled, nil
}

func (s *stubContextEngine) Compact(context.Context, string) (ctxengine.CompactResult, error) {
	return ctxengine.CompactResult{}, nil
}

func (s *stubContextEngine) Bootstrap(context.Context, string, []ctxengine.Message) (ctxengine.BootstrapResult, error) {
	return ctxengine.BootstrapResult{}, nil
}

func (s *stubContextEngine) Close() error { return nil }

func TestBuildInheritedACPTaskPayloadUsesRuntimeHintsAndContext(t *testing.T) {
	prevEngine := controlContextEngine
	controlContextEngine = &stubContextEngine{assembled: ctxengine.AssembleResult{
		Messages: []ctxengine.Message{{Role: "assistant", Content: "prior context"}},
	}}
	defer func() { controlContextEngine = prevEngine }()

	ctx := context.Background()
	ctx = agent.ContextWithSessionID(ctx, "session-acp")
	ctx = agent.ContextWithMemoryScope(ctx, agent.MemoryScopeContext{
		Scope:     string(state.AgentMemoryScopeProject),
		AgentID:   "worker",
		SessionID: "session-acp",
	})
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		ToolProfile:  "coding",
		EnabledTools: []string{"memory_search", "memory_store", "memory_search"},
	}}}

	ctx = contextWithACPTaskPayload(ctx, acppkg.TaskPayload{
		ContextMessages: encodeACPConversationMessages([]agent.ConversationMessage{{Role: "user", Content: "inherited parent history"}}),
		Task: &state.TaskSpec{
			TaskID:       "task-parent",
			GoalID:       "goal-1",
			PlanID:       "plan-1",
			CurrentRunID: "run-parent",
			Title:        "Parent task",
			Instructions: "own the parent",
			Meta:         map[string]any{"carry": "forward"},
		},
	})
	payload := buildInheritedACPTaskPayload(ctx, cfg, nil, nil, acppkg.TaskPayload{Instructions: "delegate this"})
	if payload.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("memory scope = %q, want %q", payload.MemoryScope, state.AgentMemoryScopeProject)
	}
	if payload.ToolProfile != "coding" {
		t.Fatalf("tool profile = %q, want coding", payload.ToolProfile)
	}
	if got, want := len(payload.EnabledTools), 2; got != want {
		t.Fatalf("enabled tools len = %d, want %d (%v)", got, want, payload.EnabledTools)
	}
	if payload.EnabledTools[0] != "memory_search" || payload.EnabledTools[1] != "memory_store" {
		t.Fatalf("enabled tools = %v, want ordered deduped config list", payload.EnabledTools)
	}
	if payload.ParentContext == nil {
		t.Fatal("parent context missing")
	}
	if payload.ParentContext.SessionID != "session-acp" || payload.ParentContext.AgentID != "worker" {
		t.Fatalf("parent context = %+v, want session-acp/worker", payload.ParentContext)
	}
	if got := decodeACPConversationMessages(payload.ContextMessages); len(got) != 1 || got[0].Role != "user" || got[0].Content != "inherited parent history" {
		t.Fatalf("context messages decoded = %+v", got)
	}
	if payload.Task == nil {
		t.Fatal("expected inherited child task")
	}
	if payload.Task.GoalID != "goal-1" || payload.Task.PlanID != "plan-1" {
		t.Fatalf("task goal/plan = %+v", payload.Task)
	}
	if payload.Task.ParentTaskID != "task-parent" {
		t.Fatalf("task parent_task_id = %q, want task-parent", payload.Task.ParentTaskID)
	}
	if payload.Task.SessionID != "session-acp" {
		t.Fatalf("task session_id = %q, want session-acp", payload.Task.SessionID)
	}
	if got := taskMetaString(payload.Task, "parent_run_id"); got != "run-parent" {
		t.Fatalf("task meta parent_run_id = %q, want run-parent", got)
	}
	if got := taskMetaString(payload.Task, "parent_session_id"); got != "session-acp" {
		t.Fatalf("task meta parent_session_id = %q, want session-acp", got)
	}
	if got := taskMetaString(payload.Task, "carry"); got != "forward" {
		t.Fatalf("task meta carry = %q, want forward", got)
	}
}

func TestHandleACPMessageReturnsACPErrorForMalformedTask(t *testing.T) {
	var replied string
	dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
		replied = text
		return nil
	}}
	msg := acppkg.Message{
		ACPType: "task",
		TaskID:  "task-bad",
		Payload: map[string]any{"instructions": []any{"bad"}},
	}
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agent.NewAgentRuntimeRegistry(nil), agent.NewAgentSessionRouter(), nil, nil, nil); err != nil {
		t.Fatalf("handleACPMessage malformed task: %v", err)
	}
	parsed, err := acppkg.Parse([]byte(replied))
	if err != nil {
		t.Fatalf("parse ACP result: %v", err)
	}
	if parsed.ACPType != "result" {
		t.Fatalf("acp_type = %q, want result", parsed.ACPType)
	}
	if got, _ := parsed.Payload["error"].(string); got == "" {
		t.Fatalf("expected result error payload, got %#v", parsed.Payload)
	}
}

func TestApplyACPTaskRuntimeConstraintsUsesRuntimeFilteredCapability(t *testing.T) {
	prevToolRegistry := controlToolRegistry
	controlToolRegistry = agent.NewToolRegistry()
	defer func() { controlToolRegistry = prevToolRegistry }()
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		EnabledTools: []string{"memory_search"},
	}}}
	rt := applyACPTaskRuntimeConstraints(context.Background(), &filterableRuntime{}, "worker", acppkg.TaskPayload{}, cfg, nil)
	filtered, ok := rt.(*filterableRuntime)
	if !ok {
		t.Fatalf("runtime type = %T, want *filterableRuntime", rt)
	}
	if filtered.allowed == nil || !filtered.allowed["memory_search"] {
		t.Fatalf("allowed tools = %v, want memory_search allowlist", filtered.allowed)
	}
}

func TestResolveAgentTurnToolSurfaceUsesSharedAllowlist(t *testing.T) {
	baseTools := agent.NewToolRegistry()
	baseTools.RegisterWithDef("memory_search", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemorySearchDef)
	baseTools.RegisterWithDef("memory_store", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemoryStoreDef)

	prevToolRegistry := controlToolRegistry
	controlToolRegistry = baseTools
	defer func() { controlToolRegistry = prevToolRegistry }()

	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		EnabledTools: []string{"memory_search", "memory_search"},
	}}}

	rt, exec, defs := resolveAgentTurnToolSurface(context.Background(), cfg, nil, "session-worker", "worker", &filterableRuntime{}, baseTools, turnToolConstraints{})
	filteredRuntime, ok := rt.(*filterableRuntime)
	if !ok {
		t.Fatalf("runtime type = %T, want *filterableRuntime", rt)
	}
	if filteredRuntime.allowed == nil || !filteredRuntime.allowed["memory_search"] || filteredRuntime.allowed["memory_store"] {
		t.Fatalf("runtime allowed tools = %v", filteredRuntime.allowed)
	}
	if got, want := len(defs), 1; got != want {
		t.Fatalf("definitions len = %d, want %d (%+v)", got, want, defs)
	}
	if defs[0].Name != "memory_search" {
		t.Fatalf("definitions = %+v, want memory_search only", defs)
	}
	if execDefs := agent.ToolDefinitions(exec); len(execDefs) != 1 || execDefs[0].Name != "memory_search" {
		t.Fatalf("executor definitions = %+v, want memory_search only", execDefs)
	}
}

func TestResolveAgentTurnToolSurfaceIntersectsPerTurnConstraints(t *testing.T) {
	baseTools := agent.NewToolRegistry()
	baseTools.RegisterWithDef("memory_search", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemorySearchDef)
	baseTools.RegisterWithDef("memory_store", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemoryStoreDef)

	prevToolRegistry := controlToolRegistry
	controlToolRegistry = baseTools
	defer func() { controlToolRegistry = prevToolRegistry }()

	cfg := state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		EnabledTools: []string{"memory_search", "memory_store"},
	}}}

	rt, exec, defs := resolveAgentTurnToolSurface(
		context.Background(),
		cfg,
		nil,
		"session-worker",
		"worker",
		&filterableRuntime{},
		baseTools,
		turnToolConstraints{EnabledTools: []string{"memory_store"}},
	)
	filteredRuntime, ok := rt.(*filterableRuntime)
	if !ok {
		t.Fatalf("runtime type = %T, want *filterableRuntime", rt)
	}
	if filteredRuntime.allowed == nil || filteredRuntime.allowed["memory_search"] || !filteredRuntime.allowed["memory_store"] {
		t.Fatalf("runtime allowed tools = %v", filteredRuntime.allowed)
	}
	if got, want := len(defs), 1; got != want {
		t.Fatalf("definitions len = %d, want %d (%+v)", got, want, defs)
	}
	if defs[0].Name != "memory_store" {
		t.Fatalf("definitions = %+v, want memory_store only", defs)
	}
	if execDefs := agent.ToolDefinitions(exec); len(execDefs) != 1 || execDefs[0].Name != "memory_store" {
		t.Fatalf("executor definitions = %+v, want memory_store only", execDefs)
	}
}

func TestHandleACPMessageAppliesInheritedRuntimeHints(t *testing.T) {
	provider := &capturingProvider{result: agent.ProviderResult{
		Text:  "ok",
		Usage: agent.ProviderUsage{InputTokens: 3, OutputTokens: 2},
	}}
	tools := agent.NewToolRegistry()
	tools.RegisterWithDef("memory_search", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemorySearchDef)
	tools.RegisterWithDef("memory_store", func(context.Context, map[string]any) (string, error) { return "", nil }, toolbuiltin.MemoryStoreDef)
	runtime, err := agent.NewProviderRuntime(provider, tools)
	if err != nil {
		t.Fatalf("new provider runtime: %v", err)
	}
	agentReg := agent.NewAgentRuntimeRegistry(runtime)
	sessionRouter := agent.NewAgentSessionRouter()
	sessionRouter.Assign("peer-pubkey", "worker")

	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	prevSessionStore := controlSessionStore
	prevToolRegistry := controlToolRegistry
	prevRuntimeConfig := controlRuntimeConfig
	controlSessionStore = ss
	controlToolRegistry = tools
	controlRuntimeConfig = newRuntimeConfigStore(state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		EnabledTools: []string{"memory_search", "memory_store"},
	}}})
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "author")
	transcriptRepo := state.NewTranscriptRepository(store, "author")
	defer func() {
		controlSessionStore = prevSessionStore
		controlToolRegistry = prevToolRegistry
		controlRuntimeConfig = prevRuntimeConfig
	}()

	msg := acppkg.NewTask("task-1", testACPSenderPubKey, acppkg.TaskPayload{
		Instructions: "handle this",
		EnabledTools: []string{"memory_search"},
		MemoryScope:  state.AgentMemoryScopeProject,
		ContextMessages: encodeACPConversationMessages([]agent.ConversationMessage{{
			Role:    "assistant",
			Content: "existing parent transcript",
		}}),
	})
	var replied string
	dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
		replied = text
		return nil
	}}
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, docsRepo, transcriptRepo); err != nil {
		t.Fatalf("handleACPMessage: %v", err)
	}

	parsed, err := acppkg.Parse([]byte(replied))
	if err != nil {
		t.Fatalf("parse ACP result: %v", err)
	}
	resultPayload, err := acppkg.DecodeResultPayload(parsed.Payload)
	if err != nil {
		t.Fatalf("decode ACP result payload: %v", err)
	}
	if strings.TrimSpace(resultPayload.Error) != "" {
		t.Fatalf("unexpected ACP worker error: %q", resultPayload.Error)
	}
	if strings.TrimSpace(resultPayload.Text) != "ok" {
		t.Fatalf("result text = %q, want ok", resultPayload.Text)
	}

	entries, err := transcriptRepo.ListSession(context.Background(), "acp:peer-pubkey", 10)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 transcript entries, got %d", len(entries))
	}
	if got := entries[0].Meta["acp_task_id"]; got != "task-1" {
		t.Fatalf("seed entry acp_task_id = %#v", got)
	}
	if got := entries[0].Meta["message_kind"]; got != "context_seed" {
		t.Fatalf("seed entry message_kind = %#v", got)
	}
	turnResult, ok := entries[1].Meta["turn_result"].(map[string]any)
	if !ok {
		t.Fatalf("terminal ACP transcript entry missing turn_result: %#v", entries[1].Meta)
	}
	if got := turnResult["outcome"]; got != string(agent.TurnOutcomeCompleted) {
		t.Fatalf("terminal outcome = %#v", got)
	}
}

func TestACPWorkerTaskDocs_PersistTaskRunAndResultLinkage(t *testing.T) {
	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	prevSessionStore := controlSessionStore
	controlSessionStore = ss
	defer func() { controlSessionStore = prevSessionStore }()

	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "author")
	startedAt := time.Unix(1_700_000_000, 0)
	payload := acppkg.TaskPayload{
		Instructions: "handle this",
		Task: &state.TaskSpec{
			TaskID:       "task-1",
			GoalID:       "goal-1",
			ParentTaskID: "task-root",
			PlanID:       "plan-1",
			Title:        "Worker task",
			Instructions: "handle this",
			Meta:         map[string]any{"parent_run_id": "run-root"},
		},
	}
	workerTask, workerRun, cleanup, err := beginACPWorkerTask(context.Background(), docsRepo, "acp:peer-pubkey", "peer-pubkey", "worker", "task-1", payload, startedAt)
	if err != nil {
		t.Fatalf("beginACPWorkerTask: %v", err)
	}
	defer cleanup()
	resultRef := state.TaskResultRef{Kind: "transcript_entry", ID: "entry-final"}
	turnResult := &agent.TurnResultMetadata{Outcome: agent.TurnOutcomeCompleted, StopReason: agent.TurnStopReasonModelText, Usage: agent.TurnUsage{InputTokens: 3, OutputTokens: 2}}
	if err := finishACPWorkerTaskDocs(context.Background(), docsRepo, "acp:peer-pubkey", workerTask, workerRun, resultRef, turnResult, nil, []string{"entry-seed", "entry-final"}); err != nil {
		t.Fatalf("finishACPWorkerTaskDocs: %v", err)
	}

	taskDoc, err := docsRepo.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("get task doc: %v", err)
	}
	if taskDoc.ParentTaskID != "task-root" || taskDoc.CurrentRunID != "" || taskDoc.LastRunID == "" {
		t.Fatalf("task doc linkage = %+v", taskDoc)
	}
	if taskDoc.Status != state.TaskStatusCompleted {
		t.Fatalf("task status = %q, want completed", taskDoc.Status)
	}
	if got := taskMetaString(&taskDoc, "parent_run_id"); got != "run-root" {
		t.Fatalf("task meta parent_run_id = %q, want run-root", got)
	}
	if got := taskMetaString(&taskDoc, "result_history_entry_id"); got != "entry-final" {
		t.Fatalf("task meta result_history_entry_id = %q, want entry-final", got)
	}
	runs, err := docsRepo.ListTaskRuns(context.Background(), "task-1", 10)
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 task run, got %d", len(runs))
	}
	if runs[0].ParentRunID != "run-root" || runs[0].Status != state.TaskRunStatusCompleted {
		t.Fatalf("task run = %+v", runs[0])
	}
	if runs[0].Result != resultRef {
		t.Fatalf("task run result = %+v, want %+v", runs[0].Result, resultRef)
	}
	entry, ok := ss.Get("acp:peer-pubkey")
	if !ok {
		t.Fatal("expected ACP worker session entry")
	}
	if entry.ActiveTaskID != "" || entry.ActiveRunID != "" {
		t.Fatalf("expected active task cleared, got %+v", entry)
	}
	if entry.LastCompletedTaskID != "task-1" || entry.LastCompletedRunID != runs[0].RunID {
		t.Fatalf("session completion linkage = %+v", entry)
	}
	if entry.LastTaskResult != resultRef {
		t.Fatalf("session last task result = %+v, want %+v", entry.LastTaskResult, resultRef)
	}
}

func setupACPWorkerTestRuntime(t *testing.T, provider *capturingProvider) (*agent.AgentRuntimeRegistry, *agent.AgentSessionRouter, *agent.ToolRegistry, *state.SessionStore, *state.DocsRepository, *state.TranscriptRepository, func()) {
	t.Helper()
	tools := agent.NewToolRegistry()
	runtime, err := agent.NewProviderRuntime(provider, tools)
	if err != nil {
		t.Fatalf("new provider runtime: %v", err)
	}
	agentReg := agent.NewAgentRuntimeRegistry(runtime)
	sessionRouter := agent.NewAgentSessionRouter()
	sessionRouter.Assign("peer-pubkey", "worker")
	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "author")
	transcriptRepo := state.NewTranscriptRepository(store, "author")
	prevSessionStore := controlSessionStore
	prevToolRegistry := controlToolRegistry
	prevRuntimeConfig := controlRuntimeConfig
	controlSessionStore = ss
	controlToolRegistry = tools
	controlRuntimeConfig = newRuntimeConfigStore(state.ConfigDoc{Agents: []state.AgentConfig{{
		ID:           "worker",
		EnabledTools: []string{"memory_search", "memory_store"},
	}}})
	cleanup := func() {
		controlSessionStore = prevSessionStore
		controlToolRegistry = prevToolRegistry
		controlRuntimeConfig = prevRuntimeConfig
	}
	return agentReg, sessionRouter, tools, ss, docsRepo, transcriptRepo, cleanup
}

func readACPWorkerSessionDoc(t *testing.T, docsRepo *state.DocsRepository, sessionID string) state.SessionDoc {
	t.Helper()
	doc, err := docsRepo.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("get session %s: %v", sessionID, err)
	}
	return doc
}

func requireACPWorkerTaskActive(t *testing.T, docsRepo *state.DocsRepository, sessionID, taskID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		doc, err := docsRepo.GetSession(context.Background(), sessionID)
		if err == nil {
			if active, _ := doc.Meta["active_turn"].(bool); active {
				taskMeta, ok := doc.Meta[acpWorkerTaskMetaKey].(map[string]any)
				if !ok {
					t.Fatalf("expected %s metadata, got %#v", acpWorkerTaskMetaKey, doc.Meta)
				}
				if got, _ := taskMeta["task_id"].(string); got != taskID {
					t.Fatalf("task_id = %q, want %q (%#v)", got, taskID, taskMeta)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for active ACP worker task state")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requireACPWorkerTaskCleared(t *testing.T, docsRepo *state.DocsRepository, sessionID string) {
	t.Helper()
	doc := readACPWorkerSessionDoc(t, docsRepo, sessionID)
	if active, _ := doc.Meta["active_turn"].(bool); active {
		t.Fatalf("expected active_turn cleared, got %#v", doc.Meta)
	}
	if _, ok := doc.Meta[acpWorkerTaskMetaKey]; ok {
		t.Fatalf("expected %s cleared, got %#v", acpWorkerTaskMetaKey, doc.Meta[acpWorkerTaskMetaKey])
	}
}

func TestHandleACPMessage_CleansUpWorkerTaskState_OnSuccess(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	provider := &capturingProvider{
		generate: func(ctx context.Context, turn agent.Turn) (agent.ProviderResult, error) {
			once.Do(func() { close(entered) })
			select {
			case <-release:
				return agent.ProviderResult{Text: "ok"}, nil
			case <-ctx.Done():
				return agent.ProviderResult{}, ctx.Err()
			}
		},
	}
	agentReg, sessionRouter, tools, _, docsRepo, transcriptRepo, cleanup := setupACPWorkerTestRuntime(t, provider)
	defer cleanup()

	msg := acppkg.NewTask("task-clean-success", testACPSenderPubKey, acppkg.TaskPayload{Instructions: "handle this"})
	done := make(chan error, 1)
	go func() {
		dm := nostruntime.InboundDM{Reply: func(context.Context, string) error { return nil }}
		done <- handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, docsRepo, transcriptRepo)
	}()
	<-entered
	requireACPWorkerTaskActive(t, docsRepo, "acp:peer-pubkey", "task-clean-success")
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("handleACPMessage: %v", err)
	}
	requireACPWorkerTaskCleared(t, docsRepo, "acp:peer-pubkey")
}

func TestHandleACPMessage_CleansUpWorkerTaskState_OnError(t *testing.T) {
	provider := &capturingProvider{
		generate: func(context.Context, agent.Turn) (agent.ProviderResult, error) {
			return agent.ProviderResult{}, fmt.Errorf("worker failed")
		},
	}
	agentReg, sessionRouter, tools, _, docsRepo, transcriptRepo, cleanup := setupACPWorkerTestRuntime(t, provider)
	defer cleanup()

	var replied string
	msg := acppkg.NewTask("task-clean-error", testACPSenderPubKey, acppkg.TaskPayload{Instructions: "handle this"})
	dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
		replied = text
		return nil
	}}
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, docsRepo, transcriptRepo); err != nil {
		t.Fatalf("handleACPMessage: %v", err)
	}
	requireACPWorkerTaskCleared(t, docsRepo, "acp:peer-pubkey")
	if !strings.Contains(replied, "worker failed") {
		t.Fatalf("expected worker error in reply, got %q", replied)
	}
}

func TestHandleACPMessage_CleansUpWorkerTaskState_OnCancel(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	provider := &capturingProvider{
		generate: func(ctx context.Context, turn agent.Turn) (agent.ProviderResult, error) {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return agent.ProviderResult{}, ctx.Err()
		},
	}
	agentReg, sessionRouter, tools, _, docsRepo, transcriptRepo, cleanup := setupACPWorkerTestRuntime(t, provider)
	defer cleanup()

	var replied string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	msg := acppkg.NewTask("task-clean-cancel", testACPSenderPubKey, acppkg.TaskPayload{Instructions: "handle this"})
	go func() {
		dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
			replied = text
			return nil
		}}
		done <- handleACPMessage(ctx, msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, docsRepo, transcriptRepo)
	}()
	<-entered
	requireACPWorkerTaskActive(t, docsRepo, "acp:peer-pubkey", "task-clean-cancel")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("handleACPMessage: %v", err)
	}
	requireACPWorkerTaskCleared(t, docsRepo, "acp:peer-pubkey")
	if !strings.Contains(replied, "context canceled") {
		t.Fatalf("expected cancellation in reply, got %q", replied)
	}
}

func TestHandleACPMessage_CleansUpWorkerTaskState_OnTimeout(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	provider := &capturingProvider{
		generate: func(ctx context.Context, turn agent.Turn) (agent.ProviderResult, error) {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return agent.ProviderResult{}, ctx.Err()
		},
	}
	agentReg, sessionRouter, tools, _, docsRepo, transcriptRepo, cleanup := setupACPWorkerTestRuntime(t, provider)
	defer cleanup()

	var replied string
	msg := acppkg.NewTask("task-clean-timeout", testACPSenderPubKey, acppkg.TaskPayload{
		Instructions: "handle this",
		TimeoutMS:    25,
	})
	done := make(chan error, 1)
	go func() {
		dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
			replied = text
			return nil
		}}
		done <- handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, docsRepo, transcriptRepo)
	}()
	<-entered
	requireACPWorkerTaskActive(t, docsRepo, "acp:peer-pubkey", "task-clean-timeout")
	if err := <-done; err != nil {
		t.Fatalf("handleACPMessage: %v", err)
	}
	requireACPWorkerTaskCleared(t, docsRepo, "acp:peer-pubkey")
	if !strings.Contains(replied, "deadline exceeded") {
		t.Fatalf("expected timeout in reply, got %q", replied)
	}
}

func TestHandleACPMessage_CleansUpWorkerTaskState_OnReplyFailure(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	provider := &capturingProvider{
		generate: func(ctx context.Context, turn agent.Turn) (agent.ProviderResult, error) {
			once.Do(func() { close(entered) })
			select {
			case <-release:
				return agent.ProviderResult{Text: "ok"}, nil
			case <-ctx.Done():
				return agent.ProviderResult{}, ctx.Err()
			}
		},
	}
	agentReg, sessionRouter, tools, _, docsRepo, transcriptRepo, cleanup := setupACPWorkerTestRuntime(t, provider)
	defer cleanup()

	msg := acppkg.NewTask("task-clean-reply-fail", testACPSenderPubKey, acppkg.TaskPayload{Instructions: "handle this"})
	done := make(chan error, 1)
	go func() {
		dm := nostruntime.InboundDM{Reply: func(context.Context, string) error { return fmt.Errorf("reply failed") }}
		done <- handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, docsRepo, transcriptRepo)
	}()
	<-entered
	requireACPWorkerTaskActive(t, docsRepo, "acp:peer-pubkey", "task-clean-reply-fail")
	close(release)
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "reply failed") {
		t.Fatalf("expected reply failure, got %v", err)
	}
	requireACPWorkerTaskCleared(t, docsRepo, "acp:peer-pubkey")
}

func TestHandleACPMessage_CleansUpWorkerTaskState_OnPanic(t *testing.T) {
	provider := &capturingProvider{
		generate: func(context.Context, agent.Turn) (agent.ProviderResult, error) {
			panic("boom")
		},
	}
	agentReg, sessionRouter, tools, _, docsRepo, transcriptRepo, cleanup := setupACPWorkerTestRuntime(t, provider)
	defer cleanup()

	var replied string
	msg := acppkg.NewTask("task-clean-panic", testACPSenderPubKey, acppkg.TaskPayload{Instructions: "handle this"})
	dm := nostruntime.InboundDM{Reply: func(_ context.Context, text string) error {
		replied = text
		return nil
	}}
	if err := handleACPMessage(context.Background(), msg, "peer-pubkey", dm, agentReg, sessionRouter, tools, docsRepo, transcriptRepo); err != nil {
		t.Fatalf("handleACPMessage: %v", err)
	}
	requireACPWorkerTaskCleared(t, docsRepo, "acp:peer-pubkey")
	if !strings.Contains(replied, "acp worker panic: boom") {
		t.Fatalf("expected panic in reply, got %q", replied)
	}
}
