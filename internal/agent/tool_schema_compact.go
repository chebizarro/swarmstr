package agent

import (
	"encoding/json"
	"sort"
)

// ─── Tool definition compression ─────────────────────────────────────────────
//
// For models with small context windows, tool definitions (serialized as JSON
// schemas in API calls) can consume a significant fraction of the available
// budget. This module provides budget-aware selection and compression of tool
// definitions.

// EstimateToolDefinitionChars returns a conservative character-count estimate
// for a ToolDefinition as it would appear in the JSON API request. This
// includes the name, description, and serialized parameter schema.
func EstimateToolDefinitionChars(def ToolDefinition) int {
	// Base: name + description + JSON overhead (~50 chars for wrapping)
	chars := len(def.Name) + len(def.Description) + 50

	// If we have a raw InputJSONSchema, estimate from its JSON serialization.
	if len(def.InputJSONSchema) > 0 {
		if b, err := json.Marshal(def.InputJSONSchema); err == nil {
			chars += len(b)
			return chars
		}
	}

	// Estimate from Parameters struct.
	for propName, prop := range def.Parameters.Properties {
		chars += len(propName) + len(prop.Type) + len(prop.Description) + 30
		for _, e := range prop.Enum {
			chars += len(e) + 3
		}
		if prop.Items != nil {
			chars += len(prop.Items.Type) + len(prop.Items.Description) + 20
		}
	}
	for _, req := range def.Parameters.Required {
		chars += len(req) + 3
	}
	return chars
}

// CompressToolDefinition strips optional fields from a definition to reduce
// its token footprint.
//
// For TierMicro: removes all parameter descriptions, keeps only name, type,
// and required fields. Also truncates the tool description to 80 chars.
//
// For TierSmall: truncates parameter descriptions to 40 chars and tool
// description to 150 chars.
//
// For TierStandard: returns the definition unchanged.
func CompressToolDefinition(def ToolDefinition, tier ContextTier) ToolDefinition {
	if tier == TierStandard {
		return def
	}

	compressed := ToolDefinition{
		Name:            def.Name,
		Description:     def.Description,
		InputJSONSchema: def.InputJSONSchema,
		Parameters: ToolParameters{
			Type:     def.Parameters.Type,
			Required: def.Parameters.Required,
		},
	}

	switch tier {
	case TierMicro:
		// Truncate tool description aggressively.
		compressed.Description = truncateStr(def.Description, 80)
		// Strip all parameter descriptions.
		if len(def.Parameters.Properties) > 0 {
			props := make(map[string]ToolParamProp, len(def.Parameters.Properties))
			for name, prop := range def.Parameters.Properties {
				props[name] = ToolParamProp{
					Type: prop.Type,
					Enum: prop.Enum,
				}
			}
			compressed.Parameters.Properties = props
		}
		// Clear InputJSONSchema for micro — we rely on Parameters only.
		compressed.InputJSONSchema = nil

	case TierSmall:
		compressed.Description = truncateStr(def.Description, 150)
		if len(def.Parameters.Properties) > 0 {
			props := make(map[string]ToolParamProp, len(def.Parameters.Properties))
			for name, prop := range def.Parameters.Properties {
				props[name] = ToolParamProp{
					Type:        prop.Type,
					Description: truncateStr(prop.Description, 40),
					Enum:        prop.Enum,
					Items:       prop.Items,
					Default:     prop.Default,
				}
			}
			compressed.Parameters.Properties = props
		}
	}

	return compressed
}

// FitToolDefinitions selects and optionally compresses tool definitions to fit
// within the budget's ToolDefsMax character ceiling. Critical tools are always
// included first. Remaining capacity is filled greedily (smallest first).
//
// Returns the selected (and possibly compressed) definitions. If the budget is
// zero or negative, all definitions are returned unchanged.
func FitToolDefinitions(defs []ToolDefinition, budget ContextBudget, criticalToolNames []string) []ToolDefinition {
	if budget.ToolDefsMax <= 0 || len(defs) == 0 {
		return defs
	}

	criticalSet := make(map[string]bool, len(criticalToolNames))
	for _, name := range criticalToolNames {
		criticalSet[name] = true
	}

	// Partition into critical and regular.
	var critical, regular []ToolDefinition
	for _, def := range defs {
		if criticalSet[def.Name] {
			critical = append(critical, def)
		} else {
			regular = append(regular, def)
		}
	}

	// Sort each partition by estimated size (smallest first).
	sortBySize := func(s []ToolDefinition) {
		sort.Slice(s, func(i, j int) bool {
			return EstimateToolDefinitionChars(s[i]) < EstimateToolDefinitionChars(s[j])
		})
	}
	sortBySize(critical)
	sortBySize(regular)

	// Compress if not standard tier.
	tier := budget.Profile.Tier
	compress := func(def ToolDefinition) ToolDefinition {
		return CompressToolDefinition(def, tier)
	}

	remaining := budget.ToolDefsMax
	var result []ToolDefinition

	// Always include critical tools first.
	for _, def := range critical {
		c := compress(def)
		cost := EstimateToolDefinitionChars(c)
		if remaining-cost < 0 && len(result) > 0 {
			// If even after compression a critical tool doesn't fit and we
			// already have at least one tool, skip to avoid zero-tool state.
			continue
		}
		result = append(result, c)
		remaining -= cost
	}

	// Fill remaining with regular tools.
	for _, def := range regular {
		c := compress(def)
		cost := EstimateToolDefinitionChars(c)
		if remaining-cost < 0 {
			break
		}
		result = append(result, c)
		remaining -= cost
	}

	return result
}

// DefaultCriticalToolNames returns the tool names that should always be
// included when fitting tool definitions for small models.
func DefaultCriticalToolNames() []string {
	return []string{
		"memory_search",
		"session_send",
		"session_spawn",
	}
}

// truncateStr truncates s to maxLen chars, appending "…" if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}
