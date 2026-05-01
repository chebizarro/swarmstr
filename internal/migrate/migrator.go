package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Migrator orchestrates OpenClaw → Metiq migration.
type Migrator struct {
	opts   Options
	report Report
	start  time.Time
}

// New creates a new Migrator with the given options.
func New(opts Options) *Migrator {
	return &Migrator{
		opts: opts,
		report: Report{
			SourceDir:     opts.SourceDir,
			TargetDir:     opts.TargetDir,
			MigrationDate: time.Now().UTC(),
			Artifacts:     []ArtifactEntry{},
			Issues:        []Issue{},
			OmittedPaths:  []string{},
		},
	}
}

// Run executes the full migration workflow.
func (m *Migrator) Run() (*Report, error) {
	m.start = time.Now()

	// Phase 1: Audit
	m.report.Phase = PhaseAudit
	if err := m.audit(); err != nil {
		return m.finalize(false), fmt.Errorf("audit failed: %w", err)
	}

	// Phase 2: Dry-run (always performed)
	m.report.Phase = PhaseDryRun
	if err := m.dryRun(); err != nil {
		return m.finalize(false), fmt.Errorf("dry-run failed: %w", err)
	}

	if m.opts.DryRun {
		return m.finalize(true), nil
	}

	// Phase 3: Apply
	m.report.Phase = PhaseApply
	if err := m.apply(); err != nil {
		return m.finalize(false), fmt.Errorf("apply failed: %w", err)
	}

	// Phase 4: Verify
	m.report.Phase = PhaseVerify
	if err := m.verify(); err != nil {
		return m.finalize(false), fmt.Errorf("verify failed: %w", err)
	}

	return m.finalize(true), nil
}

func (m *Migrator) finalize(success bool) *Report {
	m.report.Success = success
	m.report.DurationMs = time.Since(m.start).Milliseconds()
	return &m.report
}

// ─── Audit Phase ─────────────────────────────────────────────────────────────

func (m *Migrator) audit() error {
	src := m.opts.SourceDir

	// Check source directory exists
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source directory not found: %s", src)
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}

	// Check for openclaw.json (optional — memory-only dirs may lack it)
	configPath := filepath.Join(src, "openclaw.json")
	hasConfig := false
	if _, err := os.Stat(configPath); err != nil {
		m.addIssue(Issue{
			Severity:   SeverityWarning,
			Phase:      PhaseAudit,
			Path:       configPath,
			Message:    "openclaw.json not found — running in memory-only mode",
			Suggestion: "Config conversion and identity migration will be skipped",
		})
	} else {
		hasConfig = true
		// Parse config to extract agent name
		cfg, err := m.loadOpenClawConfig(configPath)
		if err != nil {
			m.addIssue(Issue{
				Severity: SeverityWarning,
				Phase:    PhaseAudit,
				Path:     configPath,
				Message:  fmt.Sprintf("failed to parse openclaw.json: %v", err),
			})
		} else {
			m.report.SourceAgent = m.extractAgentName(cfg)
		}
	}

	// Check for workspace (try both <src>/workspace and <src> directly)
	workspacePath := filepath.Join(src, "workspace")
	if info, err := os.Stat(workspacePath); err == nil && info.IsDir() {
		m.addArtifact(ArtifactEntry{
			Type:        ArtifactWorkspace,
			Action:      ActionMigrate,
			SourcePath:  workspacePath,
			TargetPath:  filepath.Join(m.opts.TargetDir, "workspace"),
			Description: "Agent workspace directory",
		})

		// Check for memory files
		m.auditMemoryFiles(workspacePath)
	} else if m.hasMemoryContent(src) {
		// Source dir itself contains memory content (MEMORY.md or memory/ subdir)
		// Treat src as the workspace directly
		m.addIssue(Issue{
			Severity: SeverityInfo,
			Phase:    PhaseAudit,
			Path:     src,
			Message:  "no workspace/ subdirectory found, treating source as workspace",
		})
		m.auditMemoryFiles(src)
	} else {
		m.addIssue(Issue{
			Severity: SeverityWarning,
			Phase:    PhaseAudit,
			Path:     workspacePath,
			Message:  "workspace directory not found",
		})
	}

	// Check for jobs.json (cron)
	jobsPath := filepath.Join(src, "cron", "jobs.json")
	if _, err := os.Stat(jobsPath); err == nil {
		m.addArtifact(ArtifactEntry{
			Type:        ArtifactCron,
			Action:      ActionConvert,
			SourcePath:  jobsPath,
			TargetPath:  filepath.Join(m.opts.TargetDir, "cron", "jobs.json"),
			Description: "Cron job definitions",
		})
	}

	// Check for secrets (.env)
	envPath := filepath.Join(src, ".env")
	if _, err := os.Stat(envPath); err == nil {
		if m.opts.SkipSecrets {
			m.addIssue(Issue{
				Severity: SeverityInfo,
				Phase:    PhaseAudit,
				Path:     envPath,
				Message:  ".env file found but secrets migration is disabled",
			})
		} else {
			m.addArtifact(ArtifactEntry{
				Type:        ArtifactSecrets,
				Action:      ActionMigrate,
				SourcePath:  envPath,
				TargetPath:  filepath.Join(m.opts.TargetDir, ".env"),
				Description: "Environment secrets",
			})
		}
	}

	// Identify runtime garbage to omit
	m.auditRuntimeGarbage()

	// Check for SQLite memory databases
	m.auditMemoryDatabases()

	// Check for auth profiles
	m.auditAuthProfiles()

	// Check for plugins and hooks
	m.auditPluginsAndHooks()

	// Check for managed skills (outside workspace)
	m.auditManagedSkills()

	// Check for OAuth credentials
	m.auditCredentials()

	// Add config artifacts (only when openclaw.json exists)
	if hasConfig {
		m.addArtifact(ArtifactEntry{
			Type:        ArtifactConfig,
			Action:      ActionConvert,
			SourcePath:  configPath,
			TargetPath:  filepath.Join(m.opts.TargetDir, "config.json"),
			Description: "Main configuration (converted)",
		})

		m.addArtifact(ArtifactEntry{
			Type:        ArtifactIdentity,
			Action:      ActionConvert,
			SourcePath:  configPath,
			TargetPath:  filepath.Join(m.opts.TargetDir, "bootstrap.json"),
			Description: "Identity/bootstrap configuration",
		})
	}

	return nil
}

