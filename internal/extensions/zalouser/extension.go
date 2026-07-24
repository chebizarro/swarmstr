// Package zalouser implements personal Zalo messaging through a local zca-js bridge.
// The bridge is event-driven: inbound messages arrive on one long-lived WebSocket;
// there is deliberately no receive polling fallback.
package zalouser

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
	sdk.RegisterChannelConstructor("zalouser", func() sdk.ChannelPlugin { return &ZaloUserPlugin{} })
}

// ZaloUserPlugin connects metiq to a versioned zca-js bridge.
type ZaloUserPlugin struct{}

func (p *ZaloUserPlugin) ID() string   { return "zalouser" }
func (p *ZaloUserPlugin) Type() string { return "Zalo Personal Account" }

func (p *ZaloUserPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bridge_url":          map[string]any{"type": "string", "description": "Base URL of the zca-js bridge v1."},
			"bridge_token":        map[string]any{"type": "string", "description": "Bearer token for the bridge."},
			"profile":             map[string]any{"type": "string", "description": "Authenticated zca-js profile name."},
			"default_to":          map[string]any{"type": "string", "description": "Optional default user or group ID."},
			"default_chat_type":   map[string]any{"type": "string", "enum": []string{"direct", "group"}, "default": "direct"},
			"allowed_senders":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"allow_remote_bridge": map[string]any{"type": "boolean", "default": false},
		},
		"required": []string{"bridge_url", "profile"},
	}
}

func (p *ZaloUserPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Typing: true, Reactions: true, Threads: true, MultiAccount: true}
}

func (p *ZaloUserPlugin) GatewayMethods() []sdk.GatewayMethod {
	return channels.AccountScopedGatewayMethods(p.ID(), []sdk.GatewayMethod{
		zalouserMethod("zalouser.send", "Send a personal Zalo message", func(params map[string]any) (string, any, error) {
			to, text := strings.TrimSpace(zaloString(params, "to", "default_to")), zaloString(params, "text")
			if to == "" || strings.TrimSpace(text) == "" {
				return "", nil, fmt.Errorf("zalouser.send: to/default_to and text are required")
			}
			chatType, err := normalizeChatType(zaloString(params, "chat_type", "default_chat_type"))
			return "messages", map[string]any{"to": to, "chat_type": chatType, "text": text}, err
		}),
		zalouserMethod("zalouser.typing", "Start or clear a personal Zalo typing indicator", func(params map[string]any) (string, any, error) {
			to := strings.TrimSpace(zaloString(params, "to", "default_to"))
			if to == "" {
				return "", nil, fmt.Errorf("zalouser.typing: to/default_to is required")
			}
			chatType, err := normalizeChatType(zaloString(params, "chat_type", "default_chat_type"))
			typing, _ := params["typing"].(bool)
			return "typing", map[string]any{"to": to, "chat_type": chatType, "typing": typing}, err
		}),
		zalouserMethod("zalouser.add_reaction", "Add a reaction using an opaque zca bridge message reference", reactionRoute(false)),
		zalouserMethod("zalouser.remove_reaction", "Remove a reaction using an opaque zca bridge message reference", reactionRoute(true)),
	})
}

type bridgeAccount struct {
	BaseURL     *url.URL
	Token       string
	Profile     string
	DefaultTo   string
	DefaultChat string
}

func bridgeAccountFromParams(params map[string]any) (bridgeAccount, error) {
	var account bridgeAccount
	rawURL := strings.TrimSpace(zaloString(params, "bridge_url"))
	profile := strings.TrimSpace(zaloString(params, "profile"))
	if rawURL == "" || profile == "" {
		return account, fmt.Errorf("bridge_url and profile are required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return account, fmt.Errorf("bridge_url must be an absolute HTTP(S) base URL without userinfo, query, or fragment")
	}
	host := u.Hostname()
	loopback := host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
	allowRemote, _ := params["allow_remote_bridge"].(bool)
	token := strings.TrimSpace(zaloString(params, "bridge_token"))
	if !loopback {
		if !allowRemote {
			return account, fmt.Errorf("remote bridge requires allow_remote_bridge=true")
		}
		if u.Scheme != "https" || token == "" {
			return account, fmt.Errorf("remote bridge requires HTTPS and bridge_token")
		}
	}
	chatType, err := normalizeChatType(zaloString(params, "default_chat_type"))
	if err != nil {
		return account, err
	}
	u.Path = strings.TrimRight(u.Path, "/")
	account = bridgeAccount{BaseURL: u, Token: token, Profile: profile, DefaultTo: strings.TrimSpace(zaloString(params, "default_to")), DefaultChat: chatType}
	return account, nil
}

func zalouserMethod(name, description string, build func(map[string]any) (string, any, error)) sdk.GatewayMethod {
	return sdk.GatewayMethod{Method: name, Description: description, Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
		account, err := bridgeAccountFromParams(params)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		path, payload, err := build(params)
		if err != nil {
			return nil, err
		}
		result, err := bridgePost(ctx, nil, account, path, payload)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return map[string]any{"ok": true, "result": result}, nil
	}}
}

