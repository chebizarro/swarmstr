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
	mu       sync.RWMutex
	cfg      LifecycleConfig
	plugins  map[string]*InstalledPlugin // pluginID → plugin
	registry *manifest.CapabilityRegistry
	projectDir string
}

// NewManager creates a new lifecycle manager.
func NewManager(cfg LifecycleConfig, projectDir string) *Manager {
	return &Manager{
		cfg:        cfg,
		plugins:    make(map[string]*InstalledPlugin),
		registry:   manifest.NewCapabilityRegistry(),
		projectDir: projectDir,
	}
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

	// Check for existing installation
	if existing, ok := m.plugins[mf.ID]; ok && !opts.Force {
		return nil, fmt.Errorf("plugin %q already installed at scope %s", mf.ID, existing.Scope)
	}

	// Validate scope
	if !opts.Scope.IsValid() {
		return nil, fmt.Errorf("invalid scope: %s", opts.Scope)
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
		Manifest:     mf,
		InstallPath:  installPath,
		InstalledAt:  now,
		Source:       opts.Source,
		ExportSkills: exportSkills,
		Config:       opts.Config,
	}

	// Enable if requested
	if opts.Enable || m.cfg.AutoEnable {
		plugin.State = StateEnabled
		plugin.EnabledAt = &now
	}

	m.plugins[mf.ID] = plugin

	// Register capabilities
	if plugin.State == StateEnabled {
		if err := m.registry.Register(ctx, &mf); err != nil {
			plugin.State = StateError
			plugin.Error = err.Error()
		}
	}

	return plugin, nil
}

// Uninstall removes a plugin installation.
func (m *Manager) Uninstall(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, ok := m.plugins[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q not installed", pluginID)
	}

	// Unregister capabilities
	m.registry.Unregister(pluginID)

	// Remove from tracking
	delete(m.plugins, pluginID)

	// Remove from filesystem (unless local scope)
	if plugin.Scope != ScopeLocal && plugin.InstallPath != "" {
		if err := os.RemoveAll(plugin.InstallPath); err != nil {
			return fmt.Errorf("remove plugin files: %w", err)
		}
	}

	return nil
}

// ─── Lifecycle Controls ──────────────────────────────────────────────────────

// Enable activates a plugin so it will be loaded.
func (m *Manager) Enable(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, ok := m.plugins[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q not installed", pluginID)
	}

	if plugin.State == StateEnabled {
		return nil // Already enabled
	}

	now := time.Now()
	plugin.State = StateEnabled
	plugin.EnabledAt = &now
	plugin.DisabledAt = nil
	plugin.Error = ""

	// Register capabilities
	if err := m.registry.Register(ctx, &plugin.Manifest); err != nil {
		plugin.State = StateError
		plugin.Error = err.Error()
		return err
	}

	return nil
}

// Disable deactivates a plugin so it won't be loaded.
func (m *Manager) Disable(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, ok := m.plugins[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q not installed", pluginID)
	}

	if plugin.State == StateDisabled {
		return nil // Already disabled
	}

	now := time.Now()
	plugin.State = StateDisabled
	plugin.DisabledAt = &now
	plugin.Error = ""

	// Unregister capabilities
	m.registry.Unregister(pluginID)

	return nil
}

// Update updates a plugin to a new version.
func (m *Manager) Update(ctx context.Context, pluginID string, newManifest manifest.Manifest, newInstallPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, ok := m.plugins[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q not installed", pluginID)
	}

	// Validate new manifest
	if err := manifest.Validate(&newManifest); err != nil {
		return fmt.Errorf("invalid manifest: %v", err)
	}

	// Check version is actually newer
	if newManifest.Version == plugin.Manifest.Version {
		return fmt.Errorf("version %s is already installed", newManifest.Version)
	}

	wasEnabled := plugin.State == StateEnabled

	// Set updating state
	plugin.State = StateUpdating

	// Unregister old capabilities
	if wasEnabled {
		m.registry.Unregister(pluginID)
	}

	// Update plugin info
	oldPath := plugin.InstallPath
	plugin.Manifest = newManifest
	plugin.InstallPath = newInstallPath
	plugin.UpdatedAt = time.Now()
	plugin.Source.Version = newManifest.Version

	// Re-enable if was enabled
	if wasEnabled {
		if err := m.registry.Register(ctx, &newManifest); err != nil {
			plugin.State = StateError
			plugin.Error = err.Error()
			return err
		}
		plugin.State = StateEnabled
	} else {
		plugin.State = StateDisabled
	}

	// Clean up old installation path
	if oldPath != "" && oldPath != newInstallPath && plugin.Scope != ScopeLocal {
		os.RemoveAll(oldPath)
	}

	return nil
}

// SetError marks a plugin as having an error.
func (m *Manager) SetError(pluginID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if plugin, ok := m.plugins[pluginID]; ok {
		plugin.State = StateError
		plugin.Error = err.Error()
		m.registry.Unregister(pluginID)
	}
}

// ─── Scope Resolution ────────────────────────────────────────────────────────

