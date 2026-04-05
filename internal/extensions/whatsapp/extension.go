// Package whatsapp implements a WhatsApp Business channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/whatsapp" in the daemon
// main.go to register this plugin at startup.
//
// This extension uses the Meta (Facebook) Graph API for WhatsApp Business.
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
//	  "default_recipient":  "+15551234567"    // optional: explicit fallback recipient for Send()
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

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&WhatsAppPlugin{})
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
		},
		"required": []string{"access_token", "app_secret", "phone_number_id"},
	}
}

// Capabilities declares the features supported by the WhatsApp Business channel.
func (w *WhatsAppPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       false,
		Reactions:    false,
		Threads:      false,
		Audio:        false,
		Edit:         false,
		MultiAccount: false,
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
	}
}

func (w *WhatsAppPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	token, _ := cfg["access_token"].(string)
	appSecret, _ := cfg["app_secret"].(string)
	phoneNumberID, _ := cfg["phone_number_id"].(string)
	verifyToken, _ := cfg["verify_token"].(string)
	defaultRecipient, _ := cfg["default_recipient"].(string)

	if token == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.access_token is required", channelID)
	}
	if strings.TrimSpace(appSecret) == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.app_secret is required", channelID)
	}
	if phoneNumberID == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.phone_number_id is required", channelID)
	}

	bot := &whatsappBot{
		channelID:        channelID,
		token:            token,
		phoneNumberID:    phoneNumberID,
		appSecret:        strings.TrimSpace(appSecret),
		verifyToken:      verifyToken,
		defaultRecipient: strings.TrimSpace(defaultRecipient),
		onMessage:        onMessage,
		done:             make(chan struct{}),
		httpClient:       &http.Client{Timeout: 15 * time.Second},
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

type whatsappBot struct {
	mu               sync.Mutex
	channelID        string
	token            string
	phoneNumberID    string
	appSecret        string
	verifyToken      string
	defaultRecipient string
	onMessage        func(sdk.InboundChannelMessage)
	httpClient       *http.Client
	done             chan struct{}
}

func (b *whatsappBot) ID() string { return b.channelID }

func (b *whatsappBot) Send(ctx context.Context, text string) error {
	to := strings.TrimSpace(sdk.ChannelReplyTarget(ctx))
	if to == "" {
		b.mu.Lock()
		to = b.defaultRecipient
		b.mu.Unlock()
	}
	if to == "" {
		return fmt.Errorf("whatsapp %s: no reply target set; configure default_recipient or use sdk.WithChannelReplyTarget", b.channelID)
	}
	_, err := b.sendMessage(ctx, to, text)
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

	var event struct {
		Object string `json:"object"`
		Entry  []struct {
			Changes []struct {
				Value struct {
					Metadata *struct {
						PhoneNumberID string `json:"phone_number_id"`
					} `json:"metadata,omitempty"`
					Messages []struct {
						ID        string `json:"id"`
						From      string `json:"from"`
						Timestamp string `json:"timestamp"`
						Text      *struct {
							Body string `json:"body"`
						} `json:"text"`
						Type string `json:"type"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}

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
				if msg.Type != "text" || msg.Text == nil || msg.Text.Body == "" {
					continue
				}

				b.onMessage(sdk.InboundChannelMessage{
					ChannelID: b.channelID,
					SenderID:  msg.From,
					Text:      msg.Text.Body,
					EventID:   "wa-" + msg.ID,
				})
			}
		}
	}
}

func (b *whatsappBot) sendMessage(ctx context.Context, to, text string) (string, error) {
	return sendWhatsAppMessage(ctx, b.httpClient, b.token, b.phoneNumberID, to, text)
}

// sendWhatsAppMessage sends a text message via the Meta Cloud API.
func sendWhatsAppMessage(ctx context.Context, httpClient *http.Client, token, phoneNumberID, to, text string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/messages", whatsappAPIBase, phoneNumberID)
	payload, _ := json.Marshal(map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": text},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
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
		return "", fmt.Errorf("whatsapp sendMessage: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("whatsapp sendMessage: HTTP %d: %s", resp.StatusCode, raw)
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
