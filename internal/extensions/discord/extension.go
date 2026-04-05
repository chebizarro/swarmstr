// Package discord implements a Discord Bot channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/discord" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "bot_token":  "Bot <token>",      // required: Discord bot token (include "Bot " prefix)
//	  "channel_id": "1234567890",       // required: Discord channel ID to listen/send on
//	  "guild_id":   "0987654321"        // optional: guild ID for filtering
//	}
//
// To add a Discord channel to your metiq config:
//
//	"nostr_channels": {
//	  "discord-main": {
//	    "kind": "discord",
//	    "config": {
//	      "bot_token":  "Bot YOUR_TOKEN",
//	      "channel_id": "YOUR_CHANNEL_ID"
//	    }
//	  }
//	}
//
// The extension uses Discord's REST API for outbound messages and polls the
// /messages endpoint for inbound. For production use, switch to the Discord
// Gateway WebSocket (wss://gateway.discord.gg) for real-time event delivery.
package discord

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
	channels.RegisterChannelPlugin(&DiscordPlugin{})
}

// DiscordPlugin is the factory for Discord Bot channel instances.
type DiscordPlugin struct{}

func (d *DiscordPlugin) ID() string   { return "discord" }
func (d *DiscordPlugin) Type() string { return "Discord Bot" }

func (d *DiscordPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bot_token": map[string]any{
				"type":        "string",
				"description": "Discord bot token. Include the 'Bot ' prefix: 'Bot ABC...'",
			},
			"channel_id": map[string]any{
				"type":        "string",
				"description": "Discord channel ID to listen on and send messages to.",
			},
			"guild_id": map[string]any{
				"type":        "string",
				"description": "Optional: Discord guild (server) ID for context.",
			},
		},
		"required": []string{"bot_token", "channel_id"},
	}
}

// Capabilities declares the features supported by the Discord Bot channel.
func (d *DiscordPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       true,
		Reactions:    true,
		Threads:      true,
		Audio:        false,
		Edit:         true,
		MultiAccount: true,
	}
}

func (d *DiscordPlugin) GatewayMethods() []sdk.GatewayMethod {
	return []sdk.GatewayMethod{
		{
			Method:      "discord.send",
			Description: "Send a message to a Discord channel",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["bot_token"].(string)
				channelID, _ := params["channel_id"].(string)
				text, _ := params["text"].(string)
				if token == "" || channelID == "" || text == "" {
					return nil, fmt.Errorf("discord.send: bot_token, channel_id, and text are required")
				}
				if err := sendDiscordMessage(ctx, token, channelID, text); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			},
		},
	}
}

