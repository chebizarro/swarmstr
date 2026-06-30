package main

// main_plugins.go — Plugin install, uninstall, update, and registry operations.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/plugins/installer"
	"metiq/internal/plugins/lifecycle"
	"metiq/internal/plugins/manifest"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Plugin install / uninstall / update
// ---------------------------------------------------------------------------

func applyPluginInstallRuntime(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.PluginsInstallRequest) (map[string]any, error) {
	svc := controlServices
	if svc == nil {
		// Build a minimal daemonServices so the method can call emitWSEvent
		// (which will be a no-op if no emitter is set).
		svc = &daemonServices{
			emitter:   controlWsEmitter,
			emitterMu: &controlWsEmitterMu,
		}
	}
	return svc.applyPluginInstallRuntime(ctx, docsRepo, configState, req)
}

func (s *daemonServices) applyPluginInstallRuntime(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.PluginsInstallRequest) (map[string]any, error) {
	cfg := configState.Get()
	install := map[string]any{}
	for key, value := range req.Install {
		install[key] = value
	}
	enableEntry := req.EnableEntry != nil && *req.EnableEntry
	includeLoadPath := req.IncludeLoadPath != nil && *req.IncludeLoadPath
	source := strings.ToLower(strings.TrimSpace(getString(install, "source")))
	sourcePath := strings.TrimSpace(getString(install, "sourcePath"))
	if sourcePath == "" {
		sourcePath = strings.TrimSpace(getString(install, "source_path"))
	}
	installPath := strings.TrimSpace(getString(install, "installPath"))
	if installPath == "" {
		installPath = strings.TrimSpace(getString(install, "install_path"))
	}
	spec := strings.TrimSpace(getString(install, "spec"))
	inst := installer.New()
	var installResult installer.Result
	switch source {
	case "path", "archive":
		if sourcePath == "" {
			return nil, fmt.Errorf("install.sourcePath is required for source=%s", source)
		}
		if _, err := os.Stat(sourcePath); err != nil {
			return nil, fmt.Errorf("install.sourcePath not accessible: %w", err)
		}
		if installPath == "" {
			if source == "path" {
				installPath = sourcePath
			} else {
				installPath = "./extensions/" + req.PluginID
			}
		}
		if source == "archive" {
			if !strings.HasSuffix(strings.ToLower(sourcePath), ".tar.gz") && !strings.HasSuffix(strings.ToLower(sourcePath), ".tgz") && !strings.HasSuffix(strings.ToLower(sourcePath), ".zip") {
				return nil, fmt.Errorf("install.sourcePath for archive must be .tar.gz, .tgz, or .zip file")
			}
			managedPath, ok := resolveManagedInstallPath(installPath)
			if !ok {
				return nil, fmt.Errorf("install.installPath for source=archive must be within managed extensions directory")
			}
			installPath = managedPath
			res, err := inst.ExtractArchive(ctx, sourcePath, installPath)
			if err != nil {
				log.Printf("plugins.install archive error for %s: %v", req.PluginID, err)
				return nil, fmt.Errorf("archive extraction failed: %w", err)
			}
			installResult = res
		}
		install["installPath"] = installPath
	case "npm":
		if spec == "" {
			return nil, fmt.Errorf("install.spec is required for source=npm")
		}
		if !isValidNPMSpec(spec) {
			return nil, fmt.Errorf("install.spec contains invalid or unsafe characters")
		}
		if installPath == "" {
			installPath = "./extensions/" + req.PluginID
		}
		managedPath, ok := resolveManagedInstallPath(installPath)
		if !ok {
			return nil, fmt.Errorf("install.installPath for source=npm must be within managed extensions directory")
		}
		installPath = managedPath
		res, err := inst.InstallNPM(ctx, spec, installPath)
		if err != nil {
			log.Printf("plugins.install npm error for %s: %v\nstdout: %s\nstderr: %s", req.PluginID, err, res.Stdout, res.Stderr)
			return nil, fmt.Errorf("npm install failed: %w", err)
		}
		installResult = res
		if installResult.ResolvedVersion != "" {
			install["version"] = installResult.ResolvedVersion
		}
		if installResult.ResolvedSpec != "" {
			install["resolvedSpec"] = installResult.ResolvedSpec
		}
		if installResult.Integrity != "" {
			install["integrity"] = installResult.Integrity
		}
		install["installPath"] = installPath
	case "git":
		repo := strings.TrimSpace(getString(install, "url"))
		if repo == "" {
			repo = sourcePath
		}
		if repo == "" {
			repo = spec
		}
		if repo == "" {
			return nil, fmt.Errorf("install.url or install.spec is required for source=git")
		}
		ref := strings.TrimSpace(getString(install, "ref"))
		if installPath == "" {
			installPath = "./extensions/" + req.PluginID
		}
		managedPath, ok := resolveManagedInstallPath(installPath)
		if !ok {
			return nil, fmt.Errorf("install.installPath for source=git must be within managed extensions directory")
		}
		installPath = managedPath
		res, err := inst.InstallGit(ctx, repo, ref, installPath)
		if err != nil {
			log.Printf("plugins.install git error for %s: %v\nstdout: %s\nstderr: %s", req.PluginID, err, res.Stdout, res.Stderr)
			return nil, fmt.Errorf("git install failed: %w", err)
		}
		installResult = res
		install["url"] = repo
		if ref != "" {
			install["ref"] = ref
		}
		if installResult.ResolvedVersion != "" {
			install["version"] = installResult.ResolvedVersion
		}
		if installResult.ResolvedSpec != "" {
			install["resolvedSpec"] = installResult.ResolvedSpec
		}
		install["installPath"] = installPath
	case "url":
		srcURL := strings.TrimSpace(getString(install, "url"))
		if srcURL == "" {
			srcURL = sourcePath
		}
		if srcURL == "" {
			return nil, fmt.Errorf("install.url is required for source=url")
		}
		tmpFile, err := installer.DownloadURL(ctx, srcURL)
		if err != nil {
			log.Printf("plugins.install url download error for %s: %v", req.PluginID, err)
			return nil, fmt.Errorf("URL download failed: %w", err)
		}
		defer os.Remove(tmpFile)

		// Determine whether the download is an archive or a JS file.
		lower := strings.ToLower(tmpFile)
		if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".zip") {
			// Archive: extract to managed path.
			if installPath == "" {
				installPath = "./extensions/" + req.PluginID
			}
			managedPath, ok := resolveManagedInstallPath(installPath)
			if !ok {
				return nil, fmt.Errorf("install.installPath for source=url archive must be within managed extensions directory")
			}
			installPath = managedPath
			res, err := inst.ExtractArchive(ctx, tmpFile, installPath)
			if err != nil {
				log.Printf("plugins.install url archive error for %s: %v", req.PluginID, err)
				return nil, fmt.Errorf("archive extraction failed: %w", err)
			}
			installResult = res
		} else {
			// Single JS file: copy to managed directory.
			if installPath == "" {
				installPath = "./extensions/" + req.PluginID
			}
			managedPath, ok := resolveManagedInstallPath(installPath)
			if !ok {
				return nil, fmt.Errorf("install.installPath for source=url must be within managed extensions directory")
			}
			if err := os.MkdirAll(managedPath, 0o755); err != nil {
				return nil, fmt.Errorf("create install directory: %w", err)
			}
			destFile := filepath.Join(managedPath, "index.js")
			data, err := os.ReadFile(tmpFile)
			if err != nil {
				return nil, fmt.Errorf("read downloaded file: %w", err)
			}
			if err := os.WriteFile(destFile, data, 0o644); err != nil {
				return nil, fmt.Errorf("write plugin file: %w", err)
			}
			installPath = managedPath
		}
		install["url"] = srcURL
		install["installPath"] = installPath
	default:
		return nil, fmt.Errorf("unsupported install.source %q", source)
	}
	lc := lifecycle.NewManager(lifecycle.DefaultLifecycleConfig(), ".")
	if err := lc.LoadFromConfig(cfg); err != nil {
		return nil, err
	}
	mf := lifecycleManifestFromInstall(req.PluginID, install)
	autoEnable := false
	installed, err := lc.Install(ctx, mf, installPath, lifecycle.InstallOptions{
		Scope:      lifecycle.ScopeProject,
		Source:     lifecycleInstallSource(install),
		Enable:     enableEntry,
		AutoEnable: &autoEnable,
		Force:      true,
	})
	if err != nil {
		return nil, err
	}
	next := lc.ApplyToConfig(cfg)
	if includeLoadPath && source == "path" && sourcePath != "" {
		next = appendPluginLoadPath(next, sourcePath)
	}
	if err := persistRuntimeConfigFile(next); err != nil {
		return nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return nil, err
	}
	configState.Set(next)
	record := lifecycleInstallRecord(installed)
	result := map[string]any{
		"ok":       true,
		"pluginId": req.PluginID,
		"install":  record,
		"enabled":  enableEntry,
		"source":   source,
	}
	if installResult.Stdout != "" {
		result["stdout"] = installResult.Stdout
	}
	if installResult.Stderr != "" {
		result["stderr"] = installResult.Stderr
	}
	// Notify WS clients that a plugin was installed.
	version := ""
	if v, ok := record["version"].(string); ok {
		version = v
	}
	s.emitWSEvent(gatewayws.EventPluginLoaded, gatewayws.PluginLoadedPayload{
		TS:       time.Now().UnixMilli(),
		PluginID: req.PluginID,
		Version:  version,
		Action:   "installed",
	})
	return result, nil
}

