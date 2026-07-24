// Package slack implements a Slack Bot channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/slack" in the daemon
// main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "bot_token":  "xoxb-...",          // required: Slack bot OAuth token
//	  "channel_id": "C1234567890",       // required: Slack channel ID to listen/send on
//	  "bot_user_id": "U0987654321"       // optional: bot's own user ID to skip its messages
//	}
//
// To add a Slack channel to your metiq config:
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
// The extension receives inbound Slack Events API callbacks at
// <admin_addr>/webhooks/slack/<channel_id> and uses Slack's Web API for outbound
// messages, reactions, edits, threads, Block Kit, and file uploads.
package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"metiq/internal/extensions/channelmedia"
	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("slack", func() sdk.ChannelPlugin { return &SlackPlugin{} })
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
			"signing_secret": map[string]any{
				"type":        "string",
				"description": "Slack app signing secret used to verify Events API and interactivity webhooks.",
			},
			"blocks": map[string]any{
				"type":        "array",
				"description": "Optional default Block Kit blocks used for outbound messages when no per-call blocks are supplied.",
			},
		},
		"required": []string{"bot_token", "channel_id"},
	}
}

// Capabilities declares the features supported by the Slack Bot channel.
func (s *SlackPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:          true, // Assistant threads expose status-based typing lifecycle.
		Reactions:       true,
		Threads:         true,
		Audio:           false,
		Edit:            true,
		MultiAccount:    true,
		Media:           true,
		DirectTextMedia: true,
	}
}

func (s *SlackPlugin) GatewayMethods() []sdk.GatewayMethod {
	return channels.AccountScopedGatewayMethods(s.ID(), []sdk.GatewayMethod{
		{
			Method:      "slack.send",
			Description: "Send a message to a Slack channel",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["bot_token"].(string)
				channelID, _ := params["channel_id"].(string)
				text, _ := params["text"].(string)
				blocks, err := normalizeSlackBlocks(params["blocks"])
				if err != nil {
					return nil, fmt.Errorf("slack.send: %w", err)
				}
				if token == "" || channelID == "" || (text == "" && len(blocks) == 0) {
					return nil, fmt.Errorf("slack.send: bot_token, channel_id, and text or blocks are required")
				}
				ts, err := postSlackMessage(ctx, token, channelID, text, blocks)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "ts": ts}, nil
			},
		},
	})
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
	signingSecret, _ := cfg["signing_secret"].(string)
	defaultBlocks, err := normalizeSlackBlocks(cfg["blocks"])
	if err != nil {
		return nil, fmt.Errorf("slack channel %q: config.blocks: %w", channelID, err)
	}

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
		signingSecret:  strings.TrimSpace(signingSecret),
		defaultBlocks:  defaultBlocks,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		restScheduler:  channels.NewRESTScheduler(1, 1),
	}

	registerWebhook(channelID, bot)
	log.Printf("slack: Events API webhook registered for channel %s (slack_channel=%s)", channelID, slackChannelID)
	return bot, nil
}

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*slackBot{}
)

