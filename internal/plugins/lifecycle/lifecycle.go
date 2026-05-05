// Package lifecycle provides scoped plugin installation and lifecycle management.
//
// The lifecycle manager handles:
//   - Scoped installation (user/project/local)
//   - Enable/disable/update controls
//   - Scope-aware plugin resolution
//   - Registry refresh and synchronization
//   - Opt-in skill export with manifest declaration
//
// Installation scopes:
//   - UserScope: ~/.metiq/plugins/ — available to all projects for the user
//   - ProjectScope: .metiq/plugins/ — available only within the project
//   - LocalScope: in-memory only — not persisted, for development/testing
package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"metiq/internal/plugins/manifest"
)

// ─── Installation Scope ──────────────────────────────────────────────────────

// Scope defines where a plugin is installed and its availability.
type Scope string

const (
	// ScopeUser installs to ~/.metiq/plugins/ (user-wide).
	ScopeUser Scope = "user"
	// ScopeProject installs to .metiq/plugins/ (project-specific).
	ScopeProject Scope = "project"
	// ScopeLocal is in-memory only (development/testing).
	ScopeLocal Scope = "local"
)

// AllScopes returns all scopes in resolution order (most specific first).
func AllScopes() []Scope {
	return []Scope{ScopeLocal, ScopeProject, ScopeUser}
}

// IsValid reports whether the scope is recognized.
func (s Scope) IsValid() bool {
	switch s {
	case ScopeUser, ScopeProject, ScopeLocal:
		return true
	default:
		return false
	}
}

// ─── Plugin State ────────────────────────────────────────────────────────────

// PluginState tracks the lifecycle state of an installed plugin.
type PluginState string

const (
	// StateInstalled means the plugin is installed but not enabled.
	StateInstalled PluginState = "installed"
	// StateEnabled means the plugin is active and will be loaded.
	StateEnabled PluginState = "enabled"
	// StateDisabled means the plugin is installed but won't be loaded.
	StateDisabled PluginState = "disabled"
	// StateError means the plugin failed to load or has errors.
	StateError PluginState = "error"
	// StateUpdating means an update is in progress.
	StateUpdating PluginState = "updating"
)

// ─── Installed Plugin ────────────────────────────────────────────────────────

// InstalledPlugin represents a plugin installed at a specific scope.
type InstalledPlugin struct {
	// PluginID is the unique identifier.
	PluginID string `json:"plugin_id"`

	// Scope is where the plugin is installed.
	Scope Scope `json:"scope"`

	// State is the current lifecycle state.
	State PluginState `json:"state"`

	// Manifest is the plugin's manifest.
	Manifest manifest.Manifest `json:"manifest"`

	// InstallPath is the filesystem path where the plugin is installed.
	InstallPath string `json:"install_path"`

	// InstalledAt is when the plugin was installed.
	InstalledAt time.Time `json:"installed_at"`

	// UpdatedAt is when the plugin was last updated.
	UpdatedAt time.Time `json:"updated_at,omitempty"`

	// EnabledAt is when the plugin was enabled.
	EnabledAt *time.Time `json:"enabled_at,omitempty"`

	// DisabledAt is when the plugin was disabled.
	DisabledAt *time.Time `json:"disabled_at,omitempty"`

	// Error contains error details if State is StateError.
	Error string `json:"error,omitempty"`

	// Source describes where the plugin came from.
	Source InstallSource `json:"source"`

	// ExportSkills indicates whether skills should be exported.
	ExportSkills bool `json:"export_skills"`

	// Config holds plugin-specific configuration.
	Config map[string]any `json:"config,omitempty"`
}

// InstallSource describes the origin of a plugin installation.
type InstallSource struct {
	// Type is the installation method (npm, archive, path, registry).
	Type string `json:"type"`

	// URL is the source URL (for archive/registry installs).
	URL string `json:"url,omitempty"`

	// Package is the npm package name (for npm installs).
	Package string `json:"package,omitempty"`

	// Version is the installed version.
	Version string `json:"version,omitempty"`

	// Path is the source path (for path installs).
	Path string `json:"path,omitempty"`

	// Checksum is the archive checksum (for archive installs).
	Checksum string `json:"checksum,omitempty"`
}

