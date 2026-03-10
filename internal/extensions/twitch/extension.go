// Package twitch implements a Twitch chat channel extension for swarmstr
// using the Twitch IRC WebSocket gateway (wss://irc-ws.chat.twitch.tv:443).
//
// Registration: import _ "swarmstr/internal/extensions/twitch" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "oauth_token":     "oauth:your-token",  // required: Twitch OAuth token with chat:read+chat:edit
//	  "nick":            "your_bot_nick",      // required: Twitch username of the bot account
//	  "channels":        ["#channelname"],     // required: Twitch channels to join (with # prefix)
//	  "allowed_senders": [],                   // optional: allowlist of Twitch usernames
//	  "require_mention": false                 // optional: only process messages that @mention the bot
//	}
//
// To add a Twitch channel to your swarmstr config:
//
//	"nostr_channels": {
//	  "twitch-main": {
//	    "kind": "twitch",
//	    "config": {
//	      "oauth_token": "oauth:YOUR_TOKEN",
//	      "nick":        "my_bot",
//	      "channels":    ["#mychannel"]
//	    }
//	  }
//	}
package twitch

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"swarmstr/internal/gateway/channels"
	"swarmstr/internal/plugins/sdk"
)

const (
	twitchIRCAddr   = "irc-ws.chat.twitch.tv:6697"
	reconnectDelay  = 5 * time.Second
	maxReconnects   = 20
	pingInterval    = 4 * time.Minute
)

func init() {
	channels.RegisterChannelPlugin(&TwitchPlugin{})
}

// TwitchPlugin is the factory for Twitch chat channel instances.
type TwitchPlugin struct{}

func (p *TwitchPlugin) ID() string   { return "twitch" }
func (p *TwitchPlugin) Type() string { return "Twitch" }

func (p *TwitchPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"oauth_token": map[string]any{
				"type":        "string",
				"description": "Twitch OAuth token in the form 'oauth:YOUR_TOKEN'. Requires chat:read and chat:edit scopes.",
			},
			"nick": map[string]any{
				"type":        "string",
				"description": "Twitch username of the bot account (lowercase).",
			},
			"channels": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Twitch channel names to join, with # prefix (e.g. [\"#mychannel\"]).",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of Twitch usernames.",
			},
			"require_mention": map[string]any{
				"type":        "boolean",
				"description": "If true, only respond when the bot is @mentioned.",
			},
		},
		"required": []string{"oauth_token", "nick", "channels"},
	}
}

func (p *TwitchPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{}
}

func (p *TwitchPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *TwitchPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	oauthToken, _ := cfg["oauth_token"].(string)
	nick, _ := cfg["nick"].(string)
	if oauthToken == "" || nick == "" {
		return nil, fmt.Errorf("twitch channel %q: oauth_token and nick are required", channelID)
	}

	var joinChannels []string
	if v, ok := cfg["channels"].([]interface{}); ok {
		for _, c := range v {
			if s, ok := c.(string); ok && s != "" {
				if !strings.HasPrefix(s, "#") {
					s = "#" + s
				}
				joinChannels = append(joinChannels, strings.ToLower(s))
			}
		}
	}
	if len(joinChannels) == 0 {
		return nil, fmt.Errorf("twitch channel %q: at least one channel is required", channelID)
	}

	allowedSenders := map[string]bool{}
	if v, ok := cfg["allowed_senders"].([]interface{}); ok {
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[strings.ToLower(e)] = true
			}
		}
	}

	requireMention, _ := cfg["require_mention"].(bool)

	bot := &twitchBot{
		channelID:      channelID,
		oauthToken:     oauthToken,
		nick:           strings.ToLower(nick),
		joinChannels:   joinChannels,
		allowedSenders: allowedSenders,
		requireMention: requireMention,
		onMessage:      onMessage,
		ctx:            ctx,
		sendCh:         make(chan string, 64),
	}

	go bot.run()
	return bot, nil
}

// ─── Bot ──────────────────────────────────────────────────────────────────────

type twitchBot struct {
	channelID      string
	oauthToken     string
	nick           string
	joinChannels   []string
	allowedSenders map[string]bool
	requireMention bool
	onMessage      func(sdk.InboundChannelMessage)
	ctx            context.Context
	sendCh         chan string

	mu   sync.Mutex
	conn net.Conn
}

func (b *twitchBot) ID() string { return b.channelID }

func (b *twitchBot) Close() {
	b.mu.Lock()
	if b.conn != nil {
		b.conn.Close()
	}
	b.mu.Unlock()
}

