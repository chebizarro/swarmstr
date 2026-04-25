package lifecycle

import (
	"context"
	"fmt"
	"testing"

	"metiq/internal/plugins/manifest"
)

func testManifest(id string) manifest.Manifest {
	return manifest.Manifest{
		SchemaVersion: 2,
		ID:            id,
		Version:       "1.0.0",
		Name:          "Test Plugin",
		Runtime:       manifest.RuntimeGoja,
		Capabilities: manifest.Capabilities{
			Tools: []manifest.ToolCapability{
				{Name: "test_tool", Description: "A test tool"},
			},
		},
	}
}

func testManifestWithSkillExport(id string) manifest.Manifest {
	mf := testManifest(id)
	mf.Capabilities.Skills = []manifest.SkillCapability{
		{ID: "test_skill", Name: "Test Skill", Exportable: true},
	}
	return mf
}

func TestNewManager(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, "/tmp/test-project")

	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	stats := mgr.Stats()
	if stats.TotalInstalled != 0 {
		t.Errorf("expected 0 plugins, got %d", stats.TotalInstalled)
	}
}

func TestInstallPlugin(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AutoEnable = false
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("test-plugin")
	ctx := context.Background()

	plugin, err := mgr.Install(ctx, mf, "/path/to/plugin", InstallOptions{
		Scope: ScopeProject,
		Source: InstallSource{
			Type:    "path",
			Path:    "/path/to/plugin",
			Version: "1.0.0",
		},
	})

	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if plugin.PluginID != "test-plugin" {
		t.Errorf("expected plugin ID 'test-plugin', got %q", plugin.PluginID)
	}
	if plugin.Scope != ScopeProject {
		t.Errorf("expected scope project, got %s", plugin.Scope)
	}
	if plugin.State != StateInstalled {
		t.Errorf("expected state installed, got %s", plugin.State)
	}

	// Verify in list
	list := mgr.List()
	if len(list) != 1 {
		t.Errorf("expected 1 plugin, got %d", len(list))
	}
}

func TestInstallWithAutoEnable(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AutoEnable = true
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("auto-enable-plugin")
	ctx := context.Background()

	plugin, err := mgr.Install(ctx, mf, "/path/to/plugin", InstallOptions{
		Scope: ScopeUser,
	})

	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if plugin.State != StateEnabled {
		t.Errorf("expected state enabled with auto-enable, got %s", plugin.State)
	}
	if plugin.EnabledAt == nil {
		t.Error("expected EnabledAt to be set")
	}
}

func TestInstallDuplicate(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("dup-plugin")
	ctx := context.Background()

	_, err := mgr.Install(ctx, mf, "/path/to/plugin", InstallOptions{Scope: ScopeProject})
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	// Try to install again without force
	_, err = mgr.Install(ctx, mf, "/path/to/plugin", InstallOptions{Scope: ScopeProject})
	if err == nil {
		t.Error("expected error for duplicate install")
	}

	// Install with force
	_, err = mgr.Install(ctx, mf, "/path/to/plugin", InstallOptions{Scope: ScopeProject, Force: true})
	if err != nil {
		t.Errorf("force install should succeed: %v", err)
	}
}

func TestEnableDisable(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AutoEnable = false
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("toggle-plugin")
	ctx := context.Background()

	mgr.Install(ctx, mf, "/path/to/plugin", InstallOptions{Scope: ScopeProject})

	// Enable
	err := mgr.Enable(ctx, "toggle-plugin")
	if err != nil {
		t.Fatalf("enable failed: %v", err)
	}

	plugin, _ := mgr.Resolve("toggle-plugin")
	if plugin.State != StateEnabled {
		t.Errorf("expected enabled, got %s", plugin.State)
	}

	// Disable
	err = mgr.Disable(ctx, "toggle-plugin")
	if err != nil {
		t.Fatalf("disable failed: %v", err)
	}

	plugin, _ = mgr.Resolve("toggle-plugin")
	if plugin.State != StateDisabled {
		t.Errorf("expected disabled, got %s", plugin.State)
	}
	if plugin.DisabledAt == nil {
		t.Error("expected DisabledAt to be set")
	}
}

