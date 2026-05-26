package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

func runObserve(args []string) error {
	fs := flag.NewFlagSet("observe", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var transport, controlTargetPubKey, controlSignerURL string
	var timeoutSec int
	var includeEvents, includeLogs, stream bool
	var eventCursor, logCursor int64
	var eventLimit, logLimit, maxBytes, maxBatches int
	var waitRaw string
	var agentID, sessionID, channelID, direction, subsystem, source string
	var eventNames csvListFlag
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&transport, "transport", "auto", "gateway transport: auto, http, or nostr")
	fs.StringVar(&controlTargetPubKey, "control-target-pubkey", "", "target daemon pubkey for Nostr control RPC")
	fs.StringVar(&controlSignerURL, "control-signer-url", "", "caller signer override for Nostr control RPC")
	fs.IntVar(&timeoutSec, "timeout", 30, "request timeout seconds")
	fs.BoolVar(&includeEvents, "include-events", true, "include structured runtime events")
	fs.BoolVar(&includeLogs, "include-logs", true, "include runtime log tail")
	fs.BoolVar(&stream, "stream", false, "stream runtime activity as newline-delimited JSON")
	fs.BoolVar(&stream, "follow", false, "alias for --stream")
	fs.Int64Var(&eventCursor, "event-cursor", 0, "resume events after this cursor")
	fs.Int64Var(&logCursor, "log-cursor", 0, "resume logs after this cursor")
	fs.IntVar(&eventLimit, "event-limit", 20, "maximum number of events to return")
	fs.IntVar(&logLimit, "log-limit", 20, "maximum number of log lines to return")
	fs.IntVar(&maxBytes, "max-bytes", 32*1024, "response size cap in bytes")
	fs.IntVar(&maxBatches, "max-batches", 0, "maximum stream batches before exiting (0 means until interrupted)")
	fs.StringVar(&waitRaw, "wait", "", "long-poll for changes (duration like 15s, 500ms, or integer milliseconds)")
	fs.Var(&eventNames, "event", "filter by event name (repeatable or comma-separated)")
	fs.StringVar(&agentID, "agent", "", "filter events by agent ID")
	fs.StringVar(&sessionID, "session", "", "filter events by session ID")
	fs.StringVar(&channelID, "channel", "", "filter events by channel ID")
	fs.StringVar(&direction, "direction", "", "filter events by direction (inbound|outbound)")
	fs.StringVar(&subsystem, "subsystem", "", "filter events by subsystem (relay|dm|tool|session|chat|channel|config|agent|cron|voice|update|plugin|node|device|exec|canvas)")
	fs.StringVar(&source, "source", "", "filter events by source (for example inbound, reply, stream)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !includeEvents && !includeLogs {
		return fmt.Errorf("at least one of --include-events or --include-logs must be true")
	}
	waitTimeoutMS, err := parseObserveWait(waitRaw)
	if err != nil {
		return err
	}
	if stream && waitTimeoutMS == 0 {
		waitTimeoutMS = 15_000
	}

	timeout := time.Duration(timeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if waitTimeoutMS > 0 {
		minTimeout := time.Duration(waitTimeoutMS)*time.Millisecond + 5*time.Second
		if timeout < minTimeout {
			timeout = minTimeout
		}
	}
	cl, err := resolveGWClientFn(transport, adminAddr, adminToken, bootstrapPath, controlTargetPubKey, controlSignerURL, timeout)
	if err != nil {
		return err
	}
	if closer, ok := cl.(gatewayCloser); ok {
		defer closer.Close()
	}
	if admin, ok := cl.(*adminClient); ok {
		admin.timeout = timeout
	}

	params := map[string]any{
		"include_events": includeEvents,
		"include_logs":   includeLogs,
	}
	if eventCursor > 0 {
		params["event_cursor"] = eventCursor
	}
	if logCursor > 0 {
		params["log_cursor"] = logCursor
	}
	if eventLimit > 0 {
		params["event_limit"] = eventLimit
	}
	if logLimit > 0 {
		params["log_limit"] = logLimit
	}
	if maxBytes > 0 {
		params["max_bytes"] = maxBytes
	}
	if waitTimeoutMS > 0 {
		params["wait_timeout_ms"] = waitTimeoutMS
	}
	if len(eventNames) > 0 {
		params["events"] = []string(eventNames)
	}
	if agentID = strings.TrimSpace(agentID); agentID != "" {
		params["agent_id"] = agentID
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		params["session_id"] = sessionID
	}
	if channelID = strings.TrimSpace(channelID); channelID != "" {
		params["channel_id"] = channelID
	}
	if direction = strings.TrimSpace(direction); direction != "" {
		params["direction"] = direction
	}
	if subsystem = strings.TrimSpace(subsystem); subsystem != "" {
		params["subsystem"] = subsystem
	}
	if source = strings.TrimSpace(source); source != "" {
		params["source"] = source
	}

	if stream {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return streamObserve(ctx, cl, params, observeStreamOptions{MaxBatches: maxBatches})
	}

	result, err := cl.call("runtime.observe", params)
	if err != nil {
		return err
	}
	return printJSON(result)
}

type observeStreamOptions struct {
	MaxBatches int
	LogFilter  string
}

func streamObserve(ctx context.Context, cl gatewayCaller, params map[string]any, opts observeStreamOptions) error {
	enc := json.NewEncoder(os.Stdout)
	batches := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		result, err := cl.call("runtime.observe", params)
		if err != nil {
			return err
		}
		if err := emitObserveNDJSON(enc, result, opts.LogFilter); err != nil {
			return err
		}
		advanceObserveCursor(params, result, "events", "event_cursor")
		advanceObserveCursor(params, result, "logs", "log_cursor")

		batches++
		if opts.MaxBatches > 0 && batches >= opts.MaxBatches {
			return nil
		}
	}
}

func emitObserveNDJSON(enc *json.Encoder, result map[string]any, logFilter string) error {
	if section, ok := result["events"].(map[string]any); ok {
		cursor := int64FromAny(section["cursor"])
		for _, item := range observeItems(section["events"]) {
			line := map[string]any{"type": "event", "cursor": cursor}
			if m, ok := item.(map[string]any); ok {
				for k, v := range m {
					line[k] = v
				}
			} else {
				line["event"] = item
			}
			if err := enc.Encode(line); err != nil {
				return err
			}
		}
	}
	if section, ok := result["logs"].(map[string]any); ok {
		cursor := int64FromAny(section["cursor"])
		filter := strings.ToLower(strings.TrimSpace(logFilter))
		for _, item := range observeItems(section["lines"]) {
			lineText := fmt.Sprint(item)
			if filter != "" && !strings.Contains(strings.ToLower(lineText), filter) {
				continue
			}
			if err := enc.Encode(map[string]any{"type": "log", "cursor": cursor, "line": lineText}); err != nil {
				return err
			}
		}
	}
	return nil
}

func advanceObserveCursor(params map[string]any, result map[string]any, sectionKey string, paramKey string) {
	section, ok := result[sectionKey].(map[string]any)
	if !ok {
		return
	}
	cursor := int64FromAny(section["cursor"])
	if cursor > 0 {
		params[paramKey] = cursor
	}
}

func observeItems(v any) []any {
	switch raw := v.(type) {
	case []any:
		return raw
	case []map[string]any:
		out := make([]any, 0, len(raw))
		for _, item := range raw {
			out = append(out, item)
		}
		return out
	case []string:
		out := make([]any, 0, len(raw))
		for _, item := range raw {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		out, _ := n.Int64()
		return out
	default:
		return 0
	}
}