// ─── Lifecycle Manager ───────────────────────────────────────────────────────

// LifecycleConfig configures the lifecycle manager.
type LifecycleConfig struct {
	// UserPluginDir is the user-scope plugin directory.
	// Defaults to ~/.metiq/plugins/
	UserPluginDir string `json:"user_plugin_dir,omitempty"`

	// ProjectPluginDir is the project-scope plugin directory.
	// Defaults to .metiq/plugins/
	ProjectPluginDir string `json:"project_plugin_dir,omitempty"`

	// AutoEnable enables plugins immediately after installation.
	AutoEnable bool `json:"auto_enable"`

	// AllowSkillExport allows plugins to export skills (requires manifest opt-in).
	AllowSkillExport bool `json:"allow_skill_export"`

	// RefreshInterval is how often to check for updates (0 = manual only).
	RefreshInterval time.Duration `json:"refresh_interval,omitempty"`
}

// DefaultLifecycleConfig returns sensible defaults.
func DefaultLifecycleConfig() LifecycleConfig {
	home, _ := os.UserHomeDir()
	return LifecycleConfig{
		UserPluginDir:    filepath.Join(home, ".metiq", "plugins"),
		ProjectPluginDir: filepath.Join(".metiq", "plugins"),
		AutoEnable:       true,
		AllowSkillExport: false,
		RefreshInterval:  0,
	}
}

// Manager handles plugin lifecycle operations.
type Manager struct {
	mu         sync.RWMutex
	cfg        LifecycleConfig
	plugins    map[string]map[Scope]*InstalledPlugin // pluginID → scope → plugin
	registry   *manifest.CapabilityRegistry
	projectDir string
}

// NewManager creates a new lifecycle manager.
func NewManager(cfg LifecycleConfig, projectDir string) *Manager {
	return &Manager{
		cfg:        cfg,
		plugins:    make(map[string]map[Scope]*InstalledPlugin),
		registry:   manifest.NewCapabilityRegistry(),
		projectDir: projectDir,
	}
}

func cloneInstalledPlugin(p *InstalledPlugin) *InstalledPlugin {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Manifest = cloneManifest(p.Manifest)
	cp.Config = cloneMap(p.Config)
	if p.EnabledAt != nil {
		t := *p.EnabledAt
		cp.EnabledAt = &t
	}
	if p.DisabledAt != nil {
		t := *p.DisabledAt
		cp.DisabledAt = &t
	}
	return &cp
}

func cloneManifest(mf manifest.Manifest) manifest.Manifest {
	data, err := json.Marshal(mf)
	if err != nil {
		return mf
	}
	var cp manifest.Manifest
	if err := json.Unmarshal(data, &cp); err != nil {
		return mf
	}
	return cp
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	cp := make(map[string]any, len(src))
	for k, v := range src {
		cp[k] = cloneAny(v)
	}
	return cp
}

func cloneAny(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		cp := make([]any, len(typed))
		for i, item := range typed {
			cp[i] = cloneAny(item)
		}
		return cp
	case []string:
		cp := make([]string, len(typed))
		copy(cp, typed)
		return cp
	case map[string]string:
		cp := make(map[string]string, len(typed))
		for k, v := range typed {
			cp[k] = v
		}
		return cp
	default:
		return v
	}
}

func (m *Manager) pluginByScopeLocked(pluginID string, scope Scope) (*InstalledPlugin, bool) {
	byScope := m.plugins[pluginID]
	if byScope == nil {
		return nil, false
	}
	p, ok := byScope[scope]
	return p, ok
}

func (m *Manager) putPluginLocked(plugin *InstalledPlugin) {
	if m.plugins[plugin.PluginID] == nil {
		m.plugins[plugin.PluginID] = make(map[Scope]*InstalledPlugin)
	}
	m.plugins[plugin.PluginID][plugin.Scope] = plugin
}

func (m *Manager) deletePluginLocked(pluginID string, scope Scope) {
	if byScope := m.plugins[pluginID]; byScope != nil {
		delete(byScope, scope)
		if len(byScope) == 0 {
			delete(m.plugins, pluginID)
		}
	}
}

func (m *Manager) resolveLocked(pluginID string) (*InstalledPlugin, bool) {
	for _, scope := range AllScopes() {
		if p, ok := m.pluginByScopeLocked(pluginID, scope); ok {
			return p, true
		}
	}
	return nil, false
}

