// Package whatsappweb implements unofficial personal-account WhatsApp messaging
// through a local Baileys bridge. It is intentionally separate from the official
// Meta Cloud API implementation in internal/extensions/whatsapp.
package whatsappweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("whatsappweb", func() sdk.ChannelPlugin { return &Plugin{} })
}

// Plugin connects metiq to a versioned local Baileys bridge.
type Plugin struct{}

func (p *Plugin) ID() string   { return "whatsappweb" }
func (p *Plugin) Type() string { return "WhatsApp Linked Device (Unofficial)" }

func (p *Plugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bridge_url":          map[string]any{"type": "string", "description": "Base URL of the local Baileys bridge v1."},
			"bridge_token":        map[string]any{"type": "string", "description": "Bearer token for the bridge."},
			"allow_remote_bridge": map[string]any{"type": "boolean", "default": false},
			"auth_dir":            map[string]any{"type": "string", "description": "Optional bridge-host path for this account's Baileys auth state."},
			"legacy_auth_dir":     map[string]any{"type": "string", "description": "Optional legacy auth directory migrated for the default account."},
			"default_to":          map[string]any{"type": "string", "description": "Optional default phone number or WhatsApp JID."},
			"reaction_level":      map[string]any{"type": "string", "enum": []string{"off", "ack", "minimal", "extensive"}, "default": "minimal"},
		},
		"required": []string{"bridge_url"},
	}
}

func (p *Plugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Typing: true, Reactions: true, Threads: true, MultiAccount: true}
}

func (p *Plugin) GatewayMethods() []sdk.GatewayMethod {
	return channels.AccountScopedGatewayMethods(p.ID(), []sdk.GatewayMethod{
		bridgeMethod("whatsappweb.send", "Send a linked-device WhatsApp message", func(params map[string]any) (string, any, error) {
			to, text := firstString(params, "to", "default_to"), firstString(params, "text")
			if to == "" || text == "" {
				return "", nil, fmt.Errorf("whatsappweb.send: to/default_to and text are required")
			}
			return "messages", map[string]any{"to": to, "text": text}, nil
		}),
		bridgeMethod("whatsappweb.typing", "Set a linked-device WhatsApp typing indicator", func(params map[string]any) (string, any, error) {
			to := firstString(params, "to", "default_to")
			if to == "" {
				return "", nil, fmt.Errorf("whatsappweb.typing: to/default_to is required")
			}
			typing, _ := params["typing"].(bool)
			return "typing", map[string]any{"to": to, "typing": typing}, nil
		}),
		bridgeMethod("whatsappweb.add_reaction", "Add a WhatsApp reaction", reactionPayload(false)),
		bridgeMethod("whatsappweb.remove_reaction", "Remove a WhatsApp reaction", reactionPayload(true)),
		bridgeMethod("whatsappweb.auth_status", "Get linked-device authentication status", func(map[string]any) (string, any, error) {
			return "auth/status", map[string]any{}, nil
		}),
		bridgeMethod("whatsappweb.auth_qr", "Request the next linked-device QR value", func(map[string]any) (string, any, error) {
			return "auth/qr", map[string]any{}, nil
		}),
		bridgeMethod("whatsappweb.auth_pair_code", "Request a WhatsApp phone-number pair code", func(params map[string]any) (string, any, error) {
			phone := normalizePhone(firstString(params, "phone_number"))
			if len(phone) < 8 || len(phone) > 15 {
				return "", nil, fmt.Errorf("whatsappweb.auth_pair_code: phone_number must contain 8-15 digits")
			}
			return "auth/pair-code", map[string]any{"phone_number": phone}, nil
		}),
		bridgeMethod("whatsappweb.logout", "Log out and clear linked-device credentials", func(map[string]any) (string, any, error) {
			return "auth/logout", map[string]any{}, nil
		}),
	})
}

type bridgeAccount struct {
	BaseURL       *url.URL
	Token         string
	AccountID     string
	AuthDir       string
	LegacyAuthDir string
	DefaultTo     string
	ReactionLevel string
}

