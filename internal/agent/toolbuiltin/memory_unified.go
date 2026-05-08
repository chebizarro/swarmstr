package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/memory"
)

var MemoryQueryDef = agent.ToolDefinition{
	Name:        "memory_query",
	Description: "Query the unified typed memory store. Searches explicit memories, maintained session summaries, and durable markdown memories through one SQLite-backed index. USE THIS before answering questions about prior conversations, stored preferences, project decisions, remembered constraints, or tool lessons. Normal mode hides deleted/superseded/expired records; use mode=audit only when auditing memory history.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query":           map[string]any{"type": "string", "description": "Concrete search query."},
			"scope":           map[string]any{"description": "Optional scope or scopes: user, project, local, session, agent, team."},
			"types":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional memory types to include."},
			"tags":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional required tags."},
			"mode":            map[string]any{"type": "string", "enum": []any{"fast", "semantic", "deep", "recent", "audit"}, "description": "Query mode. MVP uses FTS5 for fast/deep/semantic and recent ordering for recent."},
			"limit":           map[string]any{"type": "integer", "description": "Max results, default 8, max 50."},
			"include_sources": map[string]any{"type": "boolean", "description": "Include source/provenance in results. Default true."},
		},
		"required": []any{"query"},
	},
}

var MemoryWriteDef = agent.ToolDefinition{
	Name:        "memory_write",
	Description: "Write a typed memory record. Use for curated durable facts, preferences, decisions, constraints, feedback, references, and reusable tool lessons. Salience heuristics reject low-information chatter unless durable=true or pinned=true. Durable or pinned records are also mirrored to markdown under the agent memory surface.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"text":       map[string]any{"type": "string", "description": "Canonical memory text."},
			"type":       map[string]any{"type": "string", "description": "preference|decision|constraint|fact|episode|tool_lesson|summary|artifact_ref|feedback|reference"},
			"scope":      map[string]any{"type": "string", "description": "user|project|local|session|agent|team"},
			"subject":    map[string]any{"type": "string", "description": "Short normalized subject, e.g. deployment or model-config."},
			"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"confidence": map[string]any{"type": "number", "description": "0.0-1.0 confidence."},
			"pinned":     map[string]any{"type": "boolean"},
			"durable":    map[string]any{"type": "boolean", "description": "If true, also write/update markdown."},
			"source":     map[string]any{"type": "object"},
			"supersedes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []any{"text"},
	},
}

var MemoryGetDef = agent.ToolDefinition{
	Name:        "memory_get",
	Description: "Return the full typed memory record by ID, including provenance and lifecycle metadata.",
	Parameters:  agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{"id": {Type: "string", Description: "Memory record ID."}}, Required: []string{"id"}},
}

var MemoryUpdateDef = agent.ToolDefinition{
	Name:            "memory_update",
	Description:     "Patch a typed memory record. If text meaning changes significantly, the store creates a replacement and marks the prior record superseded.",
	InputJSONSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"id": map[string]any{"type": "string"}, "patch": map[string]any{"type": "object"}}, "required": []any{"id", "patch"}},
}

var MemoryForgetDef = agent.ToolDefinition{
	Name:        "memory_forget",
	Description: "Forget a memory by soft-deleting it by default. Use tombstone for synced/auditable deletes or local_only for local hard delete when explicitly requested.",
	Parameters:  agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{"id": {Type: "string", Description: "Memory record ID."}, "mode": {Type: "string", Description: "soft_delete, tombstone, or local_only."}}, Required: []string{"id"}},
}

func MemoryQueryTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		scopeCtx := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
		q := memory.MemoryQuery{
			Query:          agent.ArgString(args, "query"),
			Scopes:         parseStringList(args["scope"]),
			Types:          parseStringList(args["types"]),
			Tags:           parseStringList(args["tags"]),
			Mode:           agent.ArgString(args, "mode"),
			Limit:          agent.ArgInt(args, "limit", 8),
			IncludeSources: true,
			SessionID:      scopeCtx.SessionID,
		}
		if len(q.Scopes) == 0 && scopeCtx.Enabled() {
			q.Scopes = []string{string(scopeCtx.Scope), memory.MemoryRecordScopeSession}
		}
		if v, ok := args["include_sources"].(bool); ok {
			q.IncludeSources = v
		}
		if q.Query == "" && q.Mode != "recent" {
			return "", fmt.Errorf("memory_query: query is required")
		}
		ingestKnownMemorySurfaces(ctx, store)
		cards, err := memory.QueryMemoryRecords(ctx, store, q)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(cards)
		return string(b), nil
	}
}

