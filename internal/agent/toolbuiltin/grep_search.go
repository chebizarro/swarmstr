// Package toolbuiltin/grep_search provides a structured content search tool:
//   - grep_search → search file contents using ripgrep (rg) or fallback grep
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"metiq/internal/agent"
)

const (
	defaultGrepMaxResults   = 50
	maxGrepResults          = 200
	defaultGrepContextLines = 0
	grepTimeoutSec          = 30
)

// grepMatch is a single search result.
type grepMatch struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Column     int    `json:"column,omitempty"`
	Text       string `json:"text"`
	ContextPre string `json:"context_pre,omitempty"`
	ContextPost string `json:"context_post,omitempty"`
}

type grepResult struct {
	Matches      []grepMatch `json:"matches"`
	TotalMatches int         `json:"total_matches"`
	Truncated    bool        `json:"truncated,omitempty"`
	Tool         string      `json:"tool"` // "rg" or "grep"
}

// GrepSearchTool searches file contents using ripgrep (preferred) or grep fallback.
func GrepSearchTool(opts FilesystemOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		pattern := agent.ArgString(args, "pattern")
		if strings.TrimSpace(pattern) == "" {
			return "", fmt.Errorf("grep_search: 'pattern' is required")
		}

		dir := agent.ArgString(args, "directory")
		if dir == "" {
			dir = "."
		}
		resolved, err := opts.resolvePath(dir)
		if err != nil {
			return "", fmt.Errorf("grep_search: %v", err)
		}
		dir = resolved

		maxResults := agent.ArgInt(args, "max_results", defaultGrepMaxResults)
		if maxResults <= 0 {
			maxResults = defaultGrepMaxResults
		}
		if maxResults > maxGrepResults {
			maxResults = maxGrepResults
		}

		contextLines := agent.ArgInt(args, "context_lines", defaultGrepContextLines)
		if contextLines < 0 {
			contextLines = 0
		}
		if contextLines > 5 {
			contextLines = 5
		}

		includeGlob := agent.ArgString(args, "include")
		fixedStrings := false
		if v, ok := args["fixed_strings"].(bool); ok {
			fixedStrings = v
		}

		timeout := grepTimeout(ctx)
		execCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Try ripgrep first, fall back to grep.
		rgPath, rgErr := exec.LookPath("rg")
		if rgErr == nil {
			return runRipgrep(execCtx, rgPath, dir, pattern, includeGlob, fixedStrings, maxResults, contextLines)
		}
		return runGrepFallback(execCtx, dir, pattern, includeGlob, fixedStrings, maxResults, contextLines)
	}
}

func runRipgrep(ctx context.Context, rgPath, dir, pattern, includeGlob string, fixedStrings bool, maxResults, contextLines int) (string, error) {
	args := []string{
		"--json",
		"--max-count", strconv.Itoa(maxResults * 2), // fetch extra, we'll trim
		"--max-filesize", "1M",
		"--no-messages", // suppress permission errors etc.
	}
	if fixedStrings {
		args = append(args, "--fixed-strings")
	} else {
		args = append(args, "--smart-case")
	}
	if contextLines > 0 {
		args = append(args, "--context", strconv.Itoa(contextLines))
	}
	if includeGlob != "" {
		args = append(args, "--glob", includeGlob)
	}
	args = append(args, "--", pattern, ".")

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	// rg exits 1 when no matches found — that's not an error.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			result := grepResult{Tool: "rg", Matches: []grepMatch{}, TotalMatches: 0}
			raw, _ := json.Marshal(result)
			return string(raw), nil
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("grep_search: timed out")
		}
		return "", fmt.Errorf("grep_search (rg): %v", err)
	}

	return parseRipgrepJSON(string(out), maxResults)
}

