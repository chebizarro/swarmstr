package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

type catalogCursor struct {
	Version int `json:"v"`
	Offset  int `json:"offset"`
}

func handleSessionsCatalogList(ctx context.Context, cfg state.ConfigDoc, docs *state.DocsRepository, store *state.SessionStore, req methods.SessionsCatalogListRequest) (methods.SessionsCatalogListResult, error) {
	if docs == nil || store == nil {
		return methods.SessionsCatalogListResult{}, fmt.Errorf("session catalog unavailable")
	}
	if len(req.HostIDs) > 0 {
		found := false
		for _, id := range req.HostIDs {
			if id == methods.SessionCatalogHostID {
				found = true
			}
		}
		if !found {
			return localCatalogResult(nil, ""), nil
		}
	}
	agentID := defaultAgentID(req.AgentID)
	type keyed struct {
		key   string
		entry state.SessionEntry
	}
	all := make([]keyed, 0)
	query := strings.ToLower(req.Search)
	for key, entry := range store.List() {
		if defaultAgentID(entry.AgentID) != agentID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{key, entry.SessionID, entry.AgentID, entry.Label, entry.Model, entry.ModelProvider}, " ")), query) {
			continue
		}
		doc, err := docs.GetSession(ctx, key)
		if err != nil || boolMeta(doc.Meta, "deleted") {
			continue
		}
		all = append(all, keyed{key: key, entry: entry})
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].entry.UpdatedAt.Equal(all[j].entry.UpdatedAt) {
			return all[i].entry.UpdatedAt.After(all[j].entry.UpdatedAt)
		}
		return all[i].key < all[j].key
	})
	offset := 0
	if req.Cursors != nil && req.Cursors[methods.SessionCatalogHostID] != "" {
		cursor, err := decodeCatalogCursor(req.Cursors[methods.SessionCatalogHostID])
		if err != nil {
			return methods.SessionsCatalogListResult{}, err
		}
		offset = cursor.Offset
	}
	if offset > len(all) {
		return methods.SessionsCatalogListResult{}, fmt.Errorf("invalid catalog cursor")
	}
	end := offset + req.LimitPerHost
	if end > len(all) {
		end = len(all)
	}
	rows := make([]methods.SessionCatalogSession, 0, end-offset)
	for _, item := range all[offset:end] {
		entry := item.entry
		status := "idle"
		if entry.Archived {
			status = "archived"
		} else if strings.TrimSpace(entry.ActiveRunID) != "" {
			status = "running"
		} else if entry.LastTurn != nil && (entry.LastTurn.Outcome == "failed" || entry.LastTurn.Outcome == "error") {
			status = "failed"
		}
		created, updated := entry.CreatedAt.UnixMilli(), entry.UpdatedAt.UnixMilli()
		if entry.CreatedAt.IsZero() {
			created = 0
		}
		if entry.UpdatedAt.IsZero() {
			updated = 0
		}
		rows = append(rows, methods.SessionCatalogSession{ThreadID: item.key, Name: entry.Label, CWD: workspace.ResolveSessionWorkspaceDir(cfg, entry), Status: status, CreatedAt: created, UpdatedAt: updated, RecencyAt: updated, Source: "metiq", ModelProvider: entry.ModelProvider, Archived: entry.Archived, SessionKey: item.key, CanContinue: true, CanArchive: !entry.Archived && strings.TrimSpace(entry.ActiveRunID) == ""})
	}
	next := ""
	if end < len(all) {
		next = encodeCatalogCursor(catalogCursor{Version: 1, Offset: end})
	}
	return localCatalogResult(rows, next), nil
}

func localCatalogResult(rows []methods.SessionCatalogSession, next string) methods.SessionsCatalogListResult {
	if rows == nil {
		rows = []methods.SessionCatalogSession{}
	}
	host := methods.SessionCatalogHost{HostID: methods.SessionCatalogHostID, Label: "This gateway", Kind: "gateway", Connected: true, Sessions: rows, NextCursor: next}
	catalog := methods.SessionCatalog{ID: methods.SessionCatalogID, Label: "Local sessions", Capabilities: methods.SessionCatalogCapabilities{ContinueSession: true, Archive: true}, Hosts: []methods.SessionCatalogHost{host}}
	return methods.SessionsCatalogListResult{Catalogs: []methods.SessionCatalog{catalog}}
}