func applyPluginUninstallRuntime(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.PluginsUninstallRequest) (map[string]any, error) {
	cfg := configState.Get()
	var installRecord map[string]any
	if cfg.Extra != nil {
		if rawExt, ok := cfg.Extra["extensions"].(map[string]any); ok {
			if rawInstalls, ok := rawExt["installs"].(map[string]any); ok {
				installRecord, _ = rawInstalls[req.PluginID].(map[string]any)
			}
		}
	}
	lc := lifecycle.NewManager(lifecycle.DefaultLifecycleConfig(), ".")
	if err := lc.LoadFromConfig(cfg); err != nil {
		return nil, err
	}
	if err := lc.Uninstall(ctx, req.PluginID); err != nil {
		return nil, state.ErrNotFound
	}
	next := lc.ApplyToConfig(cfg)
	var actions methods.PluginUninstallActions
	actions.Entry = true
	actions.Install = true
	if err := persistRuntimeConfigFile(next); err != nil {
		return nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return nil, err
	}
	configState.Set(next)
	warnings := []string{}
	deletedFiles := false
	if installRecord != nil {
		source := strings.ToLower(strings.TrimSpace(getString(installRecord, "source")))
		installPath := strings.TrimSpace(getString(installRecord, "installPath"))
		if source != "path" && installPath != "" {
			if candidate, ok := resolveManagedInstallPath(installPath); ok {
				if err := os.RemoveAll(candidate); err != nil {
					warnings = append(warnings, fmt.Sprintf("failed to remove installPath %s: %v", candidate, err))
				} else {
					deletedFiles = true
				}
			} else {
				warnings = append(warnings, fmt.Sprintf("skipped uninstall deletion for unmanaged installPath %s", installPath))
			}
		}
	}
	return map[string]any{"ok": true, "pluginId": req.PluginID, "actions": actions, "deletedFiles": deletedFiles, "warnings": warnings}, nil
}