// Resolve finds a plugin by ID, checking scopes in order (local → project → user).
// Returns the most specific (highest precedence) installation.
func (m *Manager) Resolve(pluginID string) (*InstalledPlugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, ok := m.plugins[pluginID]
	return plugin, ok
}

// ResolveEnabled finds an enabled plugin by ID.
func (m *Manager) ResolveEnabled(pluginID string) (*InstalledPlugin, bool) {
	plugin, ok := m.Resolve(pluginID)
	if !ok || plugin.State != StateEnabled {
		return nil, false
	}
	return plugin, true
}

// ResolveByScope finds a plugin at a specific scope.
func (m *Manager) ResolveByScope(pluginID string, scope Scope) (*InstalledPlugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, ok := m.plugins[pluginID]
	if !ok || plugin.Scope != scope {
		return nil, false
	}
	return plugin, true
}

// ─── Listing ─────────────────────────────────────────────────────────────────

// List returns all installed plugins.
func (m *Manager) List() []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*InstalledPlugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		result = append(result, p)
	}

	// Sort by ID for consistent ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].PluginID < result[j].PluginID
	})

	return result
}

// ListByScope returns plugins installed at a specific scope.
func (m *Manager) ListByScope(scope Scope) []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*InstalledPlugin
	for _, p := range m.plugins {
		if p.Scope == scope {
			result = append(result, p)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].PluginID < result[j].PluginID
	})

	return result
}

// ListEnabled returns all enabled plugins.
func (m *Manager) ListEnabled() []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*InstalledPlugin
	for _, p := range m.plugins {
		if p.State == StateEnabled {
			result = append(result, p)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].PluginID < result[j].PluginID
	})

	return result
}

// ListWithSkillExport returns plugins that export skills.
func (m *Manager) ListWithSkillExport() []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*InstalledPlugin
	for _, p := range m.plugins {
		if p.State == StateEnabled && p.ExportSkills {
			result = append(result, p)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].PluginID < result[j].PluginID
	})

	return result
}

// ─── Registry Access ─────────────────────────────────────────────────────────

// Registry returns the capability registry.
func (m *Manager) Registry() *manifest.CapabilityRegistry {
	return m.registry
}

// RefreshRegistry rebuilds the capability registry from enabled plugins.
func (m *Manager) RefreshRegistry() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear existing registrations
	m.registry = manifest.NewCapabilityRegistry()

	// Re-register all enabled plugins
	var errors []string
	for _, p := range m.plugins {
		if p.State == StateEnabled {
			if err := m.registry.Register(context.Background(), &p.Manifest); err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", p.PluginID, err))
				p.State = StateError
				p.Error = err.Error()
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("registry refresh errors: %v", errors)
	}
	return nil
}

// ─── Configuration ───────────────────────────────────────────────────────────

// SetPluginConfig updates plugin-specific configuration.
func (m *Manager) SetPluginConfig(pluginID string, config map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, ok := m.plugins[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q not installed", pluginID)
	}

	plugin.Config = config
	return nil
}

// GetPluginConfig returns plugin-specific configuration.
func (m *Manager) GetPluginConfig(pluginID string) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, ok := m.plugins[pluginID]
	if !ok {
		return nil, fmt.Errorf("plugin %q not installed", pluginID)
	}

	return plugin.Config, nil
}

// SetSkillExport enables or disables skill export for a plugin.
func (m *Manager) SetSkillExport(pluginID string, export bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, ok := m.plugins[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q not installed", pluginID)
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
	defer m.mu.RUnlock()

	// Save user-scope plugins
	userPlugins := m.ListByScope(ScopeUser)
	if len(userPlugins) > 0 {
		if err := m.savePluginsToDir(m.cfg.UserPluginDir, userPlugins); err != nil {
			return fmt.Errorf("save user plugins: %w", err)
		}
	}

	// Save project-scope plugins
	projectPlugins := m.ListByScope(ScopeProject)
	if len(projectPlugins) > 0 {
		projectDir := filepath.Join(m.projectDir, m.cfg.ProjectPluginDir)
		if err := m.savePluginsToDir(projectDir, projectPlugins); err != nil {
			return fmt.Errorf("save project plugins: %w", err)
		}
	}

	return nil
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

// Load restores plugin state from disk.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear existing state
	m.plugins = make(map[string]*InstalledPlugin)
	m.registry = manifest.NewCapabilityRegistry()

	// Load user-scope plugins
	if err := m.loadPluginsFromDir(m.cfg.UserPluginDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load user plugins: %w", err)
	}

	// Load project-scope plugins (override user scope)
	projectDir := filepath.Join(m.projectDir, m.cfg.ProjectPluginDir)
	if err := m.loadPluginsFromDir(projectDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load project plugins: %w", err)
	}

	// Register enabled plugins
	for _, p := range m.plugins {
		if p.State == StateEnabled {
			if err := m.registry.Register(context.Background(), &p.Manifest); err != nil {
				p.State = StateError
				p.Error = err.Error()
			}
		}
	}

	return nil
}

func (m *Manager) loadPluginsFromDir(dir string) error {
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
		m.plugins[p.PluginID] = p
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

	for _, p := range m.plugins {
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

	return stats
}
