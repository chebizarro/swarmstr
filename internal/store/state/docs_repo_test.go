package state

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip44"
	"metiq/internal/nostr/events"
	"metiq/internal/nostr/secure"
)

type fakeStateStore struct {
	mu      sync.Mutex
	nowUnix int64
	repl    map[Address]Event
	appends []Event
}

func newFakeStateStore() *fakeStateStore {
	return &fakeStateStore{nowUnix: time.Now().Unix(), repl: map[Address]Event{}}
}

func (s *fakeStateStore) nextEventID() string {
	s.nowUnix++
	return fmt.Sprintf("evt-%d", s.nowUnix)
}

func (s *fakeStateStore) GetLatestReplaceable(_ context.Context, addr Address) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evt, ok := s.repl[addr]
	if !ok {
		return Event{}, ErrNotFound
	}
	return evt, nil
}

func (s *fakeStateStore) PutReplaceable(_ context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := make([][]string, 0, len(extraTags)+1)
	tags = append(tags, []string{"d", addr.DTag})
	tags = append(tags, extraTags...)
	evt := Event{ID: s.nextEventID(), PubKey: addr.PubKey, Kind: addr.Kind, CreatedAt: s.nowUnix, Tags: tags, Content: content}
	s.repl[addr] = evt
	return evt, nil
}

func (s *fakeStateStore) PutAppend(_ context.Context, addr Address, content string, extraTags [][]string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := make([][]string, 0, len(extraTags)+1)
	tags = append(tags, []string{"d", addr.DTag})
	tags = append(tags, extraTags...)
	evt := Event{ID: s.nextEventID(), PubKey: addr.PubKey, Kind: addr.Kind, CreatedAt: s.nowUnix, Tags: tags, Content: content}
	s.appends = append(s.appends, evt)
	return evt, nil
}

func insertFakeStateDoc(t *testing.T, store *fakeStateStore, author, dTag, typ string, value any, createdAt int64, id string, extraTags [][]string) {
	t.Helper()
	content, err := encodeEnvelopePayload(typ, value, nil)
	if err != nil {
		t.Fatalf("encode fake state doc: %v", err)
	}
	tags := make([][]string, 0, len(extraTags)+1)
	tags = append(tags, []string{"d", dTag})
	tags = append(tags, extraTags...)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.repl[Address{Kind: events.KindStateDoc, PubKey: author, DTag: dTag}] = Event{
		ID:        id,
		PubKey:    author,
		Kind:      events.KindStateDoc,
		CreatedAt: createdAt,
		Tags:      tags,
		Content:   content,
	}
}

func (s *fakeStateStore) ListByTag(_ context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]Event, error) {
	page, err := s.ListByTagPage(context.Background(), kind, tagName, tagValue, limit, nil)
	if err != nil {
		return nil, err
	}
	return page.Events, nil
}

func (s *fakeStateStore) ListByTagPage(_ context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]Event, 0, limit)
	for _, evt := range s.repl {
		if evt.Kind != kind || !hasTagValue(evt.Tags, tagName, tagValue) {
			continue
		}
		out = append(out, evt)
	}
	sortEventsNewestFirst(out)
	filtered := filterEventsForPage(out, cursor)
	page := EventPage{Events: filtered}
	if len(filtered) > limit {
		page.Events = filtered[:limit]
		page.NextCursor = nextCursorForPage(cursor, page.Events)
	}
	return page, nil
}

func (s *fakeStateStore) ListByTagForAuthor(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error) {
	page, err := s.ListByTagForAuthorPage(context.Background(), kind, authorPubKey, tagName, tagValue, limit, nil)
	if err != nil {
		return nil, err
	}
	return page.Events, nil
}

func (s *fakeStateStore) ListByTagForAuthorPage(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]Event, 0, limit)
	for _, evt := range s.repl {
		if evt.Kind != kind || evt.PubKey != authorPubKey || !hasTagValue(evt.Tags, tagName, tagValue) {
			continue
		}
		out = append(out, evt)
	}
	sortEventsNewestFirst(out)
	filtered := filterEventsForPage(out, cursor)
	page := EventPage{Events: filtered}
	if len(filtered) > limit {
		page.Events = filtered[:limit]
		page.NextCursor = nextCursorForPage(cursor, page.Events)
	}
	return page, nil
}

type stateTestKeyer struct {
	keyer.KeySigner
	sk nostr.SecretKey
}

func newStateTestKeyer(t *testing.T) nostr.Keyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("SecretKeyFromHex: %v", err)
	}
	return stateTestKeyer{KeySigner: keyer.NewPlainKeySigner([32]byte(sk)), sk: sk}
}

