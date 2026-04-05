package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/memory"
	"metiq/internal/store/state"
)

// MemoryStoreTool returns an agent.ToolFunc for the "memory_store" tool.
//
// Tool parameters:
//   - text (string, required) – content to store
//   - tags ([]string or comma-delimited string, optional) – keywords for retrieval
//   - session_id (string, optional) – scope the entry to a session
//
// MemoryStoreDef is the ToolDefinition for memory_store.
var MemoryStoreDef = agent.ToolDefinition{
	Name:        "memory_store",
	Description: "Persist durable searchable memory for later recall. Use for user facts, validated feedback, project context, or external references that are not derivable from the current repo state.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The information to store (plain text).",
			},
			"tags": map[string]any{
				"description": "Optional keywords for retrieval. Accepts either an array of strings or a single string.",
				"oneOf": []any{
					map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					map[string]any{
						"type": "string",
					},
				},
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Optional session scope for the stored entry.",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional memory category/topic. Prefer durable labels such as user, feedback, project, or reference when they apply.",
			},
		},
		"required": []any{"text"},
	},
}

// MemoryDeleteDef is the ToolDefinition for memory_delete.
var MemoryDeleteDef = agent.ToolDefinition{
	Name:        "memory_delete",
	Description: "Delete a previously stored memory entry by its ID. Use when stored information is outdated, incorrect, or no longer relevant.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "The ID of the memory record to delete (returned by memory_store or memory.search).",
			},
		},
		Required: []string{"id"},
	},
}

func MemoryStoreTool(idx memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		text := agent.ArgString(args, "text")
		if text == "" {
			return "", fmt.Errorf("memory_store: text is required")
		}
		sessionID, err := agent.ResolveSessionIDStrict(ctx, args)
		if err != nil {
			return "", fmt.Errorf("memory_store: %w", err)
		}

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

		topic := strings.TrimSpace(agent.ArgString(args, "topic"))
		scope := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
		id := ""
		if topic == "" && !scope.Enabled() {
			id = idx.Store(sessionID, text, tags)
		} else {
			allKeywords := append([]string(nil), tags...)
			if topic != "" {
				allKeywords = append(allKeywords, topic)
			}
			id = memory.GenerateMemoryID()
			doc := state.MemoryDoc{
				MemoryID:  id,
				SessionID: sessionID,
				Text:      text,
				Keywords:  allKeywords,
				Topic:     topic,
				Unix:      time.Now().Unix(),
			}
			doc = memory.ApplyScope(doc, scope)
			memory.AddDoc(ctx, idx, doc)
		}
		if saveErr := idx.Save(); saveErr != nil {
			log.Printf("memory_store: index save failed: %v", saveErr)
		}

		out, _ := json.Marshal(map[string]any{"id": id, "stored": true})
		return string(out), nil
	}
}

// MemoryDeleteTool returns an agent.ToolFunc for the "memory_delete" tool.
//
// Tool parameters:
//   - id (string, required) – MemoryID returned by memory_store
func MemoryDeleteTool(idx memory.Store) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("memory_delete: id is required")
		}
		if !idx.Delete(id) {
			return "", fmt.Errorf("memory_delete: entry %q not found", id)
		}
		if saveErr := idx.Save(); saveErr != nil {
			log.Printf("memory_delete: index save failed: %v", saveErr)
		}
		out, _ := json.Marshal(map[string]any{"deleted": true, "id": id})
		return string(out), nil
	}
}
