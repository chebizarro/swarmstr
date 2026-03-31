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

// ─── Filtered executor ────────────────────────────────────────────────────────

// ProfileFilteredExecutor wraps a base ToolExecutor and rejects calls to
// tools not in the allowed set.  A nil allowed map passes all calls through.
type ProfileFilteredExecutor struct {
	Base    ToolExecutor
	Allowed map[string]bool // nil = allow all
}

func (e *ProfileFilteredExecutor) Execute(ctx context.Context, call ToolCall) (string, error) {
	if e.Allowed != nil && !e.Allowed[call.Name] {
		return "", fmt.Errorf("tool %q is not available in the current profile", call.Name)
	}
	return e.Base.Execute(ctx, call)
}

func (e *ProfileFilteredExecutor) Definitions() []ToolDefinition {
	provider, ok := e.Base.(interface{ Definitions() []ToolDefinition })
	if !ok {
		return nil
	}
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

func (e *ProfileFilteredExecutor) Descriptors() []ToolDescriptor {
	provider, ok := e.Base.(interface{ Descriptors() []ToolDescriptor })
	if !ok {
		return nil
	}
	descs := provider.Descriptors()
	if e.Allowed == nil {
		return descs
	}
	out := make([]ToolDescriptor, 0, len(descs))
	for _, desc := range descs {
		if e.Allowed[desc.Name] {
			out = append(out, desc)
		}
	}
	return out
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
