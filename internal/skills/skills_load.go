package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

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
// LoadSkillMD parses a SKILL.md file into a Skill.
// The skill key is taken from the parent directory name.
func LoadSkillMD(path string) (*Skill, error) {
	data, err := readLimitedFile(path)
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
	sanitizeManifest(&m)
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
//
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
// LoadManifest parses a legacy YAML skill manifest file (.yaml/.yml) into a Skill.
func LoadManifest(path string) (*Skill, error) {
	data, err := readLimitedFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skill manifest %q: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse skill manifest %q: %w", path, err)
	}
	sanitizeManifest(&m)
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
func readLimitedFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxSkillFileBytes {
		return nil, fmt.Errorf("skill file exceeds max size (%d bytes)", MaxSkillFileBytes)
	}
	return os.ReadFile(path)
}

func sanitizeManifest(m *Manifest) {
	if m.Metadata == nil || m.Metadata.OpenClaw == nil {
		return
	}
	specs := make([]InstallSpec, 0, len(m.Metadata.OpenClaw.Install))
	for _, spec := range m.Metadata.OpenClaw.Install {
		if normalized, ok := normalizeAndValidateInstallSpec(spec); ok {
			specs = append(specs, normalized)
		}
	}
	m.Metadata.OpenClaw.Install = specs
}
