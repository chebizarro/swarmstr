package permissions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "permissions.json")

	configJSON := `{
	"version": "1",
	"default_profile": "permissive",
	"agents": {
	"research-agent": {"profile": "autonomous"},
	"deploy-agent": {"profile": "restrictive", "allow": ["kubectl"]}
	},
	"rules": [
	{"id": "deny-sudo", "behavior": "deny", "tool": "bash", "content": "^sudo"}
	]
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Version != "1" {
		t.Errorf("expected version 1, got %s", cfg.Version)
	}
	if cfg.DefaultProfile != "permissive" {
		t.Errorf("expected default_profile permissive, got %s", cfg.DefaultProfile)
	}
	if len(cfg.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(cfg.Agents))
	}
	if len(cfg.Rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(cfg.Rules))
	}
}

func TestNewEngineFromConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "permissions.json")

	configJSON := `{
	"version": "1",
	"default_profile": "standard",
	"global": {
	"default_behavior": "ask",
	"audit_enabled": false
	},
	"agents": {
	"trusted-agent": {"profile": "autonomous"},
	"restricted-agent": {"profile": "restrictive"}
	},
	"rules": [
	{"id": "custom-deny", "behavior": "deny", "tool": "bash", "content": "^danger", "scope": "session"}
	]
}`

	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	engine, err := NewEngineFromConfig(tmpDir, cfg)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	ctx := context.Background()

	// Trusted agent should be allowed
	reqTrusted := NewToolRequest("bash", CategoryExec).
		WithContent("ls -la").
		WithContext("", "", "trusted-agent", "")
	decisionTrusted := engine.Evaluate(ctx, reqTrusted)

	if decisionTrusted.Behavior != BehaviorAllow {
		t.Errorf("trusted-agent should be allowed, got %s", decisionTrusted.Behavior)
	}

	// Restricted agent should ask
	reqRestricted := NewToolRequest("bash", CategoryExec).
		WithContent("ls -la").
		WithContext("", "", "restricted-agent", "")
	decisionRestricted := engine.Evaluate(ctx, reqRestricted)

	if decisionRestricted.Behavior != BehaviorAsk {
		t.Errorf("restricted-agent should ask, got %s", decisionRestricted.Behavior)
	}

	// Custom deny rule should work
	reqDanger := NewToolRequest("bash", CategoryExec).
		WithContent("danger command").
		WithContext("", "", "trusted-agent", "")
	decisionDanger := engine.Evaluate(ctx, reqDanger)

	if decisionDanger.Behavior != BehaviorDeny {
		t.Errorf("danger command should be denied, got %s", decisionDanger.Behavior)
	}
}

func TestAgentAllowDenyConfig(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		Version:        "1",
		DefaultProfile: "standard",
		Agents: map[string]AgentConfig{
			"deploy-bot": {
				Allow: []string{"kubectl", "helm"},
				Deny:  []string{"bash"},
			},
		},
	}

	engine, err := NewEngineFromConfig(tmpDir, cfg)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	ctx := context.Background()

	// kubectl should be allowed
	reqKubectl := NewToolRequest("kubectl", CategoryExec).
		WithContext("", "", "deploy-bot", "")
	decisionKubectl := engine.Evaluate(ctx, reqKubectl)

	if decisionKubectl.Behavior != BehaviorAllow {
		t.Errorf("kubectl should be allowed for deploy-bot, got %s", decisionKubectl.Behavior)
	}

	// bash should be denied
	reqBash := NewToolRequest("bash", CategoryExec).
		WithContent("echo hello").
		WithContext("", "", "deploy-bot", "")
	decisionBash := engine.Evaluate(ctx, reqBash)

	if decisionBash.Behavior != BehaviorDeny {
		t.Errorf("bash should be denied for deploy-bot, got %s", decisionBash.Behavior)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Version != "1" {
		t.Errorf("expected version 1, got %s", cfg.Version)
	}
	if cfg.DefaultProfile != "standard" {
		t.Errorf("expected default_profile standard, got %s", cfg.DefaultProfile)
	}
	if cfg.Agents == nil {
		t.Error("expected non-nil agents map")
	}
}

func TestSaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".metiq", "permissions.json")

	cfg := &Config{
		Version:        "1",
		DefaultProfile: "permissive",
		Agents: map[string]AgentConfig{
			"test-agent": {Profile: "autonomous"},
		},
	}

	if err := SaveConfig(cfg, configPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Reload and verify
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}

	if loaded.DefaultProfile != "permissive" {
		t.Errorf("expected permissive, got %s", loaded.DefaultProfile)
	}
}
