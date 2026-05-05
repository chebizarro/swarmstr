// Package runtime implements the Goja (ES2015+) plugin host for Metiq.
//
// A GojaPlugin is a loaded JS plugin: the script has been compiled, exports.manifest
// has been read, and the VM is ready to dispatch tool invocations via Invoke().
//
// Lifecycle:
//
//	src, _ := os.ReadFile("my-plugin.js")
//	host := &sdk.Host{Nostr: ..., Config: ..., ...}
//	plugin, err := runtime.LoadPlugin(ctx, src, host)
//	result, err := plugin.Invoke(ctx, sdk.InvokeRequest{Tool: "tool_name", Args: args})
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"metiq/internal/plugins/sdk"
)

// GojaPlugin is a compiled, host-wired Goja plugin instance.
// Each plugin gets its own VM; there is no shared global state between plugins.
type GojaPlugin struct {
	manifest sdk.Manifest
	vm       *goja.Runtime
	invokeFn goja.Callable
	log      *slog.Logger
	hostCtx  *pluginHostContext
	mu       sync.Mutex
}

// Manifest returns the plugin's declared manifest.
func (p *GojaPlugin) Manifest() sdk.Manifest { return p.manifest }

// Invoke calls exports.invoke(toolName, args, ctx) inside the Goja VM.
// The call is run synchronously; async/Promise results are awaited via the
// Goja event-loop helper.
func (p *GojaPlugin) Invoke(ctx context.Context, req sdk.InvokeRequest) (sdk.InvokeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return sdk.InvokeResult{}, err
	}

	argsVal := p.vm.ToValue(req.Args)
	metaVal := p.vm.ToValue(req.Meta)

	if p.hostCtx != nil {
		prev := p.hostCtx.ctx
		p.hostCtx.ctx = ctx
		defer func() { p.hostCtx.ctx = prev }()
	}

	interruptStop := make(chan struct{})
	defer close(interruptStop)
	go func() {
		select {
		case <-ctx.Done():
			p.vm.Interrupt("context cancelled")
		case <-interruptStop:
		}
	}()

	val, err := p.invokeFn(goja.Undefined(), p.vm.ToValue(req.Tool), argsVal, metaVal)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return sdk.InvokeResult{}, cerr
		}
		wrapped := wrapPluginExecutionError("plugin invoke", err)
		return sdk.InvokeResult{Error: wrapped.Error()}, wrapped
	}

	// Unwrap promise if the function is async.
	if promise, ok := val.Export().(*goja.Promise); ok {
		switch promise.State() {
		case goja.PromiseStatePending:
			// We can't await without an event loop; return an error.
			err := fmt.Errorf("plugin returned a pending Promise; async plugins require an event loop")
			return sdk.InvokeResult{Error: err.Error()}, err
		case goja.PromiseStateRejected:
			err := fmt.Errorf("plugin promise rejected: %v", promise.Result())
			return sdk.InvokeResult{Error: err.Error()}, err
		case goja.PromiseStateFulfilled:
			val = promise.Result()
		}
	}

	if err := ctx.Err(); err != nil {
		return sdk.InvokeResult{}, err
	}

	return sdk.InvokeResult{Value: val.Export()}, nil
}

// ─── Loader ───────────────────────────────────────────────────────────────────

