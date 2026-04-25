package permissions

import (
	"context"
	"testing"
	"time"
)

func TestNewEngine(t *testing.T) {
	cfg := DefaultEngineConfig()
	tmpDir := t.TempDir()

	engine := NewEngine(tmpDir, cfg)
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}

	stats := engine.Stats()
	if stats.TotalRules != 0 {
		t.Errorf("expected 0 rules, got %d", stats.TotalRules)
	}
}

func TestAddAndRemoveRule(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	rule := NewRule("test-rule", ScopeProject, BehaviorAllow, "test_tool")
	err := engine.AddRule(rule)
	if err != nil {
		t.Fatalf("failed to add rule: %v", err)
	}

	// Verify rule exists
	retrieved, ok := engine.GetRule("test-rule")
	if !ok {
		t.Fatal("expected to find rule")
	}
	if retrieved.ID != "test-rule" {
		t.Errorf("expected ID 'test-rule', got %q", retrieved.ID)
	}

	// Remove rule
	removed := engine.RemoveRule("test-rule")
	if !removed {
		t.Error("expected rule to be removed")
	}

	// Verify removed
	_, ok = engine.GetRule("test-rule")
	if ok {
		t.Error("expected rule to not exist after removal")
	}
}

func TestEvaluateNoRules(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.DefaultBehavior = BehaviorAsk
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	req := NewToolRequest("unknown_tool", CategoryBuiltin)
	ctx := context.Background()

	decision := engine.Evaluate(ctx, req)
	if decision.Behavior != BehaviorAsk {
		t.Errorf("expected ask behavior, got %s", decision.Behavior)
	}
	if decision.Reason == "" {
		t.Error("expected reason to be set")
	}
}

func TestEvaluateMatchingRule(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add allow rule for test_tool
	rule := NewRule("allow-test", ScopeProject, BehaviorAllow, "test_tool")
	engine.AddRule(rule)

	req := NewToolRequest("test_tool", CategoryBuiltin)
	ctx := context.Background()

	decision := engine.Evaluate(ctx, req)
	if decision.Behavior != BehaviorAllow {
		t.Errorf("expected allow behavior, got %s", decision.Behavior)
	}
	if len(decision.MatchedRules) != 1 {
		t.Errorf("expected 1 matched rule, got %d", len(decision.MatchedRules))
	}
}

func TestEvaluateWildcardPattern(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add rule with wildcard
	rule := NewRule("allow-mcp", ScopeGlobal, BehaviorAllow, "mcp:*")
	engine.AddRule(rule)

	// Test matching
	tests := []struct {
		toolName string
		expected bool
	}{
		{"mcp:server1", true},
		{"mcp:read_file", true},
		{"mcp:", true},
		{"not_mcp", false},
		{"bash", false},
	}

	ctx := context.Background()
	for _, tc := range tests {
		req := NewToolRequest(tc.toolName, CategoryMCP)
		decision := engine.Evaluate(ctx, req)

		matched := len(decision.MatchedRules) > 0
		if matched != tc.expected {
			t.Errorf("tool %q: expected matched=%v, got %v", tc.toolName, tc.expected, matched)
		}
	}
}

func TestScopePrecedence(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add conflicting rules at different scopes
	engine.AddRule(NewRule("global-deny", ScopeGlobal, BehaviorDeny, "bash"))
	engine.AddRule(NewRule("project-allow", ScopeProject, BehaviorAllow, "bash"))

	req := NewToolRequest("bash", CategoryExec)
	ctx := context.Background()

	decision := engine.Evaluate(ctx, req)

	// Project scope should take precedence over global
	if decision.Behavior != BehaviorAllow {
		t.Errorf("expected allow (project precedence), got %s", decision.Behavior)
	}
	if decision.Scope != ScopeProject {
		t.Errorf("expected scope project, got %s", decision.Scope)
	}
}

func TestBehaviorPriority(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add conflicting rules at same scope
	engine.AddRule(NewRule("allow-bash", ScopeProject, BehaviorAllow, "bash"))
	engine.AddRule(NewRule("deny-bash", ScopeProject, BehaviorDeny, "bash"))

	req := NewToolRequest("bash", CategoryExec)
	ctx := context.Background()

	decision := engine.Evaluate(ctx, req)

	// Deny should take precedence over allow at same scope
	if decision.Behavior != BehaviorDeny {
		t.Errorf("expected deny (behavior priority), got %s", decision.Behavior)
	}
}

