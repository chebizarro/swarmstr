// Package signal implements a Signal channel extension for swarmstr via a
// local signal-rest-api (or signal-cli-rest-api) sidecar.
//
// Because Signal has no public API, this plugin delegates all protocol work to
// a separately-running sidecar process.  The recommended sidecar is
// bbernhard/signal-cli-rest-api (https://github.com/bbernhard/signal-cli-rest-api).
//
// Registration: import _ "swarmstr/internal/extensions/signal" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "api_url":         "http://localhost:8080",  // required: sidecar base URL
//	  "account":         "+15551234567",            // required: E.164 sender number
//	  "allowed_senders": [],                        // optional: E.164 allowlist
//	  "poll_interval_ms": 3000                      // default 3000
//	}
//
// To add a Signal channel to your swarmstr config:
//
//	"nostr_channels": {
//	  "signal-main": {
//	    "kind": "signal",
//	    "config": {
//	      "api_url": "http://localhost:8080",
//	      "account": "+15551234567"
//	    }
//	  }
//	}
package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"swarmstr/internal/gateway/channels"
	"swarmstr/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&SignalPlugin{})
}

// SignalPlugin is the factory for Signal channel instances.
type SignalPlugin struct{}

func (p *SignalPlugin) ID() string   { return "signal" }
func (p *SignalPlugin) Type() string { return "Signal" }

func (p *SignalPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"api_url": map[string]any{
				"type":        "string",
				"description": "Base URL of the signal-rest-api sidecar (e.g. http://localhost:8080).",
			},
			"account": map[string]any{
				"type":        "string",
				"description": "E.164 phone number registered in the sidecar (e.g. +15551234567).",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional E.164 phone number allowlist.",
			},
			"poll_interval_ms": map[string]any{
				"type":        "integer",
				"description": "Polling interval in milliseconds. Default 3000.",
			},
		},
		"required": []string{"api_url", "account"},
	}
}

func (p *SignalPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Reactions:    true,
		MultiAccount: true,
	}
}

func (p *SignalPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	apiURL, _ := cfg["api_url"].(string)
	account, _ := cfg["account"].(string)
	if apiURL == "" || account == "" {
		return nil, fmt.Errorf("signal channel %q: api_url and account are required", channelID)
	}
	apiURL = strings.TrimRight(apiURL, "/")

	allowedSenders := map[string]bool{}
	switch v := cfg["allowed_senders"].(type) {
	case []interface{}:
		for _, s := range v {
			if n, ok := s.(string); ok && n != "" {
				allowedSenders[n] = true
			}
		}
	}

	pollInterval := 3 * time.Second
	switch v := cfg["poll_interval_ms"].(type) {
	case float64:
		if v > 0 {
			pollInterval = time.Duration(v) * time.Millisecond
		}
	case int:
		if v > 0 {
			pollInterval = time.Duration(v) * time.Millisecond
		}
	}

	bot := &signalBot{
		channelID:      channelID,
		apiURL:         apiURL,
		account:        account,
		allowedSenders: allowedSenders,
		pollInterval:   pollInterval,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}

	go bot.poll(ctx)
	log.Printf("signal: polling started for channel %s (account=%s, sidecar=%s)", channelID, account, apiURL)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

type signalBot struct {
	mu             sync.Mutex
	channelID      string
	apiURL         string
	account        string
	allowedSenders map[string]bool
	pollInterval   time.Duration
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client
}

func (b *signalBot) ID() string { return b.channelID }

func (b *signalBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// ─── Polling ──────────────────────────────────────────────────────────────────

func (b *signalBot) poll(ctx context.Context) {
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-ticker.C:
			b.receive(ctx)
		}
	}
}

// signalEnvelope is the top-level object returned by GET /v1/receive/{account}.
type signalEnvelope struct {
	Envelope struct {
		Source      string `json:"source"`
		Timestamp   int64  `json:"timestamp"`
		DataMessage *struct {
			Message   string `json:"message"`
			Timestamp int64  `json:"timestamp"`
		} `json:"dataMessage"`
	} `json:"envelope"`
}

func (b *signalBot) receive(ctx context.Context) {
	url := fmt.Sprintf("%s/v1/receive/%s", b.apiURL, b.account)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}

	// The sidecar may return a JSON array of envelopes or a newline-delimited
	// stream.  Try array first, then fall back to NDJSON.
	var envelopes []signalEnvelope
	if err := json.Unmarshal(raw, &envelopes); err != nil {
		// Try NDJSON.
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var env signalEnvelope
			if jsonErr := json.Unmarshal([]byte(line), &env); jsonErr == nil {
				envelopes = append(envelopes, env)
			}
		}
	}

	for _, env := range envelopes {
		dm := env.Envelope.DataMessage
		if dm == nil || dm.Message == "" {
			continue
		}
		sender := env.Envelope.Source
		if len(b.allowedSenders) > 0 && !b.allowedSenders[sender] {
			continue
		}
		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  sender,
			Text:      dm.Message,
			EventID:   fmt.Sprintf("signal-%s-%d", sender, env.Envelope.Timestamp),
		})
	}
}

// ─── Send ─────────────────────────────────────────────────────────────────────

// signalSendRequest is the body for POST /v2/send.
type signalSendRequest struct {
	Number     string   `json:"number"`
	Recipients []string `json:"recipients"`
	Message    string   `json:"message"`
}

func (b *signalBot) Send(ctx context.Context, text string) error {
	// Determine recipient from the channel config.  The channel_id in swarmstr
	// config should be set to the recipient number or group ID.  Fall back to
	// the account itself for self-tests.
	recipient := b.channelID
	if recipient == "" {
		recipient = b.account
	}
	body, _ := json.Marshal(signalSendRequest{
		Number:     b.account,
		Recipients: []string{recipient},
		Message:    text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.apiURL+"/v2/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("signal send: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// ─── ReactionHandle ───────────────────────────────────────────────────────────

// AddReaction sends an emoji reaction via POST /v1/react.
// eventID must be of the form "signal-{sender}-{timestamp}".
func (b *signalBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	// Parse sender and timestamp from eventID.
	if !strings.HasPrefix(eventID, "signal-") {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	parts := strings.SplitN(strings.TrimPrefix(eventID, "signal-"), "-", 2)
	if len(parts) != 2 {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	sender, timestamp := parts[0], parts[1]

	body, _ := json.Marshal(map[string]any{
		"recipient":            b.channelID,
		"reaction":             emoji,
		"target_author":        sender,
		"target_sent_timestamp": timestamp,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.apiURL+"/v1/react/"+b.account, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal react: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("signal react: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// RemoveReaction removes an emoji reaction by sending a remove flag.
func (b *signalBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	if !strings.HasPrefix(eventID, "signal-") {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	parts := strings.SplitN(strings.TrimPrefix(eventID, "signal-"), "-", 2)
	if len(parts) != 2 {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	sender, timestamp := parts[0], parts[1]

	body, _ := json.Marshal(map[string]any{
		"recipient":            b.channelID,
		"reaction":             emoji,
		"target_author":        sender,
		"target_sent_timestamp": timestamp,
		"remove":               true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.apiURL+"/v1/react/"+b.account, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal remove reaction: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("signal remove reaction: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
