# OpenClaw Plugin SDK Adoption Plan for Swarmstr

> **Status**: Draft  
> **Author**: Architecture Review  
> **Created**: 2026-05-03  
> **Target**: Full OpenClaw plugin interoperability

## Executive Summary

This document outlines a comprehensive plan to adopt OpenClaw's plugin SDK as Swarmstr's native plugin system. This adoption will enable Swarmstr to run unmodified OpenClaw plugins, providing immediate access to OpenClaw's rich plugin ecosystem including 50+ provider integrations, 20+ channel plugins, and extensive tool libraries.

### Goals

1. **Full Plugin Compatibility**: Run unmodified OpenClaw plugins in Swarmstr
2. **SDK Parity**: Implement `OpenClawPluginApi` with all 35+ registration methods
3. **Hook System**: Support all 35+ hook event types
4. **Provider Support**: Enable OpenClaw provider plugins (Anthropic, OpenAI, Google, etc.)
5. **Channel Support**: Enable OpenClaw channel plugins alongside native Swarmstr channels
6. **Ecosystem Access**: Allow installation of plugins from ClawHub registry

### Non-Goals

- Porting OpenClaw's UI components
- Supporting OpenClaw's CLI command infrastructure (partial support only)
- Real-time voice/WebSocket providers (Phase 2+)

---

## Part 1: Current State Analysis

### 1.1 Swarmstr Plugin Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Swarmstr Current State                    │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Plugin Runtimes:                                           │
│  ├── Goja (embedded JS VM) ─── Simple JS plugins           │
│  └── Node.js subprocess ────── Complex JS/TS plugins       │
│                                                             │
│  Plugin Interface:                                          │
│  ├── exports.manifest = { id, version, tools: [...] }      │
│  └── exports.invoke = async (toolName, args, ctx) => ...   │
│                                                             │
│  Host APIs:                                                 │
│  ├── nostr.* ─── Nostr protocol operations                 │
│  ├── config.* ── Read-only config access                   │
│  ├── http.* ──── HTTP GET/POST                             │
│  ├── storage.* ─ Key-value storage                         │
│  ├── log.* ───── Structured logging                        │
│  └── agent.* ─── LLM completions                           │
│                                                             │
│  Capability Registries:                                     │
│  ├── internal/plugins/manifest/registry.go                 │
│  ├── internal/plugins/lifecycle/lifecycle.go               │
│  └── internal/plugins/manager/manager.go                   │
│                                                             │
│  Channel Plugins (Go-native):                               │
│  ├── internal/extensions/discord/                          │
│  ├── internal/extensions/telegram/                         │
│  ├── internal/extensions/slack/                            │
│  └── ... (18 total)                                        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Key Files:**
- `internal/plugins/sdk/api.go` - SDK type definitions
- `internal/plugins/runtime/goja_host.go` - Goja plugin loader
- `internal/plugins/runtime/node_host.go` - Node.js plugin loader
- `internal/plugins/runtime/node_shim.js` - Node.js bridge shim
- `internal/plugins/manager/manager.go` - Plugin manager
- `internal/agent/provider.go` - Provider interface

### 1.2 OpenClaw Plugin Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    OpenClaw Plugin System                    │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Plugin Entry:                                              │
│  export default definePluginEntry({                         │
│    id, name, description, configSchema,                     │
│    register(api: OpenClawPluginApi) { ... }                 │
│  });                                                        │
│                                                             │
│  OpenClawPluginApi (35+ methods):                           │
│  ├── registerTool(tool, opts)                               │
│  ├── registerProvider(provider)                             │
│  ├── registerChannel(registration)                          │
│  ├── registerHook(events, handler, opts)                    │
│  ├── registerService(service)                               │
│  ├── registerCommand(command)                               │
│  ├── registerGatewayMethod(method, handler, opts)           │
│  ├── registerSpeechProvider(provider)                       │
│  ├── registerRealtimeTranscriptionProvider(provider)        │
│  ├── registerImageGenerationProvider(provider)              │
│  ├── registerVideoGenerationProvider(provider)              │
│  ├── registerWebSearchProvider(provider)                    │
│  ├── registerWebFetchProvider(provider)                     │
│  ├── registerMemoryEmbeddingProvider(provider)              │
│  ├── registerMigrationProvider(provider)                    │
│  ├── registerConfigMigration(migrate)                       │
│  └── ... (20+ more)                                         │
│                                                             │
│  Hook Events (35+ types):                                   │
│  ├── before_agent_start, before_agent_reply                 │
│  ├── before_tool_call, after_tool_call                      │
│  ├── before_prompt_build, before_model_resolve              │
│  ├── llm_input, llm_output                                  │
│  ├── inbound_claim, reply_dispatch                          │
│  ├── session_start, session_end                             │
│  ├── message_received, message_sending, message_sent        │
│  ├── subagent_spawning, subagent_spawned, subagent_ended    │
│  ├── gateway_start, gateway_stop                            │
│  └── ... (20+ more)                                         │
│                                                             │
│  Provider Interface:                                        │
│  ├── id, label, docsPath                                    │
│  ├── auth: ProviderAuthMethod[]                             │
│  ├── catalog: (ctx) => ProviderCatalogResult                │
│  ├── transport hooks (stream, replay, thinking)             │
│  └── model normalization, failover, etc.                    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Key Files:**
- `src/plugins/types.ts` - Core type definitions (2560 lines)
- `src/plugin-sdk/plugin-entry.ts` - Plugin entry helpers
- `src/plugins/hook-types.ts` - Hook event definitions
- `src/plugins/host-hooks.ts` - Host hook implementations
- `src/plugins/providers.ts` - Provider registry
- `packages/plugin-sdk/` - Published SDK package

### 1.3 Compatibility Gap Analysis

| Capability | Swarmstr | OpenClaw | Gap |
|-----------|----------|----------|-----|
| **Plugin Format** | Declarative manifest | Imperative registration | High |
| **Tool Definition** | JSON Schema | TypeBox + execute() | Medium |
| **Tool Execution** | Single invoke() | Per-tool execute() | Medium |
| **Providers** | Go ChatProvider | ProviderPlugin | High |
| **Channels** | Go ChannelPlugin | TS ChannelPlugin | Medium |
| **Hooks** | None | 35+ event types | Critical |
| **Services** | None | Background services | High |
| **Commands** | None | CLI extensions | Medium |
| **Config Schema** | None | Zod schemas | Medium |
| **Gateway Methods** | Go GatewayMethod | TS handlers | Low |
| **Speech/TTS** | None | SpeechProvider | High |
| **Image Gen** | None | ImageGenProvider | High |
| **Web Search** | None | WebSearchProvider | Medium |
| **Memory** | None | MemoryProvider | High |

---

## Part 2: Architecture Design

### 2.1 Target Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                      Swarmstr with OpenClaw SDK                      │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    Plugin Host Layer (Go)                    │   │
│  │  ┌─────────────────┐    ┌─────────────────────────────────┐ │   │
│  │  │  Node.js Host   │◄──►│  OpenClaw SDK Runtime (Node)    │ │   │
│  │  │  (subprocess)   │    │  ┌───────────────────────────┐  │ │   │
│  │  │                 │    │  │  OpenClawPluginApi impl   │  │ │   │
│  │  │  JSON-RPC over  │    │  │  - Registration capture   │  │ │   │
│  │  │  stdin/stdout   │    │  │  - Hook handler storage   │  │ │   │
│  │  │                 │    │  │  - Provider bridging      │  │ │   │
│  │  └─────────────────┘    │  └───────────────────────────┘  │ │   │
│  │                         └─────────────────────────────────┘ │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                              │                                      │
│                              ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              Unified Capability Registry (Go)                │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐   │   │
│  │  │  Tools   │ │ Channels │ │Providers │ │    Hooks     │   │   │
│  │  │ Registry │ │ Registry │ │ Registry │ │   Registry   │   │   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────────┘   │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐   │   │
│  │  │ Services │ │ Commands │ │ Gateway  │ │   Speech     │   │   │
│  │  │ Registry │ │ Registry │ │ Methods  │ │  Providers   │   │   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────────┘   │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐   │   │
│  │  │ Web Srch │ │ Image    │ │  Memory  │ │ Transcribe   │   │   │
│  │  │ Providers│ │   Gen    │ │ Embedder │ │  Providers   │   │   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────────┘   │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                              │                                      │
│                              ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    Runtime Integration                       │   │
│  │  ┌──────────────────────────────────────────────────────┐   │   │
│  │  │  Agent Runtime                                        │   │   │
│  │  │  - Tool execution with hook invocation                │   │   │
│  │  │  - Provider selection via registry                    │   │   │
│  │  │  - Session lifecycle hooks                            │   │   │
│  │  └──────────────────────────────────────────────────────┘   │   │
│  │  ┌──────────────────────────────────────────────────────┐   │   │
│  │  │  Channel Runtime                                      │   │   │
│  │  │  - Hybrid Go + Node.js channel support                │   │   │
│  │  │  - Inbound message routing with hooks                 │   │   │
│  │  └──────────────────────────────────────────────────────┘   │   │
│  │  ┌──────────────────────────────────────────────────────┐   │   │
│  │  │  Gateway Runtime                                      │   │   │
│  │  │  - Plugin-registered RPC methods                      │   │   │
│  │  │  - Gateway lifecycle hooks                            │   │   │
│  │  └──────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 Key Design Decisions

#### Decision 1: Node.js as Primary Plugin Runtime

**Rationale**: OpenClaw plugins are written in TypeScript and depend on Node.js APIs. Goja cannot run them without significant transpilation and polyfilling.

**Approach**: Extend the existing `node_host.go` to support the full OpenClaw plugin lifecycle.

#### Decision 2: Registration Capture Model

**Rationale**: OpenClaw plugins use imperative `api.registerX()` calls. We need to capture these registrations and translate them to Swarmstr's registry format.

**Approach**: Implement a mock `OpenClawPluginApi` in the Node.js shim that:
1. Captures all registration calls
2. Stores handler references for later invocation
3. Sends registration metadata to Go over JSON-RPC

#### Decision 3: Hybrid Channel Support

**Rationale**: Swarmstr has 18 Go-native channel implementations that work well. We don't want to abandon them.

**Approach**: Support both:
- Go-native channels (existing `internal/extensions/`)
- OpenClaw Node.js channels (via plugin system)

The channel registry will unify both sources.

#### Decision 4: Hook Invocation via RPC

**Rationale**: Hook handlers are JavaScript functions that must run in Node.js.

**Approach**: When Go needs to invoke a hook:
1. Go sends JSON-RPC request to Node.js with hook event data
2. Node.js executes registered handlers
3. Node.js returns aggregated results to Go

---

## Part 3: Implementation Phases

### Phase 1: Node.js Plugin Host Foundation
**Duration**: 2 weeks  
**Priority**: Critical  
**Dependencies**: None

#### 1.1 Objectives

- Replace simple Node shim with full OpenClaw SDK runtime
- Implement bidirectional JSON-RPC communication
- Support plugin lifecycle (load, init, invoke, shutdown)
- Implement registration capture mechanism

#### 1.2 Files to Create

```
internal/plugins/runtime/
├── openclaw_host.go          # Main Go host controller
├── openclaw_protocol.go      # JSON-RPC protocol types
├── openclaw_shim.js          # OpenClaw SDK shim (replaces node_shim.js)
├── openclaw_api.js           # OpenClawPluginApi implementation
└── openclaw_host_test.go     # Integration tests
```

#### 1.3 Implementation Details

**openclaw_host.go**:
```go
package runtime

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "os/exec"
    "sync"
    "sync/atomic"
)

// OpenClawPluginHost manages a Node.js subprocess running OpenClaw plugins.
type OpenClawPluginHost struct {
    proc     *exec.Cmd
    stdin    io.WriteCloser
    stdout   io.Reader
    
    mu       sync.Mutex
    pending  map[int64]chan *RPCResponse
    nextID   atomic.Int64
    closed   bool
    
    // Captured registrations from plugins
    tools        map[string]*RegisteredTool
    providers    map[string]*RegisteredProvider
    channels     map[string]*RegisteredChannel
    hooks        map[string][]*RegisteredHook
    services     map[string]*RegisteredService
    commands     map[string]*RegisteredCommand
    // ... other capability maps
}

// NewOpenClawPluginHost starts the Node.js subprocess with the OpenClaw shim.
func NewOpenClawPluginHost(ctx context.Context) (*OpenClawPluginHost, error) {
    shimPath := resolveShimPath()
    cmd := exec.CommandContext(ctx, "node", shimPath)
    
    stdin, err := cmd.StdinPipe()
    if err != nil {
        return nil, fmt.Errorf("stdin pipe: %w", err)
    }
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return nil, fmt.Errorf("stdout pipe: %w", err)
    }
    cmd.Stderr = os.Stderr // Log errors to console
    
    if err := cmd.Start(); err != nil {
        return nil, fmt.Errorf("start node: %w", err)
    }
    
    host := &OpenClawPluginHost{
        proc:      cmd,
        stdin:     stdin,
        stdout:    stdout,
        pending:   make(map[int64]chan *RPCResponse),
        tools:     make(map[string]*RegisteredTool),
        providers: make(map[string]*RegisteredProvider),
        // ... initialize other maps
    }
    
    go host.readLoop()
    return host, nil
}

// LoadPlugin loads an OpenClaw plugin from the given path.
func (h *OpenClawPluginHost) LoadPlugin(ctx context.Context, pluginPath string) error {
    resp, err := h.call(ctx, "load_plugin", map[string]any{
        "plugin_path": pluginPath,
    })
    if err != nil {
        return err
    }
    
    // Process captured registrations
    if regs, ok := resp.Result.(map[string]any)["registrations"]; ok {
        h.processRegistrations(regs.([]any))
    }
    return nil
}

// InvokeHook calls registered hook handlers for the given event.
func (h *OpenClawPluginHost) InvokeHook(ctx context.Context, event string, payload any) ([]HookResult, error) {
    resp, err := h.call(ctx, "invoke_hook", map[string]any{
        "event":   event,
        "payload": payload,
    })
    if err != nil {
        return nil, err
    }
    // Parse and return hook results
    return parseHookResults(resp.Result)
}

// InvokeTool executes a plugin-registered tool.
func (h *OpenClawPluginHost) InvokeTool(ctx context.Context, pluginID, toolName string, args map[string]any) (any, error) {
    resp, err := h.call(ctx, "invoke_tool", map[string]any{
        "plugin_id": pluginID,
        "tool":      toolName,
        "args":      args,
    })
    if err != nil {
        return nil, err
    }
    return resp.Result, nil
}

// InvokeProvider calls a provider method (chat, catalog, etc.)
func (h *OpenClawPluginHost) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
    resp, err := h.call(ctx, "invoke_provider", map[string]any{
        "provider_id": providerID,
        "method":      method,
        "params":      params,
    })
    if err != nil {
        return nil, err
    }
    return resp.Result, nil
}

func (h *OpenClawPluginHost) call(ctx context.Context, method string, params any) (*RPCResponse, error) {
    id := h.nextID.Add(1)
    respCh := make(chan *RPCResponse, 1)
    
    h.mu.Lock()
    if h.closed {
        h.mu.Unlock()
        return nil, fmt.Errorf("host closed")
    }
    h.pending[id] = respCh
    h.mu.Unlock()
    
    defer func() {
        h.mu.Lock()
        delete(h.pending, id)
        h.mu.Unlock()
    }()
    
    req := RPCRequest{ID: id, Method: method, Params: params}
    if err := h.send(req); err != nil {
        return nil, err
    }
    
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case resp := <-respCh:
        if resp.Error != "" {
            return nil, fmt.Errorf("rpc error: %s", resp.Error)
        }
        return resp, nil
    }
}

func (h *OpenClawPluginHost) readLoop() {
    scanner := bufio.NewScanner(h.stdout)
    for scanner.Scan() {
        var resp RPCResponse
        if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
            continue
        }
        
        h.mu.Lock()
        if ch, ok := h.pending[resp.ID]; ok {
            ch <- &resp
        }
        h.mu.Unlock()
    }
}

func (h *OpenClawPluginHost) send(req RPCRequest) error {
    data, err := json.Marshal(req)
    if err != nil {
        return err
    }
    _, err = h.stdin.Write(append(data, '\n'))
    return err
}

func (h *OpenClawPluginHost) Close() error {
    h.mu.Lock()
    h.closed = true
    h.mu.Unlock()
    
    h.call(context.Background(), "shutdown", nil)
    return h.proc.Wait()
}
```

