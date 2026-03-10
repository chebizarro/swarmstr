// Package nextcloud implements a Nextcloud Talk channel extension for swarmstr.
//
// Supports two inbound modes:
//   - Webhook push (preferred): register an HTTP handler at webhook_path;
//     Nextcloud Talk posts new messages to this URL.
//   - Polling: if webhook_path is empty, poll the Talk API every poll_interval_s
//     seconds (default 5).
//
// Outbound messages are sent via the OCS v2 chat REST API.
//
// Registration: import _ "swarmstr/internal/extensions/nextcloud" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "base_url":         "https://cloud.example.com",  // required
//	  "username":         "bot_user",                   // required
//	  "app_password":     "app-password",               // required
//	  "room_token":       "abc123",                     // required: Talk room token
//	  "webhook_path":     "/webhooks/nextcloud/my-ch",  // optional; enables push mode
//	  "poll_interval_s":  5,                            // optional; polling interval (push mode disables this)
//	  "allowed_senders":  []                            // optional: allowlist of Nextcloud usernames
//	}
//
// Webhook endpoint (push mode): <admin_addr>/webhooks/nextcloud/<channel_id>
package nextcloud

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
	channels.RegisterChannelPlugin(&NextcloudPlugin{})
}

// NextcloudPlugin is the factory for Nextcloud Talk channel instances.
type NextcloudPlugin struct{}

func (p *NextcloudPlugin) ID() string   { return "nextcloud-talk" }
func (p *NextcloudPlugin) Type() string { return "Nextcloud Talk" }

func (p *NextcloudPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"base_url": map[string]any{
				"type":        "string",
				"description": "Nextcloud instance URL, e.g. https://cloud.example.com.",
			},
			"username": map[string]any{
				"type":        "string",
				"description": "Nextcloud username for the bot account.",
			},
			"app_password": map[string]any{
				"type":        "string",
				"description": "Nextcloud app password (generated in Security → App passwords).",
			},
			"room_token": map[string]any{
				"type":        "string",
				"description": "Talk room token (from the room URL, e.g. abc123).",
			},
			"webhook_path": map[string]any{
				"type":        "string",
				"description": "Optional HTTP path for push mode. If set, Nextcloud posts new messages here.",
			},
			"poll_interval_s": map[string]any{
				"type":        "integer",
				"description": "Polling interval in seconds when not using webhook push (default 5).",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of Nextcloud actor IDs.",
			},
		},
		"required": []string{"base_url", "username", "app_password", "room_token"},
	}
}

func (p *NextcloudPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Reactions: true, MultiAccount: true}
}

func (p *NextcloudPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *NextcloudPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	baseURL, _ := cfg["base_url"].(string)
	baseURL = strings.TrimRight(baseURL, "/")
	username, _ := cfg["username"].(string)
	appPassword, _ := cfg["app_password"].(string)
	roomToken, _ := cfg["room_token"].(string)
	if baseURL == "" || username == "" || appPassword == "" || roomToken == "" {
		return nil, fmt.Errorf("nextcloud-talk channel %q: base_url, username, app_password and room_token are required", channelID)
	}

	pollInterval := 5 * time.Second
	if v, ok := cfg["poll_interval_s"].(float64); ok && v > 0 {
		pollInterval = time.Duration(v) * time.Second
	}

	allowedSenders := map[string]bool{}
	if v, ok := cfg["allowed_senders"].([]interface{}); ok {
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[e] = true
			}
		}
	}

	webhookPath, _ := cfg["webhook_path"].(string)

	bot := &nextcloudBot{
		channelID:      channelID,
		baseURL:        baseURL,
		username:       username,
		appPassword:    appPassword,
		roomToken:      roomToken,
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}

	if webhookPath != "" {
		// Push mode: register HTTP handler.
		registerWebhook(channelID, bot)
		log.Printf("nextcloud-talk: webhook registered channel=%s path=%s", channelID, webhookPath)
	} else {
		// Polling mode.
		go bot.pollLoop(ctx, pollInterval)
		log.Printf("nextcloud-talk: polling mode channel=%s interval=%v", channelID, pollInterval)
	}
	return bot, nil
}

// ─── Global webhook registry ──────────────────────────────────────────────────

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*nextcloudBot{}
)

func registerWebhook(channelID string, bot *nextcloudBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an inbound Nextcloud Talk push event.
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

type nextcloudBot struct {
	channelID      string
	baseURL        string
	username       string
	appPassword    string
	roomToken      string
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client

	seenMu   sync.Mutex
	seenIDs  map[int64]struct{}
	lastMsgID int64
}

func (b *nextcloudBot) ID() string { return b.channelID }

func (b *nextcloudBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
}

// ─── Polling ─────────────────────────────────────────────────────────────────

type ncChatMsg struct {
	ID        int64  `json:"id"`
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

func (b *nextcloudBot) pollLoop(ctx context.Context, interval time.Duration) {
	b.seenIDs = map[int64]struct{}{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-ticker.C:
			b.pollOnce(ctx)
		}
	}
}

func (b *nextcloudBot) pollOnce(ctx context.Context) {
	apiURL := fmt.Sprintf("%s/ocs/v2.php/apps/spreed/api/v1/chat/%s?lookIntoFuture=1&limit=20&lastKnownMessageId=%d",
		b.baseURL, b.roomToken, b.lastMsgID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth(b.username, b.appPassword)
	req.Header.Set("OCS-APIREQUEST", "true")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var result struct {
		OCS struct {
			Data []ncChatMsg `json:"data"`
		} `json:"ocs"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return
	}

	b.seenMu.Lock()
	defer b.seenMu.Unlock()

	for _, msg := range result.OCS.Data {
		if _, seen := b.seenIDs[msg.ID]; seen {
			continue
		}
		b.seenIDs[msg.ID] = struct{}{}
		if msg.ID > b.lastMsgID {
			b.lastMsgID = msg.ID
		}

		if msg.ActorID == b.username {
			continue // skip own messages
		}
		text := strings.TrimSpace(msg.Message)
		if text == "" {
			continue
		}
		if len(b.allowedSenders) > 0 && !b.allowedSenders[msg.ActorID] {
			continue
		}
		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  msg.ActorID,
			Text:      text,
			CreatedAt: msg.Timestamp,
		})
	}
}

// ─── Webhook push handler ─────────────────────────────────────────────────────

func (b *nextcloudBot) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var msg ncChatMsg
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}
	if msg.ActorID == b.username {
		w.WriteHeader(http.StatusOK)
		return
	}
	text := strings.TrimSpace(msg.Message)
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if len(b.allowedSenders) > 0 && !b.allowedSenders[msg.ActorID] {
		w.WriteHeader(http.StatusOK)
		return
	}
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  msg.ActorID,
		Text:      text,
		CreatedAt: msg.Timestamp,
	})
	w.WriteHeader(http.StatusOK)
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func (b *nextcloudBot) Send(ctx context.Context, text string) error {
	apiURL := fmt.Sprintf("%s/ocs/v2.php/apps/spreed/api/v1/chat/%s", b.baseURL, b.roomToken)
	payload, _ := json.Marshal(map[string]any{"message": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.SetBasicAuth(b.username, b.appPassword)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OCS-APIREQUEST", "true")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("nextcloud-talk send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("nextcloud-talk send: status %d", resp.StatusCode)
	}
	return nil
}
