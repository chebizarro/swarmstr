package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

type SkillSourceKind string

const (
	SkillSourceExtra     SkillSourceKind = "metiq-extra"
	SkillSourceBundled   SkillSourceKind = "metiq-bundled"
	SkillSourceManaged   SkillSourceKind = "metiq-managed"
	SkillSourceWorkspace SkillSourceKind = "metiq-workspace"
	SkillSourceConfig    SkillSourceKind = "metiq-config"
)

type SkillConfigEntry struct {
	Enabled   *bool
	APIKey    string
	Env       map[string]string
	InstallID string
	UpdatedAt int64
	Raw       map[string]any
}

type ConfigCheck struct {
	Path      string
	Satisfied bool
}

type ResolvedSkill struct {
	Skill                  *Skill
	SourceKind             SkillSourceKind
	Disabled               bool
	BlockedByAllowlist     bool
	Always                 bool
	PrimaryEnv             string
	WhenToUse              string
	UserInvocable          bool
	DisableModelInvocation bool
	EffectiveRequirements  Requirements
	Missing                Requirements
	ConfigChecks           []ConfigCheck
	Eligible               bool
	PromptEligible         bool
	Status                 string
	SelectedInstallID      string
	Config                 SkillConfigEntry
}

type SkillCatalog struct {
	AgentID               string
	WorkspaceDir          string
	ManagedSkillsDir      string
	BundledSkillsDir      string
	ExtraDirs             []string
	Skills                []*ResolvedSkill
	Fingerprint           string
	ConfigDependencyHash  string
	ConfigDependencyPaths []string
	RuntimeDependencyHash string
	RuntimeDependencyEnv  []string
	RuntimeDependencyBins []string
}

type RequirementEvalContext struct {
	HasBin         func(string) bool
	HasEnv         func(string) bool
	IsConfigTruthy func(string) bool
	CurrentOS      string
}

type PromptLimits struct {
	MaxCount int
	MaxChars int
}

type InstallPreferences struct {
	PreferBrew  bool
	NodeManager string
}

const (
	defaultPromptSkillCount = 150
	defaultPromptSkillChars = 30000
	defaultNodeManager      = "npm"
)

var (
	skillCatalogCacheMu sync.Mutex
	skillCatalogCache   = map[string]*SkillCatalog{}
)

func InvalidateSkillCatalogCache() {
	skillCatalogCacheMu.Lock()
	defer skillCatalogCacheMu.Unlock()
	skillCatalogCache = map[string]*SkillCatalog{}
}

func ResolveAgentWorkspaceDir(cfg state.ConfigDoc, agentID string) string {
	return workspace.ResolveWorkspaceDir(cfg, agentID)
}

func ResolvePromptLimits(cfg state.ConfigDoc) PromptLimits {
	limits := PromptLimits{MaxCount: defaultPromptSkillCount, MaxChars: defaultPromptSkillChars}
	rawSkills, _ := cfg.Extra["skills"].(map[string]any)
	rawPrompt, _ := rawSkills["prompt"].(map[string]any)
	if n := normalizePositiveInt(rawPrompt["max_count"], defaultPromptSkillCount, 5000); n > 0 {
		limits.MaxCount = n
	}
	if n := normalizePositiveInt(rawPrompt["max_chars"], defaultPromptSkillChars, 500000); n > 0 {
		limits.MaxChars = n
	}
	return limits
}

// ResolvePromptLimitsWithBudget is like ResolvePromptLimits but clamps the
// result to context-budget limits. Budget values win when more restrictive.
// Pass zero for budgetMaxCount or budgetMaxChars to skip clamping that field.
func ResolvePromptLimitsWithBudget(cfg state.ConfigDoc, budgetMaxCount, budgetMaxChars int) PromptLimits {
	base := ResolvePromptLimits(cfg)
	if budgetMaxCount > 0 && budgetMaxCount < base.MaxCount {
		base.MaxCount = budgetMaxCount
	}
	if budgetMaxChars > 0 && budgetMaxChars < base.MaxChars {
		base.MaxChars = budgetMaxChars
	}
	return base
}

