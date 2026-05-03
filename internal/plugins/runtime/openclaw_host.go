package runtime

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed openclaw_shim.js
var openClawShimJS []byte

//go:embed openclaw_api.js
var openClawAPIJS []byte

// OpenClawPluginHost manages a Node.js subprocess running OpenClaw-format
// plugins. Communication is line-delimited JSON over stdin/stdout.
type OpenClawPluginHost struct {
	proc   *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader

	tempDir string

	mu        sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	pending   map[int64]chan *RPCResponse
	nextID    atomic.Int64
	closed    bool
	closing   bool
	closeErr  error
	done      chan struct{}

	registrations []CapabilityRegistration
	tools         map[string]*RegisteredTool
	providers     map[string]*RegisteredProvider
	channels      map[string]*RegisteredChannel
	hooks         map[string][]*RegisteredHook
	services      map[string]*RegisteredService
	commands      map[string]*RegisteredCommand
	capabilities  map[string][]*CapabilityRegistration
}

// NewOpenClawPluginHost starts a Node.js subprocess with the embedded OpenClaw
// shim and API implementation.
func NewOpenClawPluginHost(ctx context.Context) (*OpenClawPluginHost, error) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("node.js not found in PATH (required for OpenClaw plugins): %w", err)
	}

	tempDir, err := os.MkdirTemp("", "metiq-openclaw-host-*")
	if err != nil {
		return nil, fmt.Errorf("create OpenClaw shim temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }

	shimPath := filepath.Join(tempDir, "openclaw_shim.js")
	apiPath := filepath.Join(tempDir, "openclaw_api.js")
	if err := os.WriteFile(shimPath, openClawShimJS, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("write OpenClaw shim: %w", err)
	}
	if err := os.WriteFile(apiPath, openClawAPIJS, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("write OpenClaw api shim: %w", err)
	}

	cmd := exec.CommandContext(ctx, nodeBin, shimPath)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("start node process: %w", err)
	}

	h := &OpenClawPluginHost{
		proc:         cmd,
		stdin:        stdin,
		stdout:       stdout,
		tempDir:      tempDir,
		pending:      map[int64]chan *RPCResponse{},
		done:         make(chan struct{}),
		tools:        map[string]*RegisteredTool{},
		providers:    map[string]*RegisteredProvider{},
		channels:     map[string]*RegisteredChannel{},
		hooks:        map[string][]*RegisteredHook{},
		services:     map[string]*RegisteredService{},
		commands:     map[string]*RegisteredCommand{},
		capabilities: map[string][]*CapabilityRegistration{},
	}
	go h.readLoop(stdout)
	return h, nil
}

// LoadPlugin loads an OpenClaw plugin entry from a file or directory path and
// indexes every registration captured during its register(api) callback.
func (h *OpenClawPluginHost) LoadPlugin(ctx context.Context, pluginPath string) error {
	result, err := h.LoadPluginResult(ctx, pluginPath, nil)
	if err != nil {
		return err
	}
	h.processRegistrations(result.PluginID, result.Registrations)
	return nil
}

// LoadPluginResult is like LoadPlugin but returns the shim load metadata.
func (h *OpenClawPluginHost) LoadPluginResult(ctx context.Context, pluginPath string, config map[string]any) (OpenClawLoadResult, error) {
	absPath, err := filepath.Abs(filepath.Clean(pluginPath))
	if err != nil {
		return OpenClawLoadResult{}, fmt.Errorf("resolve plugin path: %w", err)
	}
	params := map[string]any{"plugin_path": absPath}
	if config != nil {
		params["config"] = config
	}
	resp, err := h.call(ctx, "load_plugin", params)
	if err != nil {
		return OpenClawLoadResult{}, err
	}
	var result OpenClawLoadResult
	if err := decodeRPCResult(resp.Result, &result); err != nil {
		return OpenClawLoadResult{}, fmt.Errorf("decode load result: %w", err)
	}
	return result, nil
}