**openclaw_shim.js** (core structure):
```javascript
/**
 * OpenClaw SDK Runtime Shim for Swarmstr
 * 
 * This shim provides a complete OpenClawPluginApi implementation,
 * captures plugin registrations, and bridges to the Go host.
 */
'use strict';

const readline = require('readline');
const path = require('path');

// ─── State Management ─────────────────────────────────────────────────────────

const loadedPlugins = new Map();      // pluginId -> plugin module
const registeredTools = new Map();    // qualifiedName -> { pluginId, tool, execute }
const registeredProviders = new Map(); // providerId -> { pluginId, provider }
const registeredHooks = new Map();    // event -> [{ pluginId, handler, opts }]
const registeredChannels = new Map();
const registeredServices = new Map();
const registeredCommands = new Map();
// ... other registries

// ─── OpenClawPluginApi Implementation ─────────────────────────────────────────

function createPluginApi(pluginId, pluginConfig, runtimeConfig) {
    const registrations = [];
    
    const api = {
        id: pluginId,
        name: pluginConfig.name || pluginId,
        version: pluginConfig.version,
        description: pluginConfig.description,
        source: pluginConfig.source || 'swarmstr',
        registrationMode: 'full',
        config: runtimeConfig,
        pluginConfig: pluginConfig.config || {},
        
        runtime: createRuntimeProxy(pluginId),
        logger: createLogger(pluginId),
        
        // ─── Core Registrations ───────────────────────────────────────────
        
        registerTool(tool, opts = {}) {
            const toolDef = typeof tool === 'function' ? tool(api) : tool;
            const qualifiedName = `${pluginId}/${toolDef.name}`;
            
            registeredTools.set(qualifiedName, {
                pluginId,
                tool: toolDef,
                execute: toolDef.execute,
                opts,
            });
            
            registrations.push({
                type: 'tool',
                name: toolDef.name,
                qualifiedName,
                description: toolDef.description,
                parameters: extractJsonSchema(toolDef.parameters),
                ownerOnly: toolDef.ownerOnly || opts.ownerOnly,
            });
        },
        
        registerProvider(provider) {
            registeredProviders.set(provider.id, { pluginId, provider });
            
            registrations.push({
                type: 'provider',
                id: provider.id,
                label: provider.label,
                docsPath: provider.docsPath,
                hasAuth: Array.isArray(provider.auth) && provider.auth.length > 0,
                hasCatalog: typeof provider.catalog?.run === 'function',
            });
        },
        
        registerChannel(registration) {
            const plugin = registration.plugin || registration;
            registeredChannels.set(plugin.ID(), { pluginId, plugin });
            
            registrations.push({
                type: 'channel',
                id: plugin.ID(),
                channelType: plugin.Type(),
                configSchema: plugin.ConfigSchema(),
            });
        },
        
        registerHook(events, handler, opts = {}) {
            const eventList = Array.isArray(events) ? events : [events];
            const hookId = `${pluginId}:${Date.now()}:${Math.random()}`;
            
            for (const event of eventList) {
                if (!registeredHooks.has(event)) {
                    registeredHooks.set(event, []);
                }
                registeredHooks.get(event).push({ pluginId, hookId, handler, opts });
            }
            
            registrations.push({
                type: 'hook',
                hookId,
                events: eventList,
                priority: opts.priority,
            });
        },
        
        registerService(service) {
            registeredServices.set(service.id, { pluginId, service });
            
            registrations.push({
                type: 'service',
                id: service.id,
            });
        },
        
        registerCommand(command) {
            registeredCommands.set(command.name, { pluginId, command });
            
            registrations.push({
                type: 'command',
                name: command.name,
                description: command.description,
                acceptsArgs: command.acceptsArgs,
            });
        },
        
        registerGatewayMethod(method, handler, opts = {}) {
            registrations.push({
                type: 'gateway_method',
                method,
                scope: opts.scope || 'operator.agent',
            });
            // Store handler for invocation
        },
        
        // ─── Provider Registrations ───────────────────────────────────────
        
        registerSpeechProvider(provider) {
            registrations.push({ type: 'speech_provider', id: provider.id });
        },
        
        registerRealtimeTranscriptionProvider(provider) {
            registrations.push({ type: 'transcription_provider', id: provider.id });
        },
        
        registerRealtimeVoiceProvider(provider) {
            registrations.push({ type: 'voice_provider', id: provider.id });
        },
        
        registerMediaUnderstandingProvider(provider) {
            registrations.push({ type: 'media_understanding_provider', id: provider.id });
        },
        
        registerImageGenerationProvider(provider) {
            registrations.push({ type: 'image_gen_provider', id: provider.id });
        },
        
        registerVideoGenerationProvider(provider) {
            registrations.push({ type: 'video_gen_provider', id: provider.id });
        },
        
        registerMusicGenerationProvider(provider) {
            registrations.push({ type: 'music_gen_provider', id: provider.id });
        },
        
        registerWebFetchProvider(provider) {
            registrations.push({ type: 'web_fetch_provider', id: provider.id });
        },
        
        registerWebSearchProvider(provider) {
            registrations.push({ type: 'web_search_provider', id: provider.id });
        },
        
        registerMemoryEmbeddingProvider(provider) {
            registrations.push({ type: 'memory_embedding_provider', id: provider.id });
        },
        
        // ─── Setup & Migration Registrations ──────────────────────────────
        
        registerConfigMigration(migrate) {
            registrations.push({ type: 'config_migration', pluginId });
        },
        
        registerMigrationProvider(provider) {
            registrations.push({ type: 'migration_provider', id: provider.id });
        },
        
        registerAutoEnableProbe(probe) {
            registrations.push({ type: 'auto_enable_probe', pluginId });
        },
        
        // ─── Additional Registrations ─────────────────────────────────────
        
        registerCli(registrar, opts = {}) {
            registrations.push({ type: 'cli', commands: opts.commands || [] });
        },
        
        registerCliBackend(backend) {
            registrations.push({ type: 'cli_backend', id: backend.id });
        },
        
        registerHttpRoute(params) {
            registrations.push({ type: 'http_route', path: params.path });
        },
        
        registerReload(registration) {
            registrations.push({ type: 'reload', pluginId });
        },
        
        registerNodeHostCommand(command) {
            registrations.push({ type: 'node_host_command', command: command.command });
        },
        
        registerNodeInvokePolicy(policy) {
            registrations.push({ type: 'node_invoke_policy', commands: policy.commands });
        },
        
        registerSecurityAuditCollector(collector) {
            registrations.push({ type: 'security_audit_collector', pluginId });
        },
        
        registerGatewayDiscoveryService(service) {
            registrations.push({ type: 'gateway_discovery', id: service.id });
        },
        
        registerTextTransforms(transforms) {
            registrations.push({ type: 'text_transforms', pluginId });
        },
        
        registerInteractiveHandler(registration) {
            registrations.push({ type: 'interactive_handler', pluginId });
        },
        
        onConversationBindingResolved(handler) {
            registrations.push({ type: 'conversation_binding_listener', pluginId });
        },
        
        on(event, handler) {
            // Alias for registerHook
            api.registerHook(event, handler);
        },
    };
    
    return { api, getRegistrations: () => registrations };
}

// ─── Runtime Proxy ────────────────────────────────────────────────────────────

function createRuntimeProxy(pluginId) {
    return {
        // Swarmstr-specific: Nostr access
        nostr: {
            publish: (event) => callHost('nostr_publish', { event }),
            fetch: (filter, limit) => callHost('nostr_fetch', { filter, limit }),
            encrypt: (pubkey, content) => callHost('nostr_encrypt', { pubkey, content }),
            decrypt: (pubkey, ciphertext) => callHost('nostr_decrypt', { pubkey, ciphertext }),
        },
        
        // Config access
        config: {
            get: (key) => callHostSync('config_get', { key }),
        },
        
        // HTTP client
        fetch: (url, opts) => callHost('http_fetch', { url, opts }),
        
        // Storage
        storage: {
            get: (key) => callHost('storage_get', { pluginId, key }),
            set: (key, value) => callHost('storage_set', { pluginId, key, value }),
            del: (key) => callHost('storage_del', { pluginId, key }),
        },
        
        // Agent completion
        agent: {
            complete: (prompt, opts) => callHost('agent_complete', { prompt, opts }),
        },
        
        // Session management
        sessions: {
            get: (key) => callHost('session_get', { key }),
            set: (key, value) => callHost('session_set', { key, value }),
        },
        
        // Events
        events: {
            emit: (event, payload) => callHost('event_emit', { event, payload }),
        },
    };
}

// ─── Request Handlers ─────────────────────────────────────────────────────────

async function handleRequest(req) {
    const { id, method, params } = req;
    
    try {
        switch (method) {
            case 'load_plugin':
                return await handleLoadPlugin(params);
                
            case 'invoke_tool':
                return await handleInvokeTool(params);
                
            case 'invoke_hook':
                return await handleInvokeHook(params);
                
            case 'invoke_provider':
                return await handleInvokeProvider(params);
                
            case 'start_service':
                return await handleStartService(params);
                
            case 'stop_service':
                return await handleStopService(params);
                
            case 'shutdown':
                process.exit(0);
                
            default:
                throw new Error(`unknown method: ${method}`);
        }
    } catch (err) {
        return { error: err.message };
    }
}

async function handleLoadPlugin({ plugin_path, config }) {
    // Load the plugin module
    const mod = require(plugin_path);
    const entry = mod.default || mod;
    
    // Extract plugin metadata
    const pluginId = entry.id || path.basename(plugin_path, path.extname(plugin_path));
    
    // Create API and capture registrations
    const { api, getRegistrations } = createPluginApi(pluginId, entry, config || {});
    
    // Call register function
    if (typeof entry.register === 'function') {
        entry.register(api);
    } else if (typeof entry === 'function') {
        entry(api);
    }
    
    loadedPlugins.set(pluginId, { entry, api });
    
    return {
        plugin_id: pluginId,
        name: entry.name || pluginId,
        version: entry.version,
        registrations: getRegistrations(),
    };
}

async function handleInvokeTool({ plugin_id, tool, args }) {
    const qualifiedName = `${plugin_id}/${tool}`;
    const registration = registeredTools.get(qualifiedName);
    
    if (!registration) {
        throw new Error(`tool not found: ${qualifiedName}`);
    }
    
    const result = await registration.execute(
        `call-${Date.now()}`,  // toolCallId
        args,
        undefined,             // signal
        undefined,             // onUpdate
    );
    
    return { result };
}

async function handleInvokeHook({ event, payload }) {
    const handlers = registeredHooks.get(event) || [];
    const results = [];
    
    // Sort by priority (lower = earlier)
    const sorted = [...handlers].sort((a, b) => 
        (a.opts.priority || 100) - (b.opts.priority || 100)
    );
    
    for (const { pluginId, handler, opts } of sorted) {
        try {
            const result = await handler(payload);
            results.push({ pluginId, result, ok: true });
        } catch (err) {
            results.push({ pluginId, error: err.message, ok: false });
            if (opts.stopOnError) break;
        }
    }
    
    return { results };
}

async function handleInvokeProvider({ provider_id, method, params }) {
    const registration = registeredProviders.get(provider_id);
    
    if (!registration) {
        throw new Error(`provider not found: ${provider_id}`);
    }
    
    const { provider } = registration;
    
    switch (method) {
        case 'catalog':
            return await provider.catalog.run(params);
            
        case 'auth':
            const authMethod = provider.auth.find(a => a.id === params.auth_id);
            if (!authMethod) throw new Error(`auth method not found: ${params.auth_id}`);
            return await authMethod.run(params.ctx);
            
        // Add other provider methods as needed
            
        default:
            throw new Error(`unknown provider method: ${method}`);
    }
}

// ─── Main Loop ────────────────────────────────────────────────────────────────

const rl = readline.createInterface({ input: process.stdin, terminal: false });

rl.on('line', async (line) => {
    line = line.trim();
    if (!line) return;
    
    let req;
    try {
        req = JSON.parse(line);
    } catch (e) {
        process.stderr.write(`parse error: ${e.message}\n`);
        return;
    }
    
    const result = await handleRequest(req);
    const response = { id: req.id, ...result };
    process.stdout.write(JSON.stringify(response) + '\n');
});

rl.on('close', () => process.exit(0));
```

