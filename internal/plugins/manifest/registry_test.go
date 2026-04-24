package manifest

import (
	"context"
	"testing"
)

func TestRegistryRegister(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools: []ToolCapability{
				{Name: "tool1", Description: "First tool", Category: ToolCategoryRead},
				{Name: "tool2", Description: "Second tool"},
			},
			Channels: []ChannelCapability{
				{ID: "telegram", Name: "Telegram"},
			},
			MCPServers: []MCPServerCapability{
				{ID: "my-mcp", Transport: MCPTransportStdio},
			},
			Skills: []SkillCapability{
				{ID: "my-skill", Name: "My Skill"},
			},
			GatewayMethods: []GatewayMethodCapability{
				{Method: "test.method", Description: "A method"},
			},
			Hooks: []HookCapability{
				{Event: "message.pre", Priority: 50},
				{Event: "message.post"},
			},
		},
	}

	err := reg.Register(ctx, m)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Verify plugin registered
	plugins := reg.Plugins()
	if len(plugins) != 1 || plugins[0] != "test-plugin" {
		t.Errorf("Plugins = %v, want [test-plugin]", plugins)
	}

	// Verify tools
	tools := reg.Tools()
	if len(tools) != 2 {
		t.Errorf("Tools count = %d, want 2", len(tools))
	}
	tool, ok := reg.Tool("test-plugin/tool1")
	if !ok {
		t.Error("tool1 not found")
	}
	if tool.Tool.Category != ToolCategoryRead {
		t.Errorf("tool1 category = %q, want %q", tool.Tool.Category, ToolCategoryRead)
	}

	// Verify channels
	channels := reg.Channels()
	if len(channels) != 1 {
		t.Errorf("Channels count = %d, want 1", len(channels))
	}

	// Verify MCP servers
	mcpServers := reg.MCPServers()
	if len(mcpServers) != 1 {
		t.Errorf("MCPServers count = %d, want 1", len(mcpServers))
	}

	// Verify skills
	skills := reg.Skills()
	if len(skills) != 1 {
		t.Errorf("Skills count = %d, want 1", len(skills))
	}

	// Verify gateway methods
	methods := reg.GatewayMethods()
	if len(methods) != 1 {
		t.Errorf("GatewayMethods count = %d, want 1", len(methods))
	}

	// Verify hooks
	preHooks := reg.HooksForEvent("message.pre")
	if len(preHooks) != 1 {
		t.Errorf("message.pre hooks count = %d, want 1", len(preHooks))
	}
	if preHooks[0].Hook.Priority != 50 {
		t.Errorf("message.pre priority = %d, want 50", preHooks[0].Hook.Priority)
	}

	postHooks := reg.HooksForEvent("message.post")
	if len(postHooks) != 1 {
		t.Errorf("message.post hooks count = %d, want 1", len(postHooks))
	}
	// Default priority should be 100
	if postHooks[0].Hook.Priority != 100 {
		t.Errorf("message.post priority = %d, want 100 (default)", postHooks[0].Hook.Priority)
	}
}

func TestRegistryDuplicatePlugin(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
	}

	if err := reg.Register(ctx, m); err != nil {
		t.Fatalf("First register failed: %v", err)
	}

	if err := reg.Register(ctx, m); err == nil {
		t.Error("Expected error for duplicate plugin registration")
	}
}

func TestRegistryDuplicateTool(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	m1 := &Manifest{
		SchemaVersion: 2,
		ID:            "plugin1",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools: []ToolCapability{{Name: "shared"}},
		},
	}

	m2 := &Manifest{
		SchemaVersion: 2,
		ID:            "plugin2",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			// Same tool name but different plugin - should succeed
			Tools: []ToolCapability{{Name: "shared"}},
		},
	}

	if err := reg.Register(ctx, m1); err != nil {
		t.Fatalf("First register failed: %v", err)
	}

	// Different plugins can have tools with the same name
	if err := reg.Register(ctx, m2); err != nil {
		t.Errorf("Second register should succeed (different namespaces): %v", err)
	}

	// Both tools should be registered
	tools := reg.Tools()
	if len(tools) != 2 {
		t.Errorf("Tools count = %d, want 2", len(tools))
	}
}

func TestRegistryDuplicateChannel(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	m1 := &Manifest{
		SchemaVersion: 2,
		ID:            "plugin1",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Channels: []ChannelCapability{{ID: "telegram"}},
		},
	}

	m2 := &Manifest{
		SchemaVersion: 2,
		ID:            "plugin2",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Channels: []ChannelCapability{{ID: "telegram"}},
		},
	}

	if err := reg.Register(ctx, m1); err != nil {
		t.Fatalf("First register failed: %v", err)
	}

	// Channels with same ID should conflict
	if err := reg.Register(ctx, m2); err == nil {
		t.Error("Expected error for duplicate channel ID")
	}
}

