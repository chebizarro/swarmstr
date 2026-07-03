// Package signal implements a Signal channel extension for metiq via a
// local signal-rest-api (or signal-cli-rest-api) sidecar.
//
// Because Signal has no public API, this plugin delegates all protocol work to
// a separately-running sidecar process.  The recommended sidecar is
// bbernhard/signal-cli-rest-api (https://github.com/bbernhard/signal-cli-rest-api).
//
// Registration: import _ "metiq/internal/extensions/signal" in the daemon
// main.go to include this plugin in the binary.
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
// To add a Signal channel to your metiq config:
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

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("signal", func() sdk.ChannelPlugin { return &SignalPlugin{} })
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
		Typing:       true,
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

	// Prefer the event-driven signal-cli JSON-RPC WebSocket receive stream;
	// run() falls back to REST /v1/receive polling (a documented,
	// non-event-driven fallback) only if the WebSocket cannot be reached.
	runCtx, cancel := context.WithCancel(ctx)
	bot.cancel = cancel
	go bot.run(runCtx)
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
	cancel         context.CancelFunc
	httpClient     *http.Client
}

func (b *signalBot) ID() string { return b.channelID }

func (b *signalBot) Close() {
	if b.cancel != nil {
		b.cancel()
	}
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// ─── Polling (documented fallback) ──────────────────────────────────────────
//
// poll is a wait-and-check loop over the signal-cli REST receive endpoint. It is
// a documented fallback for the not-yet-implemented signal-cli JSON-RPC
// WebSocket push transport; prefer implementing push before relying on this in
// production.
func (b *signalBot) poll(ctx context.Context) {
	backoff := b.pollInterval
	if backoff <= 0 {
		backoff = 3 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-time.After(backoff):
		}
		if err := b.receive(ctx); err != nil {
			log.Printf("signal: receive error channel=%s: %v", b.channelID, err)
			if backoff < 60*time.Second {
				backoff *= 2
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
			}
			continue
		}
		backoff = b.pollInterval
		if backoff <= 0 {
			backoff = 3 * time.Second
		}
	}
}

// signalEnvelope is the top-level object returned by GET /v1/receive/{account}.
type signalEnvelope struct {
	Envelope struct {
		Source      string `json:"source"`
		Timestamp   int64  `json:"timestamp"`
		DataMessage *struct {
			Message     string             `json:"message"`
			Timestamp   int64              `json:"timestamp"`
			Attachments []signalAttachment `json:"attachments"`
		} `json:"dataMessage"`
	} `json:"envelope"`
}

type signalAttachment struct {
	ID           string `json:"id"`
	AttachmentID string `json:"attachmentId"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType"`
	MIMEType     string `json:"mimeType"`
}

func (b *signalBot) receive(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/receive/%s", b.apiURL, b.account)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
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
		b.deliverEnvelope(env)
	}
	return nil
}

// deliverEnvelope applies allowlist filtering and delivers a Signal envelope to
// the agent. It is shared by the JSON-RPC WebSocket path and the polling
// fallback.
func (b *signalBot) deliverEnvelope(env signalEnvelope) {
	dm := env.Envelope.DataMessage
	if dm == nil {
		return
	}
	sender := env.Envelope.Source
	if len(b.allowedSenders) > 0 && !b.allowedSenders[sender] {
		return
	}
	text := strings.TrimSpace(dm.Message)
	mediaURL, mediaMIME := firstSignalAttachment(dm.Attachments)
	if text == "" && mediaURL == "" {
		return
	}
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  sender,
		Text:      text,
		EventID:   fmt.Sprintf("signal-%s-%d", sender, env.Envelope.Timestamp),
		MediaURL:  mediaURL,
		MediaMIME: mediaMIME,
	})
}

// ─── Event-driven receive (signal-cli JSON-RPC WebSocket) ─────────────────────

const signalMaxReconnects = 10

// run prefers the event-driven JSON-RPC WebSocket receive stream and falls back
// to REST /v1/receive polling (a documented, non-event-driven fallback) if the
// WebSocket cannot be reached.
func (b *signalBot) run(ctx context.Context) {
	conn, err := b.dialWS(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("signal: channel=%s JSON-RPC WebSocket receive unavailable (%v); using REST /v1/receive polling fallback (account=%s, sidecar=%s)", b.channelID, err, b.account, b.apiURL)
		b.poll(ctx)
		return
	}
	log.Printf("signal: channel=%s connected to signal-cli JSON-RPC WebSocket receive (account=%s, sidecar=%s)", b.channelID, b.account, b.apiURL)
	b.serveWS(ctx, conn)
}

func (b *signalBot) wsURL() string {
	u := b.apiURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/v1/receive/" + b.account
}

func (b *signalBot) dialWS(ctx context.Context) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, b.wsURL(), nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)
	return conn, nil
}

