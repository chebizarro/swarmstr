// Package slack implements a Slack Bot channel extension for swarmstr.
//
// Registration: import _ "swarmstr/internal/extensions/slack" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "bot_token":  "xoxb-...",          // required: Slack bot OAuth token
//	  "channel_id": "C1234567890",       // required: Slack channel ID to listen/send on
//	  "bot_user_id": "U0987654321"       // optional: bot's own user ID to skip its messages
//	}
//
// To add a Slack channel to your swarmstr config:
//
//	"nostr_channels": {
//	  "slack-main": {
//	    "kind": "slack",
//	    "config": {
//	      "bot_token":  "xoxb-YOUR_BOT_TOKEN",
//	      "channel_id": "C_YOUR_CHANNEL_ID"
//	    }
//	  }
//	}
//
// The extension uses Slack's Web API (conversations.history for polling,
// chat.postMessage for sending). For production deployments, consider
// switching to Slack's Events API or Socket Mode for real-time delivery.
package slack

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

	"swarmstr/internal/gateway/channels"
	"swarmstr/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&SlackPlugin{})
}

// SlackPlugin is the factory for Slack Bot channel instances.
type SlackPlugin struct{}

func (s *SlackPlugin) ID() string   { return "slack" }
func (s *SlackPlugin) Type() string { return "Slack Bot" }

func (s *SlackPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bot_token": map[string]any{
				"type":        "string",
				"description": "Slack bot OAuth token starting with 'xoxb-'.",
			},
			"channel_id": map[string]any{
				"type":        "string",
				"description": "Slack channel ID (e.g. C1234567890) to listen on and send to.",
			},
			"bot_user_id": map[string]any{
				"type":        "string",
				"description": "Optional: bot's own Slack user ID (Uxxx) to skip its own messages.",
			},
		},
		"required": []string{"bot_token", "channel_id"},
	}
}

// Capabilities declares the features supported by the Slack Bot channel.
func (s *SlackPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       false, // Slack removed bot typing API in 2021
		Reactions:    true,
		Threads:      true,
		Audio:        false,
		Edit:         true,
		MultiAccount: true,
	}
}

func (s *SlackPlugin) GatewayMethods() []sdk.GatewayMethod {
	return []sdk.GatewayMethod{
		{
			Method:      "slack.send",
			Description: "Send a message to a Slack channel",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["bot_token"].(string)
				channelID, _ := params["channel_id"].(string)
				text, _ := params["text"].(string)
				if token == "" || channelID == "" || text == "" {
					return nil, fmt.Errorf("slack.send: bot_token, channel_id, and text are required")
				}
				ts, err := postSlackMessage(ctx, token, channelID, text)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "ts": ts}, nil
			},
		},
	}
}

