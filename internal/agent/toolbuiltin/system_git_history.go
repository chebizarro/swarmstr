// Package toolbuiltin/system_git_history provides git history tools:
//   - git_log   → structured commit history
//   - git_blame → line-level authorship
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"metiq/internal/agent"
)

// ─── git_log ──────────────────────────────────────────────────────────────────

type gitLogEntry struct {
	Hash       string `json:"hash"`
	Author     string `json:"author"`
	AuthorDate string `json:"author_date"`
	Subject    string `json:"subject"`
	Body       string `json:"body,omitempty"`
	Files      int    `json:"files_changed,omitempty"`
}

type gitLogResult struct {
	Commits []gitLogEntry `json:"commits"`
	Total   int           `json:"total"`
}

func GitLogTool(ctx context.Context, args map[string]any) (string, error) {
	dir := agent.ArgString(args, "directory")
	maxCount := agent.ArgInt(args, "max_count", 20)
	if maxCount < 1 {
		maxCount = 1
	}
	if maxCount > 100 {
		maxCount = 100
	}

	ref := agent.ArgString(args, "ref")
	if ref == "" {
		ref = "HEAD"
	}

	filePath := agent.ArgString(args, "file")
	author := agent.ArgString(args, "author")
	grep := agent.ArgString(args, "grep")

	// Use a custom format with delimiters for reliable parsing.
	// Format: hash<SEP>author<SEP>date<SEP>subject<SEP>body<END>
	sep := "\x1f" // unit separator
	end := "\x1e" // record separator
	format := fmt.Sprintf("%%H%s%%an%s%%aI%s%%s%s%%b%s", sep, sep, sep, sep, end)

	gitArgs := []string{"log",
		"--format=" + format,
		"-n", strconv.Itoa(maxCount),
		"--shortstat",
	}
	if author != "" {
		gitArgs = append(gitArgs, "--author="+author)
	}
	if grep != "" {
		gitArgs = append(gitArgs, "--grep="+grep, "-i")
	}
	gitArgs = append(gitArgs, ref)
	if filePath != "" {
		gitArgs = append(gitArgs, "--", filePath)
	}

	out, err := runGit(ctx, dir, gitArgs...)
	if err != nil {
		return "", fmt.Errorf("git_log: %v — %s", err, out)
	}

	var commits []gitLogEntry
	records := strings.Split(out, end)
	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}

		// Split off the shortstat line(s) that follow the record.
		parts := strings.SplitN(rec, sep, 5)
		if len(parts) < 4 {
			continue
		}

		entry := gitLogEntry{
			Hash:       parts[0][:minInt(12, len(parts[0]))], // short hash
			Author:     strings.TrimSpace(parts[1]),
			AuthorDate: strings.TrimSpace(parts[2]),
			Subject:    strings.TrimSpace(parts[3]),
		}

		if len(parts) >= 5 {
			body := strings.TrimSpace(parts[4])
			// The body may contain shortstat output appended after blank lines.
			// Extract file change count from shortstat if present.
			lines := strings.Split(body, "\n")
			var bodyLines []string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.Contains(trimmed, "file changed") || strings.Contains(trimmed, "files changed") {
					// Parse "N files changed" from shortstat.
					fields := strings.Fields(trimmed)
					if len(fields) >= 1 {
						if n, err := strconv.Atoi(fields[0]); err == nil {
							entry.Files = n
						}
					}
				} else if trimmed != "" {
					bodyLines = append(bodyLines, trimmed)
				}
			}
			if len(bodyLines) > 0 {
				entry.Body = strings.Join(bodyLines, "\n")
				// Truncate long bodies.
				if len(entry.Body) > 500 {
					entry.Body = entry.Body[:500] + "..."
				}
			}
		}

		commits = append(commits, entry)
	}

	result := gitLogResult{
		Commits: commits,
		Total:   len(commits),
	}
	raw, _ := json.Marshal(result)
	return string(raw), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var GitLogDef = agent.ToolDefinition{
	Name:        "git_log",
	Description: "Returns structured JSON of git commit history: hash, author, date, subject, body, and files changed. Supports filtering by ref, author, grep pattern, and file path. Use to understand code history, find when changes were made, or review recent activity.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"directory": {
				Type:        "string",
				Description: "Working directory for the git command. Defaults to current directory.",
			},
			"ref": {
				Type:        "string",
				Description: "Git ref to start from (branch, tag, commit). Default: HEAD.",
			},
			"max_count": {
				Type:        "integer",
				Description: "Maximum commits to return (1–100, default 20).",
			},
			"file": {
				Type:        "string",
				Description: "Show only commits that touch this file path.",
			},
			"author": {
				Type:        "string",
				Description: "Filter by author name/email (substring match).",
			},
			"grep": {
				Type:        "string",
				Description: "Filter commits whose message matches this pattern.",
			},
		},
	},
	ParamAliases: map[string]string{
		"count":  "max_count",
		"limit":  "max_count",
		"n":      "max_count",
		"dir":    "directory",
		"path":   "file",
		"branch": "ref",
		"commit": "ref",
	},
}

