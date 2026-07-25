package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// AutonomousEngineConfig returns a configuration for maximum agent autonomy.
// All operations are allowed by default with only critical safety rules.
// Audit logging remains enabled for accountability.
func AutonomousEngineConfig() EngineConfig {
	return EngineConfig{
		DefaultBehavior: BehaviorAllow,
		AuditEnabled:    true,
		AuditPath:       "audit",
		CacheEnabled:    true,
		CacheTTL:        5 * time.Minute,
		AutoClassify:    true,
	}
}

// PermissiveEngineConfig returns a configuration that allows most operations
// but still asks for confirmation on dangerous commands.
func PermissiveEngineConfig() EngineConfig {
	return EngineConfig{
		DefaultBehavior: BehaviorAllow,
		AuditEnabled:    true,
		AuditPath:       "audit",
		CacheEnabled:    true,
		CacheTTL:        5 * time.Minute,
		AutoClassify:    true,
	}
}

// RestrictiveEngineConfig returns a configuration that denies by default,
// requiring explicit allow rules for each operation type.
func RestrictiveEngineConfig() EngineConfig {
	return EngineConfig{
		DefaultBehavior: BehaviorDeny,
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
	mu         sync.RWMutex
	cfg        EngineConfig
	baseDir    string
	ruleSet    *RuleSet
	auditor    *Auditor
	cache      map[string]*cachedDecision
	classify   *Classifier
	allowlists map[string]*agentAllowlist
}

// agentAllowlist is a restrictive per-agent tool allowlist. When configured for
// an agent, only tools whose name matches one of the compiled patterns (or
// whose capability category is explicitly allowed) may run; every other tool is
// denied before normal rule evaluation. This makes AllowedTools an exclusive
// allowlist rather than an additive set of allow rules.
type agentAllowlist struct {
	toolPatterns []*regexp.Regexp
	categories   map[ToolCategory]bool
}

// permits reports whether the request is admitted by the allowlist.
func (a *agentAllowlist) permits(req *ToolRequest) bool {
	if a == nil {
		return true
	}
	if req.Category != "" && a.categories[req.Category] {
		return true
	}
	for _, re := range a.toolPatterns {
		if re.MatchString(req.ToolName) {
			return true
		}
	}
	return false
}

// cachedDecision holds a cached permission decision.
type cachedDecision struct {
	Decision  *Decision
	ExpiresAt time.Time
}

// NewEngine creates a new permission engine.
func NewEngine(baseDir string, cfg EngineConfig) *Engine {
	e := &Engine{
		cfg:        cfg,
		baseDir:    baseDir,
		ruleSet:    NewRuleSet(),
		cache:      make(map[string]*cachedDecision),
		allowlists: make(map[string]*agentAllowlist),
	}

	if cfg.AuditEnabled {
		e.auditor = NewAuditor(filepath.Join(baseDir, cfg.AuditPath))
	}

	if cfg.AutoClassify {
		e.classify = NewClassifier()
	}

	return e
}

// AuditEnabled reports whether the engine is recording permission audit events.
func (e *Engine) AuditEnabled() bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.auditor != nil
}

// QueryAudit returns permission audit events matching opts. It returns an empty
// slice (no error) when auditing is disabled, so callers can surface an honest
// "no audit log configured" state without special-casing a nil engine.
func (e *Engine) QueryAudit(opts AuditQueryOptions) ([]AuditEvent, error) {
	if e == nil {
		return nil, nil
	}
	e.mu.RLock()
	auditor := e.auditor
	e.mu.RUnlock()
	if auditor == nil {
		return nil, nil
	}
	return auditor.Query(opts)
}

// ─── Rule Management ─────────────────────────────────────────────────────────

// AddRule adds a permission rule.
func (e *Engine) AddRule(rule *Rule) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ruleCopy := cloneRule(rule)
	if ruleCopy == nil {
		return fmt.Errorf("rule cannot be nil")
	}

	if err := e.ruleSet.AddRule(ruleCopy); err != nil {
		return err
	}

	// Invalidate cache
	e.clearCache()

	// Audit rule addition
	if e.auditor != nil {
		e.auditor.LogEvent(AuditEvent{
			Type:      AuditEventRuleAdded,
			RuleID:    ruleCopy.ID,
			Timestamp: time.Now(),
			Details: map[string]any{
				"scope":        ruleCopy.Scope,
				"behavior":     ruleCopy.Behavior,
				"tool_pattern": ruleCopy.ToolPattern,
			},
		})
	}

	return nil
}

