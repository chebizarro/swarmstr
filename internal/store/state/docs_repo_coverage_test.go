package state

import (
	"context"
	"encoding/json"
	"testing"
)

// ─── Session CRUD ─────────────────────────────────────────────────────────────

func TestDocsRepo_GetSession(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	// Put a session
	_, err := repo.PutSession(ctx, "sess-1", SessionDoc{Version: 1, SessionID: "sess-1", PeerPubKey: "peer1"})
	if err != nil {
		t.Fatal(err)
	}

	// Get it back
	doc, err := repo.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PeerPubKey != "peer1" {
		t.Errorf("PeerPubKey: %q", doc.PeerPubKey)
	}
}

func TestDocsRepo_GetSession_NotFound(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	_, err := repo.GetSession(context.Background(), "missing")
	if err == nil {
		t.Error("expected error")
	}
}

// ─── GetListWithEvent ─────────────────────────────────────────────────────────

func TestDocsRepo_GetListWithEvent(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	_, err := repo.PutList(ctx, "mylist", ListDoc{Version: 1, Name: "mylist", Items: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}

	doc, evt, err := repo.GetListWithEvent(ctx, "mylist")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "mylist" || len(doc.Items) != 2 {
		t.Errorf("doc: %+v", doc)
	}
	if evt.ID == "" {
		t.Error("event should have an ID")
	}
}

// ─── Agent CRUD ───────────────────────────────────────────────────────────────

func TestDocsRepo_PutGetAgent(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	_, err := repo.PutAgent(ctx, "agent-1", AgentDoc{Version: 1, AgentID: "agent-1", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}

	doc, err := repo.GetAgent(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "Alice" {
		t.Errorf("Name: %q", doc.Name)
	}
}

func TestDocsRepo_ListAgents(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	repo.PutAgent(ctx, "a1", AgentDoc{Version: 1, AgentID: "a1", Name: "Alice"})
	repo.PutAgent(ctx, "a2", AgentDoc{Version: 1, AgentID: "a2", Name: "Bob"})

	agents, err := repo.ListAgents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

// ─── AgentFile CRUD ──────────────────────────────────────────────────────────

func TestDocsRepo_PutGetAgentFile(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	_, err := repo.PutAgentFile(ctx, "agent-1", "readme.md", AgentFileDoc{Version: 1, AgentID: "agent-1", Name: "readme.md", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	doc, err := repo.GetAgentFile(ctx, "agent-1", "readme.md")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Content != "hello" {
		t.Errorf("Content: %q", doc.Content)
	}
}

func TestDocsRepo_ListAgentFiles(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	repo.PutAgentFile(ctx, "agent-1", "a.txt", AgentFileDoc{Version: 1, AgentID: "agent-1", Name: "a.txt", Content: "aaa"})
	repo.PutAgentFile(ctx, "agent-1", "b.txt", AgentFileDoc{Version: 1, AgentID: "agent-1", Name: "b.txt", Content: "bbb"})

	files, err := repo.ListAgentFiles(ctx, "agent-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

// ─── WorkflowJournal CRUD ────────────────────────────────────────────────────

func TestDocsRepo_PutGetWorkflowJournal(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	doc := WorkflowJournalDoc{
		Version: 1,
		TaskID:  "task-1",
		RunID:   "run-1",
		NextSeq: 5,
	}
	_, err := repo.PutWorkflowJournal(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetWorkflowJournal(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "task-1" || got.NextSeq != 5 {
		t.Errorf("doc: %+v", got)
	}
}

func TestDocsRepo_PutWorkflowJournal_EmptyRunID(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	_, err := repo.PutWorkflowJournal(context.Background(), WorkflowJournalDoc{})
	if err == nil {
		t.Error("expected error for empty run_id")
	}
}

func TestDocsRepo_ListWorkflowJournals(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	repo.PutWorkflowJournal(ctx, WorkflowJournalDoc{Version: 1, TaskID: "t1", RunID: "r1", UpdatedAt: 100})
	repo.PutWorkflowJournal(ctx, WorkflowJournalDoc{Version: 1, TaskID: "t1", RunID: "r2", UpdatedAt: 200})

	journals, err := repo.ListWorkflowJournals(ctx, "t1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(journals) != 2 {
		t.Errorf("expected 2 journals, got %d", len(journals))
	}
}

// ─── CronJobs CRUD ───────────────────────────────────────────────────────────

func TestDocsRepo_PutGetCronJobs(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	raw := json.RawMessage(`[{"name":"job1"}]`)
	_, err := repo.PutCronJobs(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetCronJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
}

func TestDocsRepo_GetCronJobs_NotFound(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	got, err := repo.GetCronJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Not found returns nil, no error
	if got != nil {
		t.Errorf("expected nil, got %s", string(got))
	}
}

// ─── Watches CRUD ─────────────────────────────────────────────────────────────

func TestDocsRepo_PutGetWatches(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	raw := json.RawMessage(`{"watches":[]}`)
	_, err := repo.PutWatches(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetWatches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
}

// ─── ListFeedbackByRun ──────────────────────────────────────────────────────

func TestDocsRepo_ListFeedbackByRun(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	fb := FeedbackRecord{
		Version:    1,
		FeedbackID: "fb-1",
		RunID:      "run-1",
		Source:     FeedbackSourceAgent,
		Severity:   FeedbackSeverityInfo,
		Category:   FeedbackCategoryGeneral,
		Summary:    "good",
		CreatedAt:  100,
	}
	_, err := repo.PutFeedback(ctx, fb)
	if err != nil {
		t.Fatal(err)
	}

	list, err := repo.ListFeedbackByRun(ctx, "run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
}

// ─── ListFeedbackBySeverity / ByStep ─────────────────────────────────────────

func TestDocsRepo_ListFeedbackBySeverity(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	fb := FeedbackRecord{
		Version:    1,
		FeedbackID: "fb-sev",
		RunID:      "r",
		Source:     FeedbackSourceAgent,
		Severity:   FeedbackSeverityWarning,
		Category:   FeedbackCategoryGeneral,
		Summary:    "warn",
		CreatedAt:  100,
	}
	repo.PutFeedback(ctx, fb)

	list, err := repo.ListFeedbackBySeverity(ctx, FeedbackSeverityWarning, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
}

func TestDocsRepo_ListFeedbackByStep(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	fb := FeedbackRecord{
		Version:    1,
		FeedbackID: "fb-step",
		StepID:     "step-1",
		RunID:      "r",
		Source:     FeedbackSourceAgent,
		Severity:   FeedbackSeverityInfo,
		Category:   FeedbackCategoryGeneral,
		Summary:    "step",
		CreatedAt:  100,
	}
	repo.PutFeedback(ctx, fb)

	list, err := repo.ListFeedbackByStep(ctx, "step-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
}

// ─── ListRetrospectivesByRun / ByOutcome ─────────────────────────────────────

func TestDocsRepo_ListRetrospectivesByRun(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	retro := Retrospective{
		Version:   1,
		RetroID:   "ret-1",
		RunID:     "run-1",
		Trigger:   RetroTriggerRunCompleted,
		Outcome:   RetroOutcomeSuccess,
		Summary:   "done",
		CreatedAt: 100,
	}
	repo.PutRetrospective(ctx, retro)

	list, err := repo.ListRetrospectivesByRun(ctx, "run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
}

func TestDocsRepo_ListRetrospectivesByOutcome(t *testing.T) {
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	ctx := context.Background()

	retro := Retrospective{
		Version:   1,
		RetroID:   "ret-2",
		RunID:     "r",
		Trigger:   RetroTriggerRunFailed,
		Outcome:   RetroOutcomeFailure,
		Summary:   "failed",
		CreatedAt: 100,
	}
	repo.PutRetrospective(ctx, retro)

	list, err := repo.ListRetrospectivesByOutcome(ctx, RetroOutcomeFailure, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
}
