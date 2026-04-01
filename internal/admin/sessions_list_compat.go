package admin

import (
	"context"
	"sort"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

type SessionsListResponseOptions struct {
	Config         state.ConfigDoc
	SessionStore   *state.SessionStore
	ListSessions   func(context.Context, int) ([]state.SessionDoc, error)
	ListTranscript func(context.Context, string, int) ([]state.TranscriptEntryDoc, error)
	Path           string
}

type sessionTranscriptSummary struct {
	firstUserMessage   string
	lastMessagePreview string
}

func BuildSessionsListResponse(ctx context.Context, req methods.SessionsListRequest, opts SessionsListResponseOptions) (map[string]any, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" && opts.SessionStore != nil {
		path = strings.TrimSpace(opts.SessionStore.Path())
	}
	if path == "" {
		path = "nostr://state/sessions"
	}

	docsByID := map[string]state.SessionDoc{}
	if opts.ListSessions != nil {
		loadLimit := req.Limit
		if loadLimit < 2000 {
			loadLimit = 2000
		}
		sessions, err := opts.ListSessions(ctx, loadLimit)
		if err != nil {
			return nil, err
		}
		for _, doc := range sessions {
			sessionID := strings.TrimSpace(doc.SessionID)
			if sessionID == "" || sessionDocDeleted(doc) {
				continue
			}
			if prior, ok := docsByID[sessionID]; !ok || sessionDocActivityMS(doc) > sessionDocActivityMS(prior) {
				docsByID[sessionID] = doc
			}
		}
	}

	storeEntries := map[string]state.SessionEntry{}
	if opts.SessionStore != nil {
		storeEntries = opts.SessionStore.List()
	}

	keys := map[string]struct{}{}
	for key, entry := range storeEntries {
		key = strings.TrimSpace(key)
		if key == "" {
			key = strings.TrimSpace(entry.SessionID)
		}
		if key == "" {
			continue
		}
		keys[key] = struct{}{}
	}
	for sessionID := range docsByID {
		keys[sessionID] = struct{}{}
	}

	transcriptCache := map[string]sessionTranscriptSummary{}
	rows := make([]map[string]any, 0, len(keys))
	for key := range keys {
		entry, hasEntry := storeEntries[key]
		sessionID := strings.TrimSpace(entry.SessionID)
		if sessionID == "" {
			sessionID = key
		}
		doc, hasDoc := docsByID[sessionID]
		if !hasDoc {
			doc, hasDoc = docsByID[key]
		}
		if hasDoc && sessionDocDeleted(doc) {
			continue
		}

		if !req.IncludeGlobal && key == "global" {
			continue
		}
		if !req.IncludeUnknown && key == "unknown" {
			continue
		}

		agentID := firstNonEmpty(
			strings.TrimSpace(entry.AgentID),
			parseAgentIDFromSessionKey(key),
			sessionMetaString(doc.Meta, "agent_id"),
		)
		if agentID == "" && key != "global" && key != "unknown" {
			agentID = "main"
		}
		agentID = normalizeCompatAgentID(agentID)
		if req.AgentID != "" {
			if key == "global" || key == "unknown" || agentID != normalizeCompatAgentID(req.AgentID) {
				continue
			}
		}

		spawnedBy := firstNonEmpty(strings.TrimSpace(entry.SpawnedBy), sessionMetaString(doc.Meta, "spawned_by"))
		if req.SpawnedBy != "" && spawnedBy != strings.TrimSpace(req.SpawnedBy) {
			continue
		}

		label := firstNonEmpty(strings.TrimSpace(entry.Label), sessionMetaString(doc.Meta, "label"))
		if req.Label != "" && label != strings.TrimSpace(req.Label) {
			continue
		}

		updatedAtMS := sessionEntryUpdatedAtMS(entry)
		if updatedAtMS == 0 {
			updatedAtMS = sessionDocActivityMS(doc)
		}
		if req.ActiveMinutes > 0 {
			cutoff := time.Now().Add(-time.Duration(req.ActiveMinutes) * time.Minute).UnixMilli()
			if updatedAtMS < cutoff {
				continue
			}
		}

		row := map[string]any{
			"key":              key,
			"sessionId":        firstNonEmpty(sessionID, strings.TrimSpace(doc.SessionID)),
			"updatedAt":        updatedAtMS,
			"displayName":      firstNonEmpty(label, firstNonEmpty(sessionID, key)),
			"totalTokensFresh": false,
		}
		if spawnedBy != "" {
			row["spawnedBy"] = spawnedBy
		}
		if label != "" {
			row["label"] = label
		}
		if agentID != "" && key != "global" && key != "unknown" {
			row["agentId"] = agentID
		}
		if hasDoc {
			if doc.LastInboundAt > 0 {
				row["lastInboundAt"] = doc.LastInboundAt
			}
			if doc.LastReplyAt > 0 {
				row["lastReplyAt"] = doc.LastReplyAt
			}
		}
		if hasEntry {
			if entry.ThinkingLevel != "" {
				row["thinkingLevel"] = entry.ThinkingLevel
			}
			if entry.VerboseLevel != "" {
				row["verboseLevel"] = entry.VerboseLevel
			}
			if entry.ReasoningLevel != "" {
				row["reasoningLevel"] = entry.ReasoningLevel
			}
			if entry.ResponseUsage != "" {
				row["responseUsage"] = entry.ResponseUsage
			}
			if entry.FastMode {
				row["fastMode"] = true
			}
			if entry.InputTokens != 0 {
				row["inputTokens"] = entry.InputTokens
			}
			if entry.OutputTokens != 0 {
				row["outputTokens"] = entry.OutputTokens
			}
			if entry.TotalTokens != 0 {
				row["totalTokens"] = entry.TotalTokens
			}
			if entry.TotalTokensFresh != nil {
				row["totalTokensFresh"] = *entry.TotalTokensFresh
			}
			if entry.ContextTokens != 0 {
				row["contextTokens"] = entry.ContextTokens
			}
			if entry.CacheRead != 0 {
				row["cacheRead"] = entry.CacheRead
			}
			if entry.CacheWrite != 0 {
				row["cacheWrite"] = entry.CacheWrite
			}
			if entry.LastChannel != "" {
				row["lastChannel"] = entry.LastChannel
			}
			if entry.LastTo != "" {
				row["lastTo"] = entry.LastTo
			}
			if entry.LastAccountID != "" {
				row["lastAccountId"] = entry.LastAccountID
			}
			if entry.LastThreadID != "" {
				row["lastThreadId"] = entry.LastThreadID
			}
			if delivery := buildDeliveryContext(entry); len(delivery) > 0 {
				row["deliveryContext"] = delivery
			}
			modelProvider, model := resolveSessionModelValues(entry, opts.Config)
			if modelProvider != "" {
				row["modelProvider"] = modelProvider
			}
			if model != "" {
				row["model"] = model
			}
		}

		if sessionID != "" && (req.IncludeDerivedTitles || req.IncludeLastMessage) && opts.ListTranscript != nil {
			summary, ok := transcriptCache[sessionID]
			if !ok {
				summary = summarizeSessionTranscript(ctx, opts.ListTranscript, sessionID)
				transcriptCache[sessionID] = summary
			}
			if req.IncludeDerivedTitles {
				if derived := deriveSessionTitle(summary.firstUserMessage); derived != "" {
					row["derivedTitle"] = derived
				}
			}
			if req.IncludeLastMessage && summary.lastMessagePreview != "" {
				row["lastMessagePreview"] = summary.lastMessagePreview
			}
		}

		if req.Search != "" && !sessionRowMatchesSearch(row, strings.ToLower(strings.TrimSpace(req.Search))) {
			continue
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		ai, _ := rows[i]["updatedAt"].(int64)
		aj, _ := rows[j]["updatedAt"].(int64)
		if ai == aj {
			ki, _ := rows[i]["key"].(string)
			kj, _ := rows[j]["key"].(string)
			return ki < kj
		}
		return ai > aj
	})

	if req.Limit > 0 && len(rows) > req.Limit {
		rows = rows[:req.Limit]
	}

	defaults := map[string]any{"modelProvider": nil, "model": nil, "contextTokens": nil}
	if model := strings.TrimSpace(opts.Config.Agent.DefaultModel); model != "" {
		defaults["model"] = model
	}

	return map[string]any{
		"ts":       time.Now().UnixMilli(),
		"path":     path,
		"count":    len(rows),
		"total":    len(rows),
		"defaults": defaults,
		"sessions": rows,
	}, nil
}

