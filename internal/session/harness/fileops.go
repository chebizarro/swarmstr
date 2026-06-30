package harness

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

func ExtractFileOperations(entries []Entry) FileOperations {
	read := map[string]bool{}
	written := map[string]bool{}
	edited := map[string]bool{}
	for _, e := range entries {
		if e.ToolCall != nil {
			classifyToolCall(*e.ToolCall, read, written, edited)
		}
		if e.Message != nil {
			for _, tc := range e.Message.ToolCalls {
				classifyToolCall(tc, read, written, edited)
			}
		}
	}
	return FileOperations{ReadFiles: keys(read), WrittenFiles: keys(written), EditedFiles: keys(edited)}
}

func classifyToolCall(tc ToolCall, read, written, edited map[string]bool) {
	name := strings.ToLower(tc.Name)
	paths := argumentPaths(tc.Arguments)
	for _, p := range paths {
		switch {
		case strings.Contains(name, "read") || strings.Contains(name, "view") || strings.Contains(name, "grep") || strings.Contains(name, "search"):
			read[p] = true
		case strings.Contains(name, "edit") || strings.Contains(name, "patch") || strings.Contains(name, "replace"):
			edited[p] = true
		case strings.Contains(name, "write") || strings.Contains(name, "create"):
			written[p] = true
		}
	}
}

func argumentPaths(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, v := range t {
				lk := strings.ToLower(k)
				if s, ok := v.(string); ok && (lk == "path" || lk == "file" || lk == "filename" || lk == "filepath") {
					addPath(s, seen)
				}
				walk(v)
			}
		case []any:
			for _, v := range t {
				walk(v)
			}
		}
	}
	walk(v)
	return keys(seen)
}

func addPath(s string, out map[string]bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	out[filepath.Clean(s)] = true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
