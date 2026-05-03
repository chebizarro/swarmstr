# Writing Plugins

Create a plugin directory with a JavaScript entry point and optional `package.json`.

```js
module.exports = {
  id: 'my-plugin',
  name: 'My Plugin',
  version: '0.1.0',
  register(api) {
    api.registerTool({
      name: 'echo',
      description: 'Echo text',
      parameters: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] },
      execute: async (_id, args) => ({ text: args.text })
    })
  }
}
```

## Recommendations

- Keep registration deterministic and side-effect-light.
- Put network setup in `init`, provider calls, channel `Connect`, or service `start`.
- Return JSON-safe values only.
- Give tools explicit JSON Schema parameters.
- Use stable IDs: changing IDs creates new capabilities.
- For hooks, keep handlers fast and idempotent.
- For channels, use callbacks for inbound messages instead of polling.

See `docs/plugins/examples/` for complete examples.