func (m *Manager) resolveTargetLocked(pluginID string, scope *Scope) (*InstalledPlugin, error) {
	if scope == nil {
		if plugin, ok := m.resolveLocked(pluginID); ok {
			return plugin, nil
		}
		return nil, fmt.Errorf("plugin %q not installed", pluginID)
	}
	if !scope.IsValid() {
		return nil, fmt.Errorf("invalid scope: %s", *scope)
	}
	if plugin, ok := m.pluginByScopeLocked(pluginID, *scope); ok {
		return plugin, nil
	}
	return nil, fmt.Errorf("plugin %q not installed at scope %s", pluginID, *scope)
}

func (m *Manager) allPluginsLocked() []*InstalledPlugin {
	result := make([]*InstalledPlugin, 0)
	for _, byScope := range m.plugins {
		for _, p := range byScope {
			result = append(result, p)
		}
	}
	sortPlugins(result)
	return result
}

func sortPlugins(plugins []*InstalledPlugin) {
	scopeOrder := map[Scope]int{
		ScopeLocal:   0,
		ScopeProject: 1,
		ScopeUser:    2,
	}
	sort.Slice(plugins, func(i, j int) bool {
		if plugins[i].PluginID != plugins[j].PluginID {
			return plugins[i].PluginID < plugins[j].PluginID
		}
		return scopeOrder[plugins[i].Scope] < scopeOrder[plugins[j].Scope]
	})
}

func clonePluginSlice(plugins []*InstalledPlugin) []*InstalledPlugin {
	out := make([]*InstalledPlugin, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, cloneInstalledPlugin(p))
	}
	return out
}

func (m *Manager) rebuildRegistryLocked(ctx context.Context) error {
	newRegistry := manifest.NewCapabilityRegistry()
	ids := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var errors []string
	for _, id := range ids {
		p, ok := m.resolveLocked(id)
		if !ok || p.State != StateEnabled {
			continue
		}
		mf := cloneManifest(p.Manifest)
		if err := newRegistry.Register(ctx, &mf); err != nil {
			errors = append(errors, fmt.Sprintf("%s[%s]: %v", p.PluginID, p.Scope, err))
			p.State = StateError
			p.Error = err.Error()
			continue
		}
	}

	m.registry = newRegistry
	if len(errors) > 0 {
		return fmt.Errorf("registry refresh errors: %v", errors)
	}
	return nil
}

// ─── Installation ────────────────────────────────────────────────────────────

// InstallOptions configures plugin installation.
type InstallOptions struct {
	// Scope is where to install the plugin.
	Scope Scope `json:"scope"`

	// Source describes the installation source.
	Source InstallSource `json:"source"`

	// Enable immediately enables the plugin after installation.
	Enable bool `json:"enable"`

	// ExportSkills enables skill export (requires manifest opt-in).
	ExportSkills bool `json:"export_skills"`

	// Config provides initial plugin configuration.
	Config map[string]any `json:"config,omitempty"`

	// Force overwrites existing installation.
	Force bool `json:"force"`
}

// Install installs a plugin at the specified scope.
func (m *Manager) Install(ctx context.Context, mf manifest.Manifest, installPath string, opts InstallOptions) (*InstalledPlugin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate manifest
	if err := manifest.Validate(&mf); err != nil {
		return nil, fmt.Errorf("invalid manifest: %v", err)
	}

	// Validate scope
	if !opts.Scope.IsValid() {
		return nil, fmt.Errorf("invalid scope: %s", opts.Scope)
	}

	// Check for existing installation at this exact scope. Other scopes may
	// intentionally contain the same plugin ID and are resolved by precedence.
	if existing, ok := m.pluginByScopeLocked(mf.ID, opts.Scope); ok && !opts.Force {
		return nil, fmt.Errorf("plugin %q already installed at scope %s", mf.ID, existing.Scope)
	}

	// Check skill export eligibility
	exportSkills := opts.ExportSkills
	if exportSkills {
		if !m.cfg.AllowSkillExport {
			return nil, fmt.Errorf("skill export is disabled in configuration")
		}
		if !mf.Capabilities.HasSkillExportCapability() {
			return nil, fmt.Errorf("plugin manifest does not declare skill export capability")
		}
	}

	now := time.Now()
	plugin := &InstalledPlugin{
		PluginID:     mf.ID,
		Scope:        opts.Scope,
		State:        StateInstalled,
		Manifest:     cloneManifest(mf),
		InstallPath:  installPath,
		InstalledAt:  now,
		Source:       opts.Source,
		ExportSkills: exportSkills,
		Config:       cloneMap(opts.Config),
	}

	// Enable if requested
	if opts.Enable || m.cfg.AutoEnable {
		plugin.State = StateEnabled
		plugin.EnabledAt = &now
	}

	m.putPluginLocked(plugin)
	if err := m.rebuildRegistryLocked(ctx); err != nil && plugin.State == StateEnabled {
		plugin.State = StateError
		plugin.Error = err.Error()
		_ = m.rebuildRegistryLocked(ctx)
	}

	return cloneInstalledPlugin(plugin), nil
}