func MemorySearchCompatTool(store memory.Store) agent.ToolFunc {
	query := MemoryQueryTool(store)
	return func(ctx context.Context, args map[string]any) (string, error) {
		out, err := query(ctx, args)
		if err != nil {
			return "", err
		}
		var cards []memory.MemoryCard
		if json.Unmarshal([]byte(out), &cards) != nil {
			return out, nil
		}
		legacy := make([]map[string]any, 0, len(cards))
		for _, card := range cards {
			legacy = append(legacy, map[string]any{
				"memory_id":  card.ID,
				"type":       card.Type,
				"topic":      card.Subject,
				"text":       firstNonEmpty(card.Text, card.Summary),
				"keywords":   card.Tags,
				"confidence": card.Confidence,
				"source":     card.Source.Kind,
				"updated_at": card.UpdatedAt,
			})
		}
		b, _ := json.Marshal(legacy)
		return string(b), nil
	}
}

func MemoryWriteTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		text := strings.TrimSpace(agent.ArgString(args, "text"))
		if text == "" {
			return "", fmt.Errorf("memory_write: text is required")
		}
		tags := parseStringList(args["tags"])
		decision := memory.ClassifyMemorySalience(text, "agent", tags)
		pinned, _ := args["pinned"].(bool)
		durable, durableSet := args["durable"].(bool)
		if !durableSet {
			durable = pinned || decision.Durable || decision.Score >= memory.SalienceDurableThreshold
		}
		if !pinned && !durable && decision.Score < memory.SalienceDiscardThreshold {
			out, _ := json.Marshal(memory.MemoryWriteResult{Stored: false, Skipped: true, Reason: decision.Reason, Salience: decision})
			return string(out), nil
		}
		scopeCtx := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
		scope := agent.ArgString(args, "scope")
		if scope == "" {
			scope = string(scopeCtx.Scope)
		}
		if scope == "" {
			scope = memory.MemoryRecordScopeLocal
		}
		memType := agent.ArgString(args, "type")
		topic := strings.ToLower(strings.TrimSpace(agent.ArgString(args, "topic")))
		if memType == "" {
			switch topic {
			case "user":
				memType = memory.MemoryRecordTypePreference
			case "feedback":
				memType = memory.MemoryRecordTypeFeedback
			case "reference":
				memType = memory.MemoryRecordTypeReference
			case "project":
				memType = decision.ProposedType
			default:
				memType = decision.ProposedType
			}
		}
		confidence := 0.75
		if v, ok := anyFloat(args["confidence"]); ok {
			confidence = v
		}
		src := memory.MemorySource{Kind: memory.MemorySourceKindManual, SessionID: scopeCtx.SessionID}
		if raw, ok := args["source"].(map[string]any); ok {
			src = sourceFromMap(raw, src)
		}
		subject := agent.ArgString(args, "subject")
		if subject == "" {
			subject = topic
		}
		rec := memory.MemoryRecord{
			ID:         memory.NewMemoryRecordID(),
			Type:       memType,
			Scope:      scope,
			Subject:    subject,
			Text:       text,
			Tags:       tags,
			Confidence: confidence,
			Salience:   decision.Score,
			Source:     src,
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
			Pinned:     pinned,
			Supersedes: parseStringList(args["supersedes"]),
			Metadata:   map[string]any{"salience_reason": decision.Reason, "durable": durable},
		}
		if err := memory.WriteMemoryRecord(ctx, store, rec); err != nil {
			return "", err
		}
		if durable || pinned {
			if path, err := writeRecordToScopedMarkdown(scopeCtx, rec); err != nil {
				log.Printf("memory_write: durable markdown write failed id=%s err=%v", rec.ID, err)
			} else if path != "" {
				rec.Source.FilePath = path
				rec.Source.Kind = memory.MemorySourceKindFile
				_ = memory.WriteMemoryRecord(ctx, store, rec)
			}
		}
		out, _ := json.Marshal(memory.MemoryWriteResult{ID: rec.ID, Stored: true, Durable: durable, Pinned: pinned, Salience: decision})
		return string(out), nil
	}
}

func MemoryStoreCompatTool(store memory.Store) agent.ToolFunc {
	write := MemoryWriteTool(store)
	return func(ctx context.Context, args map[string]any) (string, error) {
		if _, ok := args["durable"]; !ok {
			args["durable"] = true
		}
		return write(ctx, args)
	}
}

func MemoryGetTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("memory_get: id is required")
		}
		rec, ok, err := memory.GetMemoryRecord(ctx, store, id)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("memory_get: record %q not found", id)
		}
		b, _ := json.Marshal(rec)
		return string(b), nil
	}
}

func MemoryUpdateTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		patch, _ := args["patch"].(map[string]any)
		if id == "" || patch == nil {
			return "", fmt.Errorf("memory_update: id and patch are required")
		}
		typed, ok := any(store).(interface {
			UpdateMemoryRecord(context.Context, string, map[string]any) (memory.MemoryRecord, error)
		})
		if !ok {
			return "", fmt.Errorf("memory_update: backend does not support typed updates")
		}
		rec, err := typed.UpdateMemoryRecord(ctx, id, patch)
		if err != nil {
			return "", err
		}
		if root := durableRootFromFile(rec.Source.FilePath); root != "" && rec.DeletedAt == nil {
			if _, fileErr := memory.WriteDurableMemoryFile(root, rec); fileErr != nil {
				log.Printf("memory_update: durable markdown update failed id=%s err=%v", rec.ID, fileErr)
			}
		}
		b, _ := json.Marshal(rec)
		return string(b), nil
	}
}

func MemoryForgetTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("memory_forget: id is required")
		}
		mode := agent.ArgString(args, "mode")
		rec, found, _ := memory.GetMemoryRecord(ctx, store, id)
		ok, err := memory.ForgetMemoryRecord(ctx, store, id, mode)
		if err != nil {
			return "", err
		}
		if ok && found && rec.Source.FilePath != "" && mode != "local_only" {
			if renameErr := os.Rename(rec.Source.FilePath, rec.Source.FilePath+".tombstone"); renameErr != nil && !os.IsNotExist(renameErr) {
				log.Printf("memory_forget: durable markdown tombstone failed id=%s err=%v", id, renameErr)
			} else if root := durableRootFromFile(rec.Source.FilePath); root != "" {
				_ = memory.GenerateMemoryEntrypoint(root)
			}
		}
		b, _ := json.Marshal(map[string]any{"id": id, "forgotten": ok, "mode": firstNonEmpty(mode, "soft_delete")})
		return string(b), nil
	}
}

func ingestKnownMemorySurfaces(ctx context.Context, store memory.Store) {
	scopeCtx := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
	if scopeCtx.WorkspaceDir != "" {
		_, _ = memory.IngestSessionMemoryFiles(ctx, store, scopeCtx.WorkspaceDir, scopeCtx.SessionID)
	}
	if scopeCtx.Enabled() {
		surface := memory.ResolveFileMemorySurface(scopeCtx, scopeCtx.WorkspaceDir)
		if surface.RootDir != "" {
			_, _ = memory.IngestDurableMemoryFiles(ctx, store, surface.RootDir)
		}
	}
}

func writeRecordToScopedMarkdown(scopeCtx memory.ScopedContext, rec memory.MemoryRecord) (string, error) {
	if !scopeCtx.Enabled() {
		return "", nil
	}
	surface := memory.ResolveFileMemorySurface(scopeCtx, scopeCtx.WorkspaceDir)
	if surface.RootDir == "" {
		return "", nil
	}
	return memory.WriteDurableMemoryFile(surface.RootDir, rec)
}

func parseStringList(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.Contains(t, ",") {
			return strings.Split(t, ",")
		}
		if strings.TrimSpace(t) != "" {
			return []string{t}
		}
	}
	return nil
}

func durableRootFromFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	categoryDir := filepath.Dir(path)
	root := filepath.Dir(categoryDir)
	if root == "." || root == string(filepath.Separator) {
		return ""
	}
	return root
}

func sourceFromMap(raw map[string]any, fallback memory.MemorySource) memory.MemorySource {
	src := fallback
	if s, ok := raw["kind"].(string); ok && s != "" {
		src.Kind = s
	}
	if s, ok := raw["ref"].(string); ok {
		src.Ref = s
	}
	if s, ok := raw["session_id"].(string); ok {
		src.SessionID = s
	}
	if s, ok := raw["event_id"].(string); ok {
		src.EventID = s
	}
	if s, ok := raw["file_path"].(string); ok {
		src.FilePath = s
	}
	if s, ok := raw["nostr_event_id"].(string); ok {
		src.NostrEventID = s
	}
	return src
}

func anyFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	default:
		return 0, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