// InitPlugin invokes an optional plugin init/initialize lifecycle function.
func (h *OpenClawPluginHost) InitPlugin(ctx context.Context, pluginID string, params any) (any, error) {
	resp, err := h.call(ctx, "init_plugin", map[string]any{"plugin_id": pluginID, "params": params})
	if err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// InvokeTool executes a plugin-registered OpenClaw tool.
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

// InvokeHook calls every registered handler for event in priority order.
func (h *OpenClawPluginHost) InvokeHook(ctx context.Context, event string, payload any) ([]HookResult, error) {
	resp, err := h.call(ctx, "invoke_hook", map[string]any{"event": event, "payload": payload})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Results []HookResult `json:"results"`
	}
	if err := decodeRPCResult(resp.Result, &envelope); err != nil {
		var direct []HookResult
		if directErr := decodeRPCResult(resp.Result, &direct); directErr != nil {
			return nil, fmt.Errorf("decode hook results: %w", err)
		}
		return direct, nil
	}
	return envelope.Results, nil
}

// InvokeProvider calls a provider method such as catalog or auth.
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

// StartService invokes a registered service start hook when present.
func (h *OpenClawPluginHost) StartService(ctx context.Context, serviceID string, params any) (any, error) {
	resp, err := h.call(ctx, "start_service", map[string]any{"service_id": serviceID, "params": params})
	if err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// StopService invokes a registered service stop hook when present.
func (h *OpenClawPluginHost) StopService(ctx context.Context, serviceID string, params any) (any, error) {
	resp, err := h.call(ctx, "stop_service", map[string]any{"service_id": serviceID, "params": params})
	if err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// Close gracefully shuts down the Node.js subprocess and cleans temp files.
func (h *OpenClawPluginHost) Close() error {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		alreadyClosed := h.closed
		if !alreadyClosed {
			h.closing = true
		}
		h.mu.Unlock()

		var callErr error
		if !alreadyClosed {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, callErr = h.call(ctx, "shutdown", nil)
			cancel()
		}

		h.mu.Lock()
		h.closed = true
		h.mu.Unlock()
		_ = h.stdin.Close()

		waitErr := h.proc.Wait()
		<-h.done
		_ = os.RemoveAll(h.tempDir)
		if callErr != nil && !strings.Contains(callErr.Error(), "process exited") {
			h.closeErr = callErr
			return
		}
		h.closeErr = waitErr
	})
	return h.closeErr
}

// Registrations returns a snapshot of all captured registrations.
func (h *OpenClawPluginHost) Registrations() []CapabilityRegistration {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]CapabilityRegistration, len(h.registrations))
	copy(out, h.registrations)
	return out
}

func (h *OpenClawPluginHost) Tools() map[string]*RegisteredTool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]*RegisteredTool, len(h.tools))
	for k, v := range h.tools {
		cp := *v
		out[k] = &cp
	}
	return out
}

func (h *OpenClawPluginHost) Providers() map[string]*RegisteredProvider {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]*RegisteredProvider, len(h.providers))
	for k, v := range h.providers {
		cp := *v
		out[k] = &cp
	}
	return out
}

func (h *OpenClawPluginHost) call(ctx context.Context, method string, params any) (*RPCResponse, error) {
	id := h.nextID.Add(1)
	ch := make(chan *RPCResponse, 1)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, fmt.Errorf("openclaw plugin host is closed")
	}
	if h.closing && method != "shutdown" {
		h.mu.Unlock()
		return nil, fmt.Errorf("openclaw plugin host is closing")
	}
	h.pending[id] = ch
	h.mu.Unlock()

	req := RPCRequest{ID: id, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		h.removePending(id)
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	line = append(line, '\n')

	h.writeMu.Lock()
	_, writeErr := h.stdin.Write(line)
	h.writeMu.Unlock()
	if writeErr != nil {
		h.removePending(id)
		return nil, fmt.Errorf("write to openclaw node stdin: %w", writeErr)
	}

	select {
	case <-ctx.Done():
		h.removePending(id)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != "" {
			return nil, fmt.Errorf("openclaw rpc %s: %s", method, resp.Error)
		}
		return resp, nil
	}
}

func (h *OpenClawPluginHost) removePending(id int64) {
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()
}