func buildDeliveryContext(entry state.SessionEntry) map[string]any {
	delivery := map[string]any{}
	if entry.LastChannel != "" {
		delivery["channel"] = entry.LastChannel
	}
	if entry.LastTo != "" {
		delivery["to"] = entry.LastTo
	}
	if entry.LastAccountID != "" {
		delivery["accountId"] = entry.LastAccountID
	}
	if entry.LastThreadID != "" {
		delivery["threadId"] = entry.LastThreadID
	}
	return delivery
}

func resolveSessionModelValues(entry state.SessionEntry, cfg state.ConfigDoc) (string, string) {
	provider := strings.TrimSpace(entry.ProviderOverride)
	if provider == "" {
		provider = strings.TrimSpace(entry.ModelProvider)
	}
	model := strings.TrimSpace(entry.ModelOverride)
	if model == "" {
		model = strings.TrimSpace(entry.Model)
	}
	if model == "" {
		model = strings.TrimSpace(cfg.Agent.DefaultModel)
	}
	return provider, model
}

func summarizeSessionTranscript(ctx context.Context, listFn func(context.Context, string, int) ([]state.TranscriptEntryDoc, error), sessionID string) sessionTranscriptSummary {
	entries, err := listFn(ctx, sessionID, 2000)
	if err != nil {
		return sessionTranscriptSummary{}
	}
	out := sessionTranscriptSummary{}
	for _, entry := range entries {
		text := compactText(entry.Text)
		if text == "" {
			continue
		}
		if out.firstUserMessage == "" && strings.EqualFold(entry.Role, "user") {
			out.firstUserMessage = text
		}
		out.lastMessagePreview = text
	}
	out.firstUserMessage = truncateText(out.firstUserMessage, 80)
	out.lastMessagePreview = truncateText(out.lastMessagePreview, 140)
	return out
}

