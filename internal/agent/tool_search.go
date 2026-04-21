package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ─── ToolSearch built-in tool ─────────────────────────────────────────────────
//
// When deferred tool loading is active, this tool allows the model to discover
// and load deferred tools on demand. The model calls tool_search with a query
// and gets back matching tool names + descriptions. Found tools are then
// dynamically added to the tool list for subsequent LLM calls in the same
// agentic loop iteration.
//
// Usage modes (matching src/):
//   - Keyword search: "slack message" → find tools for slack messaging
//   - Direct select:  "select:web_fetch,web_search" → load specific tools
//
// Ported from src/utils/toolSearch.ts ToolSearchTool.

// ToolSearchResult is a single result from tool_search.
type ToolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolSearchDefinition returns the ToolDefinition for the tool_search tool.
// The description is dynamically generated to include the list of available
// deferred tools.
func ToolSearchDefinition(deferred *DeferredToolSet) ToolDefinition {
	desc := "Search for and load deferred tools. " +
		"Use keyword search to find tools, or 'select:tool_name' to load a specific tool by name."
	if deferred != nil && deferred.Count() > 0 {
		summaries := deferred.ListSummaries()
		desc += "\n\nAvailable deferred tools:\n" + strings.Join(summaries, "\n")
	}

	return ToolDefinition{
		Name:        ToolSearchToolName,
		Description: desc,
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"query": {
					Type:        "string",
					Description: "Search query (keywords or 'select:tool_name' for direct selection)",
				},
				"max_results": {
					Type:        "integer",
					Description: "Maximum results to return (default: 5)",
				},
			},
			Required: []string{"query"},
		},
	}
}

// ToolSearchFunc creates the executor function for the tool_search tool.
// It captures the deferred tool set and a callback to dynamically add
// discovered tools to the current turn's tool list.
func ToolSearchFunc(deferred *DeferredToolSet, onDiscover func([]ToolDefinition)) ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		query := ArgString(args, "query")
		if strings.TrimSpace(query) == "" {
			return "Error: query is required", nil
		}

		maxResults := ArgInt(args, "max_results", 5)
		if maxResults <= 0 {
			maxResults = 5
		}

		results := deferred.Search(query, maxResults)
		if len(results) == 0 {
			return fmt.Sprintf("No tools found matching %q", query), nil
		}

		// Collect discovered tool definitions for dynamic loading.
		var discovered []ToolDefinition
		var searchResults []ToolSearchResult
		for _, entry := range results {
			searchResults = append(searchResults, ToolSearchResult{
				Name:        entry.Name,
				Description: entry.Summary,
			})
			discovered = append(discovered, entry.Definition)
		}

		// Notify the caller to add these tools to the current turn.
		if onDiscover != nil && len(discovered) > 0 {
			onDiscover(discovered)
		}

		out, err := json.MarshalIndent(searchResults, "", "  ")
		if err != nil {
			return fmt.Sprintf("Found %d tools but failed to format results", len(results)), nil
		}
		return fmt.Sprintf("Found %d tool(s):\n%s\n\nThese tools are now available for use.", len(results), string(out)), nil
	}
}
