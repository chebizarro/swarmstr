// Package skills implements the Swarmstr skills runtime:
// workspace scanning, YAML manifest loading, and binary requirement checking.
//
// OpenClaw compatibility: skills are YAML files discovered in the agent
// workspace directory.  Each manifest declares what binaries, env vars,
// and OS constraints are required before the skill can be used.
package skills

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ─── Manifest types ───────────────────────────────────────────────────────────

// Manifest is a parsed skill YAML manifest file.
type Manifest struct {
	// Name is the human-readable skill name.  Defaults to file base name.
	Name string `yaml:"name"`

	// Description explains what the skill does.
	Description string `yaml:"description"`

	// Enabled controls whether the skill is active.  Default: true.
	Enabled *bool `yaml:"enabled"`

	// Source is an optional URL or reference for the skill.
	Source string `yaml:"source"`

	// Requirements declares what the system needs before this skill works.
	Requirements Requirements `yaml:"requirements"`

	// Install lists commands to install the skill's dependencies.
	Install []InstallStep `yaml:"install"`

	// Bins is a shorthand list of binaries this skill exposes to the agent.
	Bins []string `yaml:"bins"`
}

// Requirements describes system prerequisites for a skill.
type Requirements struct {
	// Bins are binaries that must ALL be present on PATH.
	Bins []string `yaml:"bins"`

	// AnyBins requires at least one of these binaries to be present.
	AnyBins []string `yaml:"anyBins"`

	// Env lists environment variable names that must be set.
	Env []string `yaml:"env"`

	// OS lists OS identifiers that this skill supports (e.g. "linux", "darwin").
	OS []string `yaml:"os"`

	// Config lists config key paths that must be set.
	Config []string `yaml:"config"`
}

// InstallStep is a single installation command.
type InstallStep struct {
	Cmd string `yaml:"cmd"`
	Cwd string `yaml:"cwd"`
}

// ─── Loaded skill ─────────────────────────────────────────────────────────────

// Skill is a fully resolved skill: manifest + filesystem metadata + requirement check.
type Skill struct {
	// SkillKey is the unique identifier (usually file base name without extension).
	SkillKey string

	// FilePath is the absolute path to the manifest YAML.
	FilePath string

	// BaseDir is the directory containing the manifest.
	BaseDir string

	// Manifest is the parsed content.
	Manifest Manifest

	// Missing lists which requirements are not satisfied.
	Missing Requirements

	// Eligible is true when all requirements are satisfied.
	Eligible bool
}

// IsEnabled returns true unless explicitly disabled in the manifest.
func (s *Skill) IsEnabled() bool {
	if s.Manifest.Enabled != nil {
		return *s.Manifest.Enabled
	}
	return true
}

// ─── Scanner ─────────────────────────────────────────────────────────────────

// ScanWorkspace scans dir for skill YAML manifests and returns loaded skills.
// Non-YAML files and directories are silently skipped.
// dir may be an absolute path; relative paths are resolved from the process cwd.
func ScanWorkspace(dir string) ([]*Skill, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("workspace directory must not be empty")
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // empty workspace is not an error
		}
		return nil, fmt.Errorf("stat workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace path %q is not a directory", dir)
	}

	var skills []*Skill
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			// Don't recurse into hidden dirs or node_modules.
			base := d.Name()
			if strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		skill, err := LoadManifest(path)
		if err != nil {
			// Log skip; don't abort the whole scan.
			return nil
		}
		skills = append(skills, skill)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workspace: %w", err)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].SkillKey < skills[j].SkillKey
	})
	return skills, nil
}

// LoadManifest parses a single YAML manifest file into a Skill.
func LoadManifest(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skill manifest %q: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse skill manifest %q: %w", path, err)
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	skillKey := strings.TrimSuffix(base, ext)
	if m.Name == "" {
		m.Name = skillKey
	}

	skill := &Skill{
		SkillKey: skillKey,
		FilePath: path,
		BaseDir:  filepath.Dir(path),
		Manifest: m,
	}
	skill.Missing, skill.Eligible = CheckRequirements(m.Requirements)
	return skill, nil
}

// ─── Requirement checker ─────────────────────────────────────────────────────

// CheckRequirements verifies all requirement fields and returns which are missing.
// eligible is true when nothing is missing.
func CheckRequirements(req Requirements) (missing Requirements, eligible bool) {
	// Bins: all must be present.
	for _, bin := range req.Bins {
		if !BinExists(bin) {
			missing.Bins = append(missing.Bins, bin)
		}
	}

	// AnyBins: at least one must be present.
	if len(req.AnyBins) > 0 {
		found := false
		for _, bin := range req.AnyBins {
			if BinExists(bin) {
				found = true
				break
			}
		}
		if !found {
			missing.AnyBins = append(missing.AnyBins, req.AnyBins...)
		}
	}

	// Env: all must be set.
	for _, envVar := range req.Env {
		if os.Getenv(envVar) == "" {
			missing.Env = append(missing.Env, envVar)
		}
	}

	// OS: current OS must be in list (if list is non-empty).
	if len(req.OS) > 0 {
		currentOS := runtime.GOOS
		found := false
		for _, os := range req.OS {
			if strings.EqualFold(strings.TrimSpace(os), currentOS) {
				found = true
				break
			}
		}
		if !found {
			missing.OS = req.OS
		}
	}

	eligible = len(missing.Bins) == 0 &&
		len(missing.AnyBins) == 0 &&
		len(missing.Env) == 0 &&
		len(missing.OS) == 0 &&
		len(missing.Config) == 0
	return missing, eligible
}

// BinExists returns true when name can be found on the system PATH.
func BinExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ─── Workspace config helper ──────────────────────────────────────────────────

// WorkspaceDir resolves the agent workspace directory from config.
// It checks (in order):
//  1. extra["skills"]["workspace"]
//  2. SWARMSTR_WORKSPACE env var
//  3. fallback: ~/swarmstr/workspace/<agentID>
func WorkspaceDir(extra map[string]any, agentID string) string {
	if extra != nil {
		if rawSkills, ok := extra["skills"].(map[string]any); ok {
			if ws, ok := rawSkills["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
				return strings.TrimSpace(ws)
			}
		}
	}
	if ws := os.Getenv("SWARMSTR_WORKSPACE"); strings.TrimSpace(ws) != "" {
		return strings.TrimSpace(ws)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	if agentID == "" {
		agentID = "main"
	}
	return filepath.Join(home, "swarmstr", "workspace", agentID)
}

// ─── Bins aggregate ──────────────────────────────────────────────────────────

// AggregateBins collects all unique bin names declared across a set of skills.
func AggregateBins(skills []*Skill) []string {
	seen := map[string]struct{}{}
	var out []string
	push := func(bin string) {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			return
		}
		if _, ok := seen[bin]; ok {
			return
		}
		seen[bin] = struct{}{}
		out = append(out, bin)
	}
	for _, s := range skills {
		for _, b := range s.Manifest.Bins {
			push(b)
		}
		for _, b := range s.Manifest.Requirements.Bins {
			push(b)
		}
		for _, b := range s.Manifest.Requirements.AnyBins {
			push(b)
		}
	}
	sort.Strings(out)
	return out
}
