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

type docsStoreState struct {
	mu          sync.Mutex
	replaceable map[string]state.Event
	seq         int64
}

func newDocsStoreState() *docsStoreState {
	return &docsStoreState{replaceable: map[string]state.Event{}}
}

func (m *docsStoreState) storeKey(addr state.Address) string {
	return fmt.Sprintf("%d|%s|%s", addr.Kind, addr.PubKey, addr.DTag)
}

func (m *docsStoreState) GetLatestReplaceable(_ context.Context, addr state.Address) (state.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evt, ok := m.replaceable[m.storeKey(addr)]
	if !ok {
		return state.Event{}, state.ErrNotFound
	}
	return evt, nil
}

func (m *docsStoreState) PutReplaceable(_ context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	evt := state.Event{
		ID:        fmt.Sprintf("evt:%d:%s", m.seq, m.storeKey(addr)),
		PubKey:    addr.PubKey,
		Kind:      addr.Kind,
		CreatedAt: 1000 + m.seq,
		Tags:      append(extraTags, []string{"d", addr.DTag}),
		Content:   content,
	}
	m.replaceable[m.storeKey(addr)] = evt
	return evt, nil
}

func (m *docsStoreState) PutAppend(_ context.Context, _ state.Address, _ string, _ [][]string) (state.Event, error) {
	return state.Event{}, nil
}

func (m *docsStoreState) ListByTag(_ context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]state.Event, error) {
	return m.listByTag(kind, "", tagName, tagValue, limit), nil
}

func (m *docsStoreState) ListByTagForAuthor(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]state.Event, error) {
	return m.listByTag(kind, authorPubKey, tagName, tagValue, limit), nil
}

func (m *docsStoreState) ListByTagPage(_ context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	return m.listByTagPage(kind, "", tagName, tagValue, limit, cursor)
}

func (m *docsStoreState) ListByTagForAuthorPage(_ context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	return m.listByTagPage(kind, authorPubKey, tagName, tagValue, limit, cursor)
}

func (m *docsStoreState) listByTag(kind events.Kind, authorPubKey, tagName, tagValue string, limit int) []state.Event {
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
		if hasDocStoreTag(evt.Tags, tagName, tagValue) {
			out = append(out, evt)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *docsStoreState) listByTagPage(kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *state.EventPageCursor) (state.EventPage, error) {
	if limit <= 0 {
		limit = 100
	}
	all := m.listByTag(kind, authorPubKey, tagName, tagValue, 10_000)
	if cursor == nil || cursor.Until == 0 {
		if len(all) <= limit {
			return state.EventPage{Events: all}, nil
		}
		page := all[:limit]
		return state.EventPage{Events: page, NextCursor: &state.EventPageCursor{Until: page[len(page)-1].CreatedAt, SkipIDs: []string{page[len(page)-1].ID}}}, nil
	}
	out := make([]state.Event, 0, len(all))
	skip := map[string]struct{}{}
	for _, id := range cursor.SkipIDs {
		skip[id] = struct{}{}
	}
	for _, evt := range all {
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
	if len(out) <= limit {
		return state.EventPage{Events: out}, nil
	}
	page := out[:limit]
	return state.EventPage{Events: page, NextCursor: &state.EventPageCursor{Until: page[len(page)-1].CreatedAt, SkipIDs: []string{page[len(page)-1].ID}}}, nil
}

func hasDocStoreTag(tags [][]string, key, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key && tag[1] == value {
			return true
		}
	}
	return false
}

func newDocsStore(t *testing.T) *DocsStore {
	t.Helper()
	repo := state.NewDocsRepository(newDocsStoreState(), "author")
	return NewDocsStore(repo)
}

func TestDocsStoreSaveLoadTaskHydratesRunsWithoutEmbeddedDupes(t *testing.T) {
	store := newDocsStore(t)
	ctx := context.Background()

	entry := &LedgerEntry{
		Task:      state.TaskSpec{TaskID: "task-1", Title: "Task 1", Instructions: "Do it", Status: state.TaskStatusReady, CreatedAt: 101, UpdatedAt: 102},
		Source:    TaskSourceWorkflow,
		SourceRef: "wf-1",
		CreatedAt: 101,
		UpdatedAt: 102,
		Runs:      []state.TaskRun{{RunID: "embedded-run", TaskID: "task-1", Status: state.TaskRunStatusQueued, Attempt: 1}},
	}
	if err := store.SaveTask(ctx, entry); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	loaded, err := store.LoadTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if len(loaded.Runs) != 0 {
		t.Fatalf("expected no loaded runs until SaveRun persists canonical run docs, got %d", len(loaded.Runs))
	}
	if loaded.Source != TaskSourceWorkflow || loaded.SourceRef != "wf-1" {
		t.Fatalf("unexpected source mapping: %+v", loaded)
	}

	runEntry := &RunEntry{
		Run:       state.TaskRun{RunID: "run-1", TaskID: "task-1", Status: state.TaskRunStatusQueued, Attempt: 1},
		Source:    TaskSourceWorkflow,
		SourceRef: "wf-1",
		CreatedAt: 103,
		UpdatedAt: 103,
	}
	if err := store.SaveRun(ctx, runEntry); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	loaded, err = store.LoadTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("LoadTask after SaveRun: %v", err)
	}
	if len(loaded.Runs) != 1 || loaded.Runs[0].RunID != "run-1" {
		t.Fatalf("expected canonical run hydration from task_run docs, got %+v", loaded.Runs)
	}
}

