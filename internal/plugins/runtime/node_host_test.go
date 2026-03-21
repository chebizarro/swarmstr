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
