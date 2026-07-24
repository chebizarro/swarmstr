package skills

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type LintSeverity string

const (
	LintError   LintSeverity = "error"
	LintWarning LintSeverity = "warning"
)

// LintFinding is one actionable static skill validation result.
type LintFinding struct {
	Severity LintSeverity `json:"severity"`
	Code     string       `json:"code"`
	Field    string       `json:"field,omitempty"`
	Message  string       `json:"message"`
}

// LintReport is stable, machine-readable output for editor/CI integrations.
type LintReport struct {
	Path     string        `json:"path,omitempty"`
	Findings []LintFinding `json:"findings,omitempty"`
}

// LintResult aggregates deterministic reports for CLI and CI consumers.
const LintSchemaVersion = "metiq.skills.lint.v1"

type LintResult struct {
	SchemaVersion string       `json:"schema_version"`
	Valid         bool         `json:"valid"`
	ErrorCount    int          `json:"error_count"`
	WarningCount  int          `json:"warning_count"`
	Reports       []LintReport `json:"reports"`
}

// LintPaths lints explicit skill files or recursively discovers supported
// manifests beneath directories. Parse/read failures are represented as stable
// findings so JSON consumers always receive a complete result set.
func LintPaths(paths []string) LintResult {
	files := make([]string, 0)
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil {
			files = append(files, candidate)
			continue
		}
		if !info.IsDir() {
			files = append(files, candidate)
			continue
		}
		_ = filepath.WalkDir(candidate, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				files = append(files, path)
				return nil
			}
			if entry.IsDir() {
				if path != candidate && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if isLintableSkillFile(entry.Name()) {
				files = append(files, path)
			}
			return nil
		})
	}
	sort.Strings(files)
	files = uniqueLintPaths(files)
	result := LintResult{SchemaVersion: LintSchemaVersion, Valid: true, Reports: make([]LintReport, 0, len(files))}
	for _, file := range files {
		report, err := LintSkillFile(file)
		if err != nil {
			report = LintReport{Path: file, Findings: []LintFinding{{Severity: LintError, Code: "parse-error", Message: err.Error()}}}
		}
		for _, finding := range report.Findings {
			switch finding.Severity {
			case LintError:
				result.ErrorCount++
			case LintWarning:
				result.WarningCount++
			}
		}
		result.Reports = append(result.Reports, report)
	}
	result.Valid = result.ErrorCount == 0
	return result
}

func isLintableSkillFile(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "skill.md" || lower == "skill.yaml" || lower == "skill.yml"
}

func uniqueLintPaths(paths []string) []string {
	out := paths[:0]
	last := ""
	for _, path := range paths {
		if path == last {
			continue
		}
		out = append(out, path)
		last = path
	}
	return out
}

func (r LintReport) Valid() bool {
	for _, finding := range r.Findings {
		if finding.Severity == LintError {
			return false
		}
	}
	return true
}

