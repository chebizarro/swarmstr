package skills

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestBuildSkillCatalogPrecedenceAndConfigOverlay(t *testing.T) {
	InvalidateSkillCatalogCache()
	var logBuf bytes.Buffer
	origWriter := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer log.SetOutput(origWriter)
	defer log.SetFlags(origFlags)
	bundledDir := t.TempDir()
	managedDir := t.TempDir()
	workspaceDir := t.TempDir()
	extraDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", managedDir)
	t.Setenv("METIQ_WORKSPACE", workspaceDir)

	writeSkillMD(t, extraDir, "dup", `---
name: dup
description: from extra
metadata:
  openclaw:
    requires:
      env: ["DUP_API_KEY"]
    primaryEnv: DUP_API_KEY
---
# Extra
`)
	writeSkillMD(t, bundledDir, "dup", `---
name: dup
description: from bundled
metadata:
  openclaw:
    requires:
      env: ["DUP_API_KEY"]
    primaryEnv: DUP_API_KEY
---
# Bundled
`)
	writeSkillMD(t, managedDir, "dup", `---
name: dup
description: from managed
metadata:
  openclaw:
    requires:
      env: ["DUP_API_KEY"]
    primaryEnv: DUP_API_KEY
---
# Managed
`)
	writeSkillMD(t, workspaceDir, "dup", `---
name: dup
description: from workspace
metadata:
  openclaw:
    requires:
      env: ["DUP_API_KEY"]
    primaryEnv: DUP_API_KEY
---
# Workspace
`)

	cfg := state.ConfigDoc{Extra: map[string]any{
		"skills": map[string]any{
			"extra_dirs": []any{extraDir},
			"entries": map[string]any{
				"dup": map[string]any{
					"api_key": "token",
				},
			},
		},
	}}

	catalog, err := BuildSkillCatalog(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog: %v", err)
	}
	if len(catalog.Skills) != 1 {
		t.Fatalf("expected 1 resolved skill, got %d", len(catalog.Skills))
	}
	resolved := catalog.Skills[0]
	if resolved.SourceKind != SkillSourceWorkspace {
		t.Fatalf("expected workspace precedence, got %s", resolved.SourceKind)
	}
	if got := resolved.Skill.Manifest.Description; got != "from workspace" {
		t.Fatalf("expected workspace description, got %q", got)
	}
	if !resolved.Eligible {
		t.Fatalf("expected config api_key to satisfy primary env requirement")
	}
	if resolved.PrimaryEnv != "DUP_API_KEY" {
		t.Fatalf("unexpected primary env: %q", resolved.PrimaryEnv)
	}
	if !strings.Contains(logBuf.String(), "skills: shadowed skill key=dup") {
		t.Fatalf("expected shadow log for duplicate skill, got %q", logBuf.String())
	}
}

func TestSkillCatalogFingerprintIgnoresUnrelatedConfigButTracksRequiredConfigPaths(t *testing.T) {
	InvalidateSkillCatalogCache()
	bundledDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", t.TempDir())
	t.Setenv("METIQ_WORKSPACE", t.TempDir())
	writeSkillMD(t, bundledDir, "cfgskill", `---
name: cfgskill
description: config gated
metadata:
  openclaw:
    requires:
      config: ["extra.feature.enabled"]
---
# Cfgskill
`)
	cfgA := state.ConfigDoc{Extra: map[string]any{"skills": map[string]any{"prompt": map[string]any{"max_count": 10}}, "unrelated": map[string]any{"foo": "bar"}}}
	cfgB := state.ConfigDoc{Extra: map[string]any{"skills": map[string]any{"prompt": map[string]any{"max_count": 10}}, "unrelated": map[string]any{"foo": "baz"}}}
	catalogA, err := BuildSkillCatalog(cfgA, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog A: %v", err)
	}
	catalogB, err := BuildSkillCatalog(cfgB, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog B: %v", err)
	}
	if catalogA.Fingerprint != catalogB.Fingerprint {
		t.Fatalf("expected unrelated config change to keep fingerprint stable: %q vs %q", catalogA.Fingerprint, catalogB.Fingerprint)
	}
	if len(catalogA.Skills) != 1 || catalogA.Skills[0].Eligible {
		t.Fatalf("expected cfgskill to start ineligible: %#v", catalogA.Skills)
	}
	cfgC := state.ConfigDoc{Extra: map[string]any{"skills": map[string]any{"prompt": map[string]any{"max_count": 10}}, "feature": map[string]any{"enabled": true}}}
	catalogC, err := BuildSkillCatalog(cfgC, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog C: %v", err)
	}
	if catalogC.Fingerprint != catalogA.Fingerprint {
		t.Fatalf("expected required-config change to reuse base fingerprint, got %q vs %q", catalogC.Fingerprint, catalogA.Fingerprint)
	}
	if len(catalogC.Skills) != 1 || !catalogC.Skills[0].Eligible {
		t.Fatalf("expected cfgskill to become eligible after required config change: %#v", catalogC.Skills)
	}
}

