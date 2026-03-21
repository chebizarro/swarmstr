// Package synology implements a Synology Chat channel extension for swarmstr.
//
// Synology Chat uses an incoming/outgoing webhook model exposed by Synology NAS.
// Inbound messages arrive via HTTP POST to a registered webhook path.
// Outbound messages are POSTed to the Synology Chat webhook_url.
//
// Registration: import _ "metiq/internal/extensions/synology" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "webhook_url":     "https://nas.example.com/webapi/entry.cgi?api=SYNO.Chat.External&method=incoming&version=2&token=...",
//	  "incoming_token":  "token-from-synology-chat-settings",
//	  "allowed_senders": []
//	}
//
// To add a Synology Chat channel to your swarmstr config:
//
//	"nostr_channels": {
//	  "synology-ops": {
//	    "kind": "synology-chat",
//	    "config": {
//	      "webhook_url":    "https://nas.example.com/...",
//	      "incoming_token": "your-token"
//	    }
//	  }
//	}
//
// Inbound webhook endpoint:  <admin_addr>/webhooks/synology/<channel_id>
package synology

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&SynologyPlugin{})
}

// SynologyPlugin is the factory for Synology Chat channel instances.
type SynologyPlugin struct{}

func (p *SynologyPlugin) ID() string   { return "synology-chat" }
func (p *SynologyPlugin) Type() string { return "Synology Chat" }

func (p *SynologyPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"webhook_url": map[string]any{
				"type":        "string",
				"description": "Synology Chat outgoing webhook URL (the URL you copy from Synology Chat → Integrations → Outgoing Webhooks).",
			},
			"incoming_token": map[string]any{
				"type":        "string",
				"description": "Token from Synology Chat → Integrations → Incoming Webhooks, used to verify inbound requests.",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of Synology Chat usernames.",
			},
		},
		"required": []string{"webhook_url", "incoming_token"},
	}
}

func (p *SynologyPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{}
}

func (p *SynologyPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *SynologyPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	webhookURL, _ := cfg["webhook_url"].(string)
	incomingToken, _ := cfg["incoming_token"].(string)
	if webhookURL == "" || incomingToken == "" {
		return nil, fmt.Errorf("synology-chat channel %q: webhook_url and incoming_token are required", channelID)
	}

	allowedSenders := map[string]bool{}
	if v, ok := cfg["allowed_senders"].([]interface{}); ok {
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[e] = true
			}
		}
	}

	bot := &synologyBot{
		channelID:      channelID,
		webhookURL:     webhookURL,
		incomingToken:  incomingToken,
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}

	registerWebhook(channelID, bot)
	log.Printf("synology-chat: webhook registered channel=%s", channelID)
	return bot, nil
}

// ─── Global webhook registry ──────────────────────────────────────────────────

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*synologyBot{}
)

func registerWebhook(channelID string, bot *synologyBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an inbound Synology Chat POST to the registered bot.
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

// ─── Bot ──────────────────────────────────────────────────────────────────────

type synologyBot struct {
	channelID      string
	webhookURL     string
	incomingToken  string
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client
}

func (b *synologyBot) ID() string { return b.channelID }

func (b *synologyBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
}

// handlePush parses an inbound Synology Chat webhook POST.
// Synology sends: token=<token>&user_id=<id>&username=<name>&text=<text>&channel_id=<id>
func (b *synologyBot) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// Synology sends form-encoded or JSON depending on the version.
	var token, userID, username, text string
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var payload struct {
			Token    string `json:"token"`
			UserID   string `json:"user_id"`
			Username string `json:"username"`
			Text     string `json:"text"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "parse body", http.StatusBadRequest)
			return
		}
		token, userID, username, text = payload.Token, payload.UserID, payload.Username, payload.Text
	} else {
		vals, _ := url.ParseQuery(string(body))
		token = vals.Get("token")
		userID = vals.Get("user_id")
		username = vals.Get("username")
		text = vals.Get("text")
	}

	if token != b.incomingToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	senderID := username
	if senderID == "" {
		senderID = userID
	}

	if len(b.allowedSenders) > 0 && !b.allowedSenders[senderID] {
		w.WriteHeader(http.StatusOK)
		return
	}

	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  senderID,
		Text:      text,
	})
	w.WriteHeader(http.StatusOK)
}

// Send POSTs a message to the Synology Chat incoming webhook URL.
func (b *synologyBot) Send(ctx context.Context, text string) error {
	payload, _ := json.Marshal(map[string]any{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.webhookURL,
		bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("synology-chat send: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("synology-chat send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("synology-chat send: status %d", resp.StatusCode)
	}
	return nil
}