func registerWebhook(channelID string, bot *slackBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches inbound Slack Events API and interactivity requests.
func HandleWebhook(channelID string, w http.ResponseWriter, r *http.Request) {
	webhookMu.RLock()
	bot, ok := webhookHandlers[channelID]
	webhookMu.RUnlock()
	if !ok {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	bot.handleWebhook(w, r)
}

// ─── Bot implementation ───────────────────────────────────────────────────────

const slackAPIBase = "https://slack.com/api"

type slackBot struct {
	mu             sync.Mutex
	channelID      string
	token          string
	slackChannelID string
	botUserID      string
	signingSecret  string
	defaultBlocks  []map[string]any
	lastThreadTS   string
	onMessage      func(sdk.InboundChannelMessage)
	seenEvents     map[string]struct{}
	done           chan struct{}
	httpClient     *http.Client
	restScheduler  *channels.RESTScheduler
}

func (b *slackBot) ID() string { return b.channelID }

func (b *slackBot) client(timeout time.Duration) *http.Client {
	if b.httpClient != nil {
		return b.httpClient
	}
	return &http.Client{Timeout: timeout}
}

func (b *slackBot) Send(ctx context.Context, text string) error {
	_, err := b.SendWithReceipt(ctx, text)
	return err
}

// SendTyping maps the shared typing capability to Slack Assistant thread
// status. Slack removed the legacy bot typing API, so status is only emitted
// when an inbound/outbound thread context is known.
func (b *slackBot) SendTyping(ctx context.Context, _ int) error {
	b.mu.Lock()
	threadTS := b.lastThreadTS
	b.mu.Unlock()
	if threadTS == "" {
		return nil
	}
	return b.SetThreadStatus(ctx, threadTS, "is typing...")
}

// ClearTyping clears the Assistant thread status at the end of a turn.
func (b *slackBot) ClearTyping(ctx context.Context) error {
	b.mu.Lock()
	threadTS := b.lastThreadTS
	b.mu.Unlock()
	if threadTS == "" {
		return nil
	}
	return b.SetThreadStatus(ctx, threadTS, "")
}

// SetThreadStatus updates Slack's Assistant thread status for explicit
// start/update/clear lifecycle calls.
func (b *slackBot) SetThreadStatus(ctx context.Context, threadID, status string) error {
	threadID = strings.TrimPrefix(strings.TrimSpace(threadID), "slack-")
	if threadID == "" {
		return fmt.Errorf("slack SetThreadStatus: threadID is required")
	}
	body, _ := json.Marshal(map[string]any{
		"channel_id": b.slackChannelID,
		"thread_ts":  threadID,
		"status":     status,
	})
	return b.slackPost(ctx, slackAPIBase+"/assistant.threads.setStatus", body)
}

func (b *slackBot) SendWithReceipt(ctx context.Context, text string) (channels.DeliveryReceipt, error) {
	ts, err := postSlackMessageWithClient(ctx, b.client(15*time.Second), b.restScheduler, b.token, b.slackChannelID, text, b.defaultBlocks)
	receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "slack", Attempts: 1, CreatedAt: time.Now()}
	if ts != "" {
		receipt.MessageID = "slack-" + ts
	}
	if err != nil {
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	receipt.Status = channels.DeliveryDelivered
	receipt.DeliveredAt = time.Now()
	return receipt, nil
}

// SendMedia delivers the shared direct text/media outbound contract as one
// Slack chat.postMessage call: image URLs become image blocks and other media
// are linked in the message text. Media are validated against the central
// gateway limits.
func (b *slackBot) SendMedia(ctx context.Context, payload sdk.DirectTextMediaPayload) error {
	if err := channelmedia.Validate(payload.Media, channels.MediaLimits{}); err != nil {
		return fmt.Errorf("slack %s: %w", b.channelID, err)
	}
	if len(payload.Media) == 0 {
		return b.Send(ctx, payload.Text)
	}
	channelID := strings.TrimSpace(payload.To)
	if channelID == "" {
		channelID = b.slackChannelID
	}
	text := payload.Text
	var blocks []map[string]any
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": text},
		})
	}
	for _, item := range payload.Media {
		if channelmedia.Kind(item) == channelmedia.KindImage && channelmedia.IsHTTPURL(item.Path) {
			altText := strings.TrimSpace(item.ContentType)
			if altText == "" {
				altText = "image"
			}
			blocks = append(blocks, map[string]any{
				"type":      "image",
				"image_url": item.Path,
				"alt_text":  altText,
			})
			continue
		}
		if strings.TrimSpace(text) != "" {
			text += "\n"
		}
		text += item.Path
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": item.Path},
		})
	}
	_, err := postSlackMessageWithClient(ctx, b.client(15*time.Second), b.restScheduler, b.token, channelID, text, blocks)
	if err != nil {
		return fmt.Errorf("slack %s: media send: %w", b.channelID, err)
	}
	return nil
}

func (b *slackBot) DeleteMessage(ctx context.Context, eventID string) error {
	ts := strings.TrimPrefix(eventID, "slack-")
	body, _ := json.Marshal(map[string]any{"channel": b.slackChannelID, "ts": ts})
	return b.slackPost(ctx, slackAPIBase+"/chat.delete", body)
}

func (b *slackBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
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
	_, err := b.SendInThreadWithReceipt(ctx, threadID, text)
	return err
}

