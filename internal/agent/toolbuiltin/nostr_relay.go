// Package toolbuiltin nostr_relay.go — relay inspection tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
)

var ensureRelay = func(pool *nostr.Pool, relayURL string) error {
	_, err := pool.EnsureRelay(relayURL)
	return err
}

// NostrRelayToolOpts holds the relay tool configuration.
type NostrRelayToolOpts struct {
	// ReadRelays is the list to return from relay_list (read side).
	ReadRelays []string
	// WriteRelays is the list to return from relay_list (write side).
	WriteRelays []string
}

// ─── relay_list ───────────────────────────────────────────────────────────────

// NostrRelayListTool returns an agent tool that lists configured relays.
func NostrRelayListTool(opts NostrRelayToolOpts) agent.ToolFunc {
	return func(_ context.Context, _ map[string]any) (string, error) {
		out, _ := json.Marshal(map[string]any{
			"read":  opts.ReadRelays,
			"write": opts.WriteRelays,
		})
		return string(out), nil
	}
}

// ─── relay_ping ───────────────────────────────────────────────────────────────

// NostrRelayPingTool returns an agent tool that dials a relay and measures latency.
//
// Parameters:
//   - url string — relay WebSocket URL (required)
func NostrRelayPingTool() agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		relayURL, _ := args["url"].(string)
		if relayURL == "" {
			return "", fmt.Errorf("relay_ping: url is required")
		}

		start := time.Now()
		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})
		defer pool.Close("ping done")

		ensureRelayFn := ensureRelay
		errCh := make(chan error, 1)
		go func() {
			errCh <- ensureRelayFn(pool, relayURL)
		}()

		var err error
		select {
		case err = <-errCh:
		case <-ctx2.Done():
			err = ctx2.Err()
		}

		latencyMs := time.Since(start).Milliseconds()
		if err != nil {
			out, _ := json.Marshal(map[string]any{
				"url":        relayURL,
				"ok":         false,
				"latency_ms": latencyMs,
				"error":      err.Error(),
			})
			return string(out), nil
		}

		out, _ := json.Marshal(map[string]any{
			"url":        relayURL,
			"ok":         true,
			"latency_ms": latencyMs,
		})
		return string(out), nil
	}
}

// ─── relay_info ───────────────────────────────────────────────────────────────

// NostrRelayInfoTool returns an agent tool that fetches a relay's NIP-11 info document.
//
// Parameters:
//   - url string — relay WebSocket or HTTP URL (required)
func NostrRelayInfoTool() agent.ToolFunc {
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context, args map[string]any) (string, error) {
		rawURL, _ := args["url"].(string)
		if rawURL == "" {
			return "", fmt.Errorf("relay_info: url is required")
		}

		// Convert ws:// → http:// and wss:// → https://.
		httpURL := rawURL
		switch {
		case len(rawURL) > 5 && rawURL[:5] == "ws://":
			httpURL = "http://" + rawURL[5:]
		case len(rawURL) > 6 && rawURL[:6] == "wss://":
			httpURL = "https://" + rawURL[6:]
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpURL, nil)
		if err != nil {
			return "", fmt.Errorf("relay_info: build request: %w", err)
		}
		req.Header.Set("Accept", "application/nostr+json")

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("relay_info: HTTP request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("relay_info: server returned %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		// Return raw JSON so the agent receives the full NIP-11 document.
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			return "", fmt.Errorf("relay_info: invalid JSON: %w", err)
		}
		out, _ := json.Marshal(doc)
		return string(out), nil
	}
}
