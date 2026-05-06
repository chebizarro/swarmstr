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

// CompressToolDefinitionByPressure strips optional fields from a definition
// based on a compression pressure gradient (0.0 = no compression, 1.0 = max).
//
// Pressure thresholds:
//   - < 0.05: no compression (return as-is)
//   - < 0.35: light — truncate description to 200ch, param descriptions to 60ch
//   - < 0.65: moderate — truncate description to 150ch, param descriptions to 40ch
//   - ≥ 0.65: aggressive — truncate description to 80ch, strip param descriptions,
//     clear InputJSONSchema
func CompressToolDefinitionByPressure(def ToolDefinition, pressure float64) ToolDefinition {
	if pressure < 0.05 {
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

	switch {
	case pressure >= 0.65:
		// Aggressive: strip param descriptions, minimal tool description.
		compressed.Description = truncateStr(def.Description, 80)
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
		compressed.InputJSONSchema = nil

	case pressure >= 0.35:
		// Moderate: truncate descriptions.
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

	default:
		// Light: gentle truncation.
		compressed.Description = truncateStr(def.Description, 200)
		if len(def.Parameters.Properties) > 0 {
			props := make(map[string]ToolParamProp, len(def.Parameters.Properties))
			for name, prop := range def.Parameters.Properties {
				props[name] = ToolParamProp{
					Type:        prop.Type,
					Description: truncateStr(prop.Description, 60),
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

// CompressToolDefinition is a backward-compatible wrapper that maps a
// ContextTier to a pressure value and delegates to CompressToolDefinitionByPressure.
func CompressToolDefinition(def ToolDefinition, tier ContextTier) ToolDefinition {
	var pressure float64
	switch tier {
	case TierMicro:
		pressure = 0.85
	case TierSmall:
		pressure = 0.50
	default:
		pressure = 0
	}
	return CompressToolDefinitionByPressure(def, pressure)
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

	// Compute compression pressure from the ratio of total tool chars to budget.
	totalEstChars := 0
	for _, def := range defs {
		totalEstChars += EstimateToolDefinitionChars(def)
	}
	pressure := CompressionPressure(budget.ToolDefsMax, totalEstChars)
	compress := func(def ToolDefinition) ToolDefinition {
		return CompressToolDefinitionByPressure(def, pressure)
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

	// Fill remaining with regular tools (respecting both char budget and count cap).
	maxCount := budget.MaxToolCount
	if maxCount <= 0 {
		maxCount = 200 // no cap
	}
	for _, def := range regular {
		if len(result) >= maxCount {
			break
		}
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
		// Tools required by test suite
		"bash_exec",
		"write_file",
		"nostr_watch",
		"nostr_unwatch",
		"nostr_watch_list",
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