func TestContentPattern(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.AutoClassify = false  // Disable auto-classify to avoid side effects
	cfg.CacheEnabled = false  // Disable caching for this test
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add rule with content pattern
	rule := NewRule("deny-rm-rf", ScopeGlobal, BehaviorDeny, "bash").
		WithContentPattern(`rm\s+-rf`)
	err := engine.AddRule(rule)
	if err != nil {
		t.Fatalf("failed to add rule: %v", err)
	}

	ctx := context.Background()

	// Should match
	req1 := NewToolRequest("bash", CategoryExec).WithContent("rm -rf /tmp/test")
	decision1 := engine.Evaluate(ctx, req1)
	if decision1.Behavior != BehaviorDeny {
		t.Errorf("expected deny for 'rm -rf', got %s (reason: %s)", decision1.Behavior, decision1.Reason)
	}

	// Should not match - content doesn't match pattern
	req2 := NewToolRequest("bash", CategoryExec).WithContent("ls -la")
	decision2 := engine.Evaluate(ctx, req2)
	if len(decision2.MatchedRules) != 0 {
		t.Error("expected no match for 'ls -la'")
	}
}

func TestCategoryFilter(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add rule with category filter
	rule := NewRule("allow-fs", ScopeGlobal, BehaviorAllow, "*").
		WithCategory(CategoryFilesystem)
	engine.AddRule(rule)

	ctx := context.Background()

	// Should match filesystem tools
	req1 := NewToolRequest("read_file", CategoryFilesystem)
	decision1 := engine.Evaluate(ctx, req1)
	if decision1.Behavior != BehaviorAllow {
		t.Errorf("expected allow for filesystem tool, got %s", decision1.Behavior)
	}

	// Should not match other categories
	req2 := NewToolRequest("bash", CategoryExec)
	decision2 := engine.Evaluate(ctx, req2)
	if len(decision2.MatchedRules) != 0 {
		t.Error("expected no match for exec tool")
	}
}

func TestRuleExpiry(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add expired rule
	expiredRule := NewRule("expired", ScopeGlobal, BehaviorAllow, "test").
		WithExpiry(time.Now().Add(-time.Hour))
	engine.AddRule(expiredRule)

	// Add active rule
	activeRule := NewRule("active", ScopeGlobal, BehaviorDeny, "test").
		WithExpiry(time.Now().Add(time.Hour))
	engine.AddRule(activeRule)

	req := NewToolRequest("test", CategoryBuiltin)
	ctx := context.Background()

	decision := engine.Evaluate(ctx, req)

	// Only active rule should match
	if decision.Behavior != BehaviorDeny {
		t.Errorf("expected deny (active rule), got %s", decision.Behavior)
	}
}

func TestCaching(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.CacheEnabled = true
	cfg.CacheTTL = time.Minute
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	rule := NewRule("allow-test", ScopeGlobal, BehaviorAllow, "test")
	engine.AddRule(rule)

	req := NewToolRequest("test", CategoryBuiltin)
	ctx := context.Background()

	// First evaluation
	decision1 := engine.Evaluate(ctx, req)

	// Second evaluation (should be cached)
	decision2 := engine.Evaluate(ctx, req)

	if decision1.Behavior != decision2.Behavior {
		t.Error("cached decision should match original")
	}

	// Verify cache size
	stats := engine.Stats()
	if stats.CacheSize == 0 {
		t.Error("expected non-zero cache size")
	}

	// Clear cache
	engine.ClearCache()
	stats = engine.Stats()
	if stats.CacheSize != 0 {
		t.Errorf("expected cache size 0 after clear, got %d", stats.CacheSize)
	}
}

func TestListRules(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	engine.AddRule(NewRule("global-1", ScopeGlobal, BehaviorAllow, "a"))
	engine.AddRule(NewRule("global-2", ScopeGlobal, BehaviorDeny, "b"))
	engine.AddRule(NewRule("project-1", ScopeProject, BehaviorAllow, "c"))

	// List all rules
	all := engine.ListRules("")
	if len(all) != 3 {
		t.Errorf("expected 3 rules, got %d", len(all))
	}

	// List by scope
	global := engine.ListRules(ScopeGlobal)
	if len(global) != 2 {
		t.Errorf("expected 2 global rules, got %d", len(global))
	}

	project := engine.ListRules(ScopeProject)
	if len(project) != 1 {
		t.Errorf("expected 1 project rule, got %d", len(project))
	}
}

func TestDefaultGlobalRules(t *testing.T) {
	rules := DefaultGlobalRules()
	if len(rules) == 0 {
		t.Error("expected default rules")
	}

	// Verify all rules compile
	for _, rule := range rules {
		if err := rule.Compile(); err != nil {
			t.Errorf("rule %s failed to compile: %v", rule.ID, err)
		}
	}
}

func TestLoadDefaultRules(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	err := engine.LoadDefaultRules()
	if err != nil {
		t.Fatalf("failed to load default rules: %v", err)
	}

	stats := engine.Stats()
	if stats.TotalRules == 0 {
		t.Error("expected rules after loading defaults")
	}
}

