// Package permissions provides a unified permission engine for tool execution.
package permissions

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ─── Configuration File Schema ───────────────────────────────────────────────

// Config is the top-level permission configuration loaded from YAML.
// Typically stored at ~/.metiq/permissions.yaml or .metiq/permissions.yaml
type Config struct {
	// Version is the config schema version (currently "1")
	Version string `yaml:"version"`

	// DefaultProfile is the profile for agents without explicit configuration.
	// Options: "autonomous", "permissive", "restrictive", "standard"
	// Default: "standard"
	DefaultProfile string `yaml:"default_profile,omitempty"`

	// Agents maps agent IDs to their permission configuration.
	Agents map[string]AgentConfig `yaml:"agents,omitempty"`

	// Rules defines custom permission rules.
	Rules []RuleConfig `yaml:"rules,omitempty"`

	// GlobalSettings affects all permission evaluation.
	GlobalSettings GlobalConfig `yaml:"global,omitempty"`
}

// AgentConfig defines permissions for a specific agent.
type AgentConfig struct {
	// Profile is a predefined permission profile.
	// Options: "autonomous", "permissive", "restrictive", "readonly", "standard"
	Profile string `yaml:"profile,omitempty"`

	// Allow lists tool patterns this agent can use without confirmation.
	Allow []string `yaml:"allow,omitempty"`

	// Deny lists tool patterns this agent cannot use.
	Deny []string `yaml:"deny,omitempty"`

	// Ask lists tool patterns requiring confirmation.
	Ask []string `yaml:"ask,omitempty"`

	// AllowCategories lists categories this agent can use freely.
	AllowCategories []string `yaml:"allow_categories,omitempty"`

	// DenyCategories lists categories this agent cannot use.
	DenyCategories []string `yaml:"deny_categories,omitempty"`

	// Enabled controls whether this agent configuration is active.
	// Default: true
	Enabled *bool `yaml:"enabled,omitempty"`
}

// RuleConfig defines a custom permission rule in YAML.
type RuleConfig struct {
	// ID is a unique identifier for this rule.
	ID string `yaml:"id"`

	// Behavior is "allow", "ask", or "deny".
	Behavior string `yaml:"behavior"`

	// Tool is a glob pattern matching tool names (e.g., "bash", "mcp:*").
	Tool string `yaml:"tool"`

	// Content is an optional regex pattern for tool arguments.
	Content string `yaml:"content,omitempty"`

	// Category restricts the rule to a specific tool category.
	Category string `yaml:"category,omitempty"`

	// Agent restricts the rule to a specific agent ID.
	Agent string `yaml:"agent,omitempty"`

	// Scope is the rule scope: "global", "user", "project", "agent", "session".
	// Default: "global"
	Scope string `yaml:"scope,omitempty"`

	// Description explains the rule's purpose.
	Description string `yaml:"description,omitempty"`

	// Enabled controls whether this rule is active. Default: true
	Enabled *bool `yaml:"enabled,omitempty"`

	// ExpiresAt optionally sets when the rule expires (RFC3339 format).
	ExpiresAt string `yaml:"expires_at,omitempty"`
}

// GlobalConfig defines global permission settings.
type GlobalConfig struct {
	// DefaultBehavior when no rules match: "allow", "ask", "deny".
	// Default: "ask"
	DefaultBehavior string `yaml:"default_behavior,omitempty"`

	// AuditEnabled enables permission decision logging.
	// Default: true
	AuditEnabled *bool `yaml:"audit_enabled,omitempty"`

	// CacheEnabled enables decision caching.
	// Default: true
	CacheEnabled *bool `yaml:"cache_enabled,omitempty"`

	// CacheTTL is how long cached decisions are valid (e.g., "5m", "1h").
	// Default: "5m"
	CacheTTL string `yaml:"cache_ttl,omitempty"`
}

// ─── Configuration Loading ───────────────────────────────────────────────────

// DefaultConfig returns a minimal default configuration.
func DefaultConfig() *Config {
	return &Config{
		Version:        "1",
		DefaultProfile: "standard",
		Agents:         make(map[string]AgentConfig),
		GlobalSettings: GlobalConfig{
			DefaultBehavior: "ask",
		},
	}
}

