package policy

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// ToolPolicyAction is the decision returned by the per-tool policy engine.
type ToolPolicyAction string

const (
	ToolPolicyAllow ToolPolicyAction = "allow"
	ToolPolicyAsk   ToolPolicyAction = "ask"
	ToolPolicyDeny  ToolPolicyAction = "deny"
)

// Valid reports whether a policy action is recognized.
func (a ToolPolicyAction) Valid() bool {
	switch a {
	case ToolPolicyAllow, ToolPolicyAsk, ToolPolicyDeny:
		return true
	default:
		return false
	}
}

func (a ToolPolicyAction) priority() int {
	switch a {
	case ToolPolicyDeny:
		return 3
	case ToolPolicyAsk:
		return 2
	case ToolPolicyAllow:
		return 1
	default:
		return 0
	}
}

// ToolPolicyRule defines a granular allow/ask/deny rule for one tool, a glob,
// or a named group such as group:fs, group:runtime, group:web, group:mcp.
type ToolPolicyRule struct {
	ID          string           `json:"id,omitempty"`
	ToolName    string           `json:"tool_name,omitempty"`
	Group       string           `json:"group,omitempty"`
	Action      ToolPolicyAction `json:"action"`
	AgentID     string           `json:"agent_id,omitempty"`
	Origin      string           `json:"origin,omitempty"`
	OriginName  string           `json:"origin_name,omitempty"`
	Description string           `json:"description,omitempty"`
	Enabled     *bool            `json:"enabled,omitempty"`
	CreatedAt   time.Time        `json:"created_at,omitempty"`
}

// ToolPolicyRequest describes a pending tool execution.
type ToolPolicyRequest struct {
	ToolName   string         `json:"tool_name"`
	AgentID    string         `json:"agent_id,omitempty"`
	Origin     string         `json:"origin,omitempty"`
	OriginName string         `json:"origin_name,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
}

// ToolPolicyDecision is the result of evaluating a per-tool policy.
type ToolPolicyDecision struct {
	Action       ToolPolicyAction `json:"action"`
	Reason       string           `json:"reason,omitempty"`
	MatchedRules []ToolPolicyRule `json:"matched_rules,omitempty"`
}

// ToolPolicy evaluates per-tool rules. It is deliberately small and independent
// from tool dispatch so callers can gate built-in, MCP, plugin, and sandbox tools
// from any execution path.
type ToolPolicy struct {
	Rules            []ToolPolicyRule    `json:"rules,omitempty"`
	DefaultAction    ToolPolicyAction    `json:"default_action,omitempty"`
	GroupDefinitions map[string][]string `json:"groups,omitempty"`
}

var defaultToolGroups = map[string][]string{
	"fs":         {"read_file", "write_file", "edit_file", "apply_patch", "apply_edits", "file_search", "workspace.*", "filesystem.*"},
	"filesystem": {"group:fs"},
	"runtime":    {"bash", "exec", "shell", "sandbox.run", "docker.*", "process.*"},
	"web":        {"web.*", "http.*", "fetch", "browser.*", "search", "url.fetch"},
	"network":    {"group:web", "mcp:*"},
	"mcp":        {"mcp:*", "mcp.*"},
	"plugin":     {"plugin:*", "plugin.*"},
}

// Evaluate applies enabled matching rules and resolves conflicts as
// deny > ask > allow > profile/default. Agent-scoped rules match only the named
// agent; global rules have an empty AgentID.
func (p ToolPolicy) Evaluate(req ToolPolicyRequest) ToolPolicyDecision {
	tool := strings.TrimSpace(req.ToolName)
	if tool == "" {
		return ToolPolicyDecision{Action: ToolPolicyDeny, Reason: "tool name is required"}
	}

	var matches []ToolPolicyRule
	for _, rule := range p.Rules {
		if !ruleEnabled(rule) || !rule.Action.Valid() || !p.ruleMatches(rule, req) {
			continue
		}
		matches = append(matches, rule)
	}
	if len(matches) == 0 {
		def := p.DefaultAction
		if !def.Valid() {
			def = ToolPolicyAllow
		}
		return ToolPolicyDecision{Action: def, Reason: "no matching per-tool policy rule; using default/profile behavior"}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Action.priority() != matches[j].Action.priority() {
			return matches[i].Action.priority() > matches[j].Action.priority()
		}
		// Prefer agent-scoped over global when the behavior priority is equal.
		if (matches[i].AgentID != "") != (matches[j].AgentID != "") {
			return matches[i].AgentID != ""
		}
		return matches[i].ID < matches[j].ID
	})
	winner := matches[0]
	return ToolPolicyDecision{
		Action:       winner.Action,
		Reason:       fmt.Sprintf("matched per-tool policy rule %q", firstNonEmptyString(winner.ID, winner.ToolName, winner.Group)),
		MatchedRules: matches,
	}
}

func (p ToolPolicy) ruleMatches(rule ToolPolicyRule, req ToolPolicyRequest) bool {
	if rule.AgentID != "" && rule.AgentID != req.AgentID {
		return false
	}
	if rule.Origin != "" && !strings.EqualFold(rule.Origin, req.Origin) {
		return false
	}
	if rule.OriginName != "" && !globMatch(rule.OriginName, req.OriginName) {
		return false
	}
	if rule.ToolName != "" && globMatch(rule.ToolName, req.ToolName) {
		return true
	}
	if rule.Group != "" && p.toolInGroup(req.ToolName, rule.Group, map[string]bool{}) {
		return true
	}
	return false
}

func (p ToolPolicy) toolInGroup(tool, group string, seen map[string]bool) bool {
	group = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(group)), "group:")
	if group == "" || seen[group] {
		return false
	}
	seen[group] = true
	patterns := append([]string{}, defaultToolGroups[group]...)
	if p.GroupDefinitions != nil {
		patterns = append(patterns, p.GroupDefinitions[group]...)
		patterns = append(patterns, p.GroupDefinitions["group:"+group]...)
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, "group:") {
			if p.toolInGroup(tool, pattern, seen) {
				return true
			}
			continue
		}
		if globMatch(pattern, tool) {
			return true
		}
	}
	return false
}

func ruleEnabled(rule ToolPolicyRule) bool {
	return rule.Enabled == nil || *rule.Enabled
}

func globMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	if pattern == "" || value == "" {
		return false
	}
	if pattern == "*" || strings.EqualFold(pattern, value) {
		return true
	}
	ok, err := path.Match(pattern, value)
	if err == nil && ok {
		return true
	}
	// Common policy shorthand: prefix.* should also match prefix:tool.
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(value, prefix+".") || strings.HasPrefix(value, prefix+":")
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "<unnamed>"
}