func TestUninstall(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("uninstall-plugin")
	ctx := context.Background()

	mgr.Install(ctx, mf, "", InstallOptions{Scope: ScopeLocal})

	err := mgr.Uninstall(ctx, "uninstall-plugin")
	if err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	_, ok := mgr.Resolve("uninstall-plugin")
	if ok {
		t.Error("plugin should not exist after uninstall")
	}
}

func TestUninstallNotFound(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())

	ctx := context.Background()
	err := mgr.Uninstall(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent plugin")
	}
}

func TestUpdate(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("update-plugin")
	ctx := context.Background()

	mgr.Install(ctx, mf, "/path/v1", InstallOptions{Scope: ScopeProject, Enable: true})

	// Update to new version
	newMf := testManifest("update-plugin")
	newMf.Version = "2.0.0"

	err := mgr.Update(ctx, "update-plugin", newMf, "/path/v2")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	plugin, _ := mgr.Resolve("update-plugin")
	if plugin.Manifest.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %s", plugin.Manifest.Version)
	}
	if plugin.State != StateEnabled {
		t.Errorf("expected enabled after update, got %s", plugin.State)
	}
	if plugin.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestUpdateSameVersion(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("same-version-plugin")
	ctx := context.Background()

	mgr.Install(ctx, mf, "/path/v1", InstallOptions{Scope: ScopeProject})

	// Try to update with same version
	err := mgr.Update(ctx, "same-version-plugin", mf, "/path/v1")
	if err == nil {
		t.Error("expected error when updating to same version")
	}
}

func TestResolve(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("resolve-plugin")
	ctx := context.Background()

	mgr.Install(ctx, mf, "/path", InstallOptions{Scope: ScopeProject, Enable: true})

	// Resolve should find it
	plugin, ok := mgr.Resolve("resolve-plugin")
	if !ok {
		t.Fatal("expected to find plugin")
	}
	if plugin.PluginID != "resolve-plugin" {
		t.Errorf("expected 'resolve-plugin', got %q", plugin.PluginID)
	}

	// ResolveEnabled should find it (it's enabled)
	plugin, ok = mgr.ResolveEnabled("resolve-plugin")
	if !ok {
		t.Error("expected to find enabled plugin")
	}

	// Disable and ResolveEnabled should not find it
	mgr.Disable(ctx, "resolve-plugin")
	_, ok = mgr.ResolveEnabled("resolve-plugin")
	if ok {
		t.Error("should not find disabled plugin via ResolveEnabled")
	}

	// But Resolve should still find it
	_, ok = mgr.Resolve("resolve-plugin")
	if !ok {
		t.Error("should still find plugin via Resolve")
	}
}

func TestResolveByScope(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())

	mf := testManifest("scoped-plugin")
	ctx := context.Background()

	mgr.Install(ctx, mf, "/path", InstallOptions{Scope: ScopeProject})

	// Should find at correct scope
	_, ok := mgr.ResolveByScope("scoped-plugin", ScopeProject)
	if !ok {
		t.Error("should find plugin at project scope")
	}

	// Should not find at wrong scope
	_, ok = mgr.ResolveByScope("scoped-plugin", ScopeUser)
	if ok {
		t.Error("should not find plugin at user scope")
	}
}

