// Package permissions provides a unified permission engine for tool execution.
package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"metiq/internal/store/state"
)

// ─── Configuration File Schema ───────────────────────────────────────────────

// Config is the top-level permission configuration loaded from JSON.
// Typically stored at ~/.metiq/permissions.json or .metiq/permissions.json
type Config struct {
	// Version is the config schema version (currently "1")
	Version string `json:"version"`

	// DefaultProfile is the profile for agents without explicit configuration.
	// Options: "autonomous", "permissive", "restrictive", "standard"
	// Default: "standard"
	DefaultProfile string `json:"default_profile,omitempty"`

	// Agents maps agent IDs to their permission configuration.
	Agents map[string]AgentConfig `json:"agents,omitempty"`

	// Rules defines custom permission rules.
	Rules []RuleConfig `json:"rules,omitempty"`

	// GlobalSettings affects all permission evaluation.
	GlobalSettings GlobalConfig `json:"global,omitempty"`
}

// AgentConfig defines permissions for a specific agent.
type AgentConfig struct {
	// Profile is a predefined permission profile.
	// Options: "autonomous", "permissive", "restrictive", "readonly", "standard"
	Profile string `json:"profile,omitempty"`

	// Allow lists tool patterns this agent can use without confirmation.
	Allow []string `json:"allow,omitempty"`

	// Deny lists tool patterns this agent cannot use.
	Deny []string `json:"deny,omitempty"`

	// Ask lists tool patterns requiring confirmation.
	Ask []string `json:"ask,omitempty"`

	// AllowCategories lists categories this agent can use freely.
	AllowCategories []string `json:"allow_categories,omitempty"`

	// DenyCategories lists categories this agent cannot use.
	DenyCategories []string `json:"deny_categories,omitempty"`

	// Enabled controls whether this agent configuration is active.
	// Default: true
	Enabled *bool `json:"enabled,omitempty"`
}

// RuleConfig defines a custom permission rule.
type RuleConfig struct {
	// ID is a unique identifier for this rule.
	ID string `json:"id"`

	// Behavior is "allow", "ask", or "deny".
	Behavior string `json:"behavior"`

	// Tool is a glob pattern matching tool names (e.g., "bash", "mcp:*").
	Tool string `json:"tool"`

	// Content is an optional regex pattern for tool arguments.
	Content string `json:"content,omitempty"`

	// Category restricts the rule to a specific tool capability category.
	Category string `json:"category,omitempty"`

	// Origin restricts the rule to a tool provenance kind: "builtin", "plugin", or "mcp".
	Origin string `json:"origin,omitempty"`

	// OriginName restricts the rule to a provenance source name pattern. For MCP
	// this is the server name; for plugins this is the plugin ID.
	OriginName string `json:"origin_name,omitempty"`

	// Agent restricts the rule to a specific agent ID.
	Agent string `json:"agent,omitempty"`

	// Scope is the rule scope: "global", "user", "project", "agent", "session".
	// Default: "global"
	Scope string `json:"scope,omitempty"`

	// Description explains the rule's purpose.
	Description string `json:"description,omitempty"`

	// Enabled controls whether this rule is active. Default: true
	Enabled *bool `json:"enabled,omitempty"`

	// ExpiresAt optionally sets when the rule expires (RFC3339 format).
	ExpiresAt string `json:"expires_at,omitempty"`
}