var (
	skillKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	envNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// LintSkillFile performs strict static validation without executing requirement
// checks or installer commands.
func LintSkillFile(filePath string) (LintReport, error) {
	data, err := readLimitedFile(filePath)
	if err != nil {
		return LintReport{}, err
	}
	var manifest Manifest
	isSkillMD := strings.EqualFold(filepath.Base(filePath), "SKILL.md")
	if isSkillMD {
		frontmatter, body, err := parseFrontmatter(data)
		if err != nil {
			return LintReport{}, err
		}
		if len(frontmatter) == 0 {
			return LintReport{Path: filePath, Findings: []LintFinding{{
				Severity: LintError, Code: "missing-frontmatter", Field: "frontmatter", Message: "SKILL.md must declare YAML frontmatter",
			}}}, nil
		}
		if err := decodeStrictSkillYAML(preprocessFrontmatter(frontmatter), &manifest); err != nil {
			return LintReport{}, fmt.Errorf("parse skill frontmatter: %w", err)
		}
		manifest.Body = strings.TrimSpace(string(body))
	} else {
		if err := decodeStrictSkillYAML(data, &manifest); err != nil {
			return LintReport{}, fmt.Errorf("parse skill manifest: %w", err)
		}
	}
	skillKey := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	if isSkillMD {
		skillKey = filepath.Base(filepath.Dir(filePath))
	}
	if manifest.Metadata != nil && manifest.Metadata.OpenClaw != nil && strings.TrimSpace(manifest.Metadata.OpenClaw.SkillKey) != "" {
		skillKey = strings.TrimSpace(manifest.Metadata.OpenClaw.SkillKey)
	}
	report := LintManifest(skillKey, manifest, isSkillMD)
	report.Path = filePath
	return report, nil
}

func decodeStrictSkillYAML(data []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

// LintManifest validates a parsed skill and returns deterministic findings.
func LintManifest(skillKey string, manifest Manifest, requireBody bool) LintReport {
	var findings []LintFinding
	add := func(severity LintSeverity, code, field, message string) {
		findings = append(findings, LintFinding{Severity: severity, Code: code, Field: field, Message: message})
	}
	skillKey = strings.TrimSpace(skillKey)
	if !skillKeyPattern.MatchString(skillKey) {
		add(LintError, "invalid-skill-key", "skillKey", "skill key must be lowercase alphanumeric with '.', '_', or '-' separators")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		add(LintError, "missing-name", "name", "skill name is required")
	}
	if strings.TrimSpace(manifest.Description) == "" {
		add(LintError, "missing-description", "description", "skill description is required for discovery")
	}
	if requireBody && strings.TrimSpace(manifest.Body) == "" {
		add(LintError, "missing-body", "body", "SKILL.md must include instruction content")
	}
	if manifest.UserInvocable != nil && !*manifest.UserInvocable && manifest.DisableModelInvocation != nil && *manifest.DisableModelInvocation {
		add(LintWarning, "unreachable-skill", "user-invocable", "skill is disabled for both user and model invocation")
	}

	req := manifest.Requirements
	if manifest.Metadata != nil && manifest.Metadata.OpenClaw != nil {
		meta := manifest.Metadata.OpenClaw
		if meta.Requires != nil {
			req = Requirements{Bins: meta.Requires.Bins, AnyBins: meta.Requires.AnyBins, Env: meta.Requires.Env, OS: append(append([]string{}, meta.OS...), meta.Requires.OS...), Config: meta.Requires.Config}
		}
		for i, spec := range meta.Install {
			field := fmt.Sprintf("metadata.openclaw.install[%d]", i)
			kind := strings.ToLower(strings.TrimSpace(spec.Kind))
			switch kind {
			case "brew":
				if strings.TrimSpace(spec.Formula) == "" {
					add(LintError, "invalid-install-spec", field+".formula", "brew install requires formula")
				}
			case "npm", "apt", "uv":
				if strings.TrimSpace(spec.Package) == "" {
					add(LintError, "invalid-install-spec", field+".package", kind+" install requires package")
				}
			case "go":
				if strings.TrimSpace(spec.Module) == "" {
					add(LintError, "invalid-install-spec", field+".module", "go install requires module")
				}
			case "download":
				u, err := url.Parse(strings.TrimSpace(spec.URL))
				if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
					add(LintError, "unsafe-download-url", field+".url", "download install requires an absolute HTTPS URL without credentials")
				}
			default:
				add(LintError, "unsupported-installer", field+".kind", "installer kind must be brew, npm, apt, uv, go, or download")
			}
		}
	}
	for i, step := range manifest.Install {
		if strings.TrimSpace(step.Cmd) != "" {
			add(LintWarning, "legacy-shell-install", fmt.Sprintf("install[%d].cmd", i), "raw shell installers require explicit review; prefer structured install metadata")
		}
	}

	lintUniqueList := func(field string, values []string, validate func(string) bool) {
		seen := map[string]struct{}{}
		for i, raw := range values {
			value := strings.TrimSpace(raw)
			itemField := fmt.Sprintf("%s[%d]", field, i)
			if value == "" || (validate != nil && !validate(value)) {
				add(LintError, "invalid-requirement", itemField, "requirement value is invalid")
				continue
			}
			key := strings.ToLower(value)
			if _, ok := seen[key]; ok {
				add(LintWarning, "duplicate-requirement", itemField, "duplicate requirement")
				continue
			}
			seen[key] = struct{}{}
		}
	}
	lintUniqueList("requires.bins", req.Bins, func(v string) bool { return !strings.ContainsAny(v, " 	/\\") })
	lintUniqueList("requires.anyBins", req.AnyBins, func(v string) bool { return !strings.ContainsAny(v, " 	/\\") })
	lintUniqueList("requires.env", req.Env, envNamePattern.MatchString)
	lintUniqueList("requires.os", req.OS, func(v string) bool {
		switch strings.ToLower(v) {
		case "darwin", "linux", "windows", "freebsd":
			return true
		default:
			return false
		}
	})
	lintUniqueList("requires.config", req.Config, func(v string) bool {
		return !strings.ContainsAny(v, " 	") && !strings.Contains(v, "..") && !strings.Contains(v, "__proto__")
	})

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity < findings[j].Severity
		}
		if findings[i].Field != findings[j].Field {
			return findings[i].Field < findings[j].Field
		}
		return findings[i].Code < findings[j].Code
	})
	return LintReport{Findings: findings}
}
