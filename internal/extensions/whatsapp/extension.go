// Package whatsapp implements a WhatsApp Business channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/whatsapp" in the daemon
// main.go to include this plugin in the binary.
//
// This extension supports Meta (Facebook) Graph API WhatsApp Business accounts.
// Multiple accounts can be configured on one channel by adding an "accounts" map
// and selecting a send account with "account_id" in gateway calls. WhatsApp Web
// sessions are represented in config for directory/login status discovery; live
// Web transport is intentionally not started from this Go connector.
// Inbound messages are received via the admin HTTP server at
// <admin_addr>/webhooks/whatsapp/<channel_id>, where the extension is registered
// by channel ID and verification/events are dispatched through a shared registry.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "access_token":       "EAABs...",       // required: Meta access token
//	  "app_secret":         "abcd1234",       // required: Meta app secret for X-Hub-Signature-256 verification
//	  "phone_number_id":    "1234567890",     // required: WhatsApp phone number ID
//	  "verify_token":       "my-token",       // optional: webhook verification token
//	  "default_recipient":  "+15551234567",   // optional: explicit fallback recipient for Send()
//	  "accounts": {                           // optional: extra Meta Cloud accounts
//	    "support": {"access_token":"...", "phone_number_id":"...", "default_recipient":"+1555"}
//	  },
//	  "web_sessions": {"agent1": {"auth_dir":"/secure/wa-agent1"}} // optional directory/login metadata
//	}
//
// To add a WhatsApp channel to your metiq config:
//
//	"nostr_channels": {
//	  "whatsapp-main": {
//	    "kind": "whatsapp",
//	    "config": {
//	      "access_token":    "EAABs...",
//	      "phone_number_id": "YOUR_PHONE_NUMBER_ID",
//	      "verify_token":    "your-verify-token"
//	    }
//	  }
//	}
package whatsapp

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
	"strings"
	"sync"
	"time"

	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("whatsapp", func() sdk.ChannelPlugin { return &WhatsAppPlugin{} })
}

// WhatsAppPlugin is the factory for WhatsApp Business channel instances.
type WhatsAppPlugin struct{}

func (w *WhatsAppPlugin) ID() string   { return "whatsapp" }
func (w *WhatsAppPlugin) Type() string { return "WhatsApp Business" }

func (w *WhatsAppPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"access_token": map[string]any{
				"type":        "string",
				"description": "Meta (Facebook) permanent or user access token with WhatsApp permissions.",
			},
			"app_secret": map[string]any{
				"type":        "string",
				"description": "Meta app secret used to verify X-Hub-Signature-256 on inbound webhooks.",
			},
			"phone_number_id": map[string]any{
				"type":        "string",
				"description": "WhatsApp phone number ID from the Meta developer dashboard.",
			},
			"verify_token": map[string]any{
				"type":        "string",
				"description": "Webhook verification token set in the Meta developer console.",
			},
			"default_recipient": map[string]any{
				"type":        "string",
				"description": "Optional explicit fallback recipient phone number in E.164 format (e.g. '+15551234567') when no channel reply target is set.",
			},
			"accounts": map[string]any{
				"type":        "object",
				"description": "Optional named Meta Cloud accounts. Each account may set access_token, phone_number_id, app_secret, verify_token, and default_recipient.",
			},
			"web_sessions": map[string]any{
				"type":        "object",
				"description": "Optional WhatsApp Web login/session directory metadata keyed by account name. Used for directory/status discovery.",
			},
		},
		"required": []string{"access_token", "app_secret", "phone_number_id"},
	}
}

// Capabilities declares the features supported by the WhatsApp Business channel.
func (w *WhatsAppPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       false,
		Reactions:    true,
		Threads:      true,
		Audio:        true,
		Edit:         false,
		MultiAccount: true,
	}
}

