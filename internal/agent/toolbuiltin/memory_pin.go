package toolbuiltin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"metiq/internal/agent"
	"metiq/internal/memory"
	"metiq/internal/store/state"
)

// agentKnowledgeTopic is the dedicated topic name for long-term pinned knowledge.
const agentKnowledgeTopic = "agent_knowledge"

// MemoryPinDef is the ToolDefinition for memory_pin.
var MemoryPinDef = agent.ToolDefinition{
	Name: "memory_pin",
	Description: "Persist stable long-term knowledge to your pinned memory. " +
		"Pinned entries are always included in your system prompt at the start of every turn, " +
		"so they persist across all sessions and conversations. " +
		"Use for stable facts, durable preferences, rules, or anything you always need to remember.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"text": {
				Type:        "string",
				Description: "The knowledge to pin (plain text, concise).",
			},
			"label": {
				Type:        "string",
				Description: "Short label for this entry (e.g. \"user_timezone\", \"project_rule\"). Used in memory_pinned listings.",
			},
		},
		Required: []string{"text"},
	},
}

// MemoryPinnedDef is the ToolDefinition for memory_pinned.
var MemoryPinnedDef = agent.ToolDefinition{
	Name:        "memory_pinned",
	Description: "List all entries in your pinned long-term knowledge base. Returns IDs and text so you can audit or remove outdated entries with memory_delete.",
	Parameters: agent.ToolParameters{
		Type:       "object",
		Properties: map[string]agent.ToolParamProp{},
	},
}

// MemoryPinTool returns an agent.ToolFunc for the "memory_pin" tool.
func MemoryPinTool(idx memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		text := agent.ArgString(args, "text")
		if text == "" {
			return "", fmt.Errorf("memory_pin: text is required")
		}
		label := agent.ArgString(args, "label")

		scope := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
		id := generatePinID()
		doc := state.MemoryDoc{
			MemoryID:  id,
			SessionID: scope.SessionID,
			Text:      text,
			Topic:     agentKnowledgeTopic,
			Keywords:  []string{agentKnowledgeTopic},
			Unix:      time.Now().Unix(),
		}
		doc = memory.ApplyScope(doc, scope)
		if label != "" {
			doc.Keywords = append(doc.Keywords, label)
		}
		idx.Add(doc)
		if saveErr := idx.Save(); saveErr != nil {
			log.Printf("memory_pin: index save failed: %v", saveErr)
		}

		out, _ := json.Marshal(map[string]any{"id": id, "pinned": true})
		return string(out), nil
	}
}

// MemoryPinnedTool returns an agent.ToolFunc for the "memory_pinned" tool.
func MemoryPinnedTool(idx memory.Store) agent.ToolFunc {
	return func(ctx context.Context, _ map[string]any) (string, error) {
		entries := memory.FilterByScope(idx.ListByTopic(agentKnowledgeTopic, 200), memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx)))
		type row struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, row{ID: e.MemoryID, Text: e.Text})
		}
		out, _ := json.Marshal(map[string]any{"pinned": rows, "count": len(rows)})
		return string(out), nil
	}
}

// generatePinID generates a random ID for pinned entries.
func generatePinID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("pin-%x", b)
}
