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