func (w *WhatsAppPlugin) GatewayMethods() []sdk.GatewayMethod {
	return []sdk.GatewayMethod{
		{
			Method:      "whatsapp.send",
			Description: "Send a WhatsApp message via the Meta Cloud API",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["access_token"].(string)
				phoneNumberID, _ := params["phone_number_id"].(string)
				to, _ := params["to"].(string)
				text, _ := params["text"].(string)
				if token == "" || phoneNumberID == "" || to == "" || text == "" {
					return nil, fmt.Errorf("whatsapp.send: access_token, phone_number_id, to, and text are required")
				}
				msgID, err := sendWhatsAppMessage(ctx, nil, token, phoneNumberID, to, text)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "message_id": msgID}, nil
			},
		},
		{
			Method:      "whatsapp.send_media",
			Description: "Send WhatsApp image, audio, document, or video media by URL or uploaded media ID",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["access_token"].(string)
				phoneNumberID, _ := params["phone_number_id"].(string)
				to, _ := params["to"].(string)
				mediaType, _ := params["media_type"].(string)
				mediaURL, _ := params["media_url"].(string)
				mediaID, _ := params["media_id"].(string)
				caption, _ := params["caption"].(string)
				if token == "" || phoneNumberID == "" || to == "" || mediaType == "" || (mediaURL == "" && mediaID == "") {
					return nil, fmt.Errorf("whatsapp.send_media: access_token, phone_number_id, to, media_type, and media_url or media_id are required")
				}
				msgID, err := sendWhatsAppMedia(ctx, nil, token, phoneNumberID, to, mediaType, mediaURL, mediaID, caption)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "message_id": msgID}, nil
			},
		},
		{
			Method:      "whatsapp.react",
			Description: "Add or remove a WhatsApp emoji reaction",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["access_token"].(string)
				phoneNumberID, _ := params["phone_number_id"].(string)
				to, _ := params["to"].(string)
				messageID, _ := params["message_id"].(string)
				emoji, _ := params["emoji"].(string)
				if token == "" || phoneNumberID == "" || to == "" || messageID == "" {
					return nil, fmt.Errorf("whatsapp.react: access_token, phone_number_id, to, and message_id are required")
				}
				msgID, err := sendWhatsAppReaction(ctx, nil, token, phoneNumberID, to, messageID, emoji)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "message_id": msgID}, nil
			},
		},
	}
}

func (w *WhatsAppPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	primary := whatsappAccount{
		ID:               "default",
		Token:            stringValue(cfg, "access_token"),
		AppSecret:        strings.TrimSpace(stringValue(cfg, "app_secret")),
		PhoneNumberID:    stringValue(cfg, "phone_number_id"),
		VerifyToken:      stringValue(cfg, "verify_token"),
		DefaultRecipient: strings.TrimSpace(stringValue(cfg, "default_recipient")),
	}

	if primary.Token == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.access_token is required", channelID)
	}
	if primary.AppSecret == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.app_secret is required", channelID)
	}
	if primary.PhoneNumberID == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.phone_number_id is required", channelID)
	}

	bot := &whatsappBot{
		channelID:        channelID,
		token:            primary.Token,
		phoneNumberID:    primary.PhoneNumberID,
		appSecret:        primary.AppSecret,
		verifyToken:      primary.VerifyToken,
		defaultRecipient: primary.DefaultRecipient,
		accounts:         map[string]whatsappAccount{primary.ID: primary},
		webSessions:      parseWebSessions(cfg["web_sessions"]),
		onMessage:        onMessage,
		done:             make(chan struct{}),
		httpClient:       &http.Client{Timeout: 15 * time.Second},
	}
	for id, account := range parseAccounts(cfg["accounts"], primary) {
		bot.accounts[id] = account
	}

	registerWebhook(channelID, bot)
	log.Printf("whatsapp: webhook handler registered for channel %s", channelID)
	return bot, nil
}

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*whatsappBot{}
)

