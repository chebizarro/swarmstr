package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FSWorkflowStore persists workflows using the legacy JSON directory layout.
type FSWorkflowStore struct {
	dir string
}

// NewFSWorkflowStore creates a filesystem-backed workflow store under dir.
func NewFSWorkflowStore(dir string) (*FSWorkflowStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("workflow directory is required")
	}
	if err := os.MkdirAll(filepath.Join(dir, "definitions"), 0755); err != nil {
		return nil, fmt.Errorf("create definitions directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0755); err != nil {
		return nil, fmt.Errorf("create runs directory: %w", err)
	}
	return &FSWorkflowStore{dir: dir}, nil
}

func (s *FSWorkflowStore) LoadDefinitions(ctx context.Context) ([]*WorkflowDefinition, error) {
	dir := filepath.Join(s.dir, "definitions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	defs := make([]*WorkflowDefinition, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var def WorkflowDefinition
		if err := json.Unmarshal(data, &def); err != nil {
			continue
		}
		defs = append(defs, &def)
	}
	return defs, nil
}

func (s *FSWorkflowStore) LoadRuns(ctx context.Context) ([]*WorkflowRun, error) {
	dir := filepath.Join(s.dir, "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	runs := make([]*WorkflowRun, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var run WorkflowRun
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		runs = append(runs, &run)
	}
	return runs, nil
}

func (s *FSWorkflowStore) SaveDefinition(ctx context.Context, def *WorkflowDefinition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "definitions", def.ID+".json"), data, 0644)
}

func (s *FSWorkflowStore) SaveRun(ctx context.Context, run *WorkflowRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "runs", run.RunID+".json"), data, 0644)
}

var _ WorkflowStore = (*FSWorkflowStore)(nil)
