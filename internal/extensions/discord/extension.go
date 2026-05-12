// Package discord implements a Discord Bot channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/discord" in the daemon
// main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "bot_token":  "Bot <token>",      // required: Discord bot token (include "Bot " prefix)
//	  "channel_id": "1234567890",       // required: Discord channel ID to listen/send on
//	  "guild_id":   "1234567890",       // optional: enables guild directory methods
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
// The extension uses Discord's REST API for outbound messages and the Discord
// Gateway WebSocket for inbound real-time events.
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

	"github.com/gorilla/websocket"

	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("discord", func() sdk.ChannelPlugin { return &DiscordPlugin{} })
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
				"description": "Optional Discord guild ID used by directory and thread-list gateway methods.",
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
		Audio:        true,
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
				msgID, err := sendDiscordPayload(ctx, token, channelID, map[string]any{"content": text})
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "message_id": msgID}, nil
			},
		},
		{
			Method:      "discord.send_media",
			Description: "Send a Discord message with an attached media URL embed",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				return discordSendRich(ctx, params, "discord.send_media", func(text string, params map[string]any) (map[string]any, error) {
					mediaURL, _ := params["media_url"].(string)
					if mediaURL == "" {
						return nil, fmt.Errorf("discord.send_media: media_url is required")
					}
					payload := map[string]any{"content": text, "embeds": []map[string]any{{"url": mediaURL, "image": map[string]any{"url": mediaURL}}}}
					return payload, nil
				})
			},
		},
		{
			Method:      "discord.send_poll",
			Description: "Send a Discord poll message",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				return discordSendRich(ctx, params, "discord.send_poll", func(text string, params map[string]any) (map[string]any, error) {
					question, _ := params["question"].(string)
					answers, ok := params["answers"].([]any)
					if question == "" || !ok || len(answers) == 0 {
						return nil, fmt.Errorf("discord.send_poll: question and answers are required")
					}
					pollAnswers := make([]map[string]any, 0, len(answers))
					for _, answer := range answers {
						label, _ := answer.(string)
						if strings.TrimSpace(label) != "" {
							pollAnswers = append(pollAnswers, map[string]any{"poll_media": map[string]any{"text": label}})
						}
					}
					return map[string]any{"content": text, "poll": map[string]any{"question": map[string]any{"text": question}, "answers": pollAnswers, "duration": 24, "allow_multiselect": false}}, nil
				})
			},
		},
		{
			Method:      "discord.send_components",
			Description: "Send a Discord message with components",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				return discordSendRich(ctx, params, "discord.send_components", func(text string, params map[string]any) (map[string]any, error) {
					components, ok := params["components"].([]any)
					if !ok || len(components) == 0 {
						return nil, fmt.Errorf("discord.send_components: components are required")
					}
					return map[string]any{"content": text, "components": components}, nil
				})
			},
		},
		{
			Method:      "discord.create_thread",
			Description: "Create a Discord thread from a message",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, channelID, err := discordRequiredAuth(params, "discord.create_thread")
				if err != nil {
					return nil, err
				}
				messageID, _ := params["message_id"].(string)
				name, _ := params["name"].(string)
				if messageID == "" || name == "" {
					return nil, fmt.Errorf("discord.create_thread: message_id and name are required")
				}
				return discordRESTJSON(ctx, token, http.MethodPost, fmt.Sprintf("%s/channels/%s/messages/%s/threads", discordAPIBase, channelID, messageID), map[string]any{"name": name, "auto_archive_duration": 1440})
			},
		},
		{
			Method:      "discord.list_threads",
			Description: "List active Discord threads for a guild",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _, err := discordRequiredAuth(params, "discord.list_threads")
				if err != nil {
					return nil, err
				}
				guildID, _ := params["guild_id"].(string)
				if guildID == "" {
					return nil, fmt.Errorf("discord.list_threads: guild_id is required")
				}
				return discordRESTJSON(ctx, token, http.MethodGet, fmt.Sprintf("%s/guilds/%s/threads/active", discordAPIBase, guildID), nil)
			},
		},
		{
			Method:      "discord.directory",
			Description: "List Discord guild channels",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _, err := discordRequiredAuth(params, "discord.directory")
				if err != nil {
					return nil, err
				}
				guildID, _ := params["guild_id"].(string)
				if guildID == "" {
					return nil, fmt.Errorf("discord.directory: guild_id is required")
				}
				return discordRESTJSON(ctx, token, http.MethodGet, fmt.Sprintf("%s/guilds/%s/channels", discordAPIBase, guildID), nil)
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
	guildID, _ := cfg["guild_id"].(string)

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
		guildID:          guildID,
		onMessage:        onMessage,
		done:             make(chan struct{}),
		gatewayURL:       discordGatewayURL,
	}

	go bot.runGateway(ctx)
	log.Printf("discord: gateway started for channel %s (discord_channel=%s)", channelID, discordChannelID)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

