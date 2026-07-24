package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func listSessionCompactionCheckpoints(store *state.SessionStore, key string) (map[string]any, error) {
	if store == nil {
		return nil, fmt.Errorf("session store unavailable")
	}
	entry, ok := store.Get(key)
	if !ok {
		return map[string]any{"ok": true, "key": key, "checkpoints": []map[string]any{}}, nil
	}
	refs := append([]state.CompactionCheckpointRef(nil), entry.CompactionCheckpoints...)
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].CreatedAt == refs[j].CreatedAt {
			return refs[i].CheckpointID < refs[j].CheckpointID
		}
		return refs[i].CreatedAt > refs[j].CreatedAt
	})
	checkpoints := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		checkpoints = append(checkpoints, compactionCheckpointResponse(ref))
	}
	return map[string]any{"ok": true, "key": key, "checkpoints": checkpoints}, nil
}

func getSessionCompactionCheckpoint(store *state.SessionStore, key, checkpointID string) (map[string]any, error) {
	listed, err := listSessionCompactionCheckpoints(store, key)
	if err != nil {
		return nil, err
	}
	for _, cp := range listed["checkpoints"].([]map[string]any) {
		if cp["checkpointId"] == checkpointID {
			return map[string]any{"ok": true, "key": key, "checkpoint": cp}, nil
		}
	}
	return nil, fmt.Errorf("checkpoint %q not found for session %q", checkpointID, key)
}

func compactionCheckpointResponse(ref state.CompactionCheckpointRef) map[string]any {
	out := map[string]any{
		"checkpointId":   ref.CheckpointID,
		"sessionKey":     ref.SessionKey,
		"sessionId":      ref.SessionID,
		"createdAt":      ref.CreatedAt,
		"reason":         ref.Reason,
		"preCompaction":  compactionTranscriptReference(ref.PreCompaction, ref.SessionID),
		"postCompaction": compactionTranscriptReference(ref.PostCompaction, ref.SessionID),
	}
	if ref.TokensBefore > 0 {
		out["tokensBefore"] = ref.TokensBefore
	}
	if ref.TokensAfter > 0 {
		out["tokensAfter"] = ref.TokensAfter
	}
	if ref.Summary != "" {
		out["summary"] = ref.Summary
	}
	if ref.FirstKeptEntry != "" {
		out["firstKeptEntryId"] = ref.FirstKeptEntry
	}
	return out
}

func compactionTranscriptReference(src map[string]any, fallbackSessionID string) map[string]any {
	out := map[string]any{"sessionId": fallbackSessionID}
	aliases := map[string]string{
		"session_id":   "sessionId",
		"sessionId":    "sessionId",
		"session_file": "sessionFile",
		"sessionFile":  "sessionFile",
		"leaf_id":      "leafId",
		"leafId":       "leafId",
		"entry_id":     "entryId",
		"entryId":      "entryId",
	}
	for source, target := range aliases {
		if value, ok := src[source]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			out[target] = value
		}
	}
	return out
}

type sessionSearchHit struct {
	SessionKey string
	SessionID  string
	MessageID  string
	Role       string
	Timestamp  int64
	Snippet    string
	Score      float64
}

func searchSessionTranscripts(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, sessionStore *state.SessionStore, req methods.SessionsSearchRequest) (map[string]any, error) {
	if docsRepo == nil || transcriptRepo == nil {
		return nil, fmt.Errorf("session repositories unavailable")
	}
	keys := append([]string(nil), req.SessionKeys...)
	if len(keys) == 0 {
		docs, err := docsRepo.ListSessions(ctx, 1000)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			if req.AgentID != "" && sessionStore != nil {
				entry, ok := sessionStore.Get(doc.SessionID)
				if !ok || strings.TrimSpace(entry.AgentID) != req.AgentID {
					continue
				}
			}
			keys = append(keys, doc.SessionID)
		}
	}
	sort.Strings(keys)
	query := strings.ToLower(req.Query)
	hits := make([]sessionSearchHit, 0, req.Limit+1)
	for _, key := range keys {
		if req.AgentID != "" && sessionStore != nil {
			if entry, ok := sessionStore.Get(key); ok && strings.TrimSpace(entry.AgentID) != "" && strings.TrimSpace(entry.AgentID) != req.AgentID {
				continue
			}
		}
		entries, err := transcriptRepo.ListSessionAll(ctx, key)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Role != "user" && entry.Role != "assistant" {
				continue
			}
			lower := strings.ToLower(entry.Text)
			index := strings.Index(lower, query)
			if index < 0 {
				continue
			}
			occurrences := strings.Count(lower, query)
			hits = append(hits, sessionSearchHit{
				SessionKey: key,
				SessionID:  entry.SessionID,
				MessageID:  entry.EntryID,
				Role:       entry.Role,
				Timestamp:  entry.Unix,
				Snippet:    searchSnippet(entry.Text, index, len(req.Query), 240),
				Score:      float64(occurrences) + 1/float64(1+utf8.RuneCountInString(entry.Text)),
			})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Timestamp != hits[j].Timestamp {
			return hits[i].Timestamp > hits[j].Timestamp
		}
		if hits[i].SessionKey != hits[j].SessionKey {
			return hits[i].SessionKey < hits[j].SessionKey
		}
		return hits[i].MessageID < hits[j].MessageID
	})
	truncated := len(hits) > req.Limit
	if truncated {
		hits = hits[:req.Limit]
	}
	results := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		results = append(results, map[string]any{
			"sessionKey": hit.SessionKey,
			"sessionId":  hit.SessionID,
			"messageId":  hit.MessageID,
			"role":       hit.Role,
			"timestamp":  hit.Timestamp,
			"snippet":    hit.Snippet,
			"score":      hit.Score,
		})
	}
	out := map[string]any{"results": results}
	if truncated {
		out["truncated"] = true
	}
	return out, nil
}

func searchSnippet(text string, byteIndex, queryBytes, maxRunes int) string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	startByte := byteIndex
	for startByte > 0 && !utf8.RuneStart(text[startByte]) {
		startByte--
	}
	matchRune := utf8.RuneCountInString(text[:startByte])
	queryRunes := utf8.RuneCountInString(text[startByte:min(len(text), startByte+queryBytes)])
	runes := []rune(text)
	start := matchRune - (maxRunes-queryRunes)/2
	if start < 0 {
		start = 0
	}
	end := start + maxRunes
	if end > len(runes) {
		end = len(runes)
		start = max(0, end-maxRunes)
	}
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	return prefix + string(runes[start:end]) + suffix
}
