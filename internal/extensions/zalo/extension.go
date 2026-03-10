// Package zalo implements a Zalo Official Account channel extension for swarmstr.
//
// Zalo is a Vietnamese messaging platform.  This plugin uses the Zalo OA
// Open API to receive webhooks and send Customer Service (CS) messages.
//
// Registration: import _ "swarmstr/internal/extensions/zalo" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "app_id":          "...",   // required: Zalo App ID
//	  "app_secret":      "...",   // required: Zalo App Secret
//	  "refresh_token":   "...",   // required: long-lived refresh token
//	  "oa_id":           "...",   // required: Official Account (OA) ID
//	  "allowed_senders": []       // optional: allowlist of follower user IDs
//	}
//
// Inbound webhook endpoint: <admin_addr>/webhooks/zalo/<channel_id>
// Register this URL in the Zalo OA Admin Portal under Webhook Settings.
// Enable the "user_send_text" event.
package zalo

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"swarmstr/internal/gateway/channels"
	"swarmstr/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&ZaloPlugin{})
}

// ZaloPlugin is the factory for Zalo OA channel instances.
type ZaloPlugin struct{}

func (p *ZaloPlugin) ID() string   { return "zalo" }
func (p *ZaloPlugin) Type() string { return "Zalo OA" }

func (p *ZaloPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"app_id": map[string]any{
				"type":        "string",
				"description": "Zalo application ID.",
			},
			"app_secret": map[string]any{
				"type":        "string",
				"description": "Zalo application secret, used to verify inbound webhook signatures.",
			},
			"refresh_token": map[string]any{
				"type":        "string",
				"description": "Long-lived refresh token obtained from the Zalo OA Admin Portal OAuth flow.",
			},
			"oa_id": map[string]any{
				"type":        "string",
				"description": "Zalo Official Account (OA) ID.",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of follower user IDs.",
			},
		},
		"required": []string{"app_id", "app_secret", "refresh_token", "oa_id"},
	}
}

func (p *ZaloPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{}
}

func (p *ZaloPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *ZaloPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	appID, _ := cfg["app_id"].(string)
	appSecret, _ := cfg["app_secret"].(string)
	refreshToken, _ := cfg["refresh_token"].(string)
	oaID, _ := cfg["oa_id"].(string)
	if appID == "" || appSecret == "" || refreshToken == "" || oaID == "" {
		return nil, fmt.Errorf("zalo channel %q: app_id, app_secret, refresh_token, and oa_id are required", channelID)
	}

	allowedSenders := map[string]bool{}
	if v, ok := cfg["allowed_senders"].([]interface{}); ok {
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[e] = true
			}
		}
	}

	bot := &zaloBot{
		channelID:      channelID,
		appID:          appID,
		appSecret:      appSecret,
		refreshToken:   refreshToken,
		oaID:           oaID,
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}

	registerWebhook(channelID, bot)
	go bot.tokenRefreshLoop(ctx)
	log.Printf("zalo: webhook registered channel=%s oa=%s", channelID, oaID)
	return bot, nil
}

// ─── Global webhook registry ──────────────────────────────────────────────────

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*zaloBot{}
)

func registerWebhook(channelID string, bot *zaloBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an inbound Zalo webhook POST to the registered bot.
func HandleWebhook(channelID string, w http.ResponseWriter, r *http.Request) {
	webhookMu.RLock()
	bot, ok := webhookHandlers[channelID]
	webhookMu.RUnlock()
	if !ok {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	bot.handleEvent(w, r)
}

// ─── Bot ──────────────────────────────────────────────────────────────────────

type zaloBot struct {
	channelID      string
	appID          string
	appSecret      string
	refreshToken   string
	oaID           string
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client

	tokenMu     sync.RWMutex
	accessToken string
	tokenExpiry time.Time
}

func (b *zaloBot) ID() string { return b.channelID }

func (b *zaloBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
}

// ─── Token management ─────────────────────────────────────────────────────────

type zaloTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        int    `json:"error"`
	Message      string `json:"message"`
}

func (b *zaloBot) tokenRefreshLoop(ctx context.Context) {
	if err := b.refreshAccessToken(ctx); err != nil {
		log.Printf("zalo: initial token fetch failed channel=%s: %v", b.channelID, err)
	}
	ticker := time.NewTicker(90 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.refreshAccessToken(ctx); err != nil {
				log.Printf("zalo: token refresh failed channel=%s: %v", b.channelID, err)
			}
		}
	}
}