func (m *Migrator) auditMemoryFiles(workspacePath string) {
	memoryMD := filepath.Join(workspacePath, "MEMORY.md")
	if _, err := os.Stat(memoryMD); err == nil {
		m.addArtifact(ArtifactEntry{
			Type:        ArtifactMemory,
			Action:      ActionTransform,
			SourcePath:  memoryMD,
			TargetPath:  filepath.Join(m.opts.TargetDir, "workspace", "MEMORY.md"),
			Description: "Main memory file (with front-matter injection)",
		})
	}

	memoryDir := filepath.Join(workspacePath, "memory")
	if info, err := os.Stat(memoryDir); err == nil && info.IsDir() {
		_ = filepath.Walk(memoryDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".md") {
				relPath, _ := filepath.Rel(workspacePath, path)
				m.addArtifact(ArtifactEntry{
					Type:        ArtifactMemory,
					Action:      ActionTransform,
					SourcePath:  path,
					TargetPath:  filepath.Join(m.opts.TargetDir, "workspace", relPath),
					Description: fmt.Sprintf("Memory file: %s", relPath),
				})
			}
			return nil
		})
	}
}

// hasMemoryContent checks whether a directory contains MEMORY.md or a memory/ subdirectory.
func (m *Migrator) hasMemoryContent(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "MEMORY.md")); err == nil {
		return true
	}
	if info, err := os.Stat(filepath.Join(dir, "memory")); err == nil && info.IsDir() {
		return true
	}
	return false
}

func (m *Migrator) auditRuntimeGarbage() {
	for _, garbage := range RuntimeGarbage {
		garbagePath := filepath.Join(m.opts.SourceDir, garbage)
		if info, err := os.Stat(garbagePath); err == nil && info.IsDir() {
			m.report.OmittedPaths = append(m.report.OmittedPaths, garbage)
			m.addArtifact(ArtifactEntry{
				Type:        ArtifactRuntimeState,
				Action:      ActionOmit,
				SourcePath:  garbagePath,
				Description: fmt.Sprintf("Runtime state (discarded): %s", garbage),
			})
		}
	}
}

func (m *Migrator) auditMemoryDatabases() {
	// Look for SQLite memory databases in agents/<id>/memory/<id>.sqlite
	agentsDir := filepath.Join(m.opts.SourceDir, "agents")
	if info, err := os.Stat(agentsDir); err != nil || !info.IsDir() {
		return
	}

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentID := entry.Name()

		// Check for memory directory with SQLite databases
		memoryDir := filepath.Join(agentsDir, agentID, "memory")
		if info, err := os.Stat(memoryDir); err != nil || !info.IsDir() {
			continue
		}

		// Look for .sqlite files
		sqliteFiles, _ := filepath.Glob(filepath.Join(memoryDir, "*.sqlite"))
		for _, sqlitePath := range sqliteFiles {
			if m.opts.MigrateMemoryDB {
				m.addArtifact(ArtifactEntry{
					Type:        ArtifactMemoryDB,
					Action:      ActionConvert,
					SourcePath:  sqlitePath,
					TargetPath:  filepath.Join(m.opts.TargetDir, "memory.sqlite"),
					Description: fmt.Sprintf("SQLite memory database (agent: %s)", agentID),
				})
			} else {
				m.addIssue(Issue{
					Severity:     SeverityWarning,
					Phase:        PhaseAudit,
					Path:         sqlitePath,
					Message:      fmt.Sprintf("SQLite memory database found for agent '%s' but --migrate-memory-db not set", agentID),
					Suggestion:   "Use --migrate-memory-db to migrate learned memories",
					ManualReview: true,
				})
			}
		}
	}
}

