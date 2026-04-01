package memory

import (
	"testing"

	"metiq/internal/store/state"
)

func TestApplyScopeAddsCanonicalKeywords(t *testing.T) {
	projectScope := ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/worktree",
	}
	projectDoc := ApplyScope(state.MemoryDoc{
		MemoryID: "m1",
		Text:     "project note",
		Keywords: []string{"project", "project"},
	}, projectScope)
	for _, want := range []string{
		"project",
		"memory_scope:project",
		"memory_agent:builder",
		"memory_workspace:/tmp/worktree",
	} {
		if !containsString(projectDoc.Keywords, want) {
			t.Fatalf("expected scoped keyword %q in %#v", want, projectDoc.Keywords)
		}
	}

	localScope := ScopedContext{
		Scope:     state.AgentMemoryScopeLocal,
		AgentID:   "builder",
		SessionID: "sess-1",
	}
	localDoc := ApplyScope(state.MemoryDoc{MemoryID: "m2", Text: "local note"}, localScope)
	if containsString(localDoc.Keywords, "memory_workspace:/tmp/worktree") {
		t.Fatalf("did not expect workspace keyword on local scope: %#v", localDoc.Keywords)
	}
	for _, want := range []string{
		"memory_scope:local",
		"memory_agent:builder",
	} {
		if !containsString(localDoc.Keywords, want) {
			t.Fatalf("expected scoped keyword %q in %#v", want, localDoc.Keywords)
		}
	}
}

func TestMatchScope_ProjectAndLocalPolicies(t *testing.T) {
	projectScope := ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/worktree",
	}
	projectDoc := ApplyScope(state.MemoryDoc{
		MemoryID: "p1",
		Text:     "project memory",
	}, projectScope)
	projectItem := IndexedMemory{
		MemoryID: projectDoc.MemoryID,
		Text:     projectDoc.Text,
		Keywords: append([]string(nil), projectDoc.Keywords...),
	}
	if !MatchScope(projectItem, projectScope) {
		t.Fatal("expected project-scoped item to match its project scope")
	}
	if MatchScope(projectItem, ScopedContext{
		Scope:        state.AgentMemoryScopeProject,
		AgentID:      "builder",
		WorkspaceDir: "/tmp/other",
	}) {
		t.Fatal("expected project-scoped item to reject a different workspace")
	}

	localScope := ScopedContext{
		Scope:     state.AgentMemoryScopeLocal,
		AgentID:   "builder",
		SessionID: "sess-1",
	}
	localDoc := ApplyScope(state.MemoryDoc{
		MemoryID:  "l1",
		SessionID: "sess-1",
		Text:      "local memory",
	}, localScope)
	localItem := IndexedMemory{
		MemoryID:  localDoc.MemoryID,
		SessionID: localDoc.SessionID,
		Text:      localDoc.Text,
		Keywords:  append([]string(nil), localDoc.Keywords...),
	}
	if !MatchScope(localItem, localScope) {
		t.Fatal("expected local-scoped item to match its own session")
	}
	if MatchScope(localItem, ScopedContext{
		Scope:     state.AgentMemoryScopeLocal,
		AgentID:   "builder",
		SessionID: "sess-2",
	}) {
		t.Fatal("expected local-scoped item to reject a different session")
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