// ─── git_blame ────────────────────────────────────────────────────────────────

type gitBlameEntry struct {
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Hash      string `json:"hash"`
	Author    string `json:"author"`
	Date      string `json:"date"`
	Summary   string `json:"summary"`
}

type gitBlameResult struct {
	File    string          `json:"file"`
	Entries []gitBlameEntry `json:"entries"`
}

func GitBlameTool(ctx context.Context, args map[string]any) (string, error) {
	dir := agent.ArgString(args, "directory")
	file := agent.ArgString(args, "file")
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("git_blame: 'file' is required")
	}

	gitArgs := []string{"blame", "--porcelain"}

	// Optional line range.
	startLine := agent.ArgInt(args, "start_line", 0)
	endLine := agent.ArgInt(args, "end_line", 0)
	if startLine > 0 && endLine > 0 {
		gitArgs = append(gitArgs, fmt.Sprintf("-L%d,%d", startLine, endLine))
	} else if startLine > 0 {
		gitArgs = append(gitArgs, fmt.Sprintf("-L%d,+50", startLine))
	}

	gitArgs = append(gitArgs, "--", file)

	out, err := runGit(ctx, dir, gitArgs...)
	if err != nil {
		return "", fmt.Errorf("git_blame: %v — %s", err, out)
	}

	entries := parseBlamePortable(out)

	result := gitBlameResult{
		File:    file,
		Entries: entries,
	}
	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// parseBlamePortable parses git blame --porcelain output into grouped entries.
// Consecutive lines with the same commit are merged into a single entry.
func parseBlamePortable(out string) []gitBlameEntry {
	type lineInfo struct {
		hash    string
		author  string
		date    string
		summary string
		lineNum int
	}

	var lineInfos []lineInfo
	commitMeta := make(map[string]map[string]string)

	lines := strings.Split(out, "\n")
	var currentHash string
	var currentLineNum int

	for _, line := range lines {
		if line == "" {
			continue
		}
		// Header line: <hash> <orig-line> <final-line> [<num-lines>]
		if len(line) >= 40 && !strings.HasPrefix(line, "\t") && !strings.Contains(line[:40], " ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				currentHash = parts[0][:minInt(12, len(parts[0]))]
				if n, err := strconv.Atoi(parts[2]); err == nil {
					currentLineNum = n
				}
				if _, ok := commitMeta[currentHash]; !ok {
					commitMeta[currentHash] = make(map[string]string)
				}
			}
			continue
		}
		// Metadata lines.
		if strings.HasPrefix(line, "author ") {
			if m, ok := commitMeta[currentHash]; ok {
				m["author"] = strings.TrimPrefix(line, "author ")
			}
		} else if strings.HasPrefix(line, "author-time ") {
			if m, ok := commitMeta[currentHash]; ok {
				m["date"] = strings.TrimPrefix(line, "author-time ")
			}
		} else if strings.HasPrefix(line, "summary ") {
			if m, ok := commitMeta[currentHash]; ok {
				m["summary"] = strings.TrimPrefix(line, "summary ")
			}
		} else if strings.HasPrefix(line, "\t") {
			// Content line — record this line's blame info.
			meta := commitMeta[currentHash]
			lineInfos = append(lineInfos, lineInfo{
				hash:    currentHash,
				author:  meta["author"],
				date:    meta["date"],
				summary: meta["summary"],
				lineNum: currentLineNum,
			})
		}
	}

	// Merge consecutive lines with the same commit.
	var entries []gitBlameEntry
	for _, li := range lineInfos {
		if len(entries) > 0 {
			last := &entries[len(entries)-1]
			if last.Hash == li.hash && last.LineEnd+1 == li.lineNum {
				last.LineEnd = li.lineNum
				continue
			}
		}
		entries = append(entries, gitBlameEntry{
			LineStart: li.lineNum,
			LineEnd:   li.lineNum,
			Hash:      li.hash,
			Author:    li.author,
			Date:      li.date,
			Summary:   li.summary,
		})
	}

	// Cap entries to avoid huge output.
	if len(entries) > 100 {
		entries = entries[:100]
	}
	return entries
}

var GitBlameDef = agent.ToolDefinition{
	Name:        "git_blame",
	Description: "Returns structured JSON of git blame output for a file: line ranges, commit hash, author, date, and commit summary. Consecutive lines from the same commit are merged. Use to understand who changed what and why, trace authorship, or find the commit that introduced a specific line.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"directory": {
				Type:        "string",
				Description: "Working directory for the git command. Defaults to current directory.",
			},
			"file": {
				Type:        "string",
				Description: "File path to blame (required).",
			},
			"start_line": {
				Type:        "integer",
				Description: "Start of line range to blame. If set without end_line, blames 50 lines from start.",
			},
			"end_line": {
				Type:        "integer",
				Description: "End of line range to blame.",
			},
		},
		Required: []string{"file"},
	},
	ParamAliases: map[string]string{
		"path": "file",
		"dir":  "directory",
		"line": "start_line",
		"from": "start_line",
		"to":   "end_line",
	},
}