func TestBuildSkillCatalogReturnsDefensiveCacheCopy(t *testing.T) {
	InvalidateSkillCatalogCache()
	bundledDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", t.TempDir())
	t.Setenv("METIQ_WORKSPACE", t.TempDir())
	writeSkillMD(t, bundledDir, "cache-skill", `---
name: cache-skill
description: original description
---
# Cache Skill
`)
	cfg := state.ConfigDoc{}
	first, err := BuildSkillCatalog(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog first: %v", err)
	}
	if len(first.Skills) != 1 {
		t.Fatalf("expected one skill, got %#v", first.Skills)
	}
	first.Skills[0].Skill.Manifest.Description = "mutated description"
	first.Skills[0].Status = "mutated"
	first.Skills = nil

	second, err := BuildSkillCatalog(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog second: %v", err)
	}
	if len(second.Skills) != 1 {
		t.Fatalf("cached catalog was mutated through first result: %#v", second.Skills)
	}
	if got := second.Skills[0].Skill.Manifest.Description; got != "original description" {
		t.Fatalf("cached nested skill was mutated through first result: %q", got)
	}
	if second.Skills[0].Status == "mutated" {
		t.Fatalf("cached resolved skill status was mutated through first result")
	}
}

func TestBuildSkillCatalogAllowlistAndAlwaysPromptEligibility(t *testing.T) {
	InvalidateSkillCatalogCache()
	bundledDir := t.TempDir()
	workspaceDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", t.TempDir())
	t.Setenv("METIQ_WORKSPACE", workspaceDir)

	writeSkillMD(t, bundledDir, "blocked", `---
name: blocked
description: blocked bundled skill
---
# Blocked
`)
	writeSkillMD(t, bundledDir, "always-skill", `---
name: always-skill
description: always prompt skill
metadata:
  openclaw:
    always: true
    requires:
      env: ["MISSING_TOKEN"]
---
# Always
`)

	cfg := state.ConfigDoc{Extra: map[string]any{
		"skills": map[string]any{
			"allow_bundled": []any{"always-skill"},
		},
	}}
	catalog, err := BuildSkillCatalog(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog: %v", err)
	}
	byKey := map[string]*ResolvedSkill{}
	for _, skill := range catalog.Skills {
		byKey[skill.Skill.SkillKey] = skill
	}
	if byKey["blocked"] == nil || byKey["blocked"].Status != "blocked" || byKey["blocked"].PromptEligible {
		t.Fatalf("expected bundled allowlist to block skill: %#v", byKey["blocked"])
	}
	if byKey["always-skill"] == nil || byKey["always-skill"].Status != "always" || !byKey["always-skill"].PromptEligible || byKey["always-skill"].Eligible {
		t.Fatalf("expected always skill to remain prompt-eligible but not fully eligible: %#v", byKey["always-skill"])
	}
}

func TestResolveAgentWorkspaceDirPrefersTypedAgentWorkspace(t *testing.T) {
	cfg := state.ConfigDoc{
		Agents: state.AgentsConfig{{ID: "coder", WorkspaceDir: filepath.Join(t.TempDir(), "agent-ws")}},
		Extra:  map[string]any{"skills": map[string]any{"workspace": "/fallback/workspace"}},
	}
	if got := ResolveAgentWorkspaceDir(cfg, "coder"); got != cfg.Agents[0].WorkspaceDir {
		t.Fatalf("expected typed workspace dir, got %q", got)
	}
}

