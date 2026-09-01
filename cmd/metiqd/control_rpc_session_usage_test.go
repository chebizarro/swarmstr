package main

import (
	"context"
	"encoding/json"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
)

func callSessionUsageRPCForTest(t *testing.T, h controlRPCHandler, method string, params map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	result, handled, err := h.handleSessionUsageRPC(context.Background(), nostruntime.ControlRPCInbound{Method: method, Params: raw, Internal: true}, method)
	if err != nil || !handled {
		t.Fatalf("%s: handled=%v err=%v", method, handled, err)
	}
	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("%s result type = %T", method, result.Result)
	}
	return payload
}

func TestSessionUsageSurfacesProjectDurableTurnHistory(t *testing.T) {
	docs, transcripts, sessions := newHistoryFixture(t)
	entries, err := transcripts.ListSessionAll(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	usageByID := map[string]map[string]any{
		"a1": {"input_tokens": int64(11), "output_tokens": int64(7), "cache_read_tokens": int64(3)},
		"a2": {"input_tokens": int64(5), "output_tokens": int64(2), "cache_creation_tokens": int64(4)},
	}
	for _, entry := range entries {
		usage, ok := usageByID[entry.EntryID]
		if !ok {
			continue
		}
		entry.Meta = map[string]any{"turn_result": map[string]any{"outcome": "completed", "stop_reason": "model_text", "usage": usage}}
		if _, err := transcripts.ReplaceEntry(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	}
	h := newControlRPCHandler(controlRPCDeps{docsRepo: docs, transcriptRepo: transcripts, sessionStore: sessions})

	summary := callSessionUsageRPCForTest(t, h, methods.MethodSessionsUsage, map[string]any{"key": "s1"})
	totals := summary["totals"].(usageTotals)
	if totals.Turns != 2 || totals.InputTokens != 16 || totals.OutputTokens != 9 || totals.CacheReadTokens != 3 || totals.CacheCreationToken != 4 || totals.TotalTokens != 32 {
		t.Fatalf("summary totals = %+v", totals)
	}
	if summary["source"] != "durable-transcript-turn-results" {
		t.Fatalf("summary source = %#v", summary["source"])
	}

	timeseries := callSessionUsageRPCForTest(t, h, methods.MethodSessionsUsageTimeseries, map[string]any{"key": "s1"})
	points := timeseries["points"].([]durableTurnUsage)
	if len(points) != 2 || points[0].EntryID != "a1" || points[1].TotalTokens != 11 {
		t.Fatalf("timeseries points = %+v", points)
	}
	logs := callSessionUsageRPCForTest(t, h, methods.MethodSessionsUsageLogs, map[string]any{"key": "s1", "limit": 1})
	rows := logs["logs"].([]durableTurnUsage)
	if len(rows) != 1 || rows[0].EntryID != "a2" || rows[0].Outcome != "completed" {
		t.Fatalf("logs = %+v", rows)
	}
}

func TestTranscriptTurnResultMetaPersistsAllUsageCounters(t *testing.T) {
	meta := transcriptTurnResultMeta(&agent.TurnResultMetadata{Usage: agent.TurnUsage{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4,
	}})
	usage := meta["usage"].(map[string]any)
	for key, want := range map[string]int64{"input_tokens": 1, "output_tokens": 2, "cache_read_tokens": 3, "cache_creation_tokens": 4} {
		if usage[key] != want {
			t.Errorf("%s = %#v, want %d", key, usage[key], want)
		}
	}
}
