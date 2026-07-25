package skills

// discovery.go — Skill discovery surface (skills.search / skills.detail /
// skills.securityVerdicts / skills.skillCard gateway methods, swarmstr-xfny.1).
//
// Metiq deviation vs OpenClaw: there is no ClawHub registry. Search ranks the
// merged local catalog (bundled + workspace + managed) with a substring scorer
// over skillKey/name/description/whenToUse; detail/skillCard resolve a local
// skillKey; securityVerdicts reuses the static skill linter (LintPaths) and maps
// each report to an allow/block decision.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"metiq/internal/store/state"
)

// SecurityVerdictsSchema identifies the security-verdicts result schema.
const SecurityVerdictsSchema = "metiq.skills.security-verdicts.v1"

// defaultSkillSearchLimit applies when SearchSkills receives no positive limit
// (mirrors the gateway methods.DefaultSkillsSearchLimit default).
const defaultSkillSearchLimit = 20

// skillSummary derives a short human summary for a resolved skill, preferring
// the manifest description and falling back to the when-to-use guidance.
func skillSummary(resolved *ResolvedSkill) string {
	if resolved == nil || resolved.Skill == nil {
		return ""
	}
	if d := strings.TrimSpace(resolved.Skill.Manifest.Description); d != "" {
		return d
	}
	return strings.TrimSpace(resolved.WhenToUse)
}

// scoreSkillMatch ranks a resolved skill against a normalized lowercase query.
// An empty query matches every skill with a baseline score so search doubles as
// a ranked catalog listing.
func scoreSkillMatch(query string, resolved *ResolvedSkill) int {
	if resolved == nil || resolved.Skill == nil {
		return 0
	}
	if query == "" {
		return 1
	}
	key := normalizedSkillKey(resolved.Skill.SkillKey)
	name := strings.ToLower(strings.TrimSpace(curatorSkillName(resolved)))
	desc := strings.ToLower(strings.TrimSpace(resolved.Skill.Manifest.Description))
	when := strings.ToLower(strings.TrimSpace(resolved.WhenToUse))

	score := 0
	switch {
	case key == query:
		score += 100
	case strings.HasPrefix(key, query):
		score += 60
	case strings.Contains(key, query):
		score += 40
	}
	if strings.Contains(name, query) {
		score += 30
	}
	if strings.Contains(desc, query) {
		score += 15
	}
	if strings.Contains(when, query) {
		score += 10
	}
	return score
}

// SearchSkills ranks the merged catalog against a substring query, returning the
// top matches up to limit (results:[{score,slug,displayName,summary,...}]).
func SearchSkills(cfg state.ConfigDoc, agentID, query string, limit int) (map[string]any, error) {
	catalog, err := BuildSkillCatalog(cfg, agentID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultSkillSearchLimit
	}
	q := strings.ToLower(strings.TrimSpace(query))
	index, keys := catalogSkillIndex(catalog)

	type scoredSkill struct {
		key      string
		score    int
		resolved *ResolvedSkill
	}
	matches := make([]scoredSkill, 0, len(keys))
	for _, key := range keys {
		resolved := index[key]
		s := scoreSkillMatch(q, resolved)
		if s <= 0 {
			continue
		}
		matches = append(matches, scoredSkill{key: key, score: s, resolved: resolved})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].key < matches[j].key
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}

	results := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		resolved := m.resolved
		results = append(results, map[string]any{
			"score":       m.score,
			"slug":        m.key,
			"skillKey":    m.key,
			"displayName": curatorSkillName(resolved),
			"summary":     skillSummary(resolved),
			"description": strings.TrimSpace(resolved.Skill.Manifest.Description),
			"whenToUse":   strings.TrimSpace(resolved.WhenToUse),
			"source":      string(resolved.SourceKind),
			"filePath":    strings.TrimSpace(resolved.Skill.FilePath),
		})
	}
	return map[string]any{
		"query":   strings.TrimSpace(query),
		"count":   len(results),
		"results": results,
	}, nil
}

// resolveCatalogSkill resolves one skill by (local) skillKey against the merged
// catalog, returning state.ErrNotFound when it is not installed.
func resolveCatalogSkill(cfg state.ConfigDoc, agentID, skillKey string) (*ResolvedSkill, error) {
	key := normalizedSkillKey(skillKey)
	if key == "" {
		return nil, fmt.Errorf("skillKey is required")
	}
	catalog, err := BuildSkillCatalog(cfg, agentID)
	if err != nil {
		return nil, err
	}
	index, _ := catalogSkillIndex(catalog)
	resolved, ok := index[key]
	if !ok {
		return nil, state.ErrNotFound
	}
	return resolved, nil
}

func requirementsMap(r Requirements) map[string]any {
	norm := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	return map[string]any{
		"bins":    norm(r.Bins),
		"anyBins": norm(r.AnyBins),
		"env":     norm(r.Env),
		"os":      norm(r.OS),
		"config":  norm(r.Config),
	}
}

