package tasks

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"metiq/internal/store/state"
)

const (
	docsStoreDefaultLimit = 1000
	metaLedgerSource      = "tasks.ledger.source"
	metaLedgerSourceRef   = "tasks.ledger.source_ref"
	metaLedgerCreatedAt   = "tasks.ledger.created_at"
	metaLedgerUpdatedAt   = "tasks.ledger.updated_at"
)

// DocsStore implements Store over state.DocsRepository.
type DocsStore struct {
	repo *state.DocsRepository
}

func NewDocsStore(repo *state.DocsRepository) *DocsStore {
	return &DocsStore{repo: repo}
}

func (s *DocsStore) SaveTask(ctx context.Context, entry *LedgerEntry) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("docs store repository is nil")
	}
	if entry == nil {
		return fmt.Errorf("task entry is nil")
	}

	task := entry.Task
	task.Meta = cloneAnyMap(task.Meta)
	if task.Meta == nil {
		task.Meta = map[string]any{}
	}
	task.Meta[metaLedgerSource] = string(entry.Source)
	if strings.TrimSpace(entry.SourceRef) == "" {
		delete(task.Meta, metaLedgerSourceRef)
	} else {
		task.Meta[metaLedgerSourceRef] = entry.SourceRef
	}
	if entry.CreatedAt > 0 {
		task.Meta[metaLedgerCreatedAt] = entry.CreatedAt
	}
	if entry.UpdatedAt > 0 {
		task.Meta[metaLedgerUpdatedAt] = entry.UpdatedAt
	}

	_, err := s.repo.PutTask(ctx, task)
	return err
}

func (s *DocsStore) LoadTask(ctx context.Context, taskID string) (*LedgerEntry, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("docs store repository is nil")
	}

	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		if err == state.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}

	runs, err := s.repo.ListTaskRuns(ctx, taskID, docsStoreDefaultLimit)
	if err != nil {
		return nil, err
	}

	entry := ledgerEntryFromTaskDoc(task)
	entry.Runs = append(entry.Runs, runs...)
	return entry, nil
}

func (s *DocsStore) ListTasks(ctx context.Context, opts ListTasksOptions) ([]*LedgerEntry, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("docs store repository is nil")
	}

	fetchLimit := listFetchLimit(opts.Limit, opts.Offset)
	tasks, err := s.repo.ListTasks(ctx, fetchLimit)
	if err != nil {
		return nil, err
	}

	entries := make([]*LedgerEntry, 0, len(tasks))
	for _, task := range tasks {
		entry := ledgerEntryFromTaskDoc(task)
		if matchesTaskFilter(entry, opts) {
			entries = append(entries, entry)
		}
	}

	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	sortTasks(entries, opts.OrderBy, opts.OrderDesc)
	if opts.Offset >= len(entries) {
		return []*LedgerEntry{}, nil
	}
	entries = entries[opts.Offset:]
	if len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}
	return entries, nil
}

func (s *DocsStore) DeleteTask(context.Context, string) error {
	return fmt.Errorf("docs store does not support hard delete")
}

func (s *DocsStore) SaveRun(ctx context.Context, entry *RunEntry) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("docs store repository is nil")
	}
	if entry == nil {
		return fmt.Errorf("run entry is nil")
	}

	run := entry.Run
	run.Meta = cloneAnyMap(run.Meta)
	if run.Meta == nil {
		run.Meta = map[string]any{}
	}
	run.Meta[metaLedgerSource] = string(entry.Source)
	if strings.TrimSpace(entry.SourceRef) == "" {
		delete(run.Meta, metaLedgerSourceRef)
	} else {
		run.Meta[metaLedgerSourceRef] = entry.SourceRef
	}
	if entry.CreatedAt > 0 {
		run.Meta[metaLedgerCreatedAt] = entry.CreatedAt
	}
	if entry.UpdatedAt > 0 {
		run.Meta[metaLedgerUpdatedAt] = entry.UpdatedAt
	}

	_, err := s.repo.PutTaskRun(ctx, run)
	return err
}

func (s *DocsStore) LoadRun(ctx context.Context, runID string) (*RunEntry, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("docs store repository is nil")
	}

	run, err := s.repo.GetTaskRun(ctx, runID)
	if err != nil {
		if err == state.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}

	entry := runEntryFromRunDoc(run)
	if source := strings.TrimSpace(taskSourceFromMeta(run.Meta)); source != "" {
		entry.Source = TaskSource(source)
	}
	if sourceRef := taskSourceRefFromMeta(run.Meta); sourceRef != "" {
		entry.SourceRef = sourceRef
	}
	if created := int64FromMeta(run.Meta, metaLedgerCreatedAt); created > 0 {
		entry.CreatedAt = created
	}
	if updated := int64FromMeta(run.Meta, metaLedgerUpdatedAt); updated > 0 {
		entry.UpdatedAt = updated
	}

	if (entry.Source == "" || entry.SourceRef == "") && strings.TrimSpace(run.TaskID) != "" {
		task, taskErr := s.repo.GetTask(ctx, run.TaskID)
		if taskErr == nil {
			taskEntry := ledgerEntryFromTaskDoc(task)
			if entry.Source == "" {
				entry.Source = taskEntry.Source
			}
			if entry.SourceRef == "" {
				entry.SourceRef = taskEntry.SourceRef
			}
		}
	}

	return entry, nil
}