func (m *Migrator) auditAuthProfiles() {
	// Look for auth-profiles.json in agents/<id>/agent/auth-profiles.json
	agentsDir := filepath.Join(m.opts.SourceDir, "agents")
	if info, err := os.Stat(agentsDir); err != nil || !info.IsDir() {
		return
	}

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentID := entry.Name()

		authPath := filepath.Join(agentsDir, agentID, "agent", "auth-profiles.json")
		if _, err := os.Stat(authPath); err == nil {
			if m.opts.MigrateAuth {
				m.addArtifact(ArtifactEntry{
					Type:        ArtifactAuthProfiles,
					Action:      ActionConvert,
					SourcePath:  authPath,
					TargetPath:  filepath.Join(m.opts.TargetDir, "auth-profiles.json"),
					Description: fmt.Sprintf("Model auth profiles (agent: %s)", agentID),
				})
			} else {
				m.addIssue(Issue{
					Severity:     SeverityWarning,
					Phase:        PhaseAudit,
					Path:         authPath,
					Message:      fmt.Sprintf("Auth profiles found for agent '%s' but --migrate-auth not set", agentID),
					Suggestion:   "Use --migrate-auth to migrate API keys and OAuth tokens",
					ManualReview: true,
				})
			}
		}
	}
}

func (m *Migrator) auditPluginsAndHooks() {
	// Check for plugins directory
	pluginsDir := filepath.Join(m.opts.SourceDir, "plugins")
	if info, err := os.Stat(pluginsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(pluginsDir)
		if len(entries) > 0 {
			if m.opts.MigratePlugins {
				m.addArtifact(ArtifactEntry{
					Type:        ArtifactPlugins,
					Action:      ActionMigrate,
					SourcePath:  pluginsDir,
					TargetPath:  filepath.Join(m.opts.TargetDir, "plugins"),
					Description: fmt.Sprintf("Installed plugins (%d)", len(entries)),
				})
			} else {
				m.addIssue(Issue{
					Severity:     SeverityWarning,
					Phase:        PhaseAudit,
					Path:         pluginsDir,
					Message:      fmt.Sprintf("%d plugins found but --migrate-plugins not set", len(entries)),
					Suggestion:   "Use --migrate-plugins to migrate installed plugins",
					ManualReview: true,
				})
			}
		}
	}

	// Check for hooks directory
	hooksDir := filepath.Join(m.opts.SourceDir, "hooks")
	if info, err := os.Stat(hooksDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(hooksDir)
		if len(entries) > 0 {
			if m.opts.MigratePlugins { // Hooks are migrated with plugins flag
				m.addArtifact(ArtifactEntry{
					Type:        ArtifactHooks,
					Action:      ActionMigrate,
					SourcePath:  hooksDir,
					TargetPath:  filepath.Join(m.opts.TargetDir, "hooks"),
					Description: fmt.Sprintf("Custom hooks (%d)", len(entries)),
				})
			} else {
				m.addIssue(Issue{
					Severity:     SeverityWarning,
					Phase:        PhaseAudit,
					Path:         hooksDir,
					Message:      fmt.Sprintf("%d hooks found but --migrate-plugins not set", len(entries)),
					Suggestion:   "Use --migrate-plugins to migrate custom hooks",
					ManualReview: true,
				})
			}
		}
	}
}

func (m *Migrator) auditManagedSkills() {
	// Check for managed skills directory (outside workspace)
	skillsDir := filepath.Join(m.opts.SourceDir, "skills")
	if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(skillsDir)
		if len(entries) > 0 {
			if m.opts.MigrateSkills {
				m.addArtifact(ArtifactEntry{
					Type:        ArtifactSkills,
					Action:      ActionMigrate,
					SourcePath:  skillsDir,
					TargetPath:  filepath.Join(m.opts.TargetDir, "skills"),
					Description: fmt.Sprintf("Managed skills (%d)", len(entries)),
				})
			} else {
				m.addIssue(Issue{
					Severity:     SeverityInfo,
					Phase:        PhaseAudit,
					Path:         skillsDir,
					Message:      fmt.Sprintf("%d managed skills found but --migrate-skills not set", len(entries)),
					Suggestion:   "Use --migrate-skills to migrate managed skills",
					ManualReview: false,
				})
			}
		}
	}
}