// SkillPromptText returns the text to inject for a skill given the available
// character budget and a tier indicator. The tier controls how much detail is
// included:
//   - tier "micro": name + description + when_to_use only (no body)
//   - tier "small": name + description + when_to_use + body truncated to charBudget
//   - tier "standard" (or any other value): full existing rendering (unchanged)
func SkillPromptText(skill *ResolvedSkill, charBudget int, tier string) string {
	if skill == nil || skill.Skill == nil {
		return ""
	}
	s := skill.Skill
	name := strings.TrimSpace(s.Manifest.Name)
	desc := strings.TrimSpace(s.Manifest.Description)
	whenToUse := strings.TrimSpace(skill.WhenToUse)
	if whenToUse == "" {
		whenToUse = s.WhenToUse()
	}

	switch strings.ToLower(tier) {
	case "micro":
		// Minimal: name + description + when_to_use
		parts := make([]string, 0, 3)
		if name != "" {
			parts = append(parts, "## "+name)
		}
		if desc != "" {
			parts = append(parts, desc)
		}
		if whenToUse != "" {
			parts = append(parts, "Use when: "+whenToUse)
		}
		text := strings.Join(parts, "\n")
		if charBudget > 0 && len(text) > charBudget {
			text = text[:charBudget]
		}
		return text

	case "small":
		// Condensed: name + description + when_to_use + truncated body
		parts := make([]string, 0, 4)
		if name != "" {
			parts = append(parts, "## "+name)
		}
		if desc != "" {
			parts = append(parts, desc)
		}
		if whenToUse != "" {
			parts = append(parts, "Use when: "+whenToUse)
		}
		header := strings.Join(parts, "\n")
		body := strings.TrimSpace(s.Manifest.Body)
		if body != "" {
			remaining := charBudget - len(header) - 1 // -1 for newline
			if remaining > 0 {
				if len(body) > remaining {
					body = body[:remaining]
				}
				header += "\n" + body
			}
		}
		if charBudget > 0 && len(header) > charBudget {
			header = header[:charBudget]
		}
		return header

	default:
		// Standard: full skill content (unchanged from existing behavior)
		return ""
	}
}

func ResolveInstallPreferences(cfg state.ConfigDoc) InstallPreferences {
	prefs := InstallPreferences{PreferBrew: true, NodeManager: defaultNodeManager}
	rawSkills, _ := cfg.Extra["skills"].(map[string]any)
	rawInstall, _ := rawSkills["install"].(map[string]any)
	if prefer, ok := rawInstall["prefer_brew"].(bool); ok {
		prefs.PreferBrew = prefer
	}
	prefs.NodeManager = normalizeNodeManager(stringFromMap(rawInstall, "node_manager"))
	return prefs
}

func PreferredInstallSpecID(skill *Skill, selectedID string, prefs InstallPreferences) string {
	if skill == nil {
		return strings.TrimSpace(selectedID)
	}
	specs := skill.InstallSpecs()
	if len(specs) == 0 {
		return ""
	}
	selectedID = strings.TrimSpace(selectedID)
	if selectedID != "" {
		for _, spec := range specs {
			if strings.EqualFold(strings.TrimSpace(spec.ID), selectedID) {
				return spec.ID
			}
		}
	}
	if prefs.PreferBrew {
		for _, spec := range specs {
			if strings.EqualFold(strings.TrimSpace(spec.Kind), "brew") {
				return spec.ID
			}
		}
	}
	if !prefs.PreferBrew {
		for _, spec := range specs {
			if !strings.EqualFold(strings.TrimSpace(spec.Kind), "brew") {
				return spec.ID
			}
		}
	}
	return specs[0].ID
}