// serveWS reads streamed envelopes, reconnecting with backoff. After
// signalMaxReconnects consecutive failures it falls back to REST polling.
func (b *signalBot) serveWS(ctx context.Context, conn *websocket.Conn) {
	backoff := b.pollInterval
	if backoff <= 0 {
		backoff = 3 * time.Second
	}
	attempts := 0
	for {
		err := b.readWS(ctx, conn)
		_ = conn.Close(websocket.StatusNormalClosure, "reconnect")
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		default:
		}
		log.Printf("signal: channel=%s WebSocket receive ended (%v); reconnecting", b.channelID, err)
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.done:
				return
			case <-time.After(backoff):
			}
			attempts++
			newConn, derr := b.dialWS(ctx)
			if derr == nil {
				conn = newConn
				attempts = 0
				break
			}
			log.Printf("signal: channel=%s WebSocket reconnect failed (%v)", b.channelID, derr)
			if attempts >= signalMaxReconnects {
				log.Printf("signal: channel=%s giving up on WebSocket after %d attempts; using REST /v1/receive polling fallback", b.channelID, attempts)
				b.poll(ctx)
				return
			}
		}
	}
}

// signalWSFrame accepts both a bare envelope ({"envelope":{...}}) and a
// signal-cli JSON-RPC notification ({"jsonrpc":"2.0","method":"receive",
// "params":{"envelope":{...}}}).
type signalWSFrame struct {
	Method string          `json:"method"`
	Params *signalEnvelope `json:"params"`
	signalEnvelope
}

func (b *signalBot) readWS(ctx context.Context, conn *websocket.Conn) error {
	for {
		var frame signalWSFrame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}
		env := frame.signalEnvelope
		if frame.Params != nil {
			env = *frame.Params
		}
		b.deliverEnvelope(env)
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
	_, err := b.SendWithReceipt(ctx, text)
	return err
}

func (b *signalBot) SendWithReceipt(ctx context.Context, text string) (channels.DeliveryReceipt, error) {
	recipient := b.channelID
	if recipient == "" {
		recipient = b.account
	}
	receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "signal", Attempts: 1, CreatedAt: time.Now()}
	body, _ := json.Marshal(signalSendRequest{Number: b.account, Recipients: []string{recipient}, Message: text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL+"/v2/send", bytes.NewReader(body))
	if err != nil {
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		err = fmt.Errorf("signal send: %w", err)
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("signal send: status %d: %s", resp.StatusCode, string(raw))
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	var result struct {
		Timestamp int64  `json:"timestamp"`
		ID        string `json:"id"`
	}
	_ = json.Unmarshal(raw, &result)
	if result.ID != "" {
		receipt.MessageID = result.ID
	} else if result.Timestamp > 0 {
		receipt.MessageID = fmt.Sprintf("signal-%s-%d", recipient, result.Timestamp)
	}
	receipt.Status = channels.DeliveryDelivered
	receipt.DeliveredAt = time.Now()
	return receipt, nil
}

func firstSignalAttachment(attachments []signalAttachment) (string, string) {
	for _, a := range attachments {
		id := strings.TrimSpace(firstNonEmptySignal(a.ID, a.AttachmentID))
		if id == "" {
			continue
		}
		mime := strings.TrimSpace(firstNonEmptySignal(a.ContentType, a.MIMEType))
		return "signal://attachment/" + id, mime
	}
	return "", ""
}

func firstNonEmptySignal(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (b *signalBot) ResolveMedia(ctx context.Context, ref string) (channels.MediaBlob, error) {
	id := strings.TrimSpace(strings.TrimPrefix(ref, "signal://attachment/"))
	if id == "" || id == ref {
		return channels.MediaBlob{}, fmt.Errorf("signal media: invalid attachment ref %q", ref)
	}
	apiURL := fmt.Sprintf("%s/v1/attachments/%s", b.apiURL, urlPathEscapeSignal(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return channels.MediaBlob{}, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return channels.MediaBlob{}, fmt.Errorf("signal media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return channels.MediaBlob{}, fmt.Errorf("signal media: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20+1))
	if err != nil {
		return channels.MediaBlob{}, err
	}
	if len(data) > 25<<20 {
		return channels.MediaBlob{}, fmt.Errorf("signal media: attachment exceeds 25MiB")
	}
	return channels.MediaBlob{URL: ref, MIME: resp.Header.Get("Content-Type"), Data: data}, nil
}

func urlPathEscapeSignal(s string) string {
	r := strings.NewReplacer("%", "%25", "/", "%2F", "?", "%3F", "#", "%23")
	return r.Replace(s)
}

func (b *signalBot) SendTyping(ctx context.Context, _ int) error {
	recipient := b.channelID
	if recipient == "" {
		recipient = b.account
	}
	body, _ := json.Marshal(map[string]any{"recipient": recipient})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL+"/v1/typing-indicator/"+b.account, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal typing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("signal typing: status %d: %s", resp.StatusCode, string(raw))
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
		"recipient":             b.channelID,
		"reaction":              emoji,
		"target_author":         sender,
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
		"recipient":             b.channelID,
		"reaction":              emoji,
		"target_author":         sender,
		"target_sent_timestamp": timestamp,
		"remove":                true,
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
