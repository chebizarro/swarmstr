// Package agent – tool profile definitions and profile-filtered executor.
//
// Profiles control which tools an agent is allowed to use in a given context.
// The four built-in profiles match the OpenClaw surface:
//
//	minimal   – identity + session status only
//	coding    – full filesystem, exec, memory, sessions, cron, image
//	messaging – sessions + messaging tools
//	full      – everything
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ─── Profile definitions ──────────────────────────────────────────────────────

// ProfileDef describes a tool-access profile.
type ProfileDef struct {
	ID          string
	Label       string
	Description string
	// MatchTool returns true when the given tool should be available in this profile.
	// toolID is the tool registry key; defaultProfiles is the list declared in the catalog.
	MatchTool func(toolID string, defaultProfiles []string) bool
}

// BuiltinProfiles is the ordered list of built-in profiles.
var BuiltinProfiles = []ProfileDef{
	{
		ID:          "minimal",
		Label:       "Minimal",
		Description: "Identity and session status only. No file, network, or exec access.",
		MatchTool: func(id string, defaults []string) bool {
			return profileContains(defaults, "minimal") || id == "session_status"
		},
	},
	{
		ID:          "coding",
		Label:       "Coding",
		Description: "Full coding surface: filesystem, exec, memory, sessions, web, cron, image tools.",
		MatchTool: func(id string, defaults []string) bool {
			return profileContains(defaults, "coding") || profileContains(defaults, "minimal")
		},
	},
	{
		ID:          "messaging",
		Label:       "Messaging",
		Description: "Session history, send/reply, and identity tools.",
		MatchTool: func(id string, defaults []string) bool {
			return profileContains(defaults, "messaging") || profileContains(defaults, "minimal")
		},
	},
	{
		ID:          "full",
		Label:       "Full",
		Description: "All available tools enabled.",
		MatchTool:   func(_ string, _ []string) bool { return true },
	},
}

// LookupProfile returns the ProfileDef for id, or nil if not found.
func LookupProfile(id string) *ProfileDef {
	id = strings.TrimSpace(strings.ToLower(id))
	for i := range BuiltinProfiles {
		if BuiltinProfiles[i].ID == id {
			return &BuiltinProfiles[i]
		}
	}
	return nil
}

// DefaultProfile is the profile used when none is configured.
const DefaultProfile = "full"

// ─── Catalog group filtering ──────────────────────────────────────────────────

// FilterCatalogByProfile returns a new groups slice containing only tools that
// are included in the given profile.  groups use the format returned by
// buildToolCatalogGroups.  Unknown profile IDs fall back to "full" (allow all).
func FilterCatalogByProfile(groups []map[string]any, profileID string) []map[string]any {
	profile := LookupProfile(profileID)
	if profile == nil {
		// Unknown profile → allow everything (safe degradation).
		return groups
	}

	result := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		rawTools, ok := group["tools"].([]map[string]any)
		if !ok {
			continue
		}
		filtered := make([]map[string]any, 0, len(rawTools))
		for _, tool := range rawTools {
			id, _ := tool["id"].(string)
			defaults := toStringSlice(tool["defaultProfiles"])
			if profile.MatchTool(id, defaults) {
				filtered = append(filtered, tool)
			}
		}
		if len(filtered) == 0 {
			continue
		}
		// Shallow copy group with filtered tools.
		g := make(map[string]any, len(group))
		for k, v := range group {
			g[k] = v
		}
		g["tools"] = filtered
		result = append(result, g)
	}
	return result
}

// AllowedToolIDs returns a set of tool IDs that are permitted by profileID.
// An empty profile ID means all tools are allowed.
func AllowedToolIDs(groups []map[string]any, profileID string) map[string]bool {
	filtered := FilterCatalogByProfile(groups, profileID)
	allowed := make(map[string]bool)
	for _, group := range filtered {
		rawTools, ok := group["tools"].([]map[string]any)
		if !ok {
			continue
		}
		for _, tool := range rawTools {
			if id, ok := tool["id"].(string); ok && id != "" {
				allowed[id] = true
			}
		}
	}
	return allowed
}

// AllowedToolIDsFromNames normalizes an explicit allowlist into a set while
// preserving nil as "no additional constraint".
func AllowedToolIDsFromNames(names []string) map[string]bool {
	names = NormalizeAllowedToolNames(names)
	if len(names) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	return allowed
}

