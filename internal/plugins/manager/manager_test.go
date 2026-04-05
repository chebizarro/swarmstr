package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

// ─── stub host ────────────────────────────────────────────────────────────────

type stubLog struct{}

func (s *stubLog) Info(msg string, args ...any)  {}
func (s *stubLog) Warn(msg string, args ...any)  {}
func (s *stubLog) Error(msg string, args ...any) {}

func testHost() *sdk.Host {
	return &sdk.Host{
		Log:     &stubLog{},
		Config:  &configHostImpl{cfg: &staticConfigState{}},
		Storage: &inMemoryStorage{data: map[string][]byte{}},
	}
}

type staticConfigState struct{}

func (s *staticConfigState) Get() state.ConfigDoc { return state.ConfigDoc{Version: 1} }

// ─── helpers ──────────────────────────────────────────────────────────────────

func writePlugin(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func configWithPlugin(t *testing.T, pluginID, scriptPath string) state.ConfigDoc {
	t.Helper()
	return state.ConfigDoc{
		Version: 1,
		Extra: map[string]any{
			"extensions": map[string]any{
				"enabled": true,
				"load":    true,
				"entries": map[string]any{
					pluginID: map[string]any{
						"install_path": scriptPath,
						"plugin_type":  "goja",
						"enabled":      true,
					},
				},
			},
		},
	}
}

const echoPluginSrc = `
exports.manifest = {
	id: "echo-plugin",
	version: "1.0.0",
	description: "echoes args",
	tools: [{ name: "echo", description: "echo the input" }],
};
exports.invoke = function(tool, args) {
	if (tool === "echo") return { echoed: args.message };
	return {};
};
`

const typedPluginSrc = `
exports.manifest = {
	id: "typed-plugin",
	version: "1.0.0",
	description: "typed args",
	tools: [{
		name: "sum",
		description: "sum the count",
		parameters: {
			type: "object",
			properties: { count: { type: "integer" } },
			required: ["count"]
		}
	}],
};
exports.invoke = function(tool, args) {
	if (tool === "sum") return { count: args.count };
	return {};
};
`

// ─── tests ────────────────────────────────────────────────────────────────────

func TestManager_loadAndRegister(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writePlugin(t, dir, "index.js", echoPluginSrc)

	cfg := configWithPlugin(t, "my-echo", scriptPath)
	mgr := New(testHost())
	if err := mgr.Load(context.Background(), cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ids := mgr.PluginIDs()
	if len(ids) != 1 || ids[0] != "my-echo" {
		t.Errorf("PluginIDs: %v", ids)
	}

	// Register into tool registry.
	reg := agent.NewToolRegistry()
	mgr.RegisterTools(reg)
	list := reg.List()
	if len(list) != 1 || list[0] != "my-echo/echo" {
		t.Errorf("tool list: %v", list)
	}
	desc, ok := reg.Descriptor("my-echo/echo")
	if !ok {
		t.Fatal("expected plugin tool descriptor")
	}
	if desc.Origin.Kind != agent.ToolOriginKindPlugin || desc.Origin.PluginID != "my-echo" || desc.Origin.CanonicalName != "echo" {
		t.Fatalf("unexpected descriptor origin: %+v", desc.Origin)
	}
	defs := reg.Definitions()
	if len(defs) != 1 || defs[0].Name != "my-echo/echo" {
		t.Fatalf("expected plugin tool to be provider-visible via Definitions, got %+v", defs)
	}
}

func TestManager_toolExecution(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writePlugin(t, dir, "index.js", echoPluginSrc)
	cfg := configWithPlugin(t, "my-echo", scriptPath)

	mgr := New(testHost())
	if err := mgr.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	reg := agent.NewToolRegistry()
	mgr.RegisterTools(reg)

	result, err := reg.Execute(context.Background(), agent.ToolCall{
		Name: "my-echo/echo",
		Args: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
	// Should be JSON-encoded map.
	if result[0] != '{' {
		t.Errorf("expected JSON object result, got: %q", result)
	}
}

func TestManager_pluginSchemaValidation(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writePlugin(t, dir, "index.js", typedPluginSrc)
	cfg := configWithPlugin(t, "typed", scriptPath)

	mgr := New(testHost())
	if err := mgr.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	reg := agent.NewToolRegistry()
	mgr.RegisterTools(reg)

	defs := reg.Definitions()
	if len(defs) != 1 || defs[0].Name != "typed/sum" {
		t.Fatalf("expected typed plugin definition, got %+v", defs)
	}
	if defs[0].InputJSONSchema == nil {
		t.Fatalf("expected raw plugin schema to be preserved on provider definition, got %+v", defs[0])
	}
	props, _ := defs[0].InputJSONSchema["properties"].(map[string]any)
	countProp, _ := props["count"].(map[string]any)
	if countProp["type"] != "integer" {
		t.Fatalf("expected raw plugin schema to survive, got %+v", defs[0].InputJSONSchema)
	}

	_, err := reg.Execute(context.Background(), agent.ToolCall{
		Name: "typed/sum",
		Args: map[string]any{"count": "oops"},
	})
	if err == nil {
		t.Fatal("expected schema validation error")
	}
	var execErr *agent.ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != agent.ToolExecutionPhaseSchemaValidation {
		t.Fatalf("expected schema validation phase, got %T %v", err, err)
	}
}

func TestManager_catalogGroups(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writePlugin(t, dir, "index.js", echoPluginSrc)
	cfg := configWithPlugin(t, "my-echo", scriptPath)

	mgr := New(testHost())
	if err := mgr.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	seen := map[string]struct{}{}
	groups := mgr.CatalogGroups(seen)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if g["id"] != "my-echo" {
		t.Errorf("group id: %v", g["id"])
	}
	tools, _ := g["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool in group, got %d", len(tools))
	}
	if tools[0]["id"] != "my-echo/echo" {
		t.Errorf("tool id: %v", tools[0]["id"])
	}
}

func TestManager_disabledEntrySkipped(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writePlugin(t, dir, "index.js", echoPluginSrc)
	cfg := state.ConfigDoc{
		Version: 1,
		Extra: map[string]any{
			"extensions": map[string]any{
				"entries": map[string]any{
					"my-echo": map[string]any{
						"install_path": scriptPath,
						"plugin_type":  "goja",
						"enabled":      false,
					},
				},
			},
		},
	}

	mgr := New(testHost())
	if err := mgr.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(mgr.PluginIDs()) != 0 {
		t.Errorf("expected disabled plugin to be skipped; got %v", mgr.PluginIDs())
	}
}

func TestManager_npmPluginSkipped(t *testing.T) {
	// npm plugin_type should be skipped by the Goja manager.
	cfg := state.ConfigDoc{
		Version: 1,
		Extra: map[string]any{
			"extensions": map[string]any{
				"entries": map[string]any{
					"my-npm": map[string]any{
						"install_path": "/some/path",
						"plugin_type":  "npm",
						"enabled":      true,
					},
				},
			},
		},
	}
	mgr := New(testHost())
	if err := mgr.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(mgr.PluginIDs()) != 0 {
		t.Errorf("npm plugin should be skipped; got %v", mgr.PluginIDs())
	}
}

func TestManager_packageJsonMain(t *testing.T) {
	// Plugin where main is declared in package.json.
	dir := t.TempDir()
	// Write package.json pointing to src/main.js.
	subDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, subDir, "main.js", echoPluginSrc)
	pkgJSON := `{"name": "my-plugin", "main": "src/main.js"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := configWithPlugin(t, "pkg-main-plugin", dir)
	mgr := New(testHost())
	if err := mgr.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(mgr.PluginIDs()) != 1 {
		t.Errorf("expected plugin loaded via package.json main, got %v", mgr.PluginIDs())
	}
}

func TestNavigateDotPath(t *testing.T) {
	doc := state.ConfigDoc{
		Version: 1,
		Agent:   state.AgentPolicy{DefaultModel: "claude-opus-4"},
		Extra:   map[string]any{"skills": map[string]any{"workspace": "/home/agent"}},
	}
	if v := navigateDotPath(doc, "agent.default_model"); v != "claude-opus-4" {
		t.Errorf("agent.default_model: %v", v)
	}
	if v := navigateDotPath(doc, "skills.workspace"); v != "/home/agent" {
		t.Errorf("skills.workspace: %v", v)
	}
	if v := navigateDotPath(doc, "missing.key"); v != nil {
		t.Errorf("missing key should return nil: %v", v)
	}
}
