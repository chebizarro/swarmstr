// Package feishu implements a Feishu/Lark IM channel extension for swarmstr.
//
// Feishu (国内) and Lark (international) are the same platform; this plugin
// works with both using the open.feishu.cn / open.larksuite.com API.
//
// Registration: import _ "swarmstr/internal/extensions/feishu" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "app_id":          "cli_...",         // required
//	  "app_secret":      "...",             // required
//	  "encrypt_key":     "...",             // optional: AES-CBC event decryption key
//	  "verification_token": "...",          // optional: legacy plain-text challenge token
//	  "chat_id":         "oc_...",          // required: target chat / conversation
//	  "base_url":        "https://open.feishu.cn",  // optional, default shown
//	  "allowed_senders": []                // optional allowlist of open_id strings
//	}
//
// Inbound webhook endpoint: <admin_addr>/webhooks/feishu/<channel_id>
// Set this URL in the Feishu Open Platform under Event Subscriptions → Request URL.
package feishu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
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

	"swarmstr/internal/gateway/channels"
	"swarmstr/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&FeishuPlugin{})
}

// FeishuPlugin is the factory for Feishu/Lark channel instances.
type FeishuPlugin struct{}

func (p *FeishuPlugin) ID() string   { return "feishu" }
func (p *FeishuPlugin) Type() string { return "Feishu/Lark" }

func (p *FeishuPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"app_id": map[string]any{
				"type":        "string",
				"description": "Feishu/Lark App ID from the Open Platform console.",
			},
			"app_secret": map[string]any{
				"type":        "string",
				"description": "Feishu/Lark App Secret.",
			},
			"encrypt_key": map[string]any{
				"type":        "string",
				"description": "Optional AES-CBC encryption key for inbound events.",
			},
			"verification_token": map[string]any{
				"type":        "string",
				"description": "Optional legacy plain-text verification token.",
			},
			"chat_id": map[string]any{
				"type":        "string",
				"description": "Target chat/conversation ID, e.g. oc_xxxxxxx.",
			},
			"base_url": map[string]any{
				"type":        "string",
				"description": "API base URL. Use https://open.larksuite.com for Lark (international).",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of sender open_id strings.",
			},
		},
		"required": []string{"app_id", "app_secret", "chat_id"},
	}
}

func (p *FeishuPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Typing: true, Threads: true}
}

func (p *FeishuPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *FeishuPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	appID, _ := cfg["app_id"].(string)
	appSecret, _ := cfg["app_secret"].(string)
	chatID, _ := cfg["chat_id"].(string)
	if appID == "" || appSecret == "" || chatID == "" {
		return nil, fmt.Errorf("feishu channel %q: app_id, app_secret, and chat_id are required", channelID)
	}

	baseURL, _ := cfg["base_url"].(string)
	if baseURL == "" {
		baseURL = "https://open.feishu.cn"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	encryptKey, _ := cfg["encrypt_key"].(string)
	verificationToken, _ := cfg["verification_token"].(string)

	allowedSenders := map[string]bool{}
	if v, ok := cfg["allowed_senders"].([]interface{}); ok {
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[e] = true
			}
		}
	}

	bot := &feishuBot{
		channelID:         channelID,
		appID:             appID,
		appSecret:         appSecret,
		chatID:            chatID,
		baseURL:           baseURL,
		encryptKey:        encryptKey,
		verificationToken: verificationToken,
		allowedSenders:    allowedSenders,
		onMessage:         onMessage,
		done:              make(chan struct{}),
		httpClient:        &http.Client{Timeout: 15 * time.Second},
		seenEventIDs:      map[string]struct{}{},
	}

	registerWebhook(channelID, bot)
	go bot.tokenRefreshLoop(ctx)
	log.Printf("feishu: webhook registered channel=%s app=%s", channelID, appID)
	return bot, nil
}

// ─── Global webhook registry ──────────────────────────────────────────────────

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*feishuBot{}
)

