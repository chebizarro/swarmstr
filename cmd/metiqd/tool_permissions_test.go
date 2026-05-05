package main

import (
	"context"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/permissions"
)

func TestPermissionCategoryForToolSeparatesOriginFromCapability(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterWithDescriptor("mcp_demo_echo", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindMCP, ServerName: "demo"}})
	reg.RegisterWithDescriptor("mcp_demo_read", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindMCP, ServerName: "demo", CanonicalName: "read_file"}})
	reg.RegisterWithDescriptor("plugin/echo", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindPlugin, PluginID: "echo-plugin"}})
	reg.RegisterWithDescriptor("plugin/search", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindPlugin, PluginID: "search-plugin", CanonicalName: "web_search"}})
	reg.RegisterWithDescriptor("plugin/destroy", nil, agent.ToolDescriptor{
		Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindPlugin},
		Traits: agent.ToolTraits{Destructive: true},
	})

	if got := permissionCategoryForTool(reg, "mcp_demo_echo"); got != "" {
		t.Fatalf("MCP origin category = %q, want empty classifier delegation", got)
	}
	if got := permissionCategoryForTool(reg, "mcp_demo_read"); got != permissions.CategoryFilesystem {
		t.Fatalf("MCP capability category = %q, want %q", got, permissions.CategoryFilesystem)
	}
	if got := permissionCategoryForTool(reg, "plugin/echo"); got != "" {
		t.Fatalf("plugin origin category = %q, want empty classifier delegation", got)
	}
	if got := permissionCategoryForTool(reg, "plugin/search"); got != permissions.CategoryNetwork {
		t.Fatalf("plugin capability category = %q, want %q", got, permissions.CategoryNetwork)
	}
	if got := permissionCategoryForTool(reg, "plugin/destroy"); got != permissions.CategoryExec {
		t.Fatalf("destructive plugin category = %q, want %q", got, permissions.CategoryExec)
	}
}

func TestPermissionMetadataAllowsCapabilityDenyForExternalTool(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterWithDescriptor("mcp_demo_read", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindMCP, ServerName: "demo", CanonicalName: "read_file"}})

	cfg := permissions.DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.AutoClassify = false
	cfg.CacheEnabled = false
	cfg.DefaultBehavior = permissions.BehaviorAllow
	engine := permissions.NewEngine(t.TempDir(), cfg)
	if err := engine.AddRule(permissions.NewRule("ask-mcp", permissions.ScopeGlobal, permissions.BehaviorAsk, "*").WithOrigin(permissions.ToolOriginMCP)); err != nil {
		t.Fatalf("add origin rule: %v", err)
	}
	if err := engine.AddRule(permissions.NewRule("deny-filesystem", permissions.ScopeGlobal, permissions.BehaviorDeny, "*").WithCategory(permissions.CategoryFilesystem)); err != nil {
		t.Fatalf("add category rule: %v", err)
	}

	req := permissions.NewToolRequest("mcp_demo_read", permissionCategoryForTool(reg, "mcp_demo_read"))
	if origin, originName := permissionOriginForTool(reg, "mcp_demo_read"); origin != "" || originName != "" {
		req = req.WithOrigin(origin, originName)
	}
	decision := engine.Evaluate(context.Background(), req)
	if decision.Behavior != permissions.BehaviorDeny {
		t.Fatalf("filesystem deny should match external MCP tool, got %s", decision.Behavior)
	}
}

func TestPermissionOriginForToolUsesDescriptorProvenance(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterWithDescriptor("mcp_demo_echo", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindMCP, ServerName: "demo"}})
	reg.RegisterWithDescriptor("plugin/echo", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindPlugin, PluginID: "echo-plugin"}})
	reg.RegisterWithDescriptor("builtin", nil, agent.ToolDescriptor{Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindBuiltin}})

	origin, name := permissionOriginForTool(reg, "mcp_demo_echo")
	if origin != permissions.ToolOriginMCP || name != "demo" {
		t.Fatalf("MCP origin = (%q, %q), want (%q, %q)", origin, name, permissions.ToolOriginMCP, "demo")
	}
	origin, name = permissionOriginForTool(reg, "plugin/echo")
	if origin != permissions.ToolOriginPlugin || name != "echo-plugin" {
		t.Fatalf("plugin origin = (%q, %q), want (%q, %q)", origin, name, permissions.ToolOriginPlugin, "echo-plugin")
	}
	origin, name = permissionOriginForTool(reg, "builtin")
	if origin != permissions.ToolOriginBuiltin || name != "" {
		t.Fatalf("builtin origin = (%q, %q), want (%q, %q)", origin, name, permissions.ToolOriginBuiltin, "")
	}
}

func TestToolProfileFullBypassesOnlyLegacyApproval(t *testing.T) {
	if !toolProfileBypassesApproval(" full ", false) {
		t.Fatal("full profile should bypass legacy approval when permission engine is not configured")
	}
	if toolProfileBypassesApproval("full", true) {
		t.Fatal("full profile must not bypass configured permission engine")
	}
	if toolProfileBypassesApproval("coding", false) {
		t.Fatal("non-full profile should not bypass legacy approval")
	}
}