func (h *OpenClawPluginHost) readLoop(r io.Reader) {
	defer close(h.done)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var resp RPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			h.failAllPending(fmt.Sprintf("openclaw protocol violation: invalid JSON on stdout: %q", line))
			_ = h.proc.Process.Kill()
			return
		}
		h.mu.Lock()
		ch, ok := h.pending[resp.ID]
		if ok {
			delete(h.pending, resp.ID)
		}
		h.mu.Unlock()
		if ok {
			ch <- &resp
		}
	}

	if err := scanner.Err(); err != nil {
		h.failAllPending("openclaw stdout read error: " + err.Error())
		return
	}
	h.failAllPending("openclaw node process exited")
}

func (h *OpenClawPluginHost) failAllPending(message string) {
	h.mu.Lock()
	h.closed = true
	for id, ch := range h.pending {
		ch <- &RPCResponse{ID: id, Error: message}
		delete(h.pending, id)
	}
	h.mu.Unlock()
}

func (h *OpenClawPluginHost) processRegistrations(defaultPluginID string, regs []CapabilityRegistration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range regs {
		reg := regs[i]
		if reg.PluginID == "" {
			reg.PluginID = defaultPluginID
		}
		h.registrations = append(h.registrations, reg)
		regCopy := reg
		h.capabilities[reg.Type] = append(h.capabilities[reg.Type], &regCopy)

		switch reg.Type {
		case "tool":
			name := firstNonEmpty(reg.Name, stringFromRaw(reg.Raw, "name"))
			qualified := firstNonEmpty(reg.QualifiedName, stringFromRaw(reg.Raw, "qualifiedName"))
			if qualified == "" && reg.PluginID != "" && name != "" {
				qualified = reg.PluginID + "/" + name
			}
			h.tools[qualified] = &RegisteredTool{
				PluginID:      reg.PluginID,
				Name:          name,
				QualifiedName: qualified,
				Description:   firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")),
				Parameters:    reg.Raw["parameters"],
				OwnerOnly:     boolFromRaw(reg.Raw, "ownerOnly"),
				Optional:      boolFromRaw(reg.Raw, "optional"),
				Raw:           reg.Raw,
			}
		case "provider":
			id := firstNonEmpty(reg.ID, stringFromRaw(reg.Raw, "id"))
			h.providers[id] = &RegisteredProvider{
				PluginID:   reg.PluginID,
				ID:         id,
				Label:      firstNonEmpty(reg.Label, stringFromRaw(reg.Raw, "label")),
				DocsPath:   stringFromRaw(reg.Raw, "docsPath"),
				HasAuth:    boolFromRaw(reg.Raw, "hasAuth"),
				HasCatalog: boolFromRaw(reg.Raw, "hasCatalog"),
				Raw:        reg.Raw,
			}
		case "channel":
			id := firstNonEmpty(reg.ID, stringFromRaw(reg.Raw, "id"))
			h.channels[id] = &RegisteredChannel{PluginID: reg.PluginID, ID: id, ChannelType: stringFromRaw(reg.Raw, "channelType"), Raw: reg.Raw}
		case "hook":
			hook := &RegisteredHook{PluginID: reg.PluginID, HookID: firstNonEmpty(reg.HookID, stringFromRaw(reg.Raw, "hookId")), Events: reg.Events, Priority: reg.Priority, Raw: reg.Raw}
			for _, event := range reg.Events {
				h.hooks[event] = append(h.hooks[event], hook)
			}
		case "service":
			id := firstNonEmpty(reg.ID, stringFromRaw(reg.Raw, "id"))
			h.services[id] = &RegisteredService{PluginID: reg.PluginID, ID: id, Raw: reg.Raw}
		case "command":
			name := firstNonEmpty(reg.Name, stringFromRaw(reg.Raw, "name"))
			h.commands[name] = &RegisteredCommand{PluginID: reg.PluginID, Name: name, Description: firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")), AcceptsArgs: boolFromRaw(reg.Raw, "acceptsArgs"), Raw: reg.Raw}
		}
	}
}

func decodeRPCResult(result any, dest any) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func stringFromRaw(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	v, _ := raw[key].(string)
	return v
}

func boolFromRaw(raw map[string]any, key string) bool {
	if raw == nil {
		return false
	}
	v, _ := raw[key].(bool)
	return v
}