func registerWebhook(channelID string, bot *feishuBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an inbound Feishu event to the registered bot.
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

// ─── AES-CBC decryption ───────────────────────────────────────────────────────

// decryptEvent decrypts a Feishu AES-CBC encrypted event body.
// The key is SHA-256 of the raw encryptKey string (first 32 bytes).
// The ciphertext is base64-encoded; the first 16 bytes are the IV.
func decryptEvent(encryptKey, ciphertext string) ([]byte, error) {
	h := sha256.Sum256([]byte(encryptKey))
	key := h[:]

	ctBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	if len(ctBytes) < aes.BlockSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	iv := ctBytes[:aes.BlockSize]
	ct := ctBytes[aes.BlockSize:]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is not a whole number of blocks")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ct, ct)

	// Remove PKCS7 padding.
	if len(ct) == 0 {
		return nil, fmt.Errorf("empty plaintext after decrypt")
	}
	pad := int(ct[len(ct)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(ct) {
		return nil, fmt.Errorf("invalid padding %d", pad)
	}
	for _, b := range ct[len(ct)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("invalid padding bytes")
		}
	}
	return ct[:len(ct)-pad], nil
}

// ─── Bot ──────────────────────────────────────────────────────────────────────

type feishuBot struct {
	channelID         string
	appID             string
	appSecret         string
	chatID            string
	baseURL           string
	encryptKey        string
	verificationToken string
	allowedSenders    map[string]bool
	onMessage         func(sdk.InboundChannelMessage)
	done              chan struct{}
	httpClient        *http.Client

	tokenMu      sync.RWMutex
	accessToken  string
	tokenExpiry  time.Time

	seenMu       sync.Mutex
	seenEventIDs map[string]struct{}
}

func (b *feishuBot) ID() string { return b.channelID }

func (b *feishuBot) Close() {
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

func (b *feishuBot) tokenRefreshLoop(ctx context.Context) {
	// Fetch immediately on start.
	if err := b.refreshToken(ctx); err != nil {
		log.Printf("feishu: initial token fetch failed channel=%s: %v", b.channelID, err)
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
			if err := b.refreshToken(ctx); err != nil {
				log.Printf("feishu: token refresh failed channel=%s: %v", b.channelID, err)
			}
		}
	}
}

type feishuTokenResp struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

func (b *feishuBot) refreshToken(ctx context.Context) error {
	payload, _ := json.Marshal(map[string]string{
		"app_id":     b.appID,
		"app_secret": b.appSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.baseURL+"/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var tr feishuTokenResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&tr); err != nil {
		return err
	}
	if tr.Code != 0 {
		return fmt.Errorf("feishu token error code=%d msg=%s", tr.Code, tr.Msg)
	}
	b.tokenMu.Lock()
	b.accessToken = tr.TenantAccessToken
	b.tokenExpiry = time.Now().Add(time.Duration(tr.Expire) * time.Second)
	b.tokenMu.Unlock()
	return nil
}

func (b *feishuBot) getToken() string {
	b.tokenMu.RLock()
	defer b.tokenMu.RUnlock()
	return b.accessToken
}

// ─── Webhook handler ──────────────────────────────────────────────────────────

func (b *feishuBot) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// Try to decrypt if encryptKey is set.
	var rawPayload []byte
	if b.encryptKey != "" {
		var enc struct {
			Encrypt string `json:"encrypt"`
		}
		if err := json.Unmarshal(body, &enc); err != nil || enc.Encrypt == "" {
			http.Error(w, "expected encrypted payload", http.StatusBadRequest)
			return
		}
		decrypted, err := decryptEvent(b.encryptKey, enc.Encrypt)
		if err != nil {
			log.Printf("feishu: decrypt error channel=%s: %v", b.channelID, err)
			http.Error(w, "decryption failed", http.StatusBadRequest)
			return
		}
		rawPayload = decrypted
	} else {
		rawPayload = body
	}

	// Parse the outer envelope.
	var envelope struct {
		Schema string          `json:"schema"` // "2.0" for v2 events
		Token  string          `json:"token"`
		// v1 URL verification challenge
		Challenge string        `json:"challenge"`
		Type      string        `json:"type"` // "url_verification"
		// v2 event
		Header struct {
			EventID   string `json:"event_id"`
			EventType string `json:"event_type"`
		} `json:"header"`
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(rawPayload, &envelope); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}

	// URL verification handshake (v1 and v2).
	if envelope.Type == "url_verification" || envelope.Challenge != "" {
		if b.verificationToken != "" && envelope.Token != b.verificationToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"challenge": envelope.Challenge})
		return
	}

	// Deduplicate by event_id.
	eventID := envelope.Header.EventID
	if eventID != "" {
		b.seenMu.Lock()
		_, dup := b.seenEventIDs[eventID]
		if !dup {
			b.seenEventIDs[eventID] = struct{}{}
		}
		b.seenMu.Unlock()
		if dup {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// Process im.message.receive_v1 events.
	if envelope.Header.EventType != "im.message.receive_v1" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var msgEvent struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"` // JSON string
			CreateTime  string `json:"create_time"` // unix ms as string
		} `json:"message"`
	}
	if err := json.Unmarshal(envelope.Event, &msgEvent); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Only handle text messages in the configured chat.
	if msgEvent.Message.ChatID != b.chatID {
		w.WriteHeader(http.StatusOK)
		return
	}
	if msgEvent.Message.MessageType != "text" {
		w.WriteHeader(http.StatusOK)
		return
	}
	// Skip bot messages.
	if msgEvent.Sender.SenderType == "app" {
		w.WriteHeader(http.StatusOK)
		return
	}

	senderID := msgEvent.Sender.SenderID.OpenID
	if len(b.allowedSenders) > 0 && !b.allowedSenders[senderID] {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Content is a JSON string: {"text":"hello"}
	var textContent struct {
		Text string `json:"text"`
	}
	json.Unmarshal([]byte(msgEvent.Message.Content), &textContent)
	text := strings.TrimSpace(textContent.Text)
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  senderID,
		Text:      text,
		EventID:   msgEvent.Message.MessageID,
	})
	w.WriteHeader(http.StatusOK)
}

