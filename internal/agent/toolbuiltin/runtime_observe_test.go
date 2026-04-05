package toolbuiltin

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRuntimeObserveToolReturnsStructuredEventsAndLogs(t *testing.T) {
	prev := runtimeObserveProvider
	defer SetRuntimeObserveProvider(prev)

	var observedReq RuntimeObserveRequest
	SetRuntimeObserveProvider(RuntimeObserveProvider{
		Observe: func(_ context.Context, req RuntimeObserveRequest) (map[string]any, error) {
			observedReq = req
			return map[string]any{
				"timed_out":       false,
				"wait_timeout_ms": req.WaitTimeoutMS,
				"events": map[string]any{
					"cursor": req.EventCursor + 2,
					"size":   3,
					"events": []map[string]any{{
						"id":         2,
						"event":      "tool.error",
						"agent_id":   req.Filters.AgentID,
						"session_id": req.Filters.SessionID,
						"subsystem":  req.Filters.Subsystem,
						"source":     req.Filters.Source,
					}},
					"max_bytes": req.MaxBytes,
					"limit":     req.EventLimit,
				},
				"logs": map[string]any{
					"cursor": req.LogCursor + 1,
					"lines":  []string{"123 [info] hello"},
					"limit":  req.LogLimit,
				},
			}, nil
		},
		TailEvents: func(cursor int64, limit int, maxBytes int, filters RuntimeObserveFilters) map[string]any {
			return map[string]any{
				"cursor": cursor + 2,
				"size":   3,
				"events": []map[string]any{{
					"id":         2,
					"event":      "tool.error",
					"agent_id":   filters.AgentID,
					"session_id": filters.SessionID,
				}},
				"max_bytes": maxBytes,
				"limit":     limit,
			}
		},
		TailLogs: func(cursor int64, limit int, maxBytes int) map[string]any {
			return map[string]any{
				"cursor": cursor + 1,
				"lines":  []string{"123 [info] hello"},
				"limit":  limit,
			}
		},
	})

	out, err := RuntimeObserveTool(context.Background(), map[string]any{
		"event_cursor":    int64(5),
		"log_cursor":      int64(9),
		"event_limit":     7,
		"log_limit":       4,
		"wait_timeout_ms": 500,
		"agent_id":        "wizard",
		"session_id":      "sess-1",
		"subsystem":       "chat",
		"source":          "inbound",
	})
	if err != nil {
		t.Fatalf("RuntimeObserveTool error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	events, ok := decoded["events"].(map[string]any)
	if !ok {
		t.Fatalf("missing events section: %#v", decoded)
	}
	if got := int(events["cursor"].(float64)); got != 7 {
		t.Fatalf("unexpected events cursor: %d", got)
	}
	logs, ok := decoded["logs"].(map[string]any)
	if !ok {
		t.Fatalf("missing logs section: %#v", decoded)
	}
	if got := int(logs["cursor"].(float64)); got != 10 {
		t.Fatalf("unexpected logs cursor: %d", got)
	}
	if got := int(decoded["wait_timeout_ms"].(float64)); got != 500 {
		t.Fatalf("unexpected wait timeout: %d", got)
	}
	if observedReq.Filters.Subsystem != "chat" || observedReq.Filters.Source != "inbound" {
		t.Fatalf("unexpected parsed filters: %+v", observedReq.Filters)
	}
}

func TestRuntimeObserveToolRejectsDisabledSections(t *testing.T) {
	if _, err := RuntimeObserveTool(context.Background(), map[string]any{
		"include_events": false,
		"include_logs":   false,
	}); err == nil {
		t.Fatal("expected error when both sections are disabled")
	}
}
