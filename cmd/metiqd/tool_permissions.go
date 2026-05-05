package main

import (
	"strings"

	"metiq/internal/agent"
	"metiq/internal/permissions"
)

func permissionCategoryForTool(reg *agent.ToolRegistry, toolName string) permissions.ToolCategory {
	if reg != nil {
		if desc, ok := reg.Descriptor(toolName); ok {
			if desc.Traits.Destructive {
				// Category is capability-only. Do not put MCP/plugin provenance here:
				// external origin is carried separately by permissionOriginForTool.
				// Known-destructive descriptors are elevated into the high-risk exec bucket.
				return permissions.CategoryExec
			}
			if category := permissionCapabilityCategoryFromDescriptor(desc); category != "" {
				return category
			}
		}
	}
	// Empty category delegates to the permission engine's classifier. This keeps
	// builtin tool names such as bash/read_file/web_fetch categorized consistently
	// with direct Engine.Evaluate callers.
	return ""
}

func permissionCapabilityCategoryFromDescriptor(desc agent.ToolDescriptor) permissions.ToolCategory {
	switch desc.Origin.Kind {
	case agent.ToolOriginKindMCP, agent.ToolOriginKindPlugin:
		classifier := permissions.NewClassifier()
		for _, candidate := range []string{desc.Origin.CanonicalName, desc.Name} {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			category := classifier.Classify(candidate)
			switch category {
			case permissions.CategoryBuiltin, permissions.CategoryMCP, permissions.CategoryPlugin:
				continue
			default:
				return category
			}
		}
	}
	return ""
}

func permissionOriginForTool(reg *agent.ToolRegistry, toolName string) (permissions.ToolOrigin, string) {
	if reg == nil {
		return "", ""
	}
	desc, ok := reg.Descriptor(toolName)
	if !ok {
		return "", ""
	}
	switch desc.Origin.Kind {
	case agent.ToolOriginKindBuiltin:
		return permissions.ToolOriginBuiltin, ""
	case agent.ToolOriginKindMCP:
		return permissions.ToolOriginMCP, strings.TrimSpace(desc.Origin.ServerName)
	case agent.ToolOriginKindPlugin:
		return permissions.ToolOriginPlugin, strings.TrimSpace(desc.Origin.PluginID)
	default:
		return "", ""
	}
}

func toolProfileBypassesApproval(profile string, permissionsConfigured bool) bool {
	return !permissionsConfigured && strings.EqualFold(strings.TrimSpace(profile), "full")
}
