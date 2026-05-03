# Plugin API Reference

## Entry point

```ts
interface PluginEntry {
  id: string
  name?: string
  version?: string
  description?: string
  register(api: PluginApi): void | Promise<void>
  init?(api: PluginApi, params?: unknown): unknown | Promise<unknown>
}
```

## Common registration methods

- `api.registerTool(tool, options?)`
- `api.registerProvider(provider)`
- `api.registerHook(eventOrEvents, handler, options?)`
- `api.registerChannel({ plugin })`
- `api.registerService(service)`
- `api.registerCommand(command)`
- `api.registerGatewayMethod(method, handler, options?)`
- `api.registerImageGenerationProvider(provider)`
- `api.registerVideoGenerationProvider(provider)`
- `api.registerMusicGenerationProvider(provider)`
- `api.registerRealtimeTranscriptionProvider(provider)`
- `api.registerRealtimeVoiceProvider(provider)`
- `api.registerWebSearchProvider(provider)`
- `api.registerWebFetchProvider(provider)`

## Result conventions

- Tool result: any JSON-safe value.
- Hook result: `{ mutation?: object, reject?: boolean, reason?: string }`.
- Provider chat result: `{ content?: string, message?: { role, content }, tool_calls?: [], usage?: {} }`.
- Catalog result: `{ models: [{ id, name?, contextWindow?, input?, output? }] }`.
- Channel connect result: a handle with `send`, `close`, and optional feature methods.
