// Package telegram implements a Telegram Bot channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/telegram" in the daemon
// main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "token": "123456:ABC-DEF...",           // required: Telegram bot token
//	  "webhook_url": "https://yourhost/...",  // optional: use webhook instead of polling
//	  "allowed_users": [123456789]            // optional: restrict by Telegram user ID
//	}
//
// Inbound webhook endpoint: <admin_addr>/webhooks/telegram/<channel_id>
// When config.webhook_url is set, point it at that admin endpoint.
//
// To add a Telegram channel to your metiq config:
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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("telegram", func() sdk.ChannelPlugin { return &TelegramPlugin{} })
}

var newTelegramHTTPClient = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
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
				"description": "Optional: HTTPS webhook URL to register with Telegram (typically <admin_addr>/webhooks/telegram/<channel_id>). If absent, long-polling is used.",
			},
			"allowed_users": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Optional: list of Telegram user IDs allowed to send messages.",
			},
			"default_parse_mode": map[string]any{
				"type":        "string",
				"description": "Optional parse mode for outbound messages (MarkdownV2, HTML, or Markdown).",
			},
		},
		"required": []string{"token"},
	}
}

// Capabilities declares the features supported by the Telegram Bot channel.
func (t *TelegramPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       true,
		Reactions:    true,
		Threads:      true,
		Audio:        true,
		Edit:         true,
		MultiAccount: true,
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
				parseMode, _ := params["parse_mode"].(string)
				buttons, _ := params["buttons"].([]any)
				if token == "" || chatID == "" || text == "" {
					return nil, fmt.Errorf("telegram.send: token, chat_id, and text are required")
				}
				if err := sendTelegramMessageWithOptions(ctx, token, chatID, text, parseMode, buttons, nil); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			},
		},
		{
			Method:      "telegram.sendMedia",
			Description: "Send photo, video, audio, voice, document, or animation media to a Telegram chat",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["token"].(string)
				chatID, _ := params["chat_id"].(string)
				mediaType, _ := params["type"].(string)
				mediaURL, _ := params["media_url"].(string)
				caption, _ := params["caption"].(string)
				if token == "" || chatID == "" || mediaType == "" || mediaURL == "" {
					return nil, fmt.Errorf("telegram.sendMedia: token, chat_id, type, and media_url are required")
				}
				if err := sendTelegramMedia(ctx, nil, token, chatID, mediaType, mediaURL, caption); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			},
		},
		{
			Method:      "telegram.sendPoll",
			Description: "Send a native Telegram poll",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				token, _ := params["token"].(string)
				chatID, _ := params["chat_id"].(string)
				question, _ := params["question"].(string)
				options, _ := params["options"].([]any)
				if token == "" || chatID == "" || question == "" || len(options) == 0 {
					return nil, fmt.Errorf("telegram.sendPoll: token, chat_id, question, and options are required")
				}
				if err := sendTelegramPoll(ctx, nil, token, chatID, question, options); err != nil {
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
	webhookURL, _ := cfg["webhook_url"].(string)
	webhookURL = strings.TrimSpace(webhookURL)
	defaultParseMode, _ := cfg["default_parse_mode"].(string)
	defaultParseMode = strings.TrimSpace(defaultParseMode)

	var allowedUsers []int64
	if users, ok := cfg["allowed_users"].([]any); ok {
		for _, u := range users {
			if id, ok := u.(float64); ok {
				allowedUsers = append(allowedUsers, int64(id))
			}
		}
	}

	bot := &telegramBot{
		channelID:        channelID,
		token:            token,
		allowedUsers:     allowedUsers,
		onMessage:        onMessage,
		done:             make(chan struct{}),
		webhookURL:       webhookURL,
		webhookSecret:    deriveTelegramWebhookSecret(token, channelID),
		defaultParseMode: defaultParseMode,
	}

	if webhookURL != "" {
		registerWebhook(channelID, bot)
		if err := bot.configureWebhook(ctx); err != nil {
			webhookMu.Lock()
			delete(webhookHandlers, channelID)
			webhookMu.Unlock()
			return nil, fmt.Errorf("telegram channel %q: configure webhook: %w", channelID, err)
		}
		log.Printf("telegram: webhook registered for channel %s", channelID)
		return bot, nil
	}

	if err := bot.deleteWebhook(ctx); err != nil {
		log.Printf("telegram: failed clearing webhook before polling channel=%s: %v", channelID, err)
	}
	go bot.poll(ctx)
	log.Printf("telegram: polling started for channel %s", channelID)
	return bot, nil
}

type telegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query,omitempty"`
}

