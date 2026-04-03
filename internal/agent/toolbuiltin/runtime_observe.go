package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"metiq/internal/agent"
)

type RuntimeObserveFilters struct {
	Events    []string
	AgentID   string
	SessionID string
	ChannelID string
	Direction string
	Subsystem string
	Source    string
}

type RuntimeObserveRequest struct {
	IncludeEvents bool
	IncludeLogs   bool
	EventCursor   int64
	LogCursor     int64
	EventLimit    int
	LogLimit      int
	MaxBytes      int
	WaitTimeoutMS int
	Filters       RuntimeObserveFilters
}

type RuntimeObserveProvider struct {
	Observe    func(context.Context, RuntimeObserveRequest) (map[string]any, error)
	TailEvents func(cursor int64, limit int, maxBytes int, filters RuntimeObserveFilters) map[string]any
	TailLogs   func(cursor int64, limit int, maxBytes int) map[string]any
}

var (
	runtimeObserveMu       sync.RWMutex
	runtimeObserveProvider RuntimeObserveProvider
)

func SetRuntimeObserveProvider(provider RuntimeObserveProvider) {
	runtimeObserveMu.Lock()
	defer runtimeObserveMu.Unlock()
	runtimeObserveProvider = provider
}

func ObserveRuntime(ctx context.Context, req RuntimeObserveRequest) (map[string]any, error) {
	runtimeObserveMu.RLock()
	provider := runtimeObserveProvider
	runtimeObserveMu.RUnlock()

	if provider.Observe != nil {
		return provider.Observe(ctx, req)
	}
	out := map[string]any{}
	if req.IncludeEvents {
		if provider.TailEvents == nil {
			return nil, fmt.Errorf("runtime_observe: event observer not configured")
		}
		out["events"] = provider.TailEvents(req.EventCursor, req.EventLimit, req.MaxBytes, req.Filters)
	}
	if req.IncludeLogs {
		if provider.TailLogs == nil {
			return nil, fmt.Errorf("runtime_observe: log observer not configured")
		}
		out["logs"] = provider.TailLogs(req.LogCursor, req.LogLimit, req.MaxBytes)
	}
	return out, nil
}

func RuntimeObserveTool(ctx context.Context, args map[string]any) (string, error) {
	includeEvents := runtimeObserveArgBool(args, "include_events", true)
	includeLogs := runtimeObserveArgBool(args, "include_logs", true)
	if !includeEvents && !includeLogs {
		return "", fmt.Errorf("runtime_observe: at least one of include_events or include_logs must be true")
	}

	maxBytes := agent.ArgInt(args, "max_bytes", 32*1024)
	if maxBytes <= 0 {
		maxBytes = 32 * 1024
	}
	if maxBytes > 256*1024 {
		maxBytes = 256 * 1024
	}
	waitTimeoutMS := agent.ArgInt(args, "wait_timeout_ms", 0)
	if waitTimeoutMS < 0 {
		waitTimeoutMS = 0
	}
	if waitTimeoutMS > 60_000 {
		waitTimeoutMS = 60_000
	}

	req := RuntimeObserveRequest{
		IncludeEvents: includeEvents,
		IncludeLogs:   includeLogs,
		EventCursor:   runtimeObserveArgInt64(args, "event_cursor"),
		LogCursor:     runtimeObserveArgInt64(args, "log_cursor"),
		EventLimit:    clampRuntimeObserveLimit(agent.ArgInt(args, "event_limit", 20)),
		LogLimit:      clampRuntimeObserveLimit(agent.ArgInt(args, "log_limit", 20)),
		MaxBytes:      maxBytes,
		WaitTimeoutMS: waitTimeoutMS,
		Filters: RuntimeObserveFilters{
			Events:    runtimeObserveArgStrings(args, "events"),
			AgentID:   strings.TrimSpace(agent.ArgString(args, "agent_id")),
			SessionID: strings.TrimSpace(agent.ArgString(args, "session_id")),
			ChannelID: strings.TrimSpace(agent.ArgString(args, "channel_id")),
			Direction: strings.TrimSpace(agent.ArgString(args, "direction")),
			Subsystem: strings.TrimSpace(agent.ArgString(args, "subsystem")),
			Source:    strings.TrimSpace(agent.ArgString(args, "source")),
		},
	}

	out, err := ObserveRuntime(ctx, req)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("runtime_observe: marshal response: %w", err)
	}
	return string(raw), nil
}

func clampRuntimeObserveLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func runtimeObserveArgInt64(args map[string]any, key string) int64 {
	switch v := args[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		i, _ := v.Int64()
		return i
	default:
		return 0
	}
}

func runtimeObserveArgBool(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func runtimeObserveArgStrings(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok {
		return nil
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		for _, item := range v {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

var RuntimeObserveDef = agent.ToolDefinition{
	Name:        "runtime_observe",
	Description: "Inspect recent daemon runtime activity without shell access. Returns structured recent events and/or runtime logs with cursors for incremental polling. Use to debug tool execution, DM/session flow, channel activity, and daemon state.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"include_events":  {Type: "boolean", Description: "Include structured runtime events. Defaults to true."},
			"include_logs":    {Type: "boolean", Description: "Include recent runtime log lines. Defaults to true."},
			"event_cursor":    {Type: "integer", Description: "Only return events with IDs greater than this cursor. Use the previous response's events.cursor for incremental polling."},
			"log_cursor":      {Type: "integer", Description: "Only return log lines newer than this cursor. Use the previous response's logs.cursor for incremental polling."},
			"event_limit":     {Type: "integer", Description: "Maximum number of events to return (1-200, default 20)."},
			"log_limit":       {Type: "integer", Description: "Maximum number of log lines to return (1-200, default 20)."},
			"max_bytes":       {Type: "integer", Description: "Maximum encoded bytes per events/logs section before truncation (default 32768, max 262144)."},
			"wait_timeout_ms": {Type: "integer", Description: "Optional long-poll timeout in milliseconds. When > 0, wait until new matching events/logs arrive or the timeout expires (max 60000)."},
			"events": {
				Type:        "array",
				Description: "Optional event-name filter, e.g. [\"tool.start\", \"tool.error\", \"turn.result\"].",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"agent_id":   {Type: "string", Description: "Optional filter for event payloads tied to an agent_id."},
			"session_id": {Type: "string", Description: "Optional filter for event payloads tied to a session_id."},
			"channel_id": {Type: "string", Description: "Optional filter for event payloads tied to a channel_id."},
			"direction":  {Type: "string", Description: "Optional filter for event payload direction, e.g. inbound or outbound."},
			"subsystem":  {Type: "string", Description: "Optional filter for structured runtime subsystem, e.g. dm, relay, tool, channel, chat, config."},
			"source":     {Type: "string", Description: "Optional filter for structured runtime source, e.g. reply, inbound, relay-monitor, nip17, dm."},
		},
	},
}
