package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintSkillFileValid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secure-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `---
name: Secure Skill
description: Safely does one thing.
metadata:
  openclaw:
    requires:
      bins: [git]
      env: [API_TOKEN]
      os: [darwin, linux]
    install:
      - id: brew
        kind: brew
        formula: git
---
Use the git binary only for the requested repository.
`
	file := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := LintSkillFile(file)
	if err != nil {
		t.Fatalf("LintSkillFile: %v", err)
	}
	if !report.Valid() || len(report.Findings) != 0 {
		t.Fatalf("valid skill findings: %+v", report.Findings)
	}
}

func TestLintSkillFileReportsUnsafeAndInvalidMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Bad Skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `---
name: ""
description: ""
metadata:
  openclaw:
    requires:
      env: [BAD-NAME, BAD-NAME]
      config: [__proto__.polluted]
    install:
      - kind: download
        url: http://example.com/tool
---
`
	file := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := LintSkillFile(file)
	if err != nil {
		t.Fatalf("LintSkillFile: %v", err)
	}
	if report.Valid() {
		t.Fatalf("invalid skill passed: %+v", report)
	}
	var codes []string
	for _, finding := range report.Findings {
		codes = append(codes, finding.Code)
	}
	joined := strings.Join(codes, ",")
	for _, want := range []string{"invalid-skill-key", "missing-name", "missing-description", "missing-body", "invalid-requirement", "unsafe-download-url"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in findings: %+v", want, report.Findings)
		}
	}
}

func TestLintPathsRecursesAndReturnsParseErrorsAsFindings(t *testing.T) {
	root := t.TempDir()
	validDir := filepath.Join(root, "valid")
	invalidDir := filepath.Join(root, "invalid")
	for _, dir := range []string{validDir, invalidDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(validDir, "SKILL.md"), []byte("---\nname: Valid\ndescription: valid skill\n---\nDo one thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidDir, "SKILL.md"), []byte("---\nname: [broken\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := LintPaths([]string{root})
	if result.SchemaVersion != LintSchemaVersion {
		t.Fatalf("schema version = %q", result.SchemaVersion)
	}
	if result.Valid || result.ErrorCount != 1 || len(result.Reports) != 2 {
		t.Fatalf("unexpected lint result: %+v", result)
	}
	foundParseError := false
	for _, report := range result.Reports {
		for _, finding := range report.Findings {
			foundParseError = foundParseError || finding.Code == "parse-error"
		}
	}
	if !foundParseError {
		t.Fatalf("expected parse-error finding: %+v", result.Reports)
	}
}

func TestLintSkillFileStrictUnknownFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "strict")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(file, []byte("---\nname: Strict\ndescription: test\nunknownField: true\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LintSkillFile(file); err == nil || !strings.Contains(err.Error(), "unknownField") {
		t.Fatalf("expected strict unknown field error, got %v", err)
	}
}