func applyPluginUpdateRuntime(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.PluginsUpdateRequest) (map[string]any, error) {
	cfg := configState.Get()
	runner := func(pluginID string, record map[string]any, dryRun bool) methods.PluginUpdateResult {
		currentVersion := strings.TrimSpace(getString(record, "version"))
		source := strings.ToLower(strings.TrimSpace(getString(record, "source")))
		spec := strings.TrimSpace(getString(record, "spec"))
		if source == "git" && spec == "" {
			spec = strings.TrimSpace(getString(record, "url"))
		}
		pinned := ""
		if source != "git" {
			pinned = parsePinnedNPMVersion(spec)
		}
		if pinned != "" && pinned == currentVersion {
			return methods.PluginUpdateResult{
				Status:      methods.PluginUpdateStatusUnchanged,
				Message:     fmt.Sprintf("%s already at %s.", pluginID, currentVersion),
				NextVersion: currentVersion,
			}
		}
		if dryRun {
			nextVersion := pinned
			if nextVersion == "" {
				nextVersion = currentVersion
			}
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusUpdated, Message: fmt.Sprintf("Would update %s.", pluginID), NextVersion: nextVersion}
		}
		installPath := strings.TrimSpace(getString(record, "installPath"))
		if installPath == "" {
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusError, Message: fmt.Sprintf("No installPath for %s.", pluginID)}
		}
		// Safety: only allow updating within managed extensions directory
		if _, ok := resolveManagedInstallPath(installPath); !ok {
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusError, Message: fmt.Sprintf("installPath for %s is outside managed directory.", pluginID)}
		}
		inst := installer.New()
		var res installer.Result
		var err error
		if source == "git" {
			res, err = inst.UpdateGit(ctx, spec, strings.TrimSpace(getString(record, "ref")), installPath)
		} else {
			res, err = inst.UpdateNPM(ctx, spec, installPath)
		}
		if err != nil {
			log.Printf("plugins.update %s error for %s: %v\nstdout: %s\nstderr: %s", source, pluginID, err, res.Stdout, res.Stderr)
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusError, Message: fmt.Sprintf("%s update failed: %v", source, err)}
		}
		nextVersion := res.ResolvedVersion
		if nextVersion == "" {
			nextVersion = pinned
		}
		if nextVersion == "" {
			nextVersion = currentVersion
		}
		patch := map[string]any{}
		if res.ResolvedSpec != "" {
			patch["resolvedSpec"] = res.ResolvedSpec
		}
		if res.Integrity != "" {
			patch["integrity"] = res.Integrity
		}
		status := methods.PluginUpdateStatusUpdated
		if nextVersion == currentVersion && nextVersion != "" {
			status = methods.PluginUpdateStatusUnchanged
		}
		return methods.PluginUpdateResult{
			Status:      status,
			Message:     fmt.Sprintf("Updated %s to %s.", pluginID, nextVersion),
			NextVersion: nextVersion,
			InstallPath: res.InstallPath,
			RecordPatch: patch,
		}
	}
	lc := lifecycle.NewManager(lifecycle.DefaultLifecycleConfig(), ".")
	if err := lc.LoadFromConfig(cfg); err != nil {
		return nil, err
	}
	compatCfg := lc.ApplyToConfig(cfg)
	next, changed, outcomes := methods.ApplyPluginUpdateOperation(compatCfg, req.PluginIDs, req.DryRun, runner)
	if changed && !req.DryRun {
		removePluginLifecycleConfig(next)
		if err := lc.LoadFromConfig(next); err != nil {
			return nil, err
		}
		next = lc.ApplyToConfig(cfg)
		if err := persistRuntimeConfigFile(next); err != nil {
			return nil, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nil, err
		}
		configState.Set(next)
	}
	return map[string]any{"ok": true, "changed": changed, "outcomes": outcomes}, nil
}