// skillDetailEntry renders the detail view for a resolved skill.
func skillDetailEntry(resolved *ResolvedSkill) map[string]any {
	skill := resolved.Skill
	key := normalizedSkillKey(skill.SkillKey)
	return map[string]any{
		"slug":                   key,
		"skillKey":               key,
		"displayName":            curatorSkillName(resolved),
		"name":                   curatorSkillName(resolved),
		"description":            strings.TrimSpace(skill.Manifest.Description),
		"summary":                skillSummary(resolved),
		"whenToUse":              strings.TrimSpace(resolved.WhenToUse),
		"source":                 string(resolved.SourceKind),
		"bundled":                resolved.SourceKind == SkillSourceBundled,
		"status":                 resolved.Status,
		"filePath":               strings.TrimSpace(skill.FilePath),
		"baseDir":                strings.TrimSpace(skill.BaseDir),
		"emoji":                  skill.Emoji(),
		"homepage":               strings.TrimSpace(skill.Manifest.Homepage),
		"primaryEnv":             resolved.PrimaryEnv,
		"always":                 resolved.Always,
		"eligible":               resolved.Eligible,
		"disabled":               resolved.Disabled,
		"blockedByAllowlist":     resolved.BlockedByAllowlist,
		"userInvocable":          resolved.UserInvocable,
		"disableModelInvocation": resolved.DisableModelInvocation,
		"requirements":           requirementsMap(resolved.EffectiveRequirements),
		"missing":                requirementsMap(resolved.Missing),
	}
}

// SkillDetail resolves a local skill by slug (skillKey) and returns its detail.
func SkillDetail(cfg state.ConfigDoc, agentID, slug string) (map[string]any, error) {
	resolved, err := resolveCatalogSkill(cfg, agentID, slug)
	if err != nil {
		return nil, err
	}
	return skillDetailEntry(resolved), nil
}

// securityVerdictsFromPaths runs the skill linter over the given paths and maps
// each report to an allow/block decision (block iff it has any lint error).
func securityVerdictsFromPaths(paths []string, pathKey map[string]string) map[string]any {
	result := LintPaths(paths)
	items := make([]map[string]any, 0, len(result.Reports))
	blocked := 0
	allowed := 0
	for _, report := range result.Reports {
		findings := report.Findings
		if findings == nil {
			findings = []LintFinding{}
		}
		errorCount := 0
		warningCount := 0
		for _, f := range findings {
			switch f.Severity {
			case LintError:
				errorCount++
			case LintWarning:
				warningCount++
			}
		}
		decision := "allow"
		if errorCount > 0 {
			decision = "block"
			blocked++
		} else {
			allowed++
		}
		item := map[string]any{
			"path":         report.Path,
			"decision":     decision,
			"errorCount":   errorCount,
			"warningCount": warningCount,
			"findings":     findings,
		}
		if pathKey != nil {
			if key := pathKey[report.Path]; key != "" {
				item["skillKey"] = key
			}
		}
		items = append(items, item)
	}
	return map[string]any{
		"schema_version": SecurityVerdictsSchema,
		"valid":          result.Valid,
		"blockedCount":   blocked,
		"allowedCount":   allowed,
		"items":          items,
	}
}

// BuildSecurityVerdicts lints every resolved SKILL.md in the merged catalog and
// returns allow/block decisions (schema metiq.skills.security-verdicts.v1).
func BuildSecurityVerdicts(cfg state.ConfigDoc, agentID string) (map[string]any, error) {
	catalog, err := BuildSkillCatalog(cfg, agentID)
	if err != nil {
		return nil, err
	}
	pathKey := map[string]string{}
	paths := make([]string, 0, len(catalog.Skills))
	seen := map[string]struct{}{}
	for _, resolved := range catalog.Skills {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		fp := strings.TrimSpace(resolved.Skill.FilePath)
		if fp == "" {
			continue
		}
		if _, ok := seen[fp]; ok {
			continue
		}
		seen[fp] = struct{}{}
		paths = append(paths, fp)
		pathKey[fp] = normalizedSkillKey(resolved.Skill.SkillKey)
	}
	return securityVerdictsFromPaths(paths, pathKey), nil
}

// BuildSkillCard reads the resolved SKILL.md for one skill, capping content at
// MaxSkillFileBytes and reporting the true on-disk size.
func BuildSkillCard(cfg state.ConfigDoc, agentID, skillKey string) (map[string]any, error) {
	resolved, err := resolveCatalogSkill(cfg, agentID, skillKey)
	if err != nil {
		return nil, err
	}
	path := strings.TrimSpace(resolved.Skill.FilePath)
	if path == "" {
		return nil, fmt.Errorf("skill %q has no resolved SKILL.md", normalizedSkillKey(skillKey))
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	sizeBytes := info.Size()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	truncated := false
	if len(raw) > MaxSkillFileBytes {
		raw = raw[:MaxSkillFileBytes]
		truncated = true
	}
	return map[string]any{
		"skillKey":  normalizedSkillKey(resolved.Skill.SkillKey),
		"name":      curatorSkillName(resolved),
		"path":      path,
		"sizeBytes": sizeBytes,
		"content":   string(raw),
		"truncated": truncated,
		"source":    string(resolved.SourceKind),
	}, nil
}
