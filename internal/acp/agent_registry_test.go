package acp

import (
	"sync"
	"testing"
)

func TestAgentRegistry_RegisterAndResolve(t *testing.T) {
	r := NewAgentRegistry()
	err := r.Register(AgentEntry{ID: "Codex", Command: "/usr/bin/codex"})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := r.Resolve("codex")
	if !ok {
		t.Fatal("expected to resolve codex")
	}
	if e.Command != "/usr/bin/codex" {
		t.Fatalf("command = %q, want /usr/bin/codex", e.Command)
	}
	if e.ID != "codex" {
		t.Fatalf("id should be normalized to lowercase, got %q", e.ID)
	}
}

func TestAgentRegistry_IDNormalization(t *testing.T) {
	r := NewAgentRegistry()
	_ = r.Register(AgentEntry{ID: "  Claude  ", Command: "claude"})
	_, ok := r.Resolve("claude")
	if !ok {
		t.Fatal("should resolve trimmed lowercase id")
	}
	_, ok = r.Resolve("CLAUDE")
	if !ok {
		t.Fatal("should resolve case-insensitive")
	}
}

func TestAgentRegistry_EmptyID(t *testing.T) {
	r := NewAgentRegistry()
	err := r.Register(AgentEntry{ID: "", Command: "agent"})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestAgentRegistry_EmptyCommand(t *testing.T) {
	r := NewAgentRegistry()
	err := r.Register(AgentEntry{ID: "test", Command: ""})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestAgentRegistry_Unregister(t *testing.T) {
	r := NewAgentRegistry()
	_ = r.Register(AgentEntry{ID: "test", Command: "cmd"})
	r.Unregister("test")
	_, ok := r.Resolve("test")
	if ok {
		t.Fatal("should not resolve after unregister")
	}
}

func TestAgentRegistry_ReplaceExisting(t *testing.T) {
	r := NewAgentRegistry()
	_ = r.Register(AgentEntry{ID: "agent", Command: "old"})
	_ = r.Register(AgentEntry{ID: "agent", Command: "new"})
	e, ok := r.Resolve("agent")
	if !ok {
		t.Fatal("expected to resolve agent")
	}
	if e.Command != "new" {
		t.Fatalf("command = %q, want new", e.Command)
	}
}

func TestAgentRegistry_List(t *testing.T) {
	r := NewAgentRegistry()
	_ = r.Register(AgentEntry{ID: "alpha", Command: "a"})
	_ = r.Register(AgentEntry{ID: "beta", Command: "b"})
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
}

func TestAgentRegistry_Count(t *testing.T) {
	r := NewAgentRegistry()
	if r.Count() != 0 {
		t.Fatalf("empty registry count = %d", r.Count())
	}
	_ = r.Register(AgentEntry{ID: "x", Command: "y"})
	if r.Count() != 1 {
		t.Fatalf("count = %d after register", r.Count())
	}
}

func TestAgentRegistry_WithMCPServers(t *testing.T) {
	r := NewAgentRegistry()
	_ = r.Register(AgentEntry{
		ID:      "agent",
		Command: "cmd",
		MCPServers: []MCPServerConfig{
			{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}},
		},
	})
	e, ok := r.Resolve("agent")
	if !ok {
		t.Fatal("expected to resolve agent")
	}
	if len(e.MCPServers) != 1 {
		t.Fatalf("mcp servers = %d, want 1", len(e.MCPServers))
	}
	if e.MCPServers[0].Name != "fs" {
		t.Fatalf("mcp server name = %q", e.MCPServers[0].Name)
	}
}

func TestAgentRegistry_WithTags(t *testing.T) {
	r := NewAgentRegistry()
	_ = r.Register(AgentEntry{
		ID:      "agent",
		Command: "cmd",
		Tags:    map[string]string{"capability": "code", "region": "us-east"},
	})
	e, _ := r.Resolve("agent")
	if e.Tags["capability"] != "code" {
		t.Fatalf("tag capability = %q", e.Tags["capability"])
	}
}

func TestAgentRegistry_ResolveNotFound(t *testing.T) {
	r := NewAgentRegistry()
	_, ok := r.Resolve("nonexistent")
	if ok {
		t.Fatal("should not resolve unknown agent")
	}
}

func TestAgentRegistry_ConcurrentAccess(t *testing.T) {
	r := NewAgentRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "agent"
			_ = r.Register(AgentEntry{ID: id, Command: "cmd"})
			r.Resolve(id)
			r.List()
			r.Count()
		}(i)
	}
	wg.Wait()
}
