// Package bluebubbles implements a BlueBubbles (iMessage) channel extension for metiq.
//
// BlueBubbles is a self-hosted iMessage relay server.  This plugin receives new
// messages event-driven via the BlueBubbles Socket.IO push endpoint
// (Engine.IO v4 over WebSocket, "new-message" events) and sends replies via the
// REST API.  If the Socket.IO endpoint cannot be reached, it falls back to
// polling the REST message endpoint on a short interval — a documented,
// non-event-driven fallback.
//
// Registration: import _ "metiq/internal/extensions/bluebubbles" in the
// daemon main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "server_url":      "http://192.168.1.10:1234",  // required: BlueBubbles server base URL
//	  "password":        "secret",                    // required: server password
//	  "chat_guid":       "iMessage;-;+11234567890",   // required: iMessage chat GUID
//	  "allowed_senders": []                           // optional: handle/number allowlist
//	}
//
// No inbound webhook endpoint is required — the plugin opens an outbound
// Socket.IO WebSocket to the BlueBubbles server for inbound push (with REST
// polling as a fallback) and makes outbound REST calls to send.
package bluebubbles

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

	"github.com/coder/websocket"

	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("bluebubbles", func() sdk.ChannelPlugin { return &BlueBubblesPlugin{} })
}

// BlueBubblesPlugin is the factory for BlueBubbles channel instances.
type BlueBubblesPlugin struct{}

func (p *BlueBubblesPlugin) ID() string   { return "bluebubbles" }
func (p *BlueBubblesPlugin) Type() string { return "BlueBubbles" }

func (p *BlueBubblesPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server_url": map[string]any{
				"type":        "string",
				"description": "Base URL of the BlueBubbles server, e.g. http://192.168.1.10:1234.",
			},
			"password": map[string]any{
				"type":        "string",
				"description": "BlueBubbles server password.",
			},
			"chat_guid": map[string]any{
				"type":        "string",
				"description": "iMessage chat GUID, e.g. iMessage;-;+11234567890 or iMessage;+;chatroom-uuid.",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of sender handles.",
			},
		},
		"required": []string{"server_url", "password", "chat_guid"},
	}
}

func (p *BlueBubblesPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Reactions: true}
}

func (p *BlueBubblesPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *BlueBubblesPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	serverURL, _ := cfg["server_url"].(string)
	password, _ := cfg["password"].(string)
	chatGUID, _ := cfg["chat_guid"].(string)
	if serverURL == "" || password == "" || chatGUID == "" {
		return nil, fmt.Errorf("bluebubbles channel %q: server_url, password, and chat_guid are required", channelID)
	}
	serverURL = strings.TrimRight(serverURL, "/")

	allowedSenders := map[string]bool{}
	if v, ok := cfg["allowed_senders"].([]interface{}); ok {
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[strings.ToLower(e)] = true
			}
		}
	}

	bot := &bbBot{
		channelID:      channelID,
		serverURL:      serverURL,
		password:       password,
		chatGUID:       chatGUID,
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 20 * time.Second},
	}

	runCtx, cancel := context.WithCancel(ctx)
	bot.cancel = cancel
	go bot.run(runCtx)
	return bot, nil
}

// ─── Bot ──────────────────────────────────────────────────────────────────────

const (
	bbPollInterval  = 5 * time.Second
	bbMaxReconnects = 20
)

type bbBot struct {
	channelID      string
	serverURL      string
	password       string
	chatGUID       string
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	cancel         context.CancelFunc
	httpClient     *http.Client

	mu          sync.Mutex
	lastMsgGUID string // GUID of last seen message, for dedup
	seenGUIDs   map[string]struct{}
}

func (b *bbBot) ID() string { return b.channelID }

func (b *bbBot) Close() {
	if b.cancel != nil {
		b.cancel()
	}
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// run seeds dedup state, then prefers the event-driven Socket.IO push transport,
// falling back to REST polling (a documented, non-event-driven fallback) if it
// is unavailable.
func (b *bbBot) run(ctx context.Context) {
	b.mu.Lock()
	b.seenGUIDs = map[string]struct{}{}
	b.mu.Unlock()

	// Seed seenGUIDs with the latest 25 messages so we don't replay history on
	// startup (applies to both the Socket.IO and polling paths).
	if msgs, err := b.fetchMessages(ctx, 25); err == nil {
		b.mu.Lock()
		for _, m := range msgs {
			b.seenGUIDs[m.GUID] = struct{}{}
		}
		b.mu.Unlock()
	}

	if err := b.runSocket(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("bluebubbles: channel=%s Socket.IO push unavailable (%v); using REST polling fallback (server=%s)", b.channelID, err, b.serverURL)
		b.pollLoop(ctx)
	}
}

// pollLoop is the REST polling fallback: a wait-and-check ticker over the
// BlueBubbles message endpoint. Prefer the Socket.IO push path; this exists
// only for servers where Socket.IO cannot be reached.
func (b *bbBot) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(bbPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.poll(ctx); err != nil {
				log.Printf("bluebubbles: poll error channel=%s: %v", b.channelID, err)
			}
		}
	}
}