func (m *Migrator) auditCredentials() {
	// Check for OAuth credentials directory
	credsDir := filepath.Join(m.opts.SourceDir, "credentials")
	if info, err := os.Stat(credsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(credsDir)
		if len(entries) > 0 {
			// Always warn about credentials - they require manual review
			m.addIssue(Issue{
				Severity:     SeverityWarning,
				Phase:        PhaseAudit,
				Path:         credsDir,
				Message:      fmt.Sprintf("OAuth credentials directory found with %d entries", len(entries)),
				Suggestion:   "OAuth tokens may need re-authorization after migration. Review manually.",
				ManualReview: true,
			})

			// Copy if not skipping secrets
			if !m.opts.SkipSecrets {
				m.addArtifact(ArtifactEntry{
					Type:        ArtifactCredentials,
					Action:      ActionMigrate,
					SourcePath:  credsDir,
					TargetPath:  filepath.Join(m.opts.TargetDir, "credentials"),
					Description: fmt.Sprintf("OAuth credentials (%d entries)", len(entries)),
				})
			}
		}
	}
}

// ─── Dry-Run Phase ───────────────────────────────────────────────────────────

func (m *Migrator) dryRun() error {
	// Check target directory
	if _, err := os.Stat(m.opts.TargetDir); err == nil {
		if !m.opts.Force {
			// Check if target has existing files
			entries, _ := os.ReadDir(m.opts.TargetDir)
			if len(entries) > 0 {
				m.addIssue(Issue{
					Severity:   SeverityWarning,
					Phase:      PhaseDryRun,
					Path:       m.opts.TargetDir,
					Message:    "target directory exists and is not empty",
					Suggestion: "Use --force to overwrite existing files",
				})
			}
		}
	}

	// Validate config conversion would succeed (skip if no config)
	configPath := filepath.Join(m.opts.SourceDir, "openclaw.json")
	if _, err := os.Stat(configPath); err == nil {
		cfg, err := m.loadOpenClawConfig(configPath)
		if err != nil {
			return fmt.Errorf("cannot parse config for conversion: %w", err)
		}
		// Check for manual review items
		m.checkManualReviewItems(cfg)
	}

	return nil
}

func (m *Migrator) checkManualReviewItems(cfg *OpenClawConfig) {
	// Check for complex channel configs
	if cfg.Channels != nil && len(cfg.Channels) > 0 {
		for name := range cfg.Channels {
			m.addIssue(Issue{
				Severity:     SeverityWarning,
				Phase:        PhaseDryRun,
				Path:         fmt.Sprintf("channels.%s", name),
				Message:      fmt.Sprintf("channel '%s' requires manual review for Metiq compatibility", name),
				ManualReview: true,
			})
		}
	}

	// Check for hooks
	if cfg.Hooks != nil && len(cfg.Hooks) > 0 {
		m.addIssue(Issue{
			Severity:     SeverityWarning,
			Phase:        PhaseDryRun,
			Path:         "hooks",
			Message:      "hooks configuration requires manual review",
			ManualReview: true,
		})
	}

	// Check for MCP servers
	if cfg.MCP != nil {
		m.addIssue(Issue{
			Severity:     SeverityInfo,
			Phase:        PhaseDryRun,
			Path:         "mcp",
			Message:      "MCP configuration will be migrated; verify server paths",
			ManualReview: true,
		})
	}
}

// ─── Apply Phase ─────────────────────────────────────────────────────────────

func (m *Migrator) apply() error {
	// Create target directory
	if err := os.MkdirAll(m.opts.TargetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Process each artifact
	for i, art := range m.report.Artifacts {
		if art.Action == ActionOmit {
			continue
		}

		var err error
		switch art.Action {
		case ActionMigrate, ActionPreserve:
			err = m.copyArtifact(&m.report.Artifacts[i])
		case ActionTransform:
			err = m.transformArtifact(&m.report.Artifacts[i])
		case ActionConvert:
			err = m.convertArtifact(&m.report.Artifacts[i])
		}

		if err != nil {
			m.addIssue(Issue{
				Severity: SeverityError,
				Phase:    PhaseApply,
				Path:     art.SourcePath,
				Message:  err.Error(),
			})
			return err
		}
	}

	return nil
}

func (m *Migrator) copyArtifact(art *ArtifactEntry) error {
	srcInfo, err := os.Stat(art.SourcePath)
	if err != nil {
		return err
	}

	if srcInfo.IsDir() {
		return m.copyDir(art.SourcePath, art.TargetPath, art)
	}
	return m.copyFile(art.SourcePath, art.TargetPath, art)
}

func (m *Migrator) copyDir(src, dst string, art *ArtifactEntry) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip runtime garbage subdirectories
		for _, garbage := range RuntimeGarbage {
			if strings.Contains(path, string(os.PathSeparator)+garbage+string(os.PathSeparator)) ||
				strings.HasSuffix(path, string(os.PathSeparator)+garbage) {
				return filepath.SkipDir
			}
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return m.copyFile(path, dstPath, nil)
	})
}