func TestSaveAndLoadRules(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add rules
	engine.AddRule(NewRule("rule-1", ScopeGlobal, BehaviorAllow, "test1"))
	engine.AddRule(NewRule("rule-2", ScopeProject, BehaviorDeny, "test2"))

	// Save
	err := engine.SaveRules()
	if err != nil {
		t.Fatalf("failed to save rules: %v", err)
	}

	// Create new engine and load
	engine2 := NewEngine(tmpDir, cfg)
	err = engine2.LoadRules()
	if err != nil {
		t.Fatalf("failed to load rules: %v", err)
	}

	// Verify loaded
	stats := engine2.Stats()
	if stats.TotalRules != 2 {
		t.Errorf("expected 2 rules after load, got %d", stats.TotalRules)
	}

	rule, ok := engine2.GetRule("rule-1")
	if !ok {
		t.Error("expected to find rule-1")
	}
	if rule.Behavior != BehaviorAllow {
		t.Errorf("expected allow behavior, got %s", rule.Behavior)
	}
}

func TestEvaluateBatch(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	engine.AddRule(NewRule("allow-read", ScopeGlobal, BehaviorAllow, "read*"))

	requests := []*ToolRequest{
		NewToolRequest("read_file", CategoryFilesystem),
		NewToolRequest("read_dir", CategoryFilesystem),
		NewToolRequest("write_file", CategoryFilesystem),
	}

	ctx := context.Background()
	decisions := engine.EvaluateBatch(ctx, requests)

	if len(decisions) != 3 {
		t.Fatalf("expected 3 decisions, got %d", len(decisions))
	}

	// First two should match
	if decisions[0].Behavior != BehaviorAllow {
		t.Errorf("expected allow for read_file, got %s", decisions[0].Behavior)
	}
	if decisions[1].Behavior != BehaviorAllow {
		t.Errorf("expected allow for read_dir, got %s", decisions[1].Behavior)
	}

	// Third should use default
	if len(decisions[2].MatchedRules) != 0 {
		t.Error("expected no match for write_file")
	}
}

func TestEngineStats(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	engine.AddRule(NewRule("r1", ScopeGlobal, BehaviorAllow, "a"))
	engine.AddRule(NewRule("r2", ScopeGlobal, BehaviorDeny, "b"))
	engine.AddRule(NewRule("r3", ScopeProject, BehaviorAsk, "c"))

	stats := engine.Stats()

	if stats.TotalRules != 3 {
		t.Errorf("expected 3 total rules, got %d", stats.TotalRules)
	}
	if stats.RulesByScope["global"] != 2 {
		t.Errorf("expected 2 global rules, got %d", stats.RulesByScope["global"])
	}
	if stats.RulesByScope["project"] != 1 {
		t.Errorf("expected 1 project rule, got %d", stats.RulesByScope["project"])
	}
	if stats.RulesByBehavior["allow"] != 1 {
		t.Errorf("expected 1 allow rule, got %d", stats.RulesByBehavior["allow"])
	}
}

func TestNewAutonomousEngine(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewAutonomousEngine(tmpDir)

	// Should have safety rules loaded
	stats := engine.Stats()
	if stats.TotalRules == 0 {
		t.Error("expected safety rules to be loaded")
	}

	// Default should be allow
	ctx := context.Background()
	req := NewToolRequest("some_tool", CategoryBuiltin)
	decision := engine.Evaluate(ctx, req)

	if decision.Behavior != BehaviorAllow {
		t.Errorf("expected allow by default, got %s", decision.Behavior)
	}

	// But dangerous commands should be denied
	req2 := NewToolRequest("bash", CategoryExec).WithContent("rm -rf /etc")
	decision2 := engine.Evaluate(ctx, req2)

	if decision2.Behavior != BehaviorDeny {
		t.Errorf("expected deny for dangerous command, got %s", decision2.Behavior)
	}
}

func TestNewPermissiveEngine(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewPermissiveEngine(tmpDir)
	engine.ClearCache() // Clear any cached decisions

	ctx := context.Background()

	// Normal commands should be allowed (default behavior)
	req := NewToolRequest("bash", CategoryExec).WithContent("ls -la")
	decision := engine.Evaluate(ctx, req)

	if decision.Behavior != BehaviorAllow {
		t.Errorf("expected allow for safe command, got %s", decision.Behavior)
	}

	// Sudo should ask (matched by permissive-ask-sudo rule)
	req2 := NewToolRequest("bash", CategoryExec).WithContent("sudo apt update")
	decision2 := engine.Evaluate(ctx, req2)

	// The rule should match and ask for confirmation
	if decision2.Behavior != BehaviorAsk {
		// Log the decision for debugging
		t.Logf("Decision: behavior=%s reason=%s matched=%d",
			decision2.Behavior, decision2.Reason, len(decision2.MatchedRules))
		for _, r := range decision2.MatchedRules {
			t.Logf("  Rule: %s pattern=%s content=%s", r.ID, r.ToolPattern, r.ContentPattern)
		}
		t.Errorf("expected ask for sudo, got %s", decision2.Behavior)
	}

	// Dangerous commands should be denied
	req3 := NewToolRequest("bash", CategoryExec).WithContent("rm -rf /etc")
	decision3 := engine.Evaluate(ctx, req3)

	if decision3.Behavior != BehaviorDeny {
		t.Errorf("expected deny for dangerous command, got %s", decision3.Behavior)
	}
}

