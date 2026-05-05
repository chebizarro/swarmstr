package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"metiq/internal/nostr/events"
	"metiq/internal/nostr/secure"
)

type DocsRepository struct {
	store  NostrStateStore
	author string
	codec  secure.EnvelopeCodec
}

func NewDocsRepository(store NostrStateStore, authorPubKey string) *DocsRepository {
	return NewDocsRepositoryWithCodec(store, authorPubKey, nil)
}

func NewDocsRepositoryWithCodec(store NostrStateStore, authorPubKey string, codec secure.EnvelopeCodec) *DocsRepository {
	return &DocsRepository{store: store, author: authorPubKey, codec: ensureCodec(codec)}
}

func (r *DocsRepository) PutConfig(ctx context.Context, doc ConfigDoc) (Event, error) {
	return r.putStateDoc(ctx, "metiq:config", "config_doc", doc)
}

func (r *DocsRepository) GetConfig(ctx context.Context) (ConfigDoc, error) {
	var out ConfigDoc
	if err := r.getStateDoc(ctx, "metiq:config", &out); err != nil {
		return ConfigDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) GetConfigWithEvent(ctx context.Context) (ConfigDoc, Event, error) {
	var out ConfigDoc
	evt, err := r.getStateDocWithEvent(ctx, "metiq:config", &out)
	if err != nil {
		return ConfigDoc{}, Event{}, err
	}
	return out, evt, nil
}

func (r *DocsRepository) PutSession(ctx context.Context, sessionID string, doc SessionDoc) (Event, error) {
	tags := [][]string{{"t", "session"}, {"session", protectedTagValue(sessionID)}}
	if peer := protectedTagValue(doc.PeerPubKey); peer != "" {
		tags = append(tags, []string{"peer", peer})
	}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("metiq:session:%s", sessionID), "session_doc", doc, tags)
}

func (r *DocsRepository) GetSession(ctx context.Context, sessionID string) (SessionDoc, error) {
	var out SessionDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:session:%s", sessionID), &out); err != nil {
		return SessionDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) ListSessions(ctx context.Context, limit int) ([]SessionDoc, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative")
	}
	if limit == 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "session", limit*3)
	if err != nil {
		return nil, err
	}
	type latestSessionDoc struct {
		doc   SessionDoc
		event Event
	}
	bySession := make(map[string]latestSessionDoc, len(rows))
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "session") {
			continue
		}
		var doc SessionDoc
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc.SessionID = strings.TrimSpace(doc.SessionID)
		if doc.SessionID == "" {
			continue
		}
		if prior, ok := bySession[doc.SessionID]; !ok || row.CreatedAt > prior.event.CreatedAt || (row.CreatedAt == prior.event.CreatedAt && row.ID > prior.event.ID) {
			bySession[doc.SessionID] = latestSessionDoc{doc: doc, event: row}
		}
	}
	out := make([]SessionDoc, 0, len(bySession))
	for _, entry := range bySession {
		out = append(out, entry.doc)
	}
	sort.Slice(out, func(i, j int) bool {
		ai := sessionActivityUnix(out[i])
		aj := sessionActivityUnix(out[j])
		if ai == aj {
			return out[i].SessionID < out[j].SessionID
		}
		return ai > aj
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) PutList(ctx context.Context, listName string, doc ListDoc) (Event, error) {
	return r.putStateDoc(ctx, fmt.Sprintf("metiq:list:%s", listName), "list_doc", doc)
}

func (r *DocsRepository) GetList(ctx context.Context, listName string) (ListDoc, error) {
	var out ListDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:list:%s", listName), &out); err != nil {
		return ListDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) GetListWithEvent(ctx context.Context, listName string) (ListDoc, Event, error) {
	var out ListDoc
	evt, err := r.getStateDocWithEvent(ctx, fmt.Sprintf("metiq:list:%s", listName), &out)
	if err != nil {
		return ListDoc{}, Event{}, err
	}
	return out, evt, nil
}

func (r *DocsRepository) PutCheckpoint(ctx context.Context, name string, doc CheckpointDoc) (Event, error) {
	return r.putStateDoc(ctx, fmt.Sprintf("metiq:checkpoint:%s", name), "checkpoint_doc", doc)
}

func (r *DocsRepository) GetCheckpoint(ctx context.Context, name string) (CheckpointDoc, error) {
	var out CheckpointDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:checkpoint:%s", name), &out); err != nil {
		return CheckpointDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) PutAgent(ctx context.Context, agentID string, doc AgentDoc) (Event, error) {
	tags := [][]string{{"t", "agent"}, {"agent", protectedTagValue(agentID)}}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("metiq:agent:%s", agentID), "agent_doc", doc, tags)
}