func (k stateTestKeyer) Encrypt(_ context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(recipient, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}

func (k stateTestKeyer) Decrypt(_ context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(sender, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, ck)
}

func TestDocsRepository_ConfigListSessionCheckpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	cfg := ConfigDoc{Version: 1, DM: DMPolicy{Policy: "open"}}
	evt, err := repo.PutConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("PutConfig: %v", err)
	}
	if !strings.HasPrefix(evt.ID, "evt-") {
		t.Fatalf("unexpected config event id: %s", evt.ID)
	}
	gotCfg, gotEvt, err := repo.GetConfigWithEvent(ctx)
	if err != nil {
		t.Fatalf("GetConfigWithEvent: %v", err)
	}
	if gotCfg.DM.Policy != "open" {
		t.Fatalf("unexpected config policy: %q", gotCfg.DM.Policy)
	}
	if gotEvt.ID == "" {
		t.Fatal("expected config event metadata")
	}

	listDoc := ListDoc{Version: 1, Name: "allow", Items: []string{"npub1a", "npub1b"}}
	if _, err := repo.PutList(ctx, "allow", listDoc); err != nil {
		t.Fatalf("PutList: %v", err)
	}
	gotList, err := repo.GetList(ctx, "allow")
	if err != nil {
		t.Fatalf("GetList: %v", err)
	}
	if len(gotList.Items) != 2 {
		t.Fatalf("unexpected list item count: %d", len(gotList.Items))
	}

	if _, err := repo.PutSession(ctx, "s1", SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer-a", LastInboundAt: 10}); err != nil {
		t.Fatalf("PutSession first: %v", err)
	}
	if _, err := repo.PutSession(ctx, "s1", SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer-a", LastInboundAt: 20}); err != nil {
		t.Fatalf("PutSession second: %v", err)
	}
	sessions, err := repo.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected deduped session list, got %d", len(sessions))
	}
	if sessions[0].LastInboundAt != 20 {
		t.Fatalf("expected latest session activity, got %d", sessions[0].LastInboundAt)
	}

	if _, err := repo.PutCheckpoint(ctx, "dm_ingest", CheckpointDoc{
		Version:   1,
		Name:      "dm_ingest",
		LastEvent: "evt-9",
		LastUnix:  99,
		ControlResponses: []ControlResponseCacheDoc{{
			CallerPubKey: "caller-a",
			RequestID:    "req-1",
			Payload:      `{"result":{"ok":true}}`,
			Tags:         [][]string{{"req", "req-1"}, {"status", "ok"}},
			EventUnix:    99,
		}},
	}); err != nil {
		t.Fatalf("PutCheckpoint: %v", err)
	}
	cp, err := repo.GetCheckpoint(ctx, "dm_ingest")
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if cp.LastEvent != "evt-9" || cp.LastUnix != 99 {
		t.Fatalf("unexpected checkpoint %+v", cp)
	}
	if len(cp.ControlResponses) != 1 || cp.ControlResponses[0].RequestID != "req-1" {
		t.Fatalf("unexpected control response checkpoint payload %+v", cp.ControlResponses)
	}
}

func TestDocsRepositoryListSessionsPrefersLatestPersistedDoc(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	if _, err := repo.PutSession(ctx, "s1", SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer-a", LastInboundAt: 20}); err != nil {
		t.Fatalf("PutSession first: %v", err)
	}
	if _, err := repo.PutSession(ctx, "s1", SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer-a", LastInboundAt: 5, Meta: map[string]any{"deleted": true}}); err != nil {
		t.Fatalf("PutSession second: %v", err)
	}

	sessions, err := repo.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one session, got %d", len(sessions))
	}
	if sessions[0].LastInboundAt != 5 {
		t.Fatalf("expected latest persisted session doc, got %+v", sessions[0])
	}
	if deleted, _ := sessions[0].Meta["deleted"].(bool); !deleted {
		t.Fatalf("expected latest metadata to win, got %+v", sessions[0].Meta)
	}
}

func TestDocsRepositoryListAgentsPrefersNewestDuplicateLogicalAgent(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	insertFakeStateDoc(t, store, "author-pub", "metiq:agent:agent-1-old", "agent_doc", AgentDoc{Version: 1, AgentID: "agent-1", Name: "old"}, 100, "evt-old", [][]string{{"t", "agent"}, {"agent", "agent-1"}})
	insertFakeStateDoc(t, store, "author-pub", "metiq:agent:agent-1-new", "agent_doc", AgentDoc{Version: 1, AgentID: "agent-1", Name: "new"}, 200, "evt-new", [][]string{{"t", "agent"}, {"agent", "agent-1"}})

	agents, err := repo.ListAgents(ctx, 10)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected one logical agent, got %d: %+v", len(agents), agents)
	}
	if agents[0].Name != "new" {
		t.Fatalf("expected newest agent doc to win, got %+v", agents[0])
	}
}

func TestDocsRepositoryListAgentFilesPrefersNewestDuplicateLogicalFile(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	agentTag := protectedTagValue("agent-1")
	insertFakeStateDoc(t, store, "author-pub", "metiq:agent:agent-1:file:readme-old", "agent_file_doc", AgentFileDoc{Version: 1, AgentID: "agent-1", Name: "readme.md", Content: "old"}, 100, "evt-old", [][]string{{"t", "agent_file"}, {"agent", agentTag}, {"name", "readme.md"}})
	insertFakeStateDoc(t, store, "author-pub", "metiq:agent:agent-1:file:readme-new", "agent_file_doc", AgentFileDoc{Version: 1, AgentID: "agent-1", Name: "readme.md", Content: "new"}, 200, "evt-new", [][]string{{"t", "agent_file"}, {"agent", agentTag}, {"name", "readme.md"}})

	files, err := repo.ListAgentFiles(ctx, "agent-1", 10)
	if err != nil {
		t.Fatalf("ListAgentFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one logical file, got %d: %+v", len(files), files)
	}
	if files[0].Content != "new" {
		t.Fatalf("expected newest agent file doc to win, got %+v", files[0])
	}
}

