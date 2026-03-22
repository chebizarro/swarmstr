// Package toolbuiltin nostr_relay.go — relay inspection tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
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

// ─── relay_score ──────────────────────────────────────────────────────────────

// NostrRelayScoreDef is the ToolDefinition for relay_score.
var NostrRelayScoreDef = agent.ToolDefinition{
	Name:        "relay_score",
	Description: "Score and rank relays by health. Pings each relay, fetches NIP-11 info, and computes a 0-100 score based on reachability (40pts), latency (up to 30pts), NIP-11 availability (10pts), and NIP support count (up to 20pts). Returns relays sorted best-first.",
	Parameters: agent.ToolParameters{
		Type:     "object",
		Required: []string{"urls"},
		Properties: map[string]agent.ToolParamProp{
			"urls": {
				Type:        "array",
				Description: "Relay URLs to score. Max 20.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// relayScoreResult holds scoring data for a single relay.
type relayScoreResult struct {
	URL       string `json:"url"`
	Score     int    `json:"score"`
	Reachable bool   `json:"reachable"`
	LatencyMs int64  `json:"latency_ms"`
	NIPCount  int    `json:"nip_count,omitempty"`
	Name      string `json:"name,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NostrRelayScoreTool returns an agent tool that scores relays by health.
func NostrRelayScoreTool() agent.ToolFunc {
	httpClient := &http.Client{Timeout: 8 * time.Second}

	return func(ctx context.Context, args map[string]any) (string, error) {
		urls := toStringSlice(args["urls"])
		if len(urls) == 0 {
			return "", nostrToolErr("relay_score", "invalid_input", "urls array is required", nil)
		}
		if len(urls) > 20 {
			return "", nostrToolErr("relay_score", "invalid_input",
				fmt.Sprintf("max 20 relays, got %d", len(urls)), nil)
		}

		// Use a single shared pool for all relay dials to avoid creating
		// 20 heavyweight pools concurrently.
		pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})
		defer pool.Close("relay_score done")

		// Limit concurrency to 5 simultaneous dials.
		sem := make(chan struct{}, 5)
		results := make([]relayScoreResult, len(urls))
		var wg sync.WaitGroup

		for i, relayURL := range urls {
			wg.Add(1)
			go func(idx int, url string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[idx] = scoreRelay(ctx, pool, url, httpClient)
			}(i, relayURL)
		}
		wg.Wait()

		// Sort by score descending.
		sort.Slice(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})

		out, _ := json.Marshal(map[string]any{
			"relays": results,
			"count":  len(results),
		})
		return string(out), nil
	}
}

// scoreRelay pings a relay and fetches NIP-11, computing a 0-100 score.
//
// Scoring (bounded 0–100):
//   - Reachable (WebSocket connect): 40 points
//   - Latency bonus: up to 30 points (≤100ms=30, ≤500ms=20, ≤1s=10, ≤3s=5, >3s=0)
//   - NIP-11 available: 10 points
//   - NIP count bonus: up to 20 points (1 point per supported NIP, capped at 20)
func scoreRelay(ctx context.Context, pool *nostr.Pool, relayURL string, httpClient *http.Client) relayScoreResult {
	result := relayScoreResult{URL: relayURL}

	// Ping: WebSocket connect with enforced timeout via select.
	start := time.Now()
	pingCtx, pingCancel := context.WithTimeout(ctx, 8*time.Second)
	defer pingCancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := pool.EnsureRelay(relayURL)
		errCh <- err
	}()

	var err error
	select {
	case err = <-errCh:
	case <-pingCtx.Done():
		err = pingCtx.Err()
	}
	result.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		result.Error = err.Error()
		result.Score = 0
		return result
	}

	result.Reachable = true
	score := 40 // reachable base

	// Latency bonus.
	switch {
	case result.LatencyMs <= 100:
		score += 30
	case result.LatencyMs <= 500:
		score += 20
	case result.LatencyMs <= 1000:
		score += 10
	case result.LatencyMs <= 3000:
		score += 5
	}

	// NIP-11 info fetch.
	httpURL := relayURL
	switch {
	case len(relayURL) > 5 && relayURL[:5] == "ws://":
		httpURL = "http://" + relayURL[5:]
	case len(relayURL) > 6 && relayURL[:6] == "wss://":
		httpURL = "https://" + relayURL[6:]
	}

	infoCtx, infoCancel := context.WithTimeout(ctx, 5*time.Second)
	defer infoCancel()

	req, reqErr := http.NewRequestWithContext(infoCtx, http.MethodGet, httpURL, nil)
	if reqErr == nil {
		req.Header.Set("Accept", "application/nostr+json")
		resp, doErr := httpClient.Do(req)
		if doErr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				var doc map[string]any
				if json.Unmarshal(body, &doc) == nil {
					score += 10 // NIP-11 available

					// Extract NIP count.
					if nips, ok := doc["supported_nips"].([]any); ok {
						result.NIPCount = len(nips)
						nipBonus := len(nips)
						if nipBonus > 20 {
							nipBonus = 20
						}
						score += nipBonus
					}

					if name, ok := doc["name"].(string); ok {
						result.Name = name
					}
				}
			}
		}
	}

	result.Score = score
	return result
}
