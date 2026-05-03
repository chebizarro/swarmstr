# OpenClaw Compatibility

Swarmstr implements the OpenClaw plugin entry contract for Node.js plugins while exposing normalized Go registries and agent integrations.

## Supported entry styles

- `module.exports = { id, name, register(api), init(api, params) }`
- `definePluginEntry({ ... })`
- `defineChannelPluginEntry({ ... })`
- package `main` and `openclaw.entry` fields

## Supported registration APIs

Core registrations include tools, providers, hooks, channels, services, commands, gateway methods, speech/transcription/voice providers, image/video/music providers, web search/fetch providers, memory providers, and generic OpenClaw extension points. Unknown or future capability types are preserved in the generic registry when possible.

## Invocation semantics

- Tool calls invoke the registered `execute` function and return JSON-safe results.
- Provider calls use OpenClaw method names such as `chat`, `stream`, `catalog`, `staticCatalog`, and `auth`.
- Hook calls preserve OpenClaw priority ordering and can be invoked as full chains or single handlers.
- Channel callbacks are delivered asynchronously through registered callback IDs.
- Services use explicit `start` and `stop` methods.

## Compatibility guarantees

The Phase 11 test fixture under `internal/plugins/runtime/testdata/openclaw-realistic` verifies plugin load, registration capture, initialization, tool invocation, provider catalog, hook invocation, channel callbacks, and service lifecycle against the runtime shim.