// GlobalConfig defines global permission settings.
type GlobalConfig struct {
	// DefaultBehavior when no rules match: "allow", "ask", "deny".
	// Default: "ask"
	DefaultBehavior string `json:"default_behavior,omitempty"`

	// AuditEnabled enables permission decision logging.
	// Default: true
	AuditEnabled *bool `json:"audit_enabled,omitempty"`

	// CacheEnabled enables decision caching.
	// Default: true
	CacheEnabled *bool `json:"cache_enabled,omitempty"`

	// CacheTTL is how long cached decisions are valid (e.g., "5m", "1h").
	// Default: "5m"
	CacheTTL string `json:"cache_ttl,omitempty"`
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

// LoadConfig reads permission configuration from a JSON file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
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

// LoadConfigFromDir searches for permissions.json in standard locations.
// Search order: ./.metiq/permissions.json, ~/.metiq/permissions.json
func LoadConfigFromDir(projectDir string) (*Config, error) {
	// Try project-local config first
	projectPath := filepath.Join(projectDir, ".metiq", "permissions.json")
	if _, err := os.Stat(projectPath); err == nil {
		return LoadConfig(projectPath)
	}

	// Try user config
	home, err := os.UserHomeDir()
	if err == nil {
		userPath := filepath.Join(home, ".metiq", "permissions.json")
		if _, err := os.Stat(userPath); err == nil {
			return LoadConfig(userPath)
		}
	}

	// Return defaults if no config found
	return DefaultConfig(), nil
}

// SaveConfig writes the configuration to a JSON file.
func SaveConfig(cfg *Config, path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
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
	if cfg.Origin != "" {
		rule = rule.WithOrigin(ToolOrigin(cfg.Origin))
	}
	if cfg.OriginName != "" {
		rule = rule.WithOriginName(cfg.OriginName)
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

// ─── Integration with state.PermissionsConfig ────────────────────────────────

// NewEngineFromStateConfig creates a permission engine from state.PermissionsConfig.
// This integrates with the main config file's "permissions" section.
func NewEngineFromStateConfig(baseDir string, cfg state.PermissionsConfig) (*Engine, error) {
	// Build engine config
	engineCfg := DefaultEngineConfig()

	// Default to allow since profiles already filter tool availability
	engineCfg.DefaultBehavior = BehaviorAllow
	if cfg.DefaultBehavior != "" {
		engineCfg.DefaultBehavior = Behavior(cfg.DefaultBehavior)
	}
	if cfg.AuditEnabled != nil {
		engineCfg.AuditEnabled = *cfg.AuditEnabled
	}

	engine := NewEngine(baseDir, engineCfg)

	// Always load critical safety rules (rm -rf /, etc.)
	if err := engine.LoadCriticalSafetyRules(); err != nil {
		return nil, fmt.Errorf("loading safety rules: %w", err)
	}

	// Apply per-agent configurations
	for agentID, agentCfg := range cfg.Agents {
		if err := applyStateAgentConfig(engine, agentID, agentCfg); err != nil {
			return nil, fmt.Errorf("configuring agent %s: %w", agentID, err)
		}
	}

	// Load custom rules
	for _, ruleCfg := range cfg.Rules {
		rule, err := ruleFromStateConfig(ruleCfg)
		if err != nil {
			return nil, fmt.Errorf("invalid rule %s: %w", ruleCfg.ID, err)
		}
		if err := engine.AddRule(rule); err != nil {
			return nil, fmt.Errorf("adding rule %s: %w", ruleCfg.ID, err)
		}
	}

	return engine, nil
}

// applyStateAgentConfig applies state.AgentPermissions to the engine.
func applyStateAgentConfig(engine *Engine, agentID string, cfg state.AgentPermissions) error {
	// Apply behavior profile
	switch cfg.Behavior {
	case "autonomous":
		if err := engine.AllowAllForAgent(agentID); err != nil {
			return err
		}
	case "permissive":
		// Allow all, but load permissive ask rules for this agent
		if err := engine.AllowAllForAgent(agentID); err != nil {
			return err
		}
		// Add ask rules for dangerous patterns
		for _, rule := range PermissiveRules() {
			if rule.Behavior == BehaviorAsk {
				agentRule := NewRule(
					fmt.Sprintf("agent-%s-%s", agentID, rule.ID),
					ScopeAgent,
					BehaviorAsk,
					rule.ToolPattern,
				).ForAgent(agentID)
				if rule.ContentPattern != "" {
					agentRule = agentRule.WithContentPattern(rule.ContentPattern)
				}
				if err := engine.AddRule(agentRule); err != nil {
					return err
				}
			}
		}
	case "restrictive":
		if err := engine.AskForAgent(agentID); err != nil {
			return err
		}
	}

	// Add explicit deny patterns
	for i, pattern := range cfg.DenyPatterns {
		rule := NewRule(
			fmt.Sprintf("agent-%s-deny-%d", agentID, i),
			ScopeAgent,
			BehaviorDeny,
			"*",
		).ForAgent(agentID).WithContentPattern(pattern)
		if err := engine.AddRule(rule); err != nil {
			return err
		}
	}

	// Add explicit ask patterns
	for i, pattern := range cfg.AskPatterns {
		rule := NewRule(
			fmt.Sprintf("agent-%s-ask-%d", agentID, i),
			ScopeAgent,
			BehaviorAsk,
			"*",
		).ForAgent(agentID).WithContentPattern(pattern)
		if err := engine.AddRule(rule); err != nil {
			return err
		}
	}

	return nil
}

// ruleFromStateConfig creates a Rule from state.PermissionRule.
func ruleFromStateConfig(cfg state.PermissionRule) (*Rule, error) {
	behavior := Behavior(cfg.Behavior)
	if !behavior.IsValid() {
		return nil, fmt.Errorf("invalid behavior: %s", cfg.Behavior)
	}

	rule := NewRule(cfg.ID, ScopeGlobal, behavior, cfg.Tool)

	if cfg.Content != "" {
		rule = rule.WithContentPattern(cfg.Content)
	}
	if cfg.Category != "" {
		rule = rule.WithCategory(ToolCategory(cfg.Category))
	}
	if cfg.Origin != "" {
		rule = rule.WithOrigin(ToolOrigin(cfg.Origin))
	}
	if cfg.OriginName != "" {
		rule = rule.WithOriginName(cfg.OriginName)
	}
	if cfg.Agent != "" {
		rule = rule.ForAgent(cfg.Agent)
		rule.Scope = ScopeAgent
	}
	if cfg.Description != "" {
		rule = rule.WithDescription(cfg.Description)
	}

	return rule, nil
}

func boolPtr(b bool) *bool {
	return &b
}
