package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"metiq/internal/store/state"
)

// DocsWorkflowStore persists workflow definitions and runs in DocsRepository.
type DocsWorkflowStore struct {
	repo  *state.DocsRepository
	limit int
}

// NewDocsWorkflowStore creates a DocsRepository-backed workflow store.
func NewDocsWorkflowStore(repo *state.DocsRepository) *DocsWorkflowStore {
	return &DocsWorkflowStore{repo: repo, limit: 1000}
}

func (s *DocsWorkflowStore) LoadDefinitions(ctx context.Context) ([]*WorkflowDefinition, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("workflow docs repository is required")
	}
	docs, err := s.repo.ListWorkflowDefinitions(ctx, s.limit)
	if err != nil {
		return nil, err
	}
	defs := make([]*WorkflowDefinition, 0, len(docs))
	for _, doc := range docs {
		var def WorkflowDefinition
		if err := json.Unmarshal(doc.Definition, &def); err != nil {
			continue
		}
		if def.ID == "" {
			def.ID = doc.WorkflowID
		}
		defs = append(defs, &def)
	}
	return defs, nil
}

func (s *DocsWorkflowStore) LoadRuns(ctx context.Context) ([]*WorkflowRun, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("workflow docs repository is required")
	}
	docs, err := s.repo.ListWorkflowRuns(ctx, "", s.limit)
	if err != nil {
		return nil, err
	}
	runs := make([]*WorkflowRun, 0, len(docs))
	for _, doc := range docs {
		var run WorkflowRun
		if err := json.Unmarshal(doc.Run, &run); err != nil {
			continue
		}
		if run.RunID == "" {
			run.RunID = doc.RunID
		}
		if run.WorkflowID == "" {
			run.WorkflowID = doc.WorkflowID
		}
		runs = append(runs, &run)
	}
	return runs, nil
}

func (s *DocsWorkflowStore) SaveDefinition(ctx context.Context, def *WorkflowDefinition) error {
	if s.repo == nil {
		return fmt.Errorf("workflow docs repository is required")
	}
	if def == nil {
		return fmt.Errorf("workflow definition is required")
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return err
	}
	_, err = s.repo.PutWorkflowDefinition(ctx, state.WorkflowDefinitionDoc{
		Version:      1,
		WorkflowID:   def.ID,
		Name:         def.Name,
		Definition:   raw,
		DefinitionAt: def.UpdatedAt,
		UpdatedAt:    def.UpdatedAt,
	})
	return err
}

func (s *DocsWorkflowStore) SaveRun(ctx context.Context, run *WorkflowRun) error {
	if s.repo == nil {
		return fmt.Errorf("workflow docs repository is required")
	}
	if run == nil {
		return fmt.Errorf("workflow run is required")
	}
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = s.repo.PutWorkflowRun(ctx, state.WorkflowRunDoc{
		Version:    1,
		RunID:      run.RunID,
		WorkflowID: run.WorkflowID,
		Status:     string(run.Status),
		Run:        raw,
		StartedAt:  run.StartedAt,
		EndedAt:    run.EndedAt,
		UpdatedAt:  run.UpdatedAt,
	})
	return err
}

var _ WorkflowStore = (*DocsWorkflowStore)(nil)