// RemoveRule removes a permission rule by ID.
func (e *Engine) RemoveRule(ruleID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Immutable safety rules cannot be removed. This closes the removal path as a
	// way to neutralize the non-overridable critical-deny layer.
	if rule, ok := e.ruleSet.GetRule(ruleID); ok && rule.Immutable {
		return false
	}

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
	rule, ok := e.ruleSet.GetRule(ruleID)
	if !ok {
		return nil, false
	}
	return cloneRule(rule), true
}

// ListRules returns all rules, optionally filtered by scope.
func (e *Engine) ListRules(scope Scope) []*Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if scope == "" {
		return cloneRules(e.ruleSet.AllRules())
	}
	return cloneRules(e.ruleSet.RulesForScope(scope))
}

// ─── Permission Evaluation ───────────────────────────────────────────────────

// Evaluate checks permissions for a tool request.
func (e *Engine) Evaluate(ctx context.Context, req *ToolRequest) *Decision {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Work from an internal copy so classification and auditing never mutate
	// caller-owned request objects while another goroutine may still be using them.
	evalReq := cloneToolRequest(req)

	// Auto-classify before cache lookup so an explicit category and the same
	// auto-classified category share one cache key and rule path.
	if e.classify != nil && evalReq.Category == "" {
		evalReq.Category = e.classify.Classify(evalReq.ToolName)
	}

	// Check cache. Evaluate holds the write lock because cache lookup may purge
	// expired entries, and cache population must stay atomic with the rule snapshot
	// it was computed from.
	cacheKey := e.cacheKey(evalReq)
	if e.cfg.CacheEnabled {
		if cached := e.getCached(cacheKey); cached != nil {
			return cloneDecision(cached)
		}
	}

	// Find matching rules
	matches := e.ruleSet.MatchingRules(evalReq)

	// Make decision
	decision := e.makeDecision(evalReq, matches)

	// Audit before caching so cached decisions preserve the audit ID assigned to
	// the decision that populated the cache.
	if e.auditor != nil {
		decision.AuditID = e.auditor.LogDecision(evalReq, decision)
	}

	// Cache an immutable copy and return a fresh copy to prevent callers from
	// mutating cached/internal decision state.
	if e.cfg.CacheEnabled {
		e.setCached(cacheKey, cloneDecision(decision))
	}

	return cloneDecision(decision)
}

// EvaluatePreview resolves the decision behavior for a tool request WITHOUT
// recording an audit event and without touching the decision cache. It is for
// read-only introspection (e.g. tools.effective) that reports what the policy
// would do for many tools at once; using Evaluate there would flood the bounded
// audit log with synthetic "decision" entries for tools that were never run.
func (e *Engine) EvaluatePreview(req *ToolRequest) *Decision {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	evalReq := cloneToolRequest(req)
	if e.classify != nil && evalReq.Category == "" {
		evalReq.Category = e.classify.Classify(evalReq.ToolName)
	}
	matches := e.ruleSet.MatchingRules(evalReq)
	return cloneDecision(e.makeDecision(evalReq, matches))
}

// makeDecision determines the final behavior using the shared evaluator that
// also backs policy.ToolPolicy.
func (e *Engine) makeDecision(req *ToolRequest, matches []*Rule) *Decision {
	decision := &Decision{Timestamp: time.Now()}
	candidates := make([]EvaluationRule, len(matches))
	for i, rule := range matches {
		candidates[i] = EvaluationRule{
			ID:              rule.ID,
			Behavior:        rule.Behavior,
			ScopePrecedence: rule.Scope.Precedence(),
			Specificity:     permissionRuleSpecificity(rule),
			Immutable:       rule.Immutable,
		}
	}
	resolved := ResolveEvaluation(e.cfg.DefaultBehavior, candidates)
	orderedMatches := orderPermissionRules(matches, resolved.Order)

	// Immutable denies are resolved before the restrictive allowlist so the
	// decision retains the exact safety rule and scope that caused the denial.
	if resolved.ImmutableDeny {
		topRule := matches[resolved.Winner]
		decision.Behavior = BehaviorDeny
		decision.Scope = topRule.Scope
		decision.MatchedRules = orderedMatches
		decision.Reason = fmt.Sprintf("immutable safety rule %q denies this operation (non-overridable)", topRule.ID)
		return decision
	}

	// A per-agent allowlist is a separate admission boundary: admitted tools still
	// pass through the canonical rule evaluator, while omitted tools fail closed.
	if al := e.allowlists[req.AgentID]; al != nil && !al.permits(req) {
		decision.Behavior = BehaviorDeny
		decision.Reason = fmt.Sprintf("tool %q is not in the allowlist for agent %q", req.ToolName, req.AgentID)
		decision.MatchedRules = orderedMatches
		return decision
	}

	if !resolved.Matched {
		decision.Behavior = resolved.Behavior
		decision.Reason = "no matching rules; using default behavior"
		return decision
	}

	topRule := matches[resolved.Winner]
	decision.Behavior = resolved.Behavior
	decision.Scope = topRule.Scope
	decision.MatchedRules = orderedMatches
	decision.Reason = fmt.Sprintf("matched rule %q (scope: %s, pattern: %s)", topRule.ID, topRule.Scope, topRule.ToolPattern)
	return decision
}