func TestDocsRepositoryListAgentsPagesPastDuplicateCrowding(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i := 0; i < 9; i++ {
		insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:agent:agent-1-dup-%02d", i), "agent_doc", AgentDoc{Version: 1, AgentID: "agent-1", Name: fmt.Sprintf("dup-%02d", i)}, int64(300-i), fmt.Sprintf("evt-dup-%02d", i), [][]string{{"t", "agent"}, {"agent", "agent-1"}})
	}
	insertFakeStateDoc(t, store, "author-pub", "metiq:agent:agent-2", "agent_doc", AgentDoc{Version: 1, AgentID: "agent-2", Name: "second"}, 100, "evt-second", [][]string{{"t", "agent"}, {"agent", "agent-2"}})

	agents, err := repo.ListAgents(ctx, 2)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected paging to find 2 logical agents, got %d: %+v", len(agents), agents)
	}
	if agents[0].AgentID != "agent-1" || agents[1].AgentID != "agent-2" {
		t.Fatalf("unexpected agents after duplicate crowding: %+v", agents)
	}
}

func TestDocsRepositoryListAgentFilesPagesPastDuplicateCrowding(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")
	agentTag := protectedTagValue("agent-1")

	for i := 0; i < 9; i++ {
		insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:agent:agent-1:file:readme-dup-%02d", i), "agent_file_doc", AgentFileDoc{Version: 1, AgentID: "agent-1", Name: "readme.md", Content: fmt.Sprintf("dup-%02d", i)}, int64(300-i), fmt.Sprintf("evt-file-dup-%02d", i), [][]string{{"t", "agent_file"}, {"agent", agentTag}, {"name", "readme.md"}})
	}
	insertFakeStateDoc(t, store, "author-pub", "metiq:agent:agent-1:file:notes", "agent_file_doc", AgentFileDoc{Version: 1, AgentID: "agent-1", Name: "notes.md", Content: "notes"}, 100, "evt-notes", [][]string{{"t", "agent_file"}, {"agent", agentTag}, {"name", "notes.md"}})

	files, err := repo.ListAgentFiles(ctx, "agent-1", 2)
	if err != nil {
		t.Fatalf("ListAgentFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected paging to find 2 logical files, got %d: %+v", len(files), files)
	}
	if files[0].Name != "notes.md" || files[1].Name != "readme.md" {
		t.Fatalf("unexpected files after duplicate crowding: %+v", files)
	}
}