func deriveSessionTitle(text string) string {
	return truncateText(compactText(text), 80)
}

func compactText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func truncateText(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

func sessionRowMatchesSearch(row map[string]any, query string) bool {
	for _, key := range []string{"displayName", "label", "sessionId", "key", "lastTo", "lastAccountId", "derivedTitle", "lastMessagePreview"} {
		value, _ := row[key].(string)
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func sessionDocDeleted(doc state.SessionDoc) bool {
	if doc.Meta == nil {
		return false
	}
	deleted, _ := doc.Meta["deleted"].(bool)
	return deleted
}

func sessionDocActivityMS(doc state.SessionDoc) int64 {
	activity := doc.LastReplyAt
	if doc.LastInboundAt > activity {
		activity = doc.LastInboundAt
	}
	return activity * 1000
}

func sessionEntryUpdatedAtMS(entry state.SessionEntry) int64 {
	if entry.UpdatedAt.IsZero() {
		return 0
	}
	return entry.UpdatedAt.UnixMilli()
}

func normalizeCompatAgentID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if len([]rune(id)) > 64 {
		id = string([]rune(id)[:64])
	}
	return id
}

func parseAgentIDFromSessionKey(key string) string {
	parts := strings.Split(strings.TrimSpace(key), ":")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "agent") {
		return ""
	}
	return parts[1]
}

func sessionMetaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