func (s *SlackPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	token, _ := cfg["bot_token"].(string)
	slackChannelID, _ := cfg["channel_id"].(string)
	botUserID, _ := cfg["bot_user_id"].(string)

	if token == "" {
		return nil, fmt.Errorf("slack channel %q: config.bot_token is required", channelID)
	}
	if slackChannelID == "" {
		return nil, fmt.Errorf("slack channel %q: config.channel_id is required", channelID)
	}

	bot := &slackBot{
		channelID:      channelID,
		token:          token,
		slackChannelID: slackChannelID,
		botUserID:      botUserID,
		onMessage:      onMessage,
		done:           make(chan struct{}),
	}

	go bot.poll(ctx)
	log.Printf("slack: polling started for channel %s (slack_channel=%s)", channelID, slackChannelID)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

const slackAPIBase = "https://slack.com/api"

type slackBot struct {
	mu             sync.Mutex
	channelID      string
	token          string
	slackChannelID string
	botUserID      string
	onMessage      func(sdk.InboundChannelMessage)
	// lastTS is the Slack message timestamp cursor (float string like "1234567890.123456").
	lastTS string
	done   chan struct{}
}

func (b *slackBot) ID() string { return b.channelID }

func (b *slackBot) Send(ctx context.Context, text string) error {
	_, err := postSlackMessage(ctx, b.token, b.slackChannelID, text)
	return err
}

func (b *slackBot) Close() {
	close(b.done)
}

// ─── ReactionHandle ──────────────────────────────────────────────────────────

// AddReaction adds an emoji reaction to a message.
// eventID must be of the form "slack-{ts}".
func (b *slackBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	ts := strings.TrimPrefix(eventID, "slack-")
	body, _ := json.Marshal(map[string]any{
		"channel":   b.slackChannelID,
		"timestamp": ts,
		"name":      emoji,
	})
	return b.slackPost(ctx, slackAPIBase+"/reactions.add", body)
}

// RemoveReaction removes a previously added emoji reaction from a message.
func (b *slackBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	ts := strings.TrimPrefix(eventID, "slack-")
	body, _ := json.Marshal(map[string]any{
		"channel":   b.slackChannelID,
		"timestamp": ts,
		"name":      emoji,
	})
	return b.slackPost(ctx, slackAPIBase+"/reactions.remove", body)
}

// ─── EditHandle ──────────────────────────────────────────────────────────────

// EditMessage updates the text of a previously sent Slack message.
// eventID must be of the form "slack-{ts}".
func (b *slackBot) EditMessage(ctx context.Context, eventID, newText string) error {
	ts := strings.TrimPrefix(eventID, "slack-")
	body, _ := json.Marshal(map[string]any{
		"channel": b.slackChannelID,
		"ts":      ts,
		"text":    newText,
	})
	return b.slackPost(ctx, slackAPIBase+"/chat.update", body)
}

// ─── ThreadHandle ────────────────────────────────────────────────────────────

// SendInThread posts a reply in a Slack thread.
// threadID is the Slack message timestamp of the parent message (the thread root).
func (b *slackBot) SendInThread(ctx context.Context, threadID, text string) error {
	body, _ := json.Marshal(map[string]any{
		"channel":   b.slackChannelID,
		"text":      text,
		"thread_ts": threadID,
	})
	return b.slackPost(ctx, slackAPIBase+"/chat.postMessage", body)
}

// slackPost is a convenience helper for authenticated Slack API POSTs.
func (b *slackBot) slackPost(ctx context.Context, apiURL string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var result struct {
		OK  bool   `json:"ok"`
		Err string `json:"error,omitempty"`
	}
	if jsonErr := json.Unmarshal(raw, &result); jsonErr != nil {
		return fmt.Errorf("slack API: decode response: %w", jsonErr)
	}
	if !result.OK {
		return fmt.Errorf("slack API: %s", result.Err)
	}
	return nil
}

func (b *slackBot) poll(ctx context.Context) {
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

func (b *slackBot) fetchMessages(ctx context.Context) {
	b.mu.Lock()
	oldest := b.lastTS
	b.mu.Unlock()

	// Use conversations.history to poll for new messages.
	params := url.Values{
		"channel": {b.slackChannelID},
		"limit":   {"20"},
	}
	if oldest != "" {
		params.Set("oldest", oldest)
		params.Set("inclusive", "false")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		slackAPIBase+"/conversations.history?"+params.Encode(), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.token)

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
		OK       bool   `json:"ok"`
		Error    string `json:"error,omitempty"`
		Messages []struct {
			Type    string `json:"type"`
			User    string `json:"user"`
			BotID   string `json:"bot_id,omitempty"`
			Text    string `json:"text"`
			Ts      string `json:"ts"`
			Subtype string `json:"subtype,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
		if result.Error != "" {
			log.Printf("slack poll channel=%s error=%s", b.slackChannelID, result.Error)
		}
		return
	}

	// Messages arrive newest-first; reverse to process in chronological order.
	msgs := result.Messages
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	for _, msg := range msgs {
		// Track the cursor.
		b.mu.Lock()
		if msg.Ts > b.lastTS {
			b.lastTS = msg.Ts
		}
		b.mu.Unlock()

		// Skip bot messages (including our own).
		if msg.BotID != "" || msg.Subtype == "bot_message" {
			continue
		}
		if b.botUserID != "" && msg.User == b.botUserID {
			continue
		}
		if msg.Type != "message" || msg.Text == "" {
			continue
		}

		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  msg.User,
			Text:      msg.Text,
			EventID:   "slack-" + msg.Ts,
		})
	}
}

// postSlackMessage sends text to a Slack channel via chat.postMessage.
// Returns the message timestamp (ts) on success.
func postSlackMessage(ctx context.Context, token, channelID, text string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"channel": channelID,
		"text":    text,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		slackAPIBase+"/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("slack postMessage: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result struct {
		OK  bool   `json:"ok"`
		Err string `json:"error,omitempty"`
		Ts  string `json:"ts,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("slack postMessage: decode response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("slack postMessage: %s", result.Err)
	}
	return result.Ts, nil
}
