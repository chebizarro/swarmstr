// Package manager loads Goja plugins from a ConfigDoc and wires their tools
// into the agent ToolRegistry and the tool-catalog API.
//
// Lifecycle:
//
//	mgr := manager.New(host)
//	if err := mgr.Load(ctx, cfg); err != nil { ... }
//	mgr.RegisterTools(toolRegistry)
//	groups := mgr.CatalogGroups(seen)
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"swarmstr/internal/agent"
	"swarmstr/internal/plugins/runtime"
	"swarmstr/internal/plugins/sdk"
	"swarmstr/internal/store/state"
)

// GojaPluginManager loads and manages Goja JS plugins.
type GojaPluginManager struct {
	mu      sync.RWMutex
	host    *sdk.Host
	plugins map[string]*runtime.GojaPlugin // pluginID → plugin
	log     *slog.Logger
}

// New creates a GojaPluginManager.  host is the SDK host bundle shared by all
// plugin VMs (each plugin gets its own VM; the host is just the interface glue).
func New(host *sdk.Host) *GojaPluginManager {
	if host == nil {
		host = &sdk.Host{}
	}
	return &GojaPluginManager{
		host:    host,
		plugins: map[string]*runtime.GojaPlugin{},
		log:     slog.Default().With("component", "plugin-manager"),
	}
}

// Load reads all enabled Goja plugins from cfg and compiles them.
// It is idempotent — subsequent calls replace the previous set.
func (m *GojaPluginManager) Load(ctx context.Context, cfg state.ConfigDoc) error {
	entries := pluginEntries(cfg)
	if len(entries) == 0 {
		return nil
	}

	next := map[string]*runtime.GojaPlugin{}
	for pluginID, entry := range entries {
		if !entryEnabled(entry) {
			continue
		}
		if !isGojaPlugin(entry) {
			// Native npm or other; skip — not a Goja plugin.
			continue
		}
		installPath, _ := entry["install_path"].(string)
		if installPath == "" {
			m.log.Warn("goja plugin has no install_path, skipping", "plugin", pluginID)
			continue
		}
		src, err := readPluginScript(installPath)
		if err != nil {
			m.log.Error("read plugin script", "plugin", pluginID, "err", err)
			continue
		}
		p, err := runtime.LoadPlugin(ctx, src, m.host)
		if err != nil {
			m.log.Error("load plugin failed", "plugin", pluginID, "err", err)
			continue
		}
		next[pluginID] = p
		m.log.Info("loaded goja plugin", "plugin", pluginID, "tools", len(p.Manifest().Tools))
	}

	m.mu.Lock()
	m.plugins = next
	m.mu.Unlock()
	return nil
}

// RegisterTools registers each tool from every loaded plugin into registry.
// Tool names are namespaced as "<pluginID>.<toolName>" to avoid collisions.
func (m *GojaPluginManager) RegisterTools(registry *agent.ToolRegistry) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for pluginID, p := range m.plugins {
		plugin := p // capture
		pid := pluginID
		for _, tool := range p.Manifest().Tools {
			toolName := pluginToolName(pid, tool.Name)
			toolFn := makeToolFunc(plugin, pid, tool.Name)
			registry.Register(toolName, toolFn)
			m.log.Debug("registered plugin tool", "tool", toolName)
		}
	}
}

// CatalogGroups returns tool catalog group entries for all loaded plugins.
// seen tracks IDs already emitted by the core catalog; duplicates are skipped.
func (m *GojaPluginManager) CatalogGroups(seen map[string]struct{}) []map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	groups := make([]map[string]any, 0, len(ids))
	for _, pluginID := range ids {
		p := m.plugins[pluginID]
		m := p.Manifest()
		tools := make([]map[string]any, 0, len(m.Tools))
		for _, t := range m.Tools {
			fullName := pluginToolName(pluginID, t.Name)
			if _, exists := seen[fullName]; exists {
				continue
			}
			seen[fullName] = struct{}{}
			tools = append(tools, map[string]any{
				"id":              fullName,
				"label":           t.Name,
				"description":     t.Description,
				"source":          "goja-plugin",
				"pluginId":        pluginID,
				"parameters":      t.Parameters,
				"defaultProfiles": []string{},
			})
		}
		if len(tools) == 0 {
			continue
		}
		groups = append(groups, map[string]any{
			"id":          pluginID,
			"label":       m.ID,
			"description": m.Description,
			"source":      "goja-plugin",
			"tools":       tools,
		})
	}
	return groups
}