#### 1.4 Acceptance Criteria

- [ ] Node.js subprocess starts and maintains stable connection
- [ ] Plugin loading works with OpenClaw-format plugins
- [ ] Registration capture produces complete capability metadata
- [ ] JSON-RPC communication handles concurrent requests
- [ ] Graceful shutdown with resource cleanup
- [ ] Error handling propagates meaningful messages

#### 1.5 Test Cases

1. Load a simple OpenClaw tool plugin
2. Load a plugin with multiple tool registrations
3. Invoke a tool and verify result
4. Handle plugin load errors gracefully
5. Concurrent plugin operations
6. Subprocess crash recovery

---

### Phase 2: Unified Capability Registry
**Duration**: 2 weeks  
**Priority**: Critical  
**Dependencies**: Phase 1

#### 2.1 Objectives

- Extend existing registry to support all OpenClaw capability types
- Unify Go-native and Node.js plugin registrations
- Provide efficient lookup for all capability types
- Support capability metadata queries

#### 2.2 Files to Modify/Create

```
internal/plugins/
├── registry/
│   ├── unified.go            # Unified registry combining all sources
│   ├── tools.go              # Tool-specific registry logic
│   ├── providers.go          # Provider registry
│   ├── channels.go           # Channel registry (Go + Node.js)
│   ├── hooks.go              # Hook registry
│   ├── services.go           # Service registry
│   ├── capabilities.go       # Capability metadata types
│   └── unified_test.go       # Comprehensive tests
└── manifest/
    └── registry.go           # (existing - extend)
```

#### 2.3 Implementation Details

**registry/unified.go**:
```go
package registry

import (
    "context"
    "sync"
)

// UnifiedRegistry combines all capability registries.
type UnifiedRegistry struct {
    mu sync.RWMutex
    
    // Core capabilities
    tools     *ToolRegistry
    providers *ProviderRegistry
    channels  *ChannelRegistry
    hooks     *HookRegistry
    services  *ServiceRegistry
    commands  *CommandRegistry
    
    // Gateway
    gatewayMethods *GatewayMethodRegistry
    
    // Media/AI capabilities
    speechProviders        *SpeechProviderRegistry
    transcriptionProviders *TranscriptionProviderRegistry
    imageGenProviders      *ImageGenProviderRegistry
    videoGenProviders      *VideoGenProviderRegistry
    musicGenProviders      *MusicGenProviderRegistry
    webSearchProviders     *WebSearchProviderRegistry
    webFetchProviders      *WebFetchProviderRegistry
    memoryEmbedProviders   *MemoryEmbedProviderRegistry
    
    // Plugin tracking
    plugins map[string]*PluginRecord
}

// PluginRecord tracks a loaded plugin and its contributions.
type PluginRecord struct {
    ID           string
    Name         string
    Version      string
    Source       PluginSource // "native" | "openclaw" | "goja"
    LoadedAt     time.Time
    Capabilities []CapabilityRef
}

// CapabilityRef references a registered capability.
type CapabilityRef struct {
    Type string // "tool" | "provider" | "channel" | etc.
    ID   string // Qualified identifier
}

func NewUnifiedRegistry() *UnifiedRegistry {
    return &UnifiedRegistry{
        tools:                  NewToolRegistry(),
        providers:              NewProviderRegistry(),
        channels:               NewChannelRegistry(),
        hooks:                  NewHookRegistry(),
        services:               NewServiceRegistry(),
        commands:               NewCommandRegistry(),
        gatewayMethods:         NewGatewayMethodRegistry(),
        speechProviders:        NewSpeechProviderRegistry(),
        transcriptionProviders: NewTranscriptionProviderRegistry(),
        imageGenProviders:      NewImageGenProviderRegistry(),
        videoGenProviders:      NewVideoGenProviderRegistry(),
        musicGenProviders:      NewMusicGenProviderRegistry(),
        webSearchProviders:     NewWebSearchProviderRegistry(),
        webFetchProviders:      NewWebFetchProviderRegistry(),
        memoryEmbedProviders:   NewMemoryEmbedProviderRegistry(),
        plugins:                make(map[string]*PluginRecord),
    }
}

// RegisterFromOpenClawPlugin processes captured registrations from a Node.js plugin.
func (r *UnifiedRegistry) RegisterFromOpenClawPlugin(pluginID string, registrations []Registration) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    record := &PluginRecord{
        ID:       pluginID,
        Source:   PluginSourceOpenClaw,
        LoadedAt: time.Now(),
    }
    
    for _, reg := range registrations {
        capRef, err := r.processRegistration(pluginID, reg)
        if err != nil {
            return fmt.Errorf("registration %s: %w", reg.Type, err)
        }
        record.Capabilities = append(record.Capabilities, capRef)
    }
    
    r.plugins[pluginID] = record
    return nil
}

func (r *UnifiedRegistry) processRegistration(pluginID string, reg Registration) (CapabilityRef, error) {
    switch reg.Type {
    case "tool":
        return r.tools.Register(pluginID, reg.ToolData)
    case "provider":
        return r.providers.Register(pluginID, reg.ProviderData)
    case "channel":
        return r.channels.Register(pluginID, reg.ChannelData)
    case "hook":
        return r.hooks.Register(pluginID, reg.HookData)
    case "service":
        return r.services.Register(pluginID, reg.ServiceData)
    // ... handle all capability types
    default:
        return CapabilityRef{}, fmt.Errorf("unknown registration type: %s", reg.Type)
    }
}

// UnregisterPlugin removes all capabilities from a plugin.
func (r *UnifiedRegistry) UnregisterPlugin(pluginID string) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    record, ok := r.plugins[pluginID]
    if !ok {
        return fmt.Errorf("plugin not found: %s", pluginID)
    }
    
    for _, capRef := range record.Capabilities {
        switch capRef.Type {
        case "tool":
            r.tools.Unregister(capRef.ID)
        case "provider":
            r.providers.Unregister(capRef.ID)
        // ... handle all types
        }
    }
    
    delete(r.plugins, pluginID)
    return nil
}

// Tools returns the tool registry.
func (r *UnifiedRegistry) Tools() *ToolRegistry { return r.tools }

// Providers returns the provider registry.
func (r *UnifiedRegistry) Providers() *ProviderRegistry { return r.providers }

// Hooks returns the hook registry.
func (r *UnifiedRegistry) Hooks() *HookRegistry { return r.hooks }

// ... other accessors
```

**registry/hooks.go**:
```go
package registry

import (
    "sort"
    "sync"
)

// HookEvent represents a hook event type.
type HookEvent string

// All supported hook events (matching OpenClaw's PLUGIN_HOOK_NAMES)
const (
    HookBeforeAgentStart      HookEvent = "before_agent_start"
    HookBeforeAgentReply      HookEvent = "before_agent_reply"
    HookBeforePromptBuild     HookEvent = "before_prompt_build"
    HookBeforeModelResolve    HookEvent = "before_model_resolve"
    HookLLMInput              HookEvent = "llm_input"
    HookLLMOutput             HookEvent = "llm_output"
    HookModelCallStarted      HookEvent = "model_call_started"
    HookModelCallEnded        HookEvent = "model_call_ended"
    HookAgentEnd              HookEvent = "agent_end"
    HookBeforeAgentFinalize   HookEvent = "before_agent_finalize"
    HookBeforeCompaction      HookEvent = "before_compaction"
    HookAfterCompaction       HookEvent = "after_compaction"
    HookBeforeReset           HookEvent = "before_reset"
    HookBeforeToolCall        HookEvent = "before_tool_call"
    HookAfterToolCall         HookEvent = "after_tool_call"
    HookToolResultPersist     HookEvent = "tool_result_persist"
    HookBeforeMessageWrite    HookEvent = "before_message_write"
    HookInboundClaim          HookEvent = "inbound_claim"
    HookMessageReceived       HookEvent = "message_received"
    HookMessageSending        HookEvent = "message_sending"
    HookMessageSent           HookEvent = "message_sent"
    HookBeforeDispatch        HookEvent = "before_dispatch"
    HookReplyDispatch         HookEvent = "reply_dispatch"
    HookSessionStart          HookEvent = "session_start"
    HookSessionEnd            HookEvent = "session_end"
    HookSubagentSpawning      HookEvent = "subagent_spawning"
    HookSubagentSpawned       HookEvent = "subagent_spawned"
    HookSubagentEnded         HookEvent = "subagent_ended"
    HookSubagentDeliveryTarget HookEvent = "subagent_delivery_target"
    HookGatewayStart          HookEvent = "gateway_start"
    HookGatewayStop           HookEvent = "gateway_stop"
    HookCronChanged           HookEvent = "cron_changed"
    HookBeforeInstall         HookEvent = "before_install"
    HookAgentTurnPrepare      HookEvent = "agent_turn_prepare"
    HookHeartbeatPrompt       HookEvent = "heartbeat_prompt_contribution"
)

// RegisteredHook represents a hook registration.
type RegisteredHook struct {
    ID       string
    PluginID string
    Events   []HookEvent
    Priority int  // Lower = earlier execution
    Source   HookSource // "node" | "native"
}

type HookSource string

const (
    HookSourceNode   HookSource = "node"
    HookSourceNative HookSource = "native"
)

// HookRegistry manages hook registrations.
type HookRegistry struct {
    mu    sync.RWMutex
    hooks map[HookEvent][]*RegisteredHook
    byID  map[string]*RegisteredHook
}

func NewHookRegistry() *HookRegistry {
    return &HookRegistry{
        hooks: make(map[HookEvent][]*RegisteredHook),
        byID:  make(map[string]*RegisteredHook),
    }
}

// Register adds a hook registration.
func (r *HookRegistry) Register(pluginID string, data HookRegistrationData) (CapabilityRef, error) {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    hook := &RegisteredHook{
        ID:       data.HookID,
        PluginID: pluginID,
        Events:   data.Events,
        Priority: data.Priority,
        Source:   HookSourceNode,
    }
    
    for _, event := range data.Events {
        r.hooks[event] = append(r.hooks[event], hook)
        // Keep sorted by priority
        sort.Slice(r.hooks[event], func(i, j int) bool {
            return r.hooks[event][i].Priority < r.hooks[event][j].Priority
        })
    }
    
    r.byID[data.HookID] = hook
    
    return CapabilityRef{Type: "hook", ID: data.HookID}, nil
}

// HandlersFor returns all handlers for an event, sorted by priority.
func (r *HookRegistry) HandlersFor(event HookEvent) []*RegisteredHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    handlers := r.hooks[event]
    result := make([]*RegisteredHook, len(handlers))
    copy(result, handlers)
    return result
}

// Unregister removes a hook by ID.
func (r *HookRegistry) Unregister(hookID string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    hook, ok := r.byID[hookID]
    if !ok {
        return
    }
    
    for _, event := range hook.Events {
        handlers := r.hooks[event]
        for i, h := range handlers {
            if h.ID == hookID {
                r.hooks[event] = append(handlers[:i], handlers[i+1:]...)
                break
            }
        }
    }
    
    delete(r.byID, hookID)
}
```

#### 2.4 Acceptance Criteria

- [ ] All 15+ capability types have dedicated registries
- [ ] Unified registry aggregates all sources
- [ ] Go-native channels integrate with registry
- [ ] Plugin unregistration cleans up all capabilities
- [ ] Thread-safe concurrent access
- [ ] Efficient lookup by ID and by type

---

### Phase 3: Hook System Implementation
**Duration**: 2 weeks  
**Priority**: Critical  
**Dependencies**: Phase 1, Phase 2

#### 3.1 Objectives

- Implement hook invocation at all agent lifecycle points
- Support synchronous and asynchronous hooks
- Handle hook results (mutations, rejections, etc.)
- Integrate hooks into existing agent/channel runtimes

#### 3.2 Files to Create/Modify

```
internal/hooks/
├── emitter.go           # Hook event emitter
├── invoker.go           # Hook invocation logic
├── results.go           # Result aggregation
├── integration.go       # Runtime integration points
└── emitter_test.go

internal/agent/
├── agentic_loop.go      # (modify - add hook points)
├── turn.go              # (modify - add hook points)
└── hooks_integration.go # Agent-specific hook wiring

internal/autoreply/
└── hooks_integration.go # Reply dispatch hook wiring
```

#### 3.3 Implementation Details