// Uninstall removes a plugin installation.
func (m *Manager) Uninstall(ctx context.Context, pluginID string) error {
	return m.uninstall(ctx, pluginID, nil)
}

// UninstallByScope removes a plugin installation at a specific scope.
func (m *Manager) UninstallByScope(ctx context.Context, pluginID string, scope Scope) error {
	return m.uninstall(ctx, pluginID, &scope)
}

func (m *Manager) uninstall(ctx context.Context, pluginID string, scope *Scope) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, err := m.resolveTargetLocked(pluginID, scope)
	if err != nil {
		return err
	}

	// Remove from tracking
	m.deletePluginLocked(plugin.PluginID, plugin.Scope)

	// Remove from filesystem (unless local scope)
	if plugin.Scope != ScopeLocal && plugin.InstallPath != "" {
		if err := os.RemoveAll(plugin.InstallPath); err != nil {
			m.putPluginLocked(plugin)
			_ = m.rebuildRegistryLocked(ctx)
			return fmt.Errorf("remove plugin files: %w", err)
		}
	}

	if err := m.rebuildRegistryLocked(ctx); err != nil {
		return err
	}
	return nil
}

// ─── Lifecycle Controls ──────────────────────────────────────────────────────

// Enable activates a plugin so it will be loaded.
func (m *Manager) Enable(ctx context.Context, pluginID string) error {
	return m.enable(ctx, pluginID, nil)
}

// EnableByScope activates a plugin installation at a specific scope.
func (m *Manager) EnableByScope(ctx context.Context, pluginID string, scope Scope) error {
	return m.enable(ctx, pluginID, &scope)
}

func (m *Manager) enable(ctx context.Context, pluginID string, scope *Scope) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, err := m.resolveTargetLocked(pluginID, scope)
	if err != nil {
		return err
	}

	if plugin.State == StateEnabled {
		return nil // Already enabled
	}

	now := time.Now()
	plugin.State = StateEnabled
	plugin.EnabledAt = &now
	plugin.DisabledAt = nil
	plugin.Error = ""

	// Register capabilities for the resolved active installation.
	if err := m.rebuildRegistryLocked(ctx); err != nil {
		plugin.State = StateError
		plugin.Error = err.Error()
		_ = m.rebuildRegistryLocked(ctx)
		return err
	}

	return nil
}

// Disable deactivates a plugin so it won't be loaded.
func (m *Manager) Disable(ctx context.Context, pluginID string) error {
	return m.disable(ctx, pluginID, nil)
}

// DisableByScope deactivates a plugin installation at a specific scope.
func (m *Manager) DisableByScope(ctx context.Context, pluginID string, scope Scope) error {
	return m.disable(ctx, pluginID, &scope)
}

func (m *Manager) disable(ctx context.Context, pluginID string, scope *Scope) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, err := m.resolveTargetLocked(pluginID, scope)
	if err != nil {
		return err
	}

	if plugin.State == StateDisabled {
		return nil // Already disabled
	}

	now := time.Now()
	plugin.State = StateDisabled
	plugin.DisabledAt = &now
	plugin.Error = ""

	return m.rebuildRegistryLocked(ctx)
}

