// Package qqbot implements a QQ Open Platform Bot channel extension for metiq.
package qqbot

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

	"github.com/gorilla/websocket"

	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("qqbot", func() sdk.ChannelPlugin { return &QQBotPlugin{} })
}

var newHTTPClient = func(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }

// QQBotPlugin is the factory for QQ Bot channel instances.
type QQBotPlugin struct{}

func (p *QQBotPlugin) ID() string   { return "qqbot" }
func (p *QQBotPlugin) Type() string { return "QQ Bot" }

func (p *QQBotPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"app_id":          map[string]any{"type": "string", "description": "QQ Open Platform bot AppID."},
			"client_secret":   map[string]any{"type": "string", "description": "QQ Open Platform bot client secret."},
			"target_type":     map[string]any{"type": "string", "description": "Default outbound target type: c2c, group, guild, dm, or channel."},
			"target_id":       map[string]any{"type": "string", "description": "Default outbound openid, group_openid, channel_id, or guild_id."},
			"sandbox":         map[string]any{"type": "boolean", "description": "Use sandbox API host."},
			"allowed_senders": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional sender openid/id allowlist."},
		},
		"required": []string{"app_id", "client_secret"},
	}
}

func (p *QQBotPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Typing: true, MultiAccount: true}
}

func (p *QQBotPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *QQBotPlugin) Connect(ctx context.Context, channelID string, cfg map[string]any, onMessage func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	appID, _ := cfg["app_id"].(string)
	if appID == "" {
		appID, _ = cfg["appId"].(string)
	}
	secret, _ := cfg["client_secret"].(string)
	if secret == "" {
		secret, _ = cfg["clientSecret"].(string)
	}
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("qqbot channel %q: app_id and client_secret are required", channelID)
	}
	targetType, _ := cfg["target_type"].(string)
	if targetType == "" {
		targetType, _ = cfg["targetType"].(string)
	}
	targetID, _ := cfg["target_id"].(string)
	if targetID == "" {
		targetID, _ = cfg["targetId"].(string)
	}
	allowed := map[string]struct{}{}
	if v, ok := cfg["allowed_senders"].([]any); ok {
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				allowed[s] = struct{}{}
			}
		}
	}
	sandbox, _ := cfg["sandbox"].(bool)
	bot := &qqBot{
		channelID:      channelID,
		appID:          strings.TrimSpace(appID),
		clientSecret:   strings.TrimSpace(secret),
		targetType:     normalizeTargetType(targetType),
		targetID:       strings.TrimSpace(targetID),
		allowedSenders: allowed,
		apiBase:        qqAPIBase(sandbox),
		httpClient:     newHTTPClient(20 * time.Second),
		done:           make(chan struct{}),
		onMessage:      onMessage,
	}
	go bot.run(ctx)
	return bot, nil
}

type qqBot struct {
	mu             sync.Mutex
	channelID      string
	appID          string
	clientSecret   string
	targetType     string
	targetID       string
	allowedSenders map[string]struct{}
	apiBase        string
	httpClient     *http.Client
	done           chan struct{}
	onMessage      func(sdk.InboundChannelMessage)
	accessToken    string
	tokenExpiresAt time.Time
	lastSeq        *int64
	sessionID      string
	conn           *websocket.Conn
}

func (b *qqBot) ID() string { return b.channelID }

func (b *qqBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	b.mu.Lock()
	if b.conn != nil {
		_ = b.conn.Close()
	}
	b.mu.Unlock()
}

func (b *qqBot) Send(ctx context.Context, text string) error {
	b.mu.Lock()
	targetType, targetID := b.targetType, b.targetID
	b.mu.Unlock()
	if targetType == "" || targetID == "" {
		return fmt.Errorf("qqbot %s: no outbound target known yet", b.channelID)
	}
	return b.sendText(ctx, targetType, targetID, text, "")
}

func (b *qqBot) SendTyping(ctx context.Context, durationMS int) error {
	b.mu.Lock()
	targetType, targetID := b.targetType, b.targetID
	b.mu.Unlock()
	// The QQ input_notify (typing) API is only defined for c2c (private) chats.
	// Report an explicit error for other target types instead of silently
	// pretending success, so callers can gate on the failure.
	if targetType != "c2c" {
		return fmt.Errorf("qqbot %s: typing indicator is only supported in c2c chats (target_type=%q)", b.channelID, targetType)
	}
	if targetID == "" {
		return fmt.Errorf("qqbot %s: typing indicator requires a known c2c target", b.channelID)
	}
	seconds := durationMS / 1000
	if seconds <= 0 {
		seconds = 60
	}
	token, err := b.token(ctx)
	if err != nil {
		return err
	}
	return b.postQQ(ctx, token, qqMessagePath(targetType, targetID), map[string]any{"msg_type": 6, "input_notify": map[string]any{"input_type": 1, "input_second": seconds}, "msg_seq": 1}, nil)
}

