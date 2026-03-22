// Package toolbuiltin/system_git provides structured git tools:
//   - git_status  → parsed file lists by state (staged, modified, untracked, etc.)
//   - git_diff    → per-file hunks with metadata
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"metiq/internal/agent"
)

// ─── git_status ───────────────────────────────────────────────────────────────

type gitStatusResult struct {
	Branch    string           `json:"branch"`
	Staged    []gitFileEntry   `json:"staged,omitempty"`
	Modified  []gitFileEntry   `json:"modified,omitempty"`
	Untracked []string         `json:"untracked,omitempty"`
	Conflicts []gitFileEntry   `json:"conflicts,omitempty"`
	Clean     bool             `json:"clean"`
}

type gitFileEntry struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added, modified, deleted, renamed, copied, type_changed
}

func GitStatusTool(ctx context.Context, args map[string]any) (string, error) {
	dir := agent.ArgString(args, "directory")

	// Get branch name.
	branch, _ := runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")

	// Porcelain v2 for machine-readable output.
	out, err := runGit(ctx, dir, "status", "--porcelain=v2", "--branch", "-uall")
	if err != nil {
		return "", fmt.Errorf("git_status: %v", err)
	}

	result := gitStatusResult{Branch: strings.TrimSpace(branch)}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "# branch.head "):
			result.Branch = strings.TrimPrefix(line, "# branch.head ")

		case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 "):
			// Ordinary (1) or rename/copy (2) changed entry.
			// Format: 1 XY sub mH mI mW hH hI path
			// Format: 2 XY sub mH mI mW hH hI X{NNN} path\torigPath
			// Note: type 2 paths are tab-separated, so split on tab first.
			var path string
			var xy string
			if strings.HasPrefix(line, "2 ") {
				// Type 2: split on tab to separate newPath from origPath.
				tabParts := strings.SplitN(line, "\t", 3)
				if len(tabParts) < 2 {
					continue
				}
				path = tabParts[0][strings.LastIndex(tabParts[0], " ")+1:]
				fields := strings.Fields(tabParts[0])
				if len(fields) < 9 {
					continue
				}
				xy = fields[1]
			} else {
				// Type 1: path is everything after the 8th space-delimited field.
				// Use SplitN to preserve spaces in file paths.
				parts := strings.SplitN(line, " ", 9)
				if len(parts) < 9 {
					continue
				}
				xy = parts[1]
				path = parts[8]
			}

			indexStatus := xy[0]
			wtStatus := xy[1]

			if indexStatus != '.' && indexStatus != '?' {
				result.Staged = append(result.Staged, gitFileEntry{
					Path:   path,
					Status: gitStatusChar(indexStatus),
				})
			}
			if wtStatus != '.' && wtStatus != '?' {
				result.Modified = append(result.Modified, gitFileEntry{
					Path:   path,
					Status: gitStatusChar(wtStatus),
				})
			}

		case strings.HasPrefix(line, "u "):
			// Unmerged entry.
			parts := strings.Fields(line)
			if len(parts) >= 11 {
				result.Conflicts = append(result.Conflicts, gitFileEntry{
					Path:   parts[len(parts)-1],
					Status: "conflict",
				})
			}

		case strings.HasPrefix(line, "? "):
			result.Untracked = append(result.Untracked, strings.TrimPrefix(line, "? "))
		}
	}

	result.Clean = len(result.Staged) == 0 && len(result.Modified) == 0 &&
		len(result.Untracked) == 0 && len(result.Conflicts) == 0

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

func gitStatusChar(c byte) string {
	switch c {
	case 'A':
		return "added"
	case 'M':
		return "modified"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type_changed"
	default:
		return string(c)
	}
}

var GitStatusDef = agent.ToolDefinition{
	Name:        "git_status",
	Description: "Returns structured JSON showing the git working tree status: branch name, staged files, modified files, untracked files, and merge conflicts. Each file entry includes path and status (added/modified/deleted/renamed). Returns {clean: true} when the working tree is clean.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"directory": {
				Type:        "string",
				Description: "Working directory for the git command. Defaults to current directory.",
			},
		},
	},
}

// ─── git_diff ─────────────────────────────────────────────────────────────────

type gitDiffResult struct {
	Files   []gitDiffFile `json:"files"`
	Summary struct {
		FilesChanged int `json:"files_changed"`
		Insertions   int `json:"insertions"`
		Deletions    int `json:"deletions"`
	} `json:"summary"`
}

type gitDiffFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"` // for renames
	Status    string `json:"status"`             // added, modified, deleted, renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Diff      string `json:"diff"` // unified diff text for this file
}