// PluginIDs returns the IDs of all currently loaded plugins.
func (m *GojaPluginManager) PluginIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// PluginManifest returns the manifest for a specific plugin, or error if not found.
func (m *GojaPluginManager) PluginManifest(pluginID string) (sdk.Manifest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.plugins[pluginID]
	if !ok {
		return sdk.Manifest{}, fmt.Errorf("plugin %q not loaded", pluginID)
	}
	return p.Manifest(), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

func pluginEntries(cfg state.ConfigDoc) map[string]map[string]any {
	if cfg.Extra == nil {
		return nil
	}
	rawExt, ok := cfg.Extra["extensions"].(map[string]any)
	if !ok {
		return nil
	}
	rawEntries, ok := rawExt["entries"].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]map[string]any{}
	for id, v := range rawEntries {
		if e, ok := v.(map[string]any); ok {
			out[id] = e
		}
	}
	return out
}

func entryEnabled(entry map[string]any) bool {
	if enabled, ok := entry["enabled"].(bool); ok && !enabled {
		return false
	}
	return true
}

// isGojaPlugin returns true when the entry explicitly identifies as a goja plugin
// or when no explicit type is set (goja is the Nostr-native default).
func isGojaPlugin(entry map[string]any) bool {
	t, _ := entry["plugin_type"].(string)
	return t == "" || strings.EqualFold(t, "goja") || strings.EqualFold(t, "js")
}

// readPluginScript resolves the main script from an install path.
// It looks for (in order): index.js, plugin.js, main.js, or any *.js in root.
func readPluginScript(installPath string) ([]byte, error) {
	// Check if installPath itself is a .js file.
	if strings.HasSuffix(installPath, ".js") {
		return os.ReadFile(installPath)
	}

	// Look for well-known entry point names.
	for _, candidate := range []string{"index.js", "plugin.js", "main.js"} {
		p := filepath.Join(installPath, candidate)
		if _, err := os.Stat(p); err == nil {
			return os.ReadFile(p)
		}
	}

	// Check package.json for "main" field.
	pkgJSON := filepath.Join(installPath, "package.json")
	if data, err := os.ReadFile(pkgJSON); err == nil {
		var pkg struct {
			Main string `json:"main"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Main != "" {
			p := filepath.Join(installPath, pkg.Main)
			if _, err := os.Stat(p); err == nil {
				return os.ReadFile(p)
			}
		}
	}

	return nil, fmt.Errorf("no JS entry point found in %q", installPath)
}

// pluginToolName formats the tool name for registry dispatch.
// Format: "<pluginID>/<toolName>"  — uses / as separator to avoid dot-path confusion.
func pluginToolName(pluginID, toolName string) string {
	return pluginID + "/" + toolName
}

// makeToolFunc creates a ToolFunc that calls plugin.Invoke for the given tool.
func makeToolFunc(p *runtime.GojaPlugin, pluginID, toolName string) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		res, err := p.Invoke(ctx, sdk.InvokeRequest{
			Tool: toolName,
			Args: args,
			Meta: map[string]any{"plugin_id": pluginID},
		})
		if err != nil {
			return "", err
		}
		if res.Error != "" {
			return "", fmt.Errorf("plugin error: %s", res.Error)
		}
		// Serialize result to JSON string for agent consumption.
		b, err := json.Marshal(res.Value)
		if err != nil {
			return "", fmt.Errorf("serialize plugin result: %w", err)
		}
		return string(b), nil
	}
}
