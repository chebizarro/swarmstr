// Package permissions provides a unified permission engine for tool execution.
//
// The permission engine governs all tool execution including built-in tools,
// plugin tools, MCP tools, sandbox/exec calls, network fetches, file writes,
// and remote agent actions.
//
// Key concepts:
//   - Rules: Specify allow/ask/deny behaviors for tool patterns
//   - Scopes: Layered evaluation (global → user → project → agent → session)
//   - Classification: Automatic tool risk assessment
//   - Audit: Comprehensive logging of permission decisions
package permissions

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ─── Permission Behaviors ────────────────────────────────────────────────────

// Behavior defines how a permission rule affects tool execution.
type Behavior string

const (
	// BehaviorAllow permits the operation without prompting.
	BehaviorAllow Behavior = "allow"
	// BehaviorAsk requires user confirmation before proceeding.
	BehaviorAsk Behavior = "ask"
	// BehaviorDeny blocks the operation entirely.
	BehaviorDeny Behavior = "deny"
)

// IsValid reports whether the behavior is a recognized value.
func (b Behavior) IsValid() bool {
	switch b {
	case BehaviorAllow, BehaviorAsk, BehaviorDeny:
		return true
	default:
		return false
	}
}

// Priority returns the precedence of this behavior (higher = takes precedence).
// deny > ask > allow
func (b Behavior) Priority() int {
	switch b {
	case BehaviorDeny:
		return 3
	case BehaviorAsk:
		return 2
	case BehaviorAllow:
		return 1
	default:
		return 0
	}
}

// ─── Permission Scopes ───────────────────────────────────────────────────────

// Scope defines the layer at which a permission rule is defined.
// Scopes are evaluated in order, with later scopes taking precedence.
type Scope string

const (
	// ScopeGlobal applies to all users and projects (system defaults).
	ScopeGlobal Scope = "global"
	// ScopeUser applies to a specific user across all projects.
	ScopeUser Scope = "user"
	// ScopeProject applies to a specific project for all users.
	ScopeProject Scope = "project"
	// ScopeAgent applies to a specific agent instance.
	ScopeAgent Scope = "agent"
	// ScopeSession applies only to the current session.
	ScopeSession Scope = "session"
)

// AllScopes returns all scopes in evaluation order (lowest to highest precedence).
func AllScopes() []Scope {
	return []Scope{ScopeGlobal, ScopeUser, ScopeProject, ScopeAgent, ScopeSession}
}

// Precedence returns the evaluation order (higher = takes precedence).
func (s Scope) Precedence() int {
	switch s {
	case ScopeGlobal:
		return 1
	case ScopeUser:
		return 2
	case ScopeProject:
		return 3
	case ScopeAgent:
		return 4
	case ScopeSession:
		return 5
	default:
		return 0
	}
}

// IsValid reports whether the scope is a recognized value.
func (s Scope) IsValid() bool {
	return s.Precedence() > 0
}

// ─── Tool Categories ─────────────────────────────────────────────────────────

// ToolCategory classifies tools by their general function/capability. It should
// not be used to encode where a tool came from; use ToolOrigin for provenance.
type ToolCategory string

const (
	// CategoryBuiltin covers built-in agent tools.
	CategoryBuiltin ToolCategory = "builtin"
	// CategoryPlugin is retained for backwards compatibility. Prefer ToolOriginPlugin for provenance.
	CategoryPlugin ToolCategory = "plugin"
	// CategoryMCP is retained for backwards compatibility. Prefer ToolOriginMCP for provenance.
	CategoryMCP ToolCategory = "mcp"
	// CategoryExec covers shell/command execution.
	CategoryExec ToolCategory = "exec"
	// CategoryNetwork covers network/HTTP operations.
	CategoryNetwork ToolCategory = "network"
	// CategoryFilesystem covers file read/write operations.
	CategoryFilesystem ToolCategory = "filesystem"
	// CategoryRemoteAgent covers remote agent interactions.
	CategoryRemoteAgent ToolCategory = "remote_agent"
)

// ToolOrigin identifies where a tool descriptor originated, independently from
// its capability category. Empty means unspecified/any when used on a rule.
type ToolOrigin string

const (
	// ToolOriginBuiltin covers built-in agent tools.
	ToolOriginBuiltin ToolOrigin = "builtin"
	// ToolOriginPlugin covers plugin-provided tools.
	ToolOriginPlugin ToolOrigin = "plugin"
	// ToolOriginMCP covers tools surfaced by MCP servers.
	ToolOriginMCP ToolOrigin = "mcp"
)

