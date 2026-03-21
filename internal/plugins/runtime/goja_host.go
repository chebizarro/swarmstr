// Package runtime implements the Goja (ES2015+) plugin host for Swarmstr.
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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	mu       sync.Mutex
}

// Manifest returns the plugin's declared manifest.
func (p *GojaPlugin) Manifest() sdk.Manifest { return p.manifest }

// Invoke calls exports.invoke(toolName, args, ctx) inside the Goja VM.
// The call is run synchronously; async/Promise results are awaited via the
// Goja event-loop helper.
func (p *GojaPlugin) Invoke(ctx context.Context, req sdk.InvokeRequest) (sdk.InvokeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return sdk.InvokeResult{}, err
	}

	argsVal := p.vm.ToValue(req.Args)
	metaVal := p.vm.ToValue(req.Meta)

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
		wrapped := fmt.Errorf("plugin invoke: %w", err)
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
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	log := slog.Default().With("component", "goja-plugin")

	// ── Wire host namespaces ──────────────────────────────────────────────────
	if err := wireNostr(vm, host.Nostr, log); err != nil {
		return nil, fmt.Errorf("wire nostr: %w", err)
	}
	if err := wireConfig(vm, host.Config); err != nil {
		return nil, fmt.Errorf("wire config: %w", err)
	}
	if err := wireHTTP(vm, host.HTTP, log); err != nil {
		return nil, fmt.Errorf("wire http: %w", err)
	}
	if err := wireStorage(vm, host.Storage, log); err != nil {
		return nil, fmt.Errorf("wire storage: %w", err)
	}
	if err := wireLog(vm, host.Log); err != nil {
		return nil, fmt.Errorf("wire log: %w", err)
	}
	if err := wireAgent(vm, host.Agent, log); err != nil {
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
		return nil, fmt.Errorf("run plugin: %w", err)
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
	if manifest.ID == "" {
		return nil, fmt.Errorf("manifest.id is required")
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
	}, nil
}

// ─── Host wiring helpers ──────────────────────────────────────────────────────

func wireNostr(vm *goja.Runtime, h sdk.NostrHost, log *slog.Logger) error {
	if h == nil {
		return setStubNamespace(vm, "nostr", "nostr host not available")
	}
	obj := vm.NewObject()
	_ = obj.Set("publish", func(call goja.FunctionCall) goja.Value {
		evt, ok := call.Argument(0).Export().(map[string]any)
		if !ok {
			panic(vm.NewTypeError("nostr.publish: argument must be an object"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		return setStubNamespace(vm, "config", "config host not available")
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

func wireHTTP(vm *goja.Runtime, h sdk.HTTPHost, log *slog.Logger) error {
	if h == nil {
		// Use stdlib fallback.
		h = &stdlibHTTPHost{}
	}
	obj := vm.NewObject()
	_ = obj.Set("get", func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()
		headers := exportStringMap(call.Argument(1))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

func wireStorage(vm *goja.Runtime, h sdk.StorageHost, log *slog.Logger) error {
	if h == nil {
		return setStubNamespace(vm, "storage", "storage host not available")
	}
	obj := vm.NewObject()
	_ = obj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Set(ctx, key, []byte(val)); err != nil {
			log.Warn("storage.set failed", "key", key, "err", err)
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	_ = obj.Set("del", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func wireAgent(vm *goja.Runtime, h sdk.AgentHost, log *slog.Logger) error {
	if h == nil {
		return setStubNamespace(vm, "agent", "agent host not available")
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
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

func setStubNamespace(vm *goja.Runtime, name, reason string) error {
	obj := vm.NewObject()
	stub := func(call goja.FunctionCall) goja.Value {
		panic(vm.NewTypeError("%s: %s", name, reason))
	}
	// Attach a catch-all __noSuchMethod__ isn't standard; instead set common names.
	for _, fn := range []string{"publish", "fetch", "encrypt", "decrypt",
		"get", "set", "del", "post", "complete",
		"info", "warn", "error"} {
		_ = obj.Set(fn, stub)
	}
	return vm.Set(name, obj)
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

// ─── stdlib fallback implementations ─────────────────────────────────────────

type stdlibHTTPHost struct{}

func (h *stdlibHTTPHost) Get(ctx context.Context, url string, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

func (h *stdlibHTTPHost) Post(ctx context.Context, url string, body []byte, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

type defaultLogHost struct{}

func (l *defaultLogHost) Info(msg string, args ...any)  { slog.Info(msg, args...) }
func (l *defaultLogHost) Warn(msg string, args ...any)  { slog.Warn(msg, args...) }
func (l *defaultLogHost) Error(msg string, args ...any) { slog.Error(msg, args...) }
