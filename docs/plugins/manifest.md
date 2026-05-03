---
summary: "Plugin manifest format and agent tool plugin development for metiq"
read_when:
  - Building a metiq plugin
  - Creating custom agent tools as plugins
  - Understanding the plugin manifest format
title: "Plugin Manifest & Agent Tools"
---

# Plugin Manifest & Agent Tools

metiq supports a plugin system for extending the agent with custom tools, skills, and channel adapters. Plugins are Go packages or scripted tool bundles.

## Plugin Manifest (`metiq.plugin.json`)

Every plugin must ship a `metiq.plugin.json` manifest in the plugin root directory.

```json
{
  "id": "my-plugin",
  "name": "My Plugin",
  "description": "A custom plugin for metiq",
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
├── metiq.plugin.json   # Manifest (required)
├── README.md              # Documentation
├── tools/
│   └── my_tool.go         # Go tool implementation
└── skills/
    └── my-skill/
        └── SKILL.md       # Skill definition
```

## Creating Custom Agent Tools

### Go Plugin (Compiled, internal extensions only)

External plugins should use the OpenClaw JavaScript entry point documented in `docs/plugins/writing-plugins.md`. Compiled Go tools are in-repository extensions and are registered by adding a tool definition to the daemon's tool registry:

```go
registry.RegisterWithDef(agent.ToolDefinition{
    Name:        "my_custom_tool",
    Description: "Run my custom tool",
    Parameters: agent.ToolParameters{
        Type: "object",
    },
}, func(ctx context.Context, params map[string]any) (string, error) {
    return "result", nil
})
```

Keep compiled tools in the main module so they can import internal packages and be built with `go build ./...`.

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

Enable plugins in `~/.metiq/config.json`:

```json5
{
  "plugins": {
    "enabled": true,
    "load_paths": ["~/.metiq/plugins/my-plugin"],
    "installs": {
      "my-plugin": {
        "source": "path",
        "sourcePath": "~/.metiq/plugins/my-plugin",
        "installPath": "~/.metiq/plugins/my-plugin"
      }
    },
    "entries": {
      "my-plugin": {
        "enabled": true,
        "install_path": "~/.metiq/plugins/my-plugin",
        "apiKey": "${MY_PLUGIN_API_KEY}"
      }
    }
  }
}
```

### Trust and validation rules

The plugin loader now enforces a tighter trust model:

- `entries.<id>.install_path` must resolve inside a managed install root or a configured `plugins.load_paths` root
- when a matching `plugins.installs.<id>` record exists, the entry install path is cross-checked against that install record
- plugin entry points must resolve inside the plugin root; `package.json main` cannot escape the plugin directory
- plugin manifests are validated before registration, including duplicate tool names and basic JSON-schema shape for tool parameters
- load failures are returned as aggregated per-plugin diagnostics instead of being reduced to a best-effort silent skip

If a plugin path is outside the trusted roots, or the manifest/schema is invalid, the loader rejects it.

## Plugin Discovery Order

1. `<workspace>/plugins/` — per-agent plugins (highest precedence)
2. `~/.metiq/plugins/` — user-installed plugins
3. `plugins.load.paths` — additional plugin paths from config
4. Built-in plugins — compiled into metiqd

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
metiq plugins list

# Show plugin details
metiq plugins info my-plugin

# Install a plugin (future)
metiq plugins install ./path/to/my-plugin
```

## See Also

- [Skills](/tools/skills)
- [Nostr Tools](/tools/nostr-tools)
- [Configuration](/gateway/configuration)