func (b *slackBot) SendInThreadWithReceipt(ctx context.Context, threadID, text string) (channels.DeliveryReceipt, error) {
	receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "slack", Attempts: 1, CreatedAt: time.Now()}
	threadID = strings.TrimPrefix(strings.TrimSpace(threadID), "slack-")
	if threadID == "" {
		err := fmt.Errorf("slack SendInThread: threadID is required")
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	ts, err := postSlackMessagePayloadWithClient(ctx, b.client(15*time.Second), b.restScheduler, b.token, map[string]any{
		"channel":   b.slackChannelID,
		"text":      text,
		"thread_ts": threadID,
	})
	if ts != "" {
		receipt.MessageID = "slack-" + ts
	}
	if err != nil {
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	receipt.Status = channels.DeliveryDelivered
	receipt.DeliveredAt = time.Now()
	return receipt, nil
}

// slackPost is a convenience helper for authenticated Slack API POSTs.
func (b *slackBot) slackPost(ctx context.Context, apiURL string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := slackDo(ctx, b.client(15*time.Second), b.restScheduler, req)
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

func (b *slackBot) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if !b.verifySlackSignature(r, body) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		values, err := url.ParseQuery(string(body))
		if err != nil {
			http.Error(w, "parse form", http.StatusBadRequest)
			return
		}
		if command := strings.TrimSpace(values.Get("command")); command != "" {
			b.handleSlashCommand(values)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"response_type":"ephemeral","text":"Command received."}`))
			return
		}
		b.handleInteractionPayload(values.Get("payload"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}

	var envelope slackEventEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}
	if envelope.Type == "url_verification" {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(envelope.Challenge))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))

	if envelope.EventID != "" && !b.markSeen(envelope.EventID) {
		return
	}
	b.processSlackEvent(envelope.Event)
}

func (b *slackBot) verifySlackSignature(r *http.Request, body []byte) bool {
	if b.signingSecret == "" {
		return true
	}
	timestamp := strings.TrimSpace(r.Header.Get("X-Slack-Request-Timestamp"))
	provided := strings.TrimSpace(r.Header.Get("X-Slack-Signature"))
	if timestamp == "" || !strings.HasPrefix(provided, "v0=") {
		return false
	}
	base := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(b.signingSecret))
	_, _ = mac.Write([]byte(base))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func (b *slackBot) markSeen(eventID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seenEvents == nil {
		b.seenEvents = map[string]struct{}{}
	}
	if _, ok := b.seenEvents[eventID]; ok {
		return false
	}
	b.seenEvents[eventID] = struct{}{}
	return true
}

func (b *slackBot) handleSlashCommand(values url.Values) {
	command := strings.TrimSpace(values.Get("command"))
	if command == "" {
		return
	}
	text := strings.TrimSpace(values.Get("text"))
	if text != "" {
		text = command + " " + text
	} else {
		text = command
	}
	channelID := strings.TrimSpace(values.Get("channel_id"))
	if channelID != "" && channelID != b.slackChannelID {
		return
	}
	eventID := "slack-command-" + strings.TrimSpace(values.Get("trigger_id"))
	if eventID == "slack-command-" {
		eventID = "slack-command-" + fmt.Sprint(time.Now().UnixNano())
	}
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  strings.TrimSpace(values.Get("user_id")),
		Text:      text,
		EventID:   eventID,
	})
}

func (b *slackBot) handleInteractionPayload(raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	var payload slackInteractionPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return
	}
	for _, action := range payload.Actions {
		text := strings.TrimSpace(action.Value)
		if text == "" {
			text = strings.TrimSpace(action.Text.Text)
		}
		if text == "" {
			text = strings.TrimSpace(action.ActionID)
		}
		if text == "" {
			continue
		}
		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  payload.User.ID,
			Text:      text,
			EventID:   "slack-action-" + payload.Container.MessageTS + "-" + action.ActionID,
			ThreadID:  strings.TrimSpace(payload.Message.ThreadTS),
		})
	}
}

func (b *slackBot) processSlackEvent(event slackEvent) {
	if event.ThreadTS == "" && event.ParentUserID != "" {
		if recovered := b.recoverSlackThreadTS(context.Background(), event); recovered != "" {
			event.ThreadTS = recovered
		}
	}
	if event.Channel != "" && event.Channel != b.slackChannelID {
		return
	}
	if event.BotID != "" || event.Subtype == "bot_message" {
		return
	}
	if b.botUserID != "" && event.User == b.botUserID {
		return
	}
	text := strings.TrimSpace(event.Text)
	mediaURL, mediaMIME := firstSlackFile(event.Files)
	if event.Type == "file_shared" && text == "" {
		text = "shared a file"
	}
	if event.Type != "message" && event.Type != "app_mention" && event.Type != "file_shared" {
		return
	}
	if text == "" && mediaURL == "" {
		return
	}
	rawThreadID := strings.TrimSpace(event.ThreadTS)
	if rawThreadID != "" {
		b.mu.Lock()
		b.lastThreadTS = rawThreadID
		b.mu.Unlock()
	}
	threadID := ""
	replyToEventID := ""
	if rawThreadID != "" && rawThreadID != event.Ts {
		threadID = rawThreadID
		replyToEventID = "slack-" + rawThreadID
	}
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID:      b.channelID,
		SenderID:       event.User,
		Text:           text,
		EventID:        "slack-" + event.Ts,
		CreatedAt:      slackTimestampUnix(event.Ts),
		ThreadID:       threadID,
		ReplyToEventID: replyToEventID,
		MediaURL:       mediaURL,
		MediaMIME:      mediaMIME,
	})
}

type slackEventEnvelope struct {
	Type      string     `json:"type"`
	Challenge string     `json:"challenge,omitempty"`
	EventID   string     `json:"event_id,omitempty"`
	Event     slackEvent `json:"event"`
}

type slackEvent struct {
	Type         string      `json:"type"`
	Subtype      string      `json:"subtype,omitempty"`
	User         string      `json:"user"`
	BotID        string      `json:"bot_id,omitempty"`
	Text         string      `json:"text"`
	Ts           string      `json:"ts"`
	ThreadTS     string      `json:"thread_ts,omitempty"`
	ParentUserID string      `json:"parent_user_id,omitempty"`
	Channel      string      `json:"channel"`
	Files        []slackFile `json:"files,omitempty"`
}

func (b *slackBot) recoverSlackThreadTS(ctx context.Context, event slackEvent) string {
	if strings.TrimSpace(event.Channel) == "" || strings.TrimSpace(event.Ts) == "" {
		return ""
	}
	query := url.Values{}
	query.Set("channel", event.Channel)
	query.Set("latest", event.Ts)
	query.Set("inclusive", "true")
	query.Set("limit", "1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, slackAPIBase+"/conversations.history?"+query.Encode(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	resp, err := slackDo(ctx, b.client(15*time.Second), b.restScheduler, req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result struct {
		OK       bool         `json:"ok"`
		Messages []slackEvent `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || !result.OK {
		return ""
	}
	for _, message := range result.Messages {
		if message.Ts == event.Ts && strings.TrimSpace(message.ThreadTS) != "" {
			return strings.TrimSpace(message.ThreadTS)
		}
	}
	return ""
}

type slackFile struct {
	URLPrivate string `json:"url_private,omitempty"`
	Permalink  string `json:"permalink,omitempty"`
	Mimetype   string `json:"mimetype,omitempty"`
}

type slackInteractionPayload struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Container struct {
		MessageTS string `json:"message_ts"`
	} `json:"container"`
	Message struct {
		ThreadTS string `json:"thread_ts,omitempty"`
	} `json:"message"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
		Text     struct {
			Text string `json:"text"`
		} `json:"text"`
	} `json:"actions"`
}

func normalizeSlackBlocks(raw any) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var blocks []map[string]any
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, fmt.Errorf("blocks must be a Slack Block Kit array: %w", err)
	}
	return blocks, nil
}

func fallbackSlackText(text string, blocks []map[string]any) string {
	if strings.TrimSpace(text) != "" {
		return text
	}
	for _, block := range blocks {
		if txt, ok := block["text"].(map[string]any); ok {
			if s, ok := txt["text"].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return "Block Kit message"
}

func firstSlackFile(files []slackFile) (string, string) {
	for _, f := range files {
		if f.URLPrivate != "" {
			return f.URLPrivate, f.Mimetype
		}
		if f.Permalink != "" {
			return f.Permalink, f.Mimetype
		}
	}
	return "", ""
}

func slackTimestampUnix(ts string) int64 {
	parts := strings.SplitN(ts, ".", 2)
	sec, _ := strconv.ParseInt(parts[0], 10, 64)
	return sec
}

// postSlackMessage sends text and optional Block Kit blocks to a Slack channel via chat.postMessage.
// Returns the message timestamp (ts) on success.
func postSlackMessage(ctx context.Context, token, channelID, text string, blocks []map[string]any) (string, error) {
	return postSlackMessageWithClient(ctx, &http.Client{Timeout: 15 * time.Second}, nil, token, channelID, text, blocks)
}

func postSlackMessageWithClient(ctx context.Context, client *http.Client, scheduler *channels.RESTScheduler, token, channelID, text string, blocks []map[string]any) (string, error) {
	payload := map[string]any{
		"channel": channelID,
		"text":    fallbackSlackText(text, blocks),
	}
	if len(blocks) > 0 {
		payload["blocks"] = blocks
	}
	return postSlackMessagePayloadWithClient(ctx, client, scheduler, token, payload)
}

func postSlackMessagePayloadWithClient(ctx context.Context, client *http.Client, scheduler *channels.RESTScheduler, token string, payload map[string]any) (string, error) {
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		slackAPIBase+"/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := slackDo(ctx, client, scheduler, req)
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

func slackDo(ctx context.Context, client *http.Client, scheduler *channels.RESTScheduler, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if scheduler == nil {
		return client.Do(req)
	}
	return scheduler.Do(ctx, client, req.Method+" "+req.URL.Path, req)
}
