// Package bluebubbles implements a BlueBubbles (iMessage) channel extension for swarmstr.
//
// BlueBubbles is a self-hosted iMessage relay server.  This plugin connects
// over its Socket.IO-compatible WebSocket API to receive new messages in
// real time and sends replies via the REST API.
//
// Registration: import _ "metiq/internal/extensions/bluebubbles" in the
// daemon main.go to register this plugin at startup.
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
// No inbound webhook endpoint is required — this plugin uses an outbound
// WebSocket connection to the BlueBubbles server.
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

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&BlueBubblesPlugin{})
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

	go bot.run(ctx)
	log.Printf("bluebubbles: started polling channel=%s server=%s", channelID, serverURL)
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
	httpClient     *http.Client

	mu          sync.Mutex
	lastMsgGUID string // GUID of last seen message, for dedup
	seenGUIDs   map[string]struct{}
}

func (b *bbBot) ID() string { return b.channelID }

func (b *bbBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// run polls the BlueBubbles REST API for new messages.
// BlueBubbles also exposes a Socket.IO endpoint, but using polling avoids the
// need for a Socket.IO client library while being equally reliable for typical
// assistant response latencies.
func (b *bbBot) run(ctx context.Context) {
	b.mu.Lock()
	b.seenGUIDs = map[string]struct{}{}
	b.mu.Unlock()

	// On first run, seed seenGUIDs with the latest 25 messages so we don't
	// replay history on startup.
	if msgs, err := b.fetchMessages(ctx, 25); err == nil {
		b.mu.Lock()
		for _, m := range msgs {
			b.seenGUIDs[m.GUID] = struct{}{}
		}
		b.mu.Unlock()
	}

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

	b.mu.Lock()
	defer b.mu.Unlock()

	// msgs is newest-first; process in reverse to deliver oldest first.
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if _, seen := b.seenGUIDs[m.GUID]; seen {
			continue
		}
		b.seenGUIDs[m.GUID] = struct{}{}

		// Skip messages sent by the bot itself.
		if m.IsFromMe {
			continue
		}

		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}

		senderAddr := ""
		if m.Handle != nil {
			senderAddr = strings.ToLower(m.Handle.Address)
		}

		if len(b.allowedSenders) > 0 && !b.allowedSenders[senderAddr] {
			continue
		}

		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  senderAddr,
			Text:      text,
			EventID:   m.GUID,
			CreatedAt: m.DateCreated / 1000,
		})
	}
	return nil
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
