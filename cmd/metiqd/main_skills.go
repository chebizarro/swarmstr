package main

// main_skills.go — Skills catalog, installation, and update operations.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	skillspkg "metiq/internal/skills"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Skill config helpers
// ---------------------------------------------------------------------------

func extractSkillEntries(cfg state.ConfigDoc) map[string]map[string]any {
	out := map[string]map[string]any{}
	if cfg.Extra == nil {
		return out
	}
	rawSkills, ok := cfg.Extra["skills"].(map[string]any)
	if !ok {
		return out
	}
	rawEntries, ok := rawSkills["entries"].(map[string]any)
	if !ok {
		return out
	}
	for key, value := range rawEntries {
		entryMap, ok := value.(map[string]any)
		if !ok {
			continue
		}
		copyEntry := map[string]any{}
		for ek, ev := range entryMap {
			copyEntry[ek] = ev
		}
		out[key] = copyEntry
	}
	return out
}

func configWithSkillEntries(cfg state.ConfigDoc, entries map[string]map[string]any) state.ConfigDoc {
	next := cfg
	if next.Extra == nil {
		next.Extra = map[string]any{}
	}
	rawSkills := map[string]any{}
	if existing, ok := next.Extra["skills"].(map[string]any); ok {
		for key, value := range existing {
			rawSkills[key] = value
		}
	}
	rawEntries := map[string]any{}
	for key, entry := range entries {
		entryCopy := map[string]any{}
		for ek, ev := range entry {
			entryCopy[ek] = ev
		}
		rawEntries[key] = entryCopy
	}
	rawSkills["entries"] = rawEntries
	next.Extra["skills"] = rawSkills
	return next
}

func buildSkillsStatusReport(cfg state.ConfigDoc, agentID string) map[string]any {
	requirementsToMap := func(r skillspkg.Requirements) map[string]any {
		bins := r.Bins
		if bins == nil {
			bins = []string{}
		}
		anyBins := r.AnyBins
		if anyBins == nil {
			anyBins = []string{}
		}
		env := r.Env
		if env == nil {
			env = []string{}
		}
		osReq := r.OS
		if osReq == nil {
			osReq = []string{}
		}
		config := r.Config
		if config == nil {
			config = []string{}
		}
		return map[string]any{"bins": bins, "anyBins": anyBins, "env": env, "os": osReq, "config": config}
	}

	catalog, err := skillspkg.BuildSkillCatalog(cfg, agentID)
	if err != nil || catalog == nil {
		return map[string]any{
			"workspaceDir":     skillspkg.ResolveAgentWorkspaceDir(cfg, agentID),
			"managedSkillsDir": skillspkg.ManagedSkillsDir(),
			"skills":           []map[string]any{},
		}
	}

	skillsList := make([]map[string]any, 0, len(catalog.Skills))
	for _, resolved := range catalog.Skills {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		installSpecs := make([]map[string]any, 0)
		for _, spec := range resolved.Skill.InstallSpecs() {
			m := map[string]any{
				"id":    spec.ID,
				"kind":  spec.Kind,
				"label": spec.Label,
				"bins":  spec.Bins,
			}
			if spec.Formula != "" {
				m["formula"] = spec.Formula
			}
			if spec.Package != "" {
				m["package"] = spec.Package
			}
			if spec.Module != "" {
				m["module"] = spec.Module
			}
			if spec.URL != "" {
				m["url"] = spec.URL
			}
			installSpecs = append(installSpecs, m)
		}
		if len(installSpecs) == 0 {
			for _, step := range resolved.Skill.Manifest.Install {
				installSpecs = append(installSpecs, map[string]any{"cmd": step.Cmd, "cwd": step.Cwd})
			}
		}
		configChecks := make([]map[string]any, 0, len(resolved.ConfigChecks))
		for _, check := range resolved.ConfigChecks {
			configChecks = append(configChecks, map[string]any{"path": check.Path, "satisfied": check.Satisfied})
		}
		name := strings.TrimSpace(resolved.Skill.Manifest.Name)
		if name == "" {
			name = resolved.Skill.SkillKey
		}
		entry := map[string]any{
			"id":                     resolved.Skill.SkillKey,
			"status":                 resolved.Status,
			"name":                   name,
			"description":            strings.TrimSpace(resolved.Skill.Manifest.Description),
			"source":                 string(resolved.SourceKind),
			"bundled":                resolved.SourceKind == skillspkg.SkillSourceBundled,
			"filePath":               strings.TrimSpace(resolved.Skill.FilePath),
			"baseDir":                strings.TrimSpace(resolved.Skill.BaseDir),
			"skillKey":               resolved.Skill.SkillKey,
			"primaryEnv":             resolved.PrimaryEnv,
			"emoji":                  resolved.Skill.Emoji(),
			"homepage":               resolved.Skill.Manifest.Homepage,
			"always":                 resolved.Always,
			"disabled":               resolved.Disabled,
			"blockedByAllowlist":     resolved.BlockedByAllowlist,
			"eligible":               resolved.Eligible,
			"requirements":           requirementsToMap(resolved.EffectiveRequirements),
			"missing":                requirementsToMap(resolved.Missing),
			"configChecks":           configChecks,
			"install":                installSpecs,
			"whenToUse":              resolved.WhenToUse,
			"userInvocable":          resolved.UserInvocable,
			"disableModelInvocation": resolved.DisableModelInvocation,
		}
		if resolved.SelectedInstallID != "" {
			entry["selectedInstallId"] = resolved.SelectedInstallID
		}
		skillsList = append(skillsList, entry)
	}
	sort.Slice(skillsList, func(i, j int) bool {
		return fmt.Sprintf("%v", skillsList[i]["skillKey"]) < fmt.Sprintf("%v", skillsList[j]["skillKey"])
	})
	return map[string]any{
		"workspaceDir":     catalog.WorkspaceDir,
		"managedSkillsDir": catalog.ManagedSkillsDir,
		"skills":           skillsList,
	}
}

