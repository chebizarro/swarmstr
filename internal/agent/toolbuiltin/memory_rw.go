package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"

	"swarmstr/internal/agent"
	"swarmstr/internal/memory"
)

// MemoryStoreTool returns an agent.ToolFunc for the "memory_store" tool.
//
// Tool parameters:
//   - text (string, required) – content to store
//   - tags ([]string or comma-delimited string, optional) – keywords for retrieval
//   - session_id (string, optional) – scope the entry to a session
func MemoryStoreTool(idx *memory.Index) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		text := agent.ArgString(args, "text")
		if text == "" {
			return "", fmt.Errorf("memory_store: text is required")
		}
		sessionID := agent.ArgString(args, "session_id")

		// Accept tags as []interface{}, []string, or a plain string.
		var tags []string
		switch v := args["tags"].(type) {
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					tags = append(tags, s)
				}
			}
		case []string:
			tags = append(tags, v...)
		case string:
			if v != "" {
				tags = append(tags, v)
			}
		}

		id := idx.Store(sessionID, text, tags)
		// Best-effort persist; non-fatal if it fails.
		_ = idx.Save()

		out, _ := json.Marshal(map[string]any{"id": id, "stored": true})
		return string(out), nil
	}
}

// MemoryDeleteTool returns an agent.ToolFunc for the "memory_delete" tool.
//
// Tool parameters:
//   - id (string, required) – MemoryID returned by memory_store
func MemoryDeleteTool(idx *memory.Index) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("memory_delete: id is required")
		}
		if !idx.Delete(id) {
			return "", fmt.Errorf("memory_delete: entry %q not found", id)
		}
		_ = idx.Save()
		out, _ := json.Marshal(map[string]any{"deleted": true, "id": id})
		return string(out), nil
	}
}