**hooks/emitter.go**:
```go
package hooks

import (
    "context"
    "fmt"
    "sync"
    
    "metiq/internal/plugins/registry"
    "metiq/internal/plugins/runtime"
)

// Emitter dispatches hook events to registered handlers.
type Emitter struct {
    registry *registry.HookRegistry
    host     *runtime.OpenClawPluginHost
    
    // Native hook handlers (Go functions)
    nativeHandlers map[registry.HookEvent][]NativeHandler
    nativeMu       sync.RWMutex
}

// NativeHandler is a Go-native hook handler.
type NativeHandler func(ctx context.Context, event any) (any, error)

// EmitOptions configures hook emission behavior.
type EmitOptions struct {
    // StopOnMutation stops after first handler that returns a mutation
    StopOnMutation bool
    
    // StopOnReject stops after first handler that rejects
    StopOnReject bool
    
    // Timeout for the entire emission chain
    Timeout time.Duration
}

// EmitResult contains aggregated hook results.
type EmitResult struct {
    // Results from each handler
    Results []HandlerResult
    
    // Aggregated mutations (if any)
    Mutations []any
    
    // Whether any handler rejected
    Rejected bool
    RejectReason string
    
    // First error encountered
    Error error
}

type HandlerResult struct {
    PluginID string
    HookID   string
    Result   any
    Error    error
    Duration time.Duration
}

func NewEmitter(reg *registry.HookRegistry, host *runtime.OpenClawPluginHost) *Emitter {
    return &Emitter{
        registry:       reg,
        host:           host,
        nativeHandlers: make(map[registry.HookEvent][]NativeHandler),
    }
}

// RegisterNative registers a Go-native hook handler.
func (e *Emitter) RegisterNative(event registry.HookEvent, handler NativeHandler) {
    e.nativeMu.Lock()
    defer e.nativeMu.Unlock()
    e.nativeHandlers[event] = append(e.nativeHandlers[event], handler)
}

// Emit dispatches an event to all registered handlers.
func (e *Emitter) Emit(ctx context.Context, event registry.HookEvent, payload any, opts EmitOptions) (*EmitResult, error) {
    if opts.Timeout > 0 {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
        defer cancel()
    }
    
    result := &EmitResult{}
    
    // Get all handlers sorted by priority
    handlers := e.registry.HandlersFor(event)
    
    // Also get native handlers
    e.nativeMu.RLock()
    nativeHandlers := e.nativeHandlers[event]
    e.nativeMu.RUnlock()
    
    // Execute Node.js handlers via RPC
    for _, h := range handlers {
        if h.Source == registry.HookSourceNode {
            start := time.Now()
            
            hookResult, err := e.host.InvokeHook(ctx, string(event), map[string]any{
                "hook_id": h.ID,
                "payload": payload,
            })
            
            hr := HandlerResult{
                PluginID: h.PluginID,
                HookID:   h.ID,
                Duration: time.Since(start),
            }
            
            if err != nil {
                hr.Error = err
                result.Error = err
            } else {
                hr.Result = hookResult
                
                // Check for mutations
                if mutation := extractMutation(hookResult); mutation != nil {
                    result.Mutations = append(result.Mutations, mutation)
                    if opts.StopOnMutation {
                        result.Results = append(result.Results, hr)
                        return result, nil
                    }
                }
                
                // Check for rejection
                if isRejection(hookResult) {
                    result.Rejected = true
                    result.RejectReason = extractRejectReason(hookResult)
                    if opts.StopOnReject {
                        result.Results = append(result.Results, hr)
                        return result, nil
                    }
                }
            }
            
            result.Results = append(result.Results, hr)
        }
    }
    
    // Execute native handlers
    for _, handler := range nativeHandlers {
        start := time.Now()
        
        handlerResult, err := handler(ctx, payload)
        
        hr := HandlerResult{
            PluginID: "native",
            Duration: time.Since(start),
            Result:   handlerResult,
            Error:    err,
        }
        
        if err != nil && result.Error == nil {
            result.Error = err
        }
        
        result.Results = append(result.Results, hr)
    }
    
    return result, nil
}

// EmitBeforeToolCall emits before_tool_call and handles approval resolution.
func (e *Emitter) EmitBeforeToolCall(ctx context.Context, event BeforeToolCallEvent) (*BeforeToolCallResult, error) {
    result, err := e.Emit(ctx, registry.HookBeforeToolCall, event, EmitOptions{
        StopOnReject: true,
        Timeout:      5 * time.Second,
    })
    if err != nil {
        return nil, err
    }
    
    btcResult := &BeforeToolCallResult{
        Approved: !result.Rejected,
    }
    
    if result.Rejected {
        btcResult.RejectionReason = result.RejectReason
    }
    
    // Aggregate argument mutations
    for _, mutation := range result.Mutations {
        if argMutation, ok := mutation.(map[string]any); ok {
            if args, ok := argMutation["args"].(map[string]any); ok {
                btcResult.MutatedArgs = mergeArgs(btcResult.MutatedArgs, args)
            }
        }
    }
    
    return btcResult, nil
}
```

**Agent integration (modify agentic_loop.go)**:
```go
// In internal/agent/agentic_loop.go

// Add hook emission points

func executeSingleToolCall(ctx context.Context, executor ToolExecutor, call ToolCall, 
    sessionID, turnID string, sink ToolLifecycleSink, trace TraceContext,
    hooks *hooks.Emitter) ToolExecResult {
    
    // ─── Before Tool Call Hook ────────────────────────────────────────────
    if hooks != nil {
        beforeResult, err := hooks.EmitBeforeToolCall(ctx, hooks.BeforeToolCallEvent{
            ToolName:  call.Name,
            Args:      call.Arguments,
            SessionID: sessionID,
            TurnID:    turnID,
        })
        if err != nil {
            log.Printf("before_tool_call hook error: %v", err)
        } else if !beforeResult.Approved {
            return ToolExecResult{
                ToolCallID:  call.ID,
                Content:     fmt.Sprintf("Tool call rejected: %s", beforeResult.RejectionReason),
                LoopBlocked: true,
            }
        } else if beforeResult.MutatedArgs != nil {
            call.Arguments = beforeResult.MutatedArgs
        }
    }
    
    // Execute the tool
    result, err := executor.Execute(ctx, call)
    
    // ─── After Tool Call Hook ─────────────────────────────────────────────
    if hooks != nil {
        _, hookErr := hooks.Emit(ctx, registry.HookAfterToolCall, hooks.AfterToolCallEvent{
            ToolName:  call.Name,
            Args:      call.Arguments,
            Result:    result,
            Error:     err,
            SessionID: sessionID,
            TurnID:    turnID,
        }, hooks.EmitOptions{})
        if hookErr != nil {
            log.Printf("after_tool_call hook error: %v", hookErr)
        }
    }
    
    // ... rest of execution
}
```

#### 3.4 Hook Event Payloads

Each hook event type needs a defined payload structure. Here are key examples:

```go
// hooks/events.go

// BeforeToolCallEvent is sent before a tool executes.
type BeforeToolCallEvent struct {
    ToolName    string         `json:"tool_name"`
    ToolCallID  string         `json:"tool_call_id"`
    Args        map[string]any `json:"args"`
    SessionID   string         `json:"session_id"`
    TurnID      string         `json:"turn_id"`
    AgentID     string         `json:"agent_id,omitempty"`
}

// BeforeToolCallResult is returned from before_tool_call hooks.
type BeforeToolCallResult struct {
    Approved        bool           `json:"approved"`
    RejectionReason string         `json:"rejection_reason,omitempty"`
    MutatedArgs     map[string]any `json:"mutated_args,omitempty"`
}

// AfterToolCallEvent is sent after a tool executes.
type AfterToolCallEvent struct {
    ToolName   string         `json:"tool_name"`
    ToolCallID string         `json:"tool_call_id"`
    Args       map[string]any `json:"args"`
    Result     string         `json:"result"`
    Error      string         `json:"error,omitempty"`
    Duration   time.Duration  `json:"duration_ms"`
    SessionID  string         `json:"session_id"`
    TurnID     string         `json:"turn_id"`
}

// InboundClaimEvent is sent when a message arrives.
type InboundClaimEvent struct {
    ChannelID string `json:"channel_id"`
    SenderID  string `json:"sender_id"`
    Text      string `json:"text"`
    EventID   string `json:"event_id,omitempty"`
    ThreadID  string `json:"thread_id,omitempty"`
}

// InboundClaimResult determines message handling.
type InboundClaimResult struct {
    Claimed     bool   `json:"claimed"`
    SkipAgent   bool   `json:"skip_agent"`
    ReplyText   string `json:"reply_text,omitempty"`
    ChannelID   string `json:"channel_id,omitempty"` // Override routing
}

// SessionStartEvent is sent when a session begins.
type SessionStartEvent struct {
    SessionID   string `json:"session_id"`
    ChannelID   string `json:"channel_id"`
    AgentID     string `json:"agent_id"`
    InitiatorID string `json:"initiator_id"`
}

// ... define all 35+ event types
```

#### 3.5 Acceptance Criteria

- [ ] All 35 hook events have defined payloads
- [ ] Hook invocation works for Node.js handlers
- [ ] Native Go handlers can be registered
- [ ] Priority ordering is respected
- [ ] Mutations are properly aggregated
- [ ] Rejections stop processing correctly
- [ ] Timeouts prevent runaway hooks
- [ ] Agent loop integrates all tool hooks
- [ ] Channel runtime integrates message hooks

---

### Phase 4: Provider Plugin Support
**Duration**: 3 weeks  
**Priority**: High  
**Dependencies**: Phase 1, Phase 2, Phase 3

#### 4.1 Objectives

- Enable OpenClaw provider plugins (Anthropic, OpenAI, Google, etc.)
- Bridge provider interface to Swarmstr's `ChatProvider`
- Support provider authentication flows
- Enable model catalog discovery

#### 4.2 Files to Create/Modify

```
internal/plugins/
├── provider/
│   ├── bridge.go          # Provider interface bridge
│   ├── catalog.go         # Model catalog aggregation
│   ├── auth.go            # Auth flow support
│   ├── transport.go       # Request/response translation
│   └── bridge_test.go

internal/agent/
├── provider.go            # (modify - integrate plugin providers)
├── provider_plugin.go     # Plugin provider implementation
└── provider_selection.go  # Provider selection logic
```

#### 4.3 Implementation Details

**plugins/provider/bridge.go**:
```go
package provider

import (
    "context"
    "fmt"
    
    "metiq/internal/agent"
    "metiq/internal/plugins/registry"
    "metiq/internal/plugins/runtime"
)

// PluginProviderBridge adapts an OpenClaw provider plugin to Swarmstr's ChatProvider.
type PluginProviderBridge struct {
    providerID string
    pluginID   string
    host       *runtime.OpenClawPluginHost
    metadata   *registry.ProviderMetadata
    
    // Cached model catalog
    catalogCache []ModelEntry
    catalogTime  time.Time
}

// Ensure interface compliance
var _ agent.ChatProvider = (*PluginProviderBridge)(nil)

// Chat implements agent.ChatProvider.
func (p *PluginProviderBridge) Chat(ctx context.Context, messages []agent.LLMMessage, 
    tools []agent.ToolDefinition, opts agent.ChatOptions) (*agent.LLMResponse, error) {
    
    // Translate messages to OpenClaw format
    ocMessages := translateMessagesToOpenClaw(messages)
    ocTools := translateToolsToOpenClaw(tools)
    
    // Call provider via Node.js host
    result, err := p.host.InvokeProvider(ctx, p.providerID, "chat", map[string]any{
        "messages": ocMessages,
        "tools":    ocTools,
        "options": map[string]any{
            "max_tokens":      opts.MaxTokens,
            "thinking_budget": opts.ThinkingBudget,
        },
    })
    if err != nil {
        return nil, fmt.Errorf("provider %s chat: %w", p.providerID, err)
    }
    
    // Translate response back
    return translateResponseFromOpenClaw(result)
}

// RefreshCatalog updates the model catalog from the provider.
func (p *PluginProviderBridge) RefreshCatalog(ctx context.Context) ([]ModelEntry, error) {
    result, err := p.host.InvokeProvider(ctx, p.providerID, "catalog", map[string]any{
        "config": p.getConfig(),
    })
    if err != nil {
        return nil, err
    }
    
    entries := parseCatalogResult(result)
    p.catalogCache = entries
    p.catalogTime = time.Now()
    
    return entries, nil
}

// translateMessagesToOpenClaw converts Swarmstr messages to OpenClaw format.
func translateMessagesToOpenClaw(messages []agent.LLMMessage) []map[string]any {
    result := make([]map[string]any, 0, len(messages))
    
    for _, msg := range messages {
        ocMsg := map[string]any{
            "role": msg.Role,
        }
        
        if len(msg.Content) > 0 {
            // Handle multi-part content
            parts := make([]map[string]any, 0, len(msg.Content))
            for _, part := range msg.Content {
                switch part.Type {
                case "text":
                    parts = append(parts, map[string]any{
                        "type": "text",
                        "text": part.Text,
                    })
                case "image":
                    parts = append(parts, map[string]any{
                        "type":       "image",
                        "source":     part.ImageSource,
                        "media_type": part.MediaType,
                    })
                case "tool_use":
                    parts = append(parts, map[string]any{
                        "type":  "tool_use",
                        "id":    part.ToolCallID,
                        "name":  part.ToolName,
                        "input": part.ToolInput,
                    })
                case "tool_result":
                    parts = append(parts, map[string]any{
                        "type":        "tool_result",
                        "tool_use_id": part.ToolCallID,
                        "content":     part.Text,
                    })
                }
            }
            ocMsg["content"] = parts
        } else {
            ocMsg["content"] = msg.Text
        }
        
        result = append(result, ocMsg)
    }
    
    return result
}

// translateResponseFromOpenClaw converts OpenClaw response to Swarmstr format.
func translateResponseFromOpenClaw(result any) (*agent.LLMResponse, error) {
    data, ok := result.(map[string]any)
    if !ok {
        return nil, fmt.Errorf("unexpected response type: %T", result)
    }
    
    resp := &agent.LLMResponse{}
    
    // Extract text content
    if content, ok := data["content"].(string); ok {
        resp.Content = content
    } else if content, ok := data["content"].([]any); ok {
        // Multi-part content
        for _, part := range content {
            partMap, ok := part.(map[string]any)
            if !ok {
                continue
            }
            switch partMap["type"] {
            case "text":
                resp.Content += partMap["text"].(string)
            case "tool_use":
                resp.ToolCalls = append(resp.ToolCalls, agent.ToolCall{
                    ID:        partMap["id"].(string),
                    Name:      partMap["name"].(string),
                    Arguments: partMap["input"].(map[string]any),
                })
            }
        }
    }
    
    // Extract usage
    if usage, ok := data["usage"].(map[string]any); ok {
        resp.Usage = agent.ProviderUsage{
            InputTokens:  int64(usage["input_tokens"].(float64)),
            OutputTokens: int64(usage["output_tokens"].(float64)),
        }
        if cached, ok := usage["cache_read_input_tokens"].(float64); ok {
            resp.Usage.CacheReadTokens = int64(cached)
        }
    }
    
    // Determine if model wants tool results
    if stopReason, ok := data["stop_reason"].(string); ok {
        resp.NeedsToolResults = stopReason == "tool_use" || stopReason == "tool_calls"
    }
    
    return resp, nil
}
```