func (d *DiscordPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	token, _ := cfg["bot_token"].(string)
	discordChannelID, _ := cfg["channel_id"].(string)

	if token == "" {
		return nil, fmt.Errorf("discord channel %q: config.bot_token is required", channelID)
	}
	if discordChannelID == "" {
		return nil, fmt.Errorf("discord channel %q: config.channel_id is required", channelID)
	}

	bot := &discordBot{
		channelID:        channelID,
		token:            token,
		discordChannelID: discordChannelID,
		onMessage:        onMessage,
		done:             make(chan struct{}),
	}

	go bot.poll(ctx)
	log.Printf("discord: polling started for channel %s (discord_channel=%s)", channelID, discordChannelID)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

const discordAPIBase = "https://discord.com/api/v10"

type discordBot struct {
	mu                sync.Mutex
	channelID         string
	token             string
	discordChannelID  string
	onMessage         func(sdk.InboundChannelMessage)
	lastMessageID     string
	done              chan struct{}
	httpClient        *http.Client
	channelMetaLoaded bool
	isThreadChannel   bool
}

func (b *discordBot) ID() string { return b.channelID }

func (b *discordBot) client(timeout time.Duration) *http.Client {
	if b.httpClient != nil {
		return b.httpClient
	}
	return &http.Client{Timeout: timeout}
}

func isDiscordThreadType(kind int) bool {
	switch kind {
	case 10, 11, 12:
		return true
	default:
		return false
	}
}

func (b *discordBot) ensureChannelMetadata(ctx context.Context) {
	b.mu.Lock()
	if b.channelMetaLoaded {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	apiURL := fmt.Sprintf("%s/channels/%s", discordAPIBase, b.discordChannelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", b.token)

	resp, err := b.client(10 * time.Second).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}

	var channel struct {
		Type int `json:"type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&channel); err != nil {
		return
	}

	b.mu.Lock()
	b.channelMetaLoaded = true
	b.isThreadChannel = isDiscordThreadType(channel.Type)
	b.mu.Unlock()
}

func (b *discordBot) Send(ctx context.Context, text string) error {
	return sendDiscordMessage(ctx, b.token, b.discordChannelID, text)
}

func (b *discordBot) Close() {
	close(b.done)
}

// ─── TypingHandle ─────────────────────────────────────────────────────────────

// SendTyping triggers the typing indicator in the configured Discord channel.
func (b *discordBot) SendTyping(ctx context.Context, _ int) error {
	apiURL := fmt.Sprintf("%s/channels/%s/typing", discordAPIBase, b.discordChannelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.token)
	resp, err := b.client(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("discord sendTyping: %w", err)
	}
	resp.Body.Close()
	return nil
}

// ─── ReactionHandle ──────────────────────────────────────────────────────────

// AddReaction adds an emoji reaction to a message.
// eventID must be of the form "discord-{message_id}".
func (b *discordBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	msgID := strings.TrimPrefix(eventID, "discord-")
	apiURL := fmt.Sprintf("%s/channels/%s/messages/%s/reactions/%s/@me",
		discordAPIBase, b.discordChannelID, msgID, url.PathEscape(emoji))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.token)
	resp, err := b.client(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("discord addReaction: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord addReaction: HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
}

// RemoveReaction removes the bot's emoji reaction from a message.
func (b *discordBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	msgID := strings.TrimPrefix(eventID, "discord-")
	apiURL := fmt.Sprintf("%s/channels/%s/messages/%s/reactions/%s/@me",
		discordAPIBase, b.discordChannelID, msgID, url.PathEscape(emoji))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.token)
	resp, err := b.client(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("discord removeReaction: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord removeReaction: HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
}

// ─── EditHandle ──────────────────────────────────────────────────────────────

// EditMessage replaces the content of a previously sent Discord message.
// eventID must be of the form "discord-{message_id}".
func (b *discordBot) EditMessage(ctx context.Context, eventID, newText string) error {
	msgID := strings.TrimPrefix(eventID, "discord-")
	apiURL := fmt.Sprintf("%s/channels/%s/messages/%s", discordAPIBase, b.discordChannelID, msgID)
	body, _ := json.Marshal(map[string]any{"content": newText})
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client(15 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("discord editMessage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord editMessage: HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
}

// ─── ThreadHandle ────────────────────────────────────────────────────────────

// SendInThread posts a message to a Discord thread channel.
// threadID is the Discord channel ID of the thread (threads are channels in Discord).
func (b *discordBot) SendInThread(ctx context.Context, threadID, text string) error {
	return sendDiscordMessage(ctx, b.token, threadID, text)
}

func (b *discordBot) poll(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-ticker.C:
			b.fetchMessages(ctx)
		}
	}
}

func (b *discordBot) fetchMessages(ctx context.Context) {
	b.ensureChannelMetadata(ctx)

	b.mu.Lock()
	afterID := b.lastMessageID
	isThreadChannel := b.isThreadChannel
	b.mu.Unlock()

	url := fmt.Sprintf("%s/channels/%s/messages?limit=10", discordAPIBase, b.discordChannelID)
	if afterID != "" {
		url += "&after=" + afterID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", b.token)

	resp, err := b.client(10 * time.Second).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var messages []struct {
		ID               string `json:"id"`
		Content          string `json:"content"`
		Timestamp        string `json:"timestamp"`
		MessageReference *struct {
			MessageID string `json:"message_id"`
		} `json:"message_reference,omitempty"`
		Author *struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Bot      bool   `json:"bot"`
		} `json:"author"`
	}

	if err := json.Unmarshal(raw, &messages); err != nil {
		return
	}

	// Messages come newest-first; reverse to process in order.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	for _, msg := range messages {
		// Skip bot messages to avoid reply loops.
		if msg.Author != nil && msg.Author.Bot {
			b.mu.Lock()
			if msg.ID > b.lastMessageID {
				b.lastMessageID = msg.ID
			}
			b.mu.Unlock()
			continue
		}

		if msg.Content == "" {
			continue
		}

		b.mu.Lock()
		if msg.ID > b.lastMessageID {
			b.lastMessageID = msg.ID
		}
		b.mu.Unlock()

		senderID := ""
		if msg.Author != nil {
			senderID = msg.Author.Username + "#" + msg.Author.ID
		}

		replyToEventID := ""
		if msg.MessageReference != nil && strings.TrimSpace(msg.MessageReference.MessageID) != "" {
			replyToEventID = "discord-" + strings.TrimSpace(msg.MessageReference.MessageID)
		}
		threadID := ""
		if isThreadChannel {
			threadID = b.discordChannelID
		}

		b.onMessage(sdk.InboundChannelMessage{
			ChannelID:      b.channelID,
			SenderID:       senderID,
			Text:           msg.Content,
			EventID:        "discord-" + msg.ID,
			ThreadID:       threadID,
			ReplyToEventID: replyToEventID,
		})
	}
}

// sendDiscordMessage posts a message to a Discord channel via REST API.
func sendDiscordMessage(ctx context.Context, token, channelID, text string) error {
	url := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, channelID)
	body, _ := json.Marshal(map[string]any{"content": text})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("discord sendMessage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord sendMessage: HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
}