func normalizeNodeManager(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pnpm", "yarn", "bun":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return defaultNodeManager
	}
}

func BuildSkillCatalog(cfg state.ConfigDoc, agentID string) (*SkillCatalog, error) {
	agentID = normalizeSkillAgentID(agentID)
	workspaceDir := ResolveAgentWorkspaceDir(cfg, agentID)
	managedDir := ManagedSkillsDir()
	bundledDir := BundledSkillsDir()
	extraDirs := resolveExtraSkillDirs(cfg)
	fingerprint := hashSkillCatalogFingerprint(cfg, agentID, workspaceDir, managedDir, bundledDir, extraDirs)
	configMap := configDocToMap(cfg)

	skillCatalogCacheMu.Lock()
	if cached, ok := skillCatalogCache[fingerprint]; ok {
		if cached.ConfigDependencyHash == hashConfigDependencyPaths(configMap, cached.ConfigDependencyPaths) &&
			cached.RuntimeDependencyHash == hashRuntimeDependencies(cached.RuntimeDependencyEnv, cached.RuntimeDependencyBins) {
			skillCatalogCacheMu.Unlock()
			return cached, nil
		}
	}
	skillCatalogCacheMu.Unlock()

	entries := extractSkillConfigEntries(cfg)
	allowBundled := resolveBundledAllowlist(cfg)
	installPrefs := ResolveInstallPreferences(cfg)
	ctx := RequirementEvalContext{
		HasBin:         BinExists,
		HasEnv:         func(envName string) bool { return strings.TrimSpace(os.Getenv(envName)) != "" },
		IsConfigTruthy: func(path string) bool { return isConfigPathTruthy(configMap, path) },
		CurrentOS:      runtimeOS(),
	}

	merged := map[string]*ResolvedSkill{}
	merge := func(skills []*Skill, source SkillSourceKind) {
		for _, skill := range skills {
			if skill == nil {
				continue
			}
			key := normalizedSkillKey(skill.SkillKey)
			resolved := resolveSkill(skill, source, entries[key], allowBundled, ctx)
			if prev, ok := merged[key]; ok && prev != nil {
				log.Printf("skills: shadowed skill key=%s old_source=%s new_source=%s", key, prev.SourceKind, source)
			}
			merged[key] = resolved
		}
	}

	for _, dir := range extraDirs {
		if scanned, err := ScanWorkspace(dir); err == nil {
			merge(scanned, SkillSourceExtra)
		} else {
			log.Printf("skills: extra skill dir scan failed dir=%s err=%v", dir, err)
		}
	}
	if bundledDir != "" {
		if scanned, err := ScanBundledDir(bundledDir); err == nil {
			merge(scanned, SkillSourceBundled)
		} else {
			log.Printf("skills: bundled skill scan failed dir=%s err=%v", bundledDir, err)
		}
	}
	if managedDir != "" {
		if scanned, err := ScanBundledDir(managedDir); err == nil {
			merge(scanned, SkillSourceManaged)
		} else {
			log.Printf("skills: managed skill scan failed dir=%s err=%v", managedDir, err)
		}
	}
	if workspaceDir != "" {
		if scanned, err := ScanWorkspace(workspaceDir); err == nil {
			merge(scanned, SkillSourceWorkspace)
		} else {
			log.Printf("skills: workspace skill scan failed dir=%s err=%v", workspaceDir, err)
		}
	}

	for key, entry := range entries {
		if _, ok := merged[key]; ok {
			continue
		}
		merged[key] = resolveConfigOnlySkill(key, entry)
	}

	catalog := &SkillCatalog{
		AgentID:          agentID,
		WorkspaceDir:     workspaceDir,
		ManagedSkillsDir: managedDir,
		BundledSkillsDir: bundledDir,
		ExtraDirs:        append([]string(nil), extraDirs...),
		Fingerprint:      fingerprint,
	}
	configDepSet := map[string]struct{}{}
	runtimeEnvSet := map[string]struct{}{}
	runtimeBinSet := map[string]struct{}{}
	for _, resolved := range merged {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		if resolved.SourceKind != SkillSourceConfig {
			resolved.SelectedInstallID = PreferredInstallSpecID(resolved.Skill, resolved.SelectedInstallID, installPrefs)
			for _, path := range resolved.EffectiveRequirements.Config {
				path = strings.TrimSpace(path)
				if path != "" {
					configDepSet[path] = struct{}{}
				}
			}
			for _, envName := range resolved.EffectiveRequirements.Env {
				envName = strings.TrimSpace(envName)
				if envName != "" {
					runtimeEnvSet[envName] = struct{}{}
				}
			}
			if envName := strings.TrimSpace(resolved.PrimaryEnv); envName != "" {
				runtimeEnvSet[envName] = struct{}{}
			}
			for _, bin := range resolved.EffectiveRequirements.Bins {
				bin = strings.TrimSpace(bin)
				if bin != "" {
					runtimeBinSet[bin] = struct{}{}
				}
			}
			for _, bin := range resolved.EffectiveRequirements.AnyBins {
				bin = strings.TrimSpace(bin)
				if bin != "" {
					runtimeBinSet[bin] = struct{}{}
				}
			}
		}
		catalog.Skills = append(catalog.Skills, resolved)
	}
	for path := range configDepSet {
		catalog.ConfigDependencyPaths = append(catalog.ConfigDependencyPaths, path)
	}
	for envName := range runtimeEnvSet {
		catalog.RuntimeDependencyEnv = append(catalog.RuntimeDependencyEnv, envName)
	}
	for bin := range runtimeBinSet {
		catalog.RuntimeDependencyBins = append(catalog.RuntimeDependencyBins, bin)
	}
	sort.Strings(catalog.ConfigDependencyPaths)
	sort.Strings(catalog.RuntimeDependencyEnv)
	sort.Strings(catalog.RuntimeDependencyBins)
	catalog.ConfigDependencyHash = hashConfigDependencyPaths(configMap, catalog.ConfigDependencyPaths)
	catalog.RuntimeDependencyHash = hashRuntimeDependencies(catalog.RuntimeDependencyEnv, catalog.RuntimeDependencyBins)
	sort.Slice(catalog.Skills, func(i, j int) bool {
		return normalizedSkillKey(catalog.Skills[i].Skill.SkillKey) < normalizedSkillKey(catalog.Skills[j].Skill.SkillKey)
	})

	skillCatalogCacheMu.Lock()
	skillCatalogCache[fingerprint] = catalog
	skillCatalogCacheMu.Unlock()
	return catalog, nil
}