func TestDocsRepositoryRemainingDedupeListsPagePastDuplicateCrowding(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, repo *DocsRepository, store *fakeStateStore)
	}{
		{
			name: "sessions",
			run: func(t *testing.T, repo *DocsRepository, store *fakeStateStore) {
				for i := 0; i < 9; i++ {
					insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:session:s1-dup-%02d", i), "session_doc", SessionDoc{Version: 1, SessionID: "s1", LastInboundAt: int64(300 - i)}, int64(300-i), fmt.Sprintf("evt-session-dup-%02d", i), [][]string{{"t", "session"}, {"session", "s1"}})
				}
				insertFakeStateDoc(t, store, "author-pub", "metiq:session:s2", "session_doc", SessionDoc{Version: 1, SessionID: "s2", LastInboundAt: 100}, 100, "evt-session-second", [][]string{{"t", "session"}, {"session", "s2"}})
				got, err := repo.ListSessions(context.Background(), 2)
				if err != nil {
					t.Fatalf("ListSessions: %v", err)
				}
				if len(got) != 2 || got[0].SessionID != "s1" || got[1].SessionID != "s2" {
					t.Fatalf("expected two logical sessions after duplicate crowding, got %+v", got)
				}
			},
		},
		{
			name: "tasks",
			run: func(t *testing.T, repo *DocsRepository, store *fakeStateStore) {
				for i := 0; i < 9; i++ {
					insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:task:t1-dup-%02d", i), "task_doc", TaskSpec{Version: 1, TaskID: "task-1", Title: "Task 1", Instructions: "Do task 1", UpdatedAt: int64(300 - i)}, int64(300-i), fmt.Sprintf("evt-task-dup-%02d", i), [][]string{{"t", "task"}, {"task", "task-1"}})
				}
				insertFakeStateDoc(t, store, "author-pub", "metiq:task:t2", "task_doc", TaskSpec{Version: 1, TaskID: "task-2", Title: "Task 2", Instructions: "Do task 2", UpdatedAt: 100}, 100, "evt-task-second", [][]string{{"t", "task"}, {"task", "task-2"}})
				got, err := repo.ListTasks(context.Background(), 2)
				if err != nil {
					t.Fatalf("ListTasks: %v", err)
				}
				if len(got) != 2 || got[0].TaskID != "task-1" || got[1].TaskID != "task-2" {
					t.Fatalf("expected two logical tasks after duplicate crowding, got %+v", got)
				}
			},
		},
		{
			name: "task runs",
			run: func(t *testing.T, repo *DocsRepository, store *fakeStateStore) {
				for i := 0; i < 9; i++ {
					insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:task_run:r1-dup-%02d", i), "task_run_doc", TaskRun{Version: 1, RunID: "run-1", TaskID: "task-1", Attempt: 1}, int64(300-i), fmt.Sprintf("evt-run-dup-%02d", i), [][]string{{"t", "task_run"}, {"task", protectedTagValue("task-1")}, {"run", protectedTagValue("run-1")}})
				}
				insertFakeStateDoc(t, store, "author-pub", "metiq:task_run:r2", "task_run_doc", TaskRun{Version: 1, RunID: "run-2", TaskID: "task-1", Attempt: 2}, 100, "evt-run-second", [][]string{{"t", "task_run"}, {"task", protectedTagValue("task-1")}, {"run", protectedTagValue("run-2")}})
				got, err := repo.ListTaskRuns(context.Background(), "task-1", 2)
				if err != nil {
					t.Fatalf("ListTaskRuns: %v", err)
				}
				if len(got) != 2 || got[0].RunID != "run-2" || got[1].RunID != "run-1" {
					t.Fatalf("expected two logical task runs after duplicate crowding, got %+v", got)
				}
			},
		},
		{
			name: "plans",
			run: func(t *testing.T, repo *DocsRepository, store *fakeStateStore) {
				for i := 0; i < 9; i++ {
					insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:plan:p1-dup-%02d", i), "plan_doc", PlanSpec{Version: 1, PlanID: "plan-1", GoalID: "goal-1", Title: "Plan 1", Status: PlanStatusDraft, Steps: []PlanStep{{StepID: "s1", Title: "Do"}}, UpdatedAt: int64(300 - i)}, int64(300-i), fmt.Sprintf("evt-plan-dup-%02d", i), [][]string{{"t", "plan"}, {"goal", protectedTagValue("goal-1")}, {"plan", protectedTagValue("plan-1")}})
				}
				insertFakeStateDoc(t, store, "author-pub", "metiq:plan:p2", "plan_doc", PlanSpec{Version: 1, PlanID: "plan-2", GoalID: "goal-1", Title: "Plan 2", Status: PlanStatusDraft, Steps: []PlanStep{{StepID: "s1", Title: "Do"}}, UpdatedAt: 100}, 100, "evt-plan-second", [][]string{{"t", "plan"}, {"goal", protectedTagValue("goal-1")}, {"plan", protectedTagValue("plan-2")}})
				got, err := repo.ListPlans(context.Background(), "goal-1", 2)
				if err != nil {
					t.Fatalf("ListPlans: %v", err)
				}
				if len(got) != 2 || got[0].PlanID != "plan-1" || got[1].PlanID != "plan-2" {
					t.Fatalf("expected two logical plans after duplicate crowding, got %+v", got)
				}
			},
		},
		{
			name: "workflow journals",
			run: func(t *testing.T, repo *DocsRepository, store *fakeStateStore) {
				for i := 0; i < 9; i++ {
					insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:workflow_journal:r1-dup-%02d", i), "workflow_journal_doc", WorkflowJournalDoc{Version: 1, TaskID: "task-1", RunID: "run-1", UpdatedAt: int64(300 - i)}, int64(300-i), fmt.Sprintf("evt-journal-dup-%02d", i), [][]string{{"t", "workflow_journal"}, {"task", protectedTagValue("task-1")}, {"run", protectedTagValue("run-1")}})
				}
				insertFakeStateDoc(t, store, "author-pub", "metiq:workflow_journal:r2", "workflow_journal_doc", WorkflowJournalDoc{Version: 1, TaskID: "task-1", RunID: "run-2", UpdatedAt: 100}, 100, "evt-journal-second", [][]string{{"t", "workflow_journal"}, {"task", protectedTagValue("task-1")}, {"run", protectedTagValue("run-2")}})
				got, err := repo.ListWorkflowJournals(context.Background(), "task-1", 2)
				if err != nil {
					t.Fatalf("ListWorkflowJournals: %v", err)
				}
				if len(got) != 2 || got[0].RunID != "run-1" || got[1].RunID != "run-2" {
					t.Fatalf("expected two logical journals after duplicate crowding, got %+v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStateStore()
			repo := NewDocsRepository(store, "author-pub")
			tt.run(t, repo, store)
		})
	}
}

func TestDocsRepositoryListTaskRunsScansPastLimitForFinalSort(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i := 0; i < 4; i++ {
		insertFakeStateDoc(t, store, "author-pub", fmt.Sprintf("metiq:task_run:r1-newer-%02d", i), "task_run_doc", TaskRun{Version: 1, RunID: "run-1", TaskID: "task-1", Attempt: 1}, int64(400-i), fmt.Sprintf("evt-run-r1-%02d", i), [][]string{{"t", "task_run"}, {"task", protectedTagValue("task-1")}, {"run", protectedTagValue("run-1")}})
	}
	insertFakeStateDoc(t, store, "author-pub", "metiq:task_run:r2-older-higher-attempt", "task_run_doc", TaskRun{Version: 1, RunID: "run-2", TaskID: "task-1", Attempt: 99}, 100, "evt-run-r2", [][]string{{"t", "task_run"}, {"task", protectedTagValue("task-1")}, {"run", protectedTagValue("run-2")}})

	got, err := repo.ListTaskRuns(ctx, "task-1", 1)
	if err != nil {
		t.Fatalf("ListTaskRuns: %v", err)
	}
	if len(got) != 1 || got[0].RunID != "run-2" {
		t.Fatalf("expected final Attempt sort to consider later pages, got %+v", got)
	}
}

func TestDocsRepositoryTaskAndRunRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	task := TaskSpec{TaskID: "task-1", GoalID: "goal-1", Title: "Persist task", Instructions: "Persist lifecycle state.", AssignedAgent: "builder", SessionID: "sess-1"}.Normalize()
	if err := task.ApplyTransition(TaskStatusPlanned, 100, "planner", "planner", "planned", nil); err != nil {
		t.Fatalf("task ApplyTransition: %v", err)
	}
	if err := task.ApplyTransition(TaskStatusReady, 110, "planner", "planner", "ready", nil); err != nil {
		t.Fatalf("task ApplyTransition ready: %v", err)
	}
	if _, err := repo.PutTask(ctx, task); err != nil {
		t.Fatalf("PutTask: %v", err)
	}
	gotTask, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.Status != TaskStatusReady || len(gotTask.Transitions) != 2 {
		t.Fatalf("unexpected task round-trip: %+v", gotTask)
	}

	firstRun, err := NewTaskRunAttempt(task, "run-1", nil, 120, "manual", "runner", "runtime")
	if err != nil {
		t.Fatalf("NewTaskRunAttempt first: %v", err)
	}
	if err := firstRun.ApplyTransition(TaskRunStatusRunning, 130, "runner", "runtime", "started", nil); err != nil {
		t.Fatalf("firstRun running: %v", err)
	}
	if err := firstRun.ApplyTransition(TaskRunStatusFailed, 140, "runner", "runtime", "failed", nil); err != nil {
		t.Fatalf("firstRun failed: %v", err)
	}
	if _, err := repo.PutTaskRun(ctx, firstRun); err != nil {
		t.Fatalf("PutTaskRun first: %v", err)
	}

	secondRun, err := NewTaskRunAttempt(task, "run-2", []TaskRun{firstRun}, 150, "retry", "runner", "runtime")
	if err != nil {
		t.Fatalf("NewTaskRunAttempt second: %v", err)
	}
	if err := secondRun.ApplyTransition(TaskRunStatusRunning, 160, "runner", "runtime", "restarted", nil); err != nil {
		t.Fatalf("secondRun running: %v", err)
	}
	if _, err := repo.PutTaskRun(ctx, secondRun); err != nil {
		t.Fatalf("PutTaskRun second: %v", err)
	}

	gotRun, err := repo.GetTaskRun(ctx, "run-2")
	if err != nil {
		t.Fatalf("GetTaskRun: %v", err)
	}
	if gotRun.Attempt != 2 || gotRun.Status != TaskRunStatusRunning {
		t.Fatalf("unexpected run round-trip: %+v", gotRun)
	}
	runs, err := repo.ListTaskRuns(ctx, "task-1", 10)
	if err != nil {
		t.Fatalf("ListTaskRuns: %v", err)
	}
	if len(runs) != 2 || runs[0].RunID != "run-2" || runs[1].RunID != "run-1" {
		t.Fatalf("unexpected run list ordering: %+v", runs)
	}
	tasks, err := repo.ListTasks(ctx, 10)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskID != "task-1" {
		t.Fatalf("unexpected task list: %+v", tasks)
	}
}

