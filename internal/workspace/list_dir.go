package workspace

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"metiq/internal/store/state"
)

// MaxListDirEntries bounds the number of directory entries returned by
// ListWorkspaceDirs so a hostile or pathological directory cannot force an
// unbounded response.
const MaxListDirEntries = 1000

// DirEntry is one directory returned by ListWorkspaceDirs. Path is
// workspace-relative ("" is the root); Name is the leaf directory name.
type DirEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden"`
}

// DirListing is the result of ListWorkspaceDirs. Path/Parent are
// workspace-relative; the workspace root is the empty string.
type DirListing struct {
	Path    string
	Parent  *string
	Entries []DirEntry
}

// ListWorkspaceDirs lists the directories directly under browserPath within
// the agent workspace. It reuses the os.Root containment used by the session
// file browser: paths are confined to the workspace root, so a caller cannot
// escape with "..", absolute paths, or symlinks. This is the workspace-rooted
// Metiq analog of OpenClaw's host-wide fs.listDir folder picker.
//
// Only directories are returned (the picker never leaks file names). browserPath
// may be empty (root), workspace-relative, or an absolute path inside the root.
func ListWorkspaceDirs(ctx context.Context, cfg state.ConfigDoc, agentID, browserPath string) (DirListing, error) {
	rootPath := ResolveWorkspaceDir(cfg, agentID)
	root, canonical, err := openWorkspaceRoot(rootPath)
	if err != nil {
		return DirListing{}, err
	}
	defer root.Close()

	rel, err := normalizeTouchedPath(canonical, browserPath)
	if err != nil {
		// Empty/"." browserPath means the workspace root; normalizeTouchedPath
		// rejects "." for touched files, so translate that to the root here.
		if trimmed := strings.TrimSpace(browserPath); trimmed == "" || trimmed == "." || trimmed == "/" {
			rel = "."
		} else {
			return DirListing{}, err
		}
	}
	if rel == "" {
		rel = "."
	}

	file, err := root.Open(rel)
	if err != nil {
		return DirListing{}, fmt.Errorf("workspace directory not found")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return DirListing{}, fmt.Errorf("workspace path is not a directory")
	}
	dirents, err := file.ReadDir(-1)
	if err != nil {
		return DirListing{}, fmt.Errorf("read workspace directory")
	}

	resultPath := ""
	if rel != "." {
		resultPath = rel
	}
	entries := make([]DirEntry, 0, len(dirents))
	for _, dirent := range dirents {
		if err := ctx.Err(); err != nil {
			return DirListing{}, err
		}
		if len(entries) >= MaxListDirEntries {
			break
		}
		name := dirent.Name()
		if !isDir(root, resultPath, dirent) {
			continue
		}
		entryPath := name
		if resultPath != "" {
			entryPath = resultPath + "/" + name
		}
		entries = append(entries, DirEntry{
			Name:   name,
			Path:   entryPath,
			Hidden: strings.HasPrefix(name, "."),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Hidden != entries[j].Hidden {
			return !entries[i].Hidden
		}
		return entries[i].Name < entries[j].Name
	})

	out := DirListing{Path: resultPath, Entries: entries}
	if resultPath != "" {
		parent := path.Dir(resultPath)
		if parent == "." {
			parent = ""
		}
		out.Parent = &parent
	}
	return out, nil
}

func isDir(root *os.Root, parent string, dirent os.DirEntry) bool {
	if dirent.IsDir() {
		return true
	}
	// Resolve symlinks-to-directories via the confined root so the picker can
	// descend into legitimate directory symlinks without escaping the root.
	if dirent.Type()&os.ModeSymlink == 0 {
		return false
	}
	full := dirent.Name()
	if parent != "" {
		full = parent + "/" + dirent.Name()
	}
	info, err := root.Stat(full)
	return err == nil && info.IsDir()
}
