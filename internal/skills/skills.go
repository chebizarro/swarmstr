// Package skills implements the Metiq skills runtime:
// SKILL.md parsing, workspace scanning, bundled skills discovery,
// and binary requirement checking.
//
// Skills are SKILL.md files (Markdown with YAML frontmatter) stored in:
//   - The bundled skills directory shipped with the binary (skills/)
//   - The agent workspace directory (user-authored skills)
//   - The managed skills directory (~/.metiq/skills/)
//
// This matches the OpenClaw SKILL.md format for full drop-in compatibility.
package skills

import (
	"bytes"
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

// Manifest is a parsed SKILL.md or legacy YAML skill manifest.
type Manifest struct {
	// Name is the human-readable skill name.  Defaults to directory/file base name.
	Name string `yaml:"name"`

	// Description explains what the skill does (used by the agent for skill selection).
	Description string `yaml:"description"`

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
type SkillMetadata struct {
	OpenClaw *OpenClawMeta `yaml:"openclaw"`
}

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
type Requirements struct {
	Bins    []string `yaml:"bins"`
	AnyBins []string `yaml:"anyBins"`
	Env     []string `yaml:"env"`
	OS      []string `yaml:"os"`
	Config  []string `yaml:"config"`
}

// LegacyInstallStep is a raw shell command install step (legacy YAML skills).
type LegacyInstallStep struct {
	Cmd string `yaml:"cmd"`
	Cwd string `yaml:"cwd"`
}

// ─── Loaded skill ─────────────────────────────────────────────────────────────

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

// IsEnabled returns true unless explicitly disabled in the manifest.
func (s *Skill) IsEnabled() bool {
	if s.Manifest.Enabled != nil {
		return *s.Manifest.Enabled
	}
	return true
}

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
func (s *Skill) Emoji() string {
	if oc := s.openClawMeta(); oc != nil {
		return oc.Emoji
	}
	return ""
}

// InstallSpecs returns structured install specs for the skill.
func (s *Skill) InstallSpecs() []InstallSpec {
	if oc := s.openClawMeta(); oc != nil {
		return oc.Install
	}
	return nil
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
func parseFrontmatter(data []byte) (frontmatter []byte, body []byte, err error) {
	// Normalise line endings.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	const delim = "---"
	if !bytes.HasPrefix(data, []byte(delim)) {
		return nil, data, nil // no frontmatter
	}
	// Skip the opening delimiter line.
	rest := data[len(delim):]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	// Find closing delimiter.
	idx := bytes.Index(rest, []byte("\n"+delim))
	if idx < 0 {
		return nil, data, fmt.Errorf("unclosed frontmatter block")
	}
	fm := rest[:idx]
	body = rest[idx+1+len(delim):]
	// Trim the optional newline after the closing delimiter.
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	return fm, body, nil
}

// LoadSkillMD parses a SKILL.md file into a Skill.
// The skill key is taken from the parent directory name.
func LoadSkillMD(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md %q: %w", path, err)
	}

	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter %q: %w", path, err)
	}

	var m Manifest
	if len(fm) > 0 {
		// Pre-process: normalise JSON5-style SKILL.md frontmatter for yaml.v3.
		fm = preprocessFrontmatter(fm)
		if yamlErr := yaml.Unmarshal(fm, &m); yamlErr != nil {
			return nil, fmt.Errorf("parse YAML frontmatter %q: %w", path, yamlErr)
		}
	}
	m.Body = string(bytes.TrimSpace(body))

	// Skill key = parent directory name.
	baseDir := filepath.Dir(path)
	skillKey := filepath.Base(baseDir)
	// Allow metadata.openclaw.skillKey override.
	if oc := m.Metadata; oc != nil && oc.OpenClaw != nil && oc.OpenClaw.SkillKey != "" {
		skillKey = oc.OpenClaw.SkillKey
	}
	if m.Name == "" {
		m.Name = skillKey
	}

	req := Requirements{}
	if oc := m.Metadata; oc != nil && oc.OpenClaw != nil && oc.OpenClaw.Requires != nil {
		r := oc.OpenClaw.Requires
		req = Requirements{
			Bins: r.Bins, AnyBins: r.AnyBins, Env: r.Env,
			OS: r.OS, Config: r.Config,
		}
		// Merge top-level openclaw.os.
		if len(oc.OpenClaw.OS) > 0 {
			osSet := map[string]struct{}{}
			for _, v := range req.OS {
				osSet[v] = struct{}{}
			}
			for _, v := range oc.OpenClaw.OS {
				osSet[v] = struct{}{}
			}
			req.OS = nil
			for v := range osSet {
				req.OS = append(req.OS, v)
			}
			sort.Strings(req.OS)
		}
	}

	missing, eligible := CheckRequirements(req)
	return &Skill{
		SkillKey: skillKey,
		FilePath: path,
		BaseDir:  baseDir,
		Manifest: m,
		Missing:  missing,
		Eligible: eligible,
	}, nil
}

// preprocessFrontmatter normalises SKILL.md YAML frontmatter so that
// gopkg.in/yaml.v3 can parse it.  It handles two OpenClaw-specific patterns:
//
//  1. Trailing commas in flow mappings/sequences (JSON5 style).
//  2. Flow collections starting on a new line after a block key:
//     `key:\n  {` → `key: {`  and  `key:\n  [` → `key: [`
//
// gopkg.in/yaml.v3 does not allow flow scalars to begin on a new line after a
// block mapping key (the YAML spec technically allows it, but yaml.v3 rejects it).
func preprocessFrontmatter(data []byte) []byte {
	// Step 1: join "key:\n  {" and "key:\n  [" patterns.
	data = joinFlowOnNextLine(data)
	// Step 2: remove trailing commas before } or ].
	for {
		next := trailingCommaPass(data)
		if bytes.Equal(next, data) {
			break
		}
		data = next
	}
	return data
}

// joinFlowOnNextLine joins lines where a block mapping key has its flow
// collection start on the next line:
//
//	`  "key":\n      {`  →  `  "key": {`
//	`  "key":\n      [`  →  `  "key": [`
func joinFlowOnNextLine(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	var out [][]byte
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := bytes.TrimRight(line, " \t")
		if bytes.HasSuffix(trimmed, []byte(":")) && i+1 < len(lines) {
			nextContent := bytes.TrimLeft(lines[i+1], " \t")
			if bytes.HasPrefix(nextContent, []byte("{")) || bytes.HasPrefix(nextContent, []byte("[")) {
				// Join: "key:" + " " + nextContent
				joined := append(append([]byte(nil), trimmed...), ' ')
				joined = append(joined, nextContent...)
				out = append(out, joined)
				i++ // skip next line
				continue
			}
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}

func trailingCommaPass(data []byte) []byte {
	var buf bytes.Buffer
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimRight(line, " \t")
		if bytes.HasSuffix(trimmed, []byte(",")) && i+1 < len(lines) {
			nextTrimmed := bytes.TrimLeft(lines[i+1], " \t")
			if bytes.HasPrefix(nextTrimmed, []byte("}")) || bytes.HasPrefix(nextTrimmed, []byte("]")) {
				// Remove trailing comma.
				line = trimmed[:len(trimmed)-1]
			}
		}
		buf.Write(line)
		if i < len(lines)-1 {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

// ─── Legacy YAML manifest loader ─────────────────────────────────────────────

// LoadManifest parses a legacy YAML skill manifest file (.yaml/.yml) into a Skill.
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

// ─── Bundled skills directory resolution ─────────────────────────────────────

// BundledSkillsDir returns the directory containing the bundled SKILL.md files.
// Resolution order:
//  1. METIQ_BUNDLED_SKILLS_DIR env var
//  2. A `skills/` directory next to the running executable
//  3. A `skills/` directory walked up from the current working directory (dev mode)
func BundledSkillsDir() string {
	// 1. Explicit override.
	if override := strings.TrimSpace(os.Getenv("METIQ_BUNDLED_SKILLS_DIR")); override != "" {
		return override
	}

	// 2. Sibling to the executable (production binary).
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "skills")
		if looksLikeBundledSkillsDir(sibling) {
			return sibling
		}
	}

	// 3. Walk up from cwd looking for a `skills/` directory (dev / repo checkout).
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	current := cwd
	for depth := 0; depth < 8; depth++ {
		candidate := filepath.Join(current, "skills")
		if looksLikeBundledSkillsDir(candidate) {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return ""
}

// looksLikeBundledSkillsDir returns true if dir contains at least one SKILL.md
// in a subdirectory — the canonical bundled skills structure.
func looksLikeBundledSkillsDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			return true
		}
	}
	return false
}

// ─── Scanners ─────────────────────────────────────────────────────────────────

// ScanBundledDir scans a bundled skills directory for SKILL.md files.
// Each immediate subdirectory is checked for a SKILL.md file.
// Returns all found skills sorted by skill key.
func ScanBundledDir(dir string) ([]*Skill, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read bundled skills dir %q: %w", dir, err)
	}

	var skills []*Skill
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillMDPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillMDPath); err != nil {
			continue // no SKILL.md in this subdir, skip
		}
		s, err := LoadSkillMD(skillMDPath)
		if err != nil {
			continue // skip malformed skills
		}
		s.Bundled = true
		skills = append(skills, s)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].SkillKey < skills[j].SkillKey
	})
	return skills, nil
}