func TestDocsRepositoryConfigMigratesPlaintextToEncryptedOnWrite(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	plainRepo := NewDocsRepository(store, "author-pub")
	if _, err := plainRepo.PutConfig(ctx, ConfigDoc{Version: 1, DM: DMPolicy{Policy: "open"}}); err != nil {
		t.Fatalf("PutConfig plaintext: %v", err)
	}

	addr := Address{Kind: events.KindStateDoc, PubKey: "author-pub", DTag: "metiq:config"}
	store.mu.Lock()
	legacyContent := store.repl[addr].Content
	store.mu.Unlock()
	if strings.Contains(legacyContent, `"enc":"nip44"`) {
		t.Fatalf("expected plaintext envelope, got %s", legacyContent)
	}
	if !strings.Contains(legacyContent, `"payload":"{\"version\":1`) {
		t.Fatalf("expected visible plaintext payload, got %s", legacyContent)
	}

	codec, err := secure.NewMutableSelfEnvelopeCodec(newStateTestKeyer(t), true)
	if err != nil {
		t.Fatalf("NewMutableSelfEnvelopeCodec: %v", err)
	}
	encRepo := NewDocsRepositoryWithCodec(store, "author-pub", codec)
	got, err := encRepo.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig with encrypted codec: %v", err)
	}
	if got.DM.Policy != "open" {
		t.Fatalf("unexpected config round-trip: %+v", got)
	}
	if _, err := encRepo.PutConfig(ctx, got); err != nil {
		t.Fatalf("PutConfig re-encrypted: %v", err)
	}

	store.mu.Lock()
	encryptedContent := store.repl[addr].Content
	store.mu.Unlock()
	if !strings.Contains(encryptedContent, `"enc":"nip44"`) {
		t.Fatalf("expected nip44 envelope after rewrite, got %s", encryptedContent)
	}
	if strings.Contains(encryptedContent, `"policy":"open"`) {
		t.Fatalf("expected ciphertext payload after rewrite, got %s", encryptedContent)
	}
}

func TestDocsRepositoryPlanPutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	plan := PlanSpec{
		PlanID:   "plan-1",
		GoalID:   "goal-1",
		Title:    "Test plan",
		Revision: 1,
		Status:   PlanStatusActive,
		Steps: []PlanStep{
			{StepID: "s1", Title: "Step A", Status: PlanStepStatusCompleted},
			{StepID: "s2", Title: "Step B", Status: PlanStepStatusPending, DependsOn: []string{"s1"}},
		},
		Assumptions:      []string{"API works"},
		Risks:            []string{"Timeout"},
		RollbackStrategy: "Cancel all",
		CreatedAt:        100,
		UpdatedAt:        200,
	}

	_, err := repo.PutPlan(ctx, plan)
	if err != nil {
		t.Fatalf("PutPlan: %v", err)
	}

	got, err := repo.GetPlan(ctx, "plan-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.PlanID != "plan-1" || got.GoalID != "goal-1" {
		t.Fatalf("unexpected plan identity: %+v", got)
	}
	if got.Status != PlanStatusActive {
		t.Fatalf("expected active status, got %q", got.Status)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(got.Steps))
	}
	if got.Steps[1].DependsOn[0] != "s1" {
		t.Fatalf("dependency lost: %v", got.Steps[1].DependsOn)
	}
}

