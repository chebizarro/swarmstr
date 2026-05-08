package methods

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/nostr/events"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

type taskControlTestStore struct {
	mu          sync.Mutex
	replaceable map[string]state.Event
}

func newTaskControlTestStore() *taskControlTestStore {
	return &taskControlTestStore{replaceable: map[string]state.Event{}}
}

func (m *taskControlTestStore) storeKey(addr state.Address) string {
	return fmt.Sprintf("%d|%s|%s", addr.Kind, addr.PubKey, addr.DTag)
}

func (m *taskControlTestStore) GetLatestReplaceable(_ context.Context, addr state.Address) (state.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evt, ok := m.replaceable[m.storeKey(addr)]
	if !ok {
		return state.Event{}, state.ErrNotFound
	}
	return evt, nil
}

func (m *taskControlTestStore) PutReplaceable(_ context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evt := state.Event{
		ID:        fmt.Sprintf("evt:%s", m.storeKey(addr)),
		PubKey:    addr.PubKey,
		Kind:      addr.Kind,
		CreatedAt: time.Now().Unix(),
		Tags:      append(extraTags, []string{"d", addr.DTag}),
		Content:   content,
	}
	m.replaceable[m.storeKey(addr)] = evt
	return evt, nil
}

func (m *taskControlTestStore) PutAppend(_ context.Context, _ state.Address, _ string, _ [][]string) (state.Event, error) {
	return state.Event{}, nil
}

func (m *taskControlTestStore) ListByTag(_ context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]state.Event, error) {
	return m.listByTag(kind, "", tagName, tagValue, limit), nil
}

func (m *taskControlTestStore) ListByTagForAuthor(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]state.Event, error) {
	return m.listByTag(kind, authorPubKey, tagName, tagValue, limit), nil
}

func (m *taskControlTestStore) ListByTagPage(_ context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	return m.listByTagPage(kind, "", tagName, tagValue, limit, cursor)
}

func (m *taskControlTestStore) ListByTagForAuthorPage(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	return m.listByTagPage(kind, authorPubKey, tagName, tagValue, limit, cursor)
}

func (m *taskControlTestStore) listByTag(kind events.Kind, authorPubKey, tagName, tagValue string, limit int) []state.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]state.Event, 0, len(m.replaceable))
	for _, evt := range m.replaceable {
		if evt.Kind != kind {
			continue
		}
		if authorPubKey != "" && evt.PubKey != authorPubKey {
			continue
		}
		if hasTag(evt.Tags, tagName, tagValue) {
			out = append(out, evt)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return transcriptDTag(out[i].Tags) < transcriptDTag(out[j].Tags)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *taskControlTestStore) listByTagPage(kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]state.Event, 0, len(m.replaceable))
	for _, evt := range m.replaceable {
		if evt.Kind != kind {
			continue
		}
		if authorPubKey != "" && evt.PubKey != authorPubKey {
			continue
		}
		if hasTag(evt.Tags, tagName, tagValue) {
			out = append(out, evt)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	filtered := filterPageEvents(out, cursor)
	page := state.EventPage{Events: filtered}
	if len(filtered) > limit {
		page.Events = filtered[:limit]
		page.NextCursor = nextPageCursor(cursor, page.Events)
	}
	return page, nil
}

func filterPageEvents(events []state.Event, cursor *state.EventPageCursor) []state.Event {
	if cursor == nil || cursor.Until <= 0 {
		return append([]state.Event(nil), events...)
	}
	skip := make(map[string]struct{}, len(cursor.SkipIDs))
	for _, id := range cursor.SkipIDs {
		if id != "" {
			skip[id] = struct{}{}
		}
	}
	out := make([]state.Event, 0, len(events))
	for _, evt := range events {
		if evt.CreatedAt > cursor.Until {
			continue
		}
		if evt.CreatedAt == cursor.Until {
			if _, ok := skip[evt.ID]; ok {
				continue
			}
		}
		out = append(out, evt)
	}
	return out
}

func nextPageCursor(current *state.EventPageCursor, page []state.Event) *state.EventPageCursor {
	if len(page) == 0 {
		return nil
	}
	boundaryUnix := page[len(page)-1].CreatedAt
	seen := make(map[string]struct{})
	skipIDs := make([]string, 0, len(page))
	if current != nil && current.Until == boundaryUnix {
		for _, id := range current.SkipIDs {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			skipIDs = append(skipIDs, id)
		}
	}
	for _, evt := range page {
		if evt.CreatedAt != boundaryUnix || evt.ID == "" {
			continue
		}
		if _, ok := seen[evt.ID]; ok {
			continue
		}
		seen[evt.ID] = struct{}{}
		skipIDs = append(skipIDs, evt.ID)
	}
	sort.Strings(skipIDs)
	return &state.EventPageCursor{Until: boundaryUnix, SkipIDs: skipIDs}
}

func hasTag(tags [][]string, name, value string) bool {
	for _, tag := range tags {
		if len(tag) < 2 {
			continue
		}
		if tag[0] == name && tag[1] == value {
			return true
		}
	}
	return false
}

func transcriptDTag(tags [][]string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == "d" {
			return tag[1]
		}
	}
	return ""
}

