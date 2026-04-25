package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ─── Engine Configuration ────────────────────────────────────────────────────

// EngineConfig configures the permission engine.
type EngineConfig struct {
	// DefaultBehavior is the behavior when no rules match.
	DefaultBehavior Behavior `json:"default_behavior"`

	// AuditEnabled enables audit logging.
	AuditEnabled bool `json:"audit_enabled"`

	// AuditPath is the directory for audit logs.
	AuditPath string `json:"audit_path,omitempty"`

	// CacheEnabled enables decision caching.
	CacheEnabled bool `json:"cache_enabled"`

	// CacheTTL is how long cached decisions are valid.
	CacheTTL time.Duration `json:"cache_ttl"`

	// RulesPath is the directory for rule configuration files.
	RulesPath string `json:"rules_path,omitempty"`

	// AutoClassify enables automatic tool classification.
	AutoClassify bool `json:"auto_classify"`
}

// DefaultEngineConfig returns sensible defaults.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		DefaultBehavior: BehaviorAsk,
		AuditEnabled:    true,
		AuditPath:       "audit",
		CacheEnabled:    true,
		CacheTTL:        5 * time.Minute,
		AutoClassify:    true,
	}
}

// ─── Permission Engine ───────────────────────────────────────────────────────

// Engine evaluates permission rules and makes decisions.
type Engine struct {
	mu       sync.RWMutex
	cfg      EngineConfig
	baseDir  string
	ruleSet  *RuleSet
	auditor  *Auditor
	cache    map[string]*cachedDecision
	classify *Classifier
}

// cachedDecision holds a cached permission decision.
type cachedDecision struct {
	Decision  *Decision
	ExpiresAt time.Time
}

// NewEngine creates a new permission engine.
func NewEngine(baseDir string, cfg EngineConfig) *Engine {
	e := &Engine{
		cfg:     cfg,
		baseDir: baseDir,
		ruleSet: NewRuleSet(),
		cache:   make(map[string]*cachedDecision),
	}

	if cfg.AuditEnabled {
		e.auditor = NewAuditor(filepath.Join(baseDir, cfg.AuditPath))
	}

	if cfg.AutoClassify {
		e.classify = NewClassifier()
	}

	return e
}

// ─── Rule Management ─────────────────────────────────────────────────────────

// AddRule adds a permission rule.
func (e *Engine) AddRule(rule *Rule) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ruleSet.AddRule(rule); err != nil {
		return err
	}

	// Invalidate cache
	e.clearCache()

	// Audit rule addition
	if e.auditor != nil {
		e.auditor.LogEvent(AuditEvent{
			Type:      AuditEventRuleAdded,
			RuleID:    rule.ID,
			Timestamp: time.Now(),
			Details: map[string]any{
				"scope":        rule.Scope,
				"behavior":     rule.Behavior,
				"tool_pattern": rule.ToolPattern,
			},
		})
	}

	return nil
}

// RemoveRule removes a permission rule by ID.
func (e *Engine) RemoveRule(ruleID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	removed := e.ruleSet.RemoveRule(ruleID)
	if removed {
		e.clearCache()

		if e.auditor != nil {
			e.auditor.LogEvent(AuditEvent{
				Type:      AuditEventRuleRemoved,
				RuleID:    ruleID,
				Timestamp: time.Now(),
			})
		}
	}

	return removed
}

// GetRule returns a rule by ID.
func (e *Engine) GetRule(ruleID string) (*Rule, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ruleSet.GetRule(ruleID)
}

// ListRules returns all rules, optionally filtered by scope.
func (e *Engine) ListRules(scope Scope) []*Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if scope == "" {
		return e.ruleSet.AllRules()
	}
	return e.ruleSet.RulesForScope(scope)
}

// ─── Permission Evaluation ───────────────────────────────────────────────────

// Evaluate checks permissions for a tool request.
func (e *Engine) Evaluate(ctx context.Context, req *ToolRequest) *Decision {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Check cache
	cacheKey := e.cacheKey(req)
	if e.cfg.CacheEnabled {
		if cached := e.getCached(cacheKey); cached != nil {
			return cached
		}
	}

	// Auto-classify if needed
	if e.classify != nil && req.Category == "" {
		req.Category = e.classify.Classify(req.ToolName)
	}

	// Find matching rules
	matches := e.ruleSet.MatchingRules(req)

	// Make decision
	decision := e.makeDecision(req, matches)

	// Cache result
	if e.cfg.CacheEnabled {
		e.setCached(cacheKey, decision)
	}

	// Audit
	if e.auditor != nil {
		decision.AuditID = e.auditor.LogDecision(req, decision)
	}

	return decision
}

