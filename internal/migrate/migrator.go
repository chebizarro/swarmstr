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

	// Check for openclaw.json
	configPath := filepath.Join(src, "openclaw.json")
	if _, err := os.Stat(configPath); err != nil {
		m.addIssue(Issue{
			Severity:   SeverityError,
			Phase:      PhaseAudit,
			Path:       configPath,
			Message:    "openclaw.json not found",
			Suggestion: "Ensure this is a valid OpenClaw home directory",
		})
		return fmt.Errorf("openclaw.json not found in %s", src)
	}

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

	// Check for workspace
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

	// Add config artifacts
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

	// Validate config conversion would succeed
	configPath := filepath.Join(m.opts.SourceDir, "openclaw.json")
	cfg, err := m.loadOpenClawConfig(configPath)
	if err != nil {
		return fmt.Errorf("cannot parse config for conversion: %w", err)
	}

	// Check for manual review items
	m.checkManualReviewItems(cfg)

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
	// Create front-matter
	fm := MemoryFrontmatter{
		MigratedFrom:      "openclaw",
		MigrationDate:     m.report.MigrationDate.Format(time.RFC3339),
		SourceAgent:       m.report.SourceAgent,
		TargetRuntime:     "metiq",
		OriginalWorkspace: m.opts.SourceDir,
	}

	fmBytes, _ := yaml.Marshal(fm)
	frontMatter := "---\n" + string(fmBytes) + "---\n\n"

	// Normalize paths: replace openclaw paths with metiq paths
	normalized := m.normalizePathsInContent(content)

	// Check if file already has front-matter
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		// File has existing front-matter; append our metadata as a comment
		return "<!-- Migration metadata:\n" + string(fmBytes) + "-->\n\n" + normalized
	}

	return frontMatter + normalized
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

// ─── Verify Phase ────────────────────────────────────────────────────────────

func (m *Migrator) verify() error {
	// Verify config.json exists and is valid JSON
	configPath := filepath.Join(m.opts.TargetDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config.json not created")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("config.json is not valid JSON: %w", err)
	}

	// Verify bootstrap.json
	bootstrapPath := filepath.Join(m.opts.TargetDir, "bootstrap.json")
	if _, err := os.Stat(bootstrapPath); err != nil {
		return fmt.Errorf("bootstrap.json not created")
	}

	data, err = os.ReadFile(bootstrapPath)
	if err != nil {
		return err
	}

	var bootstrap map[string]any
	if err := json.Unmarshal(data, &bootstrap); err != nil {
		return fmt.Errorf("bootstrap.json is not valid JSON: %w", err)
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