func permissionRuleSpecificity(rule *Rule) int {
	specificity := 0
	if rule.AgentID != "" {
		specificity++
	}
	if rule.Category != "" {
		specificity++
	}
	if rule.Origin != "" {
		specificity++
	}
	if rule.OriginName != "" {
		specificity++
	}
	if rule.ContentPattern != "" {
		specificity++
	}
	if rule.ToolPattern != "" && rule.ToolPattern != "*" {
		specificity++
	}
	return specificity
}

func orderPermissionRules(rules []*Rule, order []int) []*Rule {
	if len(order) == 0 {
		return nil
	}
	ordered := make([]*Rule, 0, len(order))
	for _, index := range order {
		ordered = append(ordered, rules[index])
	}
	return ordered
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
	// Include content hash in cache key since content patterns affect matching
	contentHash := ""
	if req.Content != "" {
		// Use a simple hash to avoid very long keys
		h := 0
		for _, c := range req.Content {
			h = 31*h + int(c)
		}
		contentHash = fmt.Sprintf("%x", h)
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s:%s:%s:%s",
		req.ToolName, req.Category, req.Origin, req.OriginName, req.UserID, req.ProjectID, req.AgentID, req.SessionID, contentHash)
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

func cloneToolRequest(req *ToolRequest) *ToolRequest {
	if req == nil {
		return &ToolRequest{Metadata: make(map[string]any), Timestamp: time.Now()}
	}
	clone := *req
	if req.Metadata != nil {
		clone.Metadata = make(map[string]any, len(req.Metadata))
		for k, v := range req.Metadata {
			clone.Metadata[k] = v
		}
	} else {
		clone.Metadata = make(map[string]any)
	}
	return &clone
}

func cloneDecision(decision *Decision) *Decision {
	if decision == nil {
		return nil
	}
	clone := *decision
	clone.MatchedRules = cloneRules(decision.MatchedRules)
	return &clone
}

func cloneRules(rules []*Rule) []*Rule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]*Rule, 0, len(rules))
	for _, rule := range rules {
		if cloned := cloneRule(rule); cloned != nil {
			out = append(out, cloned)
		}
	}
	return out
}

