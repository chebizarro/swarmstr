// Package telegram implements a Telegram Bot channel extension for swarmstr.
//
// Registration: import _ "swarmstr/internal/extensions/telegram" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "token": "123456:ABC-DEF...",           // required: Telegram bot token
//	  "webhook_url": "https://yourhost/...",  // optional: use webhook instead of polling
//	  "allowed_users": [123456789]            // optional: restrict by Telegram user ID
//	}
//
// To add a Telegram channel to your swarmstr config:
//
//	"nostr_channels": {
//	  "telegram-main": {
//	    "kind": "telegram",
//	    "config": { "token": "YOUR_BOT_TOKEN" }
//	  }
//	}
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"swarmstr/internal/gateway/channels"
	"swarmstr/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&TelegramPlugin{})
}

// TelegramPlugin is the factory for Telegram Bot channel instances.
type TelegramPlugin struct{}

func (t *TelegramPlugin) ID() string   { return "telegram" }
func (t *TelegramPlugin) Type() string { return "Telegram Bot" }

func (t *TelegramPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"token": map[string]any{
				"type":        "string",
				"description": "Telegram bot token from @BotFather (e.g. 123456:ABC-DEF...)",
			},
			"webhook_url": map[string]any{
				"type":        "string",
				"description": "Optional: HTTPS webhook URL. If absent, long-polling is used.",
			},
			"allowed_users": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Optional: list of Telegram user IDs allowed to send messages.",
			},
		},
		"required": []string{"token"},
	}
}

func (t *TelegramPlugin) GatewayMethods() []sdk.GatewayMethod {
	return []sdk.GatewayMethod{
		{
			Method:      "telegram.send",
			Description: "Send a message to a Telegram chat",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["token"].(string)
				chatID, _ := params["chat_id"].(string)
				text, _ := params["text"].(string)
				if token == "" || chatID == "" || text == "" {
					return nil, fmt.Errorf("telegram.send: token, chat_id, and text are required")
				}
				if err := sendTelegramMessage(ctx, token, chatID, text); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			},
		},
	}
}

func (t *TelegramPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	token, _ := cfg["token"].(string)
	if token == "" {
		return nil, fmt.Errorf("telegram channel %q: config.token is required", channelID)
	}

	var allowedUsers []int64
	if users, ok := cfg["allowed_users"].([]any); ok {
		for _, u := range users {
			if id, ok := u.(float64); ok {
				allowedUsers = append(allowedUsers, int64(id))
			}
		}
	}

	bot := &telegramBot{
		channelID:    channelID,
		token:        token,
		allowedUsers: allowedUsers,
		onMessage:    onMessage,
		done:         make(chan struct{}),
	}

	go bot.poll(ctx)
	log.Printf("telegram: polling started for channel %s", channelID)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

type telegramBot struct {
	mu           sync.Mutex
	channelID    string
	token        string
	allowedUsers []int64
	onMessage    func(sdk.InboundChannelMessage)
	lastUpdateID int64
	lastChatID   string
	done         chan struct{}
}

func (b *telegramBot) ID() string { return b.channelID }

func (b *telegramBot) Send(ctx context.Context, text string) error {
	// When used as a reply-to-all bot, we need to track the last chat ID.
	// For direct sends, the caller should use telegram.send gateway method.
	b.mu.Lock()
	chatID := b.lastChatID
	b.mu.Unlock()
	if chatID == "" {
		return fmt.Errorf("telegram %s: no chat ID known yet (no messages received)", b.channelID)
	}
	return sendTelegramMessage(ctx, b.token, chatID, text)
}

func (b *telegramBot) Close() {
	close(b.done)
}

func (b *telegramBot) poll(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-ticker.C:
			b.fetchUpdates(ctx)
		}
	}
}

func (b *telegramBot) fetchUpdates(ctx context.Context) {
	b.mu.Lock()
	offset := b.lastUpdateID + 1
	b.mu.Unlock()

	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=1&limit=100", b.token, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	cl := &http.Client{Timeout: 10 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var result struct {
		OK     bool `json:"ok"`
		Result []struct {
			UpdateID int64 `json:"update_id"`
			Message  *struct {
				MessageID int64  `json:"message_id"`
				Text      string `json:"text"`
				Date      int64  `json:"date"`
				From      *struct {
					ID       int64  `json:"id"`
					Username string `json:"username"`
				} `json:"from"`
				Chat *struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
	}

	if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
		return
	}

	for _, update := range result.Result {
		if update.UpdateID >= offset {
			b.mu.Lock()
			if update.UpdateID > b.lastUpdateID {
				b.lastUpdateID = update.UpdateID
			}
			b.mu.Unlock()
		}

		msg := update.Message
		if msg == nil || msg.Text == "" {
			continue
		}

		// Check allowed users.
		if len(b.allowedUsers) > 0 && msg.From != nil {
			allowed := false
			for _, uid := range b.allowedUsers {
				if msg.From.ID == uid {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}

		senderID := ""
		chatIDStr := ""
		if msg.From != nil {
			senderID = fmt.Sprintf("%d", msg.From.ID)
		}
		if msg.Chat != nil {
			chatIDStr = fmt.Sprintf("%d", msg.Chat.ID)
			b.mu.Lock()
			b.lastChatID = chatIDStr
			b.mu.Unlock()
		}

		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  senderID,
			Text:      msg.Text,
			EventID:   fmt.Sprintf("tg-%d", msg.MessageID),
			CreatedAt: msg.Date,
		})
	}
}

// sendTelegramMessage sends a text message to a Telegram chat.
func sendTelegramMessage(ctx context.Context, token, chatID, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	body, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendMessage: HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
}