func (b *qqBot) run(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		default:
		}
		if err := b.connectGateway(ctx); err != nil {
			log.Printf("qqbot: gateway error channel=%s: %v", b.channelID, err)
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

func (b *qqBot) connectGateway(ctx context.Context) error {
	token, err := b.token(ctx)
	if err != nil {
		return err
	}
	gatewayURL, err := b.gatewayURL(ctx, token)
	if err != nil {
		return err
	}
	header := http.Header{}
	header.Set("User-Agent", "metiq-qqbot/1.0")
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, gatewayURL, header)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()
	defer conn.Close()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if err := b.handleGatewayPayload(ctx, conn, raw, token); err != nil {
			return err
		}
	}
}

type gatewayPayload struct {
	Op int             `json:"op"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

func (b *qqBot) handleGatewayPayload(ctx context.Context, conn *websocket.Conn, raw []byte, token string) error {
	var payload gatewayPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if payload.S != nil {
		b.mu.Lock()
		b.lastSeq = payload.S
		b.mu.Unlock()
	}
	switch payload.Op {
	case 10: // Hello
		var hello struct {
			HeartbeatInterval int `json:"heartbeat_interval"`
		}
		_ = json.Unmarshal(payload.D, &hello)
		if err := b.identify(conn, token); err != nil {
			return err
		}
		if hello.HeartbeatInterval > 0 {
			go b.heartbeatLoop(conn, time.Duration(hello.HeartbeatInterval)*time.Millisecond)
		}
	case 0: // Dispatch
		if payload.T == "READY" {
			var ready struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(payload.D, &ready)
			b.mu.Lock()
			b.sessionID = ready.SessionID
			b.mu.Unlock()
			return nil
		}
		if msg, ok := normalizeQQGatewayMessage(b.channelID, payload.T, payload.D); ok {
			b.rememberTarget(msg)
			if len(b.allowedSenders) > 0 {
				if _, allowed := b.allowedSenders[msg.SenderID]; !allowed {
					return nil
				}
			}
			b.onMessage(msg)
		}
	case 7, 9:
		return fmt.Errorf("gateway requested reconnect/invalid session")
	}
	return nil
}

func (b *qqBot) identify(conn *websocket.Conn, token string) error {
	payload := map[string]any{"op": 2, "d": map[string]any{"token": "QQBot " + token, "intents": 0x80000000 | 1<<25 | 1<<30 | 1<<9 | 1<<12, "shard": []int{0, 1}, "properties": map[string]string{"os": "linux", "browser": "metiq", "device": "metiq"}}}
	return conn.WriteJSON(payload)
}

func (b *qqBot) heartbeatLoop(conn *websocket.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-ticker.C:
			b.mu.Lock()
			seq := b.lastSeq
			b.mu.Unlock()
			_ = conn.WriteJSON(map[string]any{"op": 1, "d": seq})
		}
	}
}

func (b *qqBot) rememberTarget(msg sdk.InboundChannelMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if msg.ThreadID != "" {
		parts := strings.SplitN(msg.ThreadID, ":", 2)
		if len(parts) == 2 {
			b.targetType, b.targetID = parts[0], parts[1]
		}
	}
}

func (b *qqBot) token(ctx context.Context) (string, error) {
	b.mu.Lock()
	if b.accessToken != "" && time.Now().Before(b.tokenExpiresAt.Add(-5*time.Minute)) {
		t := b.accessToken
		b.mu.Unlock()
		return t, nil
	}
	b.mu.Unlock()
	body, _ := json.Marshal(map[string]string{"appId": b.appID, "clientSecret": b.clientSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/app/getAppAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("qqbot token: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("qqbot token: empty access_token")
	}
	if result.ExpiresIn <= 0 {
		result.ExpiresIn = 7200
	}
	b.mu.Lock()
	b.accessToken = result.AccessToken
	b.tokenExpiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	b.mu.Unlock()
	return result.AccessToken, nil
}

func (b *qqBot) gatewayURL(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.apiBase+"/gateway", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result); err != nil {
		return "", err
	}
	if result.URL == "" {
		return "", fmt.Errorf("qqbot gateway: empty url")
	}
	return result.URL, nil
}

func (b *qqBot) sendText(ctx context.Context, targetType, targetID, text, msgID string) error {
	token, err := b.token(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{"content": text}
	if targetType == "c2c" || targetType == "group" {
		payload = map[string]any{"msg_type": 0, "content": text, "msg_seq": 1}
	}
	if msgID != "" {
		payload["msg_id"] = msgID
	}
	return b.postQQ(ctx, token, qqMessagePath(targetType, targetID), payload, nil)
}

func (b *qqBot) postQQ(ctx context.Context, token, path string, payload map[string]any, out any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qqbot send: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func qqAPIBase(sandbox bool) string {
	if sandbox {
		return "https://sandbox.api.sgroup.qq.com"
	}
	return "https://api.sgroup.qq.com"
}

func normalizeTargetType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "private", "user", "c2c":
		return "c2c"
	case "group":
		return "group"
	case "guild":
		return "guild"
	case "direct", "dm":
		return "dm"
	case "channel":
		return "channel"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func qqMessagePath(targetType, targetID string) string {
	targetType = normalizeTargetType(targetType)
	switch targetType {
	case "c2c":
		return "/v2/users/" + targetID + "/messages"
	case "group":
		return "/v2/groups/" + targetID + "/messages"
	case "dm":
		return "/dms/" + targetID + "/messages"
	case "channel", "guild":
		return "/channels/" + targetID + "/messages"
	default:
		return "/v2/users/" + targetID + "/messages"
	}
}

type qqInbound struct {
	ID          string          `json:"id"`
	Content     string          `json:"content"`
	Timestamp   string          `json:"timestamp"`
	ChannelID   string          `json:"channel_id"`
	GuildID     string          `json:"guild_id"`
	GroupOpenID string          `json:"group_openid"`
	Author      json.RawMessage `json:"author"`
	Attachments []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"attachments"`
	MessageScene *struct {
		Ext string `json:"ext"`
	} `json:"message_scene"`
}