func (s *DocsStore) ListRuns(ctx context.Context, opts ListRunsOptions) ([]*RunEntry, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("docs store repository is nil")
	}

	fetchLimit := listFetchLimit(opts.Limit, opts.Offset)
	runs, err := s.repo.ListTaskRuns(ctx, opts.TaskID, fetchLimit)
	if err != nil {
		return nil, err
	}

	entries := make([]*RunEntry, 0, len(runs))
	for _, run := range runs {
		entry := runEntryFromRunDoc(run)
		if source := strings.TrimSpace(taskSourceFromMeta(run.Meta)); source != "" {
			entry.Source = TaskSource(source)
		}
		if sourceRef := taskSourceRefFromMeta(run.Meta); sourceRef != "" {
			entry.SourceRef = sourceRef
		}
		if created := int64FromMeta(run.Meta, metaLedgerCreatedAt); created > 0 {
			entry.CreatedAt = created
		}
		if updated := int64FromMeta(run.Meta, metaLedgerUpdatedAt); updated > 0 {
			entry.UpdatedAt = updated
		}
		if matchesRunFilter(entry, opts) {
			entries = append(entries, entry)
		}
	}

	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	sortRuns(entries, opts.OrderBy, opts.OrderDesc)
	if opts.Offset >= len(entries) {
		return []*RunEntry{}, nil
	}
	entries = entries[opts.Offset:]
	if len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}
	return entries, nil
}

func (s *DocsStore) Stats(ctx context.Context) (TaskStats, error) {
	tasks, err := s.ListTasks(ctx, ListTasksOptions{Limit: docsStoreDefaultLimit})
	if err != nil {
		return TaskStats{}, err
	}
	runs, err := s.ListRuns(ctx, ListRunsOptions{Limit: docsStoreDefaultLimit})
	if err != nil {
		return TaskStats{}, err
	}

	taskMap := make(map[string]*LedgerEntry, len(tasks))
	for _, task := range tasks {
		taskMap[task.Task.TaskID] = task
	}
	runMap := make(map[string]*RunEntry, len(runs))
	for _, run := range runs {
		runMap[run.Run.RunID] = run
	}

	return computeTaskStats(taskMap, runMap, time.Now()), nil
}

func (s *DocsStore) Prune(context.Context, time.Duration) (int, error) {
	return 0, nil
}

func ledgerEntryFromTaskDoc(task state.TaskSpec) *LedgerEntry {
	entry := &LedgerEntry{
		Task:      stripLedgerTaskMeta(task),
		Source:    TaskSource(taskSourceFromMeta(task.Meta)),
		SourceRef: taskSourceRefFromMeta(task.Meta),
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
	}
	if created := int64FromMeta(task.Meta, metaLedgerCreatedAt); created > 0 {
		entry.CreatedAt = created
	}
	if updated := int64FromMeta(task.Meta, metaLedgerUpdatedAt); updated > 0 {
		entry.UpdatedAt = updated
	}
	if entry.Source == "" {
		entry.Source = TaskSourceManual
	}
	return entry
}

func runEntryFromRunDoc(run state.TaskRun) *RunEntry {
	return &RunEntry{Run: stripLedgerRunMeta(run), CreatedAt: run.StartedAt, UpdatedAt: run.EndedAt}
}

func stripLedgerTaskMeta(task state.TaskSpec) state.TaskSpec {
	task.Meta = stripLedgerMeta(task.Meta)
	return task
}

func stripLedgerRunMeta(run state.TaskRun) state.TaskRun {
	run.Meta = stripLedgerMeta(run.Meta)
	return run
}

func stripLedgerMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return meta
	}
	out := cloneAnyMap(meta)
	delete(out, metaLedgerSource)
	delete(out, metaLedgerSourceRef)
	delete(out, metaLedgerCreatedAt)
	delete(out, metaLedgerUpdatedAt)
	if len(out) == 0 {
		return nil
	}
	return out
}

func taskSourceFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if raw, ok := meta[metaLedgerSource]; ok {
		if value, ok := raw.(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func taskSourceRefFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if raw, ok := meta[metaLedgerSourceRef]; ok {
		if value, ok := raw.(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func int64FromMeta(meta map[string]any, key string) int64 {
	if meta == nil {
		return 0
	}
	raw, ok := meta[key]
	if !ok {
		return 0
	}
	switch value := raw.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case jsonNumber:
		n, _ := strconv.ParseInt(string(value), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return n
	default:
		return 0
	}
}

type jsonNumber string

func listFetchLimit(limit, offset int) int {
	if limit <= 0 {
		limit = 100
	}
	fetch := limit + offset
	if fetch < docsStoreDefaultLimit {
		return docsStoreDefaultLimit
	}
	return fetch
}
