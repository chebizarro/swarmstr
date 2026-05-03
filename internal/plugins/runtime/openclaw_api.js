/**
 * OpenClawPluginApi implementation for the Swarmstr OpenClaw host.
 *
 * This module intentionally stores callable handlers in-process while returning
 * JSON-safe capability metadata to the Go host. It is a compatibility shim for
 * OpenClaw-format plugins, not a full OpenClaw core runtime.
 */
'use strict';

const path = require('path');

const registries = {
  loadedPlugins: new Map(),
  tools: new Map(),
  providers: new Map(),
  hooks: new Map(),
  channels: new Map(),
  services: new Map(),
  commands: new Map(),
  gatewayMethods: new Map(),
  serviceState: new Map(),
  generic: new Map(),
};

let hookSeq = 0;

function createPluginApi(pluginId, pluginConfig = {}, runtimeConfig = {}) {
  const registrations = [];
  const rootDir = pluginConfig.rootDir || runtimeConfig.rootDir || process.cwd();
  const runContext = new Map();

  function addRegistration(registration) {
    const reg = sanitize({ pluginId, ...registration });
    registrations.push(reg);
    if (!registries.generic.has(reg.type)) registries.generic.set(reg.type, []);
    registries.generic.get(reg.type).push(reg);
    return reg;
  }

  const api = {
    id: pluginId,
    name: pluginConfig.name || pluginId,
    version: pluginConfig.version,
    description: pluginConfig.description,
    source: pluginConfig.source || 'swarmstr',
    rootDir,
    registrationMode: runtimeConfig.registrationMode || 'full',
    config: runtimeConfig.config || runtimeConfig || {},
    pluginConfig: pluginConfig.config || runtimeConfig.pluginConfig || {},
    runtime: createRuntimeProxy(pluginId, runtimeConfig),
    logger: createLogger(pluginId),

    registerTool(tool, opts = {}) {
      const toolDef = typeof tool === 'function' ? tool(api) : tool;
      if (!toolDef || typeof toolDef !== 'object') throw new Error('registerTool: tool must be an object or factory');
      if (!toolDef.name || typeof toolDef.name !== 'string') throw new Error('registerTool: tool.name is required');
      const qualifiedName = `${pluginId}/${toolDef.name}`;
      registries.tools.set(qualifiedName, { pluginId, tool: toolDef, execute: toolDef.execute || toolDef.run, opts });
      addRegistration({
        type: 'tool',
        name: toolDef.name,
        qualifiedName,
        description: toolDef.description,
        parameters: extractJsonSchema(toolDef.parameters || toolDef.inputSchema || toolDef.schema),
        ownerOnly: Boolean(toolDef.ownerOnly || opts.ownerOnly),
        optional: Boolean(toolDef.optional || opts.optional),
        opts,
      });
    },

    registerHook(events, handler, opts = {}) {
      const eventList = Array.isArray(events) ? events : [events];
      if (typeof handler !== 'function') throw new Error('registerHook: handler must be a function');
      const hookId = `${pluginId}:hook:${++hookSeq}`;
      for (const event of eventList) {
        if (!registries.hooks.has(event)) registries.hooks.set(event, []);
        registries.hooks.get(event).push({ pluginId, hookId, handler, opts });
      }
      addRegistration({ type: 'hook', hookId, events: eventList, priority: opts.priority, timeoutMs: opts.timeoutMs });
    },

    on(event, handler, opts = {}) {
      api.registerHook(event, handler, opts);
    },

    registerProvider(provider) {
      requireID(provider, 'registerProvider');
      registries.providers.set(provider.id, { pluginId, provider });
      addRegistration({
        type: 'provider',
        id: provider.id,
        label: provider.label,
        docsPath: provider.docsPath,
        hasAuth: Array.isArray(provider.auth) && provider.auth.length > 0,
        hasCatalog: typeof provider.catalog?.run === 'function' || typeof provider.catalog === 'function',
        capabilities: provider.capabilities,
      });
    },

    registerChannel(registration) {
      const plugin = registration && (registration.plugin || registration);
      const id = callOrRead(plugin, 'ID') || plugin?.id || plugin?.ID || registration?.id;
      const channelType = callOrRead(plugin, 'Type') || plugin?.type || registration?.type;
      if (!id) throw new Error('registerChannel: channel id is required');
      registries.channels.set(id, { pluginId, plugin, registration });
      addRegistration({ type: 'channel', id, channelType, configSchema: callOrRead(plugin, 'ConfigSchema') || plugin?.configSchema });
    },

    registerGatewayMethod(method, handler, opts = {}) {
      if (!method || typeof handler !== 'function') throw new Error('registerGatewayMethod: method and handler are required');
      registries.gatewayMethods.set(method, { pluginId, method, handler, opts });
      addRegistration({ type: 'gateway_method', method, scope: opts.scope || 'operator.agent' });
    },

    registerHttpRoute(params) {
      if (!params?.path) throw new Error('registerHttpRoute: path is required');
      addRegistration({ type: 'http_route', path: params.path, auth: params.auth, method: params.method, match: params.match });
      storeGenericHandler('http_route', pluginId, params, { path: params.path });
    },

    registerCli(registrar, opts = {}) {
      addRegistration({ type: 'cli', commands: opts.commands || [], descriptors: opts.descriptors || [] });
      storeGenericHandler('cli', pluginId, registrar, opts);
    },

    registerReload(registration) { addRegistration({ type: 'reload', ...safeDescriptor(registration) }); storeGenericHandler('reload', pluginId, registration); },
    registerNodeHostCommand(command) { addRegistration({ type: 'node_host_command', command: command?.command, ...safeDescriptor(command) }); storeGenericHandler('node_host_command', pluginId, command); },
    registerNodeInvokePolicy(policy) { addRegistration({ type: 'node_invoke_policy', commands: policy?.commands || [], ...safeDescriptor(policy) }); storeGenericHandler('node_invoke_policy', pluginId, policy); },
    registerSecurityAuditCollector(collector) { addRegistration({ type: 'security_audit_collector', id: collector?.id }); storeGenericHandler('security_audit_collector', pluginId, collector); },

    registerService(service) {
      requireID(service, 'registerService');
      registries.services.set(service.id, { pluginId, service });
      addRegistration({ type: 'service', id: service.id, description: service.description, label: service.label });
    },

    registerGatewayDiscoveryService(service) { addProviderLike('gateway_discovery', service); },
    registerCliBackend(backend) { addProviderLike('cli_backend', backend); },
    registerTextTransforms(transforms) { addRegistration({ type: 'text_transforms', ...safeDescriptor(transforms) }); storeGenericHandler('text_transforms', pluginId, transforms); },
    registerConfigMigration(migrate) { addRegistration({ type: 'config_migration' }); storeGenericHandler('config_migration', pluginId, migrate); },
    registerMigrationProvider(provider) { addProviderLike('migration_provider', provider); },
    registerAutoEnableProbe(probe) { addRegistration({ type: 'auto_enable_probe', id: probe?.id }); storeGenericHandler('auto_enable_probe', pluginId, probe); },

    registerSpeechProvider(provider) { addProviderLike('speech_provider', provider); },
    registerRealtimeTranscriptionProvider(provider) { addProviderLike('transcription_provider', provider); },
    registerRealtimeVoiceProvider(provider) { addProviderLike('voice_provider', provider); },
    registerMediaUnderstandingProvider(provider) { addProviderLike('media_understanding_provider', provider); },
    registerImageGenerationProvider(provider) { addProviderLike('image_gen_provider', provider); },
    registerVideoGenerationProvider(provider) { addProviderLike('video_gen_provider', provider); },
    registerMusicGenerationProvider(provider) { addProviderLike('music_gen_provider', provider); },
    registerWebFetchProvider(provider) { addProviderLike('web_fetch_provider', provider); },
    registerWebSearchProvider(provider) { addProviderLike('web_search_provider', provider); },
    registerMemoryEmbeddingProvider(adapter) { addProviderLike('memory_embedding_provider', adapter); },

    registerInteractiveHandler(registration) { addRegistration({ type: 'interactive_handler', id: registration?.id, ...safeDescriptor(registration) }); storeGenericHandler('interactive_handler', pluginId, registration); },
    onConversationBindingResolved(handler) { addRegistration({ type: 'conversation_binding_listener' }); storeGenericHandler('conversation_binding_listener', pluginId, handler); },

    registerCommand(command) {
      if (!command?.name) throw new Error('registerCommand: command.name is required');
      registries.commands.set(command.name, { pluginId, command });
      addRegistration({ type: 'command', name: command.name, description: command.description, acceptsArgs: Boolean(command.acceptsArgs) });
    },

    registerContextEngine(id, factory) { addRegistration({ type: 'context_engine', id }); storeGenericHandler('context_engine', pluginId, factory, { id }); },
    registerCompactionProvider(provider) { addProviderLike('compaction_provider', provider); },
    registerAgentHarness(harness) { addProviderLike('agent_harness', harness); },
    registerCodexAppServerExtensionFactory(factory) { addRegistration({ type: 'codex_app_server_extension_factory' }); storeGenericHandler('codex_app_server_extension_factory', pluginId, factory); },
    registerAgentToolResultMiddleware(handler, options = {}) { addRegistration({ type: 'agent_tool_result_middleware', options }); storeGenericHandler('agent_tool_result_middleware', pluginId, handler, options); },
    registerSessionExtension(extension) { addRegistration({ type: 'session_extension', id: extension?.id || extension?.namespace, ...safeDescriptor(extension) }); storeGenericHandler('session_extension', pluginId, extension); },
    async enqueueNextTurnInjection(injection) { addRegistration({ type: 'next_turn_injection', ...safeDescriptor(injection) }); return { ok: true, queued: true }; },
    registerTrustedToolPolicy(policy) { addRegistration({ type: 'trusted_tool_policy', id: policy?.id, ...safeDescriptor(policy) }); storeGenericHandler('trusted_tool_policy', pluginId, policy); },
    registerToolMetadata(metadata) { addRegistration({ type: 'tool_metadata', name: metadata?.toolName || metadata?.name, ...safeDescriptor(metadata) }); storeGenericHandler('tool_metadata', pluginId, metadata); },
    registerControlUiDescriptor(descriptor) { addRegistration({ type: 'control_ui_descriptor', id: descriptor?.id, ...safeDescriptor(descriptor) }); storeGenericHandler('control_ui_descriptor', pluginId, descriptor); },
    registerRuntimeLifecycle(lifecycle) { addRegistration({ type: 'runtime_lifecycle', id: lifecycle?.id }); storeGenericHandler('runtime_lifecycle', pluginId, lifecycle); },
    registerAgentEventSubscription(subscription) { addRegistration({ type: 'agent_event_subscription', id: subscription?.id, events: subscription?.events }); storeGenericHandler('agent_event_subscription', pluginId, subscription); },

    setRunContext(patch) {
      if (!patch || typeof patch !== 'object') return false;
      const runId = patch.runId || 'default';
      const namespace = patch.namespace || pluginId;
      runContext.set(`${runId}:${namespace}`, patch.value ?? patch.data ?? patch.patch ?? null);
      return true;
    },
    getRunContext(params) {
      const runId = params?.runId || 'default';
      const namespace = params?.namespace || pluginId;
      return runContext.get(`${runId}:${namespace}`);
    },
    clearRunContext(params = {}) {
      const runId = params.runId || 'default';
      const namespace = params.namespace;
      for (const key of [...runContext.keys()]) {
        if (namespace ? key === `${runId}:${namespace}` : key.startsWith(`${runId}:`)) runContext.delete(key);
      }
    },

    registerSessionSchedulerJob(job) { addRegistration({ type: 'session_scheduler_job', id: job?.id, ...safeDescriptor(job) }); storeGenericHandler('session_scheduler_job', pluginId, job); return { id: job?.id, dispose() {} }; },
    registerDetachedTaskRuntime(runtime) { addRegistration({ type: 'detached_task_runtime', id: runtime?.id }); storeGenericHandler('detached_task_runtime', pluginId, runtime); },
    registerMemoryCapability(capability) { addRegistration({ type: 'memory_capability', id: capability?.id, ...safeDescriptor(capability) }); storeGenericHandler('memory_capability', pluginId, capability); },
    registerMemoryPromptSection(builder) { addRegistration({ type: 'memory_prompt_section' }); storeGenericHandler('memory_prompt_section', pluginId, builder); },
    registerMemoryPromptSupplement(builder) { addRegistration({ type: 'memory_prompt_supplement' }); storeGenericHandler('memory_prompt_supplement', pluginId, builder); },
    registerMemoryCorpusSupplement(supplement) { addRegistration({ type: 'memory_corpus_supplement', id: supplement?.id }); storeGenericHandler('memory_corpus_supplement', pluginId, supplement); },
    registerMemoryFlushPlan(resolver) { addRegistration({ type: 'memory_flush_plan' }); storeGenericHandler('memory_flush_plan', pluginId, resolver); },
    registerMemoryRuntime(runtime) { addRegistration({ type: 'memory_runtime', id: runtime?.id }); storeGenericHandler('memory_runtime', pluginId, runtime); },

    resolvePath(input) {
      if (typeof input !== 'string') throw new Error('resolvePath: input must be a string');
      return path.isAbsolute(input) ? path.normalize(input) : path.resolve(rootDir, input);
    },
  };

  function addProviderLike(type, provider) {
    const id = provider?.id || provider?.name || `${pluginId}:${type}`;
    addRegistration({ type, id, label: provider?.label, description: provider?.description, hasAuth: Array.isArray(provider?.auth) && provider.auth.length > 0, hasCatalog: typeof provider?.catalog?.run === 'function' });
    storeGenericHandler(type, pluginId, provider, { id });
  }

  return { api, getRegistrations: () => registrations };
}