func applySkillsBins(cfg state.ConfigDoc) map[string]any {
	seen := map[string]struct{}{}
	bins := make([]string, 0)
	push := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		bins = append(bins, v)
	}

	agentIDs := []string{"main"}
	for _, ag := range cfg.Agents {
		if id := defaultAgentID(ag.ID); id != "main" {
			agentIDs = append(agentIDs, id)
		}
	}
	seenAgents := map[string]struct{}{}
	for _, agentID := range agentIDs {
		agentID = defaultAgentID(agentID)
		if _, ok := seenAgents[agentID]; ok {
			continue
		}
		seenAgents[agentID] = struct{}{}
		catalog, err := skillspkg.BuildSkillCatalog(cfg, agentID)
		if err != nil || catalog == nil {
			continue
		}
		finalSkills := make([]*skillspkg.Skill, 0, len(catalog.Skills))
		for _, resolved := range catalog.Skills {
			if resolved == nil || resolved.Skill == nil || resolved.SourceKind == skillspkg.SkillSourceConfig {
				continue
			}
			finalSkills = append(finalSkills, resolved.Skill)
		}
		for _, b := range skillspkg.AggregateBins(finalSkills) {
			push(b)
		}
	}
	sort.Strings(bins)
	return map[string]any{"bins": bins}
}


var execCommandContext = exec.CommandContext

// findInstallSpec searches the merged skill catalog for the install spec with the
// given ID on the named skill.
func findInstallSpec(cfg state.ConfigDoc, agentID, name, installID string) (*skillspkg.InstallSpec, *skillspkg.ResolvedSkill, bool) {
	nameNorm := strings.ToLower(strings.TrimSpace(name))
	idNorm := strings.ToLower(strings.TrimSpace(installID))

	catalog, err := skillspkg.BuildSkillCatalog(cfg, agentID)
	if err != nil || catalog == nil {
		return nil, nil, false
	}
	for _, resolved := range catalog.Skills {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		skillName := strings.ToLower(strings.TrimSpace(resolved.Skill.Manifest.Name))
		if skillName != nameNorm && strings.ToLower(resolved.Skill.SkillKey) != nameNorm {
			continue
		}
		for _, spec := range resolved.Skill.InstallSpecs() {
			if strings.ToLower(spec.ID) == idNorm {
				cp := spec
				return &cp, resolved, true
			}
		}
	}
	return nil, nil, false
}

