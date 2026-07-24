// Package runtime - Node.js subprocess plugin host.
//
// NodePlugin runs a plugin in a Node.js subprocess via the embedded shim.
// The protocol is line-delimited JSON-RPC over stdin/stdout.
//
// Usage:
//
//	p, err := LoadNodePlugin(ctx, "/path/to/plugin", manifest)
//	result, err := p.Invoke(ctx, sdk.InvokeRequest{Tool: "my_tool", Args: args})
//	p.Close()
//
// The bridge is off by default. It is activated when:
//   - IsNodePlugin(installPath) returns true (node_modules directory detected)
//   - OR the plugin entry has plugin_type = "node" or "nodejs"

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

	"metiq/internal/plugins/sdk"
	"metiq/internal/sandbox"
)

//go:embed node_shim.js
var nodeShimJS []byte

// NodePlugin manages a running Node.js subprocess hosting a plugin.
type NodePlugin struct {
	manifest     sdk.Manifest
	capabilities []NodeCapability
	proc         *exec.Cmd
	sandboxProc  *sandbox.InteractiveProcess
	stdin        io.WriteCloser
	mu           sync.Mutex
	pending      map[int64]chan nodeResponse
	nextID       atomic.Int64
	closed       bool
}

// NodeCapability describes a media/search/memory provider registered by a
// Node.js plugin through the shim (speech, transcription, voice, image/video/
// music generation, web fetch/search, memory embedding). Descriptors are
// captured at init time and their handler methods are invokable via
// InvokeProvider — replacing the former silent no-op registration behaviour.
type NodeCapability struct {
	// Type is the capability namespace, e.g. "speech_provider",
	// "image_gen_provider", "web_search_provider", "memory_embedding_provider".
	Type string `json:"type"`
	// ID is the provider identifier used to route InvokeProvider calls.
	ID string `json:"id"`
	// Name is a human-readable label (defaults to ID).
	Name string `json:"name,omitempty"`
	// Description is an optional human-readable description.
	Description string `json:"description,omitempty"`
	// Methods lists the invokable handler method names on the provider.
	Methods []string `json:"methods,omitempty"`
}

