// Package irc implements an IRC channel extension for swarmstr.
//
// Registration: import _ "swarmstr/internal/extensions/irc" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "host":            "irc.libera.chat",   // required
//	  "port":            6697,                // default 6697 (TLS) or 6667 (plain)
//	  "tls":             true,                // default true
//	  "nick":            "swarmbot",          // required
//	  "username":        "swarmbot",          // defaults to nick
//	  "realname":        "Swarmstr Agent",    // defaults to nick
//	  "password":        "",                  // NickServ password (optional)
//	  "channels":        ["#general"],        // required: channels to join
//	  "allowed_senders": [],                  // optional: allowlist of nicks
//	  "poll_interval_s": 0                    // unused (event-driven)
//	}
//
// To add an IRC channel to your swarmstr config:
//
//	"nostr_channels": {
//	  "libera-general": {
//	    "kind": "irc",
//	    "channel_id": "#general",
//	    "config": {
//	      "host": "irc.libera.chat",
//	      "nick": "mybot",
//	      "channels": ["#general"],
//	      "password": "s3cr3t"
//	    }
//	  }
//	}
package irc

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

func init() {
	channels.RegisterChannelPlugin(&IRCPlugin{})
}

// IRCPlugin is the factory for IRC channel instances.
type IRCPlugin struct{}

func (p *IRCPlugin) ID() string   { return "irc" }
func (p *IRCPlugin) Type() string { return "IRC" }

func (p *IRCPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"host":            map[string]any{"type": "string", "description": "IRC server hostname."},
			"port":            map[string]any{"type": "integer", "description": "IRC server port. Default 6697 (TLS) or 6667 (plain)."},
			"tls":             map[string]any{"type": "boolean", "description": "Use TLS. Default true."},
			"nick":            map[string]any{"type": "string", "description": "Bot nick name."},
			"username":        map[string]any{"type": "string", "description": "IRC username (ident). Defaults to nick."},
			"realname":        map[string]any{"type": "string", "description": "IRC realname. Defaults to nick."},
			"password":        map[string]any{"type": "string", "description": "NickServ password for identification."},
			"channels":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "IRC channels to join on connect."},
			"allowed_senders": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional nick allowlist."},
		},
		"required": []string{"host", "nick", "channels"},
	}
}

func (p *IRCPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{MultiAccount: true}
}