// makeDecision determines the final behavior based on matching rules.
func (e *Engine) makeDecision(req *ToolRequest, matches []*Rule) *Decision {
	decision := &Decision{
		Timestamp: time.Now(),
	}

	if len(matches) == 0 {
		// No rules match - use default behavior
		decision.Behavior = e.cfg.DefaultBehavior
		decision.Reason = "no matching rules; using default behavior"
		return decision
	}

	// Sort by scope precedence (higher first) then by behavior priority
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Scope.Precedence() != matches[j].Scope.Precedence() {
			return matches[i].Scope.Precedence() > matches[j].Scope.Precedence()
		}
		return matches[i].Behavior.Priority() > matches[j].Behavior.Priority()
	})

	// Take the highest precedence rule
	topRule := matches[0]
	decision.Behavior = topRule.Behavior
	decision.Scope = topRule.Scope
	decision.MatchedRules = matches
	decision.Reason = fmt.Sprintf("matched rule %q (scope: %s, pattern: %s)",
		topRule.ID, topRule.Scope, topRule.ToolPattern)

	// Check for conflicting rules at the same scope
	conflicting := e.findConflicts(matches)
	if len(conflicting) > 1 {
		// When there are conflicts at the same scope, deny takes precedence
		for _, r := range conflicting {
			if r.Behavior == BehaviorDeny {
				decision.Behavior = BehaviorDeny
				decision.Reason = fmt.Sprintf("conflicting rules resolved to deny (rules: %v)", ruleIDs(conflicting))
				break
			}
		}
	}

	return decision
}

// findConflicts returns rules at the highest precedence scope.
func (e *Engine) findConflicts(matches []*Rule) []*Rule {
	if len(matches) == 0 {
		return nil
	}

	topScope := matches[0].Scope
	var conflicts []*Rule
	for _, r := range matches {
		if r.Scope == topScope {
			conflicts = append(conflicts, r)
		}
	}
	return conflicts
}

// ─── Batch Operations ────────────────────────────────────────────────────────

// EvaluateBatch checks permissions for multiple requests.
func (e *Engine) EvaluateBatch(ctx context.Context, requests []*ToolRequest) []*Decision {
	decisions := make([]*Decision, len(requests))
	for i, req := range requests {
		decisions[i] = e.Evaluate(ctx, req)
	}
	return decisions
}

// ─── Cache Management ────────────────────────────────────────────────────────

func (e *Engine) cacheKey(req *ToolRequest) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		req.ToolName, req.Category, req.UserID, req.ProjectID, req.AgentID, req.SessionID)
}

func (e *Engine) getCached(key string) *Decision {
	cached, ok := e.cache[key]
	if !ok {
		return nil
	}
	if time.Now().After(cached.ExpiresAt) {
		delete(e.cache, key)
		return nil
	}
	return cached.Decision
}

func (e *Engine) setCached(key string, decision *Decision) {
	e.cache[key] = &cachedDecision{
		Decision:  decision,
		ExpiresAt: time.Now().Add(e.cfg.CacheTTL),
	}
}

func (e *Engine) clearCache() {
	e.cache = make(map[string]*cachedDecision)
}

// ClearCache invalidates all cached decisions.
func (e *Engine) ClearCache() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearCache()
}

// ─── Persistence ─────────────────────────────────────────────────────────────

// SaveRules persists all rules to disk.
func (e *Engine) SaveRules() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	rulesDir := filepath.Join(e.baseDir, e.cfg.RulesPath)
	if rulesDir == e.baseDir {
		rulesDir = filepath.Join(e.baseDir, "rules")
	}

	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return fmt.Errorf("create rules dir: %w", err)
	}

	// Save rules by scope
	for _, scope := range AllScopes() {
		rules := e.ruleSet.RulesForScope(scope)
		if len(rules) == 0 {
			continue
		}

		data, err := json.MarshalIndent(rules, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal %s rules: %w", scope, err)
		}

		path := filepath.Join(rulesDir, string(scope)+".json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("write %s rules: %w", scope, err)
		}
	}

	return nil
}