// parseRipgrepJSON parses rg --json output into structured results.
func parseRipgrepJSON(output string, maxResults int) (string, error) {
	var matches []grepMatch
	totalMatches := 0

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		typ, _ := event["type"].(string)
		if typ != "match" {
			continue
		}
		totalMatches++
		if len(matches) >= maxResults {
			continue // count but don't collect
		}

		data, _ := event["data"].(map[string]any)
		if data == nil {
			continue
		}

		pathData, _ := data["path"].(map[string]any)
		filePath, _ := pathData["text"].(string)
		lineNum := 0
		if ln, ok := data["line_number"].(float64); ok {
			lineNum = int(ln)
		}

		lineText := ""
		if lines, ok := data["lines"].(map[string]any); ok {
			lineText, _ = lines["text"].(string)
		}
		lineText = strings.TrimRight(lineText, "\n\r")

		// Truncate very long lines.
		if len(lineText) > 500 {
			lineText = lineText[:500] + "..."
		}

		// Clean up relative path prefix.
		filePath = strings.TrimPrefix(filePath, "./")

		col := 0
		if submatches, ok := data["submatches"].([]any); ok && len(submatches) > 0 {
			if sm, ok := submatches[0].(map[string]any); ok {
				if start, ok := sm["start"].(float64); ok {
					col = int(start) + 1
				}
			}
		}

		matches = append(matches, grepMatch{
			File:   filePath,
			Line:   lineNum,
			Column: col,
			Text:   lineText,
		})
	}

	result := grepResult{
		Tool:         "rg",
		Matches:      matches,
		TotalMatches: totalMatches,
		Truncated:    totalMatches > maxResults,
	}
	raw, _ := json.Marshal(result)
	return string(raw), nil
}

func runGrepFallback(ctx context.Context, dir, pattern, includeGlob string, fixedStrings bool, maxResults, contextLines int) (string, error) {
	args := []string{"-r", "-n"}
	if fixedStrings {
		args = append(args, "-F")
	} else {
		args = append(args, "-E", "-i")
	}
	if contextLines > 0 {
		args = append(args, fmt.Sprintf("-C%d", contextLines))
	}
	if includeGlob != "" {
		args = append(args, "--include="+includeGlob)
	}
	args = append(args, "--", pattern, ".")

	cmd := exec.CommandContext(ctx, "grep", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			result := grepResult{Tool: "grep", Matches: []grepMatch{}, TotalMatches: 0}
			raw, _ := json.Marshal(result)
			return string(raw), nil
		}
		return "", fmt.Errorf("grep_search (grep): %v", err)
	}

	var matches []grepMatch
	totalMatches := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		totalMatches++
		if len(matches) >= maxResults {
			continue
		}

		// Format: ./file:line:text
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		filePath := strings.TrimPrefix(parts[0], "./")
		lineNum, _ := strconv.Atoi(parts[1])
		text := parts[2]
		if len(text) > 500 {
			text = text[:500] + "..."
		}

		matches = append(matches, grepMatch{
			File: filePath,
			Line: lineNum,
			Text: text,
		})
	}

	result := grepResult{
		Tool:         "grep",
		Matches:      matches,
		TotalMatches: totalMatches,
		Truncated:    totalMatches > maxResults,
	}
	raw, _ := json.Marshal(result)
	return string(raw), nil
}

var GrepSearchDef = agent.ToolDefinition{
	Name:        "grep_search",
	Description: "Search file contents across a directory using ripgrep (rg) or grep. Returns structured JSON with file paths, line numbers, column offsets, and matching text. Use for finding code patterns, function definitions, imports, TODOs, or any text across a codebase. Prefers ripgrep for speed; falls back to grep.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pattern": {
				Type:        "string",
				Description: "Search pattern (regex by default, literal with fixed_strings:true). E.g. \"func main\", \"TODO|FIXME\", \"import.*http\".",
			},
			"directory": {
				Type:        "string",
				Description: "Directory to search in. Defaults to workspace root.",
			},
			"include": {
				Type:        "string",
				Description: "Glob pattern to filter files, e.g. \"*.go\", \"*.{ts,tsx}\", \"Makefile\".",
			},
			"fixed_strings": {
				Type:        "boolean",
				Description: "Treat pattern as a literal string instead of regex. Default false.",
			},
			"max_results": {
				Type:        "integer",
				Description: "Maximum matches to return (1–200, default 50).",
			},
			"context_lines": {
				Type:        "integer",
				Description: "Lines of context before/after each match (0–5, default 0).",
			},
		},
		Required: []string{"pattern"},
	},
	ParamAliases: map[string]string{
		"dir":       "directory",
		"path":      "directory",
		"folder":    "directory",
		"query":     "pattern",
		"search":    "pattern",
		"regex":     "pattern",
		"glob":      "include",
		"filter":    "include",
		"literal":   "fixed_strings",
		"limit":     "max_results",
		"max":       "max_results",
		"count":     "max_results",
		"context":   "context_lines",
	},
}