func (r *DocsRepository) GetAgent(ctx context.Context, agentID string) (AgentDoc, error) {
	var out AgentDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:agent:%s", agentID), &out); err != nil {
		return AgentDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) ListAgents(ctx context.Context, limit int) ([]AgentDoc, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative")
	}
	if limit == 0 {
		limit = 100
	}
	type latestAgentDoc struct {
		doc   AgentDoc
		event Event
	}
	byID := make(map[string]latestAgentDoc, limit)
	pageLimit := limit * 4
	var cursor *EventPageCursor
	for {
		page, err := r.store.ListByTagForAuthorPage(ctx, events.KindStateDoc, r.author, "t", "agent", pageLimit, cursor)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Events {
			if !hasTagValue(row.Tags, "t", "agent") {
				continue
			}
			var doc AgentDoc
			if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
				continue
			}
			doc.AgentID = strings.TrimSpace(doc.AgentID)
			if doc.AgentID == "" {
				doc.AgentID = strings.TrimSpace(tagValue(row.Tags, "agent"))
			}
			if doc.AgentID == "" {
				continue
			}
			if prior, ok := byID[doc.AgentID]; !ok || eventIsNewer(row, prior.event) {
				byID[doc.AgentID] = latestAgentDoc{doc: doc, event: row}
			}
		}
		if len(byID) >= limit || page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	out := make([]AgentDoc, 0, len(byID))
	for _, entry := range byID {
		out = append(out, entry.doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) PutAgentFile(ctx context.Context, agentID string, name string, doc AgentFileDoc) (Event, error) {
	tags := [][]string{{"t", "agent_file"}, {"agent", protectedTagValue(agentID)}, {"name", protectedTagValue(name)}}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("metiq:agent:%s:file:%s", agentID, name), "agent_file_doc", doc, tags)
}

func (r *DocsRepository) PutTask(ctx context.Context, doc TaskSpec) (Event, error) {
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Event{}, err
	}
	tags := [][]string{{"t", "task"}, {"task", protectedTagValue(doc.TaskID)}, {"status", string(doc.Status)}, {"priority", string(doc.Priority)}}
	if goal := protectedTagValue(doc.GoalID); goal != "" {
		tags = append(tags, []string{"goal", goal})
	}
	if parent := protectedTagValue(doc.ParentTaskID); parent != "" {
		tags = append(tags, []string{"parent", parent})
	}
	if agent := protectedTagValue(doc.AssignedAgent); agent != "" {
		tags = append(tags, []string{"agent", agent})
	}
	if session := protectedTagValue(doc.SessionID); session != "" {
		tags = append(tags, []string{"session", session})
	}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("metiq:task:%s", doc.TaskID), "task_doc", doc, tags)
}

func (r *DocsRepository) GetTask(ctx context.Context, taskID string) (TaskSpec, error) {
	var out TaskSpec
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:task:%s", taskID), &out); err != nil {
		return TaskSpec{}, err
	}
	return out.Normalize(), nil
}