// LoadConfig reads permission configuration from a YAML file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Version == "" {
		cfg.Version = "1"
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]AgentConfig)
	}

	return &cfg, nil
}

// LoadConfigFromDir searches for permissions.yaml in standard locations.
// Search order: ./.metiq/permissions.yaml, ~/.metiq/permissions.yaml
func LoadConfigFromDir(projectDir string) (*Config, error) {
	// Try project-local config first
	projectPath := filepath.Join(projectDir, ".metiq", "permissions.yaml")
	if _, err := os.Stat(projectPath); err == nil {
		return LoadConfig(projectPath)
	}

	// Try user config
	home, err := os.UserHomeDir()
	if err == nil {
		userPath := filepath.Join(home, ".metiq", "permissions.yaml")
		if _, err := os.Stat(userPath); err == nil {
			return LoadConfig(userPath)
		}
	}

	// Return defaults if no config found
	return DefaultConfig(), nil
}

// SaveConfig writes the configuration to a YAML file.
func SaveConfig(cfg *Config, path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

// ─── Engine Configuration from YAML ──────────────────────────────────────────

// NewEngineFromConfig creates a permission engine from a YAML configuration.
func NewEngineFromConfig(baseDir string, cfg *Config) (*Engine, error) {
	// Build engine config from global settings
	engineCfg := DefaultEngineConfig()

	if cfg.GlobalSettings.DefaultBehavior != "" {
		engineCfg.DefaultBehavior = Behavior(cfg.GlobalSettings.DefaultBehavior)
	}
	if cfg.GlobalSettings.AuditEnabled != nil {
		engineCfg.AuditEnabled = *cfg.GlobalSettings.AuditEnabled
	}
	if cfg.GlobalSettings.CacheEnabled != nil {
		engineCfg.CacheEnabled = *cfg.GlobalSettings.CacheEnabled
	}
	if cfg.GlobalSettings.CacheTTL != "" {
		if ttl, err := time.ParseDuration(cfg.GlobalSettings.CacheTTL); err == nil {
			engineCfg.CacheTTL = ttl
		}
	}

	engine := NewEngine(baseDir, engineCfg)

	// Load default rules based on default profile
	switch cfg.DefaultProfile {
	case "autonomous":
		engine.LoadCriticalSafetyRules()
	case "permissive":
		engine.LoadPermissiveRules()
	case "restrictive", "standard", "":
		engine.LoadDefaultRules()
	}

	// Apply agent configurations
	for agentID, agentCfg := range cfg.Agents {
		if agentCfg.Enabled != nil && !*agentCfg.Enabled {
			continue
		}

		if err := applyAgentConfig(engine, agentID, agentCfg); err != nil {
			return nil, fmt.Errorf("configuring agent %s: %w", agentID, err)
		}
	}

	// Load custom rules
	for _, ruleCfg := range cfg.Rules {
		if ruleCfg.Enabled != nil && !*ruleCfg.Enabled {
			continue
		}

		rule, err := ruleFromConfig(ruleCfg)
		if err != nil {
			return nil, fmt.Errorf("invalid rule %s: %w", ruleCfg.ID, err)
		}

		if err := engine.AddRule(rule); err != nil {
			return nil, fmt.Errorf("adding rule %s: %w", ruleCfg.ID, err)
		}
	}

	return engine, nil
}

// applyAgentConfig applies an agent's configuration to the engine.
func applyAgentConfig(engine *Engine, agentID string, cfg AgentConfig) error {
	// Apply profile first (provides base rules)
	if cfg.Profile != "" {
		if err := engine.ConfigureAgentProfile(agentID, cfg.Profile); err != nil {
			return err
		}
	}

	// Add explicit allow rules
	for _, pattern := range cfg.Allow {
		if err := engine.AllowToolForAgent(agentID, pattern); err != nil {
			return err
		}
	}

	// Add explicit deny rules
	for _, pattern := range cfg.Deny {
		rule := NewRule(fmt.Sprintf("agent-%s-deny-%s", agentID, pattern), ScopeAgent, BehaviorDeny, pattern).
			ForAgent(agentID)
		if err := engine.AddRule(rule); err != nil {
			return err
		}
	}

	// Add explicit ask rules
	for _, pattern := range cfg.Ask {
		rule := NewRule(fmt.Sprintf("agent-%s-ask-%s", agentID, pattern), ScopeAgent, BehaviorAsk, pattern).
			ForAgent(agentID)
		if err := engine.AddRule(rule); err != nil {
			return err
		}
	}

	// Add category allows
	for _, cat := range cfg.AllowCategories {
		if err := engine.AllowCategoryForAgent(agentID, ToolCategory(cat)); err != nil {
			return err
		}
	}

	// Add category denies
	for _, cat := range cfg.DenyCategories {
		if err := engine.DenyCategoryForAgent(agentID, ToolCategory(cat)); err != nil {
			return err
		}
	}

	return nil
}

// ruleFromConfig creates a Rule from a RuleConfig.
func ruleFromConfig(cfg RuleConfig) (*Rule, error) {
	behavior := Behavior(cfg.Behavior)
	if !behavior.IsValid() {
		return nil, fmt.Errorf("invalid behavior: %s", cfg.Behavior)
	}

	scope := ScopeGlobal
	if cfg.Scope != "" {
		scope = Scope(cfg.Scope)
		if !scope.IsValid() {
			return nil, fmt.Errorf("invalid scope: %s", cfg.Scope)
		}
	}

	rule := NewRule(cfg.ID, scope, behavior, cfg.Tool)

	if cfg.Content != "" {
		rule = rule.WithContentPattern(cfg.Content)
	}
	if cfg.Category != "" {
		rule = rule.WithCategory(ToolCategory(cfg.Category))
	}
	if cfg.Agent != "" {
		rule = rule.ForAgent(cfg.Agent)
	}
	if cfg.Description != "" {
		rule = rule.WithDescription(cfg.Description)
	}
	if cfg.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, cfg.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expires_at: %w", err)
		}
		rule = rule.WithExpiry(t)
	}

	return rule, nil
}

