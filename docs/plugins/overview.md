# Plugin System Overview

Swarmstr supports OpenClaw-compatible plugins through a Node.js runtime host and a unified Go capability registry. A plugin can register tools, model providers, hooks, channels, services, commands, gateway methods, and media providers from a single OpenClaw entry point.

## Runtime flow

1. The plugin host loads an OpenClaw `index.js` or `package.json` entry.
2. The plugin calls `register(api)` and uses `api.registerTool`, `api.registerProvider`, `api.registerHook`, `api.registerChannel`, and related OpenClaw APIs.
3. Swarmstr normalizes registrations into `internal/plugins/registry`.
4. Capabilities are invoked through event-driven runtime calls: tool execution, provider chat/catalog/auth calls, hook events, channel methods, and service lifecycle actions.

## Capability areas

- **Tools**: JSON-schema parameterized functions exposed to agents.
- **Providers**: model, image, video, music, search, fetch, realtime STT, and realtime voice adapters.
- **Hooks**: ordered event handlers for agent and channel lifecycle events.
- **Channels**: inbound/outbound chat transports with optional typing, reactions, threads, audio, and edits.
- **Services**: long-running plugin background tasks with start/stop/health management.

## Safety model

Plugins must be explicitly installed/enabled. Entry points must resolve inside trusted plugin roots. Runtime communication is JSON-RPC over a managed subprocess; plugin stdout logging is redirected so it cannot corrupt protocol responses.
