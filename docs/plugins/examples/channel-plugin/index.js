'use strict'
module.exports = {
  id: 'channel-plugin-example',
  name: 'Channel Plugin Example',
  register(api) {
    api.registerChannel({ plugin: {
      ID: () => 'example-channel',
      Type: () => 'chat',
      ConfigSchema: () => ({ type: 'object', properties: { token: { type: 'string' } } }),
      Capabilities: () => ({ typing: true, reactions: true }),
      Connect: async (_channelID, _config, onMessage) => {
        onMessage({ id: 'welcome', text: 'channel connected', user_id: 'system' })
        return {
          id: 'example-handle',
          send: async (text) => ({ sent: text }),
          close: async () => ({ closed: true }),
          sendTyping: async () => ({ typing: true }),
          addReaction: async () => ({ ok: true })
        }
      }
    } })
  }
}