func TestListByScope(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	// Install plugins at different scopes
	mgr.Install(ctx, testManifest("user-plugin-1"), "/path", InstallOptions{Scope: ScopeUser})
	mgr.Install(ctx, testManifest("user-plugin-2"), "/path", InstallOptions{Scope: ScopeUser})
	mgr.Install(ctx, testManifest("project-plugin"), "/path", InstallOptions{Scope: ScopeProject})
	mgr.Install(ctx, testManifest("local-plugin"), "/path", InstallOptions{Scope: ScopeLocal})

	userPlugins := mgr.ListByScope(ScopeUser)
	if len(userPlugins) != 2 {
		t.Errorf("expected 2 user plugins, got %d", len(userPlugins))
	}

	projectPlugins := mgr.ListByScope(ScopeProject)
	if len(projectPlugins) != 1 {
		t.Errorf("expected 1 project plugin, got %d", len(projectPlugins))
	}

	localPlugins := mgr.ListByScope(ScopeLocal)
	if len(localPlugins) != 1 {
		t.Errorf("expected 1 local plugin, got %d", len(localPlugins))
	}
}

func TestListEnabled(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AutoEnable = false
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mgr.Install(ctx, testManifest("plugin-1"), "/path", InstallOptions{Scope: ScopeProject})
	mgr.Install(ctx, testManifest("plugin-2"), "/path", InstallOptions{Scope: ScopeProject, Enable: true})
	mgr.Install(ctx, testManifest("plugin-3"), "/path", InstallOptions{Scope: ScopeProject, Enable: true})

	enabled := mgr.ListEnabled()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled plugins, got %d", len(enabled))
	}
}

func TestSkillExport(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AllowSkillExport = true
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mf := testManifestWithSkillExport("skill-plugin")

	plugin, err := mgr.Install(ctx, mf, "/path", InstallOptions{
		Scope:        ScopeProject,
		Enable:       true,
		ExportSkills: true,
	})

	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if !plugin.ExportSkills {
		t.Error("expected ExportSkills to be true")
	}

	// List plugins with skill export
	withExport := mgr.ListWithSkillExport()
	if len(withExport) != 1 {
		t.Errorf("expected 1 plugin with skill export, got %d", len(withExport))
	}
}

func TestSkillExportDisabled(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AllowSkillExport = false // Disabled
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mf := testManifestWithSkillExport("skill-plugin")

	_, err := mgr.Install(ctx, mf, "/path", InstallOptions{
		Scope:        ScopeProject,
		ExportSkills: true, // Trying to enable
	})

	if err == nil {
		t.Error("expected error when skill export is disabled")
	}
}

func TestSkillExportNoCapability(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AllowSkillExport = true
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mf := testManifest("no-skill-plugin") // No exportable skills

	_, err := mgr.Install(ctx, mf, "/path", InstallOptions{
		Scope:        ScopeProject,
		ExportSkills: true,
	})

	if err == nil {
		t.Error("expected error when plugin lacks skill export capability")
	}
}

func TestSetSkillExport(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AllowSkillExport = true
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mf := testManifestWithSkillExport("toggle-skill-plugin")
	mgr.Install(ctx, mf, "/path", InstallOptions{Scope: ScopeProject, Enable: true})

	// Enable skill export
	err := mgr.SetSkillExport("toggle-skill-plugin", true)
	if err != nil {
		t.Fatalf("SetSkillExport failed: %v", err)
	}

	plugin, _ := mgr.Resolve("toggle-skill-plugin")
	if !plugin.ExportSkills {
		t.Error("expected ExportSkills to be true")
	}

	// Disable skill export
	err = mgr.SetSkillExport("toggle-skill-plugin", false)
	if err != nil {
		t.Fatalf("SetSkillExport failed: %v", err)
	}

	plugin, _ = mgr.Resolve("toggle-skill-plugin")
	if plugin.ExportSkills {
		t.Error("expected ExportSkills to be false")
	}
}

