/**
 * metiq Node.js plugin shim.
 * 
 * Loaded by the Go NodePlugin host to bridge TypeScript/CommonJS plugins that
 * cannot run inside Goja (e.g. plugins using fs, http, child_process, or other
 * Node built-ins).
 *
 * Protocol (line-delimited JSON-RPC over stdin/stdout):
 *   Go → Node: {"id":N,"method":"init","params":{"plugin_path":"..."}}
 *   Node → Go: {"id":N,"result":{"manifest":{"tools":[...]}}}
 *
 *   Go → Node: {"id":N,"method":"invoke","params":{"tool":"name","args":{...}}}
 *   Node → Go: {"id":N,"result":<any>}  or  {"id":N,"error":"message"}
 *
 *   Go → Node: {"id":N,"method":"shutdown"}
 *   Node → Go: {"id":N,"result":{"ok":true}}
 */

'use strict';

const readline = require('readline');

let plugin = null;
let manifest = { tools: [] };

// sdk stub — plugins call sdk.log(), sdk.config.get() etc.
// Callbacks that need Go data are synchronous stubs in this tier.
const sdk = {
  log: { info: console.error, warn: console.error, error: console.error },
  config: { get: () => null, set: () => {} },
  nostr: { publish: () => Promise.resolve(), subscribe: () => ({}) },
  agent: { send: () => Promise.resolve('') },
  storage: { get: () => null, set: () => {}, delete: () => {} },
  http: {
    fetch: (url, opts) => {
      const https = url.startsWith('https') ? require('https') : require('http');
      return new Promise((resolve, reject) => {
        const reqOpts = Object.assign({ method: 'GET' }, opts);
        const req = https.request(url, reqOpts, (res) => {
          let body = '';
          res.on('data', (chunk) => body += chunk);
          res.on('end', () => resolve({ status: res.statusCode, text: () => Promise.resolve(body), json: () => Promise.resolve(JSON.parse(body)) }));
        });
        req.on('error', reject);
        if (opts && opts.body) req.write(opts.body);
        req.end();
      });
    }
  }
};

function sendResponse(id, result, error) {
  const msg = error ? { id, error: String(error) } : { id, result };
  process.stdout.write(JSON.stringify(msg) + '\n');
}

async function handleRequest(req) {
  const { id, method, params } = req;
  try {
    switch (method) {
      case 'init': {
        const pluginPath = params.plugin_path;
        // Support both CommonJS and ESM (via require on bundled output).
        let mod;
        try {
          mod = require(pluginPath);
        } catch (e) {
          sendResponse(id, null, `require failed: ${e.message}`);
          return;
        }
        const entry = mod.default || mod;
        if (typeof entry.register === 'function') {
          const result = entry.register(sdk);
          if (result && result.tools) {
            manifest = { tools: result.tools };
          }
        } else if (typeof entry === 'object' && entry.tools) {
          manifest = { tools: entry.tools };
        }
        plugin = entry;
        sendResponse(id, { manifest });
        break;
      }
      case 'invoke': {
        if (!plugin) {
          sendResponse(id, null, 'plugin not initialised');
          return;
        }
        const { tool, args } = params;
        const fn = (plugin.tools || {})[tool] || plugin[tool];
        if (typeof fn !== 'function') {
          sendResponse(id, null, `tool "${tool}" not found`);
          return;
        }
        const value = await Promise.resolve(fn(args || {}, sdk));
        sendResponse(id, value !== undefined ? value : null);
        break;
      }
      case 'shutdown': {
        sendResponse(id, { ok: true });
        process.exit(0);
        break;
      }
      default:
        sendResponse(id, null, `unknown method "${method}"`);
    }
  } catch (err) {
    sendResponse(id, null, err && err.message ? err.message : String(err));
  }
}

const rl = readline.createInterface({ input: process.stdin, terminal: false });
rl.on('line', (line) => {
  line = line.trim();
  if (!line) return;
  let req;
  try {
    req = JSON.parse(line);
  } catch (e) {
    process.stderr.write(`shim parse error: ${e.message}\n`);
    return;
  }
  handleRequest(req).catch((err) => {
    process.stderr.write(`shim unhandled: ${err}\n`);
  });
});

rl.on('close', () => process.exit(0));