async function invokeTool(pluginId, tool, args, meta = {}) {
  const qualifiedName = `${pluginId}/${tool}`;
  const registration = registries.tools.get(qualifiedName);
  if (!registration) throw new Error(`tool not found: ${qualifiedName}`);
  const fn = registration.execute;
  if (typeof fn !== 'function') throw new Error(`tool is not executable: ${qualifiedName}`);
  const ctx = { pluginId, toolCallId: `call-${Date.now()}`, meta };
  return await fn(ctx.toolCallId, args || {}, undefined, undefined, ctx);
}

async function invokeHook(event, payload) {
  const handlers = [...(registries.hooks.get(event) || [])].sort((a, b) => (a.opts.priority ?? 100) - (b.opts.priority ?? 100));
  const results = [];
  for (const { pluginId, hookId, handler, opts } of handlers) {
    try {
      const result = await handler(payload);
      results.push({ pluginId, hookId, ok: true, result: result === undefined ? null : result });
    } catch (err) {
      results.push({ pluginId, hookId, ok: false, error: errorMessage(err) });
      if (opts.stopOnError) break;
    }
  }
  return { results };
}

async function invokeProvider(providerId, method, params) {
  const registration = registries.providers.get(providerId);
  if (!registration) throw new Error(`provider not found: ${providerId}`);
  const provider = registration.provider;
  if (method === 'catalog') {
    if (typeof provider.catalog?.run === 'function') return await provider.catalog.run(params);
    if (typeof provider.catalog === 'function') return await provider.catalog(params);
  }
  if (method === 'auth') {
    const authId = params?.auth_id || params?.authId || params?.id;
    const authMethod = (provider.auth || []).find((a) => a.id === authId) || provider.auth?.[0];
    if (!authMethod) throw new Error(`auth method not found: ${authId}`);
    return await (typeof authMethod.run === 'function' ? authMethod.run(params?.ctx || params) : authMethod(params));
  }
  const target = provider[method];
  if (typeof target === 'function') return await target(params);
  if (typeof target?.run === 'function') return await target.run(params);
  throw new Error(`unknown provider method: ${method}`);
}

