package memory

import (
	"strings"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

const (
	memoryScopeKeywordPrefix    = "memory_scope:"
	memoryScopeAgentKeyword     = "memory_agent:"
	memoryScopeWorkspaceKeyword = "memory_workspace:"
)

// ScopedContext normalizes the worker/agent memory namespace carried through
// the runtime context.
type ScopedContext struct {
	Scope        state.AgentMemoryScope
	AgentID      string
	WorkspaceDir string
	SessionID    string
}

func ScopedContextFromAgent(scope agent.MemoryScopeContext) ScopedContext {
	return ScopedContext{
		Scope:        state.NormalizeAgentMemoryScope(scope.Scope),
		AgentID:      strings.TrimSpace(scope.AgentID),
		WorkspaceDir: strings.TrimSpace(scope.WorkspaceDir),
		SessionID:    strings.TrimSpace(scope.SessionID),
	}
}

func (s ScopedContext) Enabled() bool {
	return s.Scope.Valid() && s.AgentID != ""
}

func (s ScopedContext) keywordSet() map[string]struct{} {
	set := map[string]struct{}{}
	for _, kw := range s.Keywords() {
		set[kw] = struct{}{}
	}
	return set
}

func (s ScopedContext) Keywords() []string {
	if !s.Enabled() {
		return nil
	}
	out := []string{
		memoryScopeKeywordPrefix + string(s.Scope),
		memoryScopeAgentKeyword + s.AgentID,
	}
	if s.Scope == state.AgentMemoryScopeProject && s.WorkspaceDir != "" {
		out = append(out, memoryScopeWorkspaceKeyword+s.WorkspaceDir)
	}
	return out
}

func ApplyScope(doc state.MemoryDoc, scope ScopedContext) state.MemoryDoc {
	if !scope.Enabled() {
		return doc
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(doc.Keywords)+4)
	for _, kw := range doc.Keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		if _, ok := seen[kw]; ok {
			continue
		}
		seen[kw] = struct{}{}
		out = append(out, kw)
	}
	for _, kw := range scope.Keywords() {
		if _, ok := seen[kw]; ok {
			continue
		}
		seen[kw] = struct{}{}
		out = append(out, kw)
	}
	doc.Keywords = out
	return doc
}

func MatchScope(item IndexedMemory, scope ScopedContext) bool {
	if !scope.Enabled() {
		return true
	}
	set := map[string]struct{}{}
	for _, kw := range item.Keywords {
		set[strings.TrimSpace(kw)] = struct{}{}
	}
	if _, ok := set[memoryScopeKeywordPrefix+string(scope.Scope)]; !ok {
		return false
	}
	if _, ok := set[memoryScopeAgentKeyword+scope.AgentID]; !ok {
		return false
	}
	switch scope.Scope {
	case state.AgentMemoryScopeProject:
		if scope.WorkspaceDir == "" {
			return false
		}
		_, ok := set[memoryScopeWorkspaceKeyword+scope.WorkspaceDir]
		return ok
	case state.AgentMemoryScopeLocal:
		return scope.SessionID != "" && item.SessionID == scope.SessionID
	default:
		return true
	}
}

func FilterByScope(items []IndexedMemory, scope ScopedContext) []IndexedMemory {
	if !scope.Enabled() || len(items) == 0 {
		return items
	}
	out := make([]IndexedMemory, 0, len(items))
	for _, item := range items {
		if MatchScope(item, scope) {
			out = append(out, item)
		}
	}
	return out
}