// Update updates a plugin to a new version.
func (m *Manager) Update(ctx context.Context, pluginID string, newManifest manifest.Manifest, newInstallPath string) error {
	return m.update(ctx, pluginID, nil, newManifest, newInstallPath)
}

// UpdateByScope updates a plugin installation at a specific scope.
func (m *Manager) UpdateByScope(ctx context.Context, pluginID string, scope Scope, newManifest manifest.Manifest, newInstallPath string) error {
	return m.update(ctx, pluginID, &scope, newManifest, newInstallPath)
}

func (m *Manager) update(ctx context.Context, pluginID string, scope *Scope, newManifest manifest.Manifest, newInstallPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, err := m.resolveTargetLocked(pluginID, scope)
	if err != nil {
		return err
	}

	// Validate new manifest
	if err := manifest.Validate(&newManifest); err != nil {
		return fmt.Errorf("invalid manifest: %v", err)
	}
	if newManifest.ID != plugin.PluginID {
		return fmt.Errorf("manifest ID %q does not match installed plugin %q", newManifest.ID, plugin.PluginID)
	}

	// Check version is actually newer
	if newManifest.Version == plugin.Manifest.Version {
		return fmt.Errorf("version %s is already installed", newManifest.Version)
	}

	wasEnabled := plugin.State == StateEnabled
	oldState := plugin.State
	oldManifest := cloneManifest(plugin.Manifest)
	oldPath := plugin.InstallPath
	oldUpdatedAt := plugin.UpdatedAt
	oldSourceVersion := plugin.Source.Version
	oldError := plugin.Error

	// Set updating state
	plugin.State = StateUpdating
	// Update plugin info
	plugin.Manifest = cloneManifest(newManifest)
	plugin.InstallPath = newInstallPath
	plugin.UpdatedAt = time.Now()
	plugin.Source.Version = newManifest.Version

	// Re-enable if was enabled
	if wasEnabled {
		plugin.State = StateEnabled
		if err := m.rebuildRegistryLocked(ctx); err != nil {
			plugin.State = StateError
			plugin.Error = err.Error()
			_ = m.rebuildRegistryLocked(ctx)
			return err
		}
	} else {
		plugin.State = StateDisabled
		if err := m.rebuildRegistryLocked(ctx); err != nil {
			return err
		}
	}

	// Clean up old installation path
	if oldPath != "" && oldPath != newInstallPath && plugin.Scope != ScopeLocal {
		if err := os.RemoveAll(oldPath); err != nil {
			plugin.State = oldState
			plugin.Manifest = oldManifest
			plugin.InstallPath = oldPath
			plugin.UpdatedAt = oldUpdatedAt
			plugin.Source.Version = oldSourceVersion
			plugin.Error = oldError
			_ = m.rebuildRegistryLocked(ctx)
			return fmt.Errorf("remove old plugin files: %w", err)
		}
	}

	return nil
}

// SetError marks a plugin as having an error.
func (m *Manager) SetError(pluginID string, err error) {
	m.setError(pluginID, nil, err)
}

// SetErrorByScope marks a plugin installation at a specific scope as having an error.
func (m *Manager) SetErrorByScope(pluginID string, scope Scope, err error) {
	m.setError(pluginID, &scope, err)
}

func (m *Manager) setError(pluginID string, scope *Scope, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if plugin, resolveErr := m.resolveTargetLocked(pluginID, scope); resolveErr == nil {
		plugin.State = StateError
		plugin.Error = err.Error()
		_ = m.rebuildRegistryLocked(context.Background())
	}
}

// ─── Scope Resolution ────────────────────────────────────────────────────────

// Resolve finds a plugin by ID, checking scopes in order (local → project → user).
// Returns the most specific (highest precedence) installation.
func (m *Manager) Resolve(pluginID string) (*InstalledPlugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, ok := m.resolveLocked(pluginID)
	if !ok {
		return nil, false
	}
	return cloneInstalledPlugin(plugin), true
}

// ResolveEnabled finds an enabled plugin by ID.
func (m *Manager) ResolveEnabled(pluginID string) (*InstalledPlugin, bool) {
	plugin, ok := m.Resolve(pluginID)
	if !ok || plugin.State != StateEnabled {
		return nil, false
	}
	return cloneInstalledPlugin(plugin), true
}