func (r *DocsRepository) ListTasks(ctx context.Context, limit int) ([]TaskSpec, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "task", limit*4)
	if err != nil {
		return nil, err
	}
	type latestTaskDoc struct {
		doc   TaskSpec
		event Event
	}
	byID := make(map[string]latestTaskDoc, len(rows))
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "task") {
			continue
		}
		var doc TaskSpec
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc = doc.Normalize()
		doc.TaskID = firstNonEmpty(strings.TrimSpace(doc.TaskID), strings.TrimSpace(tagValue(row.Tags, "task")))
		if doc.TaskID == "" {
			continue
		}
		if prior, ok := byID[doc.TaskID]; !ok || row.CreatedAt > prior.event.CreatedAt || (row.CreatedAt == prior.event.CreatedAt && row.ID > prior.event.ID) {
			byID[doc.TaskID] = latestTaskDoc{doc: doc, event: row}
		}
	}
	out := make([]TaskSpec, 0, len(byID))
	for _, entry := range byID {
		out = append(out, entry.doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) PutTaskRun(ctx context.Context, doc TaskRun) (Event, error) {
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Event{}, err
	}
	tags := [][]string{{"t", "task_run"}, {"run", protectedTagValue(doc.RunID)}, {"task", protectedTagValue(doc.TaskID)}, {"status", string(doc.Status)}, {"attempt", fmt.Sprintf("%d", doc.Attempt)}}
	if goal := protectedTagValue(doc.GoalID); goal != "" {
		tags = append(tags, []string{"goal", goal})
	}
	if agent := protectedTagValue(doc.AgentID); agent != "" {
		tags = append(tags, []string{"agent", agent})
	}
	if session := protectedTagValue(doc.SessionID); session != "" {
		tags = append(tags, []string{"session", session})
	}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("metiq:task_run:%s", doc.RunID), "task_run_doc", doc, tags)
}

func (r *DocsRepository) GetTaskRun(ctx context.Context, runID string) (TaskRun, error) {
	var out TaskRun
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:task_run:%s", runID), &out); err != nil {
		return TaskRun{}, err
	}
	return out.Normalize(), nil
}

func (r *DocsRepository) ListTaskRuns(ctx context.Context, taskID string, limit int) ([]TaskRun, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "task_run", limit*6)
	if err != nil {
		return nil, err
	}
	taskTag := protectedTagValue(taskID)
	type latestTaskRunDoc struct {
		doc   TaskRun
		event Event
	}
	byID := make(map[string]latestTaskRunDoc, len(rows))
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "task_run") {
			continue
		}
		if taskTag != "" && tagValue(row.Tags, "task") != taskTag {
			continue
		}
		var doc TaskRun
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc = doc.Normalize()
		doc.RunID = firstNonEmpty(strings.TrimSpace(doc.RunID), strings.TrimSpace(tagValue(row.Tags, "run")))
		if doc.RunID == "" {
			continue
		}
		if prior, ok := byID[doc.RunID]; !ok || row.CreatedAt > prior.event.CreatedAt || (row.CreatedAt == prior.event.CreatedAt && row.ID > prior.event.ID) {
			byID[doc.RunID] = latestTaskRunDoc{doc: doc, event: row}
		}
	}
	out := make([]TaskRun, 0, len(byID))
	for _, entry := range byID {
		out = append(out, entry.doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Attempt == out[j].Attempt {
			return out[i].RunID < out[j].RunID
		}
		return out[i].Attempt > out[j].Attempt
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) PutPlan(ctx context.Context, doc PlanSpec) (Event, error) {
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Event{}, err
	}
	if doc.HasCycle() {
		return Event{}, fmt.Errorf("plan step dependency graph contains a cycle")
	}
	tags := [][]string{
		{"t", "plan"},
		{"plan", protectedTagValue(doc.PlanID)},
		{"status", string(doc.Status)},
		{"revision", fmt.Sprintf("%d", doc.Revision)},
	}
	if goal := protectedTagValue(doc.GoalID); goal != "" {
		tags = append(tags, []string{"goal", goal})
	}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("metiq:plan:%s", doc.PlanID), "plan_doc", doc, tags)
}

func (r *DocsRepository) GetPlan(ctx context.Context, planID string) (PlanSpec, error) {
	var out PlanSpec
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:plan:%s", planID), &out); err != nil {
		return PlanSpec{}, err
	}
	return out.Normalize(), nil
}

