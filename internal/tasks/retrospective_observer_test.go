package tasks

import (
	"context"
	"testing"
	"time"

	"metiq/internal/planner"
	"metiq/internal/store/state"
)

func TestRetrospectiveObserver_PersistsOnTerminalRunWhenEnabled(t *testing.T) {
	repo := &retroRepoStub{
		tasks: map[string]state.TaskSpec{
			"task-1": {
				TaskID: "task-1",
				Meta: map[string]any{
					"learning_config": map[string]any{"auto_retrospective": true},
				},
			},
		},
	}
	obs := NewRetrospectiveObserver(repo, RetroObserverConfig{
		Enabled:  false,
		Policy:   planner.AllRetroPolicy(),
		IDPrefix: "test-retro",
	})

	run := state.TaskRun{RunID: "run-1", TaskID: "task-1", Status: state.TaskRunStatusCompleted, StartedAt: 10, EndedAt: 20}
	obs.OnRunUpdated(context.Background(), RunEntry{Run: run}, state.TaskRunTransition{To: state.TaskRunStatusCompleted, At: 20})

	if repo.putRetrosCalls != 1 {
		t.Fatalf("expected 1 retrospective persisted, got %d", repo.putRetrosCalls)
	}
	if len(repo.retrosByRun["run-1"]) != 1 {
		t.Fatalf("expected retrospective indexed by run")
	}
}

func TestRetrospectiveObserver_DoesNotPersistWhenDisabled(t *testing.T) {
	repo := &retroRepoStub{
		tasks: map[string]state.TaskSpec{
			"task-1": {TaskID: "task-1"},
		},
	}
	obs := NewRetrospectiveObserver(repo, RetroObserverConfig{Enabled: false, Policy: planner.AllRetroPolicy()})

	run := state.TaskRun{RunID: "run-1", TaskID: "task-1", Status: state.TaskRunStatusFailed, StartedAt: 10, EndedAt: 20}
	obs.OnRunUpdated(context.Background(), RunEntry{Run: run}, state.TaskRunTransition{To: state.TaskRunStatusFailed, At: 20})

	if repo.putRetrosCalls != 0 {
		t.Fatalf("expected no retrospective persisted, got %d", repo.putRetrosCalls)
	}
}

func TestRetrospectiveObserver_BoundedAndDeduped(t *testing.T) {
	repo := &retroRepoStub{
		tasks: map[string]state.TaskSpec{
			"task-1": {TaskID: "task-1", Meta: map[string]any{"learning_config": map[string]any{"auto_retrospective": true}}},
		},
		retrosByRun: map[string][]state.Retrospective{
			"run-existing": {{RetroID: "retro-existing", RunID: "run-existing", TaskID: "task-1", Trigger: state.RetroTriggerRunFailed, Outcome: state.RetroOutcomeFailure, Summary: "existing", CreatedAt: 100}},
		},
		retrosByTask: map[string][]state.Retrospective{
			"task-1": {{RetroID: "retro-recent", RunID: "run-recent", TaskID: "task-1", Trigger: state.RetroTriggerRunFailed, Outcome: state.RetroOutcomeFailure, Summary: "recent", CreatedAt: 150}},
		},
	}
	obs := NewRetrospectiveObserver(repo, RetroObserverConfig{Enabled: true, Policy: planner.AllRetroPolicy(), MinTaskSpacing: time.Hour})

	obs.OnRunUpdated(context.Background(), RunEntry{Run: state.TaskRun{RunID: "run-existing", TaskID: "task-1", Status: state.TaskRunStatusFailed, StartedAt: 10, EndedAt: 200}}, state.TaskRunTransition{To: state.TaskRunStatusFailed, At: 200})
	obs.OnRunUpdated(context.Background(), RunEntry{Run: state.TaskRun{RunID: "run-new", TaskID: "task-1", Status: state.TaskRunStatusFailed, StartedAt: 10, EndedAt: 300}}, state.TaskRunTransition{To: state.TaskRunStatusFailed, At: 300})

	if repo.putRetrosCalls != 0 {
		t.Fatalf("expected no new retrospectives due to dedupe/spacing, got %d", repo.putRetrosCalls)
	}
}

func TestLedgerAddRetrospectiveObserverRegistersObserver(t *testing.T) {
	ledger := NewLedger(nil)
	repo := &retroRepoStub{}
	obs := ledger.AddRetrospectiveObserver(repo, RetroObserverConfig{Enabled: true})
	if obs == nil {
		t.Fatal("expected observer")
	}
	if len(ledger.observers) != 1 {
		t.Fatalf("expected 1 observer, got %d", len(ledger.observers))
	}
	if _, ok := ledger.observers[0].(*RetrospectiveObserver); !ok {
		t.Fatalf("expected retrospective observer, got %T", ledger.observers[0])
	}
}

type retroRepoStub struct {
	tasks          map[string]state.TaskSpec
	retrosByRun    map[string][]state.Retrospective
	retrosByTask   map[string][]state.Retrospective
	putRetrosCalls int
}

func (s *retroRepoStub) GetTask(ctx context.Context, taskID string) (state.TaskSpec, error) {
	if s.tasks == nil {
		return state.TaskSpec{}, state.ErrNotFound
	}
	t, ok := s.tasks[taskID]
	if !ok {
		return state.TaskSpec{}, state.ErrNotFound
	}
	return t, nil
}

func (s *retroRepoStub) PutRetrospective(ctx context.Context, doc state.Retrospective) (state.Event, error) {
	s.putRetrosCalls++
	if s.retrosByRun == nil {
		s.retrosByRun = map[string][]state.Retrospective{}
	}
	if s.retrosByTask == nil {
		s.retrosByTask = map[string][]state.Retrospective{}
	}
	s.retrosByRun[doc.RunID] = append([]state.Retrospective{doc}, s.retrosByRun[doc.RunID]...)
	s.retrosByTask[doc.TaskID] = append([]state.Retrospective{doc}, s.retrosByTask[doc.TaskID]...)
	return state.Event{}, nil
}

func (s *retroRepoStub) ListRetrospectivesByRun(ctx context.Context, runID string, limit int) ([]state.Retrospective, error) {
	retros := s.retrosByRun[runID]
	if limit > 0 && len(retros) > limit {
		retros = retros[:limit]
	}
	return retros, nil
}

func (s *retroRepoStub) ListRetrospectivesByTask(ctx context.Context, taskID string, limit int) ([]state.Retrospective, error) {
	retros := s.retrosByTask[taskID]
	if limit > 0 && len(retros) > limit {
		retros = retros[:limit]
	}
	return retros, nil
}