**agent/provider_selection.go**:
```go
package agent

import (
    "context"
    "fmt"
    "strings"
    
    "metiq/internal/plugins/provider"
    "metiq/internal/plugins/registry"
)

// ProviderSelector chooses providers based on model ID and config.
type ProviderSelector struct {
    registry     *registry.ProviderRegistry
    pluginHost   *runtime.OpenClawPluginHost
    
    // Native providers (Go implementations)
    native map[string]ChatProvider
    
    // Cached plugin bridges
    bridges map[string]*provider.PluginProviderBridge
}

func NewProviderSelector(reg *registry.ProviderRegistry, host *runtime.OpenClawPluginHost) *ProviderSelector {
    return &ProviderSelector{
        registry:   reg,
        pluginHost: host,
        native:     make(map[string]ChatProvider),
        bridges:    make(map[string]*provider.PluginProviderBridge),
    }
}

// RegisterNative registers a Go-native provider.
func (s *ProviderSelector) RegisterNative(id string, p ChatProvider) {
    s.native[id] = p
}

// Select returns a ChatProvider for the given model ID.
func (s *ProviderSelector) Select(ctx context.Context, modelID string) (ChatProvider, error) {
    providerID := extractProviderID(modelID)
    
    // Check native providers first
    if native, ok := s.native[providerID]; ok {
        return native, nil
    }
    
    // Check for cached plugin bridge
    if bridge, ok := s.bridges[providerID]; ok {
        return bridge, nil
    }
    
    // Look up in registry
    meta, ok := s.registry.Get(providerID)
    if !ok {
        return nil, fmt.Errorf("provider not found: %s", providerID)
    }
    
    // Create bridge for plugin provider
    bridge := provider.NewPluginProviderBridge(providerID, meta.PluginID, s.pluginHost, meta)
    s.bridges[providerID] = bridge
    
    return bridge, nil
}

func extractProviderID(modelID string) string {
    // Handle formats like "anthropic/claude-3-opus" or "openai:gpt-4"
    if idx := strings.Index(modelID, "/"); idx > 0 {
        return modelID[:idx]
    }
    if idx := strings.Index(modelID, ":"); idx > 0 {
        return modelID[:idx]
    }
    // Infer from model name patterns
    switch {
    case strings.HasPrefix(modelID, "claude"):
        return "anthropic"
    case strings.HasPrefix(modelID, "gpt"):
        return "openai"
    case strings.HasPrefix(modelID, "gemini"):
        return "google"
    default:
        return "default"
    }
}
```

#### 4.4 Acceptance Criteria

- [ ] OpenClaw provider plugins load successfully
- [ ] Provider catalog returns available models
- [ ] Chat requests work through plugin bridge
- [ ] Token usage is correctly reported
- [ ] Tool calls are properly translated
- [ ] Streaming responses work (if supported)
- [ ] Auth flows work for providers requiring setup
- [ ] Native Go providers continue to work
- [ ] Fallback between providers works

---

### Phase 5: Channel Plugin Support
**Duration**: 2 weeks  
**Priority**: High  
**Dependencies**: Phase 1, Phase 2

#### 5.1 Objectives

- Enable OpenClaw channel plugins alongside Go-native channels
- Unify message routing for both channel types
- Support channel capabilities (typing, reactions, threads)
- Handle webhook registration for HTTP-based channels

#### 5.2 Files to Create/Modify

```
internal/plugins/
├── channel/
│   ├── bridge.go          # Channel plugin bridge
│   ├── router.go          # Unified message router
│   ├── capabilities.go    # Capability detection
│   └── bridge_test.go

internal/extensions/
├── registry.go            # (modify - integrate plugin channels)
└── loader.go              # (modify - load both native and plugin)
```

#### 5.3 Implementation Details

**plugins/channel/bridge.go**:
```go
package channel

import (
    "context"
    "fmt"
    
    "metiq/internal/plugins/runtime"
    "metiq/internal/plugins/sdk"
)

// PluginChannelBridge adapts an OpenClaw channel plugin to Swarmstr's ChannelPlugin.
type PluginChannelBridge struct {
    channelID   string
    pluginID    string
    channelType string
    host        *runtime.OpenClawPluginHost
    
    // Active connections
    handles map[string]*PluginChannelHandle
}

var _ sdk.ChannelPlugin = (*PluginChannelBridge)(nil)

func (p *PluginChannelBridge) ID() string   { return p.channelID }
func (p *PluginChannelBridge) Type() string { return p.channelType }

func (p *PluginChannelBridge) ConfigSchema() map[string]any {
    result, err := p.host.InvokeChannel(context.Background(), p.channelID, "config_schema", nil)
    if err != nil {
        return nil
    }
    if schema, ok := result.(map[string]any); ok {
        return schema
    }
    return nil
}

func (p *PluginChannelBridge) Capabilities() sdk.ChannelCapabilities {
    result, err := p.host.InvokeChannel(context.Background(), p.channelID, "capabilities", nil)
    if err != nil {
        return sdk.ChannelCapabilities{}
    }
    return parseCapabilities(result)
}

func (p *PluginChannelBridge) Connect(ctx context.Context, channelID string, 
    cfg map[string]any, onMessage func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
    
    // Register message callback
    callbackID := fmt.Sprintf("%s:%d", channelID, time.Now().UnixNano())
    p.host.RegisterCallback(callbackID, func(msg any) {
        if inbound, ok := parseInboundMessage(msg); ok {
            onMessage(inbound)
        }
    })
    
    // Connect via Node.js
    result, err := p.host.InvokeChannel(ctx, p.channelID, "connect", map[string]any{
        "channel_id":  channelID,
        "config":      cfg,
        "callback_id": callbackID,
    })
    if err != nil {
        p.host.UnregisterCallback(callbackID)
        return nil, err
    }
    
    handleID := result.(map[string]any)["handle_id"].(string)
    
    handle := &PluginChannelHandle{
        id:         channelID,
        handleID:   handleID,
        callbackID: callbackID,
        host:       p.host,
        pluginID:   p.pluginID,
    }
    
    p.handles[channelID] = handle
    return handle, nil
}

// PluginChannelHandle wraps a connected channel instance.
type PluginChannelHandle struct {
    id         string
    handleID   string
    callbackID string
    host       *runtime.OpenClawPluginHost
    pluginID   string
}

var _ sdk.ChannelHandle = (*PluginChannelHandle)(nil)

func (h *PluginChannelHandle) ID() string { return h.id }

func (h *PluginChannelHandle) Send(ctx context.Context, text string) error {
    _, err := h.host.InvokeChannel(ctx, h.handleID, "send", map[string]any{
        "text": text,
    })
    return err
}

func (h *PluginChannelHandle) Close() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    h.host.InvokeChannel(ctx, h.handleID, "close", nil)
    h.host.UnregisterCallback(h.callbackID)
}

// Optional capability methods

var _ sdk.TypingHandle = (*PluginChannelHandle)(nil)

func (h *PluginChannelHandle) SendTyping(ctx context.Context, durationMS int) error {
    _, err := h.host.InvokeChannel(ctx, h.handleID, "send_typing", map[string]any{
        "duration_ms": durationMS,
    })
    return err
}

var _ sdk.ReactionHandle = (*PluginChannelHandle)(nil)

func (h *PluginChannelHandle) AddReaction(ctx context.Context, eventID, emoji string) error {
    _, err := h.host.InvokeChannel(ctx, h.handleID, "add_reaction", map[string]any{
        "event_id": eventID,
        "emoji":    emoji,
    })
    return err
}

func (h *PluginChannelHandle) RemoveReaction(ctx context.Context, eventID, emoji string) error {
    _, err := h.host.InvokeChannel(ctx, h.handleID, "remove_reaction", map[string]any{
        "event_id": eventID,
        "emoji":    emoji,
    })
    return err
}

// ... implement ThreadHandle, AudioHandle, EditHandle similarly
```

#### 5.4 Acceptance Criteria

- [ ] OpenClaw channel plugins load and connect
- [ ] Messages route to correct handlers
- [ ] Outbound messages send successfully
- [ ] Typing indicators work
- [ ] Reactions work
- [ ] Thread replies work
- [ ] Native Go channels continue to work
- [ ] Webhook channels receive HTTP callbacks

---

### Phase 6: Service and Background Task Support
**Duration**: 1 week  
**Priority**: Medium  
**Dependencies**: Phase 1, Phase 2

#### 6.1 Objectives

- Support background services registered by plugins
- Manage service lifecycle (start, stop, health)
- Integrate with Swarmstr's daemon runtime

#### 6.2 Implementation

```go
// internal/plugins/service/manager.go

package service

import (
    "context"
    "sync"
    
    "metiq/internal/plugins/registry"
    "metiq/internal/plugins/runtime"
)

type ServiceManager struct {
    registry *registry.ServiceRegistry
    host     *runtime.OpenClawPluginHost
    
    mu       sync.RWMutex
    running  map[string]*RunningService
}

type RunningService struct {
    ID        string
    PluginID  string
    StartedAt time.Time
    Status    ServiceStatus
}

type ServiceStatus string

const (
    ServiceStatusStarting ServiceStatus = "starting"
    ServiceStatusRunning  ServiceStatus = "running"
    ServiceStatusStopping ServiceStatus = "stopping"
    ServiceStatusStopped  ServiceStatus = "stopped"
    ServiceStatusError    ServiceStatus = "error"
)

func (m *ServiceManager) StartAll(ctx context.Context) error {
    services := m.registry.All()
    
    for _, svc := range services {
        if err := m.Start(ctx, svc.ID); err != nil {
            log.Printf("service %s failed to start: %v", svc.ID, err)
        }
    }
    
    return nil
}

func (m *ServiceManager) Start(ctx context.Context, serviceID string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    if _, running := m.running[serviceID]; running {
        return nil // Already running
    }
    
    svc, ok := m.registry.Get(serviceID)
    if !ok {
        return fmt.Errorf("service not found: %s", serviceID)
    }
    
    m.running[serviceID] = &RunningService{
        ID:        serviceID,
        PluginID:  svc.PluginID,
        StartedAt: time.Now(),
        Status:    ServiceStatusStarting,
    }
    
    _, err := m.host.InvokeService(ctx, serviceID, "start", nil)
    if err != nil {
        m.running[serviceID].Status = ServiceStatusError
        return err
    }
    
    m.running[serviceID].Status = ServiceStatusRunning
    return nil
}

func (m *ServiceManager) StopAll(ctx context.Context) error {
    m.mu.RLock()
    ids := make([]string, 0, len(m.running))
    for id := range m.running {
        ids = append(ids, id)
    }
    m.mu.RUnlock()
    
    for _, id := range ids {
        m.Stop(ctx, id)
    }
    
    return nil
}

func (m *ServiceManager) Stop(ctx context.Context, serviceID string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    rs, ok := m.running[serviceID]
    if !ok {
        return nil
    }
    
    rs.Status = ServiceStatusStopping
    
    _, err := m.host.InvokeService(ctx, serviceID, "stop", nil)
    if err != nil {
        rs.Status = ServiceStatusError
        return err
    }
    
    delete(m.running, serviceID)
    return nil
}
```

---

### Phase 7: Tool System Alignment
**Duration**: 2 weeks  
**Priority**: High  
**Dependencies**: Phase 1, Phase 2

#### 7.1 Objectives

- Support OpenClaw's rich tool interface (`AnyAgentTool`)
- Handle TypeBox schema conversion
- Support tool result content types (text, image, etc.)
- Integrate with existing Swarmstr tool registry

#### 7.2 Implementation

```go
// internal/plugins/tools/bridge.go

package tools

import (
    "context"
    
    "metiq/internal/agent"
    "metiq/internal/plugins/runtime"
)

// PluginToolBridge adapts OpenClaw tools to Swarmstr's tool interface.
type PluginToolBridge struct {
    pluginID string
    tool     ToolMetadata
    host     *runtime.OpenClawPluginHost
}

type ToolMetadata struct {
    Name          string
    QualifiedName string
    Description   string
    Label         string
    Parameters    map[string]any // JSON Schema
    OwnerOnly     bool
}

// Execute runs the tool and returns the result.
func (b *PluginToolBridge) Execute(ctx context.Context, args map[string]any) (agent.ToolResult, error) {
    result, err := b.host.InvokeTool(ctx, b.pluginID, b.tool.Name, args)
    if err != nil {
        return agent.ToolResult{}, err
    }
    
    return parseToolResult(result)
}

// parseToolResult converts OpenClaw AgentToolResult to Swarmstr format.
func parseToolResult(result any) (agent.ToolResult, error) {
    data, ok := result.(map[string]any)
    if !ok {
        // Simple string result
        return agent.ToolResult{
            Content: []agent.ContentBlock{{Type: "text", Text: fmt.Sprint(result)}},
        }, nil
    }
    
    tr := agent.ToolResult{}
    
    // Handle content array
    if content, ok := data["content"].([]any); ok {
        for _, c := range content {
            block, ok := c.(map[string]any)
            if !ok {
                continue
            }
            
            switch block["type"] {
            case "text":
                tr.Content = append(tr.Content, agent.ContentBlock{
                    Type: "text",
                    Text: block["text"].(string),
                })
            case "image":
                tr.Content = append(tr.Content, agent.ContentBlock{
                    Type:      "image",
                    MediaType: block["mimeType"].(string),
                    Data:      block["data"].(string),
                })
            case "json":
                jsonBytes, _ := json.Marshal(block["json"])
                tr.Content = append(tr.Content, agent.ContentBlock{
                    Type: "text",
                    Text: string(jsonBytes),
                })
            }
        }
    }
    
    // Handle isError flag
    if isError, ok := data["isError"].(bool); ok && isError {
        tr.IsError = true
    }
    
    return tr, nil
}

// ConvertTypeBoxToJSONSchema converts TypeBox schema to standard JSON Schema.
// This handles the common TypeBox types used in OpenClaw plugins.
func ConvertTypeBoxToJSONSchema(typeboxSchema map[string]any) map[string]any {
    // TypeBox schemas are mostly JSON Schema compatible,
    // but may have additional metadata we need to strip
    
    result := make(map[string]any)
    
    for key, value := range typeboxSchema {
        switch key {
        case "$id", "$schema", "symbols", "static":
            // Skip TypeBox-specific keys
            continue
        case "properties":
            if props, ok := value.(map[string]any); ok {
                result["properties"] = convertProperties(props)
            }
        default:
            result[key] = value
        }
    }
    
    // Ensure type is set
    if _, ok := result["type"]; !ok {
        result["type"] = "object"
    }
    
    return result
}

func convertProperties(props map[string]any) map[string]any {
    result := make(map[string]any)
    
    for name, schema := range props {
        if schemaMap, ok := schema.(map[string]any); ok {
            result[name] = ConvertTypeBoxToJSONSchema(schemaMap)
        } else {
            result[name] = schema
        }
    }
    
    return result
}
```

---

### Phase 8: Media Generation & Understanding Subsystems
**Duration**: 5 weeks
**Priority**: High
**Dependencies**: Phase 1, Phase 2, Phase 4

#### 8.1 Objectives

Implement the media generation and understanding subsystems required to support OpenClaw's media plugins:

- **Image Generation**: Text-to-image, editing, variations
- **Video Generation**: Text-to-video, image-to-video, video-to-video
- **Music Generation**: Text-to-music
- **Realtime Transcription**: Streaming STT via WebSocket
- **Realtime Voice**: Bidirectional voice streaming
- **Advanced Media Understanding**: Multi-provider routing, batch processing, caching