// LoadRules loads rules from disk.
func (e *Engine) LoadRules() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	rulesDir := filepath.Join(e.baseDir, e.cfg.RulesPath)
	if rulesDir == e.baseDir {
		rulesDir = filepath.Join(e.baseDir, "rules")
	}

	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read rules dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(rulesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var rules []*Rule
		if err := json.Unmarshal(data, &rules); err != nil {
			continue
		}

		for _, rule := range rules {
			e.ruleSet.AddRule(rule)
		}
	}

	return nil
}

// ─── Statistics ──────────────────────────────────────────────────────────────

// EngineStats provides statistics about the permission engine.
type EngineStats struct {
	TotalRules      int            `json:"total_rules"`
	RulesByScope    map[string]int `json:"rules_by_scope"`
	RulesByBehavior map[string]int `json:"rules_by_behavior"`
	CacheSize       int            `json:"cache_size"`
	AuditEntries    int64          `json:"audit_entries,omitempty"`
}

// Stats returns engine statistics.
func (e *Engine) Stats() EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := EngineStats{
		RulesByScope:    make(map[string]int),
		RulesByBehavior: make(map[string]int),
		CacheSize:       len(e.cache),
	}

	for _, scope := range AllScopes() {
		rules := e.ruleSet.RulesForScope(scope)
		stats.RulesByScope[string(scope)] = len(rules)
		stats.TotalRules += len(rules)

		for _, r := range rules {
			stats.RulesByBehavior[string(r.Behavior)]++
		}
	}

	if e.auditor != nil {
		stats.AuditEntries = e.auditor.EntryCount()
	}

	return stats
}

// ─── Helper Functions ────────────────────────────────────────────────────────

func ruleIDs(rules []*Rule) []string {
	ids := make([]string, len(rules))
	for i, r := range rules {
		ids[i] = r.ID
	}
	return ids
}

// ─── Default Rules ───────────────────────────────────────────────────────────

// DefaultGlobalRules returns sensible default global rules.
func DefaultGlobalRules() []*Rule {
	return []*Rule{
		// Allow read operations by default
		NewRule("global-allow-read", ScopeGlobal, BehaviorAllow, "*").
			WithCategory(CategoryFilesystem).
			WithContentPattern(`^read|^get|^list|^show`).
			WithDescription("Allow read-only filesystem operations"),

		// Ask for write operations
		NewRule("global-ask-write", ScopeGlobal, BehaviorAsk, "*").
			WithCategory(CategoryFilesystem).
			WithContentPattern(`^write|^create|^delete|^update`).
			WithDescription("Require confirmation for write operations"),

		// Ask for command execution
		NewRule("global-ask-exec", ScopeGlobal, BehaviorAsk, "bash").
			WithCategory(CategoryExec).
			WithDescription("Require confirmation for shell commands"),

		NewRule("global-ask-exec-cmd", ScopeGlobal, BehaviorAsk, "exec").
			WithCategory(CategoryExec).
			WithDescription("Require confirmation for command execution"),

		// Deny dangerous patterns
		NewRule("global-deny-rm-rf", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`rm\s+-rf\s+/`).
			WithDescription("Block recursive deletion from root"),

		NewRule("global-deny-sudo", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`^sudo\s+`).
			WithDescription("Block sudo commands"),

		// Ask for network operations
		NewRule("global-ask-network", ScopeGlobal, BehaviorAsk, "*").
			WithCategory(CategoryNetwork).
			WithDescription("Require confirmation for network operations"),

		// Ask for MCP tools
		NewRule("global-ask-mcp", ScopeGlobal, BehaviorAsk, "mcp:*").
			WithCategory(CategoryMCP).
			WithDescription("Require confirmation for MCP tools"),

		// Ask for remote agent operations
		NewRule("global-ask-remote", ScopeGlobal, BehaviorAsk, "*").
			WithCategory(CategoryRemoteAgent).
			WithDescription("Require confirmation for remote agent operations"),
	}
}

// LoadDefaultRules adds the default global rules to the engine.
func (e *Engine) LoadDefaultRules() error {
	for _, rule := range DefaultGlobalRules() {
		if err := e.AddRule(rule); err != nil {
			return err
		}
	}
	return nil
}