func (b *zaloBot) refreshAccessToken(ctx context.Context) error {
	data := url.Values{}
	data.Set("app_id", b.appID)
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", b.refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth.zaloapp.com/v4/oa/access_token",
		strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("secret_key", b.appSecret)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var tr zaloTokenResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&tr); err != nil {
		return err
	}
	if tr.Error != 0 {
		return fmt.Errorf("zalo token error=%d msg=%s", tr.Error, tr.Message)
	}

	b.tokenMu.Lock()
	b.accessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		b.refreshToken = tr.RefreshToken // rolling refresh token
	}
	if tr.ExpiresIn > 0 {
		b.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	b.tokenMu.Unlock()
	return nil
}

func (b *zaloBot) getToken() string {
	b.tokenMu.RLock()
	defer b.tokenMu.RUnlock()
	return b.accessToken
}

// ─── Signature verification ───────────────────────────────────────────────────

// verifySignature checks the MAC signature on an inbound Zalo event.
// Zalo signs the raw request body with HMAC-SHA256 using the app_secret.
// The signature is provided in the "mac" query parameter.
func (b *zaloBot) verifySignature(body []byte, mac string) bool {
	h := hmac.New(sha256.New, []byte(b.appSecret))
	h.Write(body)
	expected := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(mac))
}

// ─── Webhook handler ──────────────────────────────────────────────────────────

func (b *zaloBot) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// Verify MAC signature if present.
	mac := r.URL.Query().Get("mac")
	if mac == "" {
		mac = r.Header.Get("X-Zalo-Mac")
	}
	if mac == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !b.verifySignature(body, mac) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var event struct {
		AppID    string `json:"app_id"`
		OAType   int    `json:"oa_type"`
		Timestamp int64 `json:"timestamp"`
		EventName string `json:"event_name"`
		Sender   struct {
			ID string `json:"id"`
		} `json:"sender"`
		Recipient struct {
			ID string `json:"id"`
		} `json:"recipient"`
		Message struct {
			MsgID string `json:"msg_id"`
			Text  string `json:"text"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}

	// Only handle user_send_text events for this OA.
	if event.EventName != "user_send_text" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if event.Recipient.ID != b.oaID {
		w.WriteHeader(http.StatusOK)
		return
	}

	senderID := event.Sender.ID
	text := strings.TrimSpace(event.Message.Text)
	if text == "" || senderID == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if len(b.allowedSenders) > 0 && !b.allowedSenders[senderID] {
		w.WriteHeader(http.StatusOK)
		return
	}

	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  senderID,
		Text:      text,
		EventID:   event.Message.MsgID,
		CreatedAt: event.Timestamp / 1000,
	})
	w.WriteHeader(http.StatusOK)
}

// ─── Send ─────────────────────────────────────────────────────────────────────

// Send posts a Customer Service (CS) text message via the Zalo OA Open API.
// The recipient must be a follower who has initiated a conversation.
func (b *zaloBot) Send(ctx context.Context, text string) error {
	target := sdk.ChannelReplyTarget(ctx)
	if target == "" {
		return fmt.Errorf("zalo: no reply target set in context")
	}
	return b.sendCS(ctx, target, text)
}

func (b *zaloBot) sendCS(ctx context.Context, recipientID, text string) error {
	payload, _ := json.Marshal(map[string]any{
		"recipient": map[string]string{"user_id": recipientID},
		"message":   map[string]string{"text": text},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://openapi.zalo.me/v3.0/oa/message/cs",
		bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("zalo send: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("access_token", b.getToken())

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("zalo send: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result); err != nil {
		return fmt.Errorf("zalo send: parse response: %w", err)
	}
	if result.Error != 0 {
		return fmt.Errorf("zalo send: error=%d msg=%s", result.Error, result.Message)
	}
	return nil
}