// runDownloadInstall downloads a binary from spec.URL into ~/.metiq/bin/.
func runDownloadInstall(ctx context.Context, spec skillspkg.InstallSpec) (stdout, stderr string, code int, err error) {
	if spec.URL == "" {
		return "", "download spec missing url", 1, fmt.Errorf("download spec missing url")
	}
	req, herr := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if herr != nil {
		return "", herr.Error(), 1, herr
	}
	resp, herr := http.DefaultClient.Do(req)
	if herr != nil {
		return "", herr.Error(), 1, herr
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("download failed: HTTP %d", resp.StatusCode)
		return "", msg, 1, fmt.Errorf("%s", msg)
	}
	filename := filepath.Base(resp.Request.URL.Path)
	if filename == "" || filename == "." || filename == "/" {
		filename = "download"
	}
	homeDir, _ := os.UserHomeDir()
	binDir := filepath.Join(homeDir, ".metiq", "bin")
	if merr := os.MkdirAll(binDir, 0o755); merr != nil {
		return "", merr.Error(), 1, merr
	}
	destPath := filepath.Join(binDir, filename)
	f, ferr := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if ferr != nil {
		return "", ferr.Error(), 1, ferr
	}
	defer f.Close()
	if _, copyErr := io.Copy(f, resp.Body); copyErr != nil {
		return "", copyErr.Error(), 1, copyErr
	}
	return fmt.Sprintf("Downloaded to %s", destPath), "", 0, nil
}

// runInstallSpec executes the installation command described by spec, using ctx
// (which should already have a deadline set from req.TimeoutMS).
func runInstallSpec(ctx context.Context, spec skillspkg.InstallSpec, prefs skillspkg.InstallPreferences) (stdout, stderr string, code int, err error) {
	var cmd *exec.Cmd
	kind := strings.ToLower(strings.TrimSpace(spec.Kind))
	if kind == "npm" {
		kind = "node"
	}
	switch kind {
	case "brew":
		formula := spec.Formula
		if formula == "" {
			formula = spec.Package
		}
		if formula == "" {
			return "", "brew spec missing formula/package", 1, fmt.Errorf("brew spec missing formula/package")
		}
		cmd = execCommandContext(ctx, "brew", "install", formula)
	case "npm", "node":
		if spec.Package == "" {
			return "", "node spec missing package", 1, fmt.Errorf("node spec missing package")
		}
		nodeManager := strings.TrimSpace(prefs.NodeManager)
		if nodeManager == "" {
			nodeManager = "npm"
		}
		switch nodeManager {
		case "pnpm":
			cmd = execCommandContext(ctx, "pnpm", "add", "-g", spec.Package)
		case "yarn":
			cmd = execCommandContext(ctx, "yarn", "global", "add", spec.Package)
		case "bun":
			cmd = execCommandContext(ctx, "bun", "install", "-g", spec.Package)
		default:
			cmd = execCommandContext(ctx, "npm", "install", "-g", spec.Package)
		}
	case "go":
		if spec.Module == "" {
			return "", "go spec missing module", 1, fmt.Errorf("go spec missing module")
		}
		cmd = execCommandContext(ctx, "go", "install", spec.Module+"@latest")
	case "uv":
		if spec.Package == "" {
			return "", "uv spec missing package", 1, fmt.Errorf("uv spec missing package")
		}
		cmd = execCommandContext(ctx, "uv", "tool", "install", spec.Package)
	case "apt":
		if spec.Package == "" {
			return "", "apt spec missing package", 1, fmt.Errorf("apt spec missing package")
		}
		cmd = execCommandContext(ctx, "apt-get", "install", "-y", spec.Package)
	case "download":
		return runDownloadInstall(ctx, spec)
	default:
		return "", fmt.Sprintf("unsupported install kind %q", spec.Kind), 1, fmt.Errorf("unsupported install kind %q", spec.Kind)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
		err = runErr
	}
	return
}