func (m *Migrator) copyFile(src, dst string, art *ArtifactEntry) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	hash := sha256.New()
	writer := io.MultiWriter(dstFile, hash)

	if _, err := io.Copy(writer, srcFile); err != nil {
		return err
	}

	if art != nil {
		art.Checksum = hex.EncodeToString(hash.Sum(nil))
	}

	// Preserve permissions
	srcInfo, _ := os.Stat(src)
	return os.Chmod(dst, srcInfo.Mode())
}

func (m *Migrator) transformArtifact(art *ArtifactEntry) error {
	if art.Type != ArtifactMemory {
		return m.copyArtifact(art)
	}

	// Read source file
	content, err := os.ReadFile(art.SourcePath)
	if err != nil {
		return err
	}

	// Transform memory file
	transformed := m.transformMemoryFile(string(content), art.SourcePath)

	// Write to target
	if err := os.MkdirAll(filepath.Dir(art.TargetPath), 0755); err != nil {
		return err
	}

	hash := sha256.New()
	hash.Write([]byte(transformed))
	art.Checksum = hex.EncodeToString(hash.Sum(nil))

	return os.WriteFile(art.TargetPath, []byte(transformed), 0644)
}

func (m *Migrator) transformMemoryFile(content, sourcePath string) string {
	// Normalize paths first
	normalized := m.normalizePathsInContent(content)

	// Create migration metadata as an HTML comment (not frontmatter)
	// This preserves provenance without breaking the runtime's frontmatter parser
	fm := MemoryFrontmatter{
		MigratedFrom:      "openclaw",
		MigrationDate:     m.report.MigrationDate.Format(time.RFC3339),
		SourceAgent:       m.report.SourceAgent,
		TargetRuntime:     "metiq",
		OriginalWorkspace: m.opts.SourceDir,
	}
	fmBytes, _ := yaml.Marshal(fm)
	migrationComment := "<!--\nMigration metadata (do not edit):\n" + string(fmBytes) + "-->\n\n"

	// Determine if this is a topic file (in memory/ dir) vs entrypoint (MEMORY.md)
	isTopicFile := strings.Contains(sourcePath, string(os.PathSeparator)+"memory"+string(os.PathSeparator))

	// Check if file already has valid frontmatter
	if strings.HasPrefix(strings.TrimSpace(normalized), "---") {
		// Preserve existing frontmatter, append migration comment after it
		parts := strings.SplitN(strings.TrimSpace(normalized), "---", 3)
		if len(parts) >= 3 {
			// Has valid frontmatter block: ---\n<yaml>\n---\n<body>
			existingFM := parts[1]
			body := parts[2]
			if strings.HasPrefix(body, "\n") {
				body = body[1:]
			}
			return "---\n" + existingFM + "---\n\n" + migrationComment + body
		}
	}

	// No existing frontmatter
	if isTopicFile {
		// Topic files need frontmatter for the runtime to recognize them
		// Generate a minimal valid frontmatter based on the filename
		baseName := filepath.Base(sourcePath)
		topicName := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		topicName = strings.ReplaceAll(topicName, "-", " ")
		topicName = strings.ReplaceAll(topicName, "_", " ")
		topicName = strings.Title(topicName)

		generatedFM := fmt.Sprintf(`---
name: "%s"
description: "Migrated from OpenClaw - review and update this description"
type: user
---

`, topicName)
		return generatedFM + migrationComment + normalized
	}

	// MEMORY.md entrypoint - no frontmatter needed, just prepend migration comment
	return migrationComment + normalized
}

func (m *Migrator) normalizePathsInContent(content string) string {
	// Replace common OpenClaw paths with Metiq equivalents
	replacements := map[string]string{
		"~/.openclaw/":          "~/.metiq/",
		"/openclaw/":            "/metiq/",
		"$HOME/.openclaw/":      "$HOME/.metiq/",
		"${HOME}/.openclaw/":    "${HOME}/.metiq/",
		"openclaw.json":         "config.json",
	}

	result := content
	for old, new := range replacements {
		result = strings.ReplaceAll(result, old, new)
	}

	// Handle absolute paths (regex for /Users/*/.../.openclaw or /home/*/.../.openclaw)
	re := regexp.MustCompile(`(/(?:Users|home)/[^/]+/(?:[^/]+/)*)\.openclaw/`)
	result = re.ReplaceAllString(result, "${1}.metiq/")

	return result
}

