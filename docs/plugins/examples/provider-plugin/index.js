'use strict'
module.exports = {
  id: 'provider-plugin-example',
  name: 'Provider Plugin Example',
  register(api) {
    api.registerProvider({
      id: 'example-provider',
      label: 'Example Provider',
      envVars: ['EXAMPLE_PROVIDER_API_KEY'],
      catalog: { run: async () => ({ models: [{ id: 'example-model', name: 'Example Model' }] }) },
      chat: { run: async (params) => ({ content: `example:${params.messages?.at(-1)?.content || ''}` }) }
    })
  }
}
