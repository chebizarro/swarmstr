---
summary: "Plugin manifest format and agent tool plugin development for swarmstr"
read_when:
  - Building a swarmstr plugin
  - Creating custom agent tools as plugins
  - Understanding the plugin manifest format
title: "Plugin Manifest & Agent Tools"
---

# Plugin Manifest & Agent Tools

swarmstr supports a plugin system for extending the agent with custom tools, skills, and channel adapters. Plugins are Go packages or scripted tool bundles.

## Plugin Manifest (`swarmstr.plugin.json`)

Every plugin must ship a `swarmstr.plugin.json` manifest in the plugin root directory.

```json
{
  "id": "my-plugin",
  "name": "My Plugin",
  "description": "A custom plugin for swarmstr",
  "version": "0.1.0",
  "kind": "tool",
  "configSchema": {
    "type": "object",
    "additionalProperties": false,
    "properties": {
      "apiKey": {
        "type": "string",
        "description": "API key for the service"
      }
    }
  }
}
```

### Required Fields

- **`id`** (string): unique plugin identifier (e.g., `"my-nostr-tool"`)
- **`configSchema`** (object): JSON Schema for plugin configuration

### Optional Fields

- **`name`** (string): display name
- **`description`** (string): short summary
- **`version`** (string): semantic version
- **`kind`** (string): plugin kind — `"tool"` | `"skill"` | `"channel"` | `"memory"`
- **`skills`** (array): skill directories to load from this plugin
- **`tools`** (array): tool names registered by this plugin
- **`channels`** (array): channel IDs this plugin registers

## Plugin Directory Structure

```
my-plugin/
├── swarmstr.plugin.json   # Manifest (required)
├── README.md              # Documentation
├── tools/
│   └── my_tool.go         # Go tool implementation
└── skills/
    └── my-skill/
        └── SKILL.md       # Skill definition
```

## Creating Custom Agent Tools

### Go Plugin (Compiled)

For performance-critical tools, implement in Go:

```go
package myplugin

import (
    "context"
    "github.com/yourorg/swarmstr/internal/agent/toolbuiltin"
)

func RegisterMyTool(tools *toolbuiltin.Registry) {
    tools.Register("my_custom_tool", func(ctx context.Context, params map[string]any) (string, error) {
        // Tool implementation
        return "result", nil
    })
}
```

Register in `cmd/swarmstrd/main.go` after the other tool registrations:

```go
myplugin.RegisterMyTool(tools)
```

### Script Plugin (Shell/Python)

For simpler tools, use a shell or Python script that's discovered from the skills directory:

```markdown
<!-- skills/my-tool/SKILL.md -->
---
name: my-tool
description: "My custom tool"
metadata:
  openclaw:
    emoji: "🔧"
    requires:
      bins: ["python3"]
---

# My Tool

This skill provides the `my_custom_tool` function.
The agent should call the script at `skills/my-tool/tool.py` to use it.
```

## Plugin Configuration

Enable plugins in `~/.swarmstr/config.json`:

```json5
{
  "plugins": {
    "enabled": true,
    "load": {
      "paths": ["~/.swarmstr/plugins/my-plugin"]
    },
    "entries": {
      "my-plugin": {
        "enabled": true,
        "apiKey": "${MY_PLUGIN_API_KEY}"
      }
    }
  }
}
```

## Plugin Discovery Order

1. `<workspace>/plugins/` — per-agent plugins (highest precedence)
2. `~/.swarmstr/plugins/` — user-installed plugins
3. `plugins.load.paths` — additional plugin paths from config
4. Built-in plugins — compiled into swarmstrd

## Channel Plugins

To add a new messaging channel (e.g., Matrix, Signal) as a plugin:

```json
{
  "id": "matrix-channel",
  "kind": "channel",
  "channels": ["matrix"],
  "configSchema": {
    "type": "object",
    "properties": {
      "homeserver": { "type": "string" },
      "accessToken": { "type": "string" }
    }
  }
}
```

Channel plugins integrate with the controlDMBus to route messages to the agent runtime.

## Memory Plugins

Plugins with `"kind": "memory"` can provide custom memory backends (vector databases, remote storage):

```json
{
  "id": "qdrant-memory",
  "kind": "memory",
  "configSchema": {
    "properties": {
      "url": { "type": "string" },
      "collection": { "type": "string" }
    }
  }
}
```

## Plugin CLI

```bash
# List installed plugins
swarmstr plugins list

# Show plugin details
swarmstr plugins info my-plugin

# Install a plugin (future)
swarmstr plugins install ./path/to/my-plugin
```

## See Also

- [Skills](/tools/skills)
- [Nostr Tools](/tools/nostr-tools)
- [Configuration](/gateway/configuration)