func (r *DocsRepository) ListPlans(ctx context.Context, goalID string, limit int) ([]PlanSpec, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "plan", limit*4)
	if err != nil {
		return nil, err
	}
	goalTag := protectedTagValue(goalID)
	type latestPlanDoc struct {
		doc   PlanSpec
		event Event
	}
	byID := make(map[string]latestPlanDoc, len(rows))
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "plan") {
			continue
		}
		if goalTag != "" && tagValue(row.Tags, "goal") != goalTag {
			continue
		}
		var doc PlanSpec
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc = doc.Normalize()
		doc.PlanID = firstNonEmpty(strings.TrimSpace(doc.PlanID), strings.TrimSpace(tagValue(row.Tags, "plan")))
		if doc.PlanID == "" {
			continue
		}
		if prior, ok := byID[doc.PlanID]; !ok || row.CreatedAt > prior.event.CreatedAt || (row.CreatedAt == prior.event.CreatedAt && row.ID > prior.event.ID) {
			byID[doc.PlanID] = latestPlanDoc{doc: doc, event: row}
		}
	}
	out := make([]PlanSpec, 0, len(byID))
	for _, entry := range byID {
		out = append(out, entry.doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].PlanID < out[j].PlanID
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) PutWorkflowJournal(ctx context.Context, doc WorkflowJournalDoc) (Event, error) {
	if strings.TrimSpace(doc.RunID) == "" {
		return Event{}, fmt.Errorf("run_id is required")
	}
	tags := [][]string{
		{"t", "workflow_journal"},
		{"run", protectedTagValue(doc.RunID)},
		{"task", protectedTagValue(doc.TaskID)},
	}
	return r.putStateDocWithTags(ctx, fmt.Sprintf("metiq:workflow_journal:%s", doc.RunID), "workflow_journal_doc", doc, tags)
}