func lifecycleManifestFromInstall(pluginID string, install map[string]any) manifest.Manifest {
	version := strings.TrimSpace(getString(install, "version"))
	if version == "" {
		version = "0.0.0"
	}
	runtime := manifest.RuntimeGoja
	if pluginType := strings.TrimSpace(getString(install, "plugin_type")); strings.EqualFold(pluginType, "node") || strings.EqualFold(pluginType, "nodejs") {
		runtime = manifest.RuntimeNode
	}
	return manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		ID:            pluginID,
		Version:       version,
		Name:          pluginID,
		Runtime:       runtime,
	}
}

func lifecycleInstallSource(install map[string]any) lifecycle.InstallSource {
	sourceType := strings.ToLower(strings.TrimSpace(getString(install, "source")))
	return lifecycle.InstallSource{
		Type:    sourceType,
		URL:     strings.TrimSpace(getString(install, "url")),
		Package: strings.TrimSpace(getString(install, "spec")),
		Version: strings.TrimSpace(getString(install, "version")),
		Path:    strings.TrimSpace(getString(install, "sourcePath")),
	}
}

func lifecycleInstallRecord(plugin *lifecycle.InstalledPlugin) map[string]any {
	if plugin == nil {
		return map[string]any{}
	}
	record := map[string]any{
		"source":      plugin.Source.Type,
		"installPath": plugin.InstallPath,
	}
	if plugin.Source.Path != "" {
		record["sourcePath"] = plugin.Source.Path
	}
	if plugin.Source.Package != "" {
		record["spec"] = plugin.Source.Package
	}
	if plugin.Source.URL != "" {
		record["url"] = plugin.Source.URL
	}
	if plugin.Source.Version != "" {
		record["version"] = plugin.Source.Version
	} else if plugin.Manifest.Version != "" {
		record["version"] = plugin.Manifest.Version
	}
	return record
}

