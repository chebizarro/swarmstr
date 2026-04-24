// Package migrate provides OpenClaw → Metiq agent migration.
package migrate

import (
	"time"
)

// Phase represents a migration phase.
type Phase string

const (
	PhaseAudit  Phase = "audit"
	PhaseDryRun Phase = "dry-run"
	PhaseApply  Phase = "apply"
	PhaseVerify Phase = "verify"
)

// ArtifactType categorizes migrated artifacts.
type ArtifactType string

const (
	ArtifactIdentity     ArtifactType = "identity"
	ArtifactWorkspace    ArtifactType = "workspace"
	ArtifactMemory       ArtifactType = "memory"
	ArtifactMemoryDB     ArtifactType = "memory_db"
	ArtifactCron         ArtifactType = "cron"
	ArtifactConfig       ArtifactType = "config"
	ArtifactSecrets      ArtifactType = "secrets"
	ArtifactAuthProfiles ArtifactType = "auth_profiles"
	ArtifactPlugins      ArtifactType = "plugins"
	ArtifactHooks        ArtifactType = "hooks"
	ArtifactSkills       ArtifactType = "skills"
	ArtifactCredentials  ArtifactType = "credentials"
	ArtifactRuntimeState ArtifactType = "runtime_state"
)

// ArtifactAction describes what happens to an artifact.
type ArtifactAction string

const (
	ActionPreserve  ArtifactAction = "preserve"
	ActionMigrate   ArtifactAction = "migrate"
	ActionTransform ArtifactAction = "transform"
	ActionConvert   ArtifactAction = "convert"
	ActionOmit      ArtifactAction = "omit"
)

// Severity for issues/warnings.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Options configures a migration run.
type Options struct {
	SourceDir       string // OpenClaw home directory (e.g., ~/.openclaw)
	TargetDir       string // Metiq home directory (e.g., ~/.metiq)
	DryRun          bool   // If true, simulate only
	Verbose         bool   // Verbose output
	Force           bool   // Overwrite existing target files
	SkipSecrets     bool   // Don't migrate .env and secrets
	MigrateMemoryDB bool   // Migrate SQLite memory database
	MigrateAuth     bool   // Migrate auth-profiles.json
	MigratePlugins  bool   // Migrate plugins and hooks
	MigrateSkills   bool   // Migrate managed skills
}

// ArtifactEntry describes a single artifact in the migration.
type ArtifactEntry struct {
	Type        ArtifactType   `json:"type"`
	Action      ArtifactAction `json:"action"`
	SourcePath  string         `json:"source_path,omitempty"`
	TargetPath  string         `json:"target_path,omitempty"`
	Description string         `json:"description,omitempty"`
	Checksum    string         `json:"checksum,omitempty"` // SHA256 after migration
}

// Issue represents a warning or error found during migration.
type Issue struct {
	Severity    Severity `json:"severity"`
	Phase       Phase    `json:"phase"`
	Path        string   `json:"path,omitempty"`
	Message     string   `json:"message"`
	Suggestion  string   `json:"suggestion,omitempty"`
	ManualReview bool    `json:"manual_review,omitempty"`
}

// Report is the migration result.
type Report struct {
	SourceDir       string          `json:"source_dir"`
	TargetDir       string          `json:"target_dir"`
	SourceAgent     string          `json:"source_agent,omitempty"`
	MigrationDate   time.Time       `json:"migration_date"`
	Phase           Phase           `json:"phase"`
	Success         bool            `json:"success"`
	Artifacts       []ArtifactEntry `json:"artifacts"`
	Issues          []Issue         `json:"issues"`
	OmittedPaths    []string        `json:"omitted_paths,omitempty"`
	DurationMs      int64           `json:"duration_ms"`
}

// MemoryFrontmatter is the YAML front-matter injected into memory files.
type MemoryFrontmatter struct {
	MigratedFrom      string `yaml:"migrated_from"`
	MigrationDate     string `yaml:"migration_date"`
	SourceAgent       string `yaml:"source_agent"`
	TargetRuntime     string `yaml:"target_runtime"`
	OriginalWorkspace string `yaml:"original_workspace"`
}

// OpenClawConfig represents the subset of openclaw.json we need to migrate.
// This is a minimal representation; actual OpenClaw configs are richer.
type OpenClawConfig struct {
	Schema string `json:"$schema,omitempty"`
	Meta   *struct {
		LastTouchedVersion string `json:"lastTouchedVersion,omitempty"`
	} `json:"meta,omitempty"`
	Auth *struct {
		NostrPrivateKey string `json:"nostrPrivateKey,omitempty"`
	} `json:"auth,omitempty"`
	Models *struct {
		Default   string `json:"default,omitempty"`
		Providers map[string]struct {
			Enabled bool   `json:"enabled,omitempty"`
			APIKey  string `json:"apiKey,omitempty"`
			BaseURL string `json:"baseUrl,omitempty"`
		} `json:"providers,omitempty"`
	} `json:"models,omitempty"`
	Agents *struct {
		Defaults *struct {
			Model     string `json:"model,omitempty"`
			Workspace string `json:"workspace,omitempty"`
		} `json:"defaults,omitempty"`
		List []struct {
			ID        string `json:"id,omitempty"`
			Name      string `json:"name,omitempty"`
			Model     string `json:"model,omitempty"`
			Workspace string `json:"workspace,omitempty"`
		} `json:"list,omitempty"`
	} `json:"agents,omitempty"`
	Channels map[string]any `json:"channels,omitempty"`
	Gateway  *struct {
		Auth *struct {
			Token string `json:"token,omitempty"`
		} `json:"auth,omitempty"`
		WS *struct {
			Port int `json:"port,omitempty"`
		} `json:"ws,omitempty"`
	} `json:"gateway,omitempty"`
	Cron *struct {
		Enabled bool `json:"enabled,omitempty"`
	} `json:"cron,omitempty"`
	Hooks   map[string]any `json:"hooks,omitempty"`
	Memory  map[string]any `json:"memory,omitempty"`
	MCP     map[string]any `json:"mcp,omitempty"`
	Secrets map[string]any `json:"secrets,omitempty"`
}

// OpenClawJob represents a cron job from jobs.json.
type OpenClawJob struct {
	ID          string `json:"id,omitempty"`
	JobID       string `json:"jobId,omitempty"` // legacy field
	Name        string `json:"name,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	Command     string `json:"command,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	AgentID     string `json:"agentId,omitempty"`
	Enabled     bool   `json:"enabled"`
	OneShot     bool   `json:"oneShot,omitempty"`
	SessionKey  string `json:"sessionKey,omitempty"`
	Description string `json:"description,omitempty"`
}

// RuntimeGarbage lists paths that should be omitted (runtime state).
var RuntimeGarbage = []string{
	"delivery-queue",
	"quarantine",
	"sessions",
	"tasks",
	"locks",
	"cache",
	"tmp",
	".tmp",
}
