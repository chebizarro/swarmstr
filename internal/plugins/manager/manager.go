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

	"metiq/internal/agent"
	"metiq/internal/plugins/installer"
	"metiq/internal/plugins/lifecycle"
	"metiq/internal/plugins/registry"
	"metiq/internal/plugins/runtime"
	"metiq/internal/plugins/sdk"
	"metiq/internal/plugins/trust"
	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

// pluginInstance is the common interface for Goja and Node.js plugin types.
type pluginInstance interface {
	Manifest() sdk.Manifest
	Invoke(ctx context.Context, req sdk.InvokeRequest) (sdk.InvokeResult, error)
}

// compile-time checks
var _ pluginInstance = (*runtime.GojaPlugin)(nil)
var _ pluginInstance = (*runtime.NodePlugin)(nil)

// GojaPluginManager loads and manages Goja JS (and Node.js compat) plugins.
type GojaPluginManager struct {
	mu      sync.RWMutex
	host    *sdk.Host
	plugins map[string]pluginInstance // pluginID → plugin
	log     *slog.Logger
}

// New creates a GojaPluginManager. host is the SDK host bundle shared by all
// plugin VMs (each plugin gets its own VM; the host is just the interface glue).
func New(host *sdk.Host) *GojaPluginManager {
	if host == nil {
		host = &sdk.Host{}
	}
	return &GojaPluginManager{
		host:    host,
		plugins: map[string]pluginInstance{},
		log:     slog.Default().With("component", "plugin-manager"),
	}
}

// Load reads all enabled Goja plugins from lifecycle state and compiles them.
// It is idempotent — subsequent calls replace the previous set.
func (m *GojaPluginManager) Load(ctx context.Context, cfg state.ConfigDoc) error {
	lc := lifecycle.NewManager(lifecycle.DefaultLifecycleConfig(), ".")
	if err := lc.LoadFromConfig(cfg); err != nil {
		return err
	}
	cfg = lc.ApplyToConfig(cfg)
	entries := pluginEntries(cfg)
	if len(entries) == 0 {
		m.mu.Lock()
		m.plugins = map[string]pluginInstance{}
		m.mu.Unlock()
		return nil
	}

	rawExt := extensionsConfig(cfg)
	sandboxCfg, sandboxEnabled := pluginSandboxConfig(rawExt)
	next := map[string]pluginInstance{}
	var issues []string

	for pluginID, entry := range entries {
		if !entryEnabled(entry) {
			continue
		}
		if !isNodePlugin(entry) && !isGojaPlugin(entry) {
			m.log.Debug("plugin type not supported, skipping", "plugin", pluginID)
			continue
		}
		installPath, err := resolveTrustedPluginInstallPath(rawExt, pluginID, entry)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
			m.log.Warn("plugin skipped during load", "plugin", pluginID, "err", err)
			continue
		}
		if err := installer.VerifyPluginIntegrity(installPath); err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
			m.log.Warn("plugin integrity verification failed", "plugin", pluginID, "err", err)
			continue
		}
		pluginTrust := resolvePluginTrust(rawExt, pluginID, entry)
		sandboxDecision := NodeSandboxDecision(pluginTrust, sandboxEnabled, sandboxCfg != nil)

		// Node.js compat bridge: activated when plugin_type is "node"/"nodejs"
		// OR when the trusted install path contains a node_modules directory.
		if isNodePlugin(entry) || runtime.IsNodePlugin(installPath) {
			entryPath, err := resolvePluginEntryPoint(installPath)
			if err != nil {
				issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
				m.log.Warn("node plugin entry resolution failed", "plugin", pluginID, "err", err)
			} else {
				m.log.Info("loading node.js plugin", "plugin", pluginID, "trust", pluginTrust, "sandbox", sandboxDecision.Action, "reason", sandboxDecision.Reason)
				if sandboxDecision.Action == SandboxActionFailOpen {
					m.log.Warn("untrusted node plugin running without sandbox; falling back to current subprocess behavior", "plugin", pluginID, "trust", pluginTrust, "reason", sandboxDecision.Reason)
				}
				if sandboxDecision.Action == SandboxActionUse {
					if _, err := sandbox.New(*sandboxCfg); err != nil {
						issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
						m.log.Warn("node plugin sandbox configuration invalid", "plugin", pluginID, "trust", pluginTrust, "err", err)
						continue
					}
				}
				np, err := runtime.LoadNodePlugin(ctx, entryPath)
				if err != nil {
					issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
					m.log.Warn("node plugin load failed (node.js may not be installed)", "plugin", pluginID, "err", err)
				} else if err := checkManifestMismatch(pluginID, np.Manifest()); err != nil {
					issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
					m.log.Warn("node plugin manifest mismatch", "plugin", pluginID, "err", err)
				} else {
					next[pluginID] = np
					m.log.Info("loaded node.js plugin", "plugin", pluginID, "tools", len(np.Manifest().Tools), "trust", pluginTrust, "sandbox", sandboxDecision.Action)
					continue
				}
			}
		}

		if !isGojaPlugin(entry) {
			continue
		}
		src, err := readPluginScript(installPath)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
			m.log.Error("read plugin script", "plugin", pluginID, "err", err)
			continue
		}
		m.log.Info("loading goja plugin", "plugin", pluginID, "trust", pluginTrust, "sandbox", "in-process-deny-sensitive")
		host, err := cloneHostForPlugin(m.host, pluginID)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
			m.log.Error("build plugin host failed", "plugin", pluginID, "err", err)
			continue
		}
		p, err := runtime.LoadPluginWithOptions(ctx, src, host, runtime.LoadOptions{Trust: pluginTrust.String()})
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
			m.log.Error("load plugin failed", "plugin", pluginID, "err", err)
			continue
		}
		if err := checkManifestMismatch(pluginID, p.Manifest()); err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
			m.log.Warn("goja plugin manifest mismatch", "plugin", pluginID, "err", err)
			continue
		}
		next[pluginID] = p
		m.log.Info("loaded goja plugin", "plugin", pluginID, "tools", len(p.Manifest().Tools), "trust", pluginTrust, "sandbox", "in-process-deny-sensitive")
	}

	m.mu.Lock()
	m.plugins = next
	m.mu.Unlock()

	if len(issues) > 0 {
		return fmt.Errorf("plugin load issues: %s", strings.Join(issues, "; "))
	}
	return nil
}