func bridgeAccountFromParams(params map[string]any, fallbackID string) (bridgeAccount, error) {
	var account bridgeAccount
	rawURL := firstString(params, "bridge_url")
	accountID := firstString(params, "account_id")
	if accountID == "" {
		accountID = strings.TrimSpace(fallbackID)
	}
	if rawURL == "" || accountID == "" {
		return account, fmt.Errorf("bridge_url and account_id are required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return account, fmt.Errorf("bridge_url must be an absolute HTTP(S) base URL without userinfo, query, or fragment")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	allowRemote, _ := params["allow_remote_bridge"].(bool)
	token := firstString(params, "bridge_token")
	if !loopback {
		if !allowRemote {
			return account, fmt.Errorf("remote bridge requires allow_remote_bridge=true")
		}
		if u.Scheme != "https" || token == "" {
			return account, fmt.Errorf("remote bridge requires HTTPS and bridge_token")
		}
	}
	level := strings.ToLower(firstString(params, "reaction_level"))
	if level == "" {
		level = "minimal"
	}
	if !validReactionLevel(level) {
		return account, fmt.Errorf("reaction_level must be off, ack, minimal, or extensive")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return bridgeAccount{
		BaseURL: u, Token: token, AccountID: accountID,
		AuthDir: firstString(params, "auth_dir"), LegacyAuthDir: firstString(params, "legacy_auth_dir"),
		DefaultTo: firstString(params, "default_to"), ReactionLevel: level,
	}, nil
}

func (a bridgeAccount) configPayload() map[string]any {
	return map[string]any{"auth_dir": a.AuthDir, "legacy_auth_dir": a.LegacyAuthDir, "reaction_level": a.ReactionLevel}
}

func bridgeMethod(name, description string, build func(map[string]any) (string, any, error)) sdk.GatewayMethod {
	return sdk.GatewayMethod{Method: name, Description: description, Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
		account, err := bridgeAccountFromParams(params, "")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		endpoint, input, err := build(params)
		if err != nil {
			return nil, err
		}
		if strings.Contains(endpoint, "reaction") {
			emoji := firstString(params, "emoji")
			if err := validateReaction(account.ReactionLevel, emoji); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
		result, err := bridgePost(ctx, nil, account, endpoint, input)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return map[string]any{"ok": true, "account_id": account.AccountID, "result": result}, nil
	}}
}

func reactionPayload(remove bool) func(map[string]any) (string, any, error) {
	return func(params map[string]any) (string, any, error) {
		ref, emoji := firstString(params, "message_ref"), firstString(params, "emoji")
		if ref == "" || emoji == "" {
			return "", nil, fmt.Errorf("message_ref and emoji are required")
		}
		return "reactions", map[string]any{"message_ref": ref, "emoji": emoji, "remove": remove}, nil
	}
}

func (p *Plugin) Connect(ctx context.Context, channelID string, cfg map[string]any, onMessage func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	account, err := bridgeAccountFromParams(cfg, channelID)
	if err != nil {
		return nil, fmt.Errorf("whatsappweb channel %q: %w", channelID, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	bot := &bot{
		channelID: channelID, account: account, onMessage: onMessage, cancel: cancel,
		httpClient: &http.Client{Timeout: 20 * time.Second}, seen: map[string]struct{}{},
		messageRefs: map[string]string{}, activeTyping: map[string]bool{}, done: make(chan struct{}),
	}
	if _, err := bridgePost(runCtx, bot.httpClient, account, "session/start", map[string]any{}); err != nil {
		cancel()
		return nil, fmt.Errorf("whatsappweb channel %q: start bridge session: %w", channelID, err)
	}
	conn, err := bot.dial(runCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("whatsappweb channel %q: connect event stream: %w", channelID, err)
	}
	bot.connMu.Lock()
	bot.conn = conn
	bot.connMu.Unlock()
	go bot.run(runCtx, conn)
	return bot, nil
}

type bridgeEvent struct {
	Type           string `json:"type"`
	EventID        string `json:"event_id"`
	MessageRef     string `json:"message_ref"`
	ChatJID        string `json:"chat_jid"`
	SenderJID      string `json:"sender_jid"`
	ThreadID       string `json:"thread_id"`
	ReplyToEventID string `json:"reply_to_event_id"`
	Text           string `json:"text"`
	Timestamp      int64  `json:"timestamp_s"`
	IsSelf         bool   `json:"is_self"`
}

type bot struct {
	mu           sync.Mutex
	connMu       sync.Mutex
	closeOnce    sync.Once
	channelID    string
	account      bridgeAccount
	onMessage    func(sdk.InboundChannelMessage)
	httpClient   *http.Client
	cancel       context.CancelFunc
	conn         *websocket.Conn
	done         chan struct{}
	seen         map[string]struct{}
	seenOrder    []string
	messageRefs  map[string]string
	activeTyping map[string]bool
}

func (b *bot) ID() string { return b.channelID }

func (b *bot) dial(ctx context.Context) (*websocket.Conn, error) {
	u := *b.account.BaseURL
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/accounts/" + url.PathEscape(b.account.AccountID) + "/events"
	header := make(http.Header)
	if b.account.Token != "" {
		header.Set("Authorization", "Bearer "+b.account.Token)
	}
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: b.httpClient, HTTPHeader: header})
	if err == nil {
		conn.SetReadLimit(1 << 20)
	}
	return conn, err
}

func (b *bot) run(ctx context.Context, conn *websocket.Conn) {
	defer close(b.done)
	backoff := time.Second
	for {
		err := b.readEvents(ctx, conn)
		_ = conn.Close(websocket.StatusNormalClosure, "reconnect")
		if ctx.Err() != nil {
			return
		}
		log.Printf("whatsappweb: event stream ended channel=%s: %v", b.channelID, err)
		for {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			_, startErr := bridgePost(ctx, b.httpClient, b.account, "session/start", map[string]any{})
			next, dialErr := b.dial(ctx)
			if startErr == nil && dialErr == nil {
				conn = next
				b.connMu.Lock()
				b.conn = next
				b.connMu.Unlock()
				backoff = time.Second
				break
			}
			if next != nil {
				_ = next.Close(websocket.StatusNormalClosure, "start failed")
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (b *bot) readEvents(ctx context.Context, conn *websocket.Conn) error {
	for {
		var event bridgeEvent
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			return err
		}
		b.handleEvent(event)
	}
}

func (b *bot) handleEvent(event bridgeEvent) {
	if event.Type != "message" || event.IsSelf || strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.MessageRef) == "" || strings.TrimSpace(event.SenderJID) == "" || strings.TrimSpace(event.Text) == "" {
		return
	}
	if event.ThreadID != "" && !strings.HasSuffix(strings.ToLower(event.ThreadID), "@g.us") {
		return
	}
	b.mu.Lock()
	if _, exists := b.seen[event.EventID]; exists {
		b.mu.Unlock()
		return
	}
	b.seen[event.EventID] = struct{}{}
	b.seenOrder = append(b.seenOrder, event.EventID)
	b.messageRefs[event.EventID] = event.MessageRef
	if len(b.seenOrder) > 1024 {
		old := b.seenOrder[0]
		b.seenOrder = b.seenOrder[1:]
		delete(b.seen, old)
		delete(b.messageRefs, old)
	}
	b.mu.Unlock()
	b.onMessage(sdk.InboundChannelMessage{ChannelID: b.channelID, SenderID: event.SenderJID, Text: event.Text, EventID: event.EventID, ThreadID: event.ThreadID, ReplyToEventID: event.ReplyToEventID, CreatedAt: event.Timestamp})
}

func (b *bot) target(ctx context.Context) (string, error) {
	to := strings.TrimSpace(sdk.ChannelReplyTarget(ctx))
	if to == "" {
		to = b.account.DefaultTo
	}
	if to == "" {
		return "", fmt.Errorf("whatsappweb %s: target is required", b.channelID)
	}
	return to, nil
}

func (b *bot) Send(ctx context.Context, text string) error {
	to, err := b.target(ctx)
	if err != nil {
		return err
	}
	defer b.clearTypingIfActive(ctx, to)
	_, err = bridgePost(ctx, b.httpClient, b.account, "messages", map[string]any{"to": to, "text": text})
	return err
}

func (b *bot) SendInThread(ctx context.Context, threadID, text string) error {
	threadID = strings.TrimSpace(threadID)
	if !strings.HasSuffix(strings.ToLower(threadID), "@g.us") || strings.TrimSpace(text) == "" {
		return fmt.Errorf("whatsappweb: group threadID ending in @g.us and text are required")
	}
	defer b.clearTypingIfActive(ctx, threadID)
	_, err := bridgePost(ctx, b.httpClient, b.account, "messages", map[string]any{"to": threadID, "text": text})
	return err
}

func (b *bot) SendTyping(ctx context.Context, _ int) error {
	to, err := b.target(ctx)
	if err != nil {
		return err
	}
	if _, err = bridgePost(ctx, b.httpClient, b.account, "typing", map[string]any{"to": to, "typing": true}); err != nil {
		return err
	}
	b.mu.Lock()
	b.activeTyping[to] = true
	b.mu.Unlock()
	return nil
}

func (b *bot) ClearTyping(ctx context.Context) error {
	to, err := b.target(ctx)
	if err != nil {
		return err
	}
	return b.clearTyping(ctx, to)
}

func (b *bot) clearTypingIfActive(ctx context.Context, to string) {
	b.mu.Lock()
	active := b.activeTyping[to]
	b.mu.Unlock()
	if active {
		_ = b.clearTyping(ctx, to)
	}
}

func (b *bot) clearTyping(ctx context.Context, to string) error {
	if _, err := bridgePost(ctx, b.httpClient, b.account, "typing", map[string]any{"to": to, "typing": false}); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.activeTyping, to)
	b.mu.Unlock()
	return nil
}

func (b *bot) AddReaction(ctx context.Context, eventID, emoji string) error {
	return b.react(ctx, eventID, emoji, false)
}

func (b *bot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return b.react(ctx, eventID, emoji, true)
}

func (b *bot) react(ctx context.Context, eventID, emoji string, remove bool) error {
	if err := validateReaction(b.account.ReactionLevel, emoji); err != nil {
		return err
	}
	b.mu.Lock()
	ref := b.messageRefs[eventID]
	b.mu.Unlock()
	if ref == "" {
		return fmt.Errorf("whatsappweb: message reference for %q expired from local cache", eventID)
	}
	_, err := bridgePost(ctx, b.httpClient, b.account, "reactions", map[string]any{"message_ref": ref, "emoji": emoji, "remove": remove})
	return err
}

func (b *bot) Close() {
	b.closeOnce.Do(func() {
		b.cancel()
		b.connMu.Lock()
		if b.conn != nil {
			_ = b.conn.Close(websocket.StatusNormalClosure, "shutdown")
		}
		b.connMu.Unlock()
		select {
		case <-b.done:
		case <-time.After(2 * time.Second):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = bridgePost(ctx, b.httpClient, b.account, "session/stop", map[string]any{})
	})
}

func bridgePost(ctx context.Context, client *http.Client, account bridgeAccount, endpoint string, input any) (map[string]any, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	payload := map[string]any{"config": account.configPayload(), "input": input}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	u := *account.BaseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/accounts/" + url.PathEscape(account.AccountID) + "/" + strings.TrimLeft(endpoint, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if account.Token != "" {
		req.Header.Set("Authorization", "Bearer "+account.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Baileys bridge: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if len(body) > 64<<10 {
		return nil, fmt.Errorf("Baileys bridge response exceeds 65536 bytes")
	}
	var envelope struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("Baileys bridge response: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.OK {
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("Baileys bridge HTTP %d %s: %s", resp.StatusCode, envelope.Error.Code, message)
	}
	if envelope.Result == nil {
		envelope.Result = map[string]any{}
	}
	return envelope.Result, nil
}

func validReactionLevel(level string) bool {
	return level == "off" || level == "ack" || level == "minimal" || level == "extensive"
}

func validateReaction(level, emoji string) error {
	if level == "off" || level == "ack" {
		return fmt.Errorf("agent reactions disabled at reaction_level=%q; use minimal or extensive", level)
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		return fmt.Errorf("emoji is required")
	}
	if level == "minimal" && !strings.Contains("👍 👎 ❤️ 😂 😮 😢 🙏 🎉 ✅ 👀", emoji) {
		return fmt.Errorf("emoji %q is not allowed at reaction_level=minimal", emoji)
	}
	if len([]byte(emoji)) > 32 {
		return fmt.Errorf("emoji exceeds 32 bytes")
	}
	return nil
}

func normalizePhone(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func firstString(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := params[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