// ScanWorkspace scans a workspace directory for skill files.
// It finds both SKILL.md files (in subdirectories) and legacy .yaml/.yml files (flat).
// Non-skill files and directories are silently skipped.
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

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		base := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			// Check for SKILL.md in this subdirectory.
			skillMDPath := filepath.Join(path, "SKILL.md")
			if _, statErr := os.Stat(skillMDPath); statErr == nil {
				s, loadErr := LoadSkillMD(skillMDPath)
				if loadErr == nil {
					skills = append(skills, s)
				}
				return filepath.SkipDir // don't recurse further into skill dirs
			}
			return nil
		}
		// Legacy: flat .yaml/.yml skill files.
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yaml" || ext == ".yml" {
			s, loadErr := LoadManifest(path)
			if loadErr == nil {
				skills = append(skills, s)
			}
		}
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
		for _, osName := range req.OS {
			if strings.EqualFold(strings.TrimSpace(osName), currentOS) {
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
// Resolution order:
//  1. extra["skills"]["workspace"] config key
//  2. METIQ_WORKSPACE env var
//  3. ~/metiq/workspace/<agentID>
func WorkspaceDir(extra map[string]any, agentID string) string {
	if extra != nil {
		if rawSkills, ok := extra["skills"].(map[string]any); ok {
			if ws, ok := rawSkills["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
				return strings.TrimSpace(ws)
			}
		}
	}
	if ws := os.Getenv("METIQ_WORKSPACE"); strings.TrimSpace(ws) != "" {
		return strings.TrimSpace(ws)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	if agentID == "" {
		agentID = "main"
	}
	return filepath.Join(home, "metiq", "workspace", agentID)
}

// ManagedSkillsDir returns the directory where installed/managed skills are stored.
// Resolution: METIQ_MANAGED_SKILLS_DIR env → ~/.metiq/skills
func ManagedSkillsDir() string {
	if d := strings.TrimSpace(os.Getenv("METIQ_MANAGED_SKILLS_DIR")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".metiq", "skills")
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
		// Legacy YAML bins.
		for _, b := range s.Manifest.Bins {
			push(b)
		}
		req := s.EffectiveRequirements()
		for _, b := range req.Bins {
			push(b)
		}
		for _, b := range req.AnyBins {
			push(b)
		}
		// Install spec bins (all options, not just active one).
		for _, spec := range s.InstallSpecs() {
			for _, b := range spec.Bins {
				push(b)
			}
		}
	}
	sort.Strings(out)
	return out
}
