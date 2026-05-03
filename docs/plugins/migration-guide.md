# Migration Guide

## From `metiq.plugin.json` tool bundles

Old bundles can remain installed, but OpenClaw plugins should expose a JavaScript entry point and call registration APIs directly.

| Old field | OpenClaw equivalent |
| --- | --- |
| `id` | `module.exports.id` |
| `name` | `module.exports.name` |
| `version` | `module.exports.version` |
| `tools[]` | `api.registerTool(...)` |
| `channels[]` | `api.registerChannel(...)` |
| `configSchema` | plugin package metadata or provider/channel schema |

## Migration steps

1. Add `package.json` with `main: "index.js"`.
2. Move static tool metadata into `api.registerTool` calls.
3. Move executable logic into async `execute` functions.
4. Convert channel adapters to `api.registerChannel({ plugin })`.
5. Convert provider integrations to `api.registerProvider` or media-specific provider registrations.
6. Run unit/integration tests with the OpenClaw fixture style used in `internal/plugins/runtime`.
