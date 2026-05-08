package tasks

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"metiq/internal/nostr/events"
	"metiq/internal/store/state"
)

type memoryWorkflowStore struct {
	definitions []*WorkflowDefinition
	runs        []*WorkflowRun
	savedDefs   []*WorkflowDefinition
	savedRuns   []*WorkflowRun
}

func (s *memoryWorkflowStore) LoadDefinitions(context.Context) ([]*WorkflowDefinition, error) {
	return s.definitions, nil
}

func (s *memoryWorkflowStore) LoadRuns(context.Context) ([]*WorkflowRun, error) {
	return s.runs, nil
}

func (s *memoryWorkflowStore) SaveDefinition(_ context.Context, def *WorkflowDefinition) error {
	s.savedDefs = append(s.savedDefs, def)
	return nil
}

func (s *memoryWorkflowStore) SaveRun(_ context.Context, run *WorkflowRun) error {
	s.savedRuns = append(s.savedRuns, run)
	return nil
}

func TestWorkflowOrchestratorUsesConfiguredStore(t *testing.T) {
	store := &memoryWorkflowStore{
		definitions: []*WorkflowDefinition{{
			Version: 1,
			ID:      "wf-loaded",
			Name:    "Loaded",
			Steps:   []StepDefinition{{ID: "approve", Name: "Approve", Type: StepTypeApproval}},
		}},
		runs: []*WorkflowRun{{
			Version:      1,
			RunID:        "run-loaded",
			WorkflowID:   "wf-loaded",
			WorkflowName: "Loaded",
			Status:       WorkflowStatusRunning,
			Steps:        []StepRun{{StepID: "approve", StepName: "Approve", Type: StepTypeApproval, Status: StepStatusBlocked}},
		}},
	}

	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Store: store})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	if _, err := o.GetDefinition(context.Background(), "wf-loaded"); err != nil {
		t.Fatalf("GetDefinition loaded from store: %v", err)
	}
	if _, err := o.GetRun(context.Background(), "run-loaded"); err != nil {
		t.Fatalf("GetRun loaded from store: %v", err)
	}

	if err := o.RegisterDefinition(context.Background(), WorkflowDefinition{
		ID:    "wf-new",
		Name:  "New",
		Steps: []StepDefinition{{ID: "s1", Name: "S1", Type: StepTypeApproval}},
	}); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if len(store.savedDefs) != 1 || store.savedDefs[0].ID != "wf-new" {
		t.Fatalf("definition not saved through configured store: %+v", store.savedDefs)
	}
}

func TestFSWorkflowStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewFSWorkflowStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSWorkflowStore: %v", err)
	}
	def := &WorkflowDefinition{
		Version: 1,
		ID:      "wf-fs",
		Name:    "Filesystem",
		Steps:   []StepDefinition{{ID: "s1", Name: "S1", Type: StepTypeApproval}},
	}
	run := &WorkflowRun{
		Version:      1,
		RunID:        "run-fs",
		WorkflowID:   def.ID,
		WorkflowName: def.Name,
		Status:       WorkflowStatusRunning,
		Steps:        []StepRun{{StepID: "s1", StepName: "S1", Type: StepTypeApproval, Status: StepStatusBlocked}},
		UpdatedAt:    123,
	}
	if err := store.SaveDefinition(ctx, def); err != nil {
		t.Fatalf("SaveDefinition: %v", err)
	}
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	defs, err := store.LoadDefinitions(ctx)
	if err != nil {
		t.Fatalf("LoadDefinitions: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != def.ID || defs[0].Steps[0].ID != "s1" {
		t.Fatalf("unexpected definitions: %+v", defs)
	}
	runs, err := store.LoadRuns(ctx)
	if err != nil {
		t.Fatalf("LoadRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != run.RunID || runs[0].Steps[0].Status != StepStatusBlocked {
		t.Fatalf("unexpected runs: %+v", runs)
	}
}

func TestDocsWorkflowStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := state.NewDocsRepository(newWorkflowStateStore(), "author-pub")
	store := NewDocsWorkflowStore(repo)

	def := &WorkflowDefinition{
		Version: 1,
		ID:      "wf-docs",
		Name:    "Docs",
		Steps:   []StepDefinition{{ID: "s1", Name: "S1", Type: StepTypeApproval}},
	}
	run := &WorkflowRun{
		Version:      1,
		RunID:        "run-docs",
		WorkflowID:   def.ID,
		WorkflowName: def.Name,
		Status:       WorkflowStatusRunning,
		Steps:        []StepRun{{StepID: "s1", StepName: "S1", Type: StepTypeApproval, Status: StepStatusBlocked}},
		UpdatedAt:    456,
	}
	if err := store.SaveDefinition(ctx, def); err != nil {
		t.Fatalf("SaveDefinition: %v", err)
	}
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	defs, err := store.LoadDefinitions(ctx)
	if err != nil {
		t.Fatalf("LoadDefinitions: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != def.ID || defs[0].Name != def.Name {
		t.Fatalf("unexpected docs definitions: %+v", defs)
	}
	runs, err := store.LoadRuns(ctx)
	if err != nil {
		t.Fatalf("LoadRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != run.RunID || runs[0].WorkflowID != def.ID {
		t.Fatalf("unexpected docs runs: %+v", runs)
	}
}

func TestWorkflowRecoveryReconcilesCompletedChildRunWithoutRerun(t *testing.T) {
	ctx := context.Background()
	repo := state.NewDocsRepository(newWorkflowStateStore(), "author-pub")
	service := NewService(NewDocsStore(repo))
	workflowStore := NewDocsWorkflowStore(repo)

	childTask, childRun, err := service.StartWorkerRun(ctx, state.TaskSpec{
		TaskID:       "child-completed-task",
		Title:        "Completed child",
		Instructions: "already done",
	}, TaskSourceWorkflow, "run-recover-completed:child", "child-completed-run", "workflow step", "test", "started", "")
	if err != nil {
		t.Fatalf("StartWorkerRun: %v", err)
	}
	_, childRun, err = service.FinishWorkerRun(ctx, childTask.TaskID, childRun.RunID, state.TaskResultRef{Kind: "test", ID: "done"}, state.TaskUsage{TotalTokens: 7}, "test", nil, nil)
	if err != nil {
		t.Fatalf("FinishWorkerRun: %v", err)
	}

	def := &WorkflowDefinition{
		Version: 1,
		ID:      "wf-recover-completed",
		Name:    "Recover completed",
		Steps:   []StepDefinition{{ID: "child", Name: "Child", Type: StepTypeAgentTurn}},
	}
	run := &WorkflowRun{
		Version:      1,
		RunID:        "run-recover-completed",
		WorkflowID:   def.ID,
		WorkflowName: def.Name,
		Status:       WorkflowStatusRunning,
		Steps: []StepRun{{
			StepID:   "child",
			StepName: "Child",
			Type:     StepTypeAgentTurn,
			Status:   StepStatusRunning,
			TaskID:   childTask.TaskID,
			RunID:    childRun.RunID,
		}},
	}
	if err := workflowStore.SaveDefinition(ctx, def); err != nil {
		t.Fatalf("SaveDefinition: %v", err)
	}
	if err := workflowStore.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	exec := &workflowTestExecutor{}
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Store: workflowStore, Ledger: service.Ledger(), Executor: exec})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	if err := o.RecoverNonTerminalRuns(ctx); err != nil {
		t.Fatalf("RecoverNonTerminalRuns: %v", err)
	}
	recovered, err := o.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if recovered.Status != WorkflowStatusCompleted || recovered.Steps[0].Status != StepStatusCompleted {
		t.Fatalf("recovered status = workflow %s step %s, want completed/completed", recovered.Status, recovered.Steps[0].Status)
	}
	if got := recovered.Steps[0].Output["run_id"]; got != childRun.RunID {
		t.Fatalf("reconciled output run_id = %v, want %s", got, childRun.RunID)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.calls) != 0 {
		t.Fatalf("completed child step was rerun: calls=%v", exec.calls)
	}
}