func EvaluateRequirements(req Requirements, ctx RequirementEvalContext) (Requirements, []ConfigCheck, bool) {
	var missing Requirements
	var configChecks []ConfigCheck

	hasBin := ctx.HasBin
	if hasBin == nil {
		hasBin = BinExists
	}
	hasEnv := ctx.HasEnv
	if hasEnv == nil {
		hasEnv = func(name string) bool { return strings.TrimSpace(os.Getenv(name)) != "" }
	}
	isConfigTruthy := ctx.IsConfigTruthy
	if isConfigTruthy == nil {
		isConfigTruthy = func(string) bool { return false }
	}
	currentOS := strings.TrimSpace(ctx.CurrentOS)
	if currentOS == "" {
		currentOS = runtimeOS()
	}

	for _, bin := range req.Bins {
		if !hasBin(bin) {
			missing.Bins = append(missing.Bins, bin)
		}
	}
	if len(req.AnyBins) > 0 {
		found := false
		for _, bin := range req.AnyBins {
			if hasBin(bin) {
				found = true
				break
			}
		}
		if !found {
			missing.AnyBins = append(missing.AnyBins, req.AnyBins...)
		}
	}
	for _, envName := range req.Env {
		if !hasEnv(envName) {
			missing.Env = append(missing.Env, envName)
		}
	}
	if len(req.OS) > 0 {
		matched := false
		for _, osName := range req.OS {
			if strings.EqualFold(strings.TrimSpace(osName), currentOS) {
				matched = true
				break
			}
		}
		if !matched {
			missing.OS = append(missing.OS, req.OS...)
		}
	}
	for _, cfgPath := range req.Config {
		satisfied := isConfigTruthy(cfgPath)
		configChecks = append(configChecks, ConfigCheck{Path: cfgPath, Satisfied: satisfied})
		if !satisfied {
			missing.Config = append(missing.Config, cfgPath)
		}
	}

	eligible := len(missing.Bins) == 0 && len(missing.AnyBins) == 0 && len(missing.Env) == 0 && len(missing.OS) == 0 && len(missing.Config) == 0
	return missing, configChecks, eligible
}