// IsValid reports whether the origin is recognized. Empty means unspecified.
func (o ToolOrigin) IsValid() bool {
	switch o {
	case "", ToolOriginBuiltin, ToolOriginPlugin, ToolOriginMCP:
		return true
	default:
		return false
	}
}

// ─── Permission Rules ────────────────────────────────────────────────────────

// Rule defines a permission rule for tool execution.
type Rule struct {
	// ID is a unique identifier for this rule.
	ID string `json:"id"`

	// Scope is the layer at which this rule applies.
	Scope Scope `json:"scope"`

	// Behavior is the action to take (allow/ask/deny).
	Behavior Behavior `json:"behavior"`

	// ToolPattern is a pattern matching tool names (glob-style with * and ?).
	// Examples: "bash", "mcp:*", "plugin:github:*"
	ToolPattern string `json:"tool_pattern"`

	// ContentPattern optionally matches the tool input/arguments.
	// If empty, matches any content.
	ContentPattern string `json:"content_pattern,omitempty"`

	// Category optionally restricts to a specific tool capability category.
	Category ToolCategory `json:"category,omitempty"`

	// Origin optionally restricts to a tool provenance kind (builtin/plugin/mcp).
	Origin ToolOrigin `json:"origin,omitempty"`

	// OriginName optionally restricts to a provenance-specific source name. For
	// MCP tools this is the server name; for plugin tools this is the plugin ID.
	// Glob-style * and ? wildcards are supported.
	OriginName string `json:"origin_name,omitempty"`

	// Description explains the purpose of this rule.
	Description string `json:"description,omitempty"`

	// CreatedAt is when the rule was created.
	CreatedAt time.Time `json:"created_at"`

	// CreatedBy identifies who created the rule.
	CreatedBy string `json:"created_by,omitempty"`

	// ExpiresAt optionally sets when the rule expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// Enabled controls whether the rule is active.
	Enabled bool `json:"enabled"`

	// AgentID restricts the rule to a specific agent (empty = all agents).
	AgentID string `json:"agent_id,omitempty"`

	// Compiled patterns (not serialized)
	toolRegex       *regexp.Regexp
	contentRegex    *regexp.Regexp
	originNameRegex *regexp.Regexp
}

// ForAgent restricts the rule to a specific agent.
func (r *Rule) ForAgent(agentID string) *Rule {
	r.AgentID = agentID
	return r
}

// NewRule creates a new permission rule.
func NewRule(id string, scope Scope, behavior Behavior, toolPattern string) *Rule {
	return &Rule{
		ID:          id,
		Scope:       scope,
		Behavior:    behavior,
		ToolPattern: toolPattern,
		CreatedAt:   time.Now(),
		Enabled:     true,
	}
}

// WithContentPattern adds a content pattern to the rule.
func (r *Rule) WithContentPattern(pattern string) *Rule {
	r.ContentPattern = pattern
	return r
}

// WithCategory restricts the rule to a specific tool capability category.
func (r *Rule) WithCategory(cat ToolCategory) *Rule {
	r.Category = cat
	return r
}

// WithOrigin restricts the rule to a specific tool provenance kind.
func (r *Rule) WithOrigin(origin ToolOrigin) *Rule {
	r.Origin = origin
	return r
}

// WithOriginName restricts the rule to a provenance source name pattern.
func (r *Rule) WithOriginName(pattern string) *Rule {
	r.OriginName = pattern
	return r
}

// WithDescription adds a description to the rule.
func (r *Rule) WithDescription(desc string) *Rule {
	r.Description = desc
	return r
}

// WithExpiry sets an expiration time for the rule.
func (r *Rule) WithExpiry(t time.Time) *Rule {
	r.ExpiresAt = &t
	return r
}

// WithCreatedBy sets the creator of the rule.
func (r *Rule) WithCreatedBy(creator string) *Rule {
	r.CreatedBy = creator
	return r
}

