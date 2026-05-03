'use strict';

module.exports = {
  id: 'realistic-openclaw',
  name: 'Realistic OpenClaw Fixture',
  version: '0.1.0',
  description: 'Fixture used by Swarmstr Phase 11 compatibility tests.',
  register(api) {
    api.registerTool({
      name: 'echo',
      description: 'Echo a value back to the caller.',
      parameters: {
        type: 'object',
        additionalProperties: false,
        properties: { text: { type: 'string' } },
        required: ['text']
      },
      execute: async (_id, args) => ({ text: args.text, ok: true })
    });
    api.registerProvider({
      id: 'fixture-provider',
      label: 'Fixture Provider',
      envVars: ['FIXTURE_API_KEY'],
      catalog: { run: async () => ({ models: [{ id: 'fixture-model', name: 'Fixture Model' }] }) },
      chat: { run: async (params) => ({ message: { role: 'assistant', content: 'fixture:' + params.messages[0].content } }) }
    });
    api.registerHook('before_tool_call', async (payload) => ({ mutation: { phase11: true, tool: payload.tool } }), { id: 'before-tool', priority: 1 });
    api.registerHook('after_tool_call', async (payload) => ({ ok: true, tool: payload.tool }), { id: 'after-tool', priority: 2 });
    api.registerChannel({
      plugin: {
        ID: () => 'fixture-channel',
        Type: () => 'chat',
        ConfigSchema: () => ({ type: 'object', properties: { token: { type: 'string' } } }),
        Capabilities: () => ({ typing: true, reactions: true }),
        Connect: async (_channelID, _cfg, onMessage) => {
          onMessage({ id: 'incoming-1', text: 'hello from channel', user_id: 'fixture-user' });
          return {
            id: 'fixture-handle',
            send: async (text) => ({ sent: text }),
            close: async () => ({ closed: true }),
            sendTyping: async () => ({ typing: true }),
            addReaction: async () => ({ reaction: 'added' })
          };
        }
      }
    });
    api.registerService({ id: 'fixture-service', start: async () => ({ started: true }), stop: async () => ({ stopped: true }) });
  },
  init(_api, params) {
    return { initialized: true, params };
  }
};