func appendPluginLoadPath(cfg state.ConfigDoc, pathValue string) state.ConfigDoc {
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	rawExt, _ := cfg.Extra["extensions"].(map[string]any)
	if rawExt == nil {
		rawExt = map[string]any{}
		cfg.Extra["extensions"] = rawExt
	}
	loadPaths := []any{}
	seen := map[string]struct{}{}
	if existing, ok := rawExt["load_paths"].([]any); ok {
		for _, item := range existing {
			if s := strings.TrimSpace(getString(map[string]any{"v": item}, "v")); s != "" {
				if _, dup := seen[s]; !dup {
					seen[s] = struct{}{}
					loadPaths = append(loadPaths, s)
				}
			}
		}
	}
	if _, dup := seen[pathValue]; !dup {
		loadPaths = append(loadPaths, pathValue)
	}
	rawExt["load_paths"] = loadPaths
	return cfg
}

func removePluginLifecycleConfig(cfg state.ConfigDoc) {
	if cfg.Extra == nil {
		return
	}
	if rawExt, ok := cfg.Extra["extensions"].(map[string]any); ok {
		delete(rawExt, "lifecycle")
	}
}

// ─── Plugin registry handlers ──────────────────────────────────────────────────

// resolveRegistryURL returns the registry URL to use for a request:
// the request's RegistryURL (if set) or the daemon's configured registry URL.
func resolveRegistryURL(configState *runtimeConfigStore, requestURL string) (string, error) {
	u := strings.TrimSpace(requestURL)
	if u != "" {
		return u, nil
	}
	cfg := configState.Get()
	if cfg.Extra != nil {
		if rawExt, ok := cfg.Extra["extensions"].(map[string]any); ok {
			if regURL, ok := rawExt["registry_url"].(string); ok && strings.TrimSpace(regURL) != "" {
				return strings.TrimSpace(regURL), nil
			}
		}
	}
	// Fall back to the default public registry.
	return "https://registry.metiq.com/plugins/index.json", nil
}

func handlePluginsRegistryList(ctx context.Context, configState *runtimeConfigStore, req methods.PluginsRegistryListRequest) (map[string]any, error) {
	regURL, err := resolveRegistryURL(configState, req.RegistryURL)
	if err != nil {
		return nil, err
	}
	idx, err := installer.FetchRegistry(ctx, regURL)
	if err != nil {
		return nil, fmt.Errorf("registry fetch failed: %w", err)
	}
	plugins := make([]map[string]any, 0, len(idx.Plugins))
	for _, p := range idx.Plugins {
		plugins = append(plugins, pluginEntryToMap(p))
	}
	return map[string]any{
		"ok":          true,
		"registryURL": regURL,
		"version":     idx.Version,
		"plugins":     plugins,
		"count":       len(plugins),
	}, nil
}