func handleSessionsCatalogRead(ctx context.Context, docs *state.DocsRepository, transcripts *state.TranscriptRepository, store *state.SessionStore, req methods.SessionsCatalogReadRequest) (methods.SessionsCatalogReadResult, error) {
	entry, err := resolveCatalogSession(ctx, docs, store, req.ThreadID)
	if err != nil {
		return methods.SessionsCatalogReadResult{}, err
	}
	if transcripts == nil {
		return methods.SessionsCatalogReadResult{}, fmt.Errorf("transcript repository unavailable")
	}
	rows, err := transcripts.ListSessionAll(ctx, entry.SessionID)
	if err != nil {
		return methods.SessionsCatalogReadResult{}, err
	}
	offset := 0
	if req.Cursor != "" {
		cursor, err := decodeCatalogCursor(req.Cursor)
		if err != nil {
			return methods.SessionsCatalogReadResult{}, err
		}
		offset = cursor.Offset
	}
	if offset > len(rows) {
		return methods.SessionsCatalogReadResult{}, fmt.Errorf("invalid catalog cursor")
	}
	end := offset + req.Limit
	if end > len(rows) {
		end = len(rows)
	}
	items := make([]methods.SessionCatalogTranscriptItem, 0, end-offset)
	for _, row := range rows[offset:end] {
		kind := "other"
		switch row.Role {
		case "user":
			kind = "userMessage"
		case "assistant":
			kind = "agentMessage"
		case "tool":
			kind = "toolResult"
		}
		if row.Meta != nil {
			if _, ok := row.Meta["tool_name"]; ok {
				kind = "toolCall"
			}
			if value, _ := row.Meta["reasoning"].(bool); value {
				kind = "reasoning"
			}
		}
		timestamp := ""
		if row.Unix > 0 {
			timestamp = time.Unix(row.Unix, 0).UTC().Format(time.RFC3339)
		}
		items = append(items, methods.SessionCatalogTranscriptItem{ID: row.EntryID, Type: kind, Text: row.Text, Timestamp: timestamp})
	}
	next := ""
	if end < len(rows) {
		next = encodeCatalogCursor(catalogCursor{Version: 1, Offset: end})
	}
	return methods.SessionsCatalogReadResult{HostID: methods.SessionCatalogHostID, Label: entry.Label, ThreadID: req.ThreadID, Items: items, NextCursor: next}, nil
}

func handleSessionsCatalogContinue(ctx context.Context, docs *state.DocsRepository, store *state.SessionStore, req methods.SessionsCatalogLocatorRequest) (map[string]any, error) {
	if _, err := resolveCatalogSession(ctx, docs, store, req.ThreadID); err != nil {
		return nil, err
	}
	if _, err := store.SetArchived(req.ThreadID, false); err != nil {
		return nil, err
	}
	return map[string]any{"sessionKey": req.ThreadID}, nil
}

func handleSessionsCatalogArchive(ctx context.Context, docs *state.DocsRepository, store *state.SessionStore, req methods.SessionsCatalogArchiveRequest) (map[string]any, error) {
	entry, err := resolveCatalogSession(ctx, docs, store, req.ThreadID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(entry.ActiveRunID) != "" {
		return nil, fmt.Errorf("active session cannot be archived")
	}
	if _, err := store.SetArchived(req.ThreadID, true); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func resolveCatalogSession(ctx context.Context, docs *state.DocsRepository, store *state.SessionStore, key string) (state.SessionEntry, error) {
	if docs == nil || store == nil {
		return state.SessionEntry{}, fmt.Errorf("session catalog unavailable")
	}
	entry, ok := store.Get(key)
	if !ok {
		return state.SessionEntry{}, fmt.Errorf("unknown session")
	}
	doc, err := docs.GetSession(ctx, key)
	if err != nil || boolMeta(doc.Meta, "deleted") {
		return state.SessionEntry{}, fmt.Errorf("unknown session")
	}
	return entry, nil
}

func encodeCatalogCursor(cursor catalogCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeCatalogCursor(value string) (catalogCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return catalogCursor{}, fmt.Errorf("invalid catalog cursor")
	}
	var cursor catalogCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.Offset < 0 {
		return catalogCursor{}, fmt.Errorf("invalid catalog cursor")
	}
	return cursor, nil
}