// NormalizeAllowedToolNames trims, de-duplicates, and preserves order.
func NormalizeAllowedToolNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CloneAllowedToolIDs copies an allowlist set, preserving nil.
func CloneAllowedToolIDs(allowed map[string]bool) map[string]bool {
	if allowed == nil {
		return nil
	}
	out := make(map[string]bool, len(allowed))
	for name, ok := range allowed {
		if ok {
			out[name] = true
		}
	}
	return out
}

// IntersectAllowedToolIDs intersects two allowlist sets while preserving nil as
// "no additional constraint".
func IntersectAllowedToolIDs(left, right map[string]bool) map[string]bool {
	if left == nil {
		return CloneAllowedToolIDs(right)
	}
	if right == nil {
		return CloneAllowedToolIDs(left)
	}
	out := make(map[string]bool)
	for name := range left {
		if right[name] {
			out[name] = true
		}
	}
	return out
}

// ─── Filtered executor ────────────────────────────────────────────────────────

// ProfileFilteredExecutor wraps a base ToolExecutor and rejects calls to
// tools not in the allowed set.  A nil allowed map passes all calls through.
type ProfileFilteredExecutor struct {
	Base    ToolExecutor
	Allowed map[string]bool // nil = allow all
}

// FilteredToolExecutor returns base unchanged when no allowlist applies, or a
// profile-filtered wrapper otherwise.
func FilteredToolExecutor(base ToolExecutor, allowed map[string]bool) ToolExecutor {
	if base == nil || allowed == nil {
		return base
	}
	return &ProfileFilteredExecutor{Base: base, Allowed: allowed}
}

func (e *ProfileFilteredExecutor) Execute(ctx context.Context, call ToolCall) (string, error) {
	if e.Allowed != nil && !e.Allowed[call.Name] {
		return "", fmt.Errorf("tool %q is not available in the current profile", call.Name)
	}
	return e.Base.Execute(ctx, call)
}

func (e *ProfileFilteredExecutor) Definitions() []ToolDefinition {
	if provider, ok := e.Base.(interface{ ProviderDescriptors() []ToolDescriptor }); ok {
		return ToolDefinitionsFromDescriptors(AssembleToolDescriptors(provider.ProviderDescriptors(), e.Allowed))
	}
	if provider, ok := e.Base.(interface{ Descriptors() []ToolDescriptor }); ok {
		return ToolDefinitionsFromDescriptors(AssembleToolDescriptors(provider.Descriptors(), e.Allowed))
	}
	if provider, ok := e.Base.(interface{ Definitions() []ToolDefinition }); ok {
		defs := provider.Definitions()
		if e.Allowed == nil {
			return defs
		}
		out := make([]ToolDefinition, 0, len(defs))
		for _, def := range defs {
			if e.Allowed[def.Name] {
				out = append(out, def)
			}
		}
		return out
	}
	return nil
}

func (e *ProfileFilteredExecutor) Descriptors() []ToolDescriptor {
	if provider, ok := e.Base.(interface{ ProviderDescriptors() []ToolDescriptor }); ok {
		return AssembleToolDescriptors(provider.ProviderDescriptors(), e.Allowed)
	}
	if provider, ok := e.Base.(interface{ Descriptors() []ToolDescriptor }); ok {
		return AssembleToolDescriptors(provider.Descriptors(), e.Allowed)
	}
	return nil
}

func (e *ProfileFilteredExecutor) EffectiveTraits(call ToolCall) (ToolTraits, bool) {
	if e.Allowed != nil && !e.Allowed[call.Name] {
		return ToolTraits{}, false
	}
	provider, ok := e.Base.(interface {
		EffectiveTraits(ToolCall) (ToolTraits, bool)
	})
	if !ok {
		return ToolTraits{}, false
	}
	return provider.EffectiveTraits(call)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func profileContains(list []string, target string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), target) {
			return true
		}
	}
	return false
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ─── Profile metadata for API responses ──────────────────────────────────────

// ProfilesAsResponse converts BuiltinProfiles to the map format for API responses.
func ProfilesAsResponse() []map[string]any {
	out := make([]map[string]any, len(BuiltinProfiles))
	for i, p := range BuiltinProfiles {
		out[i] = map[string]any{
			"id":          p.ID,
			"label":       p.Label,
			"description": p.Description,
		}
	}
	return out
}

// ─── Per-agent profile storage ────────────────────────────────────────────────

// AgentProfileKey is the AgentDoc.Meta key used to store the active profile.
const AgentProfileKey = "tool_profile"

// ProfileListSorted returns all built-in profile IDs sorted.
func ProfileListSorted() []string {
	ids := make([]string, len(BuiltinProfiles))
	for i, p := range BuiltinProfiles {
		ids[i] = p.ID
	}
	sort.Strings(ids)
	return ids
}