func registerWebhook(channelID string, bot *whatsappBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an inbound WhatsApp verification or event request to the registered bot.
func HandleWebhook(channelID string, w http.ResponseWriter, r *http.Request) {
	webhookMu.RLock()
	bot, ok := webhookHandlers[channelID]
	webhookMu.RUnlock()
	if !ok {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		bot.handleVerify(w, r)
	case http.MethodPost:
		bot.handleEvent(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ─── Bot implementation ───────────────────────────────────────────────────────

const whatsappAPIBase = "https://graph.facebook.com/v18.0"

type whatsappAccount struct {
	ID               string
	Token            string
	PhoneNumberID    string
	AppSecret        string
	VerifyToken      string
	DefaultRecipient string
}

type whatsappWebSession struct {
	ID      string
	AuthDir string
	Status  string
}

type whatsappBot struct {
	mu               sync.Mutex
	channelID        string
	token            string
	phoneNumberID    string
	appSecret        string
	verifyToken      string
	defaultRecipient string
	accounts         map[string]whatsappAccount
	webSessions      map[string]whatsappWebSession
	onMessage        func(sdk.InboundChannelMessage)
	httpClient       *http.Client
	done             chan struct{}
}

type whatsappWebhookEvent struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Value struct {
				Metadata *struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata,omitempty"`
				Messages []whatsappWebhookMessage `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type whatsappWebhookMessage struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Text      *struct {
		Body string `json:"body"`
	} `json:"text"`
	Context *struct {
		ID   string `json:"id"`
		From string `json:"from"`
	} `json:"context"`
	Reaction *struct {
		MessageID string `json:"message_id"`
		Emoji     string `json:"emoji"`
	} `json:"reaction"`
	Image    *whatsappWebhookMedia `json:"image"`
	Audio    *whatsappWebhookMedia `json:"audio"`
	Document *whatsappWebhookMedia `json:"document"`
	Video    *whatsappWebhookMedia `json:"video"`
	Sticker  *whatsappWebhookMedia `json:"sticker"`
}

type whatsappWebhookMedia struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Caption  string `json:"caption"`
}

func (m whatsappWebhookMessage) toInbound(channelID string) sdk.InboundChannelMessage {
	inbound := sdk.InboundChannelMessage{
		ChannelID: channelID,
		SenderID:  m.From,
		EventID:   "wa-" + m.ID,
	}
	if ts, err := parseUnix(m.Timestamp); err == nil {
		inbound.CreatedAt = ts
	}
	if m.Context != nil && m.Context.ID != "" {
		inbound.ReplyToEventID = "wa-" + m.Context.ID
	}
	if strings.HasSuffix(strings.ToLower(m.From), "@g.us") {
		inbound.ThreadID = m.From
	}
	if m.Text != nil {
		inbound.Text = m.Text.Body
		return inbound
	}
	if m.Reaction != nil {
		inbound.Text = ":reaction:" + m.Reaction.Emoji
		if m.Reaction.MessageID != "" {
			inbound.ReplyToEventID = "wa-" + m.Reaction.MessageID
		}
		return inbound
	}
	if media, typ := m.media(); media != nil {
		inbound.Text = media.Caption
		inbound.MediaURL = "whatsapp://media/" + media.ID
		inbound.MediaMIME = media.MimeType
		if inbound.Text == "" {
			inbound.Text = "[whatsapp " + typ + "]"
		}
	}
	return inbound
}

func (m whatsappWebhookMessage) media() (*whatsappWebhookMedia, string) {
	switch {
	case m.Image != nil:
		return m.Image, "image"
	case m.Audio != nil:
		return m.Audio, "audio"
	case m.Document != nil:
		return m.Document, "document"
	case m.Video != nil:
		return m.Video, "video"
	case m.Sticker != nil:
		return m.Sticker, "sticker"
	default:
		return nil, ""
	}
}

func parseUnix(raw string) (int64, error) {
	var ts int64
	_, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &ts)
	return ts, err
}

func (b *whatsappBot) ID() string { return b.channelID }

func stringValue(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func (b *whatsappBot) accountForID(accountID string) whatsappAccount {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		accountID = "default"
	}
	fallback := whatsappAccount{
		ID:               "default",
		Token:            b.token,
		PhoneNumberID:    b.phoneNumberID,
		AppSecret:        b.appSecret,
		VerifyToken:      b.verifyToken,
		DefaultRecipient: b.defaultRecipient,
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if account, ok := b.accounts[accountID]; ok {
		return account
	}
	if account, ok := b.accounts["default"]; ok {
		return account
	}
	return fallback
}

func splitReplyTarget(target string) (accountID, to string) {
	target = strings.TrimSpace(target)
	if strings.Contains(target, ":") && !strings.HasPrefix(target, "+") {
		parts := strings.SplitN(target, ":", 2)
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "", target
}

func (b *whatsappBot) Send(ctx context.Context, text string) error {
	accountID, to := splitReplyTarget(sdk.ChannelReplyTarget(ctx))
	account := b.accountForID(accountID)
	if to == "" {
		to = account.DefaultRecipient
	}
	if to == "" {
		return fmt.Errorf("whatsapp %s: no reply target set; configure default_recipient or use sdk.WithChannelReplyTarget", b.channelID)
	}
	_, err := b.sendMessageWithAccount(ctx, account, to, text)
	return err
}

func (b *whatsappBot) SendAudio(ctx context.Context, audio []byte, format string) error {
	return fmt.Errorf("whatsapp %s: raw audio upload is not supported; use whatsapp.send_media with an uploaded media_id or URL", b.channelID)
}

func (b *whatsappBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	accountID, to := splitReplyTarget(sdk.ChannelReplyTarget(ctx))
	account := b.accountForID(accountID)
	if to == "" {
		to = account.DefaultRecipient
	}
	if to == "" {
		return fmt.Errorf("whatsapp %s: no reaction target recipient set", b.channelID)
	}
	_, err := sendWhatsAppReaction(ctx, b.httpClient, account.Token, account.PhoneNumberID, to, strings.TrimPrefix(eventID, "wa-"), emoji)
	return err
}

func (b *whatsappBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return b.AddReaction(ctx, eventID, "")
}

func (b *whatsappBot) SendInThread(ctx context.Context, threadID, text string) error {
	account := b.accountForID("")
	_, err := b.sendMessageWithAccount(ctx, account, strings.TrimSpace(threadID), text)
	return err
}

func (b *whatsappBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
}

func parseAccounts(raw any, primary whatsappAccount) map[string]whatsappAccount {
	out := map[string]whatsappAccount{}
	accounts, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for id, value := range accounts {
		m, ok := value.(map[string]any)
		if !ok {
			continue
		}
		account := primary
		account.ID = strings.TrimSpace(id)
		if v := stringValue(m, "access_token"); v != "" {
			account.Token = v
		}
		if v := stringValue(m, "phone_number_id"); v != "" {
			account.PhoneNumberID = v
		}
		if v := stringValue(m, "app_secret"); v != "" {
			account.AppSecret = v
		}
		if v := stringValue(m, "verify_token"); v != "" {
			account.VerifyToken = v
		}
		if v := stringValue(m, "default_recipient"); v != "" {
			account.DefaultRecipient = v
		}
		if account.ID != "" && account.Token != "" && account.PhoneNumberID != "" {
			out[account.ID] = account
		}
	}
	return out
}

func parseWebSessions(raw any) map[string]whatsappWebSession {
	out := map[string]whatsappWebSession{}
	sessions, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for id, value := range sessions {
		m, ok := value.(map[string]any)
		if !ok {
			continue
		}
		session := whatsappWebSession{
			ID:      strings.TrimSpace(id),
			AuthDir: stringValue(m, "auth_dir"),
			Status:  stringValue(m, "status"),
		}
		if session.Status == "" {
			session.Status = "configured"
		}
		if session.ID != "" {
			out[session.ID] = session
		}
	}
	return out
}

func (b *whatsappBot) verifySignature(body []byte, sig string) bool {
	sig = strings.TrimSpace(sig)
	if !strings.HasPrefix(sig, "sha256=") || b.appSecret == "" {
		return false
	}
	providedHex := strings.TrimPrefix(sig, "sha256=")
	provided, err := hex.DecodeString(providedHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(b.appSecret))
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

// handleVerify responds to Meta's GET webhook verification challenge.
func (b *whatsappBot) handleVerify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && (b.verifyToken == "" || token == b.verifyToken) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, challenge)
		return
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}

// handleEvent processes incoming WhatsApp message events.
func (b *whatsappBot) handleEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	if !b.verifySignature(body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK) // Always 200 to Meta.

	var event whatsappWebhookEvent

	if err := json.Unmarshal(body, &event); err != nil {
		return
	}
	if event.Object != "whatsapp_business_account" {
		return
	}

	for _, entry := range event.Entry {
		for _, change := range entry.Changes {
			if change.Value.Metadata != nil {
				incomingPhoneID := strings.TrimSpace(change.Value.Metadata.PhoneNumberID)
				if incomingPhoneID != "" && incomingPhoneID != b.phoneNumberID {
					log.Printf("whatsapp: dropping webhook for mismatched phone_number_id channel=%s got=%s want=%s", b.channelID, incomingPhoneID, b.phoneNumberID)
					continue
				}
			}
			for _, msg := range change.Value.Messages {
				inbound := msg.toInbound(b.channelID)
				if inbound.Text == "" && inbound.MediaURL == "" {
					continue
				}
				b.onMessage(inbound)
			}
		}
	}
}

func (b *whatsappBot) sendMessage(ctx context.Context, to, text string) (string, error) {
	return sendWhatsAppMessage(ctx, b.httpClient, b.token, b.phoneNumberID, to, text)
}

func (b *whatsappBot) sendMessageWithAccount(ctx context.Context, account whatsappAccount, to, text string) (string, error) {
	return sendWhatsAppMessage(ctx, b.httpClient, account.Token, account.PhoneNumberID, to, text)
}

// sendWhatsAppMessage sends a text message via the Meta Cloud API.
func sendWhatsAppMessage(ctx context.Context, httpClient *http.Client, token, phoneNumberID, to, text string) (string, error) {
	return postWhatsAppMessage(ctx, httpClient, token, phoneNumberID, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    recipientType(to),
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": text},
	}, "sendMessage")
}

func sendWhatsAppMedia(ctx context.Context, httpClient *http.Client, token, phoneNumberID, to, mediaType, mediaURL, mediaID, caption string) (string, error) {
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if mediaType == "" {
		mediaType = "image"
	}
	if mediaType != "image" && mediaType != "audio" && mediaType != "document" && mediaType != "video" && mediaType != "sticker" {
		return "", fmt.Errorf("whatsapp sendMedia: unsupported media_type %q", mediaType)
	}
	media := map[string]string{}
	if mediaID != "" {
		media["id"] = mediaID
	} else {
		media["link"] = mediaURL
	}
	if caption != "" && mediaType != "audio" && mediaType != "sticker" {
		media["caption"] = caption
	}
	return postWhatsAppMessage(ctx, httpClient, token, phoneNumberID, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    recipientType(to),
		"to":                to,
		"type":              mediaType,
		mediaType:           media,
	}, "sendMedia")
}

func sendWhatsAppReaction(ctx context.Context, httpClient *http.Client, token, phoneNumberID, to, messageID, emoji string) (string, error) {
	return postWhatsAppMessage(ctx, httpClient, token, phoneNumberID, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    recipientType(to),
		"to":                to,
		"type":              "reaction",
		"reaction": map[string]string{
			"message_id": strings.TrimPrefix(messageID, "wa-"),
			"emoji":      emoji,
		},
	}, "sendReaction")
}

func recipientType(to string) string {
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(to)), "@g.us") {
		return "group"
	}
	return "individual"
}

func postWhatsAppMessage(ctx context.Context, httpClient *http.Client, token, phoneNumberID string, payload map[string]any, op string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/messages", whatsappAPIBase, phoneNumberID)
	rawPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(rawPayload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whatsapp %s: %w", op, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("whatsapp %s: HTTP %d: %s", op, resp.StatusCode, raw)
	}
	var result struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &result); err == nil && len(result.Messages) > 0 {
		return result.Messages[0].ID, nil
	}
	return "", nil
}
