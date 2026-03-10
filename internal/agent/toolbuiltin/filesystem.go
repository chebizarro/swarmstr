// Package toolbuiltin/filesystem provides file-system access tools:
//   - read_file    → read text content of a file
//   - write_file   → create or overwrite a file
//   - list_dir     → list directory entries
//   - make_dir     → create a directory (with parents)
package toolbuiltin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"swarmstr/internal/agent"
)

const maxReadBytes = 256 * 1024 // 256 KiB max read size

// ReadFileTool reads a text file and returns its content.
func ReadFileTool(_ context.Context, args map[string]any) (string, error) {
	path := agent.ArgString(args, "path")
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("read_file: 'path' is required")
	}
	// Clamp to maxReadBytes to avoid huge payloads to the model.
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %v", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read_file: %q is not a regular file", path)
	}
	size := info.Size()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %v", err)
	}
	truncated := false
	if int64(len(raw)) > maxReadBytes {
		raw = truncateUTF8Bytes(raw, maxReadBytes)
		truncated = true
	}
	content := string(raw)
	if size > maxReadBytes {
		content += fmt.Sprintf("\n\n[truncated: file is %d bytes, read first %d bytes]", size, len(raw))
	} else if truncated {
		content += fmt.Sprintf("\n\n[truncated to valid UTF-8 boundary at %d bytes]", len(raw))
	}
	return content, nil
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
}

// WriteFileTool creates or overwrites a file with the given content.
func WriteFileTool(_ context.Context, args map[string]any) (string, error) {
	path := agent.ArgString(args, "path")
	content := agent.ArgString(args, "content")
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("write_file: 'path' is required")
	}
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
}

// ListDirTool lists entries in a directory.
func ListDirTool(_ context.Context, args map[string]any) (string, error) {
	path := agent.ArgString(args, "path")
	if strings.TrimSpace(path) == "" {
		path = "."
	}
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
}

// MakeDirTool creates a directory (including parents).
func MakeDirTool(_ context.Context, args map[string]any) (string, error) {
	path := agent.ArgString(args, "path")
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("make_dir: 'path' is required")
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("make_dir: %v", err)
	}
	return fmt.Sprintf("directory created: %s", path), nil
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
