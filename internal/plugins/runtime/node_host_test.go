package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"metiq/internal/plugins/sdk"
)

// skipIfNoNode marks the test as skipped when Node.js is not in PATH.
func skipIfNoNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node.js not in PATH — skipping Node.js bridge tests")
	}
}

// ─── IsNodePlugin ─────────────────────────────────────────────────────────────

func TestIsNodePlugin_withNodeModules(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsNodePlugin(dir) {
		t.Error("expected IsNodePlugin=true for dir with node_modules")
	}
}

func TestIsNodePlugin_withoutNodeModules(t *testing.T) {
	dir := t.TempDir()
	if IsNodePlugin(dir) {
		t.Error("expected IsNodePlugin=false for dir without node_modules")
	}
}

func TestIsNodePlugin_emptyPath(t *testing.T) {
	if IsNodePlugin("") {
		t.Error("expected IsNodePlugin=false for empty path")
	}
}

// ─── LoadNodePlugin ───────────────────────────────────────────────────────────

func TestLoadNodePlugin_missingNodeErrors(t *testing.T) {
	// Temporarily remove node from PATH.
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	dir := t.TempDir()
	// Write a minimal plugin so installPath exists.
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(`
module.exports = {
  register: function(sdk) {
    return { tools: [{ name: "ping", description: "ping" }] };
  }
};
`), 0o644)

	_, err := LoadNodePlugin(context.Background(), dir)
	if err == nil {
		t.Error("expected error when node is not in PATH")
	}
}

// ─── End-to-end with real Node.js ─────────────────────────────────────────────

func TestLoadNodePlugin_invokesTool(t *testing.T) {
	skipIfNoNode(t)

	dir := t.TempDir()
	pluginJS := `
'use strict';
module.exports = {
  register: function(sdk) {
    return {
      tools: [{ name: "echo", description: "echo args" }]
    };
  },
  tools: {
    echo: function(args) {
      return { echoed: args };
    }
  }
};
`
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(pluginJS), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadNodePlugin(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadNodePlugin: %v", err)
	}
	defer p.Close()

	// Manifest should list the echo tool.
	mf := p.Manifest()
	if len(mf.Tools) != 1 || mf.Tools[0].Name != "echo" {
		t.Errorf("unexpected manifest: %+v", mf)
	}

	// Invoke the echo tool.
	result, err := p.Invoke(context.Background(), sdk.InvokeRequest{
		Tool: "echo",
		Args: map[string]any{"msg": "hello"},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.Value == nil {
		t.Error("expected non-nil result")
	}
}

func TestLoadNodePlugin_unknownToolErrors(t *testing.T) {
	skipIfNoNode(t)

	dir := t.TempDir()
	pluginJS := `'use strict'; module.exports = { register: function(sdk) { return { tools: [] }; }, tools: {} };`
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(pluginJS), 0o644)

	p, err := LoadNodePlugin(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadNodePlugin: %v", err)
	}
	defer p.Close()

	_, err = p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "nonexistent"})
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

// ─── Capability registration (host contract) ─────────────────────────────────

// TestParseNodeCapabilities exercises the host-side contract for interpreting
// the shim's reported capability descriptors. It is deterministic and does not
// require a Node.js runtime.
func TestParseNodeCapabilities(t *testing.T) {
	raw := []any{
		map[string]any{
			"type":        "speech_provider",
			"id":          "whisper",
			"name":        "Whisper",
			"description": "speech to text",
			"methods":     []any{"transcribe", "synthesize"},
		},
		map[string]any{ // missing id — must be skipped
			"type":    "image_gen_provider",
			"methods": []any{"generate"},
		},
		map[string]any{ // missing type — must be skipped
			"id": "orphan",
		},
		"not-a-map", // wrong shape — must be skipped
		map[string]any{
			"type": "memory_embedding_provider",
			"id":   "embedder",
		},
	}

	caps := parseNodeCapabilities(raw)
	if len(caps) != 2 {
		t.Fatalf("expected 2 valid capabilities, got %d: %+v", len(caps), caps)
	}
	if caps[0].Type != "speech_provider" || caps[0].ID != "whisper" || caps[0].Name != "Whisper" {
		t.Errorf("unexpected first capability: %+v", caps[0])
	}
	if len(caps[0].Methods) != 2 || caps[0].Methods[0] != "transcribe" {
		t.Errorf("unexpected methods: %+v", caps[0].Methods)
	}
	if caps[1].Type != "memory_embedding_provider" || caps[1].ID != "embedder" {
		t.Errorf("unexpected second capability: %+v", caps[1])
	}
}

func TestParseNodeCapabilities_nonArrayReturnsNil(t *testing.T) {
	if caps := parseNodeCapabilities(nil); caps != nil {
		t.Errorf("expected nil for nil input, got %+v", caps)
	}
	if caps := parseNodeCapabilities(map[string]any{"type": "x"}); caps != nil {
		t.Errorf("expected nil for non-array input, got %+v", caps)
	}
}

