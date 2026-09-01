package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type durableTurnUsage struct {
	EntryID            string `json:"entryId"`
	Timestamp          int64  `json:"timestamp"`
	Role               string `json:"role"`
	Outcome            string `json:"outcome,omitempty"`
	StopReason         string `json:"stopReason,omitempty"`
	InputTokens        int64  `json:"inputTokens"`
	OutputTokens       int64  `json:"outputTokens"`
	CacheReadTokens    int64  `json:"cacheReadTokens"`
	CacheCreationToken int64  `json:"cacheCreationTokens"`
	TotalTokens        int64  `json:"totalTokens"`
}

type usageTotals struct {
	Turns              int   `json:"turns"`
	InputTokens        int64 `json:"inputTokens"`
	OutputTokens       int64 `json:"outputTokens"`
	CacheReadTokens    int64 `json:"cacheReadTokens"`
	CacheCreationToken int64 `json:"cacheCreationTokens"`
	TotalTokens        int64 `json:"totalTokens"`
}

func usageInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		out, _ := typed.Int64()
		return out
	default:
		return 0
	}
}

func usageObject(value any) map[string]any {
	if value == nil {
		return nil
	}
	if direct, ok := value.(map[string]any); ok {
		return direct
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func extractDurableTurnUsage(entry state.TranscriptEntryDoc) (durableTurnUsage, bool) {
	turnResult := usageObject(entry.Meta["turn_result"])
	usage := usageObject(turnResult["usage"])
	if len(usage) == 0 {
		return durableTurnUsage{}, false
	}
	record := durableTurnUsage{
		EntryID: entry.EntryID, Timestamp: entry.Unix, Role: entry.Role,
		Outcome: fmt.Sprint(turnResult["outcome"]), StopReason: fmt.Sprint(turnResult["stop_reason"]),
		InputTokens: usageInt64(usage["input_tokens"]), OutputTokens: usageInt64(usage["output_tokens"]),
		CacheReadTokens: usageInt64(usage["cache_read_tokens"]), CacheCreationToken: usageInt64(usage["cache_creation_tokens"]),
	}
	if record.Outcome == "<nil>" {
		record.Outcome = ""
	}
	if record.StopReason == "<nil>" {
		record.StopReason = ""
	}
	record.TotalTokens = record.InputTokens + record.OutputTokens + record.CacheReadTokens + record.CacheCreationToken
	return record, true
}

func addUsageTotals(totals *usageTotals, record durableTurnUsage) {
	totals.Turns++
	totals.InputTokens += record.InputTokens
	totals.OutputTokens += record.OutputTokens
	totals.CacheReadTokens += record.CacheReadTokens
	totals.CacheCreationToken += record.CacheCreationToken
	totals.TotalTokens += record.TotalTokens
}

func resolveUsageRange(req methods.SessionsUsageRequest, now time.Time) (int64, int64, string, string, error) {
	start := int64(0)
	end := int64(^uint64(0) >> 1)
	if req.StartDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", req.StartDate, time.UTC)
		if err != nil {
			return 0, 0, "", "", err
		}
		start = parsed.Unix()
	}
	if req.EndDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", req.EndDate, time.UTC)
		if err != nil {
			return 0, 0, "", "", err
		}
		end = parsed.Add(24*time.Hour).Unix() - 1
	}
	if req.StartDate == "" && req.EndDate == "" && req.Range != "" && req.Range != "all" {
		days := 7
		switch req.Range {
		case "30d":
			days = 30
		case "90d":
			days = 90
		case "1y":
			days = 365
		}
		start = now.AddDate(0, 0, -days).Unix()
		end = now.Unix()
	}
	if start > end {
		return 0, 0, "", "", fmt.Errorf("startDate must not be after endDate")
	}
	startLabel, endLabel := req.StartDate, req.EndDate
	if startLabel == "" && start > 0 {
		startLabel = time.Unix(start, 0).UTC().Format("2006-01-02")
	}
	if endLabel == "" && end < int64(^uint64(0)>>1) {
		endLabel = time.Unix(end, 0).UTC().Format("2006-01-02")
	}
	return start, end, startLabel, endLabel, nil
}

