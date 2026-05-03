/**
 * OpenClaw SDK Runtime Shim for Swarmstr.
 *
 * The Go host sends line-delimited JSON-RPC requests on stdin and receives
 * matching responses on stdout. Plugin logs and diagnostics go to stderr only.
 */
'use strict';

const fs = require('fs');
const util = require('util');
const path = require('path');
const readline = require('readline');
const Module = require('module');
const {
  createPluginApi,
  invokeTool,
  invokeHook,
  invokeProvider,
  startService,
  stopService,
  shutdownPlugins,
  registries,
} = require('./openclaw_api.js');

redirectConsoleStdout();
installOpenClawSDKAliases();

let shuttingDown = false;

function redirectConsoleStdout() {
  const write = (level, args) => process.stderr.write(`[plugin:${level}] ${args.map((arg) => typeof arg === 'string' ? arg : util.inspect(arg, { depth: 6, colors: false })).join(' ')}\n`);
  console.log = (...args) => write('log', args);
  console.info = (...args) => write('info', args);
  console.debug = (...args) => write('debug', args);
}

function installOpenClawSDKAliases() {
  const originalLoad = Module._load;
  Module._load = function patchedOpenClawLoad(request, parent, isMain) {
    if (request === 'openclaw/plugin-sdk/plugin-entry' || request === 'openclaw/plugin-sdk/core' || request === 'openclaw/plugin-sdk/channel-core' || request === 'openclaw/plugin-sdk/channel-entry-contract') {
      return openClawSDKStub;
    }
    if (request && request.startsWith('openclaw/plugin-sdk/')) {
      return openClawSDKStub;
    }
    return originalLoad.apply(this, arguments);
  };
}

const emptyPluginConfigSchema = { type: 'object', additionalProperties: true };
const openClawSDKStub = {
  definePluginEntry: (entry) => entry,
  defineSetupPluginEntry: (entry) => entry,
  defineChannelPluginEntry: (entry) => ({
    id: entry.id,
    name: entry.name,
    description: entry.description,
    configSchema: entry.configSchema || emptyPluginConfigSchema,
    register(api) {
      if (typeof entry.setRuntime === 'function') entry.setRuntime(api.runtime);
      if (typeof entry.registerCliMetadata === 'function') entry.registerCliMetadata(api);
      if (api.registrationMode === 'full' && typeof entry.registerFull === 'function') entry.registerFull(api);
      if (entry.plugin) api.registerChannel({ plugin: entry.plugin });
    },
  }),
  defineBundledChannelEntry: (entry) => openClawSDKStub.defineChannelPluginEntry(entry),
  emptyPluginConfigSchema,
  jsonResult: (value) => value,
};

function sendResponse(id, result, error) {
  const response = error ? { id, error: String(error) } : { id, result: result === undefined ? null : result };
  process.stdout.write(JSON.stringify(response) + '\n');
}

async function handleRequest(req) {
  const { method, params } = req;
  if (shuttingDown && method !== 'shutdown') {
    throw new Error('openclaw shim is shutting down');
  }
  switch (method) {
    case 'load_plugin':
      return await handleLoadPlugin(params || {});
    case 'init_plugin':
      return await handleInitPlugin(params || {});
    case 'invoke_tool':
      return await invokeTool(params.plugin_id, params.tool, params.args || {}, params.meta || {});
    case 'invoke_hook':
      return await invokeHook(params.event, params.payload);
    case 'invoke_provider':
      return await invokeProvider(params.provider_id, params.method, params.params);
    case 'start_service':
      return await startService(params.service_id, params.params);
    case 'stop_service':
      return await stopService(params.service_id, params.params);
    case 'ping':
      return { ok: true };
    case 'shutdown':
      shuttingDown = true;
      await shutdownPlugins();
      setImmediate(() => process.exit(0));
      return { ok: true };
    default:
      throw new Error(`unknown method: ${method}`);
  }
}

async function handleLoadPlugin({ plugin_path, config = {} }) {
  if (!plugin_path) throw new Error('load_plugin: plugin_path is required');
  const resolvedPath = resolvePluginPath(plugin_path);
  delete require.cache[require.resolve(resolvedPath)];
  const mod = require(resolvedPath);
  const entry = mod.default || mod.plugin || mod;
  if (!entry) throw new Error(`plugin entry not found: ${resolvedPath}`);

  const pluginId = entry.id || path.basename(resolvedPath, path.extname(resolvedPath));
  if (registries.loadedPlugins.has(pluginId)) {
    throw new Error(`plugin already loaded: ${pluginId}`);
  }
  const rootDir = fs.statSync(plugin_path).isDirectory() ? plugin_path : path.dirname(resolvedPath);
  const pluginConfig = { ...entry, rootDir };
  const runtimeConfig = { ...(config || {}), rootDir };
  const { api, getRegistrations } = createPluginApi(pluginId, pluginConfig, runtimeConfig);

  if (typeof entry.register === 'function') {
    await entry.register(api);
  } else if (typeof entry === 'function') {
    await entry(api);
  } else {
    throw new Error(`plugin ${pluginId} does not export register(api)`);
  }

  registries.loadedPlugins.set(pluginId, { entry, api, rootDir });
  return {
    plugin_id: pluginId,
    name: entry.name || pluginId,
    version: entry.version,
    description: entry.description,
    registrations: getRegistrations(),
  };
}

async function handleInitPlugin({ plugin_id, params }) {
  const loaded = registries.loadedPlugins.get(plugin_id);
  if (!loaded) throw new Error(`plugin not loaded: ${plugin_id}`);
  const entry = loaded.entry;
  if (typeof entry.init === 'function') return await entry.init(loaded.api, params);
  if (typeof entry.initialize === 'function') return await entry.initialize(loaded.api, params);
  return { ok: true, initialized: false };
}

function resolvePluginPath(pluginPath) {
  const stat = fs.statSync(pluginPath);
  if (stat.isDirectory()) {
    const packagePath = path.join(pluginPath, 'package.json');
    if (fs.existsSync(packagePath)) {
      try {
        const pkg = JSON.parse(fs.readFileSync(packagePath, 'utf8'));
        const entry = pkg.openclaw?.entry || pkg.main || 'index.js';
        return path.resolve(pluginPath, entry);
      } catch (err) {
        throw new Error(`read package.json: ${err.message}`);
      }
    }
    return path.join(pluginPath, 'index.js');
  }
  return pluginPath;
}

const rl = readline.createInterface({ input: process.stdin, terminal: false });

rl.on('line', (line) => {
  line = line.trim();
  if (!line) return;
  let req;
  try {
    req = JSON.parse(line);
  } catch (err) {
    process.stderr.write(`openclaw shim parse error: ${err.message}\n`);
    return;
  }
  Promise.resolve(handleRequest(req))
    .then((result) => sendResponse(req.id, result))
    .catch((err) => sendResponse(req.id, null, err && err.message ? err.message : String(err)));
});

rl.on('close', () => process.exit(0));