// RegisterTools registers each tool from every loaded plugin into registry.
// Tool names are namespaced as "<pluginID>/<toolName>" to avoid collisions.
func (m *GojaPluginManager) RegisterTools(registry *agent.ToolRegistry) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for pluginID, p := range m.plugins {
		plugin := p // capture
		pid := pluginID
		for _, tool := range p.Manifest().Tools {
			toolName := pluginToolName(pid, tool.Name)
			toolFn := makeToolFunc(plugin, pid, tool.Name)
			registry.RegisterWithDescriptor(toolName, toolFn, descriptorFromPluginTool(pid, tool))
			m.log.Debug("registered plugin tool", "tool", toolName)
		}
	}
}

// capabilityLister is implemented by plugin instances (currently
// runtime.NodePlugin) that expose media/search/memory provider capabilities
// captured from the plugin at load time.
type capabilityLister interface {
	Capabilities() []runtime.NodeCapability
}

// providerInvoker is implemented by plugin instances (currently
// runtime.NodePlugin) that can route a call to a registered capability
// provider's handler method.
type providerInvoker interface {
	InvokeProvider(ctx context.Context, capType, id, method string, args map[string]any) (any, error)
}

// RegisterCapabilities registers the media/search/memory provider capabilities
// of every loaded node plugin into the unified capability registry, mirroring
// how RegisterTools wires plugin tools into the agent ToolRegistry. After this
// call a node plugin's registered providers (speech/transcription/voice/image/
// video/music/web-search/web-fetch/memory-embedding) are discoverable and
// resolvable through the unified registry the same way built-in and OpenClaw
// providers are, and invokable via InvokeProvider. Plugins that expose no
// capabilities (all Goja plugins, and node plugins that registered none) are
// skipped. It returns a combined error if any plugin's registration failed.
func (m *GojaPluginManager) RegisterCapabilities(unified *registry.UnifiedRegistry) error {
	if unified == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var issues []string
	for _, pluginID := range ids {
		p := m.plugins[pluginID]
		lister, ok := p.(capabilityLister)
		if !ok {
			continue
		}
		caps := lister.Capabilities()
		if len(caps) == 0 {
			continue
		}
		mf := p.Manifest()
		if err := unified.RegisterFromNodePlugin(pluginID, mf.ID, mf.Version, caps); err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", pluginID, err))
			m.log.Warn("register node plugin capabilities failed", "plugin", pluginID, "err", err)
			continue
		}
		m.log.Debug("registered node plugin capabilities", "plugin", pluginID, "count", len(caps))
	}
	if len(issues) > 0 {
		return fmt.Errorf("capability registration issues: %s", strings.Join(issues, "; "))
	}
	return nil
}