async function startService(serviceId, params) {
  const registration = registries.services.get(serviceId);
  if (!registration) throw new Error(`service not found: ${serviceId}`);
  const service = registration.service;
  if (typeof service.start === 'function') {
    const result = await service.start(params || {});
    registries.serviceState.set(serviceId, true);
    return result === undefined ? { ok: true } : result;
  }
  registries.serviceState.set(serviceId, true);
  return { ok: true };
}

async function stopService(serviceId, params) {
  const registration = registries.services.get(serviceId);
  if (!registration) throw new Error(`service not found: ${serviceId}`);
  const service = registration.service;
  if (typeof service.stop === 'function') {
    const result = await service.stop(params || {});
    registries.serviceState.set(serviceId, false);
    return result === undefined ? { ok: true } : result;
  }
  registries.serviceState.set(serviceId, false);
  return { ok: true };
}

async function shutdownPlugins() {
  const lifecycles = registries.generic.get('runtime_lifecycle') || [];
  for (const lifecycleReg of lifecycles) {
    const handler = lifecycleReg.__handler;
    try {
      if (typeof handler?.shutdown === 'function') await handler.shutdown();
      if (typeof handler?.cleanup === 'function') await handler.cleanup();
    } catch (err) {
      process.stderr.write(`openclaw lifecycle shutdown failed: ${errorMessage(err)}\n`);
    }
  }
}

