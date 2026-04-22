// Package toolbuiltin/filesystem provides file-system access tools:
//   - read_file    → read text content of a file
//   - write_file   → create or overwrite a file
//   - list_dir     → list directory entries
//   - make_dir     → create a directory (with parents)
package toolbuiltin

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"metiq/internal/agent"
)

const maxReadBytes = 256 * 1024 // 256 KiB max read size

// FilesystemOpts configures workspace-aware path resolution for filesystem tools.
type FilesystemOpts struct {
	// WorkspaceDir returns the agent's workspace directory.  Relative paths
	// supplied to any filesystem tool are resolved against this directory.
	// When nil or returning "", paths are used as-is (legacy behaviour).
	WorkspaceDir func() string
}

// resolvePath makes path absolute by joining it with the workspace directory
// when the path is relative, then verifies the resolved path stays within the
// workspace root (defense-in-depth containment).  Absolute paths that fall
// outside the workspace are rejected.  If no workspace is configured, paths
// are used as-is (legacy behaviour).
func (o FilesystemOpts) resolvePath(path string) (string, error) {
	ws := ""
	if o.WorkspaceDir != nil {
		ws = o.WorkspaceDir()
	}
	if ws == "" {
		// No workspace configured — legacy mode, pass through.
		return path, nil
	}
	// Normalize workspace to an absolute, clean path.
	ws = filepath.Clean(ws)

	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Join(ws, path) // Join calls Clean internally.
	}

	// Containment check: resolved must be within (or equal to) the workspace.
	// We compare with a trailing separator so that "/home/agent/workspace2"
	// doesn't match workspace "/home/agent/workspace".
	if resolved != ws && !strings.HasPrefix(resolved, ws+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q resolves to %q which is outside the workspace %q", path, resolved, ws)
	}
	return resolved, nil
}

// ReadFileTool returns a ToolFunc that reads a text file and returns its content.
// Relative paths are resolved against the configured workspace directory.
func ReadFileTool(opts FilesystemOpts) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		path := agent.ArgString(args, "path")
		if strings.TrimSpace(path) == "" {
			return "", fmt.Errorf("read_file: 'path' is required")
		}
		resolved, err := opts.resolvePath(path)
		if err != nil {
			return "", fmt.Errorf("read_file: %v", err)
		}
		path = resolved
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("read_file: %v", err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("read_file: %q is not a regular file", path)
		}
		size := info.Size()

		// Read at most maxReadBytes+1 so we can detect truncation without
		// loading the entire file into memory.
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read_file: %v", err)
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
		if err != nil {
			return "", fmt.Errorf("read_file: %v", err)
		}

		truncated := int64(len(raw)) > maxReadBytes
		if truncated {
			raw = truncateUTF8Bytes(raw, maxReadBytes)
		}
		content := string(raw)
		if truncated {
			content += fmt.Sprintf("\n\n[truncated: file is %d bytes, read first %d bytes]", size, len(raw))
		}
		return content, nil
	}
}

func truncateUTF8Bytes(raw []byte, max int64) []byte {
	if max <= 0 {
		return nil
	}
	if int64(len(raw)) <= max {
		return raw
	}
	out := raw[:max]
	for len(out) > 0 && !utf8.Valid(out) {
		out = out[:len(out)-1]
	}
	return out
}

var ReadFileDef = agent.ToolDefinition{
	Name:        "read_file",
	Description: "Read the text content of a file. Returns up to 256 KiB; larger files are truncated with a note. Use for inspecting config files, logs, source code, or any text-based data.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"path": {
				Type:        "string",
				Description: "Absolute or relative path to the file to read.",
			},
		},
		Required: []string{"path"},
	},
	ParamAliases: map[string]string{
		"file":      "path",
		"filepath":  "path",
		"file_path": "path",
		"filename":  "path",
	},
}

// WriteFileTool returns a ToolFunc that creates or overwrites a file with the given content.
// Relative paths are resolved against the configured workspace directory.
func WriteFileTool(opts FilesystemOpts) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		path := agent.ArgString(args, "path")
		content := agent.ArgString(args, "content")
		if strings.TrimSpace(path) == "" {
			return "", fmt.Errorf("write_file: 'path' is required")
		}
		resolved, err := opts.resolvePath(path)
		if err != nil {
			return "", fmt.Errorf("write_file: %v", err)
		}
		path = resolved
		// Ensure parent directory exists.
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("write_file: create parent dirs: %v", err)
			}
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("write_file: %v", err)
		}
		return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
	}
}

var WriteFileDef = agent.ToolDefinition{
	Name:        "write_file",
	Description: "Create or overwrite a file with the given text content. Parent directories are created automatically. Use for writing scripts, configs, reports, or any text output.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"path": {
				Type:        "string",
				Description: "Absolute or relative path where the file should be written.",
			},
			"content": {
				Type:        "string",
				Description: "Text content to write to the file.",
			},
		},
		Required: []string{"path", "content"},
	},
	ParamAliases: map[string]string{
		"file":      "path",
		"filepath":  "path",
		"file_path": "path",
		"text":      "content",
		"data":      "content",
		"body":      "content",
	},
}

// ListDirTool returns a ToolFunc that lists entries in a directory.
// Relative paths (including the default ".") are resolved against the
// configured workspace directory, so omitting path lists the workspace root.
func ListDirTool(opts FilesystemOpts) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		path := agent.ArgString(args, "path")
		if strings.TrimSpace(path) == "" {
			path = "."
		}
		resolved, err := opts.resolvePath(path)
		if err != nil {
			return "", fmt.Errorf("list_dir: %v", err)
		}
		path = resolved
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", fmt.Errorf("list_dir: %v", err)
		}
		if len(entries) == 0 {
			return "(empty directory)", nil
		}
		var sb strings.Builder
		for _, e := range entries {
			info, _ := e.Info()
			if e.IsDir() {
				sb.WriteString(fmt.Sprintf("d  %s/\n", e.Name()))
			} else if info != nil {
				sb.WriteString(fmt.Sprintf("f  %-40s %d bytes\n", e.Name(), info.Size()))
			} else {
				sb.WriteString(fmt.Sprintf("?  %s\n", e.Name()))
			}
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
}

var ListDirDef = agent.ToolDefinition{
	Name:        "list_dir",
	Description: "List the files and subdirectories in a directory. Returns names with type prefix (d=dir, f=file) and file sizes. Defaults to current directory if path is omitted.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"path": {
				Type:        "string",
				Description: "Directory path to list. Defaults to current working directory.",
			},
		},
	},
	ParamAliases: map[string]string{
		"dir":       "path",
		"directory": "path",
		"folder":    "path",
	},
}

// MakeDirTool returns a ToolFunc that creates a directory (including parents).
// Relative paths are resolved against the configured workspace directory.
func MakeDirTool(opts FilesystemOpts) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		path := agent.ArgString(args, "path")
		if strings.TrimSpace(path) == "" {
			return "", fmt.Errorf("make_dir: 'path' is required")
		}
		resolved, err := opts.resolvePath(path)
		if err != nil {
			return "", fmt.Errorf("make_dir: %v", err)
		}
		path = resolved
		if err := os.MkdirAll(path, 0755); err != nil {
			return "", fmt.Errorf("make_dir: %v", err)
		}
		return fmt.Sprintf("directory created: %s", path), nil
	}
}

var MakeDirDef = agent.ToolDefinition{
	Name:        "make_dir",
	Description: "Create a directory, including all necessary parent directories. Succeeds silently if the directory already exists.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"path": {
				Type:        "string",
				Description: "Path of the directory to create.",
			},
		},
		Required: []string{"path"},
	},
}