#### 8.2 Subphase A: Image Generation Runtime (1.5 weeks)

**Files to Create:**
```
internal/imagegen/
├── types.go              # Request/response types
├── provider.go           # Provider interface
├── registry.go           # Provider registry
├── runtime.go            # Generation runtime
├── normalization.go      # Request normalization
├── tool.go               # Agent tool integration
└── runtime_test.go
```

**Implementation:**

```go
// internal/imagegen/types.go

package imagegen

// ImageGenerationRequest defines an image generation request.
type ImageGenerationRequest struct {
    // Prompt is the text description of the image to generate.
    Prompt string `json:"prompt"`
    
    // NegativePrompt describes what to avoid (optional).
    NegativePrompt string `json:"negative_prompt,omitempty"`
    
    // Model specifies the model to use (provider-specific).
    Model string `json:"model,omitempty"`
    
    // Size specifies dimensions (e.g., "1024x1024").
    Size string `json:"size,omitempty"`
    
    // Quality controls generation quality ("low", "medium", "high").
    Quality string `json:"quality,omitempty"`
    
    // Format specifies output format ("png", "jpeg", "webp").
    Format string `json:"format,omitempty"`
    
    // N is the number of images to generate.
    N int `json:"n,omitempty"`
    
    // SourceImage for edit/variation modes.
    SourceImage *SourceImage `json:"source_image,omitempty"`
    
    // Mask for inpainting (base64 PNG with alpha).
    Mask string `json:"mask,omitempty"`
    
    // Mode: "generate", "edit", "variation"
    Mode string `json:"mode,omitempty"`
}

type SourceImage struct {
    URL    string `json:"url,omitempty"`
    Base64 string `json:"base64,omitempty"`
    Mime   string `json:"mime,omitempty"`
}

// ImageGenerationResult contains generated images.
type ImageGenerationResult struct {
    Images []GeneratedImage `json:"images"`
    Model  string           `json:"model"`
    Usage  *UsageInfo       `json:"usage,omitempty"`
}

type GeneratedImage struct {
    URL       string `json:"url,omitempty"`
    Base64    string `json:"base64,omitempty"`
    Mime      string `json:"mime"`
    Width     int    `json:"width"`
    Height    int    `json:"height"`
    Seed      int64  `json:"seed,omitempty"`
    LocalPath string `json:"local_path,omitempty"` // If saved to disk
}

type UsageInfo struct {
    PromptTokens int `json:"prompt_tokens,omitempty"`
    Cost         float64 `json:"cost,omitempty"`
}

// Provider capabilities
type ProviderCapabilities struct {
    Generate   bool     `json:"generate"`
    Edit       bool     `json:"edit"`
    Variation  bool     `json:"variation"`
    Inpaint    bool     `json:"inpaint"`
    Outpaint   bool     `json:"outpaint"`
    Sizes      []string `json:"sizes"`
    Formats    []string `json:"formats"`
    MaxN       int      `json:"max_n"`
}
```

```go
// internal/imagegen/provider.go

package imagegen

import (
    "context"
)

// Provider defines the image generation provider interface.
type Provider interface {
    // ID returns the unique provider identifier.
    ID() string
    
    // Name returns the human-readable provider name.
    Name() string
    
    // Configured returns true if the provider has valid credentials.
    Configured() bool
    
    // Capabilities returns what this provider supports.
    Capabilities() ProviderCapabilities
    
    // Generate creates images from the request.
    Generate(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error)
}

// PluginProvider wraps an OpenClaw image generation plugin.
type PluginProvider struct {
    providerID string
    pluginID   string
    host       PluginHost
    caps       ProviderCapabilities
}

func (p *PluginProvider) ID() string   { return p.providerID }
func (p *PluginProvider) Name() string { return p.providerID }

func (p *PluginProvider) Configured() bool {
    result, err := p.host.InvokeProvider(context.Background(), p.providerID, "configured", nil)
    if err != nil {
        return false
    }
    configured, _ := result.(bool)
    return configured
}

func (p *PluginProvider) Capabilities() ProviderCapabilities {
    return p.caps
}

func (p *PluginProvider) Generate(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
    result, err := p.host.InvokeProvider(ctx, p.providerID, "generate", map[string]any{
        "prompt":          req.Prompt,
        "negative_prompt": req.NegativePrompt,
        "model":           req.Model,
        "size":            req.Size,
        "quality":         req.Quality,
        "format":          req.Format,
        "n":               req.N,
        "mode":            req.Mode,
        "source_image":    req.SourceImage,
        "mask":            req.Mask,
    })
    if err != nil {
        return nil, err
    }
    
    return parseImageGenerationResult(result)
}
```

```go
// internal/imagegen/registry.go

package imagegen

import (
    "fmt"
    "sort"
    "sync"
)

// Registry manages image generation providers.
type Registry struct {
    mu        sync.RWMutex
    providers map[string]Provider
}

func NewRegistry() *Registry {
    return &Registry{
        providers: make(map[string]Provider),
    }
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.providers[p.ID()] = p
}

// Get returns a provider by ID.
func (r *Registry) Get(id string) (Provider, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    p, ok := r.providers[id]
    return p, ok
}

// List returns all registered providers.
func (r *Registry) List() []Provider {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    result := make([]Provider, 0, len(r.providers))
    for _, p := range r.providers {
        result = append(result, p)
    }
    sort.Slice(result, func(i, j int) bool {
        return result[i].ID() < result[j].ID()
    })
    return result
}

// ListConfigured returns providers that are ready to use.
func (r *Registry) ListConfigured() []Provider {
    all := r.List()
    result := make([]Provider, 0)
    for _, p := range all {
        if p.Configured() {
            result = append(result, p)
        }
    }
    return result
}

// Default returns the first configured provider.
func (r *Registry) Default() (Provider, error) {
    configured := r.ListConfigured()
    if len(configured) == 0 {
        return nil, fmt.Errorf("no configured image generation providers")
    }
    return configured[0], nil
}
```

```go
// internal/imagegen/tool.go

package imagegen

import (
    "context"
    "encoding/base64"
    "fmt"
    "os"
    "path/filepath"
    "time"
    
    "metiq/internal/agent"
)

// CreateImageGenerationTool creates the image_generate agent tool.
func CreateImageGenerationTool(registry *Registry, outputDir string) agent.ToolDefinition {
    return agent.ToolDefinition{
        Name:        "image_generate",
        Description: "Generate an image from a text description using AI image generation models.",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "prompt": map[string]any{
                    "type":        "string",
                    "description": "A detailed description of the image to generate",
                },
                "size": map[string]any{
                    "type":        "string",
                    "description": "Image dimensions (e.g., '1024x1024', '1792x1024')",
                    "default":     "1024x1024",
                },
                "quality": map[string]any{
                    "type":        "string",
                    "enum":        []string{"low", "medium", "high"},
                    "description": "Image quality level",
                    "default":     "medium",
                },
                "provider": map[string]any{
                    "type":        "string",
                    "description": "Provider to use (e.g., 'openai', 'google', 'fal')",
                },
            },
            "required": []string{"prompt"},
        },
        Execute: func(ctx context.Context, args map[string]any) (string, error) {
            prompt := agent.ArgString(args, "prompt")
            if prompt == "" {
                return "", fmt.Errorf("prompt is required")
            }
            
            req := ImageGenerationRequest{
                Prompt:  prompt,
                Size:    agent.ArgString(args, "size"),
                Quality: agent.ArgString(args, "quality"),
                N:       1,
                Mode:    "generate",
            }
            
            // Select provider
            var provider Provider
            var err error
            providerID := agent.ArgString(args, "provider")
            if providerID != "" {
                provider, _ = registry.Get(providerID)
            }
            if provider == nil {
                provider, err = registry.Default()
                if err != nil {
                    return "", err
                }
            }
            
            // Generate image
            result, err := provider.Generate(ctx, req)
            if err != nil {
                return "", fmt.Errorf("image generation failed: %w", err)
            }
            
            if len(result.Images) == 0 {
                return "", fmt.Errorf("no images generated")
            }
            
            // Save to disk
            img := result.Images[0]
            filename := fmt.Sprintf("generated_%d.%s", time.Now().UnixMilli(), extensionFromMime(img.Mime))
            outputPath := filepath.Join(outputDir, filename)
            
            var data []byte
            if img.Base64 != "" {
                data, err = base64.StdEncoding.DecodeString(img.Base64)
                if err != nil {
                    return "", fmt.Errorf("decode image: %w", err)
                }
            } else if img.URL != "" {
                data, err = fetchImageData(ctx, img.URL)
                if err != nil {
                    return "", fmt.Errorf("fetch image: %w", err)
                }
            }
            
            if err := os.WriteFile(outputPath, data, 0644); err != nil {
                return "", fmt.Errorf("save image: %w", err)
            }
            
            return fmt.Sprintf("Image generated successfully: %s (%dx%d)", outputPath, img.Width, img.Height), nil
        },
    }
}
```

#### 8.3 Subphase B: Video Generation Runtime (1.5 weeks)

**Files to Create:**
```
internal/videogen/
├── types.go              # Request/response types
├── provider.go           # Provider interface
├── registry.go           # Provider registry
├── runtime.go            # Generation runtime with polling
├── tool.go               # Agent tool integration
└── runtime_test.go
```

**Key Differences from Image Generation:**

```go
// internal/videogen/types.go

package videogen

// VideoGenerationRequest defines a video generation request.
type VideoGenerationRequest struct {
    Prompt      string       `json:"prompt"`
    Model       string       `json:"model,omitempty"`
    Duration    int          `json:"duration,omitempty"`    // seconds
    Resolution  string       `json:"resolution,omitempty"` // "480P", "720P", "1080P"
    AspectRatio string       `json:"aspect_ratio,omitempty"` // "16:9", "9:16", "1:1"
    FPS         int          `json:"fps,omitempty"`
    Mode        string       `json:"mode,omitempty"` // "generate", "imageToVideo", "videoToVideo"
    SourceAsset *SourceAsset `json:"source_asset,omitempty"`
}

type SourceAsset struct {
    URL    string `json:"url,omitempty"`
    Base64 string `json:"base64,omitempty"`
    Mime   string `json:"mime,omitempty"`
    Role   string `json:"role,omitempty"` // "first_frame", "last_frame", "reference"
}

// VideoGenerationResult contains generated videos.
type VideoGenerationResult struct {
    Videos []GeneratedVideo `json:"videos"`
    Status string           `json:"status"` // "completed", "pending", "failed"
    JobID  string           `json:"job_id,omitempty"`
}

type GeneratedVideo struct {
    URL       string `json:"url,omitempty"`
    LocalPath string `json:"local_path,omitempty"`
    Duration  int    `json:"duration"`
    Width     int    `json:"width"`
    Height    int    `json:"height"`
    Format    string `json:"format"` // "mp4", "webm"
}

// ProviderCapabilities for video generation.
type ProviderCapabilities struct {
    Generate       bool     `json:"generate"`
    ImageToVideo   bool     `json:"image_to_video"`
    VideoToVideo   bool     `json:"video_to_video"`
    Resolutions    []string `json:"resolutions"`
    MaxDuration    int      `json:"max_duration"`
    AspectRatios   []string `json:"aspect_ratios"`
    SupportsAsync  bool     `json:"supports_async"` // Long-running jobs
}
```

```go
// internal/videogen/runtime.go

package videogen

import (
    "context"
    "fmt"
    "time"
)

// Runtime manages video generation with async job support.
type Runtime struct {
    registry     *Registry
    pollInterval time.Duration
    maxWait      time.Duration
}

// Generate handles both sync and async video generation.
func (r *Runtime) Generate(ctx context.Context, providerID string, req VideoGenerationRequest) (*VideoGenerationResult, error) {
    provider, ok := r.registry.Get(providerID)
    if !ok {
        return nil, fmt.Errorf("provider not found: %s", providerID)
    }
    
    result, err := provider.Generate(ctx, req)
    if err != nil {
        return nil, err
    }
    
    // If async job, poll for completion
    if result.Status == "pending" && result.JobID != "" {
        return r.pollForCompletion(ctx, provider, result.JobID)
    }
    
    return result, nil
}

func (r *Runtime) pollForCompletion(ctx context.Context, provider Provider, jobID string) (*VideoGenerationResult, error) {
    deadline := time.Now().Add(r.maxWait)
    
    for time.Now().Before(deadline) {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(r.pollInterval):
            result, err := provider.CheckJob(ctx, jobID)
            if err != nil {
                return nil, err
            }
            if result.Status == "completed" {
                return result, nil
            }
            if result.Status == "failed" {
                return nil, fmt.Errorf("video generation failed")
            }
            // Still pending, continue polling
        }
    }
    
    return nil, fmt.Errorf("video generation timed out after %v", r.maxWait)
}
```

#### 8.4 Subphase C: Music Generation Runtime (0.5 weeks)

**Files to Create:**
```
internal/musicgen/
├── types.go              # Request/response types
├── provider.go           # Provider interface
├── registry.go           # Provider registry
├── tool.go               # Agent tool integration
└── runtime_test.go
```

**Implementation** (similar pattern to image/video):

```go
// internal/musicgen/types.go

package musicgen

type MusicGenerationRequest struct {
    Prompt   string `json:"prompt"`
    Duration int    `json:"duration,omitempty"` // seconds
    Format   string `json:"format,omitempty"`   // "mp3", "wav"
    Model    string `json:"model,omitempty"`
}

type MusicGenerationResult struct {
    Audio     GeneratedAudio `json:"audio"`
    Duration  int            `json:"duration"`
}

type GeneratedAudio struct {
    URL       string `json:"url,omitempty"`
    Base64    string `json:"base64,omitempty"`
    LocalPath string `json:"local_path,omitempty"`
    Format    string `json:"format"`
}
```

#### 8.5 Subphase D: Realtime Transcription (Streaming STT) (1 week)

**Files to Create:**
```
internal/realtimestt/
├── types.go              # Session and transcript types
├── provider.go           # Provider interface
├── registry.go           # Provider registry
├── session.go            # WebSocket session management
├── websocket.go          # WebSocket handling
└── session_test.go
```

**Implementation:**