func newTaskControlRepo() *state.DocsRepository {
	return state.NewDocsRepository(newTaskControlTestStore(), "author")
}

func newTaskControlService(repo *state.DocsRepository) *taskspkg.Service {
	return taskspkg.NewService(taskspkg.NewDocsStore(repo))
}

func TestCreateTask_NormalizesAndBuildsResponse(t *testing.T) {
	repo := newTaskControlRepo()
	now := time.Unix(1000, 0)
	req, err := (TasksCreateRequest{
		Task: state.TaskSpec{
			GoalID:        "goal-1",
			Instructions:  "  Review deployment output  ",
			Status:        state.TaskStatusBlocked,
			AssignedAgent: " Worker ",
		},
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	_ = now
	res, err := CreateTask(context.Background(), newTaskControlService(repo), req, "caller")
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.TaskID == "" {
		t.Fatal("expected generated task id")
	}
	if res.Task.Title != "Review deployment output" {
		t.Fatalf("unexpected title: %#v", res.Task)
	}
	if res.Task.Status != state.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %q", res.Task.Status)
	}
	if res.Task.AssignedAgent != "worker" {
		t.Fatalf("expected normalized assigned agent, got %q", res.Task.AssignedAgent)
	}
	if len(res.Task.Transitions) != 2 {
		t.Fatalf("expected pending+blocked transitions, got %#v", res.Task.Transitions)
	}
	if len(res.Runs) != 0 {
		t.Fatalf("expected no runs on create, got %#v", res.Runs)
	}
}

func TestListFilteredTasks_AppliesExpandedFilters(t *testing.T) {
	repo := newTaskControlRepo()
	ctx := context.Background()
	for _, task := range []state.TaskSpec{
		{Version: 1, TaskID: "t1", GoalID: "g1", Title: "task one", Instructions: "task one", AssignedAgent: "worker", Status: state.TaskStatusBlocked, CreatedAt: 100, UpdatedAt: 100},
		{Version: 1, TaskID: "t2", GoalID: "g2", Title: "task two", Instructions: "task two", AssignedAgent: "other", Status: state.TaskStatusReady, CreatedAt: 200, UpdatedAt: 200},
	} {
		if _, err := repo.PutTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ListFilteredTasks(ctx, newTaskControlService(repo), TasksListRequest{Status: "blocked", GoalID: "g1", AssignedAgent: "worker", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 1 || len(res.Tasks) != 1 || res.Tasks[0].TaskID != "t1" {
		t.Fatalf("unexpected filtered tasks: %#v", res)
	}
}

func TestResumeTask_CreatesQueuedRunAndMarksReady(t *testing.T) {
	repo := newTaskControlRepo()
	ctx := context.Background()
	task := state.TaskSpec{Version: 1, TaskID: "task-1", GoalID: "goal-1", Instructions: "do work", Title: "do work", Status: state.TaskStatusBlocked, CreatedAt: 100, UpdatedAt: 100}
	if _, err := repo.PutTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	res, err := ResumeTask(ctx, newTaskControlService(repo), TasksResumeRequest{TaskID: task.TaskID, Reason: "retry after unblock"}, "caller")
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.Status != state.TaskStatusReady {
		t.Fatalf("expected ready task, got %q", res.Task.Status)
	}
	if res.Task.CurrentRunID == "" || res.Task.LastRunID == "" {
		t.Fatalf("expected run ids, got %#v", res.Task)
	}
	if len(res.Runs) != 1 || res.Runs[0].Status != state.TaskRunStatusQueued {
		t.Fatalf("expected queued run, got %#v", res.Runs)
	}
}

func TestResumeTask_DecisionRejectedDoesNotCreateRun(t *testing.T) {
	repo := newTaskControlRepo()
	ctx := context.Background()
	task := state.TaskSpec{Version: 1, TaskID: "task-reject", GoalID: "goal-1", Instructions: "do work", Title: "do work", Status: state.TaskStatusAwaitingApproval, CreatedAt: 100, UpdatedAt: 100}
	if _, err := repo.PutTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	res, err := ResumeTask(ctx, newTaskControlService(repo), TasksResumeRequest{TaskID: task.TaskID, Decision: state.TaskApprovalDecisionRejected, Reason: "operator rejected"}, "caller")
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.Status != state.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %q", res.Task.Status)
	}
	if res.Task.CurrentRunID != "" || len(res.Runs) != 0 {
		t.Fatalf("rejected decision should not create run: task=%#v runs=%#v", res.Task, res.Runs)
	}
	if got := res.Task.Meta["approval_decision"]; got != string(state.TaskApprovalDecisionRejected) {
		t.Fatalf("approval_decision meta = %#v", got)
	}
}

func TestResumeTask_DecisionAmendedCreatesQueuedRun(t *testing.T) {
	repo := newTaskControlRepo()
	ctx := context.Background()
	task := state.TaskSpec{Version: 1, TaskID: "task-amend", GoalID: "goal-1", Instructions: "do work", Title: "do work", Status: state.TaskStatusAwaitingApproval, CreatedAt: 100, UpdatedAt: 100}
	if _, err := repo.PutTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	res, err := ResumeTask(ctx, newTaskControlService(repo), TasksResumeRequest{TaskID: task.TaskID, Decision: state.TaskApprovalDecisionAmended, Reason: "use safer plan"}, "caller")
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.Status != state.TaskStatusReady {
		t.Fatalf("expected ready task, got %q", res.Task.Status)
	}
	if len(res.Runs) != 1 || res.Runs[0].Status != state.TaskRunStatusQueued || res.Runs[0].Trigger != string(state.TaskApprovalDecisionAmended) {
		t.Fatalf("expected amended queued run, got %#v", res.Runs)
	}
	if got := res.Task.Meta["amendment_note"]; got != "use safer plan" {
		t.Fatalf("amendment_note meta = %#v", got)
	}
}

func TestCancelTask_CancelsActiveRunAndClearsCurrentRun(t *testing.T) {
	repo := newTaskControlRepo()
	ctx := context.Background()
	task := state.TaskSpec{Version: 1, TaskID: "task-1", GoalID: "goal-1", Instructions: "do work", Title: "do work", Status: state.TaskStatusReady, CurrentRunID: "run-1", LastRunID: "run-1", CreatedAt: 100, UpdatedAt: 100}
	run := state.TaskRun{Version: 1, RunID: "run-1", TaskID: task.TaskID, Attempt: 1, Status: state.TaskRunStatusQueued, StartedAt: 100}
	if _, err := repo.PutTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutTaskRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	res, err := CancelTask(ctx, newTaskControlService(repo), TasksCancelRequest{TaskID: task.TaskID, Reason: "operator cancelled"}, "caller")
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.Status != state.TaskStatusCancelled {
		t.Fatalf("expected cancelled task, got %q", res.Task.Status)
	}
	if res.Task.CurrentRunID != "" || res.Task.LastRunID != "run-1" {
		t.Fatalf("unexpected run linkage after cancel: %#v", res.Task)
	}
	if len(res.Runs) != 1 || res.Runs[0].Status != state.TaskRunStatusCancelled {
		t.Fatalf("expected cancelled run, got %#v", res.Runs)
	}
}

func TestBuildTaskGetResponse_ReturnsTaskAndRuns(t *testing.T) {
	repo := newTaskControlRepo()
	ctx := context.Background()
	task := state.TaskSpec{Version: 1, TaskID: "task-1", GoalID: "goal-1", Title: "x", Instructions: "x", Status: state.TaskStatusReady, CreatedAt: 100, UpdatedAt: 100}
	run := state.TaskRun{Version: 1, RunID: "run-1", TaskID: task.TaskID, Attempt: 1, Status: state.TaskRunStatusQueued, StartedAt: 100}
	if _, err := repo.PutTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutTaskRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	res, err := BuildTaskGetResponse(ctx, newTaskControlService(repo), task.TaskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.TaskID != task.TaskID || len(res.Runs) != 1 || res.Runs[0].RunID != run.RunID {
		t.Fatalf("unexpected get response: %#v", res)
	}
}

func TestListFilteredTasks_ExpandsFetchLimitWhenFiltersPresent(t *testing.T) {
	repo := newTaskControlRepo()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		status := state.TaskStatusReady
		goalID := "g2"
		if i == 19 {
			status = state.TaskStatusBlocked
			goalID = "g1"
		}
		task := state.TaskSpec{Version: 1, TaskID: fmt.Sprintf("task-%d", i), GoalID: goalID, Title: fmt.Sprintf("task-%d", i), Instructions: fmt.Sprintf("task-%d", i), Status: status, CreatedAt: int64(i + 1), UpdatedAt: int64(i + 1)}
		if _, err := repo.PutTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ListFilteredTasks(ctx, newTaskControlService(repo), TasksListRequest{Status: state.TaskStatus(strings.TrimSpace(" blocked ")), GoalID: "g1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 1 || len(res.Tasks) != 1 || res.Tasks[0].TaskID != "task-19" {
		t.Fatalf("unexpected filtered result with expanded fetch limit: %#v", res)
	}
}
