package main

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"metiq/internal/agent"
	mcppkg "metiq/internal/mcp"
)

func TestBuildMCPToolRegistration_ProjectsDescriptor(t *testing.T) {
	mgr := mcppkg.NewManager()
	name, registration := buildMCPToolRegistration(mgr, "demo", &sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "description": "text to echo"},
				"tags": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string", "description": "tag"},
				},
			},
			"required": []any{"message"},
		},
	})

	if name != "mcp_demo_echo" {
		t.Fatalf("unexpected tool name %q", name)
	}
	if !registration.ProviderVisible || registration.Func == nil {
		t.Fatalf("expected provider-visible MCP registration, got %+v", registration)
	}
	desc := registration.Descriptor
	if desc.Description != "[MCP:demo] Echo text" {
		t.Fatalf("unexpected description: %+v", desc)
	}
	if desc.Origin.Kind != agent.ToolOriginKindMCP || desc.Origin.ServerName != "demo" || desc.Origin.CanonicalName != "echo" {
		t.Fatalf("unexpected origin: %+v", desc.Origin)
	}
	if desc.Parameters.Type != "object" || len(desc.Parameters.Required) != 1 || desc.Parameters.Required[0] != "message" {
		t.Fatalf("unexpected parameters: %+v", desc.Parameters)
	}
	if desc.Parameters.Properties["message"].Type != "string" {
		t.Fatalf("unexpected message property: %+v", desc.Parameters.Properties["message"])
	}
	if desc.Parameters.Properties["tags"].Items == nil || desc.Parameters.Properties["tags"].Items.Type != "string" {
		t.Fatalf("unexpected array items property: %+v", desc.Parameters.Properties["tags"])
	}
}

func TestPruneMCPToolsForPendingConfig_RemovesRemovedAndChangedServers(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterWithDef("memory_search", func(context.Context, map[string]any) (string, error) {
		return "[]", nil
	}, agent.ToolDefinition{Name: "memory_search", Description: "builtin"})
	reg.RegisterWithDescriptor("mcp_demo_echo", func(context.Context, map[string]any) (string, error) {
		return "echo", nil
	}, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindMCP, ServerName: "demo", CanonicalName: "echo"}})
	reg.RegisterWithDescriptor("mcp_other_ping", func(context.Context, map[string]any) (string, error) {
		return "pong", nil
	}, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindMCP, ServerName: "other", CanonicalName: "ping"}})

	removed := pruneMCPToolsForPendingConfig(reg, mcppkg.ManagerSnapshot{Enabled: true, Servers: []mcppkg.ServerStateSnapshot{
		{Name: "demo", State: mcppkg.ConnectionStateConnected, Signature: "sig-1"},
		{Name: "other", State: mcppkg.ConnectionStateConnected, Signature: "sig-old"},
	}}, mcppkg.Config{Enabled: true, Servers: map[string]mcppkg.ResolvedServerConfig{
		"demo": {Name: "demo", Signature: "sig-2", ServerConfig: mcppkg.ServerConfig{Enabled: true, Command: "npx"}},
	}})
	if removed != 2 {
		t.Fatalf("expected 2 MCP tools to be pre-pruned, got %d", removed)
	}
	if _, ok := reg.Descriptor("mcp_demo_echo"); ok {
		t.Fatal("expected changed-server MCP tool to be removed")
	}
	if _, ok := reg.Descriptor("mcp_other_ping"); ok {
		t.Fatal("expected removed-server MCP tool to be removed")
	}
	defs := reg.Definitions()
	if len(defs) != 1 || defs[0].Name != "memory_search" {
		t.Fatalf("expected builtin tool to remain after pre-prune, got %+v", defs)
	}
}

func TestReconcileMCPToolRegistry_AddUpdateRemove(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterWithDef("memory_search", func(context.Context, map[string]any) (string, error) {
		return "[]", nil
	}, agent.ToolDefinition{Name: "memory_search", Description: "builtin"})

	mgr := mcppkg.NewManager()
	var serverTools []*sdkmcp.Tool
	mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		copied := make([]*sdkmcp.Tool, len(serverTools))
		copy(copied, serverTools)
		return &mcppkg.ServerConnection{
			Name:         name,
			Tools:        copied,
			Capabilities: mcppkg.CapabilitySnapshot{Tools: true},
		}, nil
	})
	applyConfig := func(sig string) {
		t.Helper()
		if err := mgr.ApplyConfig(context.Background(), mcppkg.Config{
			Enabled: true,
			Servers: map[string]mcppkg.ResolvedServerConfig{
				"demo": {
					Name:         "demo",
					ServerConfig: mcppkg.ServerConfig{Enabled: true, Command: "npx"},
					Signature:    sig,
				},
			},
		}); err != nil {
			t.Fatalf("ApplyConfig(%q) error: %v", sig, err)
		}
	}

	serverTools = []*sdkmcp.Tool{{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}},
			"required":   []any{"message"},
		},
	}}
	applyConfig("sig-1")
	result := reconcileMCPToolRegistry(reg, mgr)
	if result.Added != 1 || result.Updated != 0 || result.Removed != 0 || result.Desired != 1 {
		t.Fatalf("unexpected initial reconcile result: %+v", result)
	}
	if _, ok := reg.Descriptor("mcp_demo_echo"); !ok {
		t.Fatal("expected MCP tool to be registered")
	}

	serverTools = []*sdkmcp.Tool{
		{
			Name:        "echo",
			Description: "Echo text v2",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message":   map[string]any{"type": "string"},
					"uppercase": map[string]any{"type": "boolean"},
				},
				"required": []any{"message"},
			},
		},
		{
			Name:        "sum",
			Description: "Add numbers",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"values": map[string]any{"type": "array", "items": map[string]any{"type": "number"}}},
				"required":   []any{"values"},
			},
		},
	}
	applyConfig("sig-2")
	if got := len(mgr.GetAllTools()["demo"]); got != 2 {
		t.Fatalf("expected manager to expose 2 tools after reconnect, got %d", got)
	}
	result = reconcileMCPToolRegistry(reg, mgr)
	if result.Added != 1 || result.Updated != 1 || result.Removed != 0 || result.Desired != 2 {
		t.Fatalf("unexpected update reconcile result: %+v", result)
	}
	echoDesc, ok := reg.Descriptor("mcp_demo_echo")
	if !ok || echoDesc.Description != "[MCP:demo] Echo text v2" {
		t.Fatalf("expected updated echo descriptor, got %+v", echoDesc)
	}
	if _, ok := reg.Descriptor("mcp_demo_sum"); !ok {
		t.Fatal("expected second MCP tool to be registered")
	}

	serverTools = nil
	applyConfig("sig-3")
	result = reconcileMCPToolRegistry(reg, mgr)
	if result.Added != 0 || result.Updated != 0 || result.Removed != 2 || result.Desired != 0 {
		t.Fatalf("unexpected removal reconcile result: %+v", result)
	}
	if _, ok := reg.Descriptor("mcp_demo_echo"); ok {
		t.Fatal("expected echo tool to be removed")
	}
	if _, ok := reg.Descriptor("mcp_demo_sum"); ok {
		t.Fatal("expected sum tool to be removed")
	}
	defs := reg.Definitions()
	if len(defs) != 1 || defs[0].Name != "memory_search" {
		t.Fatalf("expected builtin tool surface to remain intact, got %+v", defs)
	}
}