func reactionRoute(remove bool) func(map[string]any) (string, any, error) {
	return func(params map[string]any) (string, any, error) {
		ref, emoji := strings.TrimSpace(zaloString(params, "message_ref")), strings.TrimSpace(zaloString(params, "emoji"))
		if ref == "" || emoji == "" {
			return "", nil, fmt.Errorf("message_ref and emoji are required")
		}
		return "reactions", map[string]any{"message_ref": ref, "emoji": emoji, "remove": remove}, nil
	}
}

func (p *ZaloUserPlugin) Connect(ctx context.Context, channelID string, cfg map[string]any, onMessage func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	account, err := bridgeAccountFromParams(cfg)
	if err != nil {
		return nil, fmt.Errorf("zalouser channel %q: %w", channelID, err)
	}
	allowed := map[string]bool{}
	for _, sender := range zaloStringSlice(cfg["allowed_senders"]) {
		if sender = strings.TrimSpace(sender); sender != "" {
			allowed[sender] = true
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	bot := &zaloUserBot{
		channelID: channelID, account: account, allowedSenders: allowed, onMessage: onMessage,
		done: make(chan struct{}), cancel: cancel, httpClient: &http.Client{Timeout: 20 * time.Second},
		seen: map[string]struct{}{}, messageRefs: map[string]string{}, activeTyping: map[string]string{},
	}
	conn, err := bot.dial(runCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("zalouser channel %q: connect bridge event stream: %w", channelID, err)
	}
	bot.connMu.Lock()
	bot.conn = conn
	bot.connMu.Unlock()
	go bot.run(runCtx, conn)
	return bot, nil
}

type bridgeEvent struct {
	Type       string `json:"type"`
	EventID    string `json:"event_id"`
	MessageRef string `json:"message_ref"`
	ThreadID   string `json:"thread_id"`
	ChatType   string `json:"chat_type"`
	SenderID   string `json:"sender_id"`
	Text       string `json:"text"`
	Timestamp  int64  `json:"timestamp_ms"`
	IsSelf     bool   `json:"is_self"`
}

type zaloUserBot struct {
	mu             sync.Mutex
	connMu         sync.Mutex
	channelID      string
	account        bridgeAccount
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	cancel         context.CancelFunc
	httpClient     *http.Client
	conn           *websocket.Conn
	seen           map[string]struct{}
	seenOrder      []string
	messageRefs    map[string]string
	activeTyping   map[string]string
}

func (b *zaloUserBot) ID() string { return b.channelID }

func (b *zaloUserBot) Close() {
	b.cancel()
	b.connMu.Lock()
	if b.conn != nil {
		_ = b.conn.Close(websocket.StatusNormalClosure, "shutdown")
	}
	b.connMu.Unlock()
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

func (b *zaloUserBot) dial(ctx context.Context) (*websocket.Conn, error) {
	wsURL := *b.account.BaseURL
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	wsURL.Path = strings.TrimRight(wsURL.Path, "/") + "/v1/profiles/" + url.PathEscape(b.account.Profile) + "/events"
	header := make(http.Header)
	if b.account.Token != "" {
		header.Set("Authorization", "Bearer "+b.account.Token)
	}
	conn, _, err := websocket.Dial(ctx, wsURL.String(), &websocket.DialOptions{HTTPClient: b.httpClient, HTTPHeader: header})
	if err == nil {
		conn.SetReadLimit(1 << 20)
	}
	return conn, err
}

func (b *zaloUserBot) run(ctx context.Context, conn *websocket.Conn) {
	backoff := time.Second
	for {
		err := b.readEvents(ctx, conn)
		_ = conn.Close(websocket.StatusNormalClosure, "reconnect")
		if ctx.Err() != nil {
			return
		}
		log.Printf("zalouser: event stream ended channel=%s: %v", b.channelID, err)
		for {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-b.done:
				timer.Stop()
				return
			case <-timer.C:
			}
			next, dialErr := b.dial(ctx)
			if dialErr == nil {
				conn = next
				b.connMu.Lock()
				b.conn = next
				b.connMu.Unlock()
				backoff = time.Second
				break
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
		}
	}
}

func (b *zaloUserBot) readEvents(ctx context.Context, conn *websocket.Conn) error {
	for {
		var event bridgeEvent
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			return err
		}
		b.handleEvent(event)
	}
}

func (b *zaloUserBot) handleEvent(event bridgeEvent) {
	if event.Type != "message" || event.IsSelf || strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.MessageRef) == "" || strings.TrimSpace(event.SenderID) == "" || strings.TrimSpace(event.ThreadID) == "" || strings.TrimSpace(event.Text) == "" {
		return
	}
	if len(b.allowedSenders) > 0 && !b.allowedSenders[event.SenderID] {
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
	threadID := ""
	if event.ChatType == "group" {
		threadID = event.ThreadID
	}
	b.onMessage(sdk.InboundChannelMessage{ChannelID: b.channelID, SenderID: event.SenderID, Text: event.Text, EventID: event.EventID, ThreadID: threadID, CreatedAt: event.Timestamp / 1000})
}

func (b *zaloUserBot) target(ctx context.Context) (string, string, error) {
	to := strings.TrimSpace(sdk.ChannelReplyTarget(ctx))
	if to == "" {
		to = b.account.DefaultTo
	}
	if to == "" {
		return "", "", fmt.Errorf("zalouser: target is required")
	}
	return to, b.account.DefaultChat, nil
}

func (b *zaloUserBot) Send(ctx context.Context, text string) error {
	to, chatType, err := b.target(ctx)
	if err != nil {
		return err
	}
	defer b.clearTypingIfActive(ctx, to, chatType)
	_, err = bridgePost(ctx, b.httpClient, b.account, "messages", map[string]any{"to": to, "chat_type": chatType, "text": text})
	return err
}

// SendInThread sends to a Zalo group conversation. Zalo group IDs are the
// provider-native thread identifiers delivered on inbound group messages.
func (b *zaloUserBot) SendInThread(ctx context.Context, threadID, text string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || strings.TrimSpace(text) == "" {
		return fmt.Errorf("zalouser: threadID and text are required")
	}
	defer b.clearTypingIfActive(ctx, threadID, "group")
	_, err := bridgePost(ctx, b.httpClient, b.account, "messages", map[string]any{"to": threadID, "chat_type": "group", "text": text})
	return err
}

func (b *zaloUserBot) SendTyping(ctx context.Context, _ int) error {
	to, chatType, err := b.target(ctx)
	if err != nil {
		return err
	}
	if _, err = bridgePost(ctx, b.httpClient, b.account, "typing", map[string]any{"to": to, "chat_type": chatType, "typing": true}); err != nil {
		return err
	}
	b.mu.Lock()
	b.activeTyping[to] = chatType
	b.mu.Unlock()
	return nil
}

// ClearTyping explicitly ends a typing lifecycle. Callers may discover this
// additive optional method with an interface assertion without changing sdk.
func (b *zaloUserBot) ClearTyping(ctx context.Context) error {
	to, chatType, err := b.target(ctx)
	if err != nil {
		return err
	}
	return b.clearTyping(ctx, to, chatType)
}

func (b *zaloUserBot) clearTypingIfActive(ctx context.Context, to, chatType string) {
	b.mu.Lock()
	_, active := b.activeTyping[to]
	b.mu.Unlock()
	if active {
		_ = b.clearTyping(ctx, to, chatType)
	}
}

func (b *zaloUserBot) clearTyping(ctx context.Context, to, chatType string) error {
	if _, err := bridgePost(ctx, b.httpClient, b.account, "typing", map[string]any{"to": to, "chat_type": chatType, "typing": false}); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.activeTyping, to)
	b.mu.Unlock()
	return nil
}

func (b *zaloUserBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	return b.react(ctx, eventID, emoji, false)
}

func (b *zaloUserBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return b.react(ctx, eventID, emoji, true)
}

func (b *zaloUserBot) react(ctx context.Context, eventID, emoji string, remove bool) error {
	b.mu.Lock()
	ref := b.messageRefs[eventID]
	b.mu.Unlock()
	if ref == "" {
		return fmt.Errorf("zalouser: message reference for %q expired from local cache", eventID)
	}
	_, err := bridgePost(ctx, b.httpClient, b.account, "reactions", map[string]any{"message_ref": ref, "emoji": emoji, "remove": remove})
	return err
}

func bridgePost(ctx context.Context, client *http.Client, account bridgeAccount, endpoint string, payload any) (map[string]any, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	u := *account.BaseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/profiles/" + url.PathEscape(account.Profile) + "/" + strings.TrimLeft(endpoint, "/")
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
		return nil, fmt.Errorf("zca bridge: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zca bridge HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 64<<10)])))
	}
	if len(body) > 64<<10 {
		return nil, fmt.Errorf("zca bridge response exceeds 65536 bytes")
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("zca bridge response: %w", err)
	}
	return result, nil
}

func normalizeChatType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "direct", nil
	}
	if value != "direct" && value != "group" {
		return "", fmt.Errorf("chat_type must be direct or group")
	}
	return value, nil
}

func zaloString(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := params[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func zaloStringSlice(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