const (
	discordAPIBase    = "https://discord.com/api/v10"
	discordGatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
)

type discordBot struct {
	mu                sync.Mutex
	channelID         string
	token             string
	discordChannelID  string
	guildID           string
	onMessage         func(sdk.InboundChannelMessage)
	lastMessageID     string
	done              chan struct{}
	httpClient        *http.Client
	gatewayURL        string
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
	select {
	case <-b.done:
	default:
		close(b.done)
	}
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

// SendAudio delivers an audio attachment URL as a Discord message. Discord voice
// channel capture is outside the sdk.AudioHandle shape, so this method accepts
// bytes for compatibility and posts a placeholder unless callers use
// discord.send_media with a hosted audio URL.
func (b *discordBot) SendAudio(ctx context.Context, _ []byte, format string) error {
	if strings.TrimSpace(format) == "" {
		format = "audio"
	}
	return sendDiscordMessage(ctx, b.token, b.discordChannelID, fmt.Sprintf("[%s attachment omitted: upload via discord.send_media media_url]", format))
}

func (b *discordBot) runGateway(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		default:
		}
		if err := b.gatewaySession(ctx); err != nil {
			log.Printf("discord gateway channel=%s disconnected: %v", b.channelID, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

type discordGatewayEnvelope struct {
	Op int             `json:"op"`
	T  string          `json:"t,omitempty"`
	S  *int            `json:"s,omitempty"`
	D  json.RawMessage `json:"d"`
}

type discordGatewayHello struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type discordGatewayMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id,omitempty"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Type      int    `json:"type"`
	Author    *struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"author"`
	MessageReference *struct {
		MessageID string `json:"message_id"`
		ChannelID string `json:"channel_id"`
		GuildID   string `json:"guild_id,omitempty"`
	} `json:"message_reference,omitempty"`
	Attachments []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"attachments,omitempty"`
	Thread *struct {
		ID string `json:"id"`
	} `json:"thread,omitempty"`
}

func (b *discordBot) gatewaySession(ctx context.Context) error {
	gatewayURL := b.gatewayURL
	if gatewayURL == "" {
		gatewayURL = discordGatewayURL
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, gatewayURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	var seq *int
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	var hello discordGatewayEnvelope
	if err := json.Unmarshal(raw, &hello); err != nil {
		return err
	}
	if hello.Op != 10 {
		return fmt.Errorf("expected gateway HELLO op=10, got op=%d", hello.Op)
	}
	var helloData discordGatewayHello
	if err := json.Unmarshal(hello.D, &helloData); err != nil {
		return err
	}
	interval := time.Duration(helloData.HeartbeatInterval) * time.Millisecond
	if interval <= 0 {
		interval = 45 * time.Second
	}

	identify := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   strings.TrimPrefix(b.token, "Bot "),
			"intents": 1 << 9,
			"properties": map[string]string{
				"os":      "metiq",
				"browser": "metiq",
				"device":  "metiq",
			},
		},
	}
	if err := conn.WriteJSON(identify); err != nil {
		return err
	}

	heartbeatDone := make(chan struct{})
	sessionDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(heartbeatDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.done:
				return
			case <-sessionDone:
				return
			case <-ticker.C:
				if err := conn.WriteJSON(map[string]any{"op": 1, "d": seq}); err != nil {
					return
				}
			}
		}
	}()
	defer func() {
		close(sessionDone)
		<-heartbeatDone
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.done:
			return nil
		default:
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var env discordGatewayEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		if env.S != nil {
			seq = env.S
		}
		switch env.Op {
		case 0:
			if env.T == "MESSAGE_CREATE" {
				b.handleGatewayMessage(env.D)
			}
		case 1:
			if err := conn.WriteJSON(map[string]any{"op": 1, "d": seq}); err != nil {
				return err
			}
		case 7, 9:
			return fmt.Errorf("gateway requested reconnect op=%d", env.Op)
		}
	}
}