```go
// internal/realtimestt/types.go

package realtimestt

import "context"

// TranscriptCallback is called when transcript text is available.
type TranscriptCallback func(text string, isFinal bool)

// SessionConfig configures a transcription session.
type SessionConfig struct {
    Language    string            `json:"language,omitempty"`
    Model       string            `json:"model,omitempty"`
    SampleRate  int               `json:"sample_rate,omitempty"` // Hz
    Encoding    string            `json:"encoding,omitempty"`   // "pcm16", "opus"
    Channels    int               `json:"channels,omitempty"`
    OnTranscript TranscriptCallback `json:"-"`
}

// Session represents an active transcription session.
type Session interface {
    // SendAudio sends audio data to be transcribed.
    SendAudio(data []byte) error
    
    // Close terminates the session.
    Close() error
    
    // Done returns a channel that closes when the session ends.
    Done() <-chan struct{}
}
```

```go
// internal/realtimestt/provider.go

package realtimestt

import "context"

// Provider creates realtime transcription sessions.
type Provider interface {
    ID() string
    Name() string
    Configured() bool
    
    // CreateSession starts a new transcription session.
    CreateSession(ctx context.Context, cfg SessionConfig) (Session, error)
}

// PluginProvider wraps an OpenClaw realtime transcription plugin.
type PluginProvider struct {
    providerID string
    pluginID   string
    host       PluginHost
}

func (p *PluginProvider) CreateSession(ctx context.Context, cfg SessionConfig) (Session, error) {
    // Create session via plugin host
    result, err := p.host.InvokeProvider(ctx, p.providerID, "create_session", map[string]any{
        "language":    cfg.Language,
        "model":       cfg.Model,
        "sample_rate": cfg.SampleRate,
        "encoding":    cfg.Encoding,
    })
    if err != nil {
        return nil, err
    }
    
    sessionID := result.(map[string]any)["session_id"].(string)
    
    return &pluginSession{
        id:          sessionID,
        host:        p.host,
        providerID:  p.providerID,
        onTranscript: cfg.OnTranscript,
        done:        make(chan struct{}),
    }, nil
}
```

```go
// internal/realtimestt/session.go

package realtimestt

import (
    "sync"
)

type pluginSession struct {
    id           string
    host         PluginHost
    providerID   string
    onTranscript TranscriptCallback
    done         chan struct{}
    closed       bool
    mu           sync.Mutex
}

func (s *pluginSession) SendAudio(data []byte) error {
    s.mu.Lock()
    if s.closed {
        s.mu.Unlock()
        return fmt.Errorf("session closed")
    }
    s.mu.Unlock()
    
    // Send audio to plugin and handle any transcript responses
    result, err := s.host.InvokeProvider(context.Background(), s.providerID, "send_audio", map[string]any{
        "session_id": s.id,
        "audio":      base64.StdEncoding.EncodeToString(data),
    })
    if err != nil {
        return err
    }
    
    // Check for transcript in response
    if resultMap, ok := result.(map[string]any); ok {
        if transcript, ok := resultMap["transcript"].(string); ok && transcript != "" {
            isFinal, _ := resultMap["is_final"].(bool)
            if s.onTranscript != nil {
                s.onTranscript(transcript, isFinal)
            }
        }
    }
    
    return nil
}

func (s *pluginSession) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.closed {
        return nil
    }
    s.closed = true
    close(s.done)
    
    _, err := s.host.InvokeProvider(context.Background(), s.providerID, "close_session", map[string]any{
        "session_id": s.id,
    })
    return err
}

func (s *pluginSession) Done() <-chan struct{} {
    return s.done
}
```

#### 8.6 Subphase E: Realtime Voice (Bidirectional) (1 week)

**Files to Create:**
```
internal/realtimevoice/
├── types.go              # Bridge and session types
├── provider.go           # Provider interface
├── registry.go           # Provider registry
├── bridge.go             # Voice bridge implementation
├── codec.go              # Audio codec handling
└── bridge_test.go
```

**Implementation:**

```go
// internal/realtimevoice/types.go

package realtimevoice

import "context"

// AudioCallback is called when audio is received from the model.
type AudioCallback func(audio []byte, format string)

// BridgeConfig configures a voice bridge.
type BridgeConfig struct {
    Model          string        `json:"model,omitempty"`
    Voice          string        `json:"voice,omitempty"`
    Language       string        `json:"language,omitempty"`
    InputFormat    AudioFormat   `json:"input_format"`
    OutputFormat   AudioFormat   `json:"output_format"`
    SystemPrompt   string        `json:"system_prompt,omitempty"`
    OnAudio        AudioCallback `json:"-"`
    OnTranscript   func(text string, role string) `json:"-"`
}

type AudioFormat struct {
    Encoding   string `json:"encoding"`    // "pcm16", "opus", "mp3"
    SampleRate int    `json:"sample_rate"` // Hz
    Channels   int    `json:"channels"`
}

// Bridge represents an active bidirectional voice session.
type Bridge interface {
    // SendAudio sends user audio to the model.
    SendAudio(data []byte) error
    
    // SendText sends a text message to the model.
    SendText(text string) error
    
    // Interrupt stops current model output.
    Interrupt() error
    
    // Close terminates the bridge.
    Close() error
    
    // Done returns a channel that closes when the bridge ends.
    Done() <-chan struct{}
}
```

```go
// internal/realtimevoice/provider.go

package realtimevoice

import "context"

// Provider creates realtime voice bridges.
type Provider interface {
    ID() string
    Name() string
    Configured() bool
    
    // CreateBridge starts a new voice bridge session.
    CreateBridge(ctx context.Context, cfg BridgeConfig) (Bridge, error)
    
    // ListVoices returns available voices.
    ListVoices(ctx context.Context) ([]VoiceInfo, error)
}

type VoiceInfo struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Language    string `json:"language"`
    Gender      string `json:"gender,omitempty"`
    Description string `json:"description,omitempty"`
}
```

```go
// internal/realtimevoice/bridge.go

package realtimevoice

import (
    "context"
    "sync"
)

type pluginBridge struct {
    sessionID    string
    host         PluginHost
    providerID   string
    onAudio      AudioCallback
    onTranscript func(string, string)
    done         chan struct{}
    closed       bool
    mu           sync.Mutex
}

func (b *pluginBridge) SendAudio(data []byte) error {
    result, err := b.host.InvokeProvider(context.Background(), b.providerID, "bridge_send_audio", map[string]any{
        "session_id": b.sessionID,
        "audio":      base64.StdEncoding.EncodeToString(data),
    })
    if err != nil {
        return err
    }
    
    // Handle any audio/transcript in response
    b.handleResponse(result)
    return nil
}

func (b *pluginBridge) SendText(text string) error {
    result, err := b.host.InvokeProvider(context.Background(), b.providerID, "bridge_send_text", map[string]any{
        "session_id": b.sessionID,
        "text":       text,
    })
    if err != nil {
        return err
    }
    
    b.handleResponse(result)
    return nil
}

func (b *pluginBridge) Interrupt() error {
    _, err := b.host.InvokeProvider(context.Background(), b.providerID, "bridge_interrupt", map[string]any{
        "session_id": b.sessionID,
    })
    return err
}

func (b *pluginBridge) handleResponse(result any) {
    resultMap, ok := result.(map[string]any)
    if !ok {
        return
    }
    
    // Handle audio output
    if audioB64, ok := resultMap["audio"].(string); ok && audioB64 != "" {
        audio, _ := base64.StdEncoding.DecodeString(audioB64)
        format, _ := resultMap["format"].(string)
        if b.onAudio != nil {
            b.onAudio(audio, format)
        }
    }
    
    // Handle transcript
    if transcript, ok := resultMap["transcript"].(string); ok && transcript != "" {
        role, _ := resultMap["role"].(string)
        if b.onTranscript != nil {
            b.onTranscript(transcript, role)
        }
    }
}

func (b *pluginBridge) Close() error {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    if b.closed {
        return nil
    }
    b.closed = true
    close(b.done)
    
    _, err := b.host.InvokeProvider(context.Background(), b.providerID, "bridge_close", map[string]any{
        "session_id": b.sessionID,
    })
    return err
}

func (b *pluginBridge) Done() <-chan struct{} {
    return b.done
}
```

#### 8.7 Subphase F: Advanced Media Understanding (0.5 weeks)

Enhance existing media handling with OpenClaw-compatible features:

**Files to Modify/Create:**
```
internal/media/
├── understanding.go      # NEW: Media understanding orchestrator
├── routing.go            # NEW: Provider routing logic
├── cache.go              # NEW: Attachment caching
├── batch.go              # NEW: Batch processing
└── media.go              # MODIFY: Add provider abstraction
```

```go
// internal/media/understanding.go

package media

import (
    "context"
    "fmt"
)

// MediaUnderstandingRequest represents a media analysis request.
type MediaUnderstandingRequest struct {
    Attachments []MediaAttachment `json:"attachments"`
    Prompt      string            `json:"prompt,omitempty"`
    Mode        string            `json:"mode,omitempty"` // "describe", "transcribe", "analyze"
}

// MediaUnderstandingResult contains analysis results.
type MediaUnderstandingResult struct {
    Outputs []MediaOutput `json:"outputs"`
}

type MediaOutput struct {
    AttachmentIndex int    `json:"attachment_index"`
    Type            string `json:"type"` // "description", "transcript", "analysis"
    Text            string `json:"text"`
    Confidence      float64 `json:"confidence,omitempty"`
}

// Orchestrator routes media to appropriate providers.
type Orchestrator struct {
    imageProviders       []ImageDescriber
    audioTranscribers    []Transcriber
    videoDescribers      []VideoDescriber
    cache                *AttachmentCache
}

func (o *Orchestrator) Process(ctx context.Context, req MediaUnderstandingRequest) (*MediaUnderstandingResult, error) {
    result := &MediaUnderstandingResult{}
    
    for i, att := range req.Attachments {
        // Check cache first
        if cached, ok := o.cache.Get(att.CacheKey()); ok {
            result.Outputs = append(result.Outputs, cached)
            continue
        }
        
        var output MediaOutput
        var err error
        
        switch {
        case att.IsImage():
            output, err = o.processImage(ctx, att, req.Prompt)
        case att.IsAudio():
            output, err = o.processAudio(ctx, att)
        case att.IsVideo():
            output, err = o.processVideo(ctx, att, req.Prompt)
        default:
            continue
        }
        
        if err != nil {
            return nil, fmt.Errorf("process attachment %d: %w", i, err)
        }
        
        output.AttachmentIndex = i
        result.Outputs = append(result.Outputs, output)
        
        // Cache the result
        o.cache.Set(att.CacheKey(), output)
    }
    
    return result, nil
}
```

#### 8.8 Acceptance Criteria

**Image Generation:**
- [ ] Provider registry supports plugin providers
- [ ] Generate, edit, and variation modes work
- [ ] Output saved to configurable directory
- [ ] Agent tool integrated and functional
- [ ] At least one provider works end-to-end (e.g., OpenAI DALL-E)

**Video Generation:**
- [ ] Async job polling works correctly
- [ ] Text-to-video generation works
- [ ] Image-to-video works with source frame
- [ ] Agent tool integrated

**Music Generation:**
- [ ] Text-to-music generation works
- [ ] Output formats (mp3, wav) supported
- [ ] Agent tool integrated

**Realtime Transcription:**
- [ ] WebSocket sessions establish correctly
- [ ] Audio streaming works
- [ ] Partial and final transcripts delivered
- [ ] Graceful session cleanup

**Realtime Voice:**
- [ ] Bidirectional audio streaming works
- [ ] Model interruption works
- [ ] Text injection works
- [ ] Voice selection works

**Media Understanding:**
- [ ] Multi-attachment processing works
- [ ] Provider routing correct by media type
- [ ] Caching reduces redundant calls
- [ ] Batch processing efficient

---

### Phase 9: Web Search & Fetch Providers
**Duration**: 1 week
**Priority**: Medium
**Dependencies**: Phase 1, Phase 2

#### 9.1 Objectives

Support OpenClaw's web capability providers:
- Web search providers (Brave, Tavily, Exa, DuckDuckGo, etc.)
- Web fetch providers (Firecrawl, readability extractors)

#### 9.2 Implementation Approach

Each provider type follows the same pattern:

1. **Registry**: Store metadata about registered providers
2. **Bridge**: Adapt OpenClaw provider interface to Swarmstr
3. **Integration**: Wire into relevant runtime (agent, auto-reply, etc.)

Example for Web Search:

```go
// internal/plugins/websearch/bridge.go

package websearch

import (
    "context"
    
    "metiq/internal/plugins/runtime"
)

type PluginWebSearchBridge struct {
    providerID string
    pluginID   string
    host       *runtime.OpenClawPluginHost
}

type SearchResult struct {
    Title   string `json:"title"`
    URL     string `json:"url"`
    Snippet string `json:"snippet"`
}

func (b *PluginWebSearchBridge) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
    result, err := b.host.InvokeWebSearch(ctx, b.providerID, "search", map[string]any{
        "query":       query,
        "max_results": opts.MaxResults,
        "locale":      opts.Locale,
    })
    if err != nil {
        return nil, err
    }
    
    return parseSearchResults(result)
}

// Wire into agent tools
func CreateWebSearchTool(bridge *PluginWebSearchBridge) agent.ToolDefinition {
    return agent.ToolDefinition{
        Name:        "web_search",
        Description: "Search the web for current information",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": map[string]any{
                    "type":        "string",
                    "description": "The search query",
                },
            },
            "required": []string{"query"},
        },
        Execute: func(ctx context.Context, args map[string]any) (string, error) {
            query := args["query"].(string)
            results, err := bridge.Search(ctx, query, SearchOptions{MaxResults: 5})
            if err != nil {
                return "", err
            }
            return formatSearchResults(results), nil
        },
    }
}
```

#### 8.3 Acceptance Criteria

For each provider type:
- [ ] Provider plugins register successfully
- [ ] Provider invocation works
- [ ] Results are properly translated
- [ ] Error handling works
- [ ] Integration with agent/tools works

---

### Phase 10: Plugin Installation and Management
**Duration**: 2 weeks  
**Priority**: Medium  
**Dependencies**: Phase 1, Phase 2

#### 9.1 Objectives

- Support installing OpenClaw plugins from npm/ClawHub
- Manage plugin lifecycle (install, enable, disable, uninstall)
- Handle plugin configuration
- Support plugin updates

#### 9.2 Files to Create/Modify

