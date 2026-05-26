/**
 * metiq Node.js plugin shim.
 * 
 * Loaded by the Go NodePlugin host to bridge TypeScript/CommonJS plugins that
 * cannot run inside Goja (e.g. plugins using fs, http, child_process, or other
 * Node built-ins).
 */

'use strict';

const fs = require('fs');
const path = require('path');
const Module = require('module');
const readline = require('readline');

let plugin = null;
let manifest = { tools: [] };
let registrations = null;
let allowedPermissions = {};
let pluginRootDir = process.cwd();
let registrationOpen = false;

function permissionsAllow(namespace) {
  if (allowedPermissions === true) return true;
  if (Array.isArray(allowedPermissions)) {
    return allowedPermissions.includes('*') || allowedPermissions.includes(namespace) || (namespace === 'http' && allowedPermissions.includes('network'));
  }
  const p = allowedPermissions || {};
  if (p.all === true || p['*'] === true) return true;
  if (namespace === 'http') return p.http === true || !!p.network;
  return p[namespace] === true || !!p[namespace];
}

function deniedNamespace(namespace) {
  const fail = () => { throw new Error(`plugin host namespace "${namespace}" is not permitted`); };
  return new Proxy({ available: false, reason: 'permission not declared in plugin manifest' }, {
    get(target, prop) {
      if (prop in target) return target[prop];
      return fail;
    },
    apply: fail
  });
}

function allow(namespace, value) {
  return permissionsAllow(namespace) ? value : deniedNamespace(namespace);
}

function makeRegistrations() {
  return {
    tools: {},
    toolSchemas: [],
    hooks: [],
    channels: [],
    providers: [],
    services: []
  };
}

function ensureRegistrationOpen(name) {
  if (!registrationOpen) throw new Error(`plugin registration window is closed for ${name || 'registration'}`);
}

