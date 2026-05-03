package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestOpenClawHost(t *testing.T) *OpenClawPluginHost {
	t.Helper()
	skipIfNoNode(t)
	h, err := NewOpenClawPluginHost(context.Background())
	if err != nil {
		t.Fatalf("NewOpenClawPluginHost: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func writeOpenClawPlugin(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestOpenClawPluginHost_startsAndCloses(t *testing.T) {
	h := newTestOpenClawHost(t)
	if h.proc == nil || h.proc.Process == nil {
		t.Fatal("expected running node process")
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestOpenClawPluginHost_loadsDefinePluginEntryAndCapturesRegistrations(t *testing.T) {
	h := newTestOpenClawHost(t)
	pluginDir := writeOpenClawPlugin(t, `
'use strict';
const { definePluginEntry } = require('openclaw/plugin-sdk/plugin-entry');
module.exports = definePluginEntry({
  id: 'phase-one',
  name: 'Phase One',
  version: '1.2.3',
  description: 'registration coverage',
  register(api) {
    api.registerTool({ name: 'echo', description: 'Echo args', parameters: { type: 'object', properties: { msg: { type: 'string' } } }, execute: async (_id, args) => ({ echoed: args.msg }) });
    api.registerTool({ name: 'sum', description: 'Sum values', execute: async (_id, args) => ({ total: args.a + args.b }) }, { optional: true });
    api.registerProvider({ id: 'prov', label: 'Provider', auth: [{ id: 'key', run: async () => ({ ok: true }) }], catalog: { run: async () => ({ models: ['m1'] }) } });
    api.registerChannel({ plugin: { ID: () => 'chan', Type: () => 'chat', ConfigSchema: () => ({ type: 'object' }) } });
    api.registerHook(['before_prompt_build', 'after_tool_call'], async (payload) => ({ seen: payload.value }), { priority: 5 });
    api.registerService({ id: 'svc', start: async () => ({ started: true }), stop: async () => ({ stopped: true }) });
    api.registerCommand({ name: 'hello', description: 'Hello command', acceptsArgs: true });
    api.registerGatewayMethod('phase.ping', async () => ({ pong: true }), { scope: 'operator.agent' });
    api.registerSpeechProvider({ id: 'speech' });
    api.registerRealtimeTranscriptionProvider({ id: 'stt' });
    api.registerRealtimeVoiceProvider({ id: 'voice' });
    api.registerMediaUnderstandingProvider({ id: 'media' });
    api.registerImageGenerationProvider({ id: 'image' });
    api.registerVideoGenerationProvider({ id: 'video' });
    api.registerMusicGenerationProvider({ id: 'music' });
    api.registerWebFetchProvider({ id: 'fetch' });
    api.registerWebSearchProvider({ id: 'search' });
    api.registerMemoryEmbeddingProvider({ id: 'embed' });
    api.registerConfigMigration(() => ({}));
    api.registerMigrationProvider({ id: 'migrate' });
    api.registerAutoEnableProbe({ id: 'probe' });
    api.registerCli(() => {}, { commands: ['phase'] });
    api.registerCliBackend({ id: 'cli-backend' });
    api.registerHttpRoute({ path: '/phase', auth: 'none' });
    api.registerReload({ paths: ['index.js'] });
    api.registerNodeHostCommand({ command: 'node-cmd' });
    api.registerNodeInvokePolicy({ commands: ['node-cmd'] });
    api.registerSecurityAuditCollector({ id: 'audit' });
    api.registerGatewayDiscoveryService({ id: 'bonjour' });
    api.registerTextTransforms({ input: [] });
    api.registerInteractiveHandler({ id: 'interactive' });
    api.onConversationBindingResolved(async () => {});
    api.registerContextEngine('ctx', () => ({}));
    api.registerCompactionProvider({ id: 'compact' });
    api.registerAgentHarness({ id: 'harness' });
    api.registerAgentToolResultMiddleware(async () => ({}), { runtime: 'test' });
    api.registerSessionExtension({ id: 'session-ext' });
    api.registerTrustedToolPolicy({ id: 'policy' });
    api.registerToolMetadata({ toolName: 'echo', title: 'Echo' });
    api.registerControlUiDescriptor({ id: 'ui' });
    api.registerRuntimeLifecycle({ id: 'life' });
    api.registerAgentEventSubscription({ id: 'agent-events', events: ['run.started'] });
    api.registerSessionSchedulerJob({ id: 'job' });
    api.registerDetachedTaskRuntime({ id: 'detached' });
    api.registerMemoryCapability({ id: 'memory' });
    api.registerMemoryPromptSection(() => 'section');
    api.registerMemoryPromptSupplement(() => 'supplement');
    api.registerMemoryCorpusSupplement({ id: 'corpus' });
    api.registerMemoryFlushPlan(() => ({}));
    api.registerMemoryRuntime({ id: 'memory-runtime' });
  }
});
`)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := h.LoadPluginResult(ctx, pluginDir, nil)
	if err != nil {
		t.Fatalf("LoadPluginResult: %v", err)
	}
	h.processRegistrations(result.PluginID, result.Registrations)

	if result.PluginID != "phase-one" || result.Name != "Phase One" {
		t.Fatalf("unexpected load result: %+v", result)
	}
	if got := len(result.Registrations); got < 45 {
		t.Fatalf("expected broad registration capture, got %d registrations", got)
	}
	tools := h.Tools()
	if tools["phase-one/echo"] == nil || tools["phase-one/sum"] == nil {
		t.Fatalf("tools not indexed: %#v", tools)
	}
	if !tools["phase-one/sum"].Optional {
		t.Error("expected optional tool metadata")
	}
	providers := h.Providers()
	if providers["prov"] == nil || !providers["prov"].HasCatalog || !providers["prov"].HasAuth {
		t.Fatalf("provider metadata not indexed: %#v", providers["prov"])
	}
	if len(h.hooks["before_prompt_build"]) != 1 || h.services["svc"] == nil || h.commands["hello"] == nil || h.channels["chan"] == nil {
		t.Fatalf("expected hook/service/command/channel indexes to be populated")
	}
}

func TestOpenClawPluginHost_invokeToolProviderHookAndService(t *testing.T) {
	h := newTestOpenClawHost(t)
	pluginDir := writeOpenClawPlugin(t, `
'use strict';
module.exports = {
  id: 'invoke-plugin',
  name: 'Invoke Plugin',
  register(api) {
    api.registerTool({ name: 'echo', execute: async (_id, args) => ({ echoed: args.msg }) });
    api.registerProvider({ id: 'prov', catalog: { run: async (params) => ({ models: [params.prefix + '-model'] }) } });
    api.registerHook('event', async (payload) => ({ order: 2, payload }), { priority: 20 });
    api.registerHook('event', async (payload) => ({ order: 1, payload }), { priority: 1 });
    api.registerService({ id: 'svc', start: async () => ({ started: true }), stop: async () => ({ stopped: true }) });
  },
  init(api, params) { return { initialized: true, plugin: api.id, params }; }
};
`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.LoadPlugin(ctx, pluginDir); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	initResult, err := h.InitPlugin(ctx, "invoke-plugin", map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("InitPlugin: %v", err)
	}
	if !nestedBool(initResult, "initialized") {
		t.Fatalf("unexpected init result: %#v", initResult)
	}

	toolResult, err := h.InvokeTool(ctx, "invoke-plugin", "echo", map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("InvokeTool: %v", err)
	}
	if nestedString(toolResult, "echoed") != "hello" {
		t.Fatalf("unexpected tool result: %#v", toolResult)
	}

	providerResult, err := h.InvokeProvider(ctx, "prov", "catalog", map[string]any{"prefix": "test"})
	if err != nil {
		t.Fatalf("InvokeProvider: %v", err)
	}
	models, _ := providerResult.(map[string]any)["models"].([]any)
	if len(models) != 1 || models[0] != "test-model" {
		t.Fatalf("unexpected provider result: %#v", providerResult)
	}

	hookResults, err := h.InvokeHook(ctx, "event", map[string]any{"value": "payload"})
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	if len(hookResults) != 2 || nestedFloat(hookResults[0].Result, "order") != 1 || nestedFloat(hookResults[1].Result, "order") != 2 {
		t.Fatalf("hooks not invoked in priority order: %#v", hookResults)
	}

	startResult, err := h.StartService(ctx, "svc", nil)
	if err != nil || !nestedBool(startResult, "started") {
		t.Fatalf("StartService result=%#v err=%v", startResult, err)
	}
	stopResult, err := h.StopService(ctx, "svc", nil)
	if err != nil || !nestedBool(stopResult, "stopped") {
		t.Fatalf("StopService result=%#v err=%v", stopResult, err)
	}
}

func TestOpenClawPluginHost_concurrentRequestsUseIDCorrelation(t *testing.T) {
	h := newTestOpenClawHost(t)
	pluginDir := writeOpenClawPlugin(t, `
'use strict';
module.exports = {
  id: 'concurrent-plugin',
  register(api) {
    api.registerTool({ name: 'delay', execute: async (_id, args) => new Promise((resolve) => setTimeout(() => resolve({ index: args.index }), args.delay)) });
  }
};
`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.LoadPlugin(ctx, pluginDir); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	const calls = 12
	results := make([]int, calls)
	var wg sync.WaitGroup
	errCh := make(chan error, calls)
	for i := 0; i < calls; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := h.InvokeTool(ctx, "concurrent-plugin", "delay", map[string]any{"index": i, "delay": (calls - i) * 3})
			if err != nil {
				errCh <- err
				return
			}
			results[i] = int(nestedFloat(res, "index"))
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, got := range results {
		if got != i {
			t.Fatalf("response correlation failed at %d: got %d results=%v", i, got, results)
		}
	}
}

func TestOpenClawPluginHost_loadErrorsAreMeaningful(t *testing.T) {
	h := newTestOpenClawHost(t)
	pluginDir := writeOpenClawPlugin(t, `module.exports = { id: 'bad-plugin' };`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := h.LoadPlugin(ctx, pluginDir)
	if err == nil || !strings.Contains(err.Error(), "register(api)") {
		t.Fatalf("expected register error, got %v", err)
	}
}

func TestOpenClawPluginHost_pluginStdoutLoggingDoesNotCorruptRPC(t *testing.T) {
	h := newTestOpenClawHost(t)
	pluginDir := writeOpenClawPlugin(t, `
module.exports = {
  id: 'logging-plugin',
  register(api) {
    console.log('register log should go to stderr');
    api.registerTool({ name: 'log', execute: async (_id, args) => {
      console.log('invoke log should go to stderr', args);
      return { ok: true };
    }});
  }
};
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.LoadPlugin(ctx, pluginDir); err != nil {
		t.Fatalf("LoadPlugin with console.log: %v", err)
	}
	result, err := h.InvokeTool(ctx, "logging-plugin", "log", map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("InvokeTool with console.log: %v", err)
	}
	if !nestedBool(result, "ok") {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestOpenClawPluginHost_duplicatePluginLoadIsRejected(t *testing.T) {
	h := newTestOpenClawHost(t)
	pluginDir := writeOpenClawPlugin(t, `
module.exports = {
  id: 'duplicate-plugin',
  register(api) {
    api.registerHook('event', async () => ({ ok: true }));
  }
};
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.LoadPlugin(ctx, pluginDir); err != nil {
		t.Fatalf("first LoadPlugin: %v", err)
	}
	err := h.LoadPlugin(ctx, pluginDir)
	if err == nil || !strings.Contains(err.Error(), "already loaded") {
		t.Fatalf("expected duplicate load error, got %v", err)
	}
	if got := len(h.hooks["event"]); got != 1 {
		t.Fatalf("duplicate load should not append hooks, got %d", got)
	}
}

func TestOpenClawPluginHost_hookErrorsPropagateInResults(t *testing.T) {
	h := newTestOpenClawHost(t)
	pluginDir := writeOpenClawPlugin(t, `
module.exports = {
  id: 'hook-errors',
  register(api) {
    api.registerHook('event', async () => { throw new Error('boom'); }, { priority: 1 });
    api.registerHook('event', async () => ({ after: true }), { priority: 2 });
  }
};
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.LoadPlugin(ctx, pluginDir); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	results, err := h.InvokeHook(ctx, "event", nil)
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	if len(results) != 2 || results[0].OK || !strings.Contains(results[0].Error, "boom") || !results[1].OK {
		t.Fatalf("unexpected hook results: %#v", results)
	}
}

func TestOpenClawPluginHost_processExitFailsPendingCalls(t *testing.T) {
	h := newTestOpenClawHost(t)

	respCh := make(chan *RPCResponse, 1)
	h.mu.Lock()
	h.pending[999] = respCh
	h.mu.Unlock()

	if err := h.proc.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	select {
	case resp := <-respCh:
		if resp == nil || !strings.Contains(resp.Error, "process exited") {
			t.Fatalf("expected process exited response, got %#v", resp)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for pending call failure")
	}
}

func TestOpenClawPluginHost_closedHostRejectsCalls(t *testing.T) {
	h := newTestOpenClawHost(t)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := h.InvokeTool(context.Background(), "p", "t", nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed error, got %v", err)
	}
}

func nestedString(v any, key string) string {
	m, _ := v.(map[string]any)
	s, _ := m[key].(string)
	return s
}

func nestedBool(v any, key string) bool {
	m, _ := v.(map[string]any)
	b, _ := m[key].(bool)
	return b
}

func nestedFloat(v any, key string) float64 {
	m, ok := v.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("not a map: %#v", v))
	}
	f, _ := m[key].(float64)
	return f
}
