---
summary: "Planning doc: metiq Go plugin SDK for type-safe plugin development"
read_when:
  - Planning the plugin SDK architecture
  - Designing the plugin interface for Go plugins
title: "Plugin SDK (Planning)"
---

# Plugin SDK (Planning)

> Status: Planning/Research — not yet implemented.

This document captures planning thoughts for a formal Go plugin SDK that would make it easier to build type-safe metiq plugins without modifying core code.

## Problem

Currently, plugins require:
1. Forking or modifying `cmd/metiqd/main.go` to register tools
2. Knowledge of internal types (`toolbuiltin.Registry`, etc.)
3. Recompiling the entire daemon

This works for core development but is a barrier for third-party extensions.

## Goals

- **Type-safe plugin interface**: plugins use well-defined Go interfaces, not reflection tricks
- **No fork required**: plugins loaded from external Go packages or shared objects
- **Hot-reload friendly**: add/remove plugins without restarting the daemon (stretch goal)
- **Discovery**: plugins auto-discovered from `~/.metiq/plugins/` like skills

## Proposed Interface

```go
// Plugin interface every metiq plugin must implement
type Plugin interface {
    // Metadata
    Manifest() Manifest

    // Called once at daemon startup
    Init(ctx context.Context, cfg PluginConfig, registry *PluginRegistry) error

    // Called at daemon shutdown
    Close(ctx context.Context) error
}

// PluginRegistry allows plugins to register tools, channels, etc.
type PluginRegistry struct {
    Tools    ToolRegistry
    Channels ChannelRegistry
    Hooks    HookRegistry
    Memory   MemoryRegistry
}
```

## Tool Registration

```go
type ToolFunc func(ctx context.Context, params map[string]any) (string, error)

type ToolRegistry interface {
    Register(name string, fn ToolFunc, opts ...ToolOption)
}

type ToolOption func(*ToolDefinition)

func WithDescription(desc string) ToolOption { ... }
func WithSchema(schema json.RawMessage) ToolOption { ... }
```

## Plugin Loading Options

### Option A: Go Plugin (.so)

```go
// Load via Go's plugin package
p, err := plugin.Open("~/.metiq/plugins/my-plugin.so")
sym, err := p.Lookup("Plugin")
metiqPlugin := sym.(metiq.Plugin)
```

Pros: Fast, type-safe
Cons: Go version pinning, compilation complexity

### Option B: External Process (gRPC)

Plugins run as separate processes and communicate via gRPC:

```protobuf
service Plugin {
  rpc Init(InitRequest) returns (InitResponse);
  rpc CallTool(ToolRequest) returns (ToolResponse);
}
```

Pros: Language-agnostic, isolated
Cons: Higher latency, complexity

### Option C: JavaScript/TypeScript (Goja)

Use Go's Goja JS engine (already used in metiq for channel plugins):

```javascript
// plugin.js
module.exports = {
  manifest: { id: "my-plugin", name: "My Plugin" },
  tools: {
    my_tool: async (params) => {
      return `Result: ${params.input}`;
    }
  }
};
```

Pros: No compilation needed, scripting-friendly
Cons: Performance, limited Go interop

## Recommended Approach

For metiq's use case (Go-native, performance-sensitive), Option A (Go plugin .so) or a hybrid approach (Go for core tools, Goja for scripted tools) seems most appropriate.

The Goja approach is already partially implemented (channel plugins use it) — extending it to agent tools is a natural next step.

## See Also

- [Plugin Manifest](/plugins/manifest)
- [Skills](/tools/skills)
- [Nostr Tools](/tools/nostr-tools) — example of built-in tool registration