function createSDK() {
  const sdk = {
    id: 'metiq-node-plugin',
    name: 'Metiq Node Plugin Host',
    version: 'metiq.plugin.host.v1',
    api: { version: 'metiq.plugin.host.v1', permissions: allowedPermissions || {} },
    registrationMode: 'runtime',
    log: { info: console.error, warn: console.error, error: console.error },
    logger: { info: console.error, warn: console.error, error: console.error },
    config: allow('config', { get: () => null, set: () => {} }),
    nostr: allow('nostr', { publish: () => Promise.resolve(), subscribe: () => ({}) }),
    agent: allow('agent', { send: () => Promise.resolve('') }),
    storage: allow('storage', { get: () => null, set: () => {}, delete: () => {} }),
    session: allow('session', { list: () => [], get: () => null, append: () => {} }),
    task: allow('task', { list: () => [], get: () => null, update: () => ({}) }),
    memory: allow('memory', { search: () => [], store: () => ({}) }),
    webSearch: allow('web_search', { search: () => [], fetch: () => ({}) }),
    http: allow('http', {
      fetch: (url, opts) => {
        const httpMod = url.startsWith('https') ? require('https') : require('http');
        return new Promise((resolve, reject) => {
          const reqOpts = Object.assign({ method: 'GET' }, opts || {});
          const req = httpMod.request(url, reqOpts, (res) => {
            let body = '';
            res.on('data', (chunk) => body += chunk);
            res.on('end', () => resolve({
              status: res.statusCode,
              headers: res.headers,
              text: () => Promise.resolve(body),
              json: () => Promise.resolve(JSON.parse(body))
            }));
          });
          req.on('error', reject);
          if (opts && opts.body) req.write(opts.body);
          req.end();
        });
      }
    }),
    resolvePath: (input) => path.resolve(process.cwd(), input),
    on: () => {},
    registerTool: (tool) => {
      ensureRegistrationOpen('registerTool');
      if (!tool || !tool.name) return;
      registrations.toolSchemas.push({
        name: tool.name,
        description: tool.description || '',
        parameters: tool.parameters || tool.input_schema || tool.inputSchema || undefined
      });
      if (typeof tool.execute === 'function') registrations.tools[tool.name] = tool.execute;
    },
    registerHook: (event, handler, opts) => { ensureRegistrationOpen('registerHook'); registrations.hooks.push({ event, handler, opts: opts || {} }); },
    registerChannel: (channel) => { ensureRegistrationOpen('registerChannel'); registrations.channels.push(channel); },
    registerProvider: (provider) => { ensureRegistrationOpen('registerProvider'); registrations.providers.push(provider); },
    registerService: (service) => { ensureRegistrationOpen('registerService'); registrations.services.push(service); },
    registerHttpRoute: () => {},
    registerHostedMediaResolver: () => {},
    registerGatewayMethod: () => {},
    registerCli: () => {},
    registerNodeCliFeature: () => {},
    registerReload: () => {},
    registerNodeHostCommand: () => {},
    registerNodeInvokePolicy: () => {},
    registerSecurityAuditCollector: () => {},
    registerGatewayDiscoveryService: () => {},
    registerCliBackend: () => {},
    registerTextTransforms: () => {},
    registerConfigMigration: () => {},
    registerMigrationProvider: () => {},
    registerAutoEnableProbe: () => {},
    registerModelCatalogProvider: () => {},
    registerSpeechProvider: () => {},
    registerRealtimeTranscriptionProvider: () => {},
    registerRealtimeVoiceProvider: () => {},
    registerMediaUnderstandingProvider: () => {},
    registerImageGenerationProvider: () => {},
    registerVideoGenerationProvider: () => {},
    registerMusicGenerationProvider: () => {},
    registerWebFetchProvider: () => {},
    registerWebSearchProvider: () => {},
    registerInteractiveHandler: () => {},
    onConversationBindingResolved: () => {},
    registerCommand: () => {},
    registerContextEngine: () => {},
    registerCompactionProvider: () => {},
    registerAgentHarness: () => {},
    registerCodexAppServerExtensionFactory: () => {},
    registerAgentToolResultMiddleware: () => {},
    registerSessionExtension: () => {},
    enqueueNextTurnInjection: async () => ({ enqueued: false, id: '', sessionKey: '' }),
    registerTrustedToolPolicy: () => {},
    registerToolMetadata: () => {},
    registerControlUiDescriptor: () => {},
    registerRuntimeLifecycle: () => {},
    registerAgentEventSubscription: () => {},
    emitAgentEvent: () => ({ emitted: false, reason: 'not wired' }),
    setRunContext: () => false,
    getRunContext: () => undefined,
    clearRunContext: () => {},
    registerSessionSchedulerJob: () => undefined,
    registerSessionAction: () => {},
    sendSessionAttachment: async () => ({ ok: false, error: 'not wired' }),
    scheduleSessionTurn: async () => undefined,
    unscheduleSessionTurnsByTag: async () => ({ removed: 0, failed: 0 }),
    registerDetachedTaskRuntime: () => {},
    registerMemoryCapability: () => {},
    registerMemoryPromptSection: () => {},
    registerMemoryPromptSupplement: () => {},
    registerMemoryCorpusSupplement: () => {},
    registerMemoryFlushPlan: () => {},
    registerMemoryRuntime: () => {},
    registerMemoryEmbeddingProvider: () => {}
  };
  return sdk;
}

let sdk = createSDK();
const originalLoad = Module._load;
Module._load = function(request, parent, isMain) {
  if (request === '@openclaw/plugin-sdk') {
    return sdk;
  }
  return originalLoad.apply(this, arguments);
};

function sendResponse(id, result, error) {
  const msg = error ? { id, error: String(error) } : { id, result };
  process.stdout.write(JSON.stringify(msg) + '\n');
}

