package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrator_DryRun(t *testing.T) {
	// Create a mock OpenClaw directory
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create openclaw.json
	cfg := map[string]any{
		"$schema": "https://openclaw.ai/schema/config.json",
		"auth": map[string]any{
			"nostrPrivateKey": "nsec1test...",
		},
		"models": map[string]any{
			"default": "claude-opus-4-5",
			"providers": map[string]any{
				"anthropic": map[string]any{
					"enabled": true,
					"apiKey":  "${ANTHROPIC_API_KEY}",
				},
			},
		},
		"agents": map[string]any{
			"defaults": map[string]any{
				"model":     "claude-opus-4-5",
				"workspace": "~/.openclaw/workspace",
			},
			"list": []map[string]any{
				{
					"id":        "main",
					"name":      "Test Agent",
					"model":     "claude-opus-4-5",
					"workspace": "~/.openclaw/workspace",
				},
			},
		},
		"cron": map[string]any{
			"enabled": true,
		},
	}

	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Create workspace with memory file
	workspaceDir := filepath.Join(srcDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatal(err)
	}

	memoryContent := `# Agent Memory

## Important Notes
- Path to config: ~/.openclaw/openclaw.json
- Workspace: /Users/test/.openclaw/workspace
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "MEMORY.md"), []byte(memoryContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run dry-run migration
	opts := Options{
		SourceDir: srcDir,
		TargetDir: dstDir,
		DryRun:    true,
	}

	m := New(opts)
	report, err := m.Run()
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}

	if !report.Success {
		t.Error("expected success=true for dry-run")
	}

	if report.Phase != PhaseDryRun {
		t.Errorf("expected phase=dry-run, got %s", report.Phase)
	}

	// Verify artifacts were identified
	if len(report.Artifacts) == 0 {
		t.Error("expected artifacts to be identified")
	}

	// Check that config conversion artifact exists
	hasConfigArtifact := false
	for _, art := range report.Artifacts {
		if art.Type == ArtifactConfig && art.Action == ActionConvert {
			hasConfigArtifact = true
			break
		}
	}
	if !hasConfigArtifact {
		t.Error("expected config artifact with convert action")
	}

	// In dry-run mode, target should not have config.json created
	if _, err := os.Stat(filepath.Join(dstDir, "config.json")); err == nil {
		t.Error("config.json should not exist in dry-run mode")
	}
}

func TestMigrator_Apply(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create openclaw.json
	cfg := map[string]any{
		"auth": map[string]any{
			"nostrPrivateKey": "nsec1abc123",
		},
		"models": map[string]any{
			"default": "claude-sonnet-4-5",
			"providers": map[string]any{
				"anthropic": map[string]any{
					"enabled": true,
					"apiKey":  "${ANTHROPIC_API_KEY}",
				},
			},
		},
		"agents": map[string]any{
			"list": []map[string]any{
				{"id": "main", "name": "My Agent"},
			},
		},
	}

	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Create workspace
	workspaceDir := filepath.Join(srcDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create memory file
	memoryContent := "# Memory\n\nTest content referencing ~/.openclaw/config"
	if err := os.WriteFile(filepath.Join(workspaceDir, "MEMORY.md"), []byte(memoryContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run apply migration
	opts := Options{
		SourceDir: srcDir,
		TargetDir: dstDir,
		DryRun:    false,
	}

	m := New(opts)
	report, err := m.Run()
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if !report.Success {
		t.Errorf("expected success=true, got issues: %+v", report.Issues)
	}

	// Verify config.json was created
	configPath := filepath.Join(dstDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Error("config.json should exist after apply")
	}

	// Verify bootstrap.json was created
	bootstrapPath := filepath.Join(dstDir, "bootstrap.json")
	if _, err := os.Stat(bootstrapPath); err != nil {
		t.Error("bootstrap.json should exist after apply")
	}

	// Verify bootstrap contains private key
	bootstrapData, _ := os.ReadFile(bootstrapPath)
	var bootstrap map[string]any
	if err := json.Unmarshal(bootstrapData, &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap["private_key"] != "nsec1abc123" {
		t.Error("bootstrap should contain private_key")
	}

	// Verify memory file was transformed
	memoryPath := filepath.Join(dstDir, "workspace", "MEMORY.md")
	memoryData, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("failed to read transformed memory file: %v", err)
	}

	memoryStr := string(memoryData)
	// MEMORY.md is entrypoint - should have migration comment, not YAML frontmatter
	if strings.HasPrefix(memoryStr, "---\n") {
		t.Error("MEMORY.md entrypoint should not have YAML frontmatter")
	}
	if !strings.Contains(memoryStr, "migrated_from: openclaw") {
		t.Error("memory file should contain migration metadata in HTML comment")
	}
	if !strings.Contains(memoryStr, "~/.metiq/config") {
		t.Error("memory file paths should be normalized from .openclaw to .metiq")
	}
}

func TestMigrator_RuntimeGarbageOmitted(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create openclaw.json
	cfg := map[string]any{
		"models": map[string]any{"default": "gpt-4"},
	}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Create runtime garbage directories
	for _, garbage := range []string{"delivery-queue", "quarantine", "sessions", "tasks"} {
		garbageDir := filepath.Join(srcDir, garbage)
		if err := os.MkdirAll(garbageDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Add a file to each
		if err := os.WriteFile(filepath.Join(garbageDir, "test.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Run migration
	opts := Options{
		SourceDir: srcDir,
		TargetDir: dstDir,
		DryRun:    false,
	}

	m := New(opts)
	report, _ := m.Run()

	// Verify garbage paths are in OmittedPaths
	if len(report.OmittedPaths) < 4 {
		t.Errorf("expected at least 4 omitted paths, got %d", len(report.OmittedPaths))
	}

	// Verify garbage directories don't exist in target
	for _, garbage := range []string{"delivery-queue", "quarantine", "sessions", "tasks"} {
		garbagePath := filepath.Join(dstDir, garbage)
		if _, err := os.Stat(garbagePath); err == nil {
			t.Errorf("runtime garbage %s should not be copied to target", garbage)
		}
	}
}

func TestMigrator_CronConversion(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create openclaw.json
	cfg := map[string]any{"cron": map[string]any{"enabled": true}}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Create cron/jobs.json
	cronDir := filepath.Join(srcDir, "cron")
	if err := os.MkdirAll(cronDir, 0755); err != nil {
		t.Fatal(err)
	}

	jobs := []map[string]any{
		{
			"id":       "daily-summary",
			"name":     "Daily Summary",
			"schedule": "0 9 * * *",
			"prompt":   "Summarize today's events",
			"enabled":  true,
		},
		{
			"jobId":    "legacy-job",
			"schedule": "*/30 * * * *",
			"command":  "~/.openclaw/scripts/check.sh",
			"enabled":  true,
		},
	}
	jobsData, _ := json.MarshalIndent(jobs, "", "  ")
	if err := os.WriteFile(filepath.Join(cronDir, "jobs.json"), jobsData, 0644); err != nil {
		t.Fatal(err)
	}

	// Run migration
	opts := Options{
		SourceDir: srcDir,
		TargetDir: dstDir,
		DryRun:    false,
	}

	m := New(opts)
	report, err := m.Run()
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify jobs.json was created
	jobsPath := filepath.Join(dstDir, "cron", "jobs.json")
	jobsData, err = os.ReadFile(jobsPath)
	if err != nil {
		t.Fatalf("failed to read converted jobs.json: %v", err)
	}

	var convertedJobs []map[string]any
	if err := json.Unmarshal(jobsData, &convertedJobs); err != nil {
		t.Fatalf("failed to parse converted jobs.json: %v", err)
	}

	if len(convertedJobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(convertedJobs))
	}

	// Check that legacy job with openclaw path generates a warning
	hasPathWarning := false
	for _, issue := range report.Issues {
		if issue.ManualReview && strings.Contains(issue.Message, "openclaw paths") {
			hasPathWarning = true
			break
		}
	}
	if !hasPathWarning {
		t.Error("expected warning for job with openclaw path in command")
	}
}

func TestTransformMemoryFile_Entrypoint(t *testing.T) {
	m := &Migrator{
		opts: Options{
			SourceDir: "/home/user/.openclaw",
			TargetDir: "/home/user/.metiq",
		},
		report: Report{
			SourceAgent: "TestBot",
		},
	}

	input := `# Agent Memory

## Configuration
The config is at ~/.openclaw/openclaw.json
Absolute path: /Users/alice/.openclaw/workspace/notes.md

## Tasks
- Check ${HOME}/.openclaw/.env for secrets
`

	result := m.transformMemoryFile(input, "/home/user/.openclaw/workspace/MEMORY.md")

	// MEMORY.md entrypoint should NOT have YAML frontmatter (just HTML comment)
	if strings.HasPrefix(result, "---\n") {
		t.Error("MEMORY.md entrypoint should not have YAML frontmatter")
	}

	// Should have migration metadata as HTML comment
	if !strings.Contains(result, "<!--") || !strings.Contains(result, "migrated_from: openclaw") {
		t.Error("should contain migration metadata as HTML comment")
	}

	// Paths should be normalized
	if strings.Contains(result, "~/.openclaw/") {
		t.Error("should normalize ~/.openclaw/ to ~/.metiq/")
	}

	if !strings.Contains(result, "~/.metiq/config.json") {
		t.Error("should contain normalized path ~/.metiq/config.json")
	}
}

func TestTransformMemoryFile_TopicFile(t *testing.T) {
	m := &Migrator{
		opts: Options{
			SourceDir: "/home/user/.openclaw",
			TargetDir: "/home/user/.metiq",
		},
		report: Report{
			SourceAgent: "TestBot",
		},
	}

	// Topic file without existing frontmatter
	input := `# Project Notes

Some notes about the project.
`

	result := m.transformMemoryFile(input, "/home/user/.openclaw/workspace/memory/project-notes.md")

	// Topic files SHOULD have YAML frontmatter for runtime compatibility
	if !strings.HasPrefix(result, "---\n") {
		t.Error("topic file should have YAML frontmatter")
	}

	// Should have required fields for runtime
	if !strings.Contains(result, "name:") {
		t.Error("topic frontmatter should have name field")
	}
	if !strings.Contains(result, "description:") {
		t.Error("topic frontmatter should have description field")
	}
	if !strings.Contains(result, "type: user") {
		t.Error("topic frontmatter should have type field")
	}

	// Should also have migration comment
	if !strings.Contains(result, "<!--") || !strings.Contains(result, "migrated_from: openclaw") {
		t.Error("should contain migration metadata as HTML comment")
	}
}

func TestTransformMemoryFile_PreservesExistingFrontmatter(t *testing.T) {
	m := &Migrator{
		opts: Options{
			SourceDir: "/home/user/.openclaw",
			TargetDir: "/home/user/.metiq",
		},
		report: Report{
			SourceAgent: "TestBot",
		},
	}

	// Topic file WITH existing valid frontmatter
	input := `---
name: "My Preferences"
description: "User preferences and settings"
type: user
---

I prefer dark mode.
Config at ~/.openclaw/openclaw.json
`

	result := m.transformMemoryFile(input, "/home/user/.openclaw/workspace/memory/preferences.md")

	// Should preserve existing frontmatter
	if !strings.Contains(result, `name: "My Preferences"`) {
		t.Error("should preserve existing name field")
	}
	if !strings.Contains(result, `type: user`) {
		t.Error("should preserve existing type field")
	}

	// Should have migration comment after frontmatter
	if !strings.Contains(result, "migrated_from: openclaw") {
		t.Error("should contain migration metadata")
	}

	// Paths should still be normalized in body
	if strings.Contains(result, "~/.openclaw/") {
		t.Error("should normalize paths in body")
	}
	if !strings.Contains(result, "~/.metiq/config.json") {
		t.Error("should contain normalized path")
	}
}

func TestNormalizePathsInContent(t *testing.T) {
	m := &Migrator{}

	tests := []struct {
		input    string
		expected string
	}{
		{"~/.openclaw/config", "~/.metiq/config"},
		{"$HOME/.openclaw/workspace", "$HOME/.metiq/workspace"},
		{"/Users/alice/.openclaw/memory", "/Users/alice/.metiq/memory"},
		{"/home/bob/.openclaw/scripts", "/home/bob/.metiq/scripts"},
		{"openclaw.json", "config.json"},
		{"no paths here", "no paths here"},
	}

	for _, tc := range tests {
		result := m.normalizePathsInContent(tc.input)
		if result != tc.expected {
			t.Errorf("normalizePathsInContent(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestMigrator_AuditMemoryDatabases(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create mock OpenClaw structure with SQLite memory DB
	agentDir := filepath.Join(srcDir, "agents", "main", "memory")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a fake SQLite file
	sqlitePath := filepath.Join(agentDir, "main.sqlite")
	if err := os.WriteFile(sqlitePath, []byte("fake sqlite"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create minimal openclaw.json
	cfg := map[string]any{"models": map[string]any{"default": "gpt-4"}}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Test WITHOUT --migrate-memory-db flag
	opts := Options{
		SourceDir:       srcDir,
		TargetDir:       dstDir,
		DryRun:          true,
		MigrateMemoryDB: false,
	}

	m := New(opts)
	report, _ := m.Run()

	// Should have a warning about SQLite DB
	hasWarning := false
	for _, issue := range report.Issues {
		if strings.Contains(issue.Message, "SQLite memory database found") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Error("expected warning about SQLite memory database")
	}

	// Test WITH --migrate-memory-db flag
	opts.MigrateMemoryDB = true
	m = New(opts)
	report, _ = m.Run()

	// Should have a MemoryDB artifact
	hasMemoryDBArtifact := false
	for _, art := range report.Artifacts {
		if art.Type == ArtifactMemoryDB {
			hasMemoryDBArtifact = true
			break
		}
	}
	if !hasMemoryDBArtifact {
		t.Error("expected MemoryDB artifact when --migrate-memory-db is set")
	}
}

func TestMigrator_AuditAuthProfiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create mock auth-profiles.json
	agentDir := filepath.Join(srcDir, "agents", "main", "agent")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	authProfiles := map[string]any{
		"anthropic": map[string]any{
			"type":   "api-key",
			"apiKey": "sk-test-123",
		},
		"openai": map[string]any{
			"type":    "api-key",
			"apiKey":  "sk-openai-456",
			"baseUrl": "https://api.openai.com/v1",
		},
	}
	authData, _ := json.MarshalIndent(authProfiles, "", "  ")
	if err := os.WriteFile(filepath.Join(agentDir, "auth-profiles.json"), authData, 0644); err != nil {
		t.Fatal(err)
	}

	// Create minimal openclaw.json
	cfg := map[string]any{"models": map[string]any{"default": "gpt-4"}}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Test WITH --migrate-auth flag
	opts := Options{
		SourceDir:   srcDir,
		TargetDir:   dstDir,
		DryRun:      false,
		MigrateAuth: true,
	}

	m := New(opts)
	report, err := m.Run()
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify auth-profiles.json was created
	authPath := filepath.Join(dstDir, "auth-profiles.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("auth-profiles.json not created: %v", err)
	}

	var converted map[string]any
	if err := json.Unmarshal(data, &converted); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Check field normalization (apiKey → api_key)
	if anthropic, ok := converted["anthropic"].(map[string]any); ok {
		if _, hasOldKey := anthropic["apiKey"]; hasOldKey {
			t.Error("should have normalized apiKey to api_key")
		}
		if _, hasNewKey := anthropic["api_key"]; !hasNewKey {
			t.Error("should have api_key field")
		}
	}

	// Verify file permissions (should be 0600 for secrets)
	info, _ := os.Stat(authPath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("auth-profiles.json should have 0600 permissions, got %o", info.Mode().Perm())
	}

	_ = report // silence unused
}

func TestMigrator_AuditPluginsAndSkills(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create plugins directory
	pluginsDir := filepath.Join(srcDir, "plugins", "test-plugin")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "manifest.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create skills directory
	skillsDir := filepath.Join(srcDir, "skills", "test-skill")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("# Test Skill"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create hooks directory
	hooksDir := filepath.Join(srcDir, "hooks", "test-hook")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create minimal openclaw.json
	cfg := map[string]any{"models": map[string]any{"default": "gpt-4"}}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, "openclaw.json"), cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Test with all migration flags
	opts := Options{
		SourceDir:      srcDir,
		TargetDir:      dstDir,
		DryRun:         false,
		MigratePlugins: true,
		MigrateSkills:  true,
	}

	m := New(opts)
	report, err := m.Run()
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Check for plugins artifact
	hasPlugins := false
	hasSkills := false
	hasHooks := false
	for _, art := range report.Artifacts {
		switch art.Type {
		case ArtifactPlugins:
			hasPlugins = true
		case ArtifactSkills:
			hasSkills = true
		case ArtifactHooks:
			hasHooks = true
		}
	}

	if !hasPlugins {
		t.Error("expected plugins artifact")
	}
	if !hasSkills {
		t.Error("expected skills artifact")
	}
	if !hasHooks {
		t.Error("expected hooks artifact")
	}

	// Verify directories were copied
	if _, err := os.Stat(filepath.Join(dstDir, "plugins")); err != nil {
		t.Error("plugins directory should be copied")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "skills")); err != nil {
		t.Error("skills directory should be copied")
	}
}