func (m *Migrator) convertArtifact(art *ArtifactEntry) error {
	switch art.Type {
	case ArtifactConfig:
		return m.convertConfig(art)
	case ArtifactCron:
		return m.convertCron(art)
	case ArtifactIdentity:
		return m.convertBootstrap(art)
	case ArtifactMemoryDB:
		return m.convertMemoryDB(art)
	case ArtifactAuthProfiles:
		return m.convertAuthProfiles(art)
	default:
		return m.copyArtifact(art)
	}
}

func (m *Migrator) convertConfig(art *ArtifactEntry) error {
	cfg, err := m.loadOpenClawConfig(art.SourcePath)
	if err != nil {
		return err
	}

	metiqCfg := m.convertOpenClawToMetiq(cfg)

	// Marshal with indentation
	data, err := json.MarshalIndent(metiqCfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(art.TargetPath), 0755); err != nil {
		return err
	}

	hash := sha256.New()
	hash.Write(data)
	art.Checksum = hex.EncodeToString(hash.Sum(nil))

	return os.WriteFile(art.TargetPath, data, 0644)
}

func (m *Migrator) convertBootstrap(art *ArtifactEntry) error {
	cfg, err := m.loadOpenClawConfig(art.SourcePath)
	if err != nil {
		return err
	}

	bootstrap := m.extractBootstrap(cfg)

	data, err := json.MarshalIndent(bootstrap, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(art.TargetPath), 0755); err != nil {
		return err
	}

	hash := sha256.New()
	hash.Write(data)
	art.Checksum = hex.EncodeToString(hash.Sum(nil))

	return os.WriteFile(art.TargetPath, data, 0644)
}