func applySkillInstall(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.SkillsInstallRequest) (state.ConfigDoc, map[string]any, error) {
	cfg := configState.Get()
	agentID := defaultAgentID(req.AgentID)
	installPrefs := skillspkg.ResolveInstallPreferences(cfg)

	// Find the install spec in the merged skill catalog.
	spec, resolved, found := findInstallSpec(cfg, agentID, req.Name, req.InstallID)

	var installResult map[string]any
	if !found {
		installResult = map[string]any{
			"ok":      false,
			"message": "Skill install option not found",
			"stdout":  "",
			"stderr":  "",
			"code":    1,
		}
		return state.ConfigDoc{}, installResult, nil
	} else {
		// Apply timeout from the request.
		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		installCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		outStr, errStr, exitCode, runErr := runInstallSpec(installCtx, *spec, installPrefs)
		if runErr != nil {
			installResult = map[string]any{
				"ok":      false,
				"message": fmt.Sprintf("install failed (exit %d)", exitCode),
				"stdout":  outStr,
				"stderr":  errStr,
				"code":    exitCode,
			}
			// Do not update config on failure.
			return state.ConfigDoc{}, installResult, nil
		}
		installResult = map[string]any{
			"ok":      true,
			"message": "Installed",
			"stdout":  outStr,
			"stderr":  errStr,
			"code":    exitCode,
		}
	}

	// Persist the install record in config.
	entries := extractSkillEntries(cfg)
	key := strings.ToLower(strings.TrimSpace(req.Name))
	if resolved != nil && resolved.Skill != nil && strings.TrimSpace(resolved.Skill.SkillKey) != "" {
		key = strings.ToLower(strings.TrimSpace(resolved.Skill.SkillKey))
	}
	entry, ok := entries[key]
	if !ok {
		entry = map[string]any{}
	}
	if resolved != nil && resolved.Skill != nil && strings.TrimSpace(resolved.Skill.Manifest.Name) != "" {
		entry["name"] = resolved.Skill.Manifest.Name
	} else {
		entry["name"] = req.Name
	}
	entry["install_id"] = req.InstallID
	entry["enabled"] = true
	entry["status"] = "installed"
	entry["updated_at"] = time.Now().Unix()
	entries[key] = entry
	next := configWithSkillEntries(cfg, entries)
	if err := persistRuntimeConfigFile(next); err != nil {
		return state.ConfigDoc{}, installResult, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return state.ConfigDoc{}, installResult, err
	}
	configState.Set(next)
	skillspkg.InvalidateSkillCatalogCache()
	return next, installResult, nil
}

func applySkillUpdate(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.SkillsUpdateRequest) (state.ConfigDoc, map[string]any, error) {
	cfg := configState.Get()
	entries := extractSkillEntries(cfg)
	rawSkillKey := strings.TrimSpace(req.SkillKey)
	skillKey := strings.ToLower(rawSkillKey)
	if skillKey == "" {
		return state.ConfigDoc{}, nil, fmt.Errorf("skill key is required")
	}
	entry, ok := entries[skillKey]
	if !ok {
		for key, existing := range entries {
			if strings.EqualFold(key, skillKey) {
				if !ok {
					entry = existing
					ok = true
				}
				delete(entries, key)
			}
		}
	}
	if !ok {
		entry = map[string]any{}
	}
	if req.Enabled != nil {
		entry["enabled"] = *req.Enabled
	}
	if req.APIKey != nil {
		if strings.TrimSpace(*req.APIKey) == "" {
			delete(entry, "api_key")
		} else {
			entry["api_key"] = strings.TrimSpace(*req.APIKey)
		}
	}
	if req.Env != nil {
		nextEnv := map[string]any{}
		if currentEnv, ok := entry["env"].(map[string]any); ok {
			for key, value := range currentEnv {
				nextEnv[key] = value
			}
		}
		for key, value := range req.Env {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			trimmedVal := strings.TrimSpace(value)
			if trimmedVal == "" {
				delete(nextEnv, trimmedKey)
				continue
			}
			nextEnv[trimmedKey] = trimmedVal
		}
		if len(nextEnv) == 0 {
			delete(entry, "env")
		} else {
			entry["env"] = nextEnv
		}
	}
	entry["updated_at"] = time.Now().Unix()
	entries[skillKey] = entry
	next := configWithSkillEntries(cfg, entries)
	if err := persistRuntimeConfigFile(next); err != nil {
		return state.ConfigDoc{}, nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return state.ConfigDoc{}, nil, err
	}
	configState.Set(next)
	skillspkg.InvalidateSkillCatalogCache()
	entryCopy := map[string]any{}
	for key, value := range entry {
		entryCopy[key] = value
	}
	return next, entryCopy, nil
}