// bbMessage is a partial BlueBubbles message object.
type bbMessage struct {
	GUID        string    `json:"guid"`
	Text        string    `json:"text"`
	IsFromMe    bool      `json:"isFromMe"`
	Handle      *bbHandle `json:"handle"`
	DateCreated int64     `json:"dateCreated"`
}

type bbHandle struct {
	Address string `json:"address"`
}

type bbMessagesResp struct {
	Status int         `json:"status"`
	Data   []bbMessage `json:"data"`
}

// fetchMessages retrieves the last `limit` messages from the chat via REST.
func (b *bbBot) fetchMessages(ctx context.Context, limit int) ([]bbMessage, error) {
	u := fmt.Sprintf("%s/api/v1/chat/%s/message?password=%s&limit=%d&sort=desc",
		b.serverURL, url.PathEscape(b.chatGUID), url.QueryEscape(b.password), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var result bbMessagesResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// poll fetches recent messages and delivers any unseen ones.
func (b *bbBot) poll(ctx context.Context) error {
	msgs, err := b.fetchMessages(ctx, 10)
	if err != nil {
		return err
	}
	// msgs is newest-first; process in reverse to deliver oldest first.
	for i := len(msgs) - 1; i >= 0; i-- {
		b.deliverMessage(msgs[i])
	}
	return nil
}

// deliverMessage dedups by GUID, applies self/allowlist filtering, and delivers
// a message to the agent. It is shared by the Socket.IO push path and the
// polling fallback.
func (b *bbBot) deliverMessage(m bbMessage) {
	b.mu.Lock()
	if b.seenGUIDs == nil {
		b.seenGUIDs = map[string]struct{}{}
	}
	if _, seen := b.seenGUIDs[m.GUID]; seen {
		b.mu.Unlock()
		return
	}
	b.seenGUIDs[m.GUID] = struct{}{}
	b.mu.Unlock()

	// Skip messages sent by the bot itself.
	if m.IsFromMe {
		return
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}
	senderAddr := ""
	if m.Handle != nil {
		senderAddr = strings.ToLower(m.Handle.Address)
	}
	if len(b.allowedSenders) > 0 && !b.allowedSenders[senderAddr] {
		return
	}
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  senderAddr,
		Text:      text,
		EventID:   m.GUID,
		CreatedAt: m.DateCreated / 1000,
	})
}

// ─── Socket.IO push (event-driven inbound) ────────────────────────────────

func (b *bbBot) socketURL() string {
	u := b.serverURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/socket.io/?EIO=4&transport=websocket&password=" + url.QueryEscape(b.password)
}

// runSocket connects the Socket.IO client and serves events, reconnecting with
// backoff. It returns an error (triggering the polling fallback) only if the
// initial connection fails or reconnection is exhausted.
func (b *bbBot) runSocket(ctx context.Context) error {
	conn, err := b.socketConnect(ctx)
	if err != nil {
		return err
	}
	log.Printf("bluebubbles: channel=%s connected via Socket.IO push (server=%s)", b.channelID, b.serverURL)

	backoff := time.Second
	attempts := 0
	for {
		serr := b.socketServe(ctx, conn)
		_ = conn.Close(websocket.StatusNormalClosure, "reconnect")
		select {
		case <-ctx.Done():
			return nil
		case <-b.done:
			return nil
		default:
		}
		log.Printf("bluebubbles: channel=%s Socket.IO stream ended (%v); reconnecting", b.channelID, serr)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-b.done:
				return nil
			case <-time.After(backoff):
			}
			attempts++
			nc, derr := b.socketConnect(ctx)
			if derr == nil {
				conn = nc
				backoff = time.Second
				attempts = 0
				break
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			if attempts >= bbMaxReconnects {
				return fmt.Errorf("socket.io reconnect exhausted after %d attempts: %w", attempts, derr)
			}
		}
	}
}