func TestResolveInstallPreferencesDefaultsAndNormalization(t *testing.T) {
	prefs := ResolveInstallPreferences(state.ConfigDoc{})
	if !prefs.PreferBrew || prefs.NodeManager != "npm" {
		t.Fatalf("unexpected default prefs: %#v", prefs)
	}
	cfg := state.ConfigDoc{Extra: map[string]any{"skills": map[string]any{"install": map[string]any{"prefer_brew": false, "node_manager": "pnpm"}}}}
	prefs = ResolveInstallPreferences(cfg)
	if prefs.PreferBrew || prefs.NodeManager != "pnpm" {
		t.Fatalf("unexpected configured prefs: %#v", prefs)
	}
	cfg = state.ConfigDoc{Extra: map[string]any{"skills": map[string]any{"install": map[string]any{"node_manager": "invalid"}}}}
	prefs = ResolveInstallPreferences(cfg)
	if prefs.NodeManager != "npm" {
		t.Fatalf("expected invalid node manager to fall back to npm: %#v", prefs)
	}
}

func TestSkillCatalogIgnoresPluginEntriesAsSkillSources(t *testing.T) {
	InvalidateSkillCatalogCache()
	bundledDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", t.TempDir())
	t.Setenv("METIQ_WORKSPACE", t.TempDir())
	cfg := state.ConfigDoc{Extra: map[string]any{
		"extensions": map[string]any{
			"entries": map[string]any{
				"demo-plugin": map[string]any{
					"enabled":      true,
					"install_path": "/tmp/demo-plugin",
					"plugin_type":  "node",
				},
			},
		},
	}}
	catalog, err := BuildSkillCatalog(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog: %v", err)
	}
	if len(catalog.Skills) != 0 {
		t.Fatalf("expected plugin config to not add skill sources, got %#v", catalog.Skills)
	}
	cfg2 := state.ConfigDoc{Extra: map[string]any{
		"extensions": map[string]any{
			"entries": map[string]any{
				"demo-plugin": map[string]any{
					"enabled":      true,
					"install_path": "/tmp/other-plugin",
					"plugin_type":  "node",
				},
			},
		},
	}}
	catalog2, err := BuildSkillCatalog(cfg2, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog second config: %v", err)
	}
	if catalog.Fingerprint != catalog2.Fingerprint {
		t.Fatalf("expected plugin-only config changes to stay outside skill fingerprint: %q vs %q", catalog.Fingerprint, catalog2.Fingerprint)
	}
}

func TestSkillCatalogRevalidatesRuntimeEnvDependencies(t *testing.T) {
	InvalidateSkillCatalogCache()
	bundledDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", t.TempDir())
	t.Setenv("METIQ_WORKSPACE", t.TempDir())
	writeSkillMD(t, bundledDir, "envskill", `---
name: envskill
description: env gated
metadata:
  openclaw:
    requires:
      env: ["ENVSKILL_TOKEN"]
    primaryEnv: ENVSKILL_TOKEN
---
# Envskill
`)
	cfg := state.ConfigDoc{}
	t.Setenv("ENVSKILL_TOKEN", "")
	catalogA, err := BuildSkillCatalog(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog A: %v", err)
	}
	if len(catalogA.Skills) != 1 || catalogA.Skills[0].Eligible {
		t.Fatalf("expected envskill to start ineligible: %#v", catalogA.Skills)
	}
	fingerprint := catalogA.Fingerprint
	t.Setenv("ENVSKILL_TOKEN", "token")
	catalogB, err := BuildSkillCatalog(cfg, "main")
	if err != nil {
		t.Fatalf("BuildSkillCatalog B: %v", err)
	}
	if catalogB.Fingerprint != fingerprint {
		t.Fatalf("expected env-only change to keep base fingerprint stable: %q vs %q", catalogB.Fingerprint, fingerprint)
	}
	if len(catalogB.Skills) != 1 || !catalogB.Skills[0].Eligible {
		t.Fatalf("expected envskill to become eligible after env change: %#v", catalogB.Skills)
	}
}