func normalizeQQGatewayMessage(channelID, eventType string, raw json.RawMessage) (sdk.InboundChannelMessage, bool) {
	var ev qqInbound
	if err := json.Unmarshal(raw, &ev); err != nil {
		return sdk.InboundChannelMessage{}, false
	}
	targetType := ""
	targetID := ""
	senderID, senderName := qqSender(ev.Author)
	switch eventType {
	case "C2C_MESSAGE_CREATE":
		targetType, targetID = "c2c", senderID
	case "GROUP_AT_MESSAGE_CREATE", "GROUP_MESSAGE_CREATE":
		targetType, targetID = "group", ev.GroupOpenID
	case "AT_MESSAGE_CREATE":
		targetType, targetID = "channel", ev.ChannelID
	case "DIRECT_MESSAGE_CREATE":
		targetType, targetID = "dm", ev.GuildID
	default:
		return sdk.InboundChannelMessage{}, false
	}
	text := strings.TrimSpace(ev.Content)
	mediaURL, mediaMIME := qqFirstAttachment(ev)
	if text == "" && mediaURL == "" {
		return sdk.InboundChannelMessage{}, false
	}
	createdAt := time.Now().Unix()
	if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
		createdAt = ts.Unix()
	}
	if senderID == "" {
		senderID = senderName
	}
	return sdk.InboundChannelMessage{ChannelID: channelID, SenderID: senderID, Text: text, EventID: ev.ID, CreatedAt: createdAt, ThreadID: targetType + ":" + targetID, MediaURL: mediaURL, MediaMIME: mediaMIME}, true
}

func qqSender(raw json.RawMessage) (string, string) {
	var a struct {
		UserOpenID   string `json:"user_openid"`
		MemberOpenID string `json:"member_openid"`
		ID           string `json:"id"`
		Username     string `json:"username"`
	}
	_ = json.Unmarshal(raw, &a)
	for _, s := range []string{a.UserOpenID, a.MemberOpenID, a.ID} {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), a.Username
		}
	}
	return "", a.Username
}

func qqFirstAttachment(ev qqInbound) (string, string) {
	if len(ev.Attachments) == 0 {
		return "", ""
	}
	return strings.TrimSpace(ev.Attachments[0].URL), strings.TrimSpace(ev.Attachments[0].ContentType)
}

var _ sdk.TypingHandle = (*qqBot)(nil)