```
internal/plugins/
├── installer/
│   ├── npm.go             # (existing - extend for OpenClaw format)
│   ├── clawhub.go         # ClawHub registry support
│   ├── manifest.go        # OpenClaw manifest parsing
│   └── installer_test.go
├── lifecycle/
│   └── lifecycle.go       # (existing - extend for OpenClaw)
```

#### 9.3 Implementation

```go
// internal/plugins/installer/clawhub.go

package installer

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

const ClawHubRegistryURL = "https://registry.clawhub.ai"

type ClawHubClient struct {
    httpClient *http.Client
    baseURL    string
}

type ClawHubPlugin struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Version     string            `json:"version"`
    Description string            `json:"description"`
    Author      string            `json:"author"`
    Repository  string            `json:"repository"`
    Downloads   int               `json:"downloads"`
    Verified    bool              `json:"verified"`
    Capabilities []string         `json:"capabilities"`
    OpenClaw    OpenClawMetadata  `json:"openclaw"`
}

type OpenClawMetadata struct {
    Compat struct {
        PluginApi string `json:"pluginApi"`
    } `json:"compat"`
    Build struct {
        OpenClawVersion string `json:"openclawVersion"`
    } `json:"build"`
}

func (c *ClawHubClient) Search(ctx context.Context, query string) ([]ClawHubPlugin, error) {
    // Search ClawHub registry
}

func (c *ClawHubClient) Install(ctx context.Context, pluginID, version string, installPath string) error {
    // Download and install plugin
}

func (c *ClawHubClient) GetPluginInfo(ctx context.Context, pluginID string) (*ClawHubPlugin, error) {
    // Get plugin metadata
}
```

```go
// internal/plugins/installer/manifest.go

package installer

import (
    "encoding/json"
    "os"
    "path/filepath"
)

// OpenClawPluginManifest represents an openclaw.plugin.json file.
type OpenClawPluginManifest struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Version     string   `json:"version"`
    Description string   `json:"description"`
    Kind        []string `json:"kind"`
    Entry       string   `json:"entry"`
    
    Capabilities struct {
        Tools     bool `json:"tools"`
        Providers bool `json:"providers"`
        Channels  bool `json:"channels"`
        Hooks     bool `json:"hooks"`
        Services  bool `json:"services"`
    } `json:"capabilities"`
    
    ConfigSchema map[string]any `json:"configSchema"`
}

// LoadOpenClawManifest reads and parses an OpenClaw plugin manifest.
func LoadOpenClawManifest(pluginPath string) (*OpenClawPluginManifest, error) {
    manifestPath := filepath.Join(pluginPath, "openclaw.plugin.json")
    
    data, err := os.ReadFile(manifestPath)
    if err != nil {
        // Try package.json openclaw block
        return loadFromPackageJSON(pluginPath)
    }
    
    var manifest OpenClawPluginManifest
    if err := json.Unmarshal(data, &manifest); err != nil {
        return nil, err
    }
    
    return &manifest, nil
}

func loadFromPackageJSON(pluginPath string) (*OpenClawPluginManifest, error) {
    pkgPath := filepath.Join(pluginPath, "package.json")
    
    data, err := os.ReadFile(pkgPath)
    if err != nil {
        return nil, err
    }
    
    var pkg struct {
        Name        string `json:"name"`
        Version     string `json:"version"`
        Description string `json:"description"`
        Main        string `json:"main"`
        OpenClaw    struct {
            ID     string   `json:"id"`
            Kind   []string `json:"kind"`
            Compat struct {
                PluginApi string `json:"pluginApi"`
            } `json:"compat"`
        } `json:"openclaw"`
    }
    
    if err := json.Unmarshal(data, &pkg); err != nil {
        return nil, err
    }
    
    return &OpenClawPluginManifest{
        ID:          pkg.OpenClaw.ID,
        Name:        pkg.Name,
        Version:     pkg.Version,
        Description: pkg.Description,
        Kind:        pkg.OpenClaw.Kind,
        Entry:       pkg.Main,
    }, nil
}
```

---

### Phase 11: Testing, Documentation, and Polish
**Duration**: 3 weeks  
**Priority**: High  
**Dependencies**: All previous phases

#### 10.1 Objectives

- Comprehensive test coverage
- Integration tests with real OpenClaw plugins
- Documentation for plugin developers
- Migration guide for existing Swarmstr plugins
- Performance optimization

#### 10.2 Testing Strategy

**Unit Tests:**
- Each registry component
- JSON-RPC protocol
- Schema translation
- Result parsing

**Integration Tests:**
- Load real OpenClaw plugins (Anthropic, Tavily, etc.)
- End-to-end tool execution
- Hook invocation chains
- Provider chat flows

**Compatibility Tests:**
- Test against OpenClaw's plugin test suite
- Verify behavior matches OpenClaw runtime

#### 10.3 Documentation

```
docs/plugins/
├── overview.md              # Plugin system overview
├── openclaw-compatibility.md # OpenClaw compatibility details
├── writing-plugins.md       # How to write plugins
├── api-reference.md         # API documentation
├── migration-guide.md       # Migrating from old format
└── examples/
    ├── simple-tool/
    ├── provider-plugin/
    └── channel-plugin/
```

---

## Part 4: Risk Assessment

### 4.1 Technical Risks

| Risk | Probability | Impact | Mitigation |
|------|------------|--------|------------|
| Node.js subprocess instability | Medium | High | Watchdog process, auto-restart |
| JSON-RPC latency | Medium | Medium | Batch operations, caching |
| TypeBox schema edge cases | Medium | Low | Comprehensive test suite |
| OpenClaw API changes | Low | High | Pin SDK version, test against releases |
| Memory leaks in long-running plugins | Medium | Medium | Resource monitoring, limits |

### 4.2 Schedule Risks

| Risk | Probability | Impact | Mitigation |
|------|------------|--------|------------|
| Hook system complexity | Medium | High | Start with core hooks, iterate |
| Provider integration issues | Medium | Medium | Focus on top 3 providers first |
| Underestimated scope | Medium | High | Weekly checkpoints, scope cuts |

---

## Part 5: Timeline Summary

```
Week 1-2:   Phase 1 - Node.js Plugin Host Foundation
Week 3-4:   Phase 2 - Unified Capability Registry  
Week 5-6:   Phase 3 - Hook System Implementation
Week 7-9:   Phase 4 - Provider Plugin Support
Week 10-11: Phase 5 - Channel Plugin Support
Week 12:    Phase 6 - Service Support
Week 13-14: Phase 7 - Tool System Alignment
Week 15-19: Phase 8 - Media Generation & Understanding (5 weeks)
Week 20:    Phase 9 - Web Search & Fetch Providers
Week 21-22: Phase 10 - Plugin Installation
Week 23-25: Phase 11 - Testing & Documentation

Total: ~25 weeks (6.25 months)
```

### Accelerated Timeline (Parallel Execution)

With 2-3 developers working in parallel:

```
Weeks 1-2:   Phase 1 (Node.js Host)
Weeks 3-4:   Phase 2 (Registry) + Phase 7 (Tools) in parallel
Weeks 5-6:   Phase 3 (Hooks) + Phase 5 (Channels) in parallel
Weeks 7-9:   Phase 4 (Providers) + Phase 6 (Services) in parallel
Weeks 10-14: Phase 8 (Media Generation & Understanding)
Weeks 15:    Phase 9 (Web Search & Fetch)
Weeks 16-17: Phase 10 (Installation)
Weeks 18-20: Phase 11 (Testing & Docs)

Total: ~20 weeks (5 months)
```

---

## Part 6: Success Metrics

### 6.1 Compatibility Metrics

- **Plugin Load Success Rate**: >95% of tested OpenClaw plugins load successfully
- **Tool Execution Parity**: Tool outputs match OpenClaw reference implementation
- **Provider Compatibility**: Top 10 providers (Anthropic, OpenAI, Google, etc.) work

### 6.2 Performance Metrics

- **Plugin Load Time**: <500ms per plugin
- **Tool Invocation Overhead**: <10ms vs native Go tools
- **Hook Invocation Latency**: <5ms for in-process hooks
- **Memory Overhead**: <50MB per loaded plugin

### 6.3 Quality Metrics

- **Test Coverage**: >80% for new code
- **Documentation Coverage**: All public APIs documented
- **Zero regressions**: Existing Swarmstr functionality unchanged

---

## Appendix A: OpenClaw Plugin API Reference

### A.1 Registration Methods

| Method | Description | Phase |
|--------|-------------|-------|
| `registerTool` | Register an agent tool | 1 |
| `registerProvider` | Register a model provider | 4 |
| `registerChannel` | Register a messaging channel | 5 |
| `registerHook` | Register an event hook | 3 |
| `registerService` | Register a background service | 6 |
| `registerCommand` | Register a CLI command | 9 |
| `registerGatewayMethod` | Register a gateway RPC method | 2 |
| `registerSpeechProvider` | Register TTS provider | 8 |
| `registerRealtimeTranscriptionProvider` | Register STT provider | 8 |
| `registerRealtimeVoiceProvider` | Register voice provider | 8 |
| `registerMediaUnderstandingProvider` | Register media analysis | 8 |
| `registerImageGenerationProvider` | Register image gen | 8 |
| `registerVideoGenerationProvider` | Register video gen | 8 |
| `registerMusicGenerationProvider` | Register music gen | 8 |
| `registerWebFetchProvider` | Register web fetcher | 8 |
| `registerWebSearchProvider` | Register web search | 8 |
| `registerMemoryEmbeddingProvider` | Register embeddings | 8 |
| `registerConfigMigration` | Register config migrator | 9 |
| `registerMigrationProvider` | Register data migrator | 9 |
| `registerAutoEnableProbe` | Register auto-enable check | 9 |
| `registerCli` | Register CLI extensions | 9 |
| `registerCliBackend` | Register CLI backend | 9 |
| `registerHttpRoute` | Register HTTP route | 2 |
| `registerReload` | Register reload handler | 6 |
| `registerNodeHostCommand` | Register node command | 1 |
| `registerNodeInvokePolicy` | Register invoke policy | 1 |
| `registerSecurityAuditCollector` | Register audit collector | 9 |
| `registerGatewayDiscoveryService` | Register discovery | 6 |
| `registerTextTransforms` | Register text transforms | 7 |
| `registerInteractiveHandler` | Register interactive | 9 |
| `onConversationBindingResolved` | Conversation binding | 3 |
| `on` | Event listener alias | 3 |

### A.2 Hook Events

| Event | Description | Phase |
|-------|-------------|-------|
| `before_agent_start` | Before agent begins | 3 |
| `before_agent_reply` | Before sending reply | 3 |
| `before_prompt_build` | Before prompt construction | 3 |
| `before_model_resolve` | Before model selection | 3 |
| `llm_input` | LLM request prepared | 3 |
| `llm_output` | LLM response received | 3 |
| `model_call_started` | Model API call started | 3 |
| `model_call_ended` | Model API call ended | 3 |
| `agent_end` | Agent turn completed | 3 |
| `before_agent_finalize` | Before finalizing response | 3 |
| `before_compaction` | Before history compaction | 3 |
| `after_compaction` | After history compaction | 3 |
| `before_reset` | Before session reset | 3 |
| `before_tool_call` | Before tool execution | 3 |
| `after_tool_call` | After tool execution | 3 |
| `tool_result_persist` | Tool result being saved | 3 |
| `before_message_write` | Before message persistence | 3 |
| `inbound_claim` | Message routing decision | 3 |
| `message_received` | Message received | 3 |
| `message_sending` | Message about to send | 3 |
| `message_sent` | Message sent | 3 |
| `before_dispatch` | Before reply dispatch | 3 |
| `reply_dispatch` | Reply being dispatched | 3 |
| `session_start` | Session started | 3 |
| `session_end` | Session ended | 3 |
| `subagent_spawning` | Subagent being created | 3 |
| `subagent_spawned` | Subagent created | 3 |
| `subagent_ended` | Subagent completed | 3 |
| `subagent_delivery_target` | Subagent target resolution | 3 |
| `gateway_start` | Gateway starting | 3 |
| `gateway_stop` | Gateway stopping | 3 |
| `cron_changed` | Cron job modified | 3 |
| `before_install` | Before plugin install | 9 |
| `agent_turn_prepare` | Turn preparation | 3 |
| `heartbeat_prompt_contribution` | Heartbeat prompt | 3 |

---

## Appendix B: File Change Summary

### New Files (~40 files)

```
internal/plugins/runtime/
├── openclaw_host.go
├── openclaw_protocol.go
├── openclaw_shim.js
├── openclaw_api.js
└── openclaw_host_test.go

internal/plugins/registry/
├── unified.go
├── tools.go
├── providers.go
├── channels.go
├── hooks.go
├── services.go
├── commands.go
├── capabilities.go
└── unified_test.go

internal/hooks/
├── emitter.go
├── invoker.go
├── results.go
├── events.go
├── integration.go
└── emitter_test.go

internal/plugins/provider/
├── bridge.go
├── catalog.go
├── auth.go
├── transport.go
└── bridge_test.go

internal/plugins/channel/
├── bridge.go
├── router.go
├── capabilities.go
└── bridge_test.go

internal/plugins/service/
├── manager.go
└── manager_test.go

internal/plugins/tools/
├── bridge.go
├── schema.go
└── bridge_test.go

internal/plugins/websearch/
├── bridge.go
└── bridge_test.go

internal/plugins/installer/
├── clawhub.go
├── manifest.go
└── clawhub_test.go

docs/plugins/
├── overview.md
├── openclaw-compatibility.md
├── writing-plugins.md
├── api-reference.md
└── migration-guide.md
```

### Modified Files (~15 files)

```
internal/plugins/runtime/node_host.go      # Extend for OpenClaw
internal/plugins/runtime/node_shim.js      # Replace with openclaw_shim.js
internal/plugins/manager/manager.go        # Integrate unified registry
internal/plugins/manifest/registry.go      # Extend capability types
internal/plugins/lifecycle/lifecycle.go    # Support OpenClaw plugins
internal/plugins/installer/npm.go          # OpenClaw manifest support
internal/agent/agentic_loop.go             # Add hook emission points
internal/agent/provider.go                 # Integrate plugin providers
internal/agent/turn.go                     # Add hook points
internal/autoreply/autoreply.go            # Hook integration
internal/extensions/registry.go            # Unified channel registry
internal/gateway/methods/methods.go        # Plugin gateway methods
cmd/metiqd/main.go                         # Initialize OpenClaw host
```

---

*End of Plan*