// ResolveByScope finds a plugin at a specific scope.
func (m *Manager) ResolveByScope(pluginID string, scope Scope) (*InstalledPlugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, ok := m.pluginByScopeLocked(pluginID, scope)
	if !ok || plugin.Scope != scope {
		return nil, false
	}
	return cloneInstalledPlugin(plugin), true
}

// ─── Listing ─────────────────────────────────────────────────────────────────

// List returns all installed plugins.
func (m *Manager) List() []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return clonePluginSlice(m.allPluginsLocked())
}

// ListByScope returns plugins installed at a specific scope.
func (m *Manager) ListByScope(scope Scope) []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*InstalledPlugin
	for _, byScope := range m.plugins {
		if p, ok := byScope[scope]; ok {
			result = append(result, p)
		}
	}

	sortPlugins(result)

	return clonePluginSlice(result)
}

// ListEnabled returns all enabled plugins.
func (m *Manager) ListEnabled() []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*InstalledPlugin
	for _, byScope := range m.plugins {
		for _, p := range byScope {
			if p.State == StateEnabled {
				result = append(result, p)
			}
		}
	}

	sortPlugins(result)

	return clonePluginSlice(result)
}

// ListWithSkillExport returns plugins that export skills.
func (m *Manager) ListWithSkillExport() []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*InstalledPlugin
	for _, byScope := range m.plugins {
		for _, p := range byScope {
			if p.State == StateEnabled && p.ExportSkills {
				result = append(result, p)
			}
		}
	}

	sortPlugins(result)

	return clonePluginSlice(result)
}

// ─── Registry Access ─────────────────────────────────────────────────────────

// Registry returns the capability registry.
func (m *Manager) Registry() *manifest.CapabilityRegistry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registry
}

// RefreshRegistry rebuilds the capability registry from enabled plugins.
func (m *Manager) RefreshRegistry() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.rebuildRegistryLocked(context.Background())
}

// ─── Configuration ───────────────────────────────────────────────────────────

// SetPluginConfig updates plugin-specific configuration.
func (m *Manager) SetPluginConfig(pluginID string, config map[string]any) error {
	return m.setPluginConfig(pluginID, nil, config)
}

// SetPluginConfigByScope updates plugin-specific configuration at a specific scope.
func (m *Manager) SetPluginConfigByScope(pluginID string, scope Scope, config map[string]any) error {
	return m.setPluginConfig(pluginID, &scope, config)
}

func (m *Manager) setPluginConfig(pluginID string, scope *Scope, config map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, err := m.resolveTargetLocked(pluginID, scope)
	if err != nil {
		return err
	}

	plugin.Config = cloneMap(config)
	return nil
}

// GetPluginConfig returns plugin-specific configuration.
func (m *Manager) GetPluginConfig(pluginID string) (map[string]any, error) {
	return m.getPluginConfig(pluginID, nil)
}

// GetPluginConfigByScope returns plugin-specific configuration at a specific scope.
func (m *Manager) GetPluginConfigByScope(pluginID string, scope Scope) (map[string]any, error) {
	return m.getPluginConfig(pluginID, &scope)
}

func (m *Manager) getPluginConfig(pluginID string, scope *Scope) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, err := m.resolveTargetLocked(pluginID, scope)
	if err != nil {
		return nil, err
	}

	return cloneMap(plugin.Config), nil
}

// SetSkillExport enables or disables skill export for a plugin.
func (m *Manager) SetSkillExport(pluginID string, export bool) error {
	return m.setSkillExport(pluginID, nil, export)
}

// SetSkillExportByScope enables or disables skill export for a plugin at a specific scope.
func (m *Manager) SetSkillExportByScope(pluginID string, scope Scope, export bool) error {
	return m.setSkillExport(pluginID, &scope, export)
}

func (m *Manager) setSkillExport(pluginID string, scope *Scope, export bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, err := m.resolveTargetLocked(pluginID, scope)
	if err != nil {
		return err
	}

	if export {
		if !m.cfg.AllowSkillExport {
			return fmt.Errorf("skill export is disabled in configuration")
		}
		if !plugin.Manifest.Capabilities.HasSkillExportCapability() {
			return fmt.Errorf("plugin manifest does not declare skill export capability")
		}
	}

	plugin.ExportSkills = export
	return nil
}