// run maintains the IRC connection with reconnect logic.
func (b *twitchBot) run() {
	reconnects := 0
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}
		if err := b.connect(); err != nil {
			log.Printf("twitch channel=%s connect error: %v", b.channelID, err)
		}
		reconnects++
		if reconnects >= maxReconnects {
			log.Printf("twitch channel=%s max reconnects reached, giving up", b.channelID)
			return
		}
		select {
		case <-b.ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

func (b *twitchBot) connect() error {
	tlsCfg := &tls.Config{ServerName: "irc-ws.chat.twitch.tv"}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", twitchIRCAddr, tlsCfg)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()
	connDone := make(chan struct{})
	defer func() {
		close(connDone)
		conn.Close()
		b.mu.Lock()
		b.conn = nil
		b.mu.Unlock()
	}()

	// Handshake.
	for _, line := range []string{
		"CAP REQ :twitch.tv/tags twitch.tv/commands",
		"PASS " + b.oauthToken,
		"NICK " + b.nick,
	} {
		if _, err := fmt.Fprintf(conn, "%s\r\n", line); err != nil {
			return err
		}
	}

	// Join channels after receiving 001 (welcome).
	scanner := bufio.NewScanner(conn)
	joined := false

	// Start sender goroutine.
	go func() {
		pingTicker := time.NewTicker(pingInterval)
		defer pingTicker.Stop()
		for {
			select {
			case <-connDone:
				return
			case <-b.ctx.Done():
				conn.Close()
				return
			case <-pingTicker.C:
				if _, err := fmt.Fprintf(conn, "PING :tmi.twitch.tv\r\n"); err != nil {
					return
				}
			case msg := <-b.sendCh:
				if _, err := fmt.Fprintf(conn, "%s\r\n", msg); err != nil {
					return
				}
			}
		}
	}()

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PING") {
			fmt.Fprintf(conn, "PONG :tmi.twitch.tv\r\n")
			continue
		}
		// Join after welcome.
		if !joined && strings.Contains(line, "001") {
			for _, ch := range b.joinChannels {
				fmt.Fprintf(conn, "JOIN %s\r\n", ch)
			}
			joined = true
			log.Printf("twitch channel=%s joined %v", b.channelID, b.joinChannels)
			continue
		}
		b.handleLine(line)
	}
	return scanner.Err()
}

// handleLine parses a Twitch IRC PRIVMSG line.
// Example: @badge-info=...;display-name=User :user!user@user.tmi.twitch.tv PRIVMSG #channel :hello
func (b *twitchBot) handleLine(line string) {
	// Strip optional @tags prefix.
	tags := ""
	if strings.HasPrefix(line, "@") {
		idx := strings.Index(line, " ")
		if idx < 0 {
			return
		}
		tags = line[1:idx]
		line = line[idx+1:]
	}
	_ = tags

	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 {
		return
	}
	// parts[0] = :nick!nick@nick.tmi.twitch.tv
	// parts[1] = PRIVMSG
	// parts[2] = #channel
	// parts[3] = :text
	if parts[1] != "PRIVMSG" {
		return
	}

	nick := parts[0]
	if strings.HasPrefix(nick, ":") {
		nick = nick[1:]
	}
	if idx := strings.Index(nick, "!"); idx >= 0 {
		nick = nick[:idx]
	}
	nick = strings.ToLower(nick)

	// Skip own messages.
	if nick == b.nick {
		return
	}

	text := parts[3]
	if strings.HasPrefix(text, ":") {
		text = text[1:]
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if b.requireMention && !strings.Contains(strings.ToLower(text), "@"+b.nick) {
		return
	}

	if len(b.allowedSenders) > 0 && !b.allowedSenders[nick] {
		return
	}

	channel := parts[2] // e.g. #channelname
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  nick,
		Text:      text,
		ThreadID:  channel,
	})
}

// Send sends a PRIVMSG to all joined channels.
func (b *twitchBot) Send(ctx context.Context, text string) error {
	// Twitch limits messages to 500 chars; truncate gracefully.
	if len(text) > 500 {
		text = text[:497] + "…"
	}
	if len(b.joinChannels) == 0 {
		return fmt.Errorf("twitch: no channels joined")
	}
	// Send to the first channel by default; use ThreadID for multi-channel routing.
	target := b.joinChannels[0]
	if t := sdk.ChannelReplyTarget(ctx); t != "" {
		target = t
	}
	select {
	case b.sendCh <- fmt.Sprintf("PRIVMSG %s :%s", target, text):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return fmt.Errorf("twitch: send channel full")
	}
}