type nodeRequest struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type nodeResponse struct {
	ID     int64  `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// IsNodePlugin returns true when the install path looks like a Node.js plugin
// (contains a node_modules directory or a package.json with native requires).
func IsNodePlugin(installPath string) bool {
	installPath = strings.TrimSpace(installPath)
	if installPath == "" {
		return false
	}
	nodeModules := filepath.Join(installPath, "node_modules")
	if _, err := os.Stat(nodeModules); err == nil {
		return true
	}
	return false
}

// LoadNodePlugin starts a Node.js subprocess for the plugin at installPath,
// initialises it via the shim protocol, and returns a ready NodePlugin.
// Returns an error if Node.js is not in PATH.
func LoadNodePlugin(ctx context.Context, installPath string) (*NodePlugin, error) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("node.js not found in PATH (required for node plugins): %w", err)
	}

	// Write the embedded shim to a temp file.
	shimFile, err := os.CreateTemp("", "metiq-node-shim-*.js")
	if err != nil {
		return nil, fmt.Errorf("create shim tempfile: %w", err)
	}
	shimPath := shimFile.Name()
	if _, err := shimFile.Write(nodeShimJS); err != nil {
		shimFile.Close()
		os.Remove(shimPath)
		return nil, fmt.Errorf("write shim: %w", err)
	}
	shimFile.Close()

	cmd := exec.CommandContext(ctx, nodeBin, shimPath)
	cmd.Stderr = os.Stderr // forward Node.js errors to daemon stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.Remove(shimPath)
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(shimPath)
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		os.Remove(shimPath)
		return nil, fmt.Errorf("start node process: %w", err)
	}

	p := &NodePlugin{
		proc:    cmd,
		stdin:   stdin,
		pending: map[int64]chan nodeResponse{},
	}

	absPath, err := filepath.Abs(filepath.Clean(installPath))
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("resolve install path: %w", err)
	}
	return initializeNodePlugin(ctx, p, stdout, shimPath, absPath)
}

// LoadNodePluginSandboxed starts a Node plugin through an interactive Docker
// sandbox. installRoot is mounted read-only by the manager and entryPath is
// translated to the corresponding in-container path.
func LoadNodePluginSandboxed(ctx context.Context, installRoot, entryPath, containerRoot string, runner sandbox.InteractiveSandboxRunner) (*NodePlugin, error) {
	if runner == nil || runner.Driver() != "docker" {
		return nil, fmt.Errorf("node plugin requires an interactive docker sandbox")
	}
	root, err := filepath.EvalSymlinks(installRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin root: %w", err)
	}
	entry, err := filepath.EvalSymlinks(entryPath)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin entry: %w", err)
	}
	rel, err := filepath.Rel(root, entry)
	if err != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return nil, fmt.Errorf("node plugin entry escapes install root")
	}
	containerEntry := filepath.ToSlash(filepath.Join(containerRoot, rel))
	proc, err := runner.StartInteractive(ctx, []string{"node", "-e", string(nodeShimJS)}, nil, containerRoot)
	if err != nil {
		return nil, fmt.Errorf("start sandboxed node process: %w", err)
	}
	p := &NodePlugin{sandboxProc: proc, stdin: proc.Stdin, pending: map[int64]chan nodeResponse{}}
	go func() { _, _ = io.Copy(os.Stderr, proc.Stderr) }()
	return initializeNodePlugin(ctx, p, proc.Stdout, "", containerEntry)
}

func initializeNodePlugin(ctx context.Context, p *NodePlugin, stdout io.Reader, shimPath, pluginPath string) (*NodePlugin, error) {
	go p.readLoop(stdout, shimPath)
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := p.call(initCtx, "init", map[string]any{"plugin_path": pluginPath})
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("node plugin init: %w", err)
	}
	if resultMap, ok := resp.(map[string]any); ok {
		if mRaw, ok := resultMap["manifest"]; ok {
			b, err := json.Marshal(mRaw)
			if err != nil {
				p.Close()
				return nil, fmt.Errorf("marshal node manifest: %w", err)
			}
			if err := json.Unmarshal(b, &p.manifest); err != nil {
				p.Close()
				return nil, fmt.Errorf("unmarshal node manifest: %w", err)
			}
		}
		p.capabilities = parseNodeCapabilities(resultMap["capabilities"])
	}
	if strings.TrimSpace(p.manifest.ID) == "" {
		base := filepath.Base(pluginPath)
		if base == "." || base == string(filepath.Separator) {
			base = "node-plugin"
		}
		p.manifest.ID = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if err := sdk.ValidateManifest(p.manifest); err != nil {
		p.Close()
		return nil, err
	}
	if _, err := p.call(initCtx, "set_permissions", map[string]any{"permissions": p.manifest.Permissions}); err != nil {
		p.Close()
		return nil, fmt.Errorf("node plugin permission setup: %w", err)
	}
	return p, nil
}

// Manifest returns the plugin's declared tools.
func (p *NodePlugin) Manifest() sdk.Manifest { return p.manifest }

// Capabilities returns a snapshot of the media/search/memory provider
// registrations captured from the plugin at init time.
func (p *NodePlugin) Capabilities() []NodeCapability {
	out := make([]NodeCapability, len(p.capabilities))
	copy(out, p.capabilities)
	return out
}

// InvokeProvider routes a call to a registered capability provider's handler
// method and returns its result. capType and id must match a NodeCapability
// reported by Capabilities(); method must be one of that capability's Methods.
// It returns an error (rather than silently succeeding) when the provider or
// method is not registered.
func (p *NodePlugin) InvokeProvider(ctx context.Context, capType, id, method string, args map[string]any) (any, error) {
	return p.call(ctx, "invoke_provider", map[string]any{
		"type":        capType,
		"provider_id": id,
		"method":      method,
		"args":        args,
	})
}

// parseNodeCapabilities decodes the "capabilities" array reported in the shim's
// init response into typed NodeCapability descriptors. Entries missing a type or
// id are skipped. It is a pure function so the host contract can be tested
// deterministically without a Node.js runtime.
func parseNodeCapabilities(raw any) []NodeCapability {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	caps := make([]NodeCapability, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		var c NodeCapability
		if err := json.Unmarshal(b, &c); err != nil {
			continue
		}
		if strings.TrimSpace(c.Type) == "" || strings.TrimSpace(c.ID) == "" {
			continue
		}
		caps = append(caps, c)
	}
	return caps
}

// Invoke calls a tool in the Node.js plugin and waits for the result.
func (p *NodePlugin) Invoke(ctx context.Context, req sdk.InvokeRequest) (sdk.InvokeResult, error) {
	result, err := p.call(ctx, "invoke", map[string]any{
		"tool": req.Tool,
		"args": req.Args,
	})
	if err != nil {
		return sdk.InvokeResult{}, err
	}
	return sdk.InvokeResult{Value: result}, nil
}

// Close shuts down the subprocess gracefully.
func (p *NodePlugin) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	// Send shutdown and close stdin; process will self-exit.
	// The closed flag intentionally prevents new calls. The shim also exits when
	// stdin closes, so shutdown remains bounded even if the RPC cannot be sent.
	_ = p.stdin.Close()
	if p.sandboxProc != nil {
		done := make(chan struct{})
		go func() { _ = p.sandboxProc.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = p.sandboxProc.Kill()
			<-done
		}
	} else if p.proc != nil {
		done := make(chan struct{})
		go func() { _ = p.proc.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = p.proc.Process.Kill()
			<-done
		}
	}
}

// call sends a JSON-RPC request and waits for the matching response.
func (p *NodePlugin) call(ctx context.Context, method string, params map[string]any) (any, error) {
	id := p.nextID.Add(1)
	ch := make(chan nodeResponse, 1)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("node plugin is closed")
	}
	p.pending[id] = ch
	p.mu.Unlock()

	req := nodeRequest{ID: id, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	line = append(line, '\n')

	p.mu.Lock()
	_, writeErr := p.stdin.Write(line)
	p.mu.Unlock()
	if writeErr != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("write to node stdin: %w", writeErr)
	}

	select {
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != "" {
			return nil, fmt.Errorf("node plugin error: %s", resp.Error)
		}
		return resp.Result, nil
	}
}

// readLoop reads JSON-RPC responses from the subprocess stdout and dispatches them.
func (p *NodePlugin) readLoop(r io.Reader, shimPath string) {
	defer os.Remove(shimPath)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var resp nodeResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		p.mu.Lock()
		ch, ok := p.pending[resp.ID]
		if ok {
			delete(p.pending, resp.ID)
		}
		p.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
	// Process exited — fail all pending calls.
	p.mu.Lock()
	for id, ch := range p.pending {
		ch <- nodeResponse{ID: id, Error: "node process exited"}
		delete(p.pending, id)
	}
	p.mu.Unlock()
}