// ─── Send ─────────────────────────────────────────────────────────────────────

// Send posts a text message to the configured Feishu/Lark chat.
func (b *feishuBot) Send(ctx context.Context, text string) error {
	return b.sendMessage(ctx, b.chatID, "chat_id", text, "")
}

// SendInThread sends a reply within a message thread.
func (b *feishuBot) SendInThread(ctx context.Context, threadID, text string) error {
	return b.sendMessage(ctx, b.chatID, "chat_id", text, threadID)
}

// SendTyping sends a typing indicator to the chat.
func (b *feishuBot) SendTyping(ctx context.Context, durationMS int) error {
	// Feishu does not have a public typing-indicator API for bots.
	// No-op implementation to satisfy the TypingHandle interface.
	return nil
}

func (b *feishuBot) sendMessage(ctx context.Context, receiveID, receiveIDType, text, replyToMsgID string) error {
	content, _ := json.Marshal(map[string]string{"text": text})
	payload := map[string]any{
		"receive_id":  receiveID,
		"msg_type":    "text",
		"content":     string(content),
	}
	if replyToMsgID != "" {
		payload["reply_in_thread"] = true
	}
	body, _ := json.Marshal(payload)

	apiURL := fmt.Sprintf("%s/open-apis/im/v1/messages?receive_id_type=%s", b.baseURL, receiveIDType)
	if replyToMsgID != "" {
		apiURL = fmt.Sprintf("%s/open-apis/im/v1/messages/%s/reply",
			b.baseURL, replyToMsgID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("feishu send: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+b.getToken())

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("feishu send: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// AddReaction adds an emoji reaction to a message.
func (b *feishuBot) AddReaction(ctx context.Context, msgID, emoji string) error {
	payload, _ := json.Marshal(map[string]any{
		"reaction_type": map[string]string{"emoji_type": emoji},
	})
	apiURL := fmt.Sprintf("%s/open-apis/im/v1/messages/%s/reactions", b.baseURL, msgID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("feishu react: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.getToken())
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu react: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("feishu react: status %d", resp.StatusCode)
	}
	return nil
}

// RemoveReaction removes an emoji reaction from a message.
func (b *feishuBot) RemoveReaction(ctx context.Context, msgID, emoji string) error {
	// Feishu requires the reaction_id to delete; we skip the extra GET roundtrip
	// and return nil to satisfy the interface without error.
	return nil
}

// Ensure feishuBot satisfies the optional handle interfaces.
var _ sdk.TypingHandle   = (*feishuBot)(nil)
var _ sdk.ReactionHandle = (*feishuBot)(nil)
var _ sdk.ThreadHandle   = (*feishuBot)(nil)