func (r *DocsRepository) GetWorkflowJournal(ctx context.Context, runID string) (WorkflowJournalDoc, error) {
	var out WorkflowJournalDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:workflow_journal:%s", runID), &out); err != nil {
		return WorkflowJournalDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) ListWorkflowJournals(ctx context.Context, taskID string, limit int) ([]WorkflowJournalDoc, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.store.ListByTagForAuthor(ctx, events.KindStateDoc, r.author, "t", "workflow_journal", limit*4)
	if err != nil {
		return nil, err
	}
	taskTag := protectedTagValue(taskID)
	type latestJournalDoc struct {
		doc   WorkflowJournalDoc
		event Event
	}
	byRun := make(map[string]latestJournalDoc, len(rows))
	for _, row := range rows {
		if !hasTagValue(row.Tags, "t", "workflow_journal") {
			continue
		}
		if taskTag != "" && tagValue(row.Tags, "task") != taskTag {
			continue
		}
		var doc WorkflowJournalDoc
		if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
			continue
		}
		doc.RunID = firstNonEmpty(strings.TrimSpace(doc.RunID), strings.TrimSpace(tagValue(row.Tags, "run")))
		if doc.RunID == "" {
			continue
		}
		if doc.UpdatedAt == 0 {
			doc.UpdatedAt = row.CreatedAt
		}
		prev, exists := byRun[doc.RunID]
		if !exists || row.CreatedAt > prev.event.CreatedAt || (row.CreatedAt == prev.event.CreatedAt && row.ID > prev.event.ID) {
			byRun[doc.RunID] = latestJournalDoc{doc: doc, event: row}
		}
	}
	out := make([]WorkflowJournalDoc, 0, len(byRun))
	for _, entry := range byRun {
		out = append(out, entry.doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *DocsRepository) GetAgentFile(ctx context.Context, agentID string, name string) (AgentFileDoc, error) {
	var out AgentFileDoc
	if err := r.getStateDoc(ctx, fmt.Sprintf("metiq:agent:%s:file:%s", agentID, name), &out); err != nil {
		return AgentFileDoc{}, err
	}
	return out, nil
}

func (r *DocsRepository) ListAgentFiles(ctx context.Context, agentID string, limit int) ([]AgentFileDoc, error) {
	if limit <= 0 {
		limit = 200
	}
	agentTag := protectedTagValue(agentID)
	type latestAgentFileDoc struct {
		doc   AgentFileDoc
		event Event
	}
	byName := make(map[string]latestAgentFileDoc, limit)
	pageLimit := limit * 4
	var cursor *EventPageCursor
	for {
		page, err := r.store.ListByTagForAuthorPage(ctx, events.KindStateDoc, r.author, "t", "agent_file", pageLimit, cursor)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Events {
			if !hasTagValue(row.Tags, "t", "agent_file") {
				continue
			}
			if tagValue(row.Tags, "agent") != agentTag {
				continue
			}
			var doc AgentFileDoc
			if err := decodeEnvelopePayload(row.Content, &doc, r.codec); err != nil {
				continue
			}
			doc.Name = strings.TrimSpace(doc.Name)
			if doc.Name == "" {
				continue
			}
			if prior, ok := byName[doc.Name]; !ok || eventIsNewer(row, prior.event) {
				byName[doc.Name] = latestAgentFileDoc{doc: doc, event: row}
			}
		}
		if len(byName) >= limit || page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	out := make([]AgentFileDoc, 0, len(byName))
	for _, entry := range byName {
		out = append(out, entry.doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PutCronJobs persists an arbitrary cron jobs payload (caller-serialised JSON)
// as a replaceable state doc.  The caller is responsible for marshalling.
func (r *DocsRepository) PutCronJobs(ctx context.Context, raw json.RawMessage) (Event, error) {
	type cronJobsEnvelope struct {
		Jobs json.RawMessage `json:"jobs"`
	}
	return r.putStateDoc(ctx, "metiq:cron_jobs", "cron_jobs", cronJobsEnvelope{Jobs: raw})
}

// GetCronJobs retrieves the persisted cron jobs payload.  Returns an empty
// RawMessage (nil) and no error if not found.
func (r *DocsRepository) GetCronJobs(ctx context.Context) (json.RawMessage, error) {
	type cronJobsEnvelope struct {
		Jobs json.RawMessage `json:"jobs"`
	}
	var env cronJobsEnvelope
	if err := r.getStateDoc(ctx, "metiq:cron_jobs", &env); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return env.Jobs, nil
}

// PutWatches persists the active nostr watch subscriptions so they survive
// daemon restarts.
func (r *DocsRepository) PutWatches(ctx context.Context, raw json.RawMessage) (Event, error) {
	type watchesEnvelope struct {
		Watches json.RawMessage `json:"watches"`
	}
	return r.putStateDoc(ctx, "metiq:watches", "watches", watchesEnvelope{Watches: raw})
}

// GetWatches retrieves the persisted watch specs.  Returns nil and no error if
// nothing was stored.
func (r *DocsRepository) GetWatches(ctx context.Context) (json.RawMessage, error) {
	type watchesEnvelope struct {
		Watches json.RawMessage `json:"watches"`
	}
	var env watchesEnvelope
	if err := r.getStateDoc(ctx, "metiq:watches", &env); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return env.Watches, nil
}

// ── Feedback ────────────────────────────────────────────────────────────────

// PutFeedback persists a feedback record. Tags are added for source, severity,
// category, task, and goal to enable efficient filtered queries.
func (r *DocsRepository) PutFeedback(ctx context.Context, doc FeedbackRecord) (Event, error) {
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Event{}, fmt.Errorf("put feedback: %w", err)
	}
	dTag := "metiq:feedback:" + doc.FeedbackID
	tags := [][]string{
		{events.TagFeedback, doc.FeedbackID},
		{events.TagFeedbackSource, string(doc.Source)},
		{events.TagFeedbackSeverity, string(doc.Severity)},
		{events.TagFeedbackCategory, string(doc.Category)},
	}
	if doc.TaskID != "" {
		tags = append(tags, []string{events.TagMemTaskID, doc.TaskID})
	}
	if doc.GoalID != "" {
		tags = append(tags, []string{events.TagGoal, doc.GoalID})
	}
	if doc.RunID != "" {
		tags = append(tags, []string{events.TagRunID, doc.RunID})
	}
	if doc.StepID != "" {
		tags = append(tags, []string{events.TagStepID, doc.StepID})
	}
	return r.putStateDocWithTags(ctx, dTag, "feedback", doc, tags)
}

// GetFeedback retrieves a single feedback record by ID.
func (r *DocsRepository) GetFeedback(ctx context.Context, feedbackID string) (FeedbackRecord, error) {
	var doc FeedbackRecord
	if err := r.getStateDoc(ctx, "metiq:feedback:"+feedbackID, &doc); err != nil {
		return FeedbackRecord{}, err
	}
	return doc, nil
}

// ListFeedbackByTask returns feedback records linked to a task, newest first.
func (r *DocsRepository) ListFeedbackByTask(ctx context.Context, taskID string, limit int) ([]FeedbackRecord, error) {
	return r.listFeedbackByTag(ctx, events.TagMemTaskID, taskID, limit)
}

// ListFeedbackByGoal returns feedback records linked to a goal, newest first.
func (r *DocsRepository) ListFeedbackByGoal(ctx context.Context, goalID string, limit int) ([]FeedbackRecord, error) {
	return r.listFeedbackByTag(ctx, events.TagGoal, goalID, limit)
}

// ListFeedbackByRun returns feedback records linked to a run, newest first.
func (r *DocsRepository) ListFeedbackByRun(ctx context.Context, runID string, limit int) ([]FeedbackRecord, error) {
	return r.listFeedbackByTag(ctx, events.TagRunID, runID, limit)
}

// ListFeedbackBySource returns feedback records from a specific source.
func (r *DocsRepository) ListFeedbackBySource(ctx context.Context, source FeedbackSource, limit int) ([]FeedbackRecord, error) {
	return r.listFeedbackByTag(ctx, events.TagFeedbackSource, string(source), limit)
}

// ListFeedbackBySeverity returns feedback records at a given severity level.
func (r *DocsRepository) ListFeedbackBySeverity(ctx context.Context, severity FeedbackSeverity, limit int) ([]FeedbackRecord, error) {
	return r.listFeedbackByTag(ctx, events.TagFeedbackSeverity, string(severity), limit)
}

// ListFeedbackByStep returns feedback records linked to a specific workflow step.
func (r *DocsRepository) ListFeedbackByStep(ctx context.Context, stepID string, limit int) ([]FeedbackRecord, error) {
	return r.listFeedbackByTag(ctx, events.TagStepID, stepID, limit)
}

func (r *DocsRepository) listFeedbackByTag(ctx context.Context, tagKey, tagValue string, limit int) ([]FeedbackRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	// Use the paging API which guarantees newest-first ordering across all
	// store implementations, avoiding the "limit-before-sort" bug that
	// would occur with ListByTagForAuthor on stores without built-in
	// newest-first ordering.
	page, err := r.store.ListByTagForAuthorPage(ctx, events.KindStateDoc, r.author, tagKey, tagValue, limit, nil)
	if err != nil {
		return nil, fmt.Errorf("list feedback by %s=%s: %w", tagKey, tagValue, err)
	}

	var docs []FeedbackRecord
	for _, evt := range page.Events {
		var doc FeedbackRecord
		if err := decodeEnvelopePayload(evt.Content, &doc, r.codec); err != nil {
			continue // skip corrupt records
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// ── Proposals ───────────────────────────────────────────────────────────────

// PutProposal persists a policy/prompt proposal. Tags are added for kind,
// status, task, and goal to enable filtered queries.
func (r *DocsRepository) PutProposal(ctx context.Context, doc PolicyProposal) (Event, error) {
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Event{}, fmt.Errorf("put proposal: %w", err)
	}
	dTag := "metiq:proposal:" + doc.ProposalID
	tags := [][]string{
		{events.TagProposal, doc.ProposalID},
		{events.TagProposalKind, string(doc.Kind)},
		{events.TagProposalStatus, string(doc.Status)},
	}
	if doc.TaskID != "" {
		tags = append(tags, []string{events.TagMemTaskID, doc.TaskID})
	}
	if doc.GoalID != "" {
		tags = append(tags, []string{events.TagGoal, doc.GoalID})
	}
	if doc.RunID != "" {
		tags = append(tags, []string{events.TagRunID, doc.RunID})
	}
	return r.putStateDocWithTags(ctx, dTag, "proposal", doc, tags)
}

// GetProposal retrieves a single proposal by ID.
func (r *DocsRepository) GetProposal(ctx context.Context, proposalID string) (PolicyProposal, error) {
	var doc PolicyProposal
	if err := r.getStateDoc(ctx, "metiq:proposal:"+proposalID, &doc); err != nil {
		return PolicyProposal{}, err
	}
	return doc, nil
}

// ListProposalsByKind returns proposals of a given kind, newest first.
func (r *DocsRepository) ListProposalsByKind(ctx context.Context, kind ProposalKind, limit int) ([]PolicyProposal, error) {
	return r.listProposalsByTag(ctx, events.TagProposalKind, string(kind), limit)
}

// ListProposalsByStatus returns proposals with a given status, newest first.
func (r *DocsRepository) ListProposalsByStatus(ctx context.Context, status ProposalStatus, limit int) ([]PolicyProposal, error) {
	return r.listProposalsByTag(ctx, events.TagProposalStatus, string(status), limit)
}

// ListProposalsByTask returns proposals linked to a task, newest first.
func (r *DocsRepository) ListProposalsByTask(ctx context.Context, taskID string, limit int) ([]PolicyProposal, error) {
	return r.listProposalsByTag(ctx, events.TagMemTaskID, taskID, limit)
}

func (r *DocsRepository) listProposalsByTag(ctx context.Context, tagKey, tagValue string, limit int) ([]PolicyProposal, error) {
	if limit <= 0 {
		limit = 50
	}
	page, err := r.store.ListByTagForAuthorPage(ctx, events.KindStateDoc, r.author, tagKey, tagValue, limit, nil)
	if err != nil {
		return nil, fmt.Errorf("list proposals by %s=%s: %w", tagKey, tagValue, err)
	}

	var docs []PolicyProposal
	for _, evt := range page.Events {
		var doc PolicyProposal
		if err := decodeEnvelopePayload(evt.Content, &doc, r.codec); err != nil {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// ── Retrospectives ──────────────────────────────────────────────────────────

// PutRetrospective persists a retrospective record. Tags are added for trigger,
// outcome, task, goal, and run to enable efficient filtered queries.
func (r *DocsRepository) PutRetrospective(ctx context.Context, doc Retrospective) (Event, error) {
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Event{}, fmt.Errorf("put retrospective: %w", err)
	}
	dTag := "metiq:retro:" + doc.RetroID
	tags := [][]string{
		{events.TagRetro, doc.RetroID},
		{events.TagRetroTrigger, string(doc.Trigger)},
		{events.TagRetroOutcome, string(doc.Outcome)},
	}
	if doc.TaskID != "" {
		tags = append(tags, []string{events.TagMemTaskID, doc.TaskID})
	}
	if doc.GoalID != "" {
		tags = append(tags, []string{events.TagGoal, doc.GoalID})
	}
	if doc.RunID != "" {
		tags = append(tags, []string{events.TagRunID, doc.RunID})
	}
	return r.putStateDocWithTags(ctx, dTag, "retrospective", doc, tags)
}

// GetRetrospective retrieves a single retrospective by ID.
func (r *DocsRepository) GetRetrospective(ctx context.Context, retroID string) (Retrospective, error) {
	var doc Retrospective
	if err := r.getStateDoc(ctx, "metiq:retro:"+retroID, &doc); err != nil {
		return Retrospective{}, err
	}
	return doc, nil
}

// ListRetrospectivesByTask returns retrospectives linked to a task, newest first.
func (r *DocsRepository) ListRetrospectivesByTask(ctx context.Context, taskID string, limit int) ([]Retrospective, error) {
	return r.listRetrosByTag(ctx, events.TagMemTaskID, taskID, limit)
}

// ListRetrospectivesByGoal returns retrospectives linked to a goal, newest first.
func (r *DocsRepository) ListRetrospectivesByGoal(ctx context.Context, goalID string, limit int) ([]Retrospective, error) {
	return r.listRetrosByTag(ctx, events.TagGoal, goalID, limit)
}

// ListRetrospectivesByRun returns retrospectives linked to a run, newest first.
func (r *DocsRepository) ListRetrospectivesByRun(ctx context.Context, runID string, limit int) ([]Retrospective, error) {
	return r.listRetrosByTag(ctx, events.TagRunID, runID, limit)
}

// ListRetrospectivesByTrigger returns retrospectives with a given trigger.
func (r *DocsRepository) ListRetrospectivesByTrigger(ctx context.Context, trigger RetroTrigger, limit int) ([]Retrospective, error) {
	return r.listRetrosByTag(ctx, events.TagRetroTrigger, string(trigger), limit)
}

// ListRetrospectivesByOutcome returns retrospectives with a given outcome.
func (r *DocsRepository) ListRetrospectivesByOutcome(ctx context.Context, outcome RetroOutcome, limit int) ([]Retrospective, error) {
	return r.listRetrosByTag(ctx, events.TagRetroOutcome, string(outcome), limit)
}

func (r *DocsRepository) listRetrosByTag(ctx context.Context, tagKey, tagValue string, limit int) ([]Retrospective, error) {
	if limit <= 0 {
		limit = 50
	}
	page, err := r.store.ListByTagForAuthorPage(ctx, events.KindStateDoc, r.author, tagKey, tagValue, limit, nil)
	if err != nil {
		return nil, fmt.Errorf("list retros by %s=%s: %w", tagKey, tagValue, err)
	}

	var docs []Retrospective
	for _, evt := range page.Events {
		var doc Retrospective
		if err := decodeEnvelopePayload(evt.Content, &doc, r.codec); err != nil {
			continue // skip corrupt records
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (r *DocsRepository) putStateDoc(ctx context.Context, dTag string, typ string, value any) (Event, error) {
	return r.putStateDocWithTags(ctx, dTag, typ, value, nil)
}

func (r *DocsRepository) putStateDocWithTags(ctx context.Context, dTag string, typ string, value any, extraTags [][]string) (Event, error) {
	raw, err := encodeEnvelopePayload(typ, value, r.codec)
	if err != nil {
		return Event{}, err
	}
	return r.store.PutReplaceable(ctx, Address{
		Kind:   events.KindStateDoc,
		PubKey: r.author,
		DTag:   dTag,
	}, raw, extraTags)
}

func (r *DocsRepository) getStateDoc(ctx context.Context, dTag string, out any) error {
	_, err := r.getStateDocWithEvent(ctx, dTag, out)
	return err
}

func (r *DocsRepository) getStateDocWithEvent(ctx context.Context, dTag string, out any) (Event, error) {
	evt, err := r.store.GetLatestReplaceable(ctx, Address{
		Kind:   events.KindStateDoc,
		PubKey: r.author,
		DTag:   dTag,
	})
	if err != nil {
		return Event{}, err
	}

	if err := decodeEnvelopePayload(evt.Content, out, r.codec); err != nil {
		return Event{}, err
	}
	return evt, nil
}

func hasTagValue(tags [][]string, key, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key && tag[1] == value {
			return true
		}
	}
	return false
}

func tagValue(tags [][]string, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return tag[1]
		}
	}
	return ""
}

func sessionActivityUnix(doc SessionDoc) int64 {
	if doc.LastReplyAt > doc.LastInboundAt {
		return doc.LastReplyAt
	}
	return doc.LastInboundAt
}

func eventIsNewer(candidate, prior Event) bool {
	return candidate.CreatedAt > prior.CreatedAt || (candidate.CreatedAt == prior.CreatedAt && candidate.ID > prior.ID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