func TestDocsStoreListTasksUsesLedgerFilters(t *testing.T) {
	store := newDocsStore(t)
	ctx := context.Background()

	_ = store.SaveTask(ctx, &LedgerEntry{Task: state.TaskSpec{TaskID: "t1", Title: "T1", Instructions: "do", Status: state.TaskStatusReady, AssignedAgent: "a1", CreatedAt: 100, UpdatedAt: 100}, Source: TaskSourceManual})
	_ = store.SaveTask(ctx, &LedgerEntry{Task: state.TaskSpec{TaskID: "t2", Title: "T2", Instructions: "do", Status: state.TaskStatusBlocked, AssignedAgent: "a2", CreatedAt: 101, UpdatedAt: 101}, Source: TaskSourceCron})
	_ = store.SaveTask(ctx, &LedgerEntry{Task: state.TaskSpec{TaskID: "t3", Title: "T3", Instructions: "do", Status: state.TaskStatusBlocked, AssignedAgent: "a2", CreatedAt: 102, UpdatedAt: 102}, Source: TaskSourceCron})

	entries, err := store.ListTasks(ctx, ListTasksOptions{Status: []state.TaskStatus{state.TaskStatusBlocked}, Source: []TaskSource{TaskSourceCron}, AgentID: "a2", OrderBy: "created_at", OrderDesc: false, Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 filtered entries, got %d", len(entries))
	}
	if entries[0].Task.TaskID != "t2" || entries[1].Task.TaskID != "t3" {
		t.Fatalf("unexpected ordering/filter results: %+v", []string{entries[0].Task.TaskID, entries[1].Task.TaskID})
	}
}

func TestDocsStoreListRunsUsesLedgerFilters(t *testing.T) {
	store := newDocsStore(t)
	ctx := context.Background()

	_ = store.SaveTask(ctx, &LedgerEntry{Task: state.TaskSpec{TaskID: "task-runs", Title: "T", Instructions: "do", Status: state.TaskStatusReady}, Source: TaskSourceManual})
	_ = store.SaveRun(ctx, &RunEntry{Run: state.TaskRun{RunID: "r1", TaskID: "task-runs", AgentID: "a1", Attempt: 1, Status: state.TaskRunStatusQueued}, Source: TaskSourceManual, CreatedAt: 100})
	_ = store.SaveRun(ctx, &RunEntry{Run: state.TaskRun{RunID: "r2", TaskID: "task-runs", AgentID: "a1", Attempt: 2, Status: state.TaskRunStatusRunning, StartedAt: 110}, Source: TaskSourceManual, CreatedAt: 110})
	_ = store.SaveRun(ctx, &RunEntry{Run: state.TaskRun{RunID: "r3", TaskID: "task-runs", AgentID: "a2", Attempt: 3, Status: state.TaskRunStatusFailed, EndedAt: 120}, Source: TaskSourceManual, CreatedAt: 120})

	entries, err := store.ListRuns(ctx, ListRunsOptions{TaskID: "task-runs", Status: []state.TaskRunStatus{state.TaskRunStatusRunning, state.TaskRunStatusFailed}, AgentID: "a1", OrderBy: "started_at", Limit: 10})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(entries) != 1 || entries[0].Run.RunID != "r2" {
		t.Fatalf("unexpected filtered runs: %+v", entries)
	}
}

func TestDocsStoreDeleteTaskUnsupported(t *testing.T) {
	store := newDocsStore(t)
	if err := store.DeleteTask(context.Background(), "task-1"); err == nil {
		t.Fatal("expected delete unsupported error")
	}
}

func TestDocsStorePruneNoop(t *testing.T) {
	store := newDocsStore(t)
	pruned, err := store.Prune(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("expected noop prune count 0, got %d", pruned)
	}
}