func TestDocsRepositoryPlanPutRejectsCycle(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	plan := PlanSpec{
		PlanID: "cycle-plan",
		Title:  "Cycle plan",
		Status: PlanStatusDraft,
		Steps: []PlanStep{
			{StepID: "s1", Title: "A", DependsOn: []string{"s2"}},
			{StepID: "s2", Title: "B", DependsOn: []string{"s1"}},
		},
	}
	_, err := repo.PutPlan(ctx, plan)
	if err == nil {
		t.Fatal("expected error for cyclic plan")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

func TestDocsRepositoryListPlansFiltersByGoal(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for _, g := range []string{"goal-A", "goal-B"} {
		_, err := repo.PutPlan(ctx, PlanSpec{
			PlanID: fmt.Sprintf("plan-%s", g),
			GoalID: g,
			Title:  "Plan for " + g,
			Status: PlanStatusDraft,
			Steps:  []PlanStep{{StepID: "s1", Title: "Do"}},
		})
		if err != nil {
			t.Fatalf("PutPlan(%s): %v", g, err)
		}
	}

	// List all.
	all, err := repo.ListPlans(ctx, "", 10)
	if err != nil {
		t.Fatalf("ListPlans all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(all))
	}

	// Filter by goal.
	filtered, err := repo.ListPlans(ctx, "goal-A", 10)
	if err != nil {
		t.Fatalf("ListPlans filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].GoalID != "goal-A" {
		t.Fatalf("expected 1 plan for goal-A, got %d", len(filtered))
	}
}

func TestDocsRepositoryPlanRevisionUpdate(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	plan := PlanSpec{
		PlanID:   "rev-plan",
		Title:    "Revisable",
		Revision: 1,
		Status:   PlanStatusActive,
		Steps: []PlanStep{
			{StepID: "s1", Title: "Original step"},
		},
		UpdatedAt: 100,
	}
	if _, err := repo.PutPlan(ctx, plan); err != nil {
		t.Fatalf("PutPlan v1: %v", err)
	}

	// Update with new revision.
	plan.Revision = 2
	plan.Status = PlanStatusRevising
	plan.Steps = append(plan.Steps, PlanStep{StepID: "s2", Title: "New step"})
	plan.UpdatedAt = 200
	if _, err := repo.PutPlan(ctx, plan); err != nil {
		t.Fatalf("PutPlan v2: %v", err)
	}

	got, err := repo.GetPlan(ctx, "rev-plan")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Revision != 2 {
		t.Fatalf("expected revision=2, got %d", got.Revision)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("expected 2 steps after revision, got %d", len(got.Steps))
	}
}

func TestDocsRepositoryConfigReadsLegacyRawJSON(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	addr := Address{Kind: events.KindStateDoc, PubKey: "author-pub", DTag: "metiq:config"}
	store.mu.Lock()
	store.repl[addr] = Event{
		ID:        "legacy-1",
		PubKey:    "author-pub",
		Kind:      events.KindStateDoc,
		CreatedAt: time.Now().Unix(),
		Tags:      [][]string{{"d", "metiq:config"}},
		Content:   `{"version":1,"dm":{"policy":"open"}}`,
	}
	store.mu.Unlock()

	codec, err := secure.NewMutableSelfEnvelopeCodec(newStateTestKeyer(t), true)
	if err != nil {
		t.Fatalf("NewMutableSelfEnvelopeCodec: %v", err)
	}
	repo := NewDocsRepositoryWithCodec(store, "author-pub", codec)
	cfg, err := repo.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig legacy raw JSON: %v", err)
	}
	if cfg.DM.Policy != "open" {
		t.Fatalf("unexpected legacy config decode: %+v", cfg)
	}
}

// ── Feedback round-trip tests ──────────────────────────────────────────────────

func TestDocsRepositoryFeedbackRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	rec := FeedbackRecord{
		FeedbackID: "fb-1",
		TaskID:     "task-1",
		GoalID:     "goal-1",
		RunID:      "run-1",
		Source:     FeedbackSourceOperator,
		Severity:   FeedbackSeverityWarning,
		Category:   FeedbackCategoryPolicy,
		Summary:    "policy concern",
		Detail:     "unauthorized tool use",
		Author:     "alice",
		CreatedAt:  1000,
	}
	if _, err := repo.PutFeedback(ctx, rec); err != nil {
		t.Fatalf("PutFeedback: %v", err)
	}
	got, err := repo.GetFeedback(ctx, "fb-1")
	if err != nil {
		t.Fatalf("GetFeedback: %v", err)
	}
	if got.FeedbackID != "fb-1" || got.Summary != "policy concern" {
		t.Fatalf("unexpected feedback round-trip: %+v", got)
	}
	if got.Source != FeedbackSourceOperator || got.Severity != FeedbackSeverityWarning {
		t.Fatalf("unexpected source/severity: %+v", got)
	}
}

func TestDocsRepositoryFeedbackListByTask(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i, taskID := range []string{"task-1", "task-1", "task-2"} {
		rec := FeedbackRecord{
			FeedbackID: fmt.Sprintf("fb-%d", i),
			TaskID:     taskID,
			Source:     FeedbackSourceAgent,
			Severity:   FeedbackSeverityInfo,
			Category:   FeedbackCategoryGeneral,
			Summary:    fmt.Sprintf("feedback %d", i),
			CreatedAt:  int64(1000 + i),
		}
		if _, err := repo.PutFeedback(ctx, rec); err != nil {
			t.Fatalf("PutFeedback %d: %v", i, err)
		}
	}

	got, err := repo.ListFeedbackByTask(ctx, "task-1", 10)
	if err != nil {
		t.Fatalf("ListFeedbackByTask: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 feedback for task-1, got %d", len(got))
	}
	// Should be newest first.
	if got[0].CreatedAt < got[1].CreatedAt {
		t.Error("expected newest first ordering")
	}
}

func TestDocsRepositoryFeedbackListByGoal(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	if _, err := repo.PutFeedback(ctx, FeedbackRecord{
		FeedbackID: "fb-g1", GoalID: "goal-1", Source: FeedbackSourceReview,
		Severity: FeedbackSeverityInfo, Category: FeedbackCategoryGeneral,
		Summary: "goal feedback", CreatedAt: 1000,
	}); err != nil {
		t.Fatalf("PutFeedback: %v", err)
	}

	got, err := repo.ListFeedbackByGoal(ctx, "goal-1", 10)
	if err != nil {
		t.Fatalf("ListFeedbackByGoal: %v", err)
	}
	if len(got) != 1 || got[0].FeedbackID != "fb-g1" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestDocsRepositoryFeedbackListBySource(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i, src := range []FeedbackSource{FeedbackSourceOperator, FeedbackSourceVerification, FeedbackSourceOperator} {
		if _, err := repo.PutFeedback(ctx, FeedbackRecord{
			FeedbackID: fmt.Sprintf("fb-s%d", i), TaskID: "t1",
			Source: src, Severity: FeedbackSeverityInfo, Category: FeedbackCategoryGeneral,
			Summary: "x", CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("PutFeedback %d: %v", i, err)
		}
	}

	got, err := repo.ListFeedbackBySource(ctx, FeedbackSourceOperator, 10)
	if err != nil {
		t.Fatalf("ListFeedbackBySource: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 operator feedback, got %d", len(got))
	}
}

func TestDocsRepositoryFeedbackValidation(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	// Missing summary should fail.
	_, err := repo.PutFeedback(ctx, FeedbackRecord{
		FeedbackID: "fb-bad", TaskID: "t1",
		Source: FeedbackSourceAgent, Severity: FeedbackSeverityInfo,
		Category: FeedbackCategoryGeneral,
	})
	if err == nil {
		t.Fatal("expected validation error for missing summary")
	}
}

// ── Proposal round-trip tests ──────────────────────────────────────────────────

func TestDocsRepositoryProposalRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	p := PolicyProposal{
		ProposalID:    "prop-1",
		Kind:          ProposalKindPrompt,
		Status:        ProposalStatusDraft,
		Title:         "Better safety prompt",
		TargetField:   "system_prompt",
		ProposedValue: "You are a safe agent.",
		FeedbackIDs:   []string{"fb-1", "fb-2"},
		GoalID:        "goal-1",
		TaskID:        "task-1",
		CreatedAt:     1000,
	}
	if _, err := repo.PutProposal(ctx, p); err != nil {
		t.Fatalf("PutProposal: %v", err)
	}
	got, err := repo.GetProposal(ctx, "prop-1")
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.ProposalID != "prop-1" || got.Title != "Better safety prompt" {
		t.Fatalf("unexpected round-trip: %+v", got)
	}
	if len(got.FeedbackIDs) != 2 {
		t.Fatalf("feedback_ids = %v", got.FeedbackIDs)
	}
}

func TestDocsRepositoryProposalListByKind(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i, kind := range []ProposalKind{ProposalKindPrompt, ProposalKindPolicy, ProposalKindPrompt} {
		if _, err := repo.PutProposal(ctx, PolicyProposal{
			ProposalID: fmt.Sprintf("prop-%d", i), Kind: kind,
			Status: ProposalStatusDraft, Title: fmt.Sprintf("p%d", i),
			TargetField: "f", ProposedValue: "v", CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("PutProposal %d: %v", i, err)
		}
	}

	got, err := repo.ListProposalsByKind(ctx, ProposalKindPrompt, 10)
	if err != nil {
		t.Fatalf("ListProposalsByKind: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 prompt proposals, got %d", len(got))
	}
}

func TestDocsRepositoryProposalListByStatus(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i, status := range []ProposalStatus{ProposalStatusDraft, ProposalStatusPending, ProposalStatusDraft} {
		if _, err := repo.PutProposal(ctx, PolicyProposal{
			ProposalID: fmt.Sprintf("prop-s%d", i), Kind: ProposalKindPolicy,
			Status: status, Title: fmt.Sprintf("p%d", i),
			TargetField: "f", ProposedValue: "v", CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("PutProposal %d: %v", i, err)
		}
	}

	got, err := repo.ListProposalsByStatus(ctx, ProposalStatusDraft, 10)
	if err != nil {
		t.Fatalf("ListProposalsByStatus: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 draft proposals, got %d", len(got))
	}
}

func TestDocsRepositoryProposalListByTask(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	if _, err := repo.PutProposal(ctx, PolicyProposal{
		ProposalID: "prop-t1", Kind: ProposalKindPolicy,
		Status: ProposalStatusDraft, Title: "linked",
		TargetField: "f", ProposedValue: "v", TaskID: "task-1", CreatedAt: 1000,
	}); err != nil {
		t.Fatalf("PutProposal: %v", err)
	}

	got, err := repo.ListProposalsByTask(ctx, "task-1", 10)
	if err != nil {
		t.Fatalf("ListProposalsByTask: %v", err)
	}
	if len(got) != 1 || got[0].ProposalID != "prop-t1" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestDocsRepositoryProposalValidation(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	_, err := repo.PutProposal(ctx, PolicyProposal{
		ProposalID: "prop-bad", Kind: ProposalKindPolicy,
		Status: ProposalStatusDraft,
		// Missing title and proposed_value.
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// ── Retrospective DocsRepository tests ──────────────────────────────────────

func TestDocsRepositoryRetroRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	r := Retrospective{
		RetroID:      "retro-1",
		GoalID:       "goal-1",
		TaskID:       "task-1",
		RunID:        "run-1",
		AgentID:      "agent-1",
		Trigger:      RetroTriggerRunFailed,
		Outcome:      RetroOutcomeFailure,
		Summary:      "Run failed due to timeout",
		WhatWorked:   []string{"Token usage ok"},
		WhatFailed:   []string{"API timeout"},
		Improvements: []string{"Add retry"},
		FeedbackIDs:  []string{"fb-1"},
		ProposalIDs:  []string{"prop-1"},
		DurationMS:   5000,
		CreatedAt:    1000,
		CreatedBy:    "system",
	}
	if _, err := repo.PutRetrospective(ctx, r); err != nil {
		t.Fatalf("PutRetrospective: %v", err)
	}
	got, err := repo.GetRetrospective(ctx, "retro-1")
	if err != nil {
		t.Fatalf("GetRetrospective: %v", err)
	}
	if got.RetroID != "retro-1" || got.Summary != "Run failed due to timeout" {
		t.Fatalf("unexpected round-trip: %+v", got)
	}
	if got.Trigger != RetroTriggerRunFailed {
		t.Fatalf("trigger = %s", got.Trigger)
	}
	if len(got.WhatWorked) != 1 || len(got.WhatFailed) != 1 {
		t.Fatal("what_worked/what_failed mismatch")
	}
	if len(got.FeedbackIDs) != 1 || len(got.ProposalIDs) != 1 {
		t.Fatal("feedback/proposal IDs mismatch")
	}
}

func TestDocsRepositoryRetroListByTask(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i, taskID := range []string{"task-1", "task-2", "task-1"} {
		if _, err := repo.PutRetrospective(ctx, Retrospective{
			RetroID:   fmt.Sprintf("retro-%d", i),
			TaskID:    taskID,
			Trigger:   RetroTriggerRunCompleted,
			Outcome:   RetroOutcomeSuccess,
			Summary:   "ok",
			CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("PutRetrospective: %v", err)
		}
	}
	docs, err := repo.ListRetrospectivesByTask(ctx, "task-1", 10)
	if err != nil {
		t.Fatalf("ListRetrospectivesByTask: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2, got %d", len(docs))
	}
}

func TestDocsRepositoryRetroListByGoal(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	if _, err := repo.PutRetrospective(ctx, Retrospective{
		RetroID: "retro-g1", GoalID: "goal-1",
		Trigger: RetroTriggerRunCompleted, Outcome: RetroOutcomeSuccess,
		Summary: "ok", CreatedAt: 1000,
	}); err != nil {
		t.Fatalf("PutRetrospective: %v", err)
	}
	docs, err := repo.ListRetrospectivesByGoal(ctx, "goal-1", 10)
	if err != nil {
		t.Fatalf("ListRetrospectivesByGoal: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1, got %d", len(docs))
	}
}

func TestDocsRepositoryRetroListByTrigger(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	for i, trigger := range []RetroTrigger{RetroTriggerRunFailed, RetroTriggerRunCompleted, RetroTriggerRunFailed} {
		if _, err := repo.PutRetrospective(ctx, Retrospective{
			RetroID:   fmt.Sprintf("retro-%d", i),
			Trigger:   trigger,
			Outcome:   RetroOutcomeFailure,
			Summary:   "test",
			CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("PutRetrospective: %v", err)
		}
	}
	docs, err := repo.ListRetrospectivesByTrigger(ctx, RetroTriggerRunFailed, 10)
	if err != nil {
		t.Fatalf("ListRetrospectivesByTrigger: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2, got %d", len(docs))
	}
}

func TestDocsRepositoryRetroValidation(t *testing.T) {
	ctx := context.Background()
	store := newFakeStateStore()
	repo := NewDocsRepository(store, "author-pub")

	_, err := repo.PutRetrospective(ctx, Retrospective{
		RetroID: "retro-bad",
		// Missing summary, trigger, outcome.
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