function createRuntimeProxy(pluginId, runtimeConfig = {}) {
  const store = new Map();
  return {
    nostr: {
      publish: async () => ({ ok: false, error: 'nostr runtime not wired in Phase 1' }),
      fetch: async () => [],
      encrypt: async (_pubkey, content) => content,
      decrypt: async (_pubkey, ciphertext) => ciphertext,
    },
    config: { get: (key) => runtimeConfig[key] ?? runtimeConfig.config?.[key] ?? null },
    fetch: globalThis.fetch ? globalThis.fetch.bind(globalThis) : async () => { throw new Error('fetch unavailable'); },
    storage: {
      get: async (key) => store.has(key) ? store.get(key) : null,
      set: async (key, value) => { store.set(key, value); return true; },
      del: async (key) => store.delete(key),
      delete: async (key) => store.delete(key),
    },
    agent: { complete: async () => '' },
    sessions: { get: async () => null, set: async () => true },
    events: { emit: async () => true },
    pluginId,
  };
}

function createLogger(pluginId) {
  const write = (level, args) => process.stderr.write(`[openclaw:${pluginId}:${level}] ${args.map(String).join(' ')}\n`);
  return {
    info: (...args) => write('info', args),
    warn: (...args) => write('warn', args),
    error: (...args) => write('error', args),
    debug: (...args) => write('debug', args),
    child: () => createLogger(pluginId),
  };
}

