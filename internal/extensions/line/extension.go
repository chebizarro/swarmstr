// Package line implements a LINE Messaging API channel extension for metiq.
//
// Inbound messages arrive via a LINE webhook.  Outbound messages are sent via
// the LINE Messaging API reply endpoint (when a replyToken is available) or
// the push endpoint.
//
// Registration: import _ "metiq/internal/extensions/line" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "channel_access_token": "...",  // required: LINE channel access token
//	  "channel_secret":       "...",  // required: for HMAC-SHA256 signature verification
//	  "allowed_senders":      []      // optional: allowlist of LINE user IDs
//	}
//
// Inbound webhook endpoint: <admin_addr>/webhooks/line/<channel_id>
// Set this URL in the LINE Developers Console under Messaging API → Webhook.
package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&LINEPlugin{})
}

// LINEPlugin is the factory for LINE Messaging API channel instances.
type LINEPlugin struct{}

func (p *LINEPlugin) ID() string   { return "line" }
func (p *LINEPlugin) Type() string { return "LINE" }

func (p *LINEPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"channel_access_token": map[string]any{
				"type":        "string",
				"description": "LINE channel access token from LINE Developers Console.",
			},
			"channel_secret": map[string]any{
				"type":        "string",
				"description": "LINE channel secret, used to verify the X-Line-Signature header.",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of LINE user IDs.",
			},
		},
		"required": []string{"channel_access_token", "channel_secret"},
	}
}

func (p *LINEPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{MultiAccount: true}
}

func (p *LINEPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *LINEPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	accessToken, _ := cfg["channel_access_token"].(string)
	channelSecret, _ := cfg["channel_secret"].(string)
	if accessToken == "" || channelSecret == "" {
		return nil, fmt.Errorf("line channel %q: channel_access_token and channel_secret are required", channelID)
	}

	allowedSenders := map[string]bool{}
	if v, ok := cfg["allowed_senders"].([]interface{}); ok {
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[e] = true
			}
		}
	}

	bot := &lineBot{
		channelID:      channelID,
		accessToken:    accessToken,
		channelSecret:  channelSecret,
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}

	registerWebhook(channelID, bot)
	log.Printf("line: webhook registered channel=%s", channelID)
	return bot, nil
}

// ─── Global webhook registry ──────────────────────────────────────────────────

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*lineBot{}
)

func registerWebhook(channelID string, bot *lineBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an inbound LINE webhook POST to the registered bot.
func HandleWebhook(channelID string, w http.ResponseWriter, r *http.Request) {
	webhookMu.RLock()
	bot, ok := webhookHandlers[channelID]
	webhookMu.RUnlock()
	if !ok {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	bot.handlePush(w, r)
}

// ─── LINE event types ──────────────────────────────────────────────────────────

type lineWebhookBody struct {
	Events []lineEvent `json:"events"`
}

type lineEvent struct {
	Type       string      `json:"type"`
	ReplyToken string      `json:"replyToken"`
	Source     lineSource  `json:"source"`
	Message    lineMessage `json:"message"`
	Timestamp  int64       `json:"timestamp"`
}

type lineSource struct {
	Type   string `json:"type"`
	UserID string `json:"userId"`
}

type lineMessage struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

// ─── Bot ──────────────────────────────────────────────────────────────────────

type lineBot struct {
	channelID      string
	accessToken    string
	channelSecret  string
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client

	// replyTokens stores the latest reply token per user for fast reply.
	replyTokensMu sync.Mutex
	replyTokens   map[string]string
}

func (b *lineBot) ID() string { return b.channelID }

func (b *lineBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
}

// verifySignature verifies the X-Line-Signature HMAC-SHA256 header.
func (b *lineBot) verifySignature(body []byte, sig string) bool {
	mac := hmac.New(sha256.New, []byte(b.channelSecret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

func (b *lineBot) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("X-Line-Signature")
	if sig == "" || !b.verifySignature(body, sig) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var wb lineWebhookBody
	if err := json.Unmarshal(body, &wb); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}

	for _, ev := range wb.Events {
		if ev.Type != "message" || ev.Message.Type != "text" {
			continue
		}
		text := strings.TrimSpace(ev.Message.Text)
		if text == "" {
			continue
		}
		senderID := ev.Source.UserID
		if len(b.allowedSenders) > 0 && !b.allowedSenders[senderID] {
			continue
		}

		// Cache reply token for this sender.
		if ev.ReplyToken != "" {
			b.replyTokensMu.Lock()
			if b.replyTokens == nil {
				b.replyTokens = map[string]string{}
			}
			b.replyTokens[senderID] = ev.ReplyToken
			b.replyTokensMu.Unlock()
		}

		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  senderID,
			Text:      text,
			EventID:   ev.Message.ID,
			CreatedAt: ev.Timestamp / 1000,
		})
	}
	w.WriteHeader(http.StatusOK)
}

// Send posts a text message via LINE Messaging API.
// Uses reply endpoint if a cached reply token exists; falls back to push.
func (b *lineBot) Send(ctx context.Context, text string) error {
	// Prefer push to the first known user if no specific target.
	// For proper per-user routing wire sdk.WithChannelReplyTarget.
	replyTarget := sdk.ChannelReplyTarget(ctx)

	// Try reply token first.
	if replyTarget != "" {
		b.replyTokensMu.Lock()
		rt := b.replyTokens[replyTarget]
		if rt != "" {
			delete(b.replyTokens, replyTarget)
		}
		b.replyTokensMu.Unlock()
		if rt != "" {
			return b.sendReply(ctx, rt, text)
		}
		return b.sendPush(ctx, replyTarget, text)
	}

	// No target: best-effort reply using most-recently cached token.
	b.replyTokensMu.Lock()
	if len(b.replyTokens) != 1 {
		b.replyTokensMu.Unlock()
		return fmt.Errorf("line: ambiguous reply target; set channel reply target explicitly")
	}
	var onlyUser, onlyToken string
	for u, t := range b.replyTokens {
		onlyUser, onlyToken = u, t
	}
	if onlyToken != "" {
		delete(b.replyTokens, onlyUser)
	}
	b.replyTokensMu.Unlock()

	if onlyToken != "" {
		return b.sendReply(ctx, onlyToken, text)
	}
	return fmt.Errorf("line: no reply token or push target available")
}

func (b *lineBot) sendReply(ctx context.Context, replyToken, text string) error {
	payload, _ := json.Marshal(map[string]any{
		"replyToken": replyToken,
		"messages":   []map[string]any{{"type": "text", "text": text}},
	})
	return b.lineAPI(ctx, "POST", "https://api.line.me/v2/bot/message/reply", payload)
}

func (b *lineBot) sendPush(ctx context.Context, to, text string) error {
	payload, _ := json.Marshal(map[string]any{
		"to":       to,
		"messages": []map[string]any{{"type": "text", "text": text}},
	})
	return b.lineAPI(ctx, "POST", "https://api.line.me/v2/bot/message/push", payload)
}

func (b *lineBot) lineAPI(ctx context.Context, method, apiURL string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.accessToken)
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("line API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("line API status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
