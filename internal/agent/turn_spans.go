package agent

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"
)

type turnSpanContextKey struct{}

// TurnSpanMetadata identifies a daemon turn for production-safe latency spans.
type TurnSpanMetadata struct {
	SessionID string
	TurnID    string
	AgentID   string
	Channel   string
}

type turnSpanRecorder struct {
	meta          TurnSpanMetadata
	mu            sync.Mutex
	recallQueries map[string]int
}

// WithTurnSpanMetadata attaches daemon-turn span metadata to ctx.
func WithTurnSpanMetadata(ctx context.Context, meta TurnSpanMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, turnSpanContextKey{}, &turnSpanRecorder{meta: meta})
}

// EmitTurnSpan writes one structured daemon turn span log line.
func EmitTurnSpan(ctx context.Context, category string, duration time.Duration, fields map[string]any) {
	if ctx == nil {
		ctx = context.Background()
	}
	category = strings.TrimSpace(category)
	if category == "" {
		return
	}
	event := map[string]any{
		"event":       "daemon_turn_span",
		"category":    category,
		"duration_ms": duration.Milliseconds(),
		"duration_ns": duration.Nanoseconds(),
		"ts_unix_ms":  time.Now().UnixMilli(),
	}
	if rec, _ := ctx.Value(turnSpanContextKey{}).(*turnSpanRecorder); rec != nil {
		if rec.meta.SessionID != "" {
			event["session_id"] = rec.meta.SessionID
		}
		if rec.meta.TurnID != "" {
			event["turn_id"] = rec.meta.TurnID
		}
		if rec.meta.AgentID != "" {
			event["agent_id"] = rec.meta.AgentID
		}
		if rec.meta.Channel != "" {
			event["channel"] = rec.meta.Channel
		}
		if category == "memory_recall_query" {
			queryHash, _ := fields["query_hash"].(string)
			queryHash = strings.TrimSpace(queryHash)
			if queryHash != "" {
				rec.mu.Lock()
				if rec.recallQueries == nil {
					rec.recallQueries = make(map[string]int)
				}
				rec.recallQueries[queryHash]++
				count := rec.recallQueries[queryHash]
				rec.mu.Unlock()
				event["query_count_this_turn"] = count
				event["duplicate_query_this_turn"] = count > 1
			}
		}
	}
	for k, v := range fields {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		event[k] = v
	}
	b, err := json.Marshal(event)
	if err != nil {
		log.Printf("daemon_turn_span marshal failed category=%s err=%v", category, err)
		return
	}
	log.Printf("daemon_turn_span %s", string(b))
}
