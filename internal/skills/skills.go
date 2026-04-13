package skills

import (
	"sort"
	"strings"
)

// Manifest is a parsed SKILL.md or legacy YAML skill manifest.
type Manifest struct {
	// Name is the human-readable skill name.  Defaults to directory/file base name.
	Name string `yaml:"name"`

	// Description explains what the skill does (used by the agent for skill selection).
	Description string `yaml:"description"`

	// WhenToUse gives the model explicit invocation guidance.
	WhenToUse string `yaml:"when_to_use"`

	// UserInvocable allows a skill to be hidden from direct user invocation surfaces.
	UserInvocable *bool `yaml:"user-invocable"`

	// DisableModelInvocation keeps a skill out of automatic prompt discovery.
	DisableModelInvocation *bool `yaml:"disable-model-invocation"`

	// Homepage is an optional reference URL.
	Homepage string `yaml:"homepage"`

	// Enabled controls whether the skill is active.  Default: true.
	Enabled *bool `yaml:"enabled"`

	// Source is an optional URL or reference for the skill (legacy YAML compat).
	Source string `yaml:"source"`

	// Metadata holds OpenClaw-compatible extended metadata.
	Metadata *SkillMetadata `yaml:"metadata"`

	// Requirements declares what the system needs before this skill works.
	// Used by legacy YAML skills; SKILL.md skills use Metadata.OpenClaw.Requires.
	Requirements Requirements `yaml:"requirements"`

	// Install lists raw shell install commands (legacy YAML compat).
	Install []LegacyInstallStep `yaml:"install"`

	// Bins is a shorthand list of binaries this skill exposes (legacy YAML compat).
	Bins []string `yaml:"bins"`

	// Body is the Markdown body of the SKILL.md (agent usage instructions).
	Body string `yaml:"-"`
}

// SkillMetadata is the SKILL.md top-level `metadata` field.
// SkillMetadata is the SKILL.md top-level `metadata` field.
type SkillMetadata struct {
	OpenClaw *OpenClawMeta `yaml:"openclaw"`
}

// OpenClawMeta is the `metadata.openclaw` block in a SKILL.md.
// OpenClawMeta is the `metadata.openclaw` block in a SKILL.md.
type OpenClawMeta struct {
	// Emoji is the display emoji for CLI/UI.
	Emoji string `yaml:"emoji"`

	// OS lists OS identifiers this skill supports at the top level
	// (e.g. ["darwin", "linux"]).  Also honoured inside Requires.OS.
	OS []string `yaml:"os"`

	// Requires declares binary/env/config prerequisites.
	Requires *OpenClawRequires `yaml:"requires"`

	// Install lists structured install specs (brew/npm/go/uv/download).
	Install []InstallSpec `yaml:"install"`

	// PrimaryEnv is the name of the primary API key env var (for quick check).
	PrimaryEnv string `yaml:"primaryEnv"`

	// Always forces the skill into context even when requirements are unmet.
	Always bool `yaml:"always"`

	// SkillKey overrides the default directory-name skill key.
	SkillKey string `yaml:"skillKey"`
}

// OpenClawRequires is the `metadata.openclaw.requires` block.
// OpenClawRequires is the `metadata.openclaw.requires` block.
type OpenClawRequires struct {
	// Bins are binaries that must ALL be present on PATH.
	Bins []string `yaml:"bins"`

	// AnyBins requires at least one of these binaries to be present.
	AnyBins []string `yaml:"anyBins"`

	// Env lists environment variable names that must be set.
	Env []string `yaml:"env"`

	// OS lists OS identifiers that this skill's requirements need.
	OS []string `yaml:"os"`

	// Config lists config key paths that must be set.
	Config []string `yaml:"config"`
}

// InstallSpec is a structured install option in a SKILL.md (brew/npm/go/uv/download/apt).
// InstallSpec is a structured install option in a SKILL.md (brew/npm/go/uv/download/apt).
type InstallSpec struct {
	// ID is a unique identifier for this install option (e.g. "brew", "apt-0").
	ID string `yaml:"id"`

	// Kind is the installer type: "brew", "npm", "go", "uv", "download", "apt".
	Kind string `yaml:"kind"`

	// Formula is the brew formula name (Kind=="brew").
	Formula string `yaml:"formula"`

	// Package is the npm/apt/uv package name.
	Package string `yaml:"package"`

	// Module is the Go module path (Kind=="go").
	Module string `yaml:"module"`

	// URL is the download URL (Kind=="download").
	URL string `yaml:"url"`

	// Bins lists binaries provided by this install.
	Bins []string `yaml:"bins"`

	// Label is the human-readable install action label.
	Label string `yaml:"label"`

	// OS lists platforms this install spec applies to (empty = all).
	OS []string `yaml:"os"`
}