// Compile prepares the rule for matching by compiling patterns.
func (r *Rule) Compile() error {
	if !r.Origin.IsValid() {
		return fmt.Errorf("invalid origin %q", r.Origin)
	}

	// Convert glob pattern to regex
	toolRegexStr := globToRegex(r.ToolPattern)
	re, err := regexp.Compile("^" + toolRegexStr + "$")
	if err != nil {
		return fmt.Errorf("invalid tool pattern %q: %w", r.ToolPattern, err)
	}
	r.toolRegex = re

	// Compile content pattern if present
	if r.ContentPattern != "" {
		cre, err := regexp.Compile(r.ContentPattern)
		if err != nil {
			return fmt.Errorf("invalid content pattern %q: %w", r.ContentPattern, err)
		}
		r.contentRegex = cre
	}

	// Compile origin-name pattern if present.
	if r.OriginName != "" {
		originRegexStr := globToRegex(r.OriginName)
		re, err := regexp.Compile("^" + originRegexStr + "$")
		if err != nil {
			return fmt.Errorf("invalid origin_name pattern %q: %w", r.OriginName, err)
		}
		r.originNameRegex = re
	}

	return nil
}

// IsExpired reports whether the rule has expired.
func (r *Rule) IsExpired() bool {
	if r.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*r.ExpiresAt)
}

// IsActive reports whether the rule is enabled and not expired.
func (r *Rule) IsActive() bool {
	return r.Enabled && !r.IsExpired()
}

// Matches checks if the rule matches a tool execution request.
func (r *Rule) Matches(req *ToolRequest) bool {
	if !r.IsActive() {
		return false
	}

	// Compile if needed
	if r.toolRegex == nil {
		if err := r.Compile(); err != nil {
			return false
		}
	}

	// Check agent restriction - if rule is for a specific agent, request must match
	if r.AgentID != "" && r.AgentID != req.AgentID {
		return false
	}

	// Check capability category if specified.
	if r.Category != "" && r.Category != req.Category {
		return false
	}

	// Check provenance kind if specified.
	if r.Origin != "" && r.Origin != req.Origin {
		return false
	}

	// Check provenance source name if specified.
	if r.OriginName != "" {
		if r.originNameRegex == nil {
			originRegexStr := globToRegex(r.OriginName)
			re, err := regexp.Compile("^" + originRegexStr + "$")
			if err != nil {
				return false
			}
			r.originNameRegex = re
		}
		if !r.originNameRegex.MatchString(req.OriginName) {
			return false
		}
	}

	// Check tool pattern
	if !r.toolRegex.MatchString(req.ToolName) {
		return false
	}

	// Check content pattern if specified
	// If ContentPattern is set but contentRegex is nil, we need to compile it
	if r.ContentPattern != "" {
		if r.contentRegex == nil {
			// This shouldn't happen if Compile() was called, but handle it
			cre, err := regexp.Compile(r.ContentPattern)
			if err != nil {
				return false
			}
			r.contentRegex = cre
		}
		if !r.contentRegex.MatchString(req.Content) {
			return false
		}
	}

	return true
}

// ─── Tool Request ────────────────────────────────────────────────────────────

// ToolRequest represents a request to execute a tool.
type ToolRequest struct {
	// ToolName is the full name of the tool.
	ToolName string `json:"tool_name"`

	// Category is the tool's capability category.
	Category ToolCategory `json:"category"`

	// Origin identifies where the tool came from, independently from Category.
	Origin ToolOrigin `json:"origin,omitempty"`

	// OriginName identifies the provenance-specific source name. For MCP tools
	// this is the server name; for plugin tools this is the plugin ID.
	OriginName string `json:"origin_name,omitempty"`

	// Content is the tool input/arguments as a string.
	Content string `json:"content,omitempty"`

	// Metadata contains additional tool-specific information.
	Metadata map[string]any `json:"metadata,omitempty"`

	// UserID identifies the requesting user.
	UserID string `json:"user_id,omitempty"`

	// ProjectID identifies the project context.
	ProjectID string `json:"project_id,omitempty"`

	// AgentID identifies the requesting agent.
	AgentID string `json:"agent_id,omitempty"`

	// SessionID identifies the current session.
	SessionID string `json:"session_id,omitempty"`

	// Timestamp is when the request was made.
	Timestamp time.Time `json:"timestamp"`
}

// NewToolRequest creates a new tool request.
func NewToolRequest(toolName string, category ToolCategory) *ToolRequest {
	return &ToolRequest{
		ToolName:  toolName,
		Category:  category,
		Timestamp: time.Now(),
		Metadata:  make(map[string]any),
	}
}

// WithContent adds content to the request.
func (r *ToolRequest) WithContent(content string) *ToolRequest {
	r.Content = content
	return r
}

// WithMetadata adds metadata to the request.
func (r *ToolRequest) WithMetadata(key string, value any) *ToolRequest {
	r.Metadata[key] = value
	return r
}