func (p *IRCPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	host, _ := cfg["host"].(string)
	nick, _ := cfg["nick"].(string)
	if host == "" || nick == "" {
		return nil, fmt.Errorf("irc channel %q: host and nick are required", channelID)
	}

	useTLS := true
	if v, ok := cfg["tls"].(bool); ok {
		useTLS = v
	}

	defaultPort := 6697
	if !useTLS {
		defaultPort = 6667
	}
	port := defaultPort
	switch v := cfg["port"].(type) {
	case float64:
		port = int(v)
	case int:
		port = v
	}

	username, _ := cfg["username"].(string)
	if username == "" {
		username = nick
	}
	realname, _ := cfg["realname"].(string)
	if realname == "" {
		realname = nick
	}
	password, _ := cfg["password"].(string)

	var ircChannels []string
	switch v := cfg["channels"].(type) {
	case []interface{}:
		for _, ch := range v {
			if s, ok := ch.(string); ok && s != "" {
				ircChannels = append(ircChannels, s)
			}
		}
	case []string:
		ircChannels = v
	}
	if len(ircChannels) == 0 {
		return nil, fmt.Errorf("irc channel %q: at least one channel to join is required", channelID)
	}

	allowedSenders := map[string]bool{}
	switch v := cfg["allowed_senders"].(type) {
	case []interface{}:
		for _, s := range v {
			if nick, ok := s.(string); ok && nick != "" {
				allowedSenders[strings.ToLower(nick)] = true
			}
		}
	}

	bot := &ircBot{
		channelID:      channelID,
		host:           host,
		port:           port,
		useTLS:         useTLS,
		nick:           nick,
		username:       username,
		realname:       realname,
		password:       password,
		ircChannels:    ircChannels,
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		done:           make(chan struct{}),
	}

	if err := bot.connect(ctx); err != nil {
		return nil, fmt.Errorf("irc channel %q: connect: %w", channelID, err)
	}

	go bot.readLoop(ctx)
	log.Printf("irc: connected to %s:%d as %s, joined %v", host, port, nick, ircChannels)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

const ircMaxLineLen = 450

type ircBot struct {
	mu             sync.Mutex
	channelID      string
	host           string
	port           int
	useTLS         bool
	nick           string
	username       string
	realname       string
	password       string
	ircChannels    []string
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	conn           net.Conn
	writer         *bufio.Writer
	done           chan struct{}
}

func (b *ircBot) ID() string { return b.channelID }

func (b *ircBot) connect(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", b.host, b.port)
	var conn net.Conn
	var err error
	if b.useTLS {
		dialer := &tls.Dialer{Config: &tls.Config{ServerName: b.host}}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	} else {
		d := &net.Dialer{}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.conn = conn
	b.writer = bufio.NewWriter(conn)
	b.mu.Unlock()

	// Register with the server.
	if b.password != "" {
		b.send("PASS " + b.password)
	}
	b.send(fmt.Sprintf("NICK %s", b.nick))
	b.send(fmt.Sprintf("USER %s 0 * :%s", b.username, b.realname))
	return nil
}

func (b *ircBot) send(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writer == nil {
		return
	}
	_, _ = fmt.Fprintf(b.writer, "%s\r\n", line)
	_ = b.writer.Flush()
}

func (b *ircBot) Send(ctx context.Context, text string) error {
	// Use channel_id as the IRC target (e.g. "#general").
	// Determine target from the first configured IRC channel.
	target := b.channelID
	if len(b.ircChannels) > 0 {
		target = b.ircChannels[0]
	}
	return b.sendPrivmsg(target, text)
}

func (b *ircBot) sendPrivmsg(target, text string) error {
	prefix := fmt.Sprintf("PRIVMSG %s :", target)
	maxBody := ircMaxLineLen - len(prefix)
	for len(text) > 0 {
		chunk := text
		if len(chunk) > maxBody {
			// Split at a space boundary if possible.
			split := maxBody
			for split > 0 && text[split] != ' ' {
				split--
			}
			if split == 0 {
				split = maxBody
			}
			chunk = text[:split]
			text = strings.TrimSpace(text[split:])
		} else {
			text = ""
		}
		b.send(prefix + chunk)
	}
	return nil
}

func (b *ircBot) Close() {
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

func (b *ircBot) readLoop(ctx context.Context) {
	defer b.Close()
	scanner := bufio.NewScanner(b.conn)
	joined := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		default:
		}
		_ = b.conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		if !scanner.Scan() {
			if ctx.Err() == nil {
				log.Printf("irc: connection lost for channel %s: %v", b.channelID, scanner.Err())
			}
			return
		}
		line := scanner.Text()
		b.handleLine(line, &joined)
	}
}

// parsePrefix extracts the nick from a ":nick!user@host" prefix.
func parsePrefix(prefix string) string {
	prefix = strings.TrimPrefix(prefix, ":")
	if idx := strings.Index(prefix, "!"); idx >= 0 {
		return prefix[:idx]
	}
	return prefix
}

func (b *ircBot) handleLine(line string, joined *bool) {
	if strings.HasPrefix(line, "PING") {
		b.send("PONG" + strings.TrimPrefix(line, "PING"))
		return
	}

	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 2 {
		return
	}

	// Numeric 001 (RPL_WELCOME) — server accepted our registration.
	if len(parts) >= 2 && parts[1] == "001" && !*joined {
		*joined = true
		// Authenticate with NickServ if password set (SASL is not used).
		// The password was already sent as PASS, so NickServ may already be handled.
		// Join configured channels.
		for _, ch := range b.ircChannels {
			b.send("JOIN " + ch)
		}
		return
	}

	// PRIVMSG handling.
	if len(parts) >= 4 && parts[1] == "PRIVMSG" {
		senderNick := parsePrefix(parts[0])
		target := parts[2]
		text := strings.TrimPrefix(parts[3], ":")

		// Skip our own messages.
		if strings.EqualFold(senderNick, b.nick) {
			return
		}

		// Allowlist check.
		if len(b.allowedSenders) > 0 && !b.allowedSenders[strings.ToLower(senderNick)] {
			return
		}

		// Determine the swarmstr channel_id: use the IRC channel name if target
		// is a channel, or the sender's nick for direct messages.
		msgChannelID := b.channelID
		if !strings.HasPrefix(target, "#") && !strings.HasPrefix(target, "&") {
			msgChannelID = "irc-dm:" + senderNick
		}

		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: msgChannelID,
			SenderID:  senderNick,
			Text:      text,
		})
	}
}
