package manifest

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// ─── Capability Registry ─────────────────────────────────────────────────────

// CapabilityRegistry tracks registered plugin capabilities for runtime use.
// It aggregates capabilities from all loaded plugins and provides queries
// for tools, channels, MCP servers, skills, and other declared capabilities.
type CapabilityRegistry struct {
	mu       sync.RWMutex
	plugins  map[string]*Manifest           // pluginID → manifest
	tools    map[string]*RegisteredTool     // qualified tool name → tool
	channels map[string]*RegisteredChannel  // channel ID → channel
	mcp      map[string]*RegisteredMCP      // MCP server ID → server
	skills   map[string]*RegisteredSkill    // skill ID → skill
	methods  map[string]*RegisteredMethod   // gateway method → method
	hooks    map[string][]*RegisteredHook   // event → hooks (sorted by priority)
}

// RegisteredTool is a tool with its source plugin context.
type RegisteredTool struct {
	PluginID string
	Tool     ToolCapability
}

// QualifiedName returns the namespaced tool name (pluginID/toolName).
func (t *RegisteredTool) QualifiedName() string {
	return t.PluginID + "/" + t.Tool.Name
}

// RegisteredChannel is a channel with its source plugin context.
type RegisteredChannel struct {
	PluginID string
	Channel  ChannelCapability
}

// RegisteredMCP is an MCP server with its source plugin context.
type RegisteredMCP struct {
	PluginID string
	Server   MCPServerCapability
}

// RegisteredSkill is a skill with its source plugin context.
type RegisteredSkill struct {
	PluginID string
	Skill    SkillCapability
}

// RegisteredMethod is a gateway method with its source plugin context.
type RegisteredMethod struct {
	PluginID string
	Method   GatewayMethodCapability
}

// RegisteredHook is a hook with its source plugin context.
type RegisteredHook struct {
	PluginID string
	Hook     HookCapability
}

// NewCapabilityRegistry creates an empty capability registry.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{
		plugins:  make(map[string]*Manifest),
		tools:    make(map[string]*RegisteredTool),
		channels: make(map[string]*RegisteredChannel),
		mcp:      make(map[string]*RegisteredMCP),
		skills:   make(map[string]*RegisteredSkill),
		methods:  make(map[string]*RegisteredMethod),
		hooks:    make(map[string][]*RegisteredHook),
	}
}

// ─── Registration ────────────────────────────────────────────────────────────

// Register adds a plugin's capabilities to the registry.
// Returns an error if registration fails (e.g., duplicate IDs).
func (r *CapabilityRegistry) Register(ctx context.Context, m *Manifest) error {
	if err := Validate(m); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate plugin
	if _, exists := r.plugins[m.ID]; exists {
		return fmt.Errorf("plugin %q already registered", m.ID)
	}

	// Check for tool conflicts
	for _, tool := range m.Capabilities.Tools {
		qualName := m.ID + "/" + tool.Name
		if _, exists := r.tools[qualName]; exists {
			return fmt.Errorf("tool %q already registered", qualName)
		}
	}

	// Check for channel conflicts
	for _, ch := range m.Capabilities.Channels {
		if existing, exists := r.channels[ch.ID]; exists {
			return fmt.Errorf("channel %q already registered by plugin %q", ch.ID, existing.PluginID)
		}
	}

	// Check for MCP conflicts
	for _, mcp := range m.Capabilities.MCPServers {
		if existing, exists := r.mcp[mcp.ID]; exists {
			return fmt.Errorf("MCP server %q already registered by plugin %q", mcp.ID, existing.PluginID)
		}
	}

	// Check for skill conflicts
	for _, skill := range m.Capabilities.Skills {
		if existing, exists := r.skills[skill.ID]; exists {
			return fmt.Errorf("skill %q already registered by plugin %q", skill.ID, existing.PluginID)
		}
	}

	// Check for method conflicts
	for _, method := range m.Capabilities.GatewayMethods {
		if existing, exists := r.methods[method.Method]; exists {
			return fmt.Errorf("gateway method %q already registered by plugin %q", method.Method, existing.PluginID)
		}
	}

	// Register plugin
	r.plugins[m.ID] = m

	// Register tools
	for _, tool := range m.Capabilities.Tools {
		qualName := m.ID + "/" + tool.Name
		r.tools[qualName] = &RegisteredTool{PluginID: m.ID, Tool: tool}
	}

	// Register channels
	for _, ch := range m.Capabilities.Channels {
		r.channels[ch.ID] = &RegisteredChannel{PluginID: m.ID, Channel: ch}
	}

	// Register MCP servers
	for _, mcp := range m.Capabilities.MCPServers {
		r.mcp[mcp.ID] = &RegisteredMCP{PluginID: m.ID, Server: mcp}
	}

	// Register skills
	for _, skill := range m.Capabilities.Skills {
		r.skills[skill.ID] = &RegisteredSkill{PluginID: m.ID, Skill: skill}
	}

	// Register gateway methods
	for _, method := range m.Capabilities.GatewayMethods {
		r.methods[method.Method] = &RegisteredMethod{PluginID: m.ID, Method: method}
	}

	// Register hooks (sorted by priority)
	for _, hook := range m.Capabilities.Hooks {
		priority := hook.Priority
		if priority == 0 {
			priority = 100 // default priority
		}
		regHook := &RegisteredHook{PluginID: m.ID, Hook: HookCapability{
			Event:       hook.Event,
			Priority:    priority,
			Description: hook.Description,
		}}
		r.hooks[hook.Event] = append(r.hooks[hook.Event], regHook)
		// Re-sort hooks by priority
		sort.Slice(r.hooks[hook.Event], func(i, j int) bool {
			return r.hooks[hook.Event][i].Hook.Priority < r.hooks[hook.Event][j].Hook.Priority
		})
	}

	return nil
}