// socketConnect performs the Engine.IO v4 + Socket.IO handshake over WebSocket
// and returns a connection ready to receive events.
func (b *bbBot) socketConnect(ctx context.Context) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, b.socketURL(), nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)

	// Engine.IO OPEN packet: "0{...}".
	_, data, err := conn.Read(dialCtx)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "handshake read")
		return nil, err
	}
	if len(data) == 0 || data[0] != '0' {
		conn.Close(websocket.StatusProtocolError, "bad handshake")
		return nil, fmt.Errorf("unexpected engine.io handshake: %q", string(data))
	}

	// Socket.IO CONNECT to the default namespace: "40".
	if err := conn.Write(dialCtx, websocket.MessageText, []byte("40")); err != nil {
		conn.Close(websocket.StatusInternalError, "connect write")
		return nil, err
	}

	// Await the Socket.IO CONNECT acknowledgement ("40..."), answering any
	// Engine.IO pings ("2") in the meantime.
	for {
		_, d, err := conn.Read(dialCtx)
		if err != nil {
			conn.Close(websocket.StatusInternalError, "connect ack")
			return nil, err
		}
		if len(d) == 0 {
			continue
		}
		switch {
		case d[0] == '2':
			_ = conn.Write(dialCtx, websocket.MessageText, []byte("3"))
		case len(d) >= 2 && d[0] == '4' && d[1] == '0':
			return conn, nil
		case len(d) >= 2 && d[0] == '4' && d[1] == '4':
			conn.Close(websocket.StatusPolicyViolation, "connect error")
			return nil, fmt.Errorf("socket.io connect error: %s", strings.TrimSpace(string(d[2:])))
		}
	}
}

// socketServe reads Engine.IO/Socket.IO frames and dispatches "new-message"
// events until the connection fails.
func (b *bbBot) socketServe(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		switch data[0] {
		case '2': // Engine.IO PING → PONG
			if err := conn.Write(ctx, websocket.MessageText, []byte("3")); err != nil {
				return err
			}
		case '4': // Engine.IO MESSAGE → Socket.IO packet
			b.handleSocketPacket(data[1:])
		case '1': // Engine.IO CLOSE
			return fmt.Errorf("server closed engine.io connection")
		}
	}
}

// handleSocketPacket parses a Socket.IO packet and delivers "new-message"
// events. Only EVENT packets (type '2') in the default namespace are handled.
func (b *bbBot) handleSocketPacket(p []byte) {
	if len(p) == 0 || p[0] != '2' {
		return
	}
	idx := bytes.IndexByte(p, '[')
	if idx < 0 {
		return
	}
	var evt []json.RawMessage
	if err := json.Unmarshal(p[idx:], &evt); err != nil || len(evt) < 2 {
		return
	}
	var name string
	if err := json.Unmarshal(evt[0], &name); err != nil {
		return
	}
	if name != "new-message" {
		return
	}
	var m bbMessage
	if err := json.Unmarshal(evt[1], &m); err != nil {
		return
	}
	b.deliverMessage(m)
}

// Send posts a text message to the BlueBubbles chat via REST API.
func (b *bbBot) Send(ctx context.Context, text string) error {
	payload, _ := json.Marshal(map[string]any{
		"chatGuid": b.chatGUID,
		"message":  text,
		"method":   "apple-script",
		"tempGuid": fmt.Sprintf("temp-%d", time.Now().UnixNano()),
	})
	u := fmt.Sprintf("%s/api/v1/message/text?password=%s", b.serverURL, url.QueryEscape(b.password))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("bluebubbles send: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("bluebubbles send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("bluebubbles send: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// AddReaction sends a Tapback reaction to a specific message.
// emoji should be one of: love, like, dislike, laugh, emphasize, question.
func (b *bbBot) AddReaction(ctx context.Context, msgGUID, emoji string) error {
	payload, _ := json.Marshal(map[string]any{
		"chatGuid":            b.chatGUID,
		"selectedMessageGuid": msgGUID,
		"reaction":            emoji,
	})
	u := fmt.Sprintf("%s/api/v1/message/react?password=%s", b.serverURL, url.QueryEscape(b.password))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("bluebubbles react: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("bluebubbles react: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("bluebubbles react: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// RemoveReaction removes a Tapback reaction. BlueBubbles does not expose a
// dedicated remove endpoint; sending the same reaction type again acts as a toggle.
func (b *bbBot) RemoveReaction(ctx context.Context, msgGUID, emoji string) error {
	return b.AddReaction(ctx, msgGUID, emoji)
}

// Ensure bbBot satisfies sdk.ReactionHandle so callers can type-assert it.
var _ sdk.ReactionHandle = (*bbBot)(nil)