// ─── Example Configuration ───────────────────────────────────────────────────

// ExampleConfig returns an example configuration with comments.
const ExampleConfig = `# Metiq Permission Configuration
# Place this file at .metiq/permissions.yaml (project) or ~/.metiq/permissions.yaml (user)

version: "1"

# Default profile for agents without explicit configuration
# Options: autonomous, permissive, restrictive, standard
default_profile: standard

# Global settings
global:
  default_behavior: ask  # allow, ask, or deny when no rules match
  audit_enabled: true    # log all permission decisions
  cache_enabled: true    # cache decisions for performance
  cache_ttl: 5m          # how long to cache decisions

# Per-agent configuration
agents:
  # Research agent: maximum autonomy
  research-agent:
    profile: autonomous

  # Deploy agent: restricted, only allow specific tools
  deploy-agent:
    profile: restrictive
    allow:
      - kubectl
      - helm
    allow_categories:
      - filesystem

  # Code review agent: read-only access
  reviewer-agent:
    profile: readonly

  # Custom agent with fine-grained control
  custom-agent:
    allow:
      - "mcp:*"           # allow all MCP tools
      - read_file
      - write_file
    deny:
      - bash              # no shell access
    ask:
      - "plugin:*"        # confirm plugin usage
    deny_categories:
      - exec
      - network

# Custom rules (applied in addition to profiles)
rules:
  # Always deny rm -rf /
  - id: deny-rm-rf-root
    behavior: deny
    tool: bash
    content: "rm\\s+-rf\\s+/"
    description: Block recursive deletion from root

  # Allow kubectl for any agent in CI
  - id: ci-allow-kubectl
    behavior: allow
    tool: bash
    content: "^kubectl\\s+"
    scope: project
    description: Allow kubectl in this project

  # Temporary rule that expires
  - id: temp-allow-deploy
    behavior: allow
    tool: bash
    content: "^deploy\\.sh"
    agent: deploy-agent
    expires_at: "2024-12-31T23:59:59Z"
    description: Temporary deploy permission
`
