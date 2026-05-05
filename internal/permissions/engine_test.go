package permissions

import (
	"context"
	"sync"
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
	cfg.AutoClassify = false // Disable auto-classify to avoid side effects
	cfg.CacheEnabled = false // Disable caching for this test
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

func TestOriginFilterSeparatesProvenanceFromCapability(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.AutoClassify = false
	cfg.CacheEnabled = false
	cfg.DefaultBehavior = BehaviorAllow
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	if err := engine.AddRule(NewRule("ask-mcp", ScopeGlobal, BehaviorAsk, "*").WithOrigin(ToolOriginMCP)); err != nil {
		t.Fatalf("add origin rule: %v", err)
	}

	ctx := context.Background()
	mcpReq := NewToolRequest("github_search", CategoryNetwork).WithOrigin(ToolOriginMCP, "github")
	decision := engine.Evaluate(ctx, mcpReq)
	if decision.Behavior != BehaviorAsk {
		t.Fatalf("MCP provenance rule should ask without category=mcp, got %s", decision.Behavior)
	}

	if err := engine.AddRule(NewRule("deny-network", ScopeGlobal, BehaviorDeny, "*").WithCategory(CategoryNetwork)); err != nil {
		t.Fatalf("add category rule: %v", err)
	}
	decision = engine.Evaluate(ctx, mcpReq)
	if decision.Behavior != BehaviorDeny {
		t.Fatalf("capability deny should still match external tool, got %s", decision.Behavior)
	}
	if len(decision.MatchedRules) != 2 {
		t.Fatalf("expected both origin and capability rules to match, got %d", len(decision.MatchedRules))
	}
}

func TestOriginNamePattern(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.AutoClassify = false
	cfg.CacheEnabled = false
	cfg.DefaultBehavior = BehaviorAllow
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	rule := NewRule("deny-github-mcp", ScopeGlobal, BehaviorDeny, "*").
		WithOrigin(ToolOriginMCP).
		WithOriginName("github*")
	if err := engine.AddRule(rule); err != nil {
		t.Fatalf("add rule: %v", err)
	}

	ctx := context.Background()
	blocked := NewToolRequest("mcp_github_search", CategoryNetwork).WithOrigin(ToolOriginMCP, "github-enterprise")
	if decision := engine.Evaluate(ctx, blocked); decision.Behavior != BehaviorDeny {
		t.Fatalf("github MCP source should be denied, got %s", decision.Behavior)
	}

	allowed := NewToolRequest("mcp_docs_search", CategoryNetwork).WithOrigin(ToolOriginMCP, "docs")
	if decision := engine.Evaluate(ctx, allowed); decision.Behavior != BehaviorAllow {
		t.Fatalf("non-matching MCP source should use default allow, got %s", decision.Behavior)
	}
}

func TestCacheKeyIncludesOrigin(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.AutoClassify = false
	cfg.CacheEnabled = true
	cfg.CacheTTL = time.Minute
	cfg.DefaultBehavior = BehaviorAllow
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	if err := engine.AddRule(NewRule("ask-mcp", ScopeGlobal, BehaviorAsk, "*").WithOrigin(ToolOriginMCP)); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	ctx := context.Background()
	plain := NewToolRequest("shared_search", CategoryNetwork)
	if decision := engine.Evaluate(ctx, plain); decision.Behavior != BehaviorAllow {
		t.Fatalf("plain request should use default allow, got %s", decision.Behavior)
	}

	mcp := NewToolRequest("shared_search", CategoryNetwork).WithOrigin(ToolOriginMCP, "github")
	if decision := engine.Evaluate(ctx, mcp); decision.Behavior != BehaviorAsk {
		t.Fatalf("origin-specific request should not reuse plain cache entry, got %s", decision.Behavior)
	}
}

func TestDefaultRulesPreserveLegacyMCPCategoryMatch(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.AutoClassify = false
	cfg.CacheEnabled = false
	cfg.DefaultBehavior = BehaviorAllow
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)
	if err := engine.LoadDefaultRules(); err != nil {
		t.Fatalf("load default rules: %v", err)
	}

	legacyReq := NewToolRequest("mcp:github_search", CategoryMCP)
	decision := engine.Evaluate(context.Background(), legacyReq)
	if decision.Behavior != BehaviorAsk {
		t.Fatalf("legacy CategoryMCP request should still ask, got %s", decision.Behavior)
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

func TestEvaluateDoesNotMutateRequestWhenAutoClassifying(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.AutoClassify = true
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	req := NewToolRequest("bash", "").WithContent("echo ok")
	decision := engine.Evaluate(context.Background(), req)
	if decision.Behavior == "" {
		t.Fatal("expected a decision")
	}
	if req.Category != "" {
		t.Fatalf("Evaluate mutated caller request category to %q", req.Category)
	}
}

func TestEvaluateReturnsImmutableCachedDecision(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.CacheEnabled = true
	cfg.CacheTTL = time.Minute
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)
	if err := engine.AddRule(NewRule("allow-test", ScopeGlobal, BehaviorAllow, "test")); err != nil {
		t.Fatalf("add rule: %v", err)
	}

	req := NewToolRequest("test", CategoryBuiltin)
	decision1 := engine.Evaluate(context.Background(), req)
	if decision1.Behavior != BehaviorAllow || len(decision1.MatchedRules) != 1 {
		t.Fatalf("unexpected first decision: %+v", decision1)
	}

	decision1.Behavior = BehaviorDeny
	decision1.MatchedRules[0].Behavior = BehaviorDeny
	decision1.MatchedRules[0].ID = "mutated"

	decision2 := engine.Evaluate(context.Background(), req)
	if decision2.Behavior != BehaviorAllow {
		t.Fatalf("cached decision was externally mutated, got behavior %s", decision2.Behavior)
	}
	if len(decision2.MatchedRules) != 1 || decision2.MatchedRules[0].ID != "allow-test" || decision2.MatchedRules[0].Behavior != BehaviorAllow {
		t.Fatalf("cached matched rules were externally mutated: %+v", decision2.MatchedRules)
	}
}

func TestRuleInputsAndAccessorsAreImmutable(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.CacheEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	rule := NewRule("allow-test", ScopeGlobal, BehaviorAllow, "test")
	if err := engine.AddRule(rule); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	rule.Behavior = BehaviorDeny

	req := NewToolRequest("test", CategoryBuiltin)
	if got := engine.Evaluate(context.Background(), req); got.Behavior != BehaviorAllow {
		t.Fatalf("mutating original rule affected engine decision: %s", got.Behavior)
	}

	gotRule, ok := engine.GetRule("allow-test")
	if !ok {
		t.Fatal("expected rule")
	}
	gotRule.Behavior = BehaviorDeny
	if got := engine.Evaluate(context.Background(), req); got.Behavior != BehaviorAllow {
		t.Fatalf("mutating GetRule result affected engine decision: %s", got.Behavior)
	}

	listed := engine.ListRules(ScopeGlobal)
	if len(listed) != 1 {
		t.Fatalf("expected one listed rule, got %d", len(listed))
	}
	listed[0].Behavior = BehaviorDeny
	if got := engine.Evaluate(context.Background(), req); got.Behavior != BehaviorAllow {
		t.Fatalf("mutating ListRules result affected engine decision: %s", got.Behavior)
	}
}

func TestEvaluateConcurrentCacheAccess(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	cfg.CacheEnabled = true
	cfg.CacheTTL = time.Minute
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)
	if err := engine.AddRule(NewRule("allow-test", ScopeGlobal, BehaviorAllow, "test")); err != nil {
		t.Fatalf("add rule: %v", err)
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				decision := engine.Evaluate(ctx, NewToolRequest("test", CategoryBuiltin))
				if decision.Behavior != BehaviorAllow {
					t.Errorf("expected allow, got %s", decision.Behavior)
				}
			}
		}()
	}
	wg.Wait()
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