func resolveSkill(skill *Skill, source SkillSourceKind, config SkillConfigEntry, allowBundled map[string]struct{}, ctx RequirementEvalContext) *ResolvedSkill {
	req := skill.EffectiveRequirements()
	hasEnv := func(envName string) bool {
		if ctx.HasEnv != nil && ctx.HasEnv(envName) {
			return true
		}
		if strings.TrimSpace(config.Env[envName]) != "" {
			return true
		}
		return strings.TrimSpace(config.APIKey) != "" && skill.PrimaryEnv() == envName
	}
	missing, configChecks, eligible := EvaluateRequirements(req, RequirementEvalContext{
		HasBin:         ctx.HasBin,
		HasEnv:         hasEnv,
		IsConfigTruthy: ctx.IsConfigTruthy,
		CurrentOS:      ctx.CurrentOS,
	})
	disabled := !skill.IsEnabled()
	if config.Enabled != nil {
		disabled = !*config.Enabled
	}
	blocked := isBundledSource(source) && len(allowBundled) > 0 && !isSkillAllowed(skill, allowBundled)
	always := skill.Always()
	promptEligible := !disabled && !blocked && (eligible || always)
	status := "missing_requirements"
	switch {
	case blocked:
		status = "blocked"
	case disabled:
		status = "disabled"
	case promptEligible && !eligible && always:
		status = "always"
	case eligible:
		status = "ready"
	}
	return &ResolvedSkill{
		Skill:                  skill,
		SourceKind:             source,
		Disabled:               disabled,
		BlockedByAllowlist:     blocked,
		Always:                 always,
		PrimaryEnv:             skill.PrimaryEnv(),
		WhenToUse:              skill.WhenToUse(),
		UserInvocable:          skill.UserInvocable(),
		DisableModelInvocation: skill.DisableModelInvocation(),
		EffectiveRequirements:  req,
		Missing:                missing,
		ConfigChecks:           configChecks,
		Eligible:               !disabled && !blocked && eligible,
		PromptEligible:         promptEligible,
		Status:                 status,
		SelectedInstallID:      strings.TrimSpace(config.InstallID),
		Config:                 config,
	}
}

func resolveConfigOnlySkill(key string, entry SkillConfigEntry) *ResolvedSkill {
	enabled := true
	if entry.Enabled != nil {
		enabled = *entry.Enabled
	}
	skill := &Skill{
		SkillKey: key,
		Manifest: Manifest{Name: key, Description: stringFromMap(entry.Raw, "description")},
	}
	status := "ready"
	if !enabled {
		status = "disabled"
	}
	return &ResolvedSkill{
		Skill:                  skill,
		SourceKind:             SkillSourceConfig,
		Disabled:               !enabled,
		BlockedByAllowlist:     false,
		Always:                 false,
		PrimaryEnv:             "",
		WhenToUse:              strings.TrimSpace(skill.Manifest.WhenToUse),
		UserInvocable:          true,
		DisableModelInvocation: true,
		EffectiveRequirements:  Requirements{},
		Missing:                Requirements{},
		ConfigChecks:           nil,
		Eligible:               enabled,
		PromptEligible:         false,
		Status:                 status,
		SelectedInstallID:      strings.TrimSpace(entry.InstallID),
		Config:                 entry,
	}
}

