package methods

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

func DefaultAgentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.EqualFold(id, "main") {
		return "main"
	}
	return id
}

func IsKnownAgentID(ctx context.Context, docsRepo *state.DocsRepository, id string) error {
	agentID := DefaultAgentID(id)
	if agentID == "main" || docsRepo == nil {
		return nil
	}
	doc, err := docsRepo.GetAgent(ctx, agentID)
	if err == nil {
		if doc.Deleted {
			return fmt.Errorf("unknown agent id %q", agentID)
		}
		return nil
	}
	if errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("unknown agent id %q", agentID)
	}
	return fmt.Errorf("failed to get agent: %w", err)
}

func ListAgents(ctx context.Context, docsRepo *state.DocsRepository, limit int) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	agents, err := docsRepo.ListAgents(ctx, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"agents": agents}, nil
}

func ListAgentFiles(ctx context.Context, docsRepo *state.DocsRepository, agentID string, limit int) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	files, err := docsRepo.ListAgentFiles(ctx, agentID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(files))
	for _, file := range files {
		out = append(out, map[string]any{"name": file.Name, "size": len(file.Content)})
	}
	return map[string]any{"agent_id": agentID, "files": out}, nil
}

func GetAgentFile(ctx context.Context, docsRepo *state.DocsRepository, agentID, name string) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	file, err := docsRepo.GetAgentFile(ctx, agentID, name)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return map[string]any{"agent_id": agentID, "file": map[string]any{"name": name, "missing": true}}, nil
		}
		return nil, err
	}
	return map[string]any{"agent_id": agentID, "file": map[string]any{"name": file.Name, "missing": false, "content": file.Content}}, nil
}

func SetAgentFile(ctx context.Context, docsRepo *state.DocsRepository, agentID, name, content string) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	doc := state.AgentFileDoc{Version: 1, AgentID: agentID, Name: name, Content: content}
	if _, err := docsRepo.PutAgentFile(ctx, agentID, name, doc); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "agent_id": agentID, "file": map[string]any{"name": name, "missing": false, "content": content}}, nil
}