func TestPerAgentPermissions(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	ctx := context.Background()

	// Configure agent profiles
	engine.ConfigureAgentProfile("research-agent", "autonomous")
	engine.ConfigureAgentProfile("deploy-agent", "restrictive")

	// Research agent should be allowed
	reqResearch := NewToolRequest("bash", CategoryExec).
		WithContent("curl https://api.example.com").
		WithContext("", "", "research-agent", "")
	decisionResearch := engine.Evaluate(ctx, reqResearch)

	if decisionResearch.Behavior != BehaviorAllow {
		t.Errorf("research-agent should be allowed, got %s", decisionResearch.Behavior)
	}

	// Deploy agent should ask
	reqDeploy := NewToolRequest("bash", CategoryExec).
		WithContent("curl https://api.example.com").
		WithContext("", "", "deploy-agent", "")
	decisionDeploy := engine.Evaluate(ctx, reqDeploy)

	if decisionDeploy.Behavior != BehaviorAsk {
		t.Errorf("deploy-agent should ask, got %s", decisionDeploy.Behavior)
	}
}

func TestAgentSpecificRules(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	ctx := context.Background()

	// Add a rule that only applies to "trusted-agent"
	rule := NewRule("trusted-allow-bash", ScopeAgent, BehaviorAllow, "bash").
		ForAgent("trusted-agent")
	engine.AddRule(rule)

	// Trusted agent should match the rule
	reqTrusted := NewToolRequest("bash", CategoryExec).
		WithContent("ls -la").
		WithContext("", "", "trusted-agent", "")
	decisionTrusted := engine.Evaluate(ctx, reqTrusted)

	if decisionTrusted.Behavior != BehaviorAllow {
		t.Errorf("trusted-agent should be allowed, got %s", decisionTrusted.Behavior)
	}
	if len(decisionTrusted.MatchedRules) == 0 {
		t.Error("expected rule to match for trusted-agent")
	}

	// Other agent should NOT match the rule (falls back to default)
	reqOther := NewToolRequest("bash", CategoryExec).
		WithContent("ls -la").
		WithContext("", "", "other-agent", "")
	decisionOther := engine.Evaluate(ctx, reqOther)

	if len(decisionOther.MatchedRules) != 0 {
		t.Error("other-agent should not match agent-specific rule")
	}
}

func TestReadonlyAgentProfile(t *testing.T) {
	cfg := DefaultEngineConfig()
	cfg.AuditEnabled = false
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir, cfg)

	ctx := context.Background()

	// Configure readonly profile
	engine.ConfigureAgentProfile("readonly-agent", "readonly")

	// Should allow filesystem operations
	reqRead := NewToolRequest("read_file", CategoryFilesystem).
		WithContext("", "", "readonly-agent", "")
	decisionRead := engine.Evaluate(ctx, reqRead)

	if decisionRead.Behavior != BehaviorAllow {
		t.Errorf("readonly-agent should allow filesystem, got %s", decisionRead.Behavior)
	}

	// Should deny exec operations
	reqExec := NewToolRequest("bash", CategoryExec).
		WithContent("rm -rf /tmp/test").
		WithContext("", "", "readonly-agent", "")
	decisionExec := engine.Evaluate(ctx, reqExec)

	if decisionExec.Behavior != BehaviorDeny {
		t.Errorf("readonly-agent should deny exec, got %s", decisionExec.Behavior)
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