// LoadPlugin compiles src as a CommonJS-style module, wires the host APIs into
// the VM global scope, reads exports.manifest, and returns a ready GojaPlugin.
func LoadPlugin(ctx context.Context, src []byte, host *sdk.Host) (*GojaPlugin, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if host == nil {
		host = &sdk.Host{}
	}
	hostCtx := &pluginHostContext{ctx: context.Background()}
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	log := slog.Default().With("component", "goja-plugin")

	// ── Wire host namespaces ──────────────────────────────────────────────────
	if err := wireNostr(vm, host.Nostr, hostCtx, log); err != nil {
		return nil, fmt.Errorf("wire nostr: %w", err)
	}
	if err := wireConfig(vm, host.Config); err != nil {
		return nil, fmt.Errorf("wire config: %w", err)
	}
	if err := wireHTTP(vm, host.HTTP, hostCtx, log); err != nil {
		return nil, fmt.Errorf("wire http: %w", err)
	}
	if err := wireStorage(vm, host.Storage, hostCtx, log); err != nil {
		return nil, fmt.Errorf("wire storage: %w", err)
	}
	if err := wireLog(vm, host.Log); err != nil {
		return nil, fmt.Errorf("wire log: %w", err)
	}
	if err := wireAgent(vm, host.Agent, hostCtx, log); err != nil {
		return nil, fmt.Errorf("wire agent: %w", err)
	}

	// ── CommonJS module wrapper ───────────────────────────────────────────────
	// Wrap in a minimal CommonJS shim so plugins can use exports.xxx = ...
	wrapped := wrapModule(string(src))

	// ── Compile + run ─────────────────────────────────────────────────────────
	prog, err := goja.Compile("plugin", wrapped, false)
	if err != nil {
		return nil, fmt.Errorf("compile plugin: %w", err)
	}
	exports := vm.NewObject()
	if err := vm.Set("exports", exports); err != nil {
		return nil, fmt.Errorf("set exports: %w", err)
	}
	// Add require shim (no-op for host modules; plugins use host.* globals).
	if err := vm.Set("require", func(call goja.FunctionCall) goja.Value {
		mod := call.Argument(0).String()
		return vm.ToValue(map[string]any{"_module": mod})
	}); err != nil {
		return nil, fmt.Errorf("set require: %w", err)
	}

	if _, err := vm.RunProgram(prog); err != nil {
		return nil, wrapPluginExecutionError("run plugin", err)
	}

	// ── Read manifest ─────────────────────────────────────────────────────────
	manifestVal := exports.Get("manifest")
	if manifestVal == nil || goja.IsUndefined(manifestVal) || goja.IsNull(manifestVal) {
		return nil, fmt.Errorf("plugin missing exports.manifest")
	}
	manifestJSON, err := json.Marshal(manifestVal.Export())
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	var manifest sdk.Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	if err := sdk.ValidateManifest(manifest); err != nil {
		return nil, err
	}

	// ── Read invoke function ──────────────────────────────────────────────────
	invokeVal := exports.Get("invoke")
	if invokeVal == nil || goja.IsUndefined(invokeVal) || goja.IsNull(invokeVal) {
		return nil, fmt.Errorf("plugin missing exports.invoke")
	}
	invokeFn, ok := goja.AssertFunction(invokeVal)
	if !ok {
		return nil, fmt.Errorf("exports.invoke must be a function")
	}

	return &GojaPlugin{
		manifest: manifest,
		vm:       vm,
		invokeFn: invokeFn,
		log:      log.With("plugin", manifest.ID),
		hostCtx:  hostCtx,
	}, nil
}

// ─── Host wiring helpers ──────────────────────────────────────────────────────