func cloneRule(rule *Rule) *Rule {
	if rule == nil {
		return nil
	}
	clone := *rule
	if rule.ExpiresAt != nil {
		expiresAt := *rule.ExpiresAt
		clone.ExpiresAt = &expiresAt
	}
	return &clone
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

		// Ask for MCP-provenance tools without overloading capability category.
		NewRule("global-ask-mcp", ScopeGlobal, BehaviorAsk, "*").
			WithOrigin(ToolOriginMCP).
			WithDescription("Require confirmation for MCP tools"),

		// Backwards compatibility for legacy callers that still encode MCP
		// provenance in Category/ToolPattern instead of ToolRequest.Origin.
		NewRule("global-ask-mcp-category", ScopeGlobal, BehaviorAsk, "mcp:*").
			WithCategory(CategoryMCP).
			WithDescription("Require confirmation for legacy MCP-category tools"),

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

// ─── Pre-configured Engines ─────────────────────────────────────────────────

// NewAutonomousEngine creates an engine configured for maximum agent autonomy.
// All operations are allowed by default. Only critical safety rules are applied
// to prevent catastrophic operations (rm -rf /, format commands, etc.).
//
// Use this when:
//   - Running in a sandboxed environment
//   - The agent is trusted and well-tested
//   - You want minimal interruption for approvals
//
// Audit logging remains enabled for accountability.
func NewAutonomousEngine(baseDir string) *Engine {
	e := NewEngine(baseDir, AutonomousEngineConfig())
	e.LoadCriticalSafetyRules()
	return e
}

// NewPermissiveEngine creates an engine that allows most operations but
// asks for confirmation on potentially dangerous commands.
//
// Use this when:
//   - You trust the agent but want visibility into risky operations
//   - Running in a development environment
//   - You want a balance of autonomy and oversight
func NewPermissiveEngine(baseDir string) *Engine {
	e := NewEngine(baseDir, PermissiveEngineConfig())
	e.LoadPermissiveRules()
	return e
}

// NewRestrictiveEngine creates an engine that denies by default and requires
// explicit allow rules. This is the most secure configuration.
//
// Use this when:
//   - Running untrusted or new agents
//   - Operating in production environments
//   - Security is the top priority
func NewRestrictiveEngine(baseDir string) *Engine {
	e := NewEngine(baseDir, RestrictiveEngineConfig())
	e.LoadDefaultRules()
	return e
}

// NewStandardEngine creates an engine with balanced defaults - allows reads,
// asks for writes/execution, denies dangerous operations.
func NewStandardEngine(baseDir string) *Engine {
	e := NewEngine(baseDir, DefaultEngineConfig())
	e.LoadDefaultRules()
	return e
}

// ─── Rule Sets ───────────────────────────────────────────────────────────────

// CriticalSafetyRules returns rules that prevent catastrophic operations.
// These should be loaded even in autonomous mode.
func CriticalSafetyRules() []*Rule {
	return []*Rule{
		// Prevent recursive deletion from root
		NewRule("safety-deny-rm-rf-root", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`rm\s+(-[a-zA-Z]*r[a-zA-Z]*f|(-[a-zA-Z]*f[a-zA-Z]*r))\s+/[^.]`).
			WithDescription("Block recursive deletion from root filesystem").
			AsImmutable(),

		// Prevent disk formatting
		NewRule("safety-deny-mkfs", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`mkfs\s`).
			WithDescription("Block filesystem creation commands").
			AsImmutable(),

		NewRule("safety-deny-fdisk", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`fdisk\s`).
			WithDescription("Block partition table modifications").
			AsImmutable(),

		// Prevent direct disk writes
		NewRule("safety-deny-dd-disk", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`dd\s+.*of=/dev/[sh]d`).
			WithDescription("Block direct disk writes with dd").
			AsImmutable(),

		// Prevent wiping boot sectors
		NewRule("safety-deny-dd-zero", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`dd\s+.*if=/dev/zero.*of=/dev/`).
			WithDescription("Block zeroing disk devices").
			AsImmutable(),

		// Prevent chmod 777 on system directories
		NewRule("safety-deny-chmod-777-system", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`chmod\s+(-[a-zA-Z]*R[a-zA-Z]*)?\s*777\s+/`).
			WithDescription("Block recursive chmod 777 on root").
			AsImmutable(),

		// Prevent deleting critical system files
		NewRule("safety-deny-rm-etc", ScopeGlobal, BehaviorDeny, "bash").
			WithContentPattern(`rm\s+.*/(etc|boot|usr|lib|bin|sbin)/`).
			WithDescription("Block deletion of system directories").
			AsImmutable(),
	}
}

// LoadCriticalSafetyRules adds only the critical safety rules.
func (e *Engine) LoadCriticalSafetyRules() error {
	for _, rule := range CriticalSafetyRules() {
		if err := e.AddRule(rule); err != nil {
			return err
		}
	}
	return nil
}

// PermissiveRules returns rules for permissive mode - allows most things
// but asks for confirmation on dangerous patterns.
func PermissiveRules() []*Rule {
	rules := CriticalSafetyRules() // Start with safety rules as deny

	// Add ask rules for potentially risky operations
	askRules := []*Rule{
		// Ask before sudo
		NewRule("permissive-ask-sudo", ScopeGlobal, BehaviorAsk, "bash").
			WithContentPattern(`^sudo\s+`).
			WithDescription("Confirm sudo commands"),

		// Ask before curl/wget to shell
		NewRule("permissive-ask-curl-sh", ScopeGlobal, BehaviorAsk, "bash").
			WithContentPattern(`(curl|wget).*\|\s*(ba)?sh`).
			WithDescription("Confirm piping downloads to shell"),

		// Ask before modifying SSH keys
		NewRule("permissive-ask-ssh", ScopeGlobal, BehaviorAsk, "bash").
			WithContentPattern(`(>|>>)\s*~?/?.*\.ssh/`).
			WithDescription("Confirm SSH directory modifications"),

		// Ask before modifying shell configs
		NewRule("permissive-ask-shell-config", ScopeGlobal, BehaviorAsk, "bash").
			WithContentPattern(`(>|>>)\s*~?/?\.(bashrc|zshrc|profile|bash_profile)`).
			WithDescription("Confirm shell configuration changes"),

		// Ask before git push --force
		NewRule("permissive-ask-force-push", ScopeGlobal, BehaviorAsk, "bash").
			WithContentPattern(`git\s+push\s+.*--force`).
			WithDescription("Confirm force push"),

		// Ask before git reset --hard
		NewRule("permissive-ask-git-reset", ScopeGlobal, BehaviorAsk, "bash").
			WithContentPattern(`git\s+reset\s+--hard`).
			WithDescription("Confirm hard reset"),
	}

	return append(rules, askRules...)
}

// LoadPermissiveRules adds the permissive rule set.
func (e *Engine) LoadPermissiveRules() error {
	for _, rule := range PermissiveRules() {
		if err := e.AddRule(rule); err != nil {
			return err
		}
	}
	return nil
}

// ─── Session Override Helpers ────────────────────────────────────────────────

// AllowAllForSession adds a session-scope rule that allows all operations.
// This overrides any global/user/project rules for the current session.
func (e *Engine) AllowAllForSession() error {
	return e.AddRule(
		NewRule("session-allow-all", ScopeSession, BehaviorAllow, "*").
			WithDescription("Session override: allow all operations"),
	)
}

// AllowCategoryForSession adds a session-scope allow rule for a specific category.
func (e *Engine) AllowCategoryForSession(category ToolCategory) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("session-allow-%s", category), ScopeSession, BehaviorAllow, "*").
			WithCategory(category).
			WithDescription(fmt.Sprintf("Session override: allow all %s operations", category)),
	)
}