type telegramMessage struct {
	MessageID       int64  `json:"message_id"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
	Text            string `json:"text"`
	Caption         string `json:"caption,omitempty"`
	Date            int64  `json:"date"`
	ReplyToMessage  *struct {
		MessageID int64 `json:"message_id"`
	} `json:"reply_to_message,omitempty"`
	From *struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
	Chat *struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Photo     []telegramPhotoSize `json:"photo,omitempty"`
	Document  *telegramFile       `json:"document,omitempty"`
	Audio     *telegramFile       `json:"audio,omitempty"`
	Voice     *telegramFile       `json:"voice,omitempty"`
	Video     *telegramFile       `json:"video,omitempty"`
	Animation *telegramFile       `json:"animation,omitempty"`
}

type telegramPhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	Width    int64  `json:"width,omitempty"`
	Height   int64  `json:"height,omitempty"`
}

type telegramFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type telegramCallbackQuery struct {
	ID      string           `json:"id"`
	Data    string           `json:"data,omitempty"`
	From    *telegramUser    `json:"from,omitempty"`
	Message *telegramMessage `json:"message,omitempty"`
}

type telegramUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*telegramBot{}
)

func registerWebhook(channelID string, bot *telegramBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an inbound Telegram webhook update to the registered bot.
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

func deriveTelegramWebhookSecret(token, channelID string) string {
	sum := sha256.Sum256([]byte(token + "\x00" + channelID))
	return hex.EncodeToString(sum[:16])
}

// ─── Bot implementation ───────────────────────────────────────────────────────

type telegramBot struct {
	mu               sync.Mutex
	channelID        string
	token            string
	allowedUsers     []int64
	onMessage        func(sdk.InboundChannelMessage)
	lastUpdateID     int64
	lastChatID       string
	done             chan struct{}
	httpClient       *http.Client
	webhookURL       string
	webhookSecret    string
	defaultParseMode string
}

func (b *telegramBot) ID() string { return b.channelID }

func (b *telegramBot) client(timeout time.Duration) *http.Client {
	if b.httpClient != nil {
		return b.httpClient
	}
	return newTelegramHTTPClient(timeout)
}

func (b *telegramBot) Send(ctx context.Context, text string) error {
	// When used as a reply-to-all bot, we need to track the last chat ID.
	// For direct sends, the caller should use telegram.send gateway method.
	b.mu.Lock()
	chatID := b.lastChatID
	parseMode := b.defaultParseMode
	b.mu.Unlock()
	if chatID == "" {
		return fmt.Errorf("telegram %s: no chat ID known yet (no messages received)", b.channelID)
	}
	return sendTelegramMessageWithOptions(ctx, b.token, chatID, text, parseMode, nil, b.client(15*time.Second))
}

func (b *telegramBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	if strings.TrimSpace(b.webhookURL) != "" {
		webhookMu.Lock()
		delete(webhookHandlers, b.channelID)
		webhookMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = b.deleteWebhook(ctx)
	}
}

func (b *telegramBot) configureWebhook(ctx context.Context) error {
	if strings.TrimSpace(b.webhookURL) == "" {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"url":          b.webhookURL,
		"secret_token": b.webhookSecret,
	})
	url := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", b.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client(15 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("telegram setWebhook: HTTP %d: %s", resp.StatusCode, raw)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return fmt.Errorf("telegram setWebhook: decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram setWebhook: %s", strings.TrimSpace(result.Description))
	}
	return nil
}

func (b *telegramBot) deleteWebhook(ctx context.Context) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/deleteWebhook", b.token)
	payload, _ := json.Marshal(map[string]any{"drop_pending_updates": false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client(15 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("telegram deleteWebhook: HTTP %d: %s", resp.StatusCode, raw)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return fmt.Errorf("telegram deleteWebhook: decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram deleteWebhook: %s", strings.TrimSpace(result.Description))
	}
	return nil
}

func (b *telegramBot) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if b.webhookSecret != "" {
		provided := strings.TrimSpace(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
		if subtle.ConstantTimeCompare([]byte(provided), []byte(b.webhookSecret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var update telegramUpdate
	if err := json.Unmarshal(body, &update); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}
	b.processUpdate(update)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (b *telegramBot) processUpdate(update telegramUpdate) {
	if update.UpdateID > 0 {
		b.mu.Lock()
		if update.UpdateID > b.lastUpdateID {
			b.lastUpdateID = update.UpdateID
		}
		b.mu.Unlock()
	}

	msg := update.Message
	if msg == nil && update.CallbackQuery != nil {
		msg = update.CallbackQuery.Message
	}
	if msg == nil {
		return
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if update.CallbackQuery != nil && strings.TrimSpace(update.CallbackQuery.Data) != "" {
		text = "/callback " + strings.TrimSpace(update.CallbackQuery.Data)
	}
	mediaURL, mediaMIME := telegramMessageMedia(msg)
	if text == "" && mediaURL == "" {
		return
	}

	fromID := int64(0)
	if msg.From != nil {
		fromID = msg.From.ID
	}
	if update.CallbackQuery != nil && update.CallbackQuery.From != nil {
		fromID = update.CallbackQuery.From.ID
	}
	if len(b.allowedUsers) > 0 && fromID != 0 {
		allowed := false
		for _, uid := range b.allowedUsers {
			if fromID == uid {
				allowed = true
				break
			}
		}
		if !allowed {
			return
		}
	}

	senderID := ""
	chatIDStr := ""
	if fromID != 0 {
		senderID = fmt.Sprintf("%d", fromID)
	}
	if msg.Chat != nil {
		chatIDStr = fmt.Sprintf("%d", msg.Chat.ID)
		b.mu.Lock()
		b.lastChatID = chatIDStr
		b.mu.Unlock()
	}

	threadID := ""
	replyToEventID := ""
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.MessageID > 0 {
		replyToEventID = fmt.Sprintf("tg-%d", msg.ReplyToMessage.MessageID)
	}
	if msg.MessageThreadID > 0 {
		threadID = fmt.Sprintf("%d", msg.MessageThreadID)
	}

	eventID := fmt.Sprintf("tg-%d", msg.MessageID)
	if update.CallbackQuery != nil && update.CallbackQuery.ID != "" {
		eventID = "tg-callback-" + update.CallbackQuery.ID
		replyToEventID = fmt.Sprintf("tg-%d", msg.MessageID)
	}

	b.onMessage(sdk.InboundChannelMessage{
		ChannelID:      b.channelID,
		SenderID:       senderID,
		Text:           text,
		EventID:        eventID,
		CreatedAt:      msg.Date,
		ThreadID:       threadID,
		ReplyToEventID: replyToEventID,
		MediaURL:       mediaURL,
		MediaMIME:      mediaMIME,
	})
}

func telegramMessageMedia(msg *telegramMessage) (string, string) {
	if msg == nil {
		return "", ""
	}
	if len(msg.Photo) > 0 {
		best := msg.Photo[0]
		for _, p := range msg.Photo[1:] {
			if p.FileSize > best.FileSize || (p.FileSize == 0 && p.Width*p.Height > best.Width*best.Height) {
				best = p
			}
		}
		if best.FileID != "" {
			return "telegram:file/" + best.FileID, "image/jpeg"
		}
	}
	for _, candidate := range []struct {
		file *telegramFile
		mime string
	}{
		{msg.Document, "application/octet-stream"},
		{msg.Audio, "audio/mpeg"},
		{msg.Voice, "audio/ogg"},
		{msg.Video, "video/mp4"},
		{msg.Animation, "image/gif"},
	} {
		if candidate.file != nil && candidate.file.FileID != "" {
			mime := strings.TrimSpace(candidate.file.MimeType)
			if mime == "" {
				mime = candidate.mime
			}
			return "telegram:file/" + candidate.file.FileID, mime
		}
	}
	return "", ""
}

// ─── ReactionHandle ──────────────────────────────────────────────────────────

// AddReaction adds an emoji reaction to a Telegram message.
// eventID must be of the form "tg-{message_id}".
func (b *telegramBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	return b.setReaction(ctx, eventID, emoji)
}

// RemoveReaction removes the bot's reaction from a Telegram message.
func (b *telegramBot) RemoveReaction(ctx context.Context, eventID, _ string) error {
	return b.setReaction(ctx, eventID, "")
}

func (b *telegramBot) setReaction(ctx context.Context, eventID, emoji string) error {
	b.mu.Lock()
	chatID := b.lastChatID
	b.mu.Unlock()
	if chatID == "" {
		return fmt.Errorf("telegram %s: no chat ID known", b.channelID)
	}
	msgID, err := telegramEventMessageID(eventID)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": msgID,
	}
	if strings.TrimSpace(emoji) != "" {
		payload["reaction"] = []map[string]string{{"type": "emoji", "emoji": emoji}}
	} else {
		payload["reaction"] = []map[string]string{}
	}
	return telegramPostJSON(ctx, b.client(10*time.Second), b.token, "setMessageReaction", payload)
}

// ─── AudioHandle ─────────────────────────────────────────────────────────────

// SendAudio delivers raw audio bytes as a Telegram audio file.
func (b *telegramBot) SendAudio(ctx context.Context, audio []byte, format string) error {
	b.mu.Lock()
	chatID := b.lastChatID
	b.mu.Unlock()
	if chatID == "" {
		return fmt.Errorf("telegram %s: no chat ID known", b.channelID)
	}
	return sendTelegramAudioBytes(ctx, b.client(30*time.Second), b.token, chatID, audio, format)
}

func telegramEventMessageID(eventID string) (int64, error) {
	msgIDStr := strings.TrimPrefix(eventID, "tg-")
	msgID, err := strconv.ParseInt(msgIDStr, 10, 64)
	if err != nil || msgID == 0 {
		return 0, fmt.Errorf("telegram: invalid eventID %q", eventID)
	}
	return msgID, nil
}

// ─── TypingHandle ─────────────────────────────────────────────────────────────

// SendTyping sends a "typing" chat action to the current chat.
func (b *telegramBot) SendTyping(ctx context.Context, _ int) error {
	b.mu.Lock()
	chatID := b.lastChatID
	b.mu.Unlock()
	if chatID == "" {
		return nil // no chat known yet
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendChatAction", b.token)
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "action": "typing"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendChatAction: %w", err)
	}
	resp.Body.Close()
	return nil
}

// ─── EditHandle ──────────────────────────────────────────────────────────────

// EditMessage replaces the text of a previously sent message.
// eventID must be of the form "tg-{message_id}".
func (b *telegramBot) EditMessage(ctx context.Context, eventID, newText string) error {
	b.mu.Lock()
	chatID := b.lastChatID
	b.mu.Unlock()
	if chatID == "" {
		return fmt.Errorf("telegram %s: no chat ID known", b.channelID)
	}
	msgIDStr := strings.TrimPrefix(eventID, "tg-")
	msgID, err := strconv.ParseInt(msgIDStr, 10, 64)
	if err != nil || msgID == 0 {
		return fmt.Errorf("telegram EditMessage: invalid eventID %q", eventID)
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", b.token)
	body, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"message_id": msgID,
		"text":       newText,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client(15 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("telegram editMessageText: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram editMessageText: HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
}

// ─── ThreadHandle ────────────────────────────────────────────────────────────

// SendInThread sends a reply to a specific message using reply_to_message_id.
// threadID should be the numeric Telegram message ID (string form) to reply to.
func (b *telegramBot) SendInThread(ctx context.Context, threadID, text string) error {
	b.mu.Lock()
	chatID := b.lastChatID
	b.mu.Unlock()
	if chatID == "" {
		return fmt.Errorf("telegram %s: no chat ID known", b.channelID)
	}
	replyTo, _ := strconv.ParseInt(threadID, 10, 64)
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if replyTo > 0 {
		payload["reply_to_message_id"] = replyTo
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client(15 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendInThread: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendInThread: HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
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

	resp, err := b.client(10 * time.Second).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var result struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}

	if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
		return
	}

	for _, update := range result.Result {
		b.processUpdate(update)
	}
}

// sendTelegramMessage sends a text message to a Telegram chat.
func sendTelegramMessage(ctx context.Context, token, chatID, text string) error {
	return sendTelegramMessageWithOptions(ctx, token, chatID, text, "", nil, nil)
}

func sendTelegramMessageWithOptions(ctx context.Context, token, chatID, text, parseMode string, buttons []any, httpClient *http.Client) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if strings.TrimSpace(parseMode) != "" {
		payload["parse_mode"] = strings.TrimSpace(parseMode)
	}
	if len(buttons) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": normalizeTelegramButtons(buttons)}
	}
	return telegramPostJSON(ctx, httpClient, token, "sendMessage", payload)
}

func sendTelegramMedia(ctx context.Context, httpClient *http.Client, token, chatID, mediaType, mediaURL, caption string) error {
	method, field, err := telegramMediaMethod(mediaType)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"chat_id": chatID,
		field:     mediaURL,
	}
	if strings.TrimSpace(caption) != "" {
		payload["caption"] = caption
	}
	return telegramPostJSON(ctx, httpClient, token, method, payload)
}

func sendTelegramPoll(ctx context.Context, httpClient *http.Client, token, chatID, question string, options []any) error {
	texts := make([]string, 0, len(options))
	for _, opt := range options {
		if s := strings.TrimSpace(fmt.Sprint(opt)); s != "" {
			texts = append(texts, s)
		}
	}
	if len(texts) == 0 {
		return fmt.Errorf("telegram sendPoll: at least one non-empty option is required")
	}
	return telegramPostJSON(ctx, httpClient, token, "sendPoll", map[string]any{
		"chat_id":  chatID,
		"question": question,
		"options":  texts,
	})
}

func sendTelegramAudioBytes(ctx context.Context, httpClient *http.Client, token, chatID string, audio []byte, format string) error {
	if len(audio) == 0 {
		return fmt.Errorf("telegram sendAudio: audio bytes are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	ext := strings.Trim(strings.TrimSpace(format), ".")
	if ext == "" {
		ext = "ogg"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", chatID)
	part, err := writer.CreateFormFile("audio", "audio."+ext)
	if err != nil {
		return err
	}
	if _, err := part.Write(audio); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendAudio", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return telegramDo(req, httpClient, "telegram sendAudio")
}

func telegramPostJSON(ctx context.Context, httpClient *http.Client, token, method string, payload map[string]any) error {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return telegramDo(req, httpClient, "telegram "+method)
}

func telegramDo(req *http.Request, httpClient *http.Client, label string) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d: %s", label, resp.StatusCode, raw)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &result); err == nil && !result.OK {
		return fmt.Errorf("%s: %s", label, strings.TrimSpace(result.Description))
	}
	return nil
}

func telegramMediaMethod(mediaType string) (method, field string, err error) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "photo", "image":
		return "sendPhoto", "photo", nil
	case "video":
		return "sendVideo", "video", nil
	case "audio":
		return "sendAudio", "audio", nil
	case "voice":
		return "sendVoice", "voice", nil
	case "document", "file":
		return "sendDocument", "document", nil
	case "animation", "gif":
		return "sendAnimation", "animation", nil
	default:
		return "", "", fmt.Errorf("telegram media: unsupported type %q", mediaType)
	}
}

func normalizeTelegramButtons(buttons []any) [][]map[string]string {
	var rows [][]map[string]string
	for _, rowAny := range buttons {
		var row []map[string]string
		if rowSlice, ok := rowAny.([]any); ok {
			for _, buttonAny := range rowSlice {
				if button := normalizeTelegramButton(buttonAny); len(button) > 0 {
					row = append(row, button)
				}
			}
		} else if button := normalizeTelegramButton(rowAny); len(button) > 0 {
			row = append(row, button)
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	return rows
}

func normalizeTelegramButton(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	text := strings.TrimSpace(fmt.Sprint(m["text"]))
	if text == "" {
		return nil
	}
	button := map[string]string{"text": text}
	if url, _ := m["url"].(string); strings.TrimSpace(url) != "" {
		button["url"] = strings.TrimSpace(url)
	} else if data, _ := m["callback_data"].(string); strings.TrimSpace(data) != "" {
		button["callback_data"] = strings.TrimSpace(data)
	}
	return button
}