// Requirements describes system prerequisites (legacy YAML skill format).
// Requirements describes system prerequisites (legacy YAML skill format).
type Requirements struct {
	Bins    []string `yaml:"bins"`
	AnyBins []string `yaml:"anyBins"`
	Env     []string `yaml:"env"`
	OS      []string `yaml:"os"`
	Config  []string `yaml:"config"`
}

// LegacyInstallStep is a raw shell command install step (legacy YAML skills).
// LegacyInstallStep is a raw shell command install step (legacy YAML skills).
type LegacyInstallStep struct {
	Cmd string `yaml:"cmd"`
	Cwd string `yaml:"cwd"`
}

// ─── Loaded skill ─────────────────────────────────────────────────────────────

// Skill is a fully resolved skill: manifest + filesystem metadata + requirement check.
// Skill is a fully resolved skill: manifest + filesystem metadata + requirement check.
type Skill struct {
	// SkillKey is the unique identifier (directory name for SKILL.md; file base for .yaml).
	SkillKey string

	// FilePath is the absolute path to the SKILL.md or .yaml manifest.
	FilePath string

	// BaseDir is the directory containing the manifest.
	BaseDir string

	// Bundled is true for skills loaded from the bundled skills directory.
	Bundled bool

	// Manifest is the parsed content.
	Manifest Manifest

	// Missing lists which requirements are not satisfied.
	Missing Requirements

	// Eligible is true when all requirements are satisfied.
	Eligible bool
}

// MaxSkillFileBytes caps manifest size loaded from disk to avoid oversized prompt assets.
// MaxSkillFileBytes caps manifest size loaded from disk to avoid oversized prompt assets.
const MaxSkillFileBytes = 256 * 1024

// IsEnabled returns true unless explicitly disabled in the manifest.
// IsEnabled returns true unless explicitly disabled in the manifest.
func (s *Skill) IsEnabled() bool {
	if s.Manifest.Enabled != nil {
		return *s.Manifest.Enabled
	}
	return true
}

// EffectiveRequirements returns the unified requirements from either the
// OpenClaw metadata block (SKILL.md) or the legacy Requirements field (.yaml).
// EffectiveRequirements returns the unified requirements from either the
// OpenClaw metadata block (SKILL.md) or the legacy Requirements field (.yaml).
func (s *Skill) EffectiveRequirements() Requirements {
	if oc := s.openClawMeta(); oc != nil && oc.Requires != nil {
		r := oc.Requires
		req := Requirements{
			Bins:    r.Bins,
			AnyBins: r.AnyBins,
			Env:     r.Env,
			Config:  r.Config,
		}
		// Merge OS from requires and top-level openclaw.os.
		osSet := map[string]struct{}{}
		for _, v := range r.OS {
			osSet[v] = struct{}{}
		}
		for _, v := range oc.OS {
			osSet[v] = struct{}{}
		}
		for v := range osSet {
			req.OS = append(req.OS, v)
		}
		sort.Strings(req.OS)
		return req
	}
	return s.Manifest.Requirements
}

// Emoji returns the display emoji (from OpenClaw metadata or empty string).
// Emoji returns the display emoji (from OpenClaw metadata or empty string).
func (s *Skill) Emoji() string {
	if oc := s.openClawMeta(); oc != nil {
		return oc.Emoji
	}
	return ""
}

// InstallSpecs returns structured install specs for the skill.
// InstallSpecs returns structured install specs for the skill.
func (s *Skill) InstallSpecs() []InstallSpec {
	if oc := s.openClawMeta(); oc != nil {
		specs := make([]InstallSpec, 0, len(oc.Install))
		for _, spec := range oc.Install {
			if normalized, ok := normalizeAndValidateInstallSpec(spec); ok {
				specs = append(specs, normalized)
			}
		}
		return specs
	}
	return nil
}

func (s *Skill) PrimaryEnv() string {
	if oc := s.openClawMeta(); oc != nil {
		return strings.TrimSpace(oc.PrimaryEnv)
	}
	return ""
}

func (s *Skill) Always() bool {
	if oc := s.openClawMeta(); oc != nil {
		return oc.Always
	}
	return false
}

func (s *Skill) WhenToUse() string {
	return strings.TrimSpace(s.Manifest.WhenToUse)
}

func (s *Skill) UserInvocable() bool {
	if s.Manifest.UserInvocable != nil {
		return *s.Manifest.UserInvocable
	}
	return true
}

func (s *Skill) DisableModelInvocation() bool {
	if s.Manifest.DisableModelInvocation != nil {
		return *s.Manifest.DisableModelInvocation
	}
	return false
}

func (s *Skill) openClawMeta() *OpenClawMeta {
	if s.Manifest.Metadata != nil {
		return s.Manifest.Metadata.OpenClaw
	}
	return nil
}

// ─── SKILL.md parser ─────────────────────────────────────────────────────────

// parseFrontmatter splits a SKILL.md file into YAML frontmatter and Markdown body.
// The file must start with "---\n" and have a closing "---\n" or "---" line.
