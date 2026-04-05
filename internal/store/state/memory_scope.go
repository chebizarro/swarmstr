package state

import "strings"

// AgentMemoryScope mirrors the canonical src agent-memory scope contract.
// metiq adapts the storage surfaces behind these scopes, but the public
// contract stays user | project | local.
type AgentMemoryScope string

const (
	AgentMemoryScopeUser    AgentMemoryScope = "user"
	AgentMemoryScopeProject AgentMemoryScope = "project"
	AgentMemoryScopeLocal   AgentMemoryScope = "local"
)

func ParseAgentMemoryScope(raw string) (AgentMemoryScope, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(AgentMemoryScopeUser):
		return AgentMemoryScopeUser, true
	case string(AgentMemoryScopeProject):
		return AgentMemoryScopeProject, true
	case string(AgentMemoryScopeLocal):
		return AgentMemoryScopeLocal, true
	default:
		return "", false
	}
}

func NormalizeAgentMemoryScope(raw string) AgentMemoryScope {
	scope, _ := ParseAgentMemoryScope(raw)
	return scope
}

func (s AgentMemoryScope) Valid() bool {
	_, ok := ParseAgentMemoryScope(string(s))
	return ok
}
