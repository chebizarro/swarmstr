// Package toolbuiltin/file_edit provides a structured file editing tool:
//   - file_edit → search-and-replace or line-range edits without rewriting entire files
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"metiq/internal/agent"
)

// editOperation describes a single search-and-replace within a file.
type editOperation struct {
	Search  string `json:"search"`
	Replace string `json:"replace"`
	All     bool   `json:"all,omitempty"`
}

type fileEditResult struct {
	Path       string `json:"path"`
	Edits      int    `json:"edits_applied"`
	BytesBefore int   `json:"bytes_before"`
	BytesAfter  int   `json:"bytes_after"`
}

// FileEditTool returns a ToolFunc that performs search-and-replace edits on a file.
// Supports single or multiple operations in one call. Each operation finds
// exact text and replaces it, preserving the rest of the file.
func FileEditTool(opts FilesystemOpts) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		path := agent.ArgString(args, "path")
		if strings.TrimSpace(path) == "" {
			return "", fmt.Errorf("file_edit: 'path' is required")
		}
		resolved, err := opts.resolvePath(path)
		if err != nil {
			return "", fmt.Errorf("file_edit: %v", err)
		}
		path = resolved

		// Read current content.
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("file_edit: %v", err)
		}
		content := string(raw)
		bytesBefore := len(raw)

		// Parse edit operations.
		ops, err := parseEditOps(args)
		if err != nil {
			return "", fmt.Errorf("file_edit: %v", err)
		}
		if len(ops) == 0 {
			return "", fmt.Errorf("file_edit: at least one edit operation is required (provide 'search'+'replace' or 'edits' array)")
		}

		// Apply edits sequentially.
		editsApplied := 0
		for i, op := range ops {
			if op.Search == "" {
				return "", fmt.Errorf("file_edit: edit[%d] has empty 'search' string", i)
			}
			if !strings.Contains(content, op.Search) {
				return "", fmt.Errorf("file_edit: edit[%d] search text not found in file:\n---\n%.200s\n---", i, op.Search)
			}
			if op.All {
				count := strings.Count(content, op.Search)
				content = strings.ReplaceAll(content, op.Search, op.Replace)
				editsApplied += count
			} else {
				content = strings.Replace(content, op.Search, op.Replace, 1)
				editsApplied++
			}
		}

		// Write back.
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("file_edit: write: %v", err)
		}

		result := fileEditResult{
			Path:        path,
			Edits:       editsApplied,
			BytesBefore: bytesBefore,
			BytesAfter:  len(content),
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// parseEditOps extracts edit operations from tool args.
// Supports two modes:
//   - Single: args has "search" and "replace" strings
//   - Batch:  args has "edits" array of {search, replace, all?} objects
func parseEditOps(args map[string]any) ([]editOperation, error) {
	// Mode 1: Single edit via top-level search/replace.
	search := agent.ArgString(args, "search")
	replace, hasReplace := args["replace"]
	if search != "" && hasReplace {
		replaceStr := ""
		if s, ok := replace.(string); ok {
			replaceStr = s
		}
		allFlag := false
		if v, ok := args["all"].(bool); ok {
			allFlag = v
		}
		return []editOperation{{Search: search, Replace: replaceStr, All: allFlag}}, nil
	}

	// Mode 2: Batch edits array.
	editsRaw, ok := args["edits"]
	if !ok {
		if search != "" {
			return nil, fmt.Errorf("'replace' is required when 'search' is provided")
		}
		return nil, nil
	}

	editsSlice, ok := editsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("'edits' must be an array")
	}

	ops := make([]editOperation, 0, len(editsSlice))
	for _, item := range editsSlice {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("each edit must be an object with 'search' and 'replace'")
		}
		s, _ := m["search"].(string)
		r, _ := m["replace"].(string)
		a, _ := m["all"].(bool)
		ops = append(ops, editOperation{Search: s, Replace: r, All: a})
	}
	return ops, nil
}

var FileEditDef = agent.ToolDefinition{
	Name:        "file_edit",
	Description: "Edit a file by searching for exact text and replacing it, without rewriting the entire file. Supports single or batch edits. Each edit must match existing content exactly (whitespace-sensitive). Use for surgical code changes: fixing a line, renaming a variable, updating a config value, inserting code at a known location. Fails with a clear error if the search text is not found.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"path": {
				Type:        "string",
				Description: "Path to the file to edit.",
			},
			"search": {
				Type:        "string",
				Description: "Exact text to find in the file (for single-edit mode).",
			},
			"replace": {
				Type:        "string",
				Description: "Text to replace the search match with (for single-edit mode). Use empty string to delete.",
			},
			"all": {
				Type:        "boolean",
				Description: "Replace all occurrences instead of just the first. Default false.",
			},
			"edits": {
				Type:        "array",
				Description: "Batch mode: array of {search, replace, all?} objects applied sequentially.",
				Items: &agent.ToolParamProp{
					Type:        "object",
					Description: "Edit operation with 'search' (string), 'replace' (string), and optional 'all' (boolean).",
				},
			},
		},
		Required: []string{"path"},
	},
}