func (m *Migrator) convertCron(art *ArtifactEntry) error {
	data, err := os.ReadFile(art.SourcePath)
	if err != nil {
		return err
	}

	var jobs []OpenClawJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		// Try parsing as object with jobs array
		var wrapper struct {
			Jobs []OpenClawJob `json:"jobs"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse jobs.json: %w", err)
		}
		jobs = wrapper.Jobs
	}

	// Convert to Metiq format (mostly compatible, just normalize IDs)
	metiqJobs := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		id := job.ID
		if id == "" {
			id = job.JobID
		}

		mj := map[string]any{
			"id":       id,
			"schedule": job.Schedule,
			"enabled":  job.Enabled,
		}

		if job.Name != "" {
			mj["name"] = job.Name
		}
		if job.Command != "" {
			mj["command"] = job.Command
		}
		if job.Prompt != "" {
			mj["prompt"] = job.Prompt
		}
		if job.AgentID != "" {
			mj["agent_id"] = job.AgentID
		}
		if job.OneShot {
			mj["one_shot"] = true
		}
		if job.SessionKey != "" {
			mj["session_key"] = job.SessionKey
		}
		if job.Description != "" {
			mj["description"] = job.Description
		}

		metiqJobs = append(metiqJobs, mj)

		// Flag jobs that need manual review
		if job.Command != "" && (strings.Contains(job.Command, "openclaw") || strings.Contains(job.Command, "~/.openclaw")) {
			m.addIssue(Issue{
				Severity:     SeverityWarning,
				Phase:        PhaseApply,
				Path:         fmt.Sprintf("cron.jobs[%s]", id),
				Message:      fmt.Sprintf("job '%s' references openclaw paths in command; needs manual update", id),
				ManualReview: true,
			})
		}
	}

	result, err := json.MarshalIndent(metiqJobs, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(art.TargetPath), 0755); err != nil {
		return err
	}

	hash := sha256.New()
	hash.Write(result)
	art.Checksum = hex.EncodeToString(hash.Sum(nil))

	return os.WriteFile(art.TargetPath, result, 0644)
}

func (m *Migrator) convertMemoryDB(art *ArtifactEntry) error {
	// Use the MemoryImporter to handle the migration
	cfg := MemoryImportConfig{
		SourcePaths:    []string{art.SourcePath},
		TargetPath:     art.TargetPath,
		Deduplicate:    true,
		CopyEmbeddings: true,
		DryRun:         m.opts.DryRun,
		Verbose:        m.opts.Verbose,
	}

	importer := NewMemoryImporter(cfg)
	stats, err := importer.Import()
	if err != nil {
		return fmt.Errorf("memory import failed: %w", err)
	}

	// Report statistics
	m.addIssue(Issue{
		Severity: SeverityInfo,
		Phase:    PhaseApply,
		Path:     art.TargetPath,
		Message: fmt.Sprintf("Imported %d/%d memory chunks from SQLite database (deduplicated: %d, skipped: %d)",
			stats.ChunksImported, stats.ChunksFound, stats.ChunksDeduplicated, stats.ChunksSkipped),
	})

	if stats.EmbeddingsCopied > 0 {
		m.addIssue(Issue{
			Severity: SeverityInfo,
			Phase:    PhaseApply,
			Path:     art.TargetPath,
			Message:  fmt.Sprintf("Copied %d embeddings (skipped: %d)", stats.EmbeddingsCopied, stats.EmbeddingsSkipped),
		})
	}

	// Report any errors from the import
	for _, importErr := range stats.Errors {
		m.addIssue(Issue{
			Severity: SeverityWarning,
			Phase:    PhaseApply,
			Path:     art.SourcePath,
			Message:  importErr,
		})
	}

	// Generate checksum of imported data for verification
	if !m.opts.DryRun {
		if info, err := os.Stat(art.TargetPath); err == nil {
			art.Checksum = fmt.Sprintf("size:%d", info.Size())
		}
	}

	return nil
}

func (m *Migrator) convertAuthProfiles(art *ArtifactEntry) error {
	// Read OpenClaw auth-profiles.json
	data, err := os.ReadFile(art.SourcePath)
	if err != nil {
		return fmt.Errorf("read auth profiles: %w", err)
	}

	// Parse the auth profiles
	var profiles map[string]any
	if err := json.Unmarshal(data, &profiles); err != nil {
		return fmt.Errorf("parse auth profiles: %w", err)
	}

	// Convert to Metiq format
	// OpenClaw structure: { "profileName": { "type": "api-key", "key": "..." } }
	// Metiq structure: Similar but may need field normalization
	converted := make(map[string]any)

	for name, profile := range profiles {
		profileMap, ok := profile.(map[string]any)
		if !ok {
			continue
		}

		// Normalize field names
		convertedProfile := make(map[string]any)
		for k, v := range profileMap {
			// Map OpenClaw field names to Metiq equivalents
			switch k {
			case "apiKey":
				convertedProfile["api_key"] = v
			case "baseUrl":
				convertedProfile["base_url"] = v
			case "type", "mode":
				convertedProfile["type"] = v
			default:
				convertedProfile[k] = v
			}
		}

		converted[name] = convertedProfile
	}

	// Write converted profiles
	result, err := json.MarshalIndent(converted, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(art.TargetPath), 0755); err != nil {
		return err
	}

	hash := sha256.New()
	hash.Write(result)
	art.Checksum = hex.EncodeToString(hash.Sum(nil))

	// Write with restricted permissions (contains secrets)
	if err := os.WriteFile(art.TargetPath, result, 0600); err != nil {
		return err
	}

	m.addIssue(Issue{
		Severity:   SeverityInfo,
		Phase:      PhaseApply,
		Path:       art.TargetPath,
		Message:    fmt.Sprintf("Migrated %d auth profiles", len(converted)),
		Suggestion: "Verify API keys and OAuth tokens work correctly",
	})

	return nil
}

// ─── Verify Phase ────────────────────────────────────────────────────────────

func (m *Migrator) verify() error {
	// Verify config.json exists and is valid JSON (only if we expected to create it)
	configPath := filepath.Join(m.opts.TargetDir, "config.json")
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("config.json is not valid JSON: %w", err)
		}
	} else if m.hasArtifactType(ArtifactConfig) {
		return fmt.Errorf("config.json not created")
	}

	// Verify bootstrap.json (only if we expected to create it)
	bootstrapPath := filepath.Join(m.opts.TargetDir, "bootstrap.json")
	if _, err := os.Stat(bootstrapPath); err == nil {
		data, err := os.ReadFile(bootstrapPath)
		if err != nil {
			return err
		}
		var bootstrap map[string]any
		if err := json.Unmarshal(data, &bootstrap); err != nil {
			return fmt.Errorf("bootstrap.json is not valid JSON: %w", err)
		}
	} else if m.hasArtifactType(ArtifactIdentity) {
		return fmt.Errorf("bootstrap.json not created")
	}

	// Verify checksums for transformed files
	for _, art := range m.report.Artifacts {
		if art.Checksum == "" || art.TargetPath == "" {
			continue
		}

		data, err := os.ReadFile(art.TargetPath)
		if err != nil {
			m.addIssue(Issue{
				Severity: SeverityWarning,
				Phase:    PhaseVerify,
				Path:     art.TargetPath,
				Message:  fmt.Sprintf("cannot read for checksum verification: %v", err),
			})
			continue
		}

		hash := sha256.New()
		hash.Write(data)
		actual := hex.EncodeToString(hash.Sum(nil))

		if actual != art.Checksum {
			m.addIssue(Issue{
				Severity: SeverityError,
				Phase:    PhaseVerify,
				Path:     art.TargetPath,
				Message:  "checksum mismatch after migration",
			})
		}
	}

	return nil
}

// hasArtifactType returns true if any artifact with the given type was registered.
func (m *Migrator) hasArtifactType(t ArtifactType) bool {
	for _, art := range m.report.Artifacts {
		if art.Type == t {
			return true
		}
	}
	return false
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (m *Migrator) loadOpenClawConfig(path string) (*OpenClawConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg OpenClawConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (m *Migrator) extractAgentName(cfg *OpenClawConfig) string {
	if cfg.Agents != nil && len(cfg.Agents.List) > 0 {
		return cfg.Agents.List[0].Name
	}
	return "main"
}

func (m *Migrator) convertOpenClawToMetiq(cfg *OpenClawConfig) map[string]any {
	result := map[string]any{
		"$schema": "https://metiq.ai/schema/config.json",
		"version": 1,
	}

	// Convert agents
	if cfg.Agents != nil {
		agents := map[string]any{}

		if cfg.Agents.Defaults != nil {
			defaults := map[string]any{}
			if cfg.Agents.Defaults.Model != "" {
				defaults["model"] = cfg.Agents.Defaults.Model
			}
			if cfg.Agents.Defaults.Workspace != "" {
				// Normalize workspace path
				ws := strings.ReplaceAll(cfg.Agents.Defaults.Workspace, ".openclaw", ".metiq")
				defaults["workspace"] = ws
			}
			if len(defaults) > 0 {
				agents["defaults"] = defaults
			}
		}

		if len(cfg.Agents.List) > 0 {
			list := make([]map[string]any, 0, len(cfg.Agents.List))
			for _, agent := range cfg.Agents.List {
				a := map[string]any{}
				if agent.ID != "" {
					a["id"] = agent.ID
				}
				if agent.Name != "" {
					a["name"] = agent.Name
				}
				if agent.Model != "" {
					a["model"] = agent.Model
				}
				if agent.Workspace != "" {
					ws := strings.ReplaceAll(agent.Workspace, ".openclaw", ".metiq")
					a["workspace_dir"] = ws
				}
				list = append(list, a)
			}
			agents["list"] = list
		}

		if len(agents) > 0 {
			result["agents"] = agents
		}
	}

	// Convert providers
	if cfg.Models != nil && cfg.Models.Providers != nil {
		providers := map[string]any{}
		for name, p := range cfg.Models.Providers {
			provider := map[string]any{
				"enabled": p.Enabled,
			}
			if p.APIKey != "" {
				provider["api_key"] = p.APIKey
			}
			if p.BaseURL != "" {
				provider["base_url"] = p.BaseURL
			}
			providers[name] = provider
		}
		if len(providers) > 0 {
			result["providers"] = providers
		}

		if cfg.Models.Default != "" {
			result["agent"] = map[string]any{
				"default_model": cfg.Models.Default,
			}
		}
	}

	// Convert cron
	if cfg.Cron != nil {
		result["cron"] = map[string]any{
			"enabled": cfg.Cron.Enabled,
		}
	}

	// Migrate MCP config directly (format is compatible)
	if cfg.MCP != nil {
		result["extra"] = map[string]any{
			"mcp": cfg.MCP,
		}
	}

	// Migrate secrets references
	if cfg.Secrets != nil {
		result["secrets"] = cfg.Secrets
	}

	// Migrate memory config
	if cfg.Memory != nil {
		if extra, ok := result["extra"].(map[string]any); ok {
			extra["memory"] = cfg.Memory
		} else {
			result["extra"] = map[string]any{
				"memory": cfg.Memory,
			}
		}
	}

	return result
}

func (m *Migrator) extractBootstrap(cfg *OpenClawConfig) map[string]any {
	bootstrap := map[string]any{}

	// Extract private key
	if cfg.Auth != nil && cfg.Auth.NostrPrivateKey != "" {
		bootstrap["private_key"] = cfg.Auth.NostrPrivateKey
	}

	// Default relays (user should customize)
	bootstrap["relays"] = []string{
		"wss://relay.damus.io",
		"wss://nos.lol",
		"wss://relay.nostr.band",
	}

	// Gateway config
	if cfg.Gateway != nil {
		if cfg.Gateway.Auth != nil && cfg.Gateway.Auth.Token != "" {
			bootstrap["admin_token"] = cfg.Gateway.Auth.Token
		}
		if cfg.Gateway.WS != nil && cfg.Gateway.WS.Port > 0 {
			bootstrap["gateway_ws_listen_addr"] = fmt.Sprintf("127.0.0.1:%d", cfg.Gateway.WS.Port)
		}
	}

	return bootstrap
}

func (m *Migrator) addArtifact(art ArtifactEntry) {
	m.report.Artifacts = append(m.report.Artifacts, art)
}

func (m *Migrator) addIssue(issue Issue) {
	m.report.Issues = append(m.report.Issues, issue)
}