// WithOrigin sets the request provenance independently from capability category.
func (r *ToolRequest) WithOrigin(origin ToolOrigin, originName string) *ToolRequest {
	r.Origin = origin
	r.OriginName = originName
	return r
}

// WithContext sets the user, project, agent, and session IDs.
func (r *ToolRequest) WithContext(userID, projectID, agentID, sessionID string) *ToolRequest {
	r.UserID = userID
	r.ProjectID = projectID
	r.AgentID = agentID
	r.SessionID = sessionID
	return r
}

// ─── Permission Decision ─────────────────────────────────────────────────────

// Decision represents the result of permission evaluation.
type Decision struct {
	// Behavior is the final decision (allow/ask/deny).
	Behavior Behavior `json:"behavior"`

	// Reason explains why the decision was made.
	Reason string `json:"reason"`

	// MatchedRules lists the rules that contributed to the decision.
	MatchedRules []*Rule `json:"matched_rules,omitempty"`

	// Scope is the highest-precedence scope that contributed.
	Scope Scope `json:"scope,omitempty"`

	// Timestamp is when the decision was made.
	Timestamp time.Time `json:"timestamp"`

	// AuditID links to the audit log entry.
	AuditID string `json:"audit_id,omitempty"`
}

// IsAllowed reports whether the decision permits the operation.
func (d *Decision) IsAllowed() bool {
	return d.Behavior == BehaviorAllow
}

// RequiresConfirmation reports whether user confirmation is needed.
func (d *Decision) RequiresConfirmation() bool {
	return d.Behavior == BehaviorAsk
}

// IsDenied reports whether the operation is blocked.
func (d *Decision) IsDenied() bool {
	return d.Behavior == BehaviorDeny
}

// ─── Rule Set ────────────────────────────────────────────────────────────────

// RuleSet is a collection of rules organized by scope.
type RuleSet struct {
	rules map[Scope][]*Rule
}

// NewRuleSet creates an empty rule set.
func NewRuleSet() *RuleSet {
	return &RuleSet{
		rules: make(map[Scope][]*Rule),
	}
}

// AddRule adds a rule to the set.
func (rs *RuleSet) AddRule(rule *Rule) error {
	if err := rule.Compile(); err != nil {
		return err
	}
	rs.rules[rule.Scope] = append(rs.rules[rule.Scope], rule)
	return nil
}

// RemoveRule removes a rule by ID.
func (rs *RuleSet) RemoveRule(ruleID string) bool {
	for scope, rules := range rs.rules {
		for i, r := range rules {
			if r.ID == ruleID {
				rs.rules[scope] = append(rules[:i], rules[i+1:]...)
				return true
			}
		}
	}
	return false
}

// GetRule returns a rule by ID.
func (rs *RuleSet) GetRule(ruleID string) (*Rule, bool) {
	for _, rules := range rs.rules {
		for _, r := range rules {
			if r.ID == ruleID {
				return r, true
			}
		}
	}
	return nil, false
}

// RulesForScope returns all rules at a specific scope.
func (rs *RuleSet) RulesForScope(scope Scope) []*Rule {
	return rs.rules[scope]
}

// AllRules returns all rules in the set.
func (rs *RuleSet) AllRules() []*Rule {
	var all []*Rule
	for _, scope := range AllScopes() {
		all = append(all, rs.rules[scope]...)
	}
	return all
}

// MatchingRules returns all rules that match a request.
func (rs *RuleSet) MatchingRules(req *ToolRequest) []*Rule {
	var matches []*Rule
	for _, scope := range AllScopes() {
		for _, r := range rs.rules[scope] {
			if r.Matches(req) {
				matches = append(matches, r)
			}
		}
	}
	return matches
}

// MarshalJSON implements json.Marshaler.
func (rs *RuleSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(rs.rules)
}

// UnmarshalJSON implements json.Unmarshaler.
func (rs *RuleSet) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &rs.rules); err != nil {
		return err
	}
	// Compile all rules
	for _, rules := range rs.rules {
		for _, r := range rules {
			if err := r.Compile(); err != nil {
				return err
			}
		}
	}
	return nil
}

// ─── Helper Functions ────────────────────────────────────────────────────────

// globToRegex converts a glob pattern to a regex pattern.
func globToRegex(glob string) string {
	var result strings.Builder
	for _, ch := range glob {
		switch ch {
		case '*':
			result.WriteString(".*")
		case '?':
			result.WriteString(".")
		case '.', '+', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			result.WriteRune('\\')
			result.WriteRune(ch)
		default:
			result.WriteRune(ch)
		}
	}
	return result.String()
}