// InvokeProvider routes a provider-capability call to the loaded node plugin
// identified by pluginID and returns the handler's result. capType and
// providerID must match a capability the plugin reported via Capabilities()
// (and registered through RegisterCapabilities); method must be one of that
// capability's handler methods. It mirrors the tool-dispatch path but targets
// media/search/memory provider handlers instead of tools.
func (m *GojaPluginManager) InvokeProvider(ctx context.Context, pluginID, capType, providerID, method string, args map[string]any) (any, error) {
	m.mu.RLock()
	p, ok := m.plugins[pluginID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin %q not loaded", pluginID)
	}
	invoker, ok := p.(providerInvoker)
	if !ok {
		return nil, fmt.Errorf("plugin %q does not support provider invocation", pluginID)
	}
	return invoker.InvokeProvider(ctx, capType, providerID, method, args)
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
		mf := p.Manifest()
		tools := make([]map[string]any, 0, len(mf.Tools))
		for _, t := range mf.Tools {
			fullName := pluginToolName(pluginID, t.Name)
			if _, exists := seen[fullName]; exists {
				continue
			}
			seen[fullName] = struct{}{}
			tools = append(tools, map[string]any{
				"id":              fullName,
				"label":           t.Name,
				"description":     t.Description,
				"source":          "plugin",
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
			"label":       mf.ID,
			"description": mf.Description,
			"source":      "plugin",
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

func extensionsConfig(cfg state.ConfigDoc) map[string]any {
	if cfg.Extra == nil {
		return nil
	}
	rawExt, _ := cfg.Extra["extensions"].(map[string]any)
	return rawExt
}

func pluginEntries(cfg state.ConfigDoc) map[string]map[string]any {
	rawExt := extensionsConfig(cfg)
	if rawExt == nil {
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

func pluginInstallRecord(rawExt map[string]any, pluginID string) map[string]any {
	if rawExt == nil {
		return nil
	}
	rawInstalls, _ := rawExt["installs"].(map[string]any)
	record, _ := rawInstalls[pluginID].(map[string]any)
	return record
}

type SandboxAction string

const (
	SandboxActionSkip     SandboxAction = "skip"
	SandboxActionUse      SandboxAction = "use"
	SandboxActionFailOpen SandboxAction = "fail-open"
)

type SandboxDecision struct {
	Action SandboxAction
	Reason string
}

func NodeSandboxDecision(level trust.Level, enabled bool, configured bool) SandboxDecision {
	if level.IsTrusted() {
		return SandboxDecision{Action: SandboxActionSkip, Reason: "trusted plugin"}
	}
	if !enabled {
		return SandboxDecision{Action: SandboxActionSkip, Reason: "sandbox disabled"}
	}
	if !configured {
		return SandboxDecision{Action: SandboxActionFailOpen, Reason: "sandbox enabled without configuration"}
	}
	return SandboxDecision{Action: SandboxActionUse, Reason: "untrusted plugin with sandbox configured"}
}

func resolvePluginTrust(rawExt map[string]any, pluginID string, entry map[string]any) trust.Level {
	if level := trust.FromInstallRecord(entry); entry != nil && (entry["trust"] != nil || entry["source"] != nil || entry["type"] != nil) {
		return level
	}
	return trust.FromInstallRecord(pluginInstallRecord(rawExt, pluginID))
}

func pluginSandboxConfig(rawExt map[string]any) (*sandbox.Config, bool) {
	if rawExt == nil {
		return nil, false
	}
	raw, _ := rawExt["sandbox"].(map[string]any)
	if raw == nil {
		return nil, false
	}
	enabled, _ := raw["enabled"].(bool)
	rawCfg, _ := raw["config"].(map[string]any)
	if rawCfg == nil {
		rawCfg = raw
	}
	cfg := sandbox.Config{
		Driver:          firstNonEmptyString(rawCfg["driver"]),
		AllowUnsafeNop:  boolValue(rawCfg["allow_unsafe_nop"]),
		MemoryLimit:     firstNonEmptyString(rawCfg["memory_limit"]),
		CPULimit:        firstNonEmptyString(rawCfg["cpu_limit"]),
		DockerImage:     firstNonEmptyString(rawCfg["docker_image"]),
		AllowNetwork:    boolValue(rawCfg["allow_network"]),
		NetworkDisabled: boolValue(rawCfg["network_disabled"]),
		ReadOnlyRootFS:  boolValue(rawCfg["read_only_rootfs"]),
		WritableRootFS:  boolValue(rawCfg["writable_rootfs"]),
	}
	return &cfg, enabled
}

func boolValue(raw any) bool {
	v, _ := raw.(bool)
	return v
}

func pluginLoadRoots(rawExt map[string]any) []string {
	if rawExt == nil {
		return nil
	}
	var out []string
	switch vals := rawExt["load_paths"].(type) {
	case []string:
		for _, pathValue := range vals {
			if trimmed := strings.TrimSpace(pathValue); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	case []any:
		for _, raw := range vals {
			if trimmed := stringValue(raw); trimmed != "" {
				out = append(out, trimmed)
			}
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

// isNodePlugin returns true when the entry explicitly requests the Node.js compat bridge.
func isNodePlugin(entry map[string]any) bool {
	t, _ := entry["plugin_type"].(string)
	return strings.EqualFold(t, "node") || strings.EqualFold(t, "nodejs")
}

func stringValue(raw any) string {
	s, _ := raw.(string)
	return strings.TrimSpace(s)
}

func firstNonEmptyString(values ...any) string {
	for _, raw := range values {
		if s := stringValue(raw); s != "" {
			return s
		}
	}
	return ""
}

func resolveTrustedPluginInstallPath(rawExt map[string]any, pluginID string, entry map[string]any) (string, error) {
	entryInstallPath := firstNonEmptyString(entry["install_path"], entry["installPath"])
	record := pluginInstallRecord(rawExt, pluginID)
	loadRoots := pluginLoadRoots(rawExt)

	if record != nil {
		source := strings.ToLower(firstNonEmptyString(record["source"]))
		recordInstallPath := firstNonEmptyString(record["installPath"], record["install_path"])
		recordSourcePath := firstNonEmptyString(record["sourcePath"], record["source_path"])

		switch source {
		case "npm", "archive":
			candidate := firstNonEmptyString(entryInstallPath, recordInstallPath)
			if candidate == "" {
				return "", fmt.Errorf("managed plugin missing installPath")
			}
			managedPath, ok := installer.ResolveManagedPath(candidate)
			if !ok {
				return "", fmt.Errorf("install path %q is outside managed extensions", candidate)
			}
			if recordInstallPath != "" {
				expectedPath, ok := installer.ResolveManagedPath(recordInstallPath)
				if !ok {
					return "", fmt.Errorf("install record installPath %q is outside managed extensions", recordInstallPath)
				}
				if managedPath != expectedPath {
					return "", fmt.Errorf("entry install_path %q does not match install record %q", entryInstallPath, recordInstallPath)
				}
			}
			return managedPath, nil
		case "path":
			candidate := firstNonEmptyString(entryInstallPath, recordInstallPath, recordSourcePath)
			if candidate == "" {
				return "", fmt.Errorf("path plugin missing install path")
			}
			resolvedCandidate, err := resolveExistingPath(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve install path: %w", err)
			}
			if same, err := sameResolvedPath(resolvedCandidate, recordInstallPath); err == nil && same {
				return resolvedCandidate, nil
			}
			if same, err := sameResolvedPath(resolvedCandidate, recordSourcePath); err == nil && same {
				return resolvedCandidate, nil
			}
			if within, err := pathWithinCandidateRoot(resolvedCandidate, recordSourcePath); err == nil && within {
				return resolvedCandidate, nil
			}
			if pathWithinAnyRoot(resolvedCandidate, loadRoots) {
				return resolvedCandidate, nil
			}
			return "", fmt.Errorf("path plugin install_path %q is outside install record and plugins.load_paths", candidate)
		case "":
			// Fall through to the generic trust rules below.
		default:
			return "", fmt.Errorf("unsupported install record source %q", source)
		}
	}

	if entryInstallPath == "" {
		return "", fmt.Errorf("plugin has no install_path")
	}
	if managedPath, ok := installer.ResolveManagedPath(entryInstallPath); ok {
		return managedPath, nil
	}
	resolvedCandidate, err := resolveExistingPath(entryInstallPath)
	if err != nil {
		return "", fmt.Errorf("resolve install_path: %w", err)
	}
	if pathWithinAnyRoot(resolvedCandidate, loadRoots) {
		return resolvedCandidate, nil
	}
	return "", fmt.Errorf("install_path %q is outside managed extensions and plugins.load_paths", entryInstallPath)
}

func resolveExistingPath(pathValue string) (string, error) {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return "", fmt.Errorf("empty path")
	}
	candidate, err := filepath.Abs(filepath.Clean(pathValue))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", err
	}
	return candidate, nil
}

func resolveRootPath(pathValue string) (string, error) {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return "", fmt.Errorf("empty root")
	}
	root, err := filepath.Abs(filepath.Clean(pathValue))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, nil
}

func sameResolvedPath(candidateResolved, otherPath string) (bool, error) {
	otherPath = strings.TrimSpace(otherPath)
	if otherPath == "" {
		return false, fmt.Errorf("empty comparison path")
	}
	otherResolved, err := resolveExistingPath(otherPath)
	if err != nil {
		return false, err
	}
	return candidateResolved == otherResolved, nil
}

func pathWithinCandidateRoot(candidateResolved, rootPath string) (bool, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return false, fmt.Errorf("empty root")
	}
	rootResolved, err := resolveRootPath(rootPath)
	if err != nil {
		return false, err
	}
	return pathWithinResolvedRoot(candidateResolved, rootResolved), nil
}

func pathWithinAnyRoot(candidateResolved string, roots []string) bool {
	for _, rootPath := range roots {
		rootResolved, err := resolveRootPath(rootPath)
		if err != nil {
			continue
		}
		if pathWithinResolvedRoot(candidateResolved, rootResolved) {
			return true
		}
	}
	return false
}

func pathWithinResolvedRoot(candidateResolved, rootResolved string) bool {
	rel, err := filepath.Rel(rootResolved, candidateResolved)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || rel == "" || (!strings.HasPrefix(rel, "../") && rel != "..")
}

func resolvePluginSubpath(rootResolved, relPath string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return "", fmt.Errorf("empty entry path")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute entry path not allowed")
	}
	candidate, err := resolveExistingPath(filepath.Join(rootResolved, filepath.Clean(relPath)))
	if err != nil {
		return "", err
	}
	if !pathWithinResolvedRoot(candidate, rootResolved) {
		return "", fmt.Errorf("entry path %q escapes install root", relPath)
	}
	return candidate, nil
}

// resolvePluginEntryPoint resolves the main script from an install path.
// It looks for (in order): index.js, plugin.js, main.js, or package.json main.
func resolvePluginEntryPoint(installPath string) (string, error) {
	installPathResolved, err := resolveExistingPath(installPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(installPathResolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return installPathResolved, nil
	}

	for _, candidate := range []string{"index.js", "plugin.js", "main.js"} {
		if entryPath, err := resolvePluginSubpath(installPathResolved, candidate); err == nil {
			return entryPath, nil
		}
	}

	pkgJSONPath := filepath.Join(installPathResolved, "package.json")
	if data, err := os.ReadFile(pkgJSONPath); err == nil {
		var pkg struct {
			Main string `json:"main"`
		}
		if json.Unmarshal(data, &pkg) == nil && strings.TrimSpace(pkg.Main) != "" {
			entryPath, err := resolvePluginSubpath(installPathResolved, pkg.Main)
			if err != nil {
				return "", fmt.Errorf("invalid package.json main: %w", err)
			}
			return entryPath, nil
		}
	}

	return "", fmt.Errorf("no JS entry point found in %q", installPath)
}

// readPluginScript resolves the plugin entry point and returns its source.
func readPluginScript(installPath string) ([]byte, error) {
	entryPath, err := resolvePluginEntryPoint(installPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(entryPath)
}

func checkManifestMismatch(pluginID string, manifest sdk.Manifest) error {
	if pluginID == "" || strings.EqualFold(strings.TrimSpace(manifest.ID), strings.TrimSpace(pluginID)) {
		return nil
	}
	return fmt.Errorf("plugin manifest id %q does not match config entry %q", manifest.ID, pluginID)
}

// pluginToolName formats the tool name for registry dispatch.
// Format: "<pluginID>/<toolName>" — uses / as separator to avoid dot-path confusion.
func pluginToolName(pluginID, toolName string) string {
	return pluginID + "/" + toolName
}

// makeToolFunc creates a ToolFunc that calls plugin.Invoke for the given tool.
func makeToolFunc(p pluginInstance, pluginID, toolName string) agent.ToolFunc {
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

func descriptorFromPluginTool(pluginID string, tool sdk.ToolSchema) agent.ToolDescriptor {
	return agent.ToolDescriptor{
		Name:            pluginToolName(pluginID, tool.Name),
		Description:     tool.Description,
		Parameters:      toolParametersFromMap(tool.Parameters),
		InputJSONSchema: cloneJSONMap(tool.Parameters),
		Origin: agent.ToolOrigin{
			Kind:          agent.ToolOriginKindPlugin,
			PluginID:      pluginID,
			CanonicalName: tool.Name,
		},
	}
}

func toolParametersFromMap(raw map[string]any) agent.ToolParameters {
	if len(raw) == 0 {
		return agent.ToolParameters{}
	}
	var params agent.ToolParameters
	b, err := json.Marshal(raw)
	if err != nil {
		return agent.ToolParameters{}
	}
	if err := json.Unmarshal(b, &params); err != nil {
		return agent.ToolParameters{}
	}
	return params
}

func cloneJSONMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	b, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}