// AllowToolForSession adds a session-scope allow rule for a specific tool pattern.
func (e *Engine) AllowToolForSession(toolPattern string) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("session-allow-%s", toolPattern), ScopeSession, BehaviorAllow, toolPattern).
			WithDescription(fmt.Sprintf("Session override: allow %s", toolPattern)),
	)
}

// AllowCommandForSession adds a session-scope allow rule for a specific command pattern.
func (e *Engine) AllowCommandForSession(commandPattern string) error {
	return e.AddRule(
		NewRule("session-allow-cmd", ScopeSession, BehaviorAllow, "bash").
			WithContentPattern(commandPattern).
			WithDescription(fmt.Sprintf("Session override: allow commands matching %s", commandPattern)),
	)
}

// ─── Per-Agent Allowlist ─────────────────────────────────────────────────────

// SetAgentAllowlist installs a restrictive tool allowlist for a specific agent.
// When set, only tools whose name matches one of the given glob patterns, or
// whose capability category appears in categories, are permitted for that agent;
// every other tool is denied before normal rule evaluation. Passing empty tools
// and empty categories clears the allowlist for the agent (no restriction).
//
// The allowlist is necessary-but-not-sufficient: admitted tools still pass
// through the normal rule pipeline (which may still ask or deny), and immutable
// safety denies always win. This is how a state config's AllowedTools becomes an
// exclusive allowlist rather than an additive set of allow rules.
func (e *Engine) SetAgentAllowlist(agentID string, tools []string, categories []ToolCategory) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(tools) == 0 && len(categories) == 0 {
		delete(e.allowlists, agentID)
		e.clearCache()
		return nil
	}

	al := &agentAllowlist{categories: make(map[ToolCategory]bool, len(categories))}
	for _, pattern := range tools {
		re, err := regexp.Compile("^" + globToRegex(pattern) + "$")
		if err != nil {
			return fmt.Errorf("invalid allowlist pattern %q for agent %s: %w", pattern, agentID, err)
		}
		al.toolPatterns = append(al.toolPatterns, re)
	}
	for _, cat := range categories {
		if cat != "" {
			al.categories[cat] = true
		}
	}

	e.allowlists[agentID] = al
	e.clearCache()
	return nil
}