// Unregister removes a plugin and all its capabilities from the registry.
func (r *CapabilityRegistry) Unregister(pluginID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	m, exists := r.plugins[pluginID]
	if !exists {
		return fmt.Errorf("plugin %q not registered", pluginID)
	}

	// Remove tools
	for _, tool := range m.Capabilities.Tools {
		delete(r.tools, pluginID+"/"+tool.Name)
	}

	// Remove channels
	for _, ch := range m.Capabilities.Channels {
		delete(r.channels, ch.ID)
	}

	// Remove MCP servers
	for _, mcp := range m.Capabilities.MCPServers {
		delete(r.mcp, mcp.ID)
	}

	// Remove skills
	for _, skill := range m.Capabilities.Skills {
		delete(r.skills, skill.ID)
	}

	// Remove gateway methods
	for _, method := range m.Capabilities.GatewayMethods {
		delete(r.methods, method.Method)
	}

	// Remove hooks
	for _, hook := range m.Capabilities.Hooks {
		hooks := r.hooks[hook.Event]
		filtered := make([]*RegisteredHook, 0, len(hooks))
		for _, h := range hooks {
			if h.PluginID != pluginID {
				filtered = append(filtered, h)
			}
		}
		if len(filtered) > 0 {
			r.hooks[hook.Event] = filtered
		} else {
			delete(r.hooks, hook.Event)
		}
	}

	delete(r.plugins, pluginID)
	return nil
}

// ─── Queries ─────────────────────────────────────────────────────────────────

// Plugin returns the manifest for a registered plugin.
func (r *CapabilityRegistry) Plugin(pluginID string) (*Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.plugins[pluginID]
	return m, ok
}

// Plugins returns all registered plugin IDs.
func (r *CapabilityRegistry) Plugins() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.plugins))
	for id := range r.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Tool returns a registered tool by qualified name (pluginID/toolName).
func (r *CapabilityRegistry) Tool(qualifiedName string) (*RegisteredTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[qualifiedName]
	return t, ok
}

// Tools returns all registered tools.
func (r *CapabilityRegistry) Tools() []*RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]*RegisteredTool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].QualifiedName() < tools[j].QualifiedName()
	})
	return tools
}

// ToolsByPlugin returns all tools registered by a specific plugin.
func (r *CapabilityRegistry) ToolsByPlugin(pluginID string) []*RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var tools []*RegisteredTool
	for _, t := range r.tools {
		if t.PluginID == pluginID {
			tools = append(tools, t)
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Tool.Name < tools[j].Tool.Name
	})
	return tools
}

// ToolsByCategory returns all tools with a specific category.
func (r *CapabilityRegistry) ToolsByCategory(category ToolCategory) []*RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var tools []*RegisteredTool
	for _, t := range r.tools {
		if t.Tool.Category == category {
			tools = append(tools, t)
		}
	}
	return tools
}

// Channel returns a registered channel by ID.
func (r *CapabilityRegistry) Channel(channelID string) (*RegisteredChannel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.channels[channelID]
	return ch, ok
}

// Channels returns all registered channels.
func (r *CapabilityRegistry) Channels() []*RegisteredChannel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	channels := make([]*RegisteredChannel, 0, len(r.channels))
	for _, ch := range r.channels {
		channels = append(channels, ch)
	}
	sort.Slice(channels, func(i, j int) bool {
		return channels[i].Channel.ID < channels[j].Channel.ID
	})
	return channels
}