func TestNewRestrictiveEngine(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewRestrictiveEngine(tmpDir)

	ctx := context.Background()

	// Unknown tools should be denied
	req := NewToolRequest("unknown_tool", CategoryBuiltin)
	decision := engine.Evaluate(ctx, req)

	// Either denied or matched by default rules
	if decision.Behavior == BehaviorAllow && len(decision.MatchedRules) == 0 {
		t.Error("restrictive engine should not allow unknown tools without rules")
	}
}

func TestAllowAllForSession(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add a global deny rule
	engine.AddRule(NewRule("global-deny", ScopeGlobal, BehaviorDeny, "test_tool"))

	ctx := context.Background()
	req := NewToolRequest("test_tool", CategoryBuiltin)

	// Should be denied
	decision1 := engine.Evaluate(ctx, req)
	if decision1.Behavior != BehaviorDeny {
		t.Errorf("expected deny before session override, got %s", decision1.Behavior)
	}

	// Add session override
	engine.ClearCache() // Clear cache before adding new rule
	engine.AllowAllForSession()

	// Should now be allowed (session overrides global)
	decision2 := engine.Evaluate(ctx, req)
	if decision2.Behavior != BehaviorAllow {
		t.Errorf("expected allow after session override, got %s", decision2.Behavior)
	}
}

func TestAllowCategoryForSession(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	// Add global ask rule for exec
	engine.AddRule(NewRule("global-ask-exec", ScopeGlobal, BehaviorAsk, "*").
		WithCategory(CategoryExec))

	ctx := context.Background()
	req := NewToolRequest("bash", CategoryExec)

	// Should ask
	decision1 := engine.Evaluate(ctx, req)
	if decision1.Behavior != BehaviorAsk {
		t.Errorf("expected ask before session override, got %s", decision1.Behavior)
	}

	// Allow exec for session
	engine.ClearCache()
	engine.AllowCategoryForSession(CategoryExec)

	// Should now be allowed
	decision2 := engine.Evaluate(ctx, req)
	if decision2.Behavior != BehaviorAllow {
		t.Errorf("expected allow after category override, got %s", decision2.Behavior)
	}
}

func TestCriticalSafetyRules(t *testing.T) {
	rules := CriticalSafetyRules()
	if len(rules) == 0 {
		t.Error("expected safety rules")
	}

	// All should be deny rules
	for _, r := range rules {
		if r.Behavior != BehaviorDeny {
			t.Errorf("safety rule %s should be deny, got %s", r.ID, r.Behavior)
		}
	}
}

func TestPermissiveRules(t *testing.T) {
	rules := PermissiveRules()
	if len(rules) == 0 {
		t.Error("expected permissive rules")
	}

	// Should have both deny and ask rules
	hasDeny := false
	hasAsk := false
	for _, r := range rules {
		if r.Behavior == BehaviorDeny {
			hasDeny = true
		}
		if r.Behavior == BehaviorAsk {
			hasAsk = true
		}
	}

	if !hasDeny {
		t.Error("expected deny rules in permissive set")
	}
	if !hasAsk {
		t.Error("expected ask rules in permissive set")
	}
}

func TestEngineConfigs(t *testing.T) {
	// Test all config constructors return valid configs
	configs := []struct {
		name string
		cfg  EngineConfig
	}{
		{"default", DefaultEngineConfig()},
		{"autonomous", AutonomousEngineConfig()},
		{"permissive", PermissiveEngineConfig()},
		{"restrictive", RestrictiveEngineConfig()},
	}

	for _, tc := range configs {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.cfg.DefaultBehavior.IsValid() {
				t.Errorf("%s config has invalid default behavior", tc.name)
			}
		})
	}

	// Verify specific defaults
	if DefaultEngineConfig().DefaultBehavior != BehaviorAsk {
		t.Error("default config should ask by default")
	}
	if AutonomousEngineConfig().DefaultBehavior != BehaviorAllow {
		t.Error("autonomous config should allow by default")
	}
	if RestrictiveEngineConfig().DefaultBehavior != BehaviorDeny {
		t.Error("restrictive config should deny by default")
	}
}
