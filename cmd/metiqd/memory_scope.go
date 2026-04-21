package main

import (
	"context"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/memory"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

func configuredAgentMemoryScope(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, agentID string) state.AgentMemoryScope {
	agentID = defaultAgentID(agentID)
	if docsRepo != nil {
		if doc, err := docsRepo.GetAgent(ctx, agentID); err == nil && doc.Meta != nil {
			if raw, ok := doc.Meta["memory_scope"].(string); ok {
				if scope := state.NormalizeAgentMemoryScope(raw); scope.Valid() {
					return scope
				}
			}
		}
	}
	for _, agCfg := range cfg.Agents {
		if strings.TrimSpace(agCfg.ID) != agentID {
			continue
		}
		if agCfg.MemoryScope.Valid() {
			return agCfg.MemoryScope
		}
		break
	}
	return ""
}

func workspaceDirForAgent(cfg state.ConfigDoc, agentID string) string {
	return workspace.ResolveWorkspaceDir(cfg, agentID)
}

func resolveMemoryScopeContext(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, sessionStore *state.SessionStore, sessionID, explicitAgentID string, explicitScope state.AgentMemoryScope) memory.ScopedContext {
	sessionID = strings.TrimSpace(sessionID)
	agentID := defaultAgentID(explicitAgentID)
	scope := explicitScope
	var sessionWorkspaceDir string
	if sessionStore != nil && sessionID != "" {
		if se, ok := sessionStore.Get(sessionID); ok {
			if agentID == "main" && strings.TrimSpace(explicitAgentID) == "" && strings.TrimSpace(se.AgentID) != "" {
				agentID = defaultAgentID(se.AgentID)
			}
			if !scope.Valid() && se.MemoryScope.Valid() {
				scope = se.MemoryScope
			}
			sessionWorkspaceDir = strings.TrimSpace(se.SpawnedWorkspace)
		}
	}
	if agentID == "main" && strings.TrimSpace(explicitAgentID) == "" && controlServices != nil && controlServices.session.sessionRouter != nil && sessionID != "" {
		if routed := strings.TrimSpace(controlServices.session.sessionRouter.Get(sessionID)); routed != "" {
			agentID = defaultAgentID(routed)
		}
	}
	if !scope.Valid() {
		scope = configuredAgentMemoryScope(ctx, cfg, docsRepo, agentID)
	}
	resolved := memory.ScopedContext{
		Scope:     scope,
		AgentID:   agentID,
		SessionID: sessionID,
	}
	if !resolved.Enabled() {
		return memory.ScopedContext{}
	}
	if resolved.Scope == state.AgentMemoryScopeProject || resolved.Scope == state.AgentMemoryScopeLocal {
		resolved.WorkspaceDir = workspaceDirForAgent(cfg, agentID)
		if resolved.Scope == state.AgentMemoryScopeLocal && strings.TrimSpace(sessionWorkspaceDir) != "" {
			resolved.WorkspaceDir = sessionWorkspaceDir
		}
	}
	if resolved.Scope == state.AgentMemoryScopeProject {
		if strings.TrimSpace(resolved.WorkspaceDir) == "" {
			return memory.ScopedContext{}
		}
	}
	if resolved.Scope == state.AgentMemoryScopeLocal {
		if resolved.SessionID == "" || strings.TrimSpace(sessionWorkspaceDir) == "" {
			return memory.ScopedContext{}
		}
	}
	return resolved
}

func contextWithMemoryScope(ctx context.Context, scope memory.ScopedContext) context.Context {
	if !scope.Enabled() {
		return ctx
	}
	return agent.ContextWithMemoryScope(ctx, agent.MemoryScopeContext{
		Scope:        string(scope.Scope),
		AgentID:      scope.AgentID,
		WorkspaceDir: scope.WorkspaceDir,
		SessionID:    scope.SessionID,
	})
}

func scopedMemoryDocs(docs []state.MemoryDoc, scope memory.ScopedContext) []state.MemoryDoc {
	if len(docs) == 0 || !scope.Enabled() {
		return docs
	}
	out := make([]state.MemoryDoc, len(docs))
	for i, doc := range docs {
		out[i] = memory.ApplyScope(doc, scope)
	}
	return out
}

func persistSessionMemoryScope(sessionStore *state.SessionStore, sessionID, agentID string, scope state.AgentMemoryScope) {
	if sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	entry := sessionStore.GetOrNew(sessionID)
	if strings.TrimSpace(agentID) != "" {
		entry.AgentID = defaultAgentID(agentID)
	}
	if scope.Valid() {
		entry.MemoryScope = scope
	}
	_ = sessionStore.Put(sessionID, entry)
}