func TestPluginConfig(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mf := testManifest("config-plugin")
	mgr.Install(ctx, mf, "/path", InstallOptions{
		Scope:  ScopeProject,
		Config: map[string]any{"setting": "value"},
	})

	// Get config
	config, err := mgr.GetPluginConfig("config-plugin")
	if err != nil {
		t.Fatalf("GetPluginConfig failed: %v", err)
	}
	if config["setting"] != "value" {
		t.Errorf("expected setting=value, got %v", config["setting"])
	}

	// Set new config
	err = mgr.SetPluginConfig("config-plugin", map[string]any{"new_setting": "new_value"})
	if err != nil {
		t.Fatalf("SetPluginConfig failed: %v", err)
	}

	config, _ = mgr.GetPluginConfig("config-plugin")
	if config["new_setting"] != "new_value" {
		t.Errorf("expected new_setting=new_value, got %v", config["new_setting"])
	}
}

func TestSetError(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mf := testManifest("error-plugin")
	mgr.Install(ctx, mf, "/path", InstallOptions{Scope: ScopeProject, Enable: true})

	mgr.SetError("error-plugin", fmt.Errorf("something went wrong"))

	plugin, _ := mgr.Resolve("error-plugin")
	if plugin.State != StateError {
		t.Errorf("expected error state, got %s", plugin.State)
	}
	if plugin.Error != "something went wrong" {
		t.Errorf("expected error message, got %q", plugin.Error)
	}
}

func TestRefreshRegistry(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mgr.Install(ctx, testManifest("plugin-1"), "/path", InstallOptions{Scope: ScopeProject, Enable: true})
	mgr.Install(ctx, testManifest("plugin-2"), "/path", InstallOptions{Scope: ScopeProject, Enable: true})

	err := mgr.RefreshRegistry()
	if err != nil {
		t.Fatalf("RefreshRegistry failed: %v", err)
	}

	// Registry should have tools from both plugins
	registry := mgr.Registry()
	tools := registry.Tools()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

func TestStats(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.AutoEnable = false
	cfg.AllowSkillExport = true
	mgr := NewManager(cfg, t.TempDir())
	ctx := context.Background()

	mgr.Install(ctx, testManifest("plugin-1"), "/path", InstallOptions{Scope: ScopeUser, Enable: true})
	mgr.Install(ctx, testManifest("plugin-2"), "/path", InstallOptions{Scope: ScopeProject, Enable: true})
	mgr.Install(ctx, testManifest("plugin-3"), "/path", InstallOptions{Scope: ScopeProject})
	mgr.Install(ctx, testManifestWithSkillExport("plugin-4"), "/path", InstallOptions{
		Scope: ScopeProject, Enable: true, ExportSkills: true,
	})

	stats := mgr.Stats()

	if stats.TotalInstalled != 4 {
		t.Errorf("expected 4 total, got %d", stats.TotalInstalled)
	}
	if stats.EnabledCount != 3 {
		t.Errorf("expected 3 enabled, got %d", stats.EnabledCount)
	}
	if stats.DisabledCount != 0 {
		t.Errorf("expected 0 disabled, got %d", stats.DisabledCount)
	}
	if stats.ByScope["user"] != 1 {
		t.Errorf("expected 1 user scope, got %d", stats.ByScope["user"])
	}
	if stats.ByScope["project"] != 3 {
		t.Errorf("expected 3 project scope, got %d", stats.ByScope["project"])
	}
	if stats.SkillExportCount != 1 {
		t.Errorf("expected 1 skill export, got %d", stats.SkillExportCount)
	}
}

func TestScopeIsValid(t *testing.T) {
	validScopes := []Scope{ScopeUser, ScopeProject, ScopeLocal}
	for _, s := range validScopes {
		if !s.IsValid() {
			t.Errorf("scope %s should be valid", s)
		}
	}

	invalidScope := Scope("invalid")
	if invalidScope.IsValid() {
		t.Error("invalid scope should not be valid")
	}
}

func TestAllScopes(t *testing.T) {
	scopes := AllScopes()
	if len(scopes) != 3 {
		t.Errorf("expected 3 scopes, got %d", len(scopes))
	}

	// Should be in order: local, project, user (most specific first)
	if scopes[0] != ScopeLocal || scopes[1] != ScopeProject || scopes[2] != ScopeUser {
		t.Error("scopes should be in order: local, project, user")
	}
}


