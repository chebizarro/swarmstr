// Package whatsapp implements a WhatsApp Business channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/whatsapp" in the daemon
// main.go to register this plugin at startup.
//
// This extension uses the Meta (Facebook) Graph API for WhatsApp Business.
// Inbound messages are received via a webhook (set up separately); the extension
// starts an HTTP listener on config.webhook_listen_addr and verifies incoming
// webhook events using the hub.verify_token you set in the Meta developer console.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "access_token":       "EAABs...",       // required: Meta access token
//	  "phone_number_id":    "1234567890",     // required: WhatsApp phone number ID
//	  "webhook_listen_addr": ":8080",         // optional: address to listen for webhooks (default ":8080")
//	  "verify_token":       "my-token",       // optional: webhook verification token
//	  "default_recipient":  "+15551234567"    // optional: default To number for Send()
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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
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
			"phone_number_id": map[string]any{
				"type":        "string",
				"description": "WhatsApp phone number ID from the Meta developer dashboard.",
			},
			"webhook_listen_addr": map[string]any{
				"type":        "string",
				"description": "Local address to listen for Meta webhook HTTP callbacks (e.g. ':8080'). Default ':8080'.",
			},
			"verify_token": map[string]any{
				"type":        "string",
				"description": "Webhook verification token set in the Meta developer console.",
			},
			"default_recipient": map[string]any{
				"type":        "string",
				"description": "Optional default recipient phone number in E.164 format (e.g. '+15551234567').",
			},
		},
		"required": []string{"access_token", "phone_number_id"},
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
				msgID, err := sendWhatsAppMessage(ctx, token, phoneNumberID, to, text)
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
	phoneNumberID, _ := cfg["phone_number_id"].(string)
	listenAddr, _ := cfg["webhook_listen_addr"].(string)
	verifyToken, _ := cfg["verify_token"].(string)
	defaultRecipient, _ := cfg["default_recipient"].(string)

	if token == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.access_token is required", channelID)
	}
	if phoneNumberID == "" {
		return nil, fmt.Errorf("whatsapp channel %q: config.phone_number_id is required", channelID)
	}
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	bot := &whatsappBot{
		channelID:        channelID,
		token:            token,
		phoneNumberID:    phoneNumberID,
		verifyToken:      verifyToken,
		defaultRecipient: defaultRecipient,
		onMessage:        onMessage,
		done:             make(chan struct{}),
	}

	if err := bot.startWebhook(ctx, listenAddr); err != nil {
		return nil, fmt.Errorf("whatsapp channel %q: start webhook: %w", channelID, err)
	}
	log.Printf("whatsapp: webhook listening on %s for channel %s", listenAddr, channelID)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

const whatsappAPIBase = "https://graph.facebook.com/v18.0"

type whatsappBot struct {
	mu               sync.Mutex
	channelID        string
	token            string
	phoneNumberID    string
	verifyToken      string
	defaultRecipient string
	onMessage        func(sdk.InboundChannelMessage)
	server           *http.Server
	done             chan struct{}
}

func (b *whatsappBot) ID() string { return b.channelID }

func (b *whatsappBot) Send(ctx context.Context, text string) error {
	b.mu.Lock()
	to := b.defaultRecipient
	b.mu.Unlock()
	if to == "" {
		return fmt.Errorf("whatsapp %s: no default_recipient configured; use whatsapp.send gateway method with explicit 'to'", b.channelID)
	}
	_, err := sendWhatsAppMessage(ctx, b.token, b.phoneNumberID, to, text)
	return err
}

func (b *whatsappBot) Close() {
	if b.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.server.Shutdown(ctx)
	}
	close(b.done)
}

func (b *whatsappBot) startWebhook(ctx context.Context, listenAddr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			b.handleVerify(w, r)
		case http.MethodPost:
			b.handleEvent(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	b.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		if sErr := b.server.Serve(ln); sErr != nil && sErr != http.ErrServerClosed {
			log.Printf("whatsapp webhook server error channel=%s: %v", b.channelID, sErr)
		}
	}()
	// Shut down when the parent context is done.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.server.Shutdown(shutCtx)
	}()
	return nil
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
	w.WriteHeader(http.StatusOK) // Always 200 to Meta.

	var event struct {
		Object string `json:"object"`
		Entry  []struct {
			Changes []struct {
				Value struct {
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
			for _, msg := range change.Value.Messages {
				if msg.Type != "text" || msg.Text == nil || msg.Text.Body == "" {
					continue
				}
				// Track last sender for Send() fallback.
				b.mu.Lock()
				if b.defaultRecipient == "" {
					b.defaultRecipient = msg.From
				}
				b.mu.Unlock()

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

// sendWhatsAppMessage sends a text message via the Meta Cloud API.
func sendWhatsAppMessage(ctx context.Context, token, phoneNumberID, to, text string) (string, error) {
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

	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
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