func GitDiffTool(ctx context.Context, args map[string]any) (string, error) {
	dir := agent.ArgString(args, "directory")
	target := agent.ArgString(args, "target") // e.g. "HEAD", "main..HEAD", "HEAD~3"
	staged := false
	if s, ok := args["staged"]; ok {
		if b, ok := s.(bool); ok {
			staged = b
		}
	}

	gitArgs := []string{"diff"}
	if staged {
		gitArgs = append(gitArgs, "--cached")
	}
	if target != "" {
		gitArgs = append(gitArgs, target)
	}

	// Get name-status for accurate file status.
	nameStatusArgs := append(append([]string{}, gitArgs...), "--name-status")
	nameStatusOut, _ := runGit(ctx, dir, nameStatusArgs...)
	statusMap := parseNameStatus(nameStatusOut)

	// Get numstat for line counts.
	numstatArgs := append(append([]string{}, gitArgs...), "--numstat")
	numstatOut, _ := runGit(ctx, dir, numstatArgs...)

	// Get the actual diff.
	diffOut, err := runGit(ctx, dir, gitArgs...)
	if err != nil {
		return "", fmt.Errorf("git_diff: %v", err)
	}

	result := gitDiffResult{}

	// Build numstat lookup: path → (adds, dels).
	numstatMap := make(map[string][2]int)
	for _, line := range strings.Split(numstatOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		numstatMap[parts[2]] = [2]int{parseInt(parts[0]), parseInt(parts[1])}
	}

	// Drive the file list from --name-status (authoritative for status/paths).
	// Enrich with numstat counts when available.
	for path, ns := range statusMap {
		entry := gitDiffFile{
			Path:   path,
			Status: ns.status,
		}
		if ns.oldPath != "" {
			entry.OldPath = ns.oldPath
		}
		// Look up line counts from numstat.
		// For renames, numstat may use the new path or old→new notation.
		if counts, ok := numstatMap[path]; ok {
			entry.Additions = counts[0]
			entry.Deletions = counts[1]
		} else if ns.oldPath != "" {
			// Try old\tnew format that numstat sometimes uses for renames.
			renameKey := ns.oldPath + "\t" + path
			if counts, ok := numstatMap[renameKey]; ok {
				entry.Additions = counts[0]
				entry.Deletions = counts[1]
			}
		}

		result.Summary.FilesChanged++
		result.Summary.Insertions += entry.Additions
		result.Summary.Deletions += entry.Deletions
		result.Files = append(result.Files, entry)
	}

	// Attach per-file diffs (truncated to keep response manageable).
	attachFileDiffs(&result, diffOut)

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// attachFileDiffs splits unified diff output by file and attaches to results.
func attachFileDiffs(result *gitDiffResult, diffOut string) {
	// Split on "diff --git" boundaries.
	sections := strings.Split("\n"+diffOut, "\ndiff --git ")
	fileMap := make(map[string]string, len(sections))
	for _, sec := range sections[1:] { // skip empty first element
		// Extract the b/ path from "a/... b/..."
		firstLine := sec
		if nl := strings.Index(sec, "\n"); nl >= 0 {
			firstLine = sec[:nl]
		}
		if bIdx := strings.LastIndex(firstLine, " b/"); bIdx >= 0 {
			fpath := firstLine[bIdx+3:]
			chunk := "diff --git " + sec
			// Truncate individual file diffs at 4000 chars.
			if len(chunk) > 4000 {
				chunk = chunk[:4000] + "\n... (truncated)"
			}
			fileMap[fpath] = chunk
		}
	}
	for i := range result.Files {
		path := result.Files[i].Path
		if d, ok := fileMap[path]; ok {
			result.Files[i].Diff = d
		}
	}
}

type nameStatusEntry struct {
	status  string
	oldPath string // for renames
}

func parseNameStatus(out string) map[string]nameStatusEntry {
	result := make(map[string]nameStatusEntry)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		code := parts[0]
		var status, path, oldPath string
		switch {
		case strings.HasPrefix(code, "A"):
			status = "added"
			path = parts[1]
		case strings.HasPrefix(code, "D"):
			status = "deleted"
			path = parts[1]
		case strings.HasPrefix(code, "M"):
			status = "modified"
			path = parts[1]
		case strings.HasPrefix(code, "R"):
			status = "renamed"
			if len(parts) >= 3 {
				oldPath = parts[1]
				path = parts[2]
			} else {
				path = parts[1]
			}
		case strings.HasPrefix(code, "C"):
			status = "copied"
			if len(parts) >= 3 {
				oldPath = parts[1]
				path = parts[2]
			} else {
				path = parts[1]
			}
		case strings.HasPrefix(code, "T"):
			status = "type_changed"
			path = parts[1]
		default:
			status = "modified"
			path = parts[1]
		}
		result[path] = nameStatusEntry{status: status, oldPath: oldPath}
	}
	return result
}

func parseInt(s string) int {
	// Binary files show "-" instead of a number.
	if s == "-" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

var GitDiffDef = agent.ToolDefinition{
	Name:        "git_diff",
	Description: "Returns structured JSON of git diff output: per-file entries with path, status (added/modified/deleted/renamed), line counts, and the unified diff text. Includes a summary with total files_changed, insertions, and deletions.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"directory": {
				Type:        "string",
				Description: "Working directory for the git command. Defaults to current directory.",
			},
			"target": {
				Type:        "string",
				Description: "Diff target: a commit ref like \"HEAD\", a range like \"main..HEAD\", or empty for unstaged changes.",
			},
			"staged": {
				Type:        "boolean",
				Description: "If true, show staged (--cached) changes instead of unstaged. Defaults to false.",
			},
		},
	},
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// runGit executes a git command with a 15s timeout and returns trimmed output.
func runGit(ctx context.Context, dir string, gitArgs ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	args := append([]string{}, gitArgs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