function storeGenericHandler(type, pluginId, handler, opts = {}) {
  if (!registries.generic.has(type)) registries.generic.set(type, []);
  registries.generic.get(type).push({ pluginId, type, __handler: handler, opts });
}

function requireID(obj, label) {
  if (!obj || !obj.id) throw new Error(`${label}: id is required`);
}

function callOrRead(obj, name) {
  if (!obj) return undefined;
  if (typeof obj[name] === 'function') return obj[name]();
  return obj[name];
}

function extractJsonSchema(schema) {
  if (schema === undefined) return undefined;
  return sanitize(schema);
}

function safeDescriptor(value) {
  const sanitized = sanitize(value);
  return sanitized && typeof sanitized === 'object' && !Array.isArray(sanitized) ? sanitized : { value: sanitized };
}

function sanitize(value, seen = new WeakSet(), depth = 0) {
  if (value === undefined || typeof value === 'function' || typeof value === 'symbol') return undefined;
  if (value === null || typeof value !== 'object') return value;
  if (seen.has(value)) return '[Circular]';
  if (depth > 8) return '[MaxDepth]';
  seen.add(value);
  if (Array.isArray(value)) return value.map((v) => sanitize(v, seen, depth + 1)).filter((v) => v !== undefined);
  const out = {};
  for (const [key, val] of Object.entries(value)) {
    const safe = sanitize(val, seen, depth + 1);
    if (safe !== undefined) out[key] = safe;
  }
  seen.delete(value);
  return out;
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}

module.exports = {
  createPluginApi,
  invokeTool,
  invokeHook,
  invokeProvider,
  startService,
  stopService,
  shutdownPlugins,
  registries,
  sanitize,
};