// ─── Persistence ─────────────────────────────────────────────────────────────

// PluginStateFile represents the persisted state of installed plugins.
type PluginStateFile struct {
	Version int                `json:"version"`
	Plugins []*InstalledPlugin `json:"plugins"`
}

// Save persists the current plugin state to disk.
func (m *Manager) Save() error {
	m.mu.RLock()
	userPlugins := m.pluginsByScopeLocked(ScopeUser)
	projectPlugins := m.pluginsByScopeLocked(ScopeProject)
	userDir := m.cfg.UserPluginDir
	projectDir := filepath.Join(m.projectDir, m.cfg.ProjectPluginDir)
	m.mu.RUnlock()

	// Save user-scope plugins, or remove stale state when the scope is empty.
	if len(userPlugins) > 0 {
		if err := m.savePluginsToDir(m.cfg.UserPluginDir, userPlugins); err != nil {
			return fmt.Errorf("save user plugins: %w", err)
		}
	} else if err := removePluginsStateFile(userDir); err != nil {
		return fmt.Errorf("remove stale user plugin state: %w", err)
	}

	// Save project-scope plugins, or remove stale state when the scope is empty.
	if len(projectPlugins) > 0 {
		if err := m.savePluginsToDir(projectDir, projectPlugins); err != nil {
			return fmt.Errorf("save project plugins: %w", err)
		}
	} else if err := removePluginsStateFile(projectDir); err != nil {
		return fmt.Errorf("remove stale project plugin state: %w", err)
	}

	return nil
}

func (m *Manager) pluginsByScopeLocked(scope Scope) []*InstalledPlugin {
	var result []*InstalledPlugin
	for _, byScope := range m.plugins {
		if p, ok := byScope[scope]; ok {
			result = append(result, cloneInstalledPlugin(p))
		}
	}
	sortPlugins(result)
	return result
}

func (m *Manager) savePluginsToDir(dir string, plugins []*InstalledPlugin) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	state := PluginStateFile{
		Version: 1,
		Plugins: plugins,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "plugins.json"), data, 0644)
}

func removePluginsStateFile(dir string) error {
	err := os.Remove(filepath.Join(dir, "plugins.json"))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

// Load restores plugin state from disk.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear existing state
	m.plugins = make(map[string]map[Scope]*InstalledPlugin)
	m.registry = manifest.NewCapabilityRegistry()

	// Load user-scope plugins
	if err := m.loadPluginsFromDir(m.cfg.UserPluginDir, ScopeUser); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load user plugins: %w", err)
	}

	// Load project-scope plugins (override user scope)
	projectDir := filepath.Join(m.projectDir, m.cfg.ProjectPluginDir)
	if err := m.loadPluginsFromDir(projectDir, ScopeProject); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load project plugins: %w", err)
	}

	return m.rebuildRegistryLocked(context.Background())
}

func (m *Manager) loadPluginsFromDir(dir string, scope Scope) error {
	path := filepath.Join(dir, "plugins.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var state PluginStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	for _, p := range state.Plugins {
		if p == nil || p.PluginID == "" {
			continue
		}
		cp := cloneInstalledPlugin(p)
		cp.Scope = scope
		m.putPluginLocked(cp)
	}

	return nil
}

// ─── Statistics ──────────────────────────────────────────────────────────────

// Stats returns lifecycle statistics.
type Stats struct {
	TotalInstalled   int            `json:"total_installed"`
	EnabledCount     int            `json:"enabled_count"`
	DisabledCount    int            `json:"disabled_count"`
	ErrorCount       int            `json:"error_count"`
	ByScope          map[string]int `json:"by_scope"`
	SkillExportCount int            `json:"skill_export_count"`
}

// Stats returns current lifecycle statistics.
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := Stats{
		ByScope: make(map[string]int),
	}

	for _, byScope := range m.plugins {
		for _, p := range byScope {
			stats.TotalInstalled++
			stats.ByScope[string(p.Scope)]++

			switch p.State {
			case StateEnabled:
				stats.EnabledCount++
			case StateDisabled:
				stats.DisabledCount++
			case StateError:
				stats.ErrorCount++
			}

			if p.ExportSkills {
				stats.SkillExportCount++
			}
		}
	}

	return stats
}