func TestRegistryUnregister(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools:      []ToolCapability{{Name: "tool1"}},
			Channels:   []ChannelCapability{{ID: "telegram"}},
			MCPServers: []MCPServerCapability{{ID: "mcp1", Transport: MCPTransportStdio}},
			Skills:     []SkillCapability{{ID: "skill1"}},
			Hooks:      []HookCapability{{Event: "message.pre"}},
		},
	}

	if err := reg.Register(ctx, m); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Verify registration
	if len(reg.Tools()) != 1 {
		t.Error("Tool should be registered")
	}

	// Unregister
	if err := reg.Unregister("test-plugin"); err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	// Verify unregistration
	if len(reg.Plugins()) != 0 {
		t.Error("Plugin should be unregistered")
	}
	if len(reg.Tools()) != 0 {
		t.Error("Tools should be unregistered")
	}
	if len(reg.Channels()) != 0 {
		t.Error("Channels should be unregistered")
	}
	if len(reg.MCPServers()) != 0 {
		t.Error("MCP servers should be unregistered")
	}
	if len(reg.Skills()) != 0 {
		t.Error("Skills should be unregistered")
	}
	if len(reg.HooksForEvent("message.pre")) != 0 {
		t.Error("Hooks should be unregistered")
	}
}

func TestRegistryUnregisterNotFound(t *testing.T) {
	reg := NewCapabilityRegistry()

	if err := reg.Unregister("nonexistent"); err == nil {
		t.Error("Expected error for unregistering nonexistent plugin")
	}
}

func TestRegistryHookPriority(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	// Register plugins with different hook priorities
	m1 := &Manifest{
		SchemaVersion: 2,
		ID:            "plugin1",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Hooks: []HookCapability{{Event: "turn.start", Priority: 200}},
		},
	}

	m2 := &Manifest{
		SchemaVersion: 2,
		ID:            "plugin2",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Hooks: []HookCapability{{Event: "turn.start", Priority: 50}},
		},
	}

	m3 := &Manifest{
		SchemaVersion: 2,
		ID:            "plugin3",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Hooks: []HookCapability{{Event: "turn.start", Priority: 100}},
		},
	}

	_ = reg.Register(ctx, m1)
	_ = reg.Register(ctx, m2)
	_ = reg.Register(ctx, m3)

	hooks := reg.HooksForEvent("turn.start")
	if len(hooks) != 3 {
		t.Fatalf("Expected 3 hooks, got %d", len(hooks))
	}

	// Should be sorted by priority: 50, 100, 200
	if hooks[0].PluginID != "plugin2" {
		t.Errorf("First hook should be plugin2 (priority 50), got %s", hooks[0].PluginID)
	}
	if hooks[1].PluginID != "plugin3" {
		t.Errorf("Second hook should be plugin3 (priority 100), got %s", hooks[1].PluginID)
	}
	if hooks[2].PluginID != "plugin1" {
		t.Errorf("Third hook should be plugin1 (priority 200), got %s", hooks[2].PluginID)
	}
}

func TestRegistrySummary(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools:          []ToolCapability{{Name: "t1"}, {Name: "t2"}},
			Channels:       []ChannelCapability{{ID: "ch1"}},
			MCPServers:     []MCPServerCapability{{ID: "mcp1", Transport: MCPTransportStdio}},
			Skills:         []SkillCapability{{ID: "s1"}, {Name: "s2", ID: "s2"}},
			GatewayMethods: []GatewayMethodCapability{{Method: "m1"}},
			Hooks:          []HookCapability{{Event: "e1"}, {Event: "e2"}},
		},
	}

	_ = reg.Register(ctx, m)

	summary := reg.Summary()

	if summary.PluginCount != 1 {
		t.Errorf("PluginCount = %d, want 1", summary.PluginCount)
	}
	if summary.ToolCount != 2 {
		t.Errorf("ToolCount = %d, want 2", summary.ToolCount)
	}
	if summary.ChannelCount != 1 {
		t.Errorf("ChannelCount = %d, want 1", summary.ChannelCount)
	}
	if summary.MCPCount != 1 {
		t.Errorf("MCPCount = %d, want 1", summary.MCPCount)
	}
	if summary.SkillCount != 2 {
		t.Errorf("SkillCount = %d, want 2", summary.SkillCount)
	}
	if summary.MethodCount != 1 {
		t.Errorf("MethodCount = %d, want 1", summary.MethodCount)
	}
	if summary.HookCount != 2 {
		t.Errorf("HookCount = %d, want 2", summary.HookCount)
	}
}

func TestRegistryToolsByCategory(t *testing.T) {
	reg := NewCapabilityRegistry()
	ctx := context.Background()

	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools: []ToolCapability{
				{Name: "read1", Category: ToolCategoryRead},
				{Name: "read2", Category: ToolCategoryRead},
				{Name: "write1", Category: ToolCategoryWrite},
				{Name: "exec1", Category: ToolCategoryExec},
			},
		},
	}

	_ = reg.Register(ctx, m)

	readTools := reg.ToolsByCategory(ToolCategoryRead)
	if len(readTools) != 2 {
		t.Errorf("Read tools count = %d, want 2", len(readTools))
	}

	writeTools := reg.ToolsByCategory(ToolCategoryWrite)
	if len(writeTools) != 1 {
		t.Errorf("Write tools count = %d, want 1", len(writeTools))
	}

	execTools := reg.ToolsByCategory(ToolCategoryExec)
	if len(execTools) != 1 {
		t.Errorf("Exec tools count = %d, want 1", len(execTools))
	}

	dangerousTools := reg.ToolsByCategory(ToolCategoryDangerous)
	if len(dangerousTools) != 0 {
		t.Errorf("Dangerous tools count = %d, want 0", len(dangerousTools))
	}
}
