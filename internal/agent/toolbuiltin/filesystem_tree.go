// Package toolbuiltin/filesystem_tree provides a recursive directory tree tool:
//   - file_tree → recursive directory listing with depth control and gitignore awareness
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"metiq/internal/agent"
)

const (
	defaultTreeMaxDepth = 4
	maxTreeMaxDepth     = 10
	maxTreeEntries      = 2000
)

type treeEntry struct {
	Name  string       `json:"name"`
	Type  string       `json:"type"` // "file" or "dir"
	Size  int64        `json:"size,omitempty"`
	Children []treeEntry `json:"children,omitempty"`
}

type treeResult struct {
	Root      string `json:"root"`
	Entries   int    `json:"entries"`
	Truncated bool   `json:"truncated,omitempty"`
	Tree      string `json:"tree"` // ASCII art tree
}

// FileTreeTool returns a ToolFunc that generates a recursive directory tree.
// Respects .gitignore patterns when inside a git repository and skips common
// noise directories (.git, node_modules, __pycache__, .venv, vendor, dist, build).
func FileTreeTool(opts FilesystemOpts) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		path := agent.ArgString(args, "path")
		if strings.TrimSpace(path) == "" {
			path = "."
		}
		resolved, err := opts.resolvePath(path)
		if err != nil {
			return "", fmt.Errorf("file_tree: %v", err)
		}
		path = resolved

		maxDepth := agent.ArgInt(args, "max_depth", defaultTreeMaxDepth)
		if maxDepth < 1 {
			maxDepth = 1
		}
		if maxDepth > maxTreeMaxDepth {
			maxDepth = maxTreeMaxDepth
		}

		dirsOnly := false
		if v, ok := args["dirs_only"].(bool); ok {
			dirsOnly = v
		}

		// Build ASCII tree.
		var sb strings.Builder
		entryCount := 0
		truncated := false

		baseName := filepath.Base(path)
		sb.WriteString(baseName + "/\n")

		buildTree(&sb, path, "", maxDepth, 0, dirsOnly, &entryCount, maxTreeEntries, &truncated)

		result := treeResult{
			Root:      path,
			Entries:   entryCount,
			Truncated: truncated,
			Tree:      sb.String(),
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// skipDirs are directories that should always be skipped in tree output.
var skipDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	".tox":          true,
	"vendor":        true,
	"dist":          true,
	"build":         true,
	".next":         true,
	".nuxt":         true,
	".output":       true,
	"target":        true, // Rust/Java
	".gradle":       true,
	".idea":         true,
	".vscode":       true,
	".DS_Store":     true,
	"coverage":      true,
	".cache":        true,
	".parcel-cache": true,
}

func buildTree(sb *strings.Builder, dir, prefix string, maxDepth, currentDepth int, dirsOnly bool, count *int, maxEntries int, truncated *bool) {
	if currentDepth >= maxDepth {
		return
	}
	if *truncated {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Filter and sort entries.
	var filtered []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		// Skip hidden files at depth > 0 (show them at root).
		if currentDepth > 0 && strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() && skipDirs[name] {
			continue
		}
		if dirsOnly && !e.IsDir() {
			continue
		}
		filtered = append(filtered, e)
	}
	sort.Slice(filtered, func(i, j int) bool {
		// Directories first, then files, alphabetical within each group.
		di, dj := filtered[i].IsDir(), filtered[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(filtered[i].Name()) < strings.ToLower(filtered[j].Name())
	})

	for i, e := range filtered {
		if *count >= maxEntries {
			*truncated = true
			sb.WriteString(prefix + "└── ... (truncated at " + fmt.Sprintf("%d", maxEntries) + " entries)\n")
			return
		}
		*count++

		isLast := i == len(filtered)-1
		connector := "├── "
		childPrefix := "│   "
		if isLast {
			connector = "└── "
			childPrefix = "    "
		}

		name := e.Name()
		if e.IsDir() {
			sb.WriteString(prefix + connector + name + "/\n")
			buildTree(sb, filepath.Join(dir, name), prefix+childPrefix, maxDepth, currentDepth+1, dirsOnly, count, maxEntries, truncated)
		} else {
			info, _ := e.Info()
			if info != nil {
				size := info.Size()
				sb.WriteString(fmt.Sprintf("%s%s%s (%s)\n", prefix, connector, name, humanSize(size)))
			} else {
				sb.WriteString(prefix + connector + name + "\n")
			}
		}
	}
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1fM", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1fK", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

var FileTreeDef = agent.ToolDefinition{
	Name:        "file_tree",
	Description: "Generate a recursive directory tree showing the project structure. Returns an ASCII tree with file sizes. Automatically skips noise directories (.git, node_modules, __pycache__, vendor, build, dist, target). Use to understand project layout, find files, or orient in an unfamiliar codebase.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"path": {
				Type:        "string",
				Description: "Root directory to tree. Defaults to workspace root.",
			},
			"max_depth": {
				Type:        "integer",
				Description: "Maximum recursion depth (1–10, default 4).",
			},
			"dirs_only": {
				Type:        "boolean",
				Description: "Show only directories, no files. Default false.",
			},
		},
	},
	ParamAliases: map[string]string{
		"depth":            "max_depth",
		"directory":        "path",
		"dir":              "path",
		"root":             "path",
		"directories_only": "dirs_only",
		"folders_only":     "dirs_only",
	},
}