// MCPServer returns a registered MCP server by ID.
func (r *CapabilityRegistry) MCPServer(serverID string) (*RegisteredMCP, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.mcp[serverID]
	return s, ok
}

// MCPServers returns all registered MCP servers.
func (r *CapabilityRegistry) MCPServers() []*RegisteredMCP {
	r.mu.RLock()
	defer r.mu.RUnlock()
	servers := make([]*RegisteredMCP, 0, len(r.mcp))
	for _, s := range r.mcp {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].Server.ID < servers[j].Server.ID
	})
	return servers
}

// Skill returns a registered skill by ID.
func (r *CapabilityRegistry) Skill(skillID string) (*RegisteredSkill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[skillID]
	return s, ok
}

// Skills returns all registered skills.
func (r *CapabilityRegistry) Skills() []*RegisteredSkill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skills := make([]*RegisteredSkill, 0, len(r.skills))
	for _, s := range r.skills {
		skills = append(skills, s)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Skill.ID < skills[j].Skill.ID
	})
	return skills
}

// GatewayMethod returns a registered gateway method.
func (r *CapabilityRegistry) GatewayMethod(method string) (*RegisteredMethod, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.methods[method]
	return m, ok
}

// GatewayMethods returns all registered gateway methods.
func (r *CapabilityRegistry) GatewayMethods() []*RegisteredMethod {
	r.mu.RLock()
	defer r.mu.RUnlock()
	methods := make([]*RegisteredMethod, 0, len(r.methods))
	for _, m := range r.methods {
		methods = append(methods, m)
	}
	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Method.Method < methods[j].Method.Method
	})
	return methods
}

// HooksForEvent returns all hooks registered for an event, sorted by priority.
func (r *CapabilityRegistry) HooksForEvent(event string) []*RegisteredHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hooks := r.hooks[event]
	// Return a copy
	result := make([]*RegisteredHook, len(hooks))
	copy(result, hooks)
	return result
}

// AllHookEvents returns all event names that have registered hooks.
func (r *CapabilityRegistry) AllHookEvents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := make([]string, 0, len(r.hooks))
	for event := range r.hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	return events
}

// ─── Summary ─────────────────────────────────────────────────────────────────

// Summary returns a summary of all registered capabilities.
type Summary struct {
	PluginCount   int      `json:"plugin_count"`
	ToolCount     int      `json:"tool_count"`
	ChannelCount  int      `json:"channel_count"`
	MCPCount      int      `json:"mcp_count"`
	SkillCount    int      `json:"skill_count"`
	MethodCount   int      `json:"method_count"`
	HookCount     int      `json:"hook_count"`
	Plugins       []string `json:"plugins"`
	Tools         []string `json:"tools"`
	Channels      []string `json:"channels"`
	MCPServers    []string `json:"mcp_servers"`
	Skills        []string `json:"skills"`
	Methods       []string `json:"methods"`
	HookEvents    []string `json:"hook_events"`
}

// Summary returns a summary of all registered capabilities.
func (r *CapabilityRegistry) Summary() Summary {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]string, 0, len(r.plugins))
	for id := range r.plugins {
		plugins = append(plugins, id)
	}
	sort.Strings(plugins)

	tools := make([]string, 0, len(r.tools))
	for name := range r.tools {
		tools = append(tools, name)
	}
	sort.Strings(tools)

	channels := make([]string, 0, len(r.channels))
	for id := range r.channels {
		channels = append(channels, id)
	}
	sort.Strings(channels)

	mcpServers := make([]string, 0, len(r.mcp))
	for id := range r.mcp {
		mcpServers = append(mcpServers, id)
	}
	sort.Strings(mcpServers)

	skills := make([]string, 0, len(r.skills))
	for id := range r.skills {
		skills = append(skills, id)
	}
	sort.Strings(skills)

	methods := make([]string, 0, len(r.methods))
	for m := range r.methods {
		methods = append(methods, m)
	}
	sort.Strings(methods)

	hookCount := 0
	hookEvents := make([]string, 0, len(r.hooks))
	for event, hooks := range r.hooks {
		hookEvents = append(hookEvents, event)
		hookCount += len(hooks)
	}
	sort.Strings(hookEvents)

	return Summary{
		PluginCount:  len(r.plugins),
		ToolCount:    len(r.tools),
		ChannelCount: len(r.channels),
		MCPCount:     len(r.mcp),
		SkillCount:   len(r.skills),
		MethodCount:  len(r.methods),
		HookCount:    hookCount,
		Plugins:      plugins,
		Tools:        tools,
		Channels:     channels,
		MCPServers:   mcpServers,
		Skills:       skills,
		Methods:      methods,
		HookEvents:   hookEvents,
	}
}
