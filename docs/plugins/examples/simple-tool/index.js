'use strict'
module.exports = {
  id: 'simple-tool-example',
  name: 'Simple Tool Example',
  register(api) {
    api.registerTool({
      name: 'reverse_text',
      description: 'Reverse input text.',
      parameters: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] },
      execute: async (_id, args) => ({ text: String(args.text || '').split('').reverse().join('') })
    })
  }
}