func wireNostr(vm *goja.Runtime, h sdk.NostrHost, hostCtx *pluginHostContext, log *slog.Logger) error {
	if h == nil {
		return setUnavailableNamespace(vm, "nostr", "nostr host not available")
	}
	obj := vm.NewObject()
	_ = obj.Set("publish", func(call goja.FunctionCall) goja.Value {
		evt, ok := call.Argument(0).Export().(map[string]any)
		if !ok {
			panic(vm.NewTypeError("nostr.publish: argument must be an object"))
		}
		ctx, cancel := hostCtx.withTimeout(10 * time.Second)
		defer cancel()
		if err := h.Publish(ctx, evt); err != nil {
			log.Warn("nostr.publish failed", "err", err)
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	_ = obj.Set("fetch", func(call goja.FunctionCall) goja.Value {
		filter, ok := call.Argument(0).Export().(map[string]any)
		if !ok {
			panic(vm.NewTypeError("nostr.fetch: first argument must be a filter object"))
		}
		limit := 20
		if l, ok := call.Argument(1).Export().(int64); ok {
			limit = int(l)
		}
		ctx, cancel := hostCtx.withTimeout(15 * time.Second)
		defer cancel()
		events, err := h.Fetch(ctx, filter, limit)
		if err != nil {
			log.Warn("nostr.fetch failed", "err", err)
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(events)
	})
	_ = obj.Set("encrypt", func(call goja.FunctionCall) goja.Value {
		pubkey := call.Argument(0).String()
		content := call.Argument(1).String()
		ctx, cancel := hostCtx.withTimeout(5 * time.Second)
		defer cancel()
		cipher, err := h.Encrypt(ctx, pubkey, content)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(cipher)
	})
	_ = obj.Set("decrypt", func(call goja.FunctionCall) goja.Value {
		pubkey := call.Argument(0).String()
		cipher := call.Argument(1).String()
		ctx, cancel := hostCtx.withTimeout(5 * time.Second)
		defer cancel()
		plain, err := h.Decrypt(ctx, pubkey, cipher)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(plain)
	})
	return vm.Set("nostr", obj)
}

func wireConfig(vm *goja.Runtime, h sdk.ConfigHost) error {
	if h == nil {
		return setUnavailableNamespace(vm, "config", "config host not available")
	}
	obj := vm.NewObject()
	_ = obj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := h.Get(key)
		if val == nil {
			return goja.Null()
		}
		return vm.ToValue(val)
	})
	return vm.Set("config", obj)
}

func wireHTTP(vm *goja.Runtime, h sdk.HTTPHost, hostCtx *pluginHostContext, log *slog.Logger) error {
	if h == nil {
		return setUnavailableNamespace(vm, "http", "http host not available")
	}
	obj := vm.NewObject()
	_ = obj.Set("get", func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()
		headers := exportStringMap(call.Argument(1))
		ctx, cancel := hostCtx.withTimeout(30 * time.Second)
		defer cancel()
		status, body, err := h.Get(ctx, url, headers)
		if err != nil {
			log.Warn("http.get failed", "url", url, "err", err)
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(map[string]any{"status": status, "body": string(body)})
	})
	_ = obj.Set("post", func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()
		bodyStr := call.Argument(1).String()
		headers := exportStringMap(call.Argument(2))
		ctx, cancel := hostCtx.withTimeout(30 * time.Second)
		defer cancel()
		status, body, err := h.Post(ctx, url, []byte(bodyStr), headers)
		if err != nil {
			log.Warn("http.post failed", "url", url, "err", err)
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(map[string]any{"status": status, "body": string(body)})
	})
	return vm.Set("http", obj)
}

func wireStorage(vm *goja.Runtime, h sdk.StorageHost, hostCtx *pluginHostContext, log *slog.Logger) error {
	if h == nil {
		return setUnavailableNamespace(vm, "storage", "storage host not available")
	}
	obj := vm.NewObject()
	_ = obj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		ctx, cancel := hostCtx.withTimeout(5 * time.Second)
		defer cancel()
		val, err := h.Get(ctx, key)
		if err != nil {
			log.Warn("storage.get failed", "key", key, "err", err)
			return goja.Null()
		}
		if val == nil {
			return goja.Null()
		}
		return vm.ToValue(string(val))
	})
	_ = obj.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		ctx, cancel := hostCtx.withTimeout(5 * time.Second)
		defer cancel()
		if err := h.Set(ctx, key, []byte(val)); err != nil {
			log.Warn("storage.set failed", "key", key, "err", err)
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	_ = obj.Set("del", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		ctx, cancel := hostCtx.withTimeout(5 * time.Second)
		defer cancel()
		if err := h.Del(ctx, key); err != nil {
			log.Warn("storage.del failed", "key", key, "err", err)
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	return vm.Set("storage", obj)
}

func wireLog(vm *goja.Runtime, h sdk.LogHost) error {
	if h == nil {
		h = &defaultLogHost{}
	}
	obj := vm.NewObject()
	_ = obj.Set("info", func(call goja.FunctionCall) goja.Value {
		h.Info(joinArgs(call.Arguments))
		return goja.Undefined()
	})
	_ = obj.Set("warn", func(call goja.FunctionCall) goja.Value {
		h.Warn(joinArgs(call.Arguments))
		return goja.Undefined()
	})
	_ = obj.Set("error", func(call goja.FunctionCall) goja.Value {
		h.Error(joinArgs(call.Arguments))
		return goja.Undefined()
	})
	return vm.Set("log", obj)
}

func wireAgent(vm *goja.Runtime, h sdk.AgentHost, hostCtx *pluginHostContext, log *slog.Logger) error {
	if h == nil {
		return setUnavailableNamespace(vm, "agent", "agent host not available")
	}
	obj := vm.NewObject()
	_ = obj.Set("complete", func(call goja.FunctionCall) goja.Value {
		prompt := call.Argument(0).String()
		var opts sdk.CompletionOpts
		if raw, ok := call.Argument(1).Export().(map[string]any); ok {
			if model, ok := raw["model"].(string); ok {
				opts.Model = model
			}
			if sys, ok := raw["system_prompt"].(string); ok {
				opts.SystemPrompt = sys
			}
			if mt, ok := raw["max_tokens"].(int64); ok {
				opts.MaxTokens = int(mt)
			}
		}
		ctx, cancel := hostCtx.withTimeout(60 * time.Second)
		defer cancel()
		reply, err := h.Complete(ctx, prompt, opts)
		if err != nil {
			log.Warn("agent.complete failed", "err", err)
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(reply)
	})
	return vm.Set("agent", obj)
}

// ─── Utility ──────────────────────────────────────────────────────────────────

type pluginHostContext struct {
	ctx context.Context
}

func (h *pluginHostContext) withTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	base := context.Background()
	if h != nil && h.ctx != nil {
		base = h.ctx
	}
	return context.WithTimeout(base, timeout)
}

// HostUnavailableError reports that plugin code attempted to use a host
// namespace the daemon did not wire for this plugin/runtime.
type HostUnavailableError struct {
	Namespace string
}

func (e *HostUnavailableError) Error() string {
	return fmt.Sprintf("plugin host namespace %q is not available", e.Namespace)
}

func setUnavailableNamespace(vm *goja.Runtime, name, reason string) error {
	obj := vm.NewObject()
	_ = obj.Set("available", false)
	_ = obj.Set("reason", reason)
	unavailable := func(call goja.FunctionCall) goja.Value {
		panic(vm.NewGoError(&HostUnavailableError{Namespace: name}))
	}
	for _, fn := range []string{"publish", "fetch", "encrypt", "decrypt", "get", "set", "del", "post", "complete"} {
		_ = obj.Set(fn, unavailable)
	}
	return vm.Set(name, obj)
}

func wrapPluginExecutionError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	var unavailable *HostUnavailableError
	if errors.As(err, &unavailable) && unavailable != nil {
		return fmt.Errorf("%s: %w", prefix, unavailable)
	}
	if namespace := missingHostNamespaceFromError(err); namespace != "" {
		return fmt.Errorf("%s: %w: %v", prefix, &HostUnavailableError{Namespace: namespace}, err)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func missingHostNamespaceFromError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	for _, namespace := range []string{"nostr", "config", "http", "storage", "agent"} {
		if strings.Contains(msg, "ReferenceError: "+namespace+" is not defined") || strings.Contains(msg, namespace+" is not defined") {
			return namespace
		}
		if strings.Contains(msg, "plugin host namespace \""+namespace+"\" is not available") {
			return namespace
		}
		if strings.Contains(lower, "typeerror") && (strings.Contains(msg, namespace+".") || strings.Contains(msg, namespace+"[") || strings.Contains(msg, "'"+namespace+"'") || strings.Contains(msg, "\""+namespace+"\"")) {
			return namespace
		}
	}
	return ""
}

func exportStringMap(v goja.Value) map[string]string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	raw, ok := v.Export().(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}

func joinArgs(args []goja.Value) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return strings.Join(parts, " ")
}

func wrapModule(src string) string {
	return "(function(exports, require, module){\n" + src + "\n})(exports, require, {exports: exports});"
}

type defaultLogHost struct{}

func (l *defaultLogHost) Info(msg string, args ...any)  { slog.Info(msg, args...) }
func (l *defaultLogHost) Warn(msg string, args ...any)  { slog.Warn(msg, args...) }
func (l *defaultLogHost) Error(msg string, args ...any) { slog.Error(msg, args...) }