// ─── Per-Agent Configuration Helpers ─────────────────────────────────────────

// AllowAllForAgent adds a rule that allows all operations for a specific agent.
// This only affects requests with a matching AgentID.
func (e *Engine) AllowAllForAgent(agentID string) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("agent-%s-allow-all", agentID), ScopeAgent, BehaviorAllow, "*").
			ForAgent(agentID).
			WithDescription(fmt.Sprintf("Allow all operations for agent %s", agentID)),
	)
}

// DenyAllForAgent adds a rule that denies all operations for a specific agent.
// Useful for temporarily blocking a misbehaving agent.
func (e *Engine) DenyAllForAgent(agentID string) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("agent-%s-deny-all", agentID), ScopeAgent, BehaviorDeny, "*").
			ForAgent(agentID).
			WithDescription(fmt.Sprintf("Deny all operations for agent %s", agentID)),
	)
}

// AllowCategoryForAgent allows a specific category for a specific agent.
func (e *Engine) AllowCategoryForAgent(agentID string, category ToolCategory) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("agent-%s-allow-%s", agentID, category), ScopeAgent, BehaviorAllow, "*").
			ForAgent(agentID).
			WithCategory(category).
			WithDescription(fmt.Sprintf("Allow %s operations for agent %s", category, agentID)),
	)
}

// DenyCategoryForAgent denies a specific category for a specific agent.
func (e *Engine) DenyCategoryForAgent(agentID string, category ToolCategory) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("agent-%s-deny-%s", agentID, category), ScopeAgent, BehaviorDeny, "*").
			ForAgent(agentID).
			WithCategory(category).
			WithDescription(fmt.Sprintf("Deny %s operations for agent %s", category, agentID)),
	)
}

// AllowToolForAgent allows a specific tool pattern for a specific agent.
func (e *Engine) AllowToolForAgent(agentID, toolPattern string) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("agent-%s-allow-%s", agentID, toolPattern), ScopeAgent, BehaviorAllow, toolPattern).
			ForAgent(agentID).
			WithDescription(fmt.Sprintf("Allow %s for agent %s", toolPattern, agentID)),
	)
}

// AskForAgent requires confirmation for all operations from a specific agent.
func (e *Engine) AskForAgent(agentID string) error {
	return e.AddRule(
		NewRule(fmt.Sprintf("agent-%s-ask-all", agentID), ScopeAgent, BehaviorAsk, "*").
			ForAgent(agentID).
			WithDescription(fmt.Sprintf("Require confirmation for all operations from agent %s", agentID)),
	)
}

// ConfigureAgentProfile applies a predefined permission profile to an agent.
// Profiles: "autonomous", "permissive", "restrictive", "readonly"
func (e *Engine) ConfigureAgentProfile(agentID, profile string) error {
	switch profile {
	case "autonomous":
		// Allow all, rely on global safety rules
		return e.AllowAllForAgent(agentID)

	case "permissive":
		// Allow all, but ask for exec operations
		if err := e.AllowAllForAgent(agentID); err != nil {
			return err
		}
		return e.AddRule(
			NewRule(fmt.Sprintf("agent-%s-ask-exec", agentID), ScopeAgent, BehaviorAsk, "*").
				ForAgent(agentID).
				WithCategory(CategoryExec).
				WithDescription(fmt.Sprintf("Ask before exec for agent %s", agentID)),
		)

	case "restrictive":
		// Ask for everything by default
		return e.AskForAgent(agentID)

	case "readonly":
		// Allow reads, deny writes/exec
		if err := e.AllowCategoryForAgent(agentID, CategoryFilesystem); err != nil {
			return err
		}
		if err := e.DenyCategoryForAgent(agentID, CategoryExec); err != nil {
			return err
		}
		return e.AddRule(
			NewRule(fmt.Sprintf("agent-%s-deny-write", agentID), ScopeAgent, BehaviorDeny, "*").
				ForAgent(agentID).
				WithContentPattern(`^write|^create|^delete|^update`).
				WithDescription(fmt.Sprintf("Deny writes for readonly agent %s", agentID)),
		)

	default:
		return fmt.Errorf("unknown agent profile: %s", profile)
	}
}
