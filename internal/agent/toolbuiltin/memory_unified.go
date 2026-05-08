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
			"token_budget":    map[string]any{"type": "integer", "description": "Optional soft token budget for returned memory text+summaries."},
			"include_sources": map[string]any{"type": "boolean", "description": "Include source/provenance in results. Default true."},
			"include_debug":   map[string]any{"type": "boolean", "description": "Include per-result retrieval explanations under why."},
			"ranking_weights": map[string]any{"type": "object", "description": "Optional scoring weights for bm25, recency, salience, confidence, pinned, durable, type_match, and scope_match."},
		},
		"required": []any{"query"},
	},
}

var MemoryStatsDef = agent.ToolDefinition{
	Name:        "memory_stats",
	Description: "Return stable JSON counts for the unified memory store, including lifecycle, type, scope, pinned, durable, and session totals.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	},
}

var MemoryHealthDef = agent.ToolDefinition{
	Name:        "memory_health",
	Description: "Diagnose memory store health and index consistency. Returns issue counts and small samples for expired active records, missing supersession targets, duplicate hashes, and FTS drift.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	},
}

var MemoryExplainQueryDef = agent.ToolDefinition{
	Name:        "memory_explain_query",
	Description: "Explain how memory_query would route and rank a query. Returns intent, effective filters, ranking weights, result why metadata, candidate counts, and exclusion samples.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query":           map[string]any{"type": "string", "description": "Concrete search query."},
			"scope":           map[string]any{"description": "Optional scope or scopes: user, project, local, session, agent, team."},
			"types":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional memory types to include."},
			"tags":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional required tags."},
			"mode":            map[string]any{"type": "string", "enum": []any{"fast", "semantic", "deep", "recent", "audit"}},
			"limit":           map[string]any{"type": "integer", "description": "Max results, default 8, max 50."},
			"include_sources": map[string]any{"type": "boolean"},
			"ranking_weights": map[string]any{"type": "object"},
		},
		"required": []any{"query"},
	},
}

var MemoryReflectDef = agent.ToolDefinition{
	Name:        "memory_reflect",
	Description: "Inspect recent episode, tool, and summary memories and persist reviewable durable-memory candidates. Returns source IDs, reasons, confidence, and proposed actions; it does not promote automatically.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "Optional source session to inspect. Defaults to the current session when available."},
			"scope":      map[string]any{"description": "Optional scope or scopes to inspect: user, project, local, session, agent, team."},
			"since":      map[string]any{"type": "string", "description": "Optional RFC3339 timestamp or duration like 24h/7d. Defaults to 7d."},
			"limit":      map[string]any{"type": "integer", "description": "Max recent records/candidates, default 50, max 200."},
			"mode":       map[string]any{"type": "string", "enum": []any{"review", "strict", "broad"}, "description": "Heuristic mode label for review workflows. Default review."},
		},
	},
}

var MemoryApplyReflectionDef = agent.ToolDefinition{
	Name:        "memory_apply_reflection",
	Description: "Apply one reviewed reflection candidate. Default action uses the candidate's proposed action; promote/supersede create typed durable records and markdown when a scoped memory surface is available, merge updates a related target, ignore records rejection.",
	InputJSONSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"candidate_id": map[string]any{"type": "string", "description": "Reflection candidate ID returned by memory_reflect."},
			"action":       map[string]any{"type": "string", "enum": []any{"promote", "merge", "supersede", "ignore"}, "description": "Optional reviewed action override."},
			"target_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional reviewed merge/supersede target overrides."},
			"durable":      map[string]any{"type": "boolean", "description": "Optional override for durable markdown write."},
		},
		"required": []any{"candidate_id"},
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
			TokenBudget:    agent.ArgInt(args, "token_budget", 0),
			IncludeSources: true,
			IncludeDebug:   argBool(args, "include_debug", false),
			RankingWeights: parseRankingWeights(args["ranking_weights"]),
			SessionID:      scopeCtx.SessionID,
			ExplicitScopes: argPresent(args, "scope"),
			ExplicitTypes:  argPresent(args, "types"),
			ExplicitMode:   argPresent(args, "mode"),
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

func MemoryStatsTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		_ = args
		ingestKnownMemorySurfaces(ctx, store)
		report, err := memory.MemoryStats(ctx, store)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(report)
		return string(b), nil
	}
}

func MemoryHealthTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		_ = args
		ingestKnownMemorySurfaces(ctx, store)
		report, err := memory.MemoryHealth(ctx, store)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(report)
		return string(b), nil
	}
}

func MemoryExplainQueryTool(store memory.Store) agent.ToolFunc {
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
			IncludeDebug:   true,
			RankingWeights: parseRankingWeights(args["ranking_weights"]),
			SessionID:      scopeCtx.SessionID,
			ExplicitScopes: argPresent(args, "scope"),
			ExplicitTypes:  argPresent(args, "types"),
			ExplicitMode:   argPresent(args, "mode"),
		}
		if len(q.Scopes) == 0 && scopeCtx.Enabled() {
			q.Scopes = []string{string(scopeCtx.Scope), memory.MemoryRecordScopeSession}
		}
		if v, ok := args["include_sources"].(bool); ok {
			q.IncludeSources = v
		}
		if q.Query == "" && q.Mode != "recent" {
			return "", fmt.Errorf("memory_explain_query: query is required")
		}
		ingestKnownMemorySurfaces(ctx, store)
		explain, err := memory.ExplainMemoryQuery(ctx, store, q)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(explain)
		return string(b), nil
	}
}

func MemoryReflectTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		scopeCtx := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
		req := memory.MemoryReflectRequest{
			SessionID: firstNonEmpty(agent.ArgString(args, "session_id"), scopeCtx.SessionID),
			Scopes:    parseStringList(args["scope"]),
			Since:     parseReflectionSince(agent.ArgString(args, "since")),
			Limit:     agent.ArgInt(args, "limit", 50),
			Mode:      agent.ArgString(args, "mode"),
		}
		if len(req.Scopes) == 0 && scopeCtx.Enabled() {
			req.Scopes = []string{string(scopeCtx.Scope), memory.MemoryRecordScopeSession}
		}
		ingestKnownMemorySurfaces(ctx, store)
		result, err := memory.MemoryReflect(ctx, store, req)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

func MemoryApplyReflectionTool(store memory.Store) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		candidateID := agent.ArgString(args, "candidate_id")
		if strings.TrimSpace(candidateID) == "" {
			return "", fmt.Errorf("memory_apply_reflection: candidate_id is required")
		}
		scopeCtx := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
		durableRoot := ""
		if scopeCtx.Enabled() {
			surface := memory.ResolveFileMemorySurface(scopeCtx, scopeCtx.WorkspaceDir)
			durableRoot = surface.RootDir
		}
		req := memory.MemoryApplyReflectionRequest{
			CandidateID: candidateID,
			Action:      agent.ArgString(args, "action"),
			TargetIDs:   parseStringList(args["target_ids"]),
			DurableRoot: durableRoot,
		}
		if v, ok := args["durable"].(bool); ok {
			req.Durable = &v
		}
		result, err := memory.MemoryApplyReflection(ctx, store, req)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
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

func parseReflectionSince(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	if strings.HasSuffix(raw, "d") {
		days, err := time.ParseDuration(strings.TrimSuffix(raw, "d") + "h")
		if err == nil {
			return time.Now().UTC().Add(-days * 24)
		}
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().UTC().Add(-d)
	}
	return time.Time{}
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

func argPresent(args map[string]any, key string) bool {
	_, ok := args[key]
	return ok
}

func argBool(args map[string]any, key string, fallback bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return fallback
}

func parseRankingWeights(v any) *memory.MemoryRankingWeights {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	wv := memory.DefaultMemoryRankingWeights()
	w := &wv
	if f, ok := anyFloat(raw["bm25"]); ok {
		w.BM25 = f
	}
	if f, ok := anyFloat(raw["recency"]); ok {
		w.Recency = f
	}
	if f, ok := anyFloat(raw["salience"]); ok {
		w.Salience = f
	}
	if f, ok := anyFloat(raw["confidence"]); ok {
		w.Confidence = f
	}
	if f, ok := anyFloat(raw["pinned"]); ok {
		w.Pinned = f
	}
	if f, ok := anyFloat(raw["durable"]); ok {
		w.Durable = f
	}
	if f, ok := anyFloat(raw["type_match"]); ok {
		w.TypeMatch = f
	}
	if f, ok := anyFloat(raw["scope_match"]); ok {
		w.ScopeMatch = f
	}
	return w
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