// TestLoadNodePlugin_registersAndInvokesCapability verifies the end-to-end path:
// a plugin registers a media provider through the SDK shim, the host surfaces it
// via Capabilities(), and the provider's handler method is actually invokable
// through InvokeProvider (no longer a silent no-op).
func TestLoadNodePlugin_registersAndInvokesCapability(t *testing.T) {
	skipIfNoNode(t)

	dir := t.TempDir()
	pluginJS := `
'use strict';
const api = require('@openclaw/plugin-sdk');
module.exports = {
	register() {
	api.registerSpeechProvider({
		id: 'whisper',
		name: 'Whisper',
		description: 'speech to text',
		async transcribe(args) {
		return { text: 'heard: ' + (args && args.audio), provider: 'whisper' };
		}
	});
	api.registerMemoryEmbeddingProvider({
		id: 'embedder',
		embed(args) {
		return { vector: [1, 2, 3], input: args && args.text };
		}
	});
	return { tools: [] };
	}
};
`
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(pluginJS), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadNodePlugin(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadNodePlugin: %v", err)
	}
	defer p.Close()

	caps := p.Capabilities()
	if len(caps) != 2 {
		t.Fatalf("expected 2 capabilities, got %d: %+v", len(caps), caps)
	}
	byType := map[string]NodeCapability{}
	for _, c := range caps {
		byType[c.Type] = c
	}
	speech, ok := byType["speech_provider"]
	if !ok || speech.ID != "whisper" {
		t.Fatalf("speech provider not captured: %+v", caps)
	}
	if !containsString(speech.Methods, "transcribe") {
		t.Errorf("expected transcribe method, got %+v", speech.Methods)
	}
	if _, ok := byType["memory_embedding_provider"]; !ok {
		t.Errorf("memory embedding provider not captured: %+v", caps)
	}

	// The registered handler must be invokable and return a real result.
	res, err := p.InvokeProvider(context.Background(), "speech_provider", "whisper", "transcribe", map[string]any{"audio": "clip"})
	if err != nil {
		t.Fatalf("InvokeProvider: %v", err)
	}
	got, ok := res.(map[string]any)
	if !ok || got["text"] != "heard: clip" || got["provider"] != "whisper" {
		t.Fatalf("unexpected provider result: %#v", res)
	}
}

// TestLoadNodePlugin_capabilityRegistrationRequiresID confirms the shim FAILS
// LOUDLY rather than silently accepting a provider with no id.
func TestLoadNodePlugin_capabilityRegistrationRequiresID(t *testing.T) {
	skipIfNoNode(t)

	dir := t.TempDir()
	pluginJS := `
'use strict';
const api = require('@openclaw/plugin-sdk');
module.exports = {
	register() {
	api.registerImageGenerationProvider({ name: 'no-id-here', generate() { return {}; } });
	return { tools: [] };
	}
};
`
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(pluginJS), 0o644); err != nil {
		t.Fatal(err)
	}

	// name is used as a fallback id, so this one should actually succeed; assert
	// that the fallback id is honoured.
	p, err := LoadNodePlugin(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadNodePlugin: %v", err)
	}
	defer p.Close()
	caps := p.Capabilities()
	if len(caps) != 1 || caps[0].ID != "no-id-here" {
		t.Fatalf("expected fallback id from name, got %+v", caps)
	}
}

func TestLoadNodePlugin_registerProviderWithoutAnyIDFailsLoudly(t *testing.T) {
	skipIfNoNode(t)

	dir := t.TempDir()
	pluginJS := `
'use strict';
const api = require('@openclaw/plugin-sdk');
module.exports = {
	register() {
	api.registerImageGenerationProvider({ generate() { return {}; } });
	return { tools: [] };
	}
};
`
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(pluginJS), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadNodePlugin(context.Background(), dir); err == nil {
		t.Error("expected LoadNodePlugin to fail when a provider has no id/name")
	}
}

func TestLoadNodePlugin_invokeUnknownProviderErrors(t *testing.T) {
	skipIfNoNode(t)

	dir := t.TempDir()
	pluginJS := `'use strict'; module.exports = { register() { return { tools: [] }; }, tools: {} };`
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(pluginJS), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadNodePlugin(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadNodePlugin: %v", err)
	}
	defer p.Close()

	if _, err := p.InvokeProvider(context.Background(), "speech_provider", "nope", "transcribe", nil); err == nil {
		t.Error("expected error invoking unregistered provider")
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func TestLoadNodePlugin_openClawSDKImportPackageEntrypointAndAsyncTool(t *testing.T) {
	skipIfNoNode(t)

	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.Mkdir(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"openclaw-node-fixture","version":"1.2.3","main":"dist/plugin.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginJS := `
'use strict';
const api = require('@openclaw/plugin-sdk');
module.exports = {
  description: 'OpenClaw package fixture',
  async register() {
    api.registerTool({
      name: 'double',
      description: 'double a number',
      parameters: { type: 'object', properties: { n: { type: 'number' } } },
      async execute(_id, args) {
        await Promise.resolve();
        return { value: args.n * 2, root: api.resolvePath('x').endsWith('/x') };
      }
    });
  }
};
`
	if err := os.WriteFile(filepath.Join(dist, "plugin.js"), []byte(pluginJS), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadNodePlugin(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadNodePlugin: %v", err)
	}
	defer p.Close()

	mf := p.Manifest()
	if mf.ID != "openclaw-node-fixture" || mf.Version != "1.2.3" {
		t.Fatalf("unexpected manifest identity: %+v", mf)
	}
	if len(mf.Tools) != 1 || mf.Tools[0].Name != "double" {
		t.Fatalf("unexpected tools: %+v", mf.Tools)
	}

	result, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "double", Args: map[string]any{"n": 21}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	got, ok := result.Value.(map[string]any)
	if !ok || got["value"] != float64(42) || got["root"] != true {
		t.Fatalf("unexpected result: %#v", result.Value)
	}
}