func sessionUsageRecords(ctx context.Context, transcripts *state.TranscriptRepository, sessionID string, start, end int64) ([]durableTurnUsage, usageTotals, error) {
	if transcripts == nil {
		return nil, usageTotals{}, fmt.Errorf("transcript repository unavailable")
	}
	entries, err := transcripts.ListSessionAll(ctx, sessionID)
	if err != nil {
		return nil, usageTotals{}, err
	}
	records := make([]durableTurnUsage, 0)
	var totals usageTotals
	for _, entry := range entries {
		if entry.Unix < start || entry.Unix > end {
			continue
		}
		record, ok := extractDurableTurnUsage(entry)
		if !ok {
			continue
		}
		records = append(records, record)
		addUsageTotals(&totals, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Timestamp != records[j].Timestamp {
			return records[i].Timestamp < records[j].Timestamp
		}
		return records[i].EntryID < records[j].EntryID
	})
	return records, totals, nil
}

func sessionDocAgentID(doc state.SessionDoc, sessions *state.SessionStore) string {
	if sessions != nil {
		if entry, ok := sessions.Get(doc.SessionID); ok {
			return strings.TrimSpace(entry.AgentID)
		}
	}
	if doc.Meta != nil {
		if value, ok := doc.Meta["agent_id"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (h controlRPCHandler) handleSessionUsageRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, bool, error) {
	if method != methods.MethodSessionsUsage && method != methods.MethodSessionsUsageTimeseries && method != methods.MethodSessionsUsageLogs {
		return nostruntime.ControlRPCResult{}, false, nil
	}
	if h.deps.docsRepo == nil || h.deps.transcriptRepo == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session usage history unavailable")
	}
	switch method {
	case methods.MethodSessionsUsage:
		req, err := methods.DecodeSessionsUsageParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		start, end, startLabel, endLabel, err := resolveUsageRange(req, time.Now())
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var docs []state.SessionDoc
		if req.Key != "" {
			doc, getErr := h.deps.docsRepo.GetSession(ctx, req.Key)
			if getErr != nil {
				return nostruntime.ControlRPCResult{}, true, getErr
			}
			docs = []state.SessionDoc{doc}
		} else {
			docs, err = h.deps.docsRepo.ListSessions(ctx, 5000)
			if err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
		}
		sessions := make([]map[string]any, 0, req.Limit)
		var totals usageTotals
		for _, doc := range docs {
			agentID := sessionDocAgentID(doc, h.deps.sessionStore)
			if req.AgentID != "" && agentID != req.AgentID {
				continue
			}
			records, sessionTotals, recordsErr := sessionUsageRecords(ctx, h.deps.transcriptRepo, doc.SessionID, start, end)
			if recordsErr != nil {
				return nostruntime.ControlRPCResult{}, true, recordsErr
			}
			if len(records) == 0 {
				continue
			}
			addAggregatedUsageTotals(&totals, sessionTotals)
			if len(sessions) < req.Limit {
				sessions = append(sessions, map[string]any{
					"key": doc.SessionID, "sessionId": doc.SessionID, "agentId": agentID,
					"updatedAt": records[len(records)-1].Timestamp * 1000, "usage": sessionTotals,
				})
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"updatedAt": time.Now().UnixMilli(), "startDate": startLabel, "endDate": endLabel,
			"sessions": sessions, "totals": totals, "source": "durable-transcript-turn-results",
		}}, true, nil
	case methods.MethodSessionsUsageTimeseries:
		req, err := methods.DecodeSessionsUsageTimeseriesParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if _, err := h.deps.docsRepo.GetSession(ctx, req.Key); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		records, totals, err := sessionUsageRecords(ctx, h.deps.transcriptRepo, req.Key, 0, int64(^uint64(0)>>1))
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if len(records) > 200 {
			records = records[len(records)-200:]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"key": req.Key, "sessionId": req.Key, "points": records, "totals": totals}}, true, nil
	case methods.MethodSessionsUsageLogs:
		req, err := methods.DecodeSessionsUsageLogsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if _, err := h.deps.docsRepo.GetSession(ctx, req.Key); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		records, _, err := sessionUsageRecords(ctx, h.deps.transcriptRepo, req.Key, 0, int64(^uint64(0)>>1))
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if len(records) > req.Limit {
			records = records[len(records)-req.Limit:]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"logs": records}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, false, nil
}

func addAggregatedUsageTotals(dst *usageTotals, src usageTotals) {
	dst.Turns += src.Turns
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheCreationToken += src.CacheCreationToken
	dst.TotalTokens += src.TotalTokens
}