func TestWorkflowRecoveryResumesPendingStepFromDocsStore(t *testing.T) {
	ctx := context.Background()
	repo := state.NewDocsRepository(newWorkflowStateStore(), "author-pub")
	workflowStore := NewDocsWorkflowStore(repo)
	exec := &workflowTestExecutor{}
	def := &WorkflowDefinition{
		Version: 1,
		ID:      "wf-recover-pending",
		Name:    "Recover pending",
		Steps:   []StepDefinition{{ID: "work", Name: "Work", Type: StepTypeAgentTurn}},
	}
	run := &WorkflowRun{
		Version:      1,
		RunID:        "run-recover-pending",
		WorkflowID:   def.ID,
		WorkflowName: def.Name,
		Status:       WorkflowStatusRunning,
		Steps:        []StepRun{{StepID: "work", StepName: "Work", Type: StepTypeAgentTurn, Status: StepStatusPending}},
	}
	if err := workflowStore.SaveDefinition(ctx, def); err != nil {
		t.Fatalf("SaveDefinition: %v", err)
	}
	if err := workflowStore.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	o, err := NewWorkflowOrchestrator(OrchestratorConfig{Store: workflowStore, Executor: exec})
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	if err := o.RecoverNonTerminalRuns(ctx); err != nil {
		t.Fatalf("RecoverNonTerminalRuns: %v", err)
	}
	waitForWorkflowTestCondition(t, time.Second, func() bool {
		recovered, err := o.GetRun(ctx, run.RunID)
		return err == nil && recovered.Status == WorkflowStatusCompleted && recovered.Steps[0].Status == StepStatusCompleted
	})
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.calls) != 1 || exec.calls[0] != "work" {
		t.Fatalf("recovery calls = %v, want [work]", exec.calls)
	}
}

func waitForWorkflowTestCondition(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

type workflowStateStore struct {
	mu      sync.Mutex
	nowUnix int64
	repl    map[state.Address]state.Event
}

func newWorkflowStateStore() *workflowStateStore {
	return &workflowStateStore{nowUnix: time.Now().Unix(), repl: map[state.Address]state.Event{}}
}

func (s *workflowStateStore) GetLatestReplaceable(_ context.Context, addr state.Address) (state.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evt, ok := s.repl[addr]
	if !ok {
		return state.Event{}, state.ErrNotFound
	}
	return evt, nil
}

func (s *workflowStateStore) PutReplaceable(_ context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nowUnix++
	tags := append([][]string{{"d", addr.DTag}}, extraTags...)
	evt := state.Event{
		ID:        fmt.Sprintf("evt-%d", s.nowUnix),
		PubKey:    addr.PubKey,
		Kind:      addr.Kind,
		CreatedAt: s.nowUnix,
		Tags:      tags,
		Content:   content,
	}
	s.repl[addr] = evt
	return evt, nil
}

func (s *workflowStateStore) PutAppend(_ context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
	return s.PutReplaceable(context.Background(), addr, content, extraTags)
}

func (s *workflowStateStore) ListByTag(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]state.Event, error) {
	page, err := s.ListByTagPage(ctx, kind, tagName, tagValue, limit, nil)
	return page.Events, err
}

func (s *workflowStateStore) ListByTagForAuthor(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]state.Event, error) {
	page, err := s.ListByTagForAuthorPage(ctx, kind, authorPubKey, tagName, tagValue, limit, nil)
	return page.Events, err
}

func (s *workflowStateStore) ListByTagPage(_ context.Context, kind events.Kind, tagName, tagValue string, limit int, _ *state.EventPageCursor) (state.EventPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return state.EventPage{Events: s.matchingEvents(kind, "", tagName, tagValue, limit)}, nil
}

func (s *workflowStateStore) ListByTagForAuthorPage(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, _ *state.EventPageCursor) (state.EventPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return state.EventPage{Events: s.matchingEvents(kind, authorPubKey, tagName, tagValue, limit)}, nil
}

func (s *workflowStateStore) matchingEvents(kind events.Kind, authorPubKey, tagName, tagValue string, limit int) []state.Event {
	if limit <= 0 {
		limit = 100
	}
	out := make([]state.Event, 0, len(s.repl))
	for _, evt := range s.repl {
		if evt.Kind != kind {
			continue
		}
		if authorPubKey != "" && evt.PubKey != authorPubKey {
			continue
		}
		if !workflowHasTagValue(evt.Tags, tagName, tagValue) {
			continue
		}
		out = append(out, evt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func workflowHasTagValue(tags [][]string, key, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key && tag[1] == value {
			return true
		}
	}
	return false
}