func extractSkillConfigEntries(cfg state.ConfigDoc) map[string]SkillConfigEntry {
	out := map[string]SkillConfigEntry{}
	rawSkills, _ := cfg.Extra["skills"].(map[string]any)
	rawEntries, _ := rawSkills["entries"].(map[string]any)
	for key, value := range rawEntries {
		entryMap, ok := value.(map[string]any)
		if !ok {
			continue
		}
		normKey := normalizedSkillKey(key)
		entry := SkillConfigEntry{Env: map[string]string{}, Raw: cloneMap(entryMap)}
		if enabled, ok := entryMap["enabled"].(bool); ok {
			entry.Enabled = &enabled
		}
		entry.APIKey = strings.TrimSpace(stringFromMap(entryMap, "api_key"))
		entry.InstallID = strings.TrimSpace(stringFromMap(entryMap, "install_id"))
		switch v := entryMap["updated_at"].(type) {
		case int64:
			entry.UpdatedAt = v
		case int:
			entry.UpdatedAt = int64(v)
		case float64:
			entry.UpdatedAt = int64(v)
		}
		if rawEnv, ok := entryMap["env"].(map[string]any); ok {
			for envKey, envValue := range rawEnv {
				trimmedKey := strings.TrimSpace(envKey)
				trimmedValue := strings.TrimSpace(fmt.Sprintf("%v", envValue))
				if trimmedKey != "" && trimmedValue != "" {
					entry.Env[trimmedKey] = trimmedValue
				}
			}
		}
		out[normKey] = entry
	}
	return out
}

func resolveExtraSkillDirs(cfg state.ConfigDoc) []string {
	var out []string
	rawSkills, _ := cfg.Extra["skills"].(map[string]any)
	rawDirs, ok := rawSkills["extra_dirs"]
	if !ok {
		return out
	}
	switch v := rawDirs.(type) {
	case []string:
		for _, dir := range v {
			if dir = strings.TrimSpace(dir); dir != "" {
				out = append(out, dir)
			}
		}
	case []any:
		for _, raw := range v {
			dir := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if dir != "" {
				out = append(out, dir)
			}
		}
	}
	return dedupeStringSlice(out)
}

func resolveBundledAllowlist(cfg state.ConfigDoc) map[string]struct{} {
	allowed := map[string]struct{}{}
	rawSkills, _ := cfg.Extra["skills"].(map[string]any)
	rawAllow, ok := rawSkills["allow_bundled"]
	if !ok {
		return allowed
	}
	var items []string
	switch v := rawAllow.(type) {
	case []string:
		items = v
	case []any:
		for _, raw := range v {
			items = append(items, fmt.Sprintf("%v", raw))
		}
	}
	for _, item := range items {
		item = normalizedSkillKey(item)
		if item != "" {
			allowed[item] = struct{}{}
		}
	}
	return allowed
}

func isSkillAllowed(skill *Skill, allow map[string]struct{}) bool {
	if len(allow) == 0 {
		return true
	}
	if _, ok := allow[normalizedSkillKey(skill.SkillKey)]; ok {
		return true
	}
	if _, ok := allow[normalizedSkillKey(skill.Manifest.Name)]; ok {
		return true
	}
	return false
}

func isBundledSource(source SkillSourceKind) bool {
	return source == SkillSourceBundled
}

func configDocToMap(cfg state.ConfigDoc) map[string]any {
	data, err := json.Marshal(cfg)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func isConfigPathTruthy(root map[string]any, path string) bool {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 || parts[0] == "" {
		return false
	}
	var current any = root
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = m[part]
		if !ok {
			return false
		}
	}
	return truthyValue(current)
}

func truthyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return strings.TrimSpace(t) != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case []any:
		return len(t) > 0
	case []string:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

func hashSkillCatalogFingerprint(cfg state.ConfigDoc, agentID, workspaceDir, managedDir, bundledDir string, extraDirs []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(hashRelevantSkillConfig(cfg)))
	_, _ = h.Write([]byte("\n" + normalizeSkillAgentID(agentID)))
	_, _ = h.Write([]byte("\n" + strings.TrimSpace(workspaceDir)))
	_, _ = h.Write([]byte("\n" + strings.TrimSpace(managedDir)))
	_, _ = h.Write([]byte("\n" + strings.TrimSpace(bundledDir)))
	for _, dir := range extraDirs {
		_, _ = h.Write([]byte("\n" + strings.TrimSpace(dir)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashRelevantSkillConfig(cfg state.ConfigDoc) string {
	rawSkills, _ := cfg.Extra["skills"].(map[string]any)
	relevant := map[string]any{}
	for _, key := range []string{"entries", "workspace", "extra_dirs", "allow_bundled", "prompt", "install"} {
		if value, ok := rawSkills[key]; ok {
			relevant[key] = value
		}
	}
	data, err := json.Marshal(relevant)
	if err != nil {
		return ""
	}
	return string(data)
}

func hashConfigDependencyPaths(root map[string]any, paths []string) string {
	h := sha256.New()
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte("="))
		value, ok := configPathValue(root, path)
		if ok {
			data, err := json.Marshal(value)
			if err == nil {
				_, _ = h.Write(data)
			}
		}
		_, _ = h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashRuntimeDependencies(envNames, binNames []string) string {
	h := sha256.New()
	for _, envName := range envNames {
		envName = strings.TrimSpace(envName)
		if envName == "" {
			continue
		}
		_, _ = h.Write([]byte("env:" + envName + "=" + os.Getenv(envName) + "\n"))
	}
	for _, bin := range binNames {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			continue
		}
		_, _ = h.Write([]byte("bin:" + bin + "="))
		if BinExists(bin) {
			_, _ = h.Write([]byte("1"))
		} else {
			_, _ = h.Write([]byte("0"))
		}
		_, _ = h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func configPathValue(root map[string]any, path string) (any, bool) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, false
	}
	var current any = root
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func normalizePositiveInt(v any, def, max int) int {
	out := def
	switch t := v.(type) {
	case int:
		out = t
	case int64:
		out = int(t)
	case float64:
		out = int(t)
	}
	if out <= 0 {
		return def
	}
	if out > max {
		return max
	}
	return out
}

func normalizedSkillKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func normalizeSkillAgentID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || strings.EqualFold(agentID, "main") {
		return "main"
	}
	return agentID
}

func dedupeStringSlice(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func stringFromMap(m map[string]any, key string) string {
	return strings.TrimSpace(fmt.Sprintf("%v", m[key]))
}

func runtimeOS() string {
	return runtime.GOOS
}

func PromptVisibleSkills(catalog *SkillCatalog) []*ResolvedSkill {
	if catalog == nil {
		return nil
	}
	out := make([]*ResolvedSkill, 0, len(catalog.Skills))
	for _, skill := range catalog.Skills {
		if skill == nil || skill.Skill == nil || !skill.PromptEligible || skill.DisableModelInvocation {
			continue
		}
		out = append(out, skill)
	}
	return out
}

func PromptSkillPath(catalog *SkillCatalog, skill *ResolvedSkill, mirror map[string]string) string {
	if skill == nil || skill.Skill == nil {
		return ""
	}
	if path := strings.TrimSpace(mirror[skill.Skill.SkillKey]); path != "" {
		return path
	}
	return strings.TrimSpace(skill.Skill.FilePath)
}