func (b *discordBot) handleGatewayMessage(raw json.RawMessage) {
	var msg discordGatewayMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	if msg.ChannelID != b.discordChannelID {
		return
	}
	if msg.Author != nil && msg.Author.Bot {
		return
	}
	text := msg.Content
	mediaURL := ""
	mediaMIME := ""
	if len(msg.Attachments) > 0 {
		mediaURL = msg.Attachments[0].URL
		mediaMIME = msg.Attachments[0].ContentType
		if strings.TrimSpace(text) == "" {
			text = mediaURL
		}
	}
	if strings.TrimSpace(text) == "" && mediaURL == "" {
		return
	}
	senderID := ""
	if msg.Author != nil {
		senderID = msg.Author.Username + "#" + msg.Author.ID
	}
	replyToEventID := ""
	if msg.MessageReference != nil && strings.TrimSpace(msg.MessageReference.MessageID) != "" {
		replyToEventID = "discord-" + strings.TrimSpace(msg.MessageReference.MessageID)
	}
	threadID := ""
	if msg.Thread != nil {
		threadID = msg.Thread.ID
	} else if b.isThreadChannel {
		threadID = b.discordChannelID
	}
	createdAt := int64(0)
	if ts, err := time.Parse(time.RFC3339Nano, msg.Timestamp); err == nil {
		createdAt = ts.Unix()
	}
	b.mu.Lock()
	if msg.ID > b.lastMessageID {
		b.lastMessageID = msg.ID
	}
	b.mu.Unlock()
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID:      b.channelID,
		SenderID:       senderID,
		Text:           text,
		EventID:        "discord-" + msg.ID,
		CreatedAt:      createdAt,
		ThreadID:       threadID,
		ReplyToEventID: replyToEventID,
		MediaURL:       mediaURL,
		MediaMIME:      mediaMIME,
	})
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

func discordRequiredAuth(params map[string]any, method string) (string, string, error) {
	token, _ := params["bot_token"].(string)
	channelID, _ := params["channel_id"].(string)
	if token == "" || channelID == "" {
		return "", "", fmt.Errorf("%s: bot_token and channel_id are required", method)
	}
	return token, channelID, nil
}

func discordSendRich(ctx context.Context, params map[string]any, method string, build func(string, map[string]any) (map[string]any, error)) (map[string]any, error) {
	token, channelID, err := discordRequiredAuth(params, method)
	if err != nil {
		return nil, err
	}
	text, _ := params["text"].(string)
	payload, err := build(text, params)
	if err != nil {
		return nil, err
	}
	msgID, err := sendDiscordPayload(ctx, token, channelID, payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "message_id": msgID}, nil
}

func discordRESTJSON(ctx context.Context, token, method, apiURL string, payload map[string]any) (map[string]any, error) {
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord API: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discord API: HTTP %d: %s", resp.StatusCode, raw)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{"ok": true}, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("discord API: decode response: %w", err)
	}
	return map[string]any{"ok": true, "result": decoded}, nil
}

func sendDiscordPayload(ctx context.Context, token, channelID string, payload map[string]any) (string, error) {
	url := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, channelID)
	rawBody, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("discord sendMessage: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord sendMessage: HTTP %d: %s", resp.StatusCode, raw)
	}
	var result struct {
		ID string `json:"id"`
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		_ = json.Unmarshal(raw, &result)
	}
	return result.ID, nil
}

// sendDiscordMessage posts a message to a Discord channel via REST API.
func sendDiscordMessage(ctx context.Context, token, channelID, text string) error {
	_, err := sendDiscordPayload(ctx, token, channelID, map[string]any{"content": text})
	return err
}