func handlePluginsRegistryGet(ctx context.Context, configState *runtimeConfigStore, req methods.PluginsRegistryGetRequest) (map[string]any, error) {
	if strings.TrimSpace(req.PluginID) == "" {
		return nil, fmt.Errorf("plugin_id is required")
	}
	regURL, err := resolveRegistryURL(configState, req.RegistryURL)
	if err != nil {
		return nil, err
	}
	idx, err := installer.FetchRegistry(ctx, regURL)
	if err != nil {
		return nil, fmt.Errorf("registry fetch failed: %w", err)
	}
	for _, p := range idx.Plugins {
		if strings.EqualFold(p.ID, req.PluginID) {
			return map[string]any{
				"ok":          true,
				"registryURL": regURL,
				"plugin":      pluginEntryToMap(p),
			}, nil
		}
	}
	return nil, state.ErrNotFound
}

func handlePluginsRegistrySearch(ctx context.Context, configState *runtimeConfigStore, req methods.PluginsRegistrySearchRequest) (map[string]any, error) {
	regURL, err := resolveRegistryURL(configState, req.RegistryURL)
	if err != nil {
		return nil, err
	}
	idx, err := installer.FetchRegistry(ctx, regURL)
	if err != nil {
		return nil, fmt.Errorf("registry fetch failed: %w", err)
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	tag := strings.ToLower(strings.TrimSpace(req.Tag))
	results := make([]map[string]any, 0)
	for _, p := range idx.Plugins {
		if tag != "" {
			matched := false
			for _, t := range p.Tags {
				if strings.EqualFold(t, tag) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if query != "" {
			haystack := strings.ToLower(p.ID + " " + p.Name + " " + p.Description + " " + strings.Join(p.Tags, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		results = append(results, pluginEntryToMap(p))
	}
	return map[string]any{
		"ok":          true,
		"registryURL": regURL,
		"query":       req.Query,
		"tag":         req.Tag,
		"plugins":     results,
		"count":       len(results),
	}, nil
}

func pluginEntryToMap(p installer.RegistryPlugin) map[string]any {
	m := map[string]any{
		"id":          p.ID,
		"name":        p.Name,
		"description": p.Description,
		"url":         p.URL,
	}
	if p.Version != "" {
		m["version"] = p.Version
	}
	if p.Type != "" {
		m["type"] = p.Type
	}
	if p.Author != "" {
		m["author"] = p.Author
	}
	if p.License != "" {
		m["license"] = p.License
	}
	if len(p.Tags) > 0 {
		m["tags"] = p.Tags
	}
	return m
}

func isValidNPMSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	if len(spec) > 500 {
		return false
	}
	if strings.ContainsAny(spec, ";|&$`\n\r\t<>(){}") {
		return false
	}
	if strings.Contains(spec, "  ") {
		return false
	}
	return true
}

func parsePinnedNPMVersion(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	at := strings.LastIndex(spec, "@")
	if at <= 0 || at == len(spec)-1 {
		return ""
	}
	version := strings.TrimSpace(spec[at+1:])
	if version == "" || strings.EqualFold(version, "latest") {
		return ""
	}
	return version
}

func resolveManagedInstallPath(pathValue string) (string, bool) {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return "", false
	}
	root, err := filepath.Abs("./extensions")
	if err != nil {
		return "", false
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	candidate, err := filepath.Abs(filepath.Clean(pathValue))
	if err != nil {
		return "", false
	}
	candidateResolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		candidateResolved = candidate
	}
	rel, err := filepath.Rel(rootResolved, candidateResolved)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", false
	}
	return candidate, true
}