function resolveEntrypoint(pluginPath) {
  const stat = fs.statSync(pluginPath);
  if (!stat.isDirectory()) return pluginPath;
  const pkgPath = path.join(pluginPath, 'package.json');
  if (fs.existsSync(pkgPath)) {
    const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
    const entry = pkg.swarmstr || pkg.openclaw || pkg.main || 'index.js';
    return path.resolve(pluginPath, typeof entry === 'string' ? entry : 'index.js');
  }
  return path.join(pluginPath, 'index.js');
}

function manifestFromEntry(entry, pluginPath) {
  const pkgPath = fs.existsSync(path.join(pluginPath, 'package.json')) ? path.join(pluginPath, 'package.json') : null;
  let pkg = {};
  if (pkgPath) {
    try { pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8')); } catch (_) { pkg = {}; }
  }
  return {
    id: entry.id || entry.name || pkg.name || path.basename(pluginPath),
    version: entry.version || pkg.version || '',
    description: entry.description || pkg.description || '',
    permissions: entry.permissions || pkg.metiq?.permissions || pkg.openclaw?.permissions || undefined,
    tools: registrations.toolSchemas.length ? registrations.toolSchemas : (entry.tools && !Array.isArray(entry.tools) ? [] : (entry.tools || []))
  };
}

async function initialisePlugin(pluginPath) {
  registrations = makeRegistrations();
  registrationOpen = true;
  pluginRootDir = pluginPath;
  sdk = createSDK();
  sdk.rootDir = pluginPath;
  sdk.resolvePath = (input) => path.resolve(pluginPath, input);

  const entrypoint = resolveEntrypoint(pluginPath);
  const mod = require(entrypoint);
  const entry = mod.default || mod;

  if (typeof entry.register === 'function') {
    const result = await Promise.resolve(entry.register(sdk));
    if (result && result.tools) {
      manifest = Object.assign(manifestFromEntry(entry, pluginPath), { tools: result.tools });
    } else {
      manifest = manifestFromEntry(entry, pluginPath);
    }
  } else if (typeof entry === 'object' && entry.tools) {
    manifest = manifestFromEntry(entry, pluginPath);
  } else {
    manifest = manifestFromEntry(entry || {}, pluginPath);
  }

  if (entry && typeof entry.init === 'function') {
    await Promise.resolve(entry.init(sdk, { plugin_path: pluginPath }));
  } else if (entry && typeof entry.initialize === 'function') {
    await Promise.resolve(entry.initialize(sdk, { plugin_path: pluginPath }));
  }

  plugin = entry;
  registrationOpen = false;
  allowedPermissions = manifest.permissions || {};
  sdk = createSDK();
  sdk.rootDir = pluginPath;
  sdk.resolvePath = (input) => path.resolve(pluginPath, input);
  return { manifest };
}

async function handleRequest(req) {
  const { id, method, params } = req;
  try {
    switch (method) {
      case 'init': {
        const pluginPath = params.plugin_path;
        try {
          sendResponse(id, await initialisePlugin(pluginPath));
        } catch (e) {
          sendResponse(id, null, `require failed: ${e.message}`);
        }
        break;
      }
      case 'set_permissions': {
        allowedPermissions = params && params.permissions ? params.permissions : {};
        sdk = createSDK();
        sdk.rootDir = pluginRootDir;
        sdk.resolvePath = (input) => path.resolve(pluginRootDir, input);
        sendResponse(id, { ok: true });
        break;
      }
      case 'invoke': {
        if (!plugin) {
          sendResponse(id, null, 'plugin not initialised');
          return;
        }
        const { tool, args } = params;
        const registeredFn = registrations.tools[tool];
        const legacyFn = !registeredFn ? ((plugin.tools || {})[tool] || plugin[tool]) : null;
        const fn = registeredFn || legacyFn;
        if (typeof fn !== 'function') {
          sendResponse(id, null, `tool "${tool}" not found`);
          return;
        }
        const value = registeredFn
          ? await Promise.resolve(fn(tool, args || {}, sdk))
          : await Promise.resolve(fn(args || {}, sdk));
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
