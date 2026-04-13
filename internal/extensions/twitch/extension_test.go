package twitch

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &TwitchPlugin{}
	if id := p.ID(); id != "twitch" {
		t.Fatalf("expected twitch, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &TwitchPlugin{}
	if typ := p.Type(); typ != "Twitch" {
		t.Fatalf("expected Twitch, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &TwitchPlugin{}
	schema := p.ConfigSchema()
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"oauth_token", "nick", "channels"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &TwitchPlugin{}
	_ = p.Capabilities()
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &TwitchPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*TwitchPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &twitchBot{channelID: "tw-1"}
	if b.ID() != "tw-1" {
		t.Errorf("expected tw-1, got %s", b.ID())
	}
}

func TestHandleLine_PRIVMSG(t *testing.T) {
	var got sdk.InboundChannelMessage
	var mu sync.Mutex
	b := &twitchBot{
		channelID: "ch-1",
		nick:      "mybot",
		onMessage: func(msg sdk.InboundChannelMessage) {
			mu.Lock()
			got = msg
			mu.Unlock()
		},
	}

	b.handleLine(":someuser!someuser@someuser.tmi.twitch.tv PRIVMSG #mychannel :hello world")

	mu.Lock()
	defer mu.Unlock()
	if got.SenderID != "someuser" {
		t.Errorf("expected sender someuser, got %q", got.SenderID)
	}
	if got.Text != "hello world" {
		t.Errorf("expected text 'hello world', got %q", got.Text)
	}
	if got.ThreadID != "#mychannel" {
		t.Errorf("expected thread #mychannel, got %q", got.ThreadID)
	}
}

func TestHandleLine_WithTags(t *testing.T) {
	var got sdk.InboundChannelMessage
	b := &twitchBot{
		channelID: "ch-1",
		nick:      "mybot",
		onMessage: func(msg sdk.InboundChannelMessage) { got = msg },
	}

	b.handleLine("@badge-info=;display-name=User :user!user@user.tmi.twitch.tv PRIVMSG #ch :tagged message")

	if got.Text != "tagged message" {
		t.Errorf("expected 'tagged message', got %q", got.Text)
	}
}

func TestHandleLine_SkipsOwnMessages(t *testing.T) {
	called := false
	b := &twitchBot{
		channelID: "ch-1",
		nick:      "mybot",
		onMessage: func(msg sdk.InboundChannelMessage) { called = true },
	}

	b.handleLine(":mybot!mybot@mybot.tmi.twitch.tv PRIVMSG #ch :echo")
	if called {
		t.Error("should skip own messages")
	}
}

func TestHandleLine_RequireMention(t *testing.T) {
	called := false
	b := &twitchBot{
		channelID:      "ch-1",
		nick:           "mybot",
		requireMention: true,
		onMessage:      func(msg sdk.InboundChannelMessage) { called = true },
	}

	b.handleLine(":user!user@user.tmi.twitch.tv PRIVMSG #ch :no mention here")
	if called {
		t.Error("should skip message without mention")
	}

	b.handleLine(":user!user@user.tmi.twitch.tv PRIVMSG #ch :hey @mybot what's up")
	if !called {
		t.Error("should accept message with mention")
	}
}

func TestHandleLine_AllowedSenders(t *testing.T) {
	called := false
	b := &twitchBot{
		channelID:      "ch-1",
		nick:           "mybot",
		allowedSenders: map[string]bool{"vip": true},
		onMessage:      func(msg sdk.InboundChannelMessage) { called = true },
	}

	b.handleLine(":nobody!nobody@nobody.tmi.twitch.tv PRIVMSG #ch :hello")
	if called {
		t.Error("should filter out non-allowed sender")
	}

	b.handleLine(":vip!vip@vip.tmi.twitch.tv PRIVMSG #ch :hello")
	if !called {
		t.Error("should accept allowed sender")
	}
}

func TestHandleLine_NotPRIVMSG(t *testing.T) {
	called := false
	b := &twitchBot{
		channelID: "ch-1",
		nick:      "mybot",
		onMessage: func(msg sdk.InboundChannelMessage) { called = true },
	}

	b.handleLine(":tmi.twitch.tv 001 mybot :Welcome, GLHF!")
	if called {
		t.Error("should ignore non-PRIVMSG lines")
	}
}

func TestConnect_MissingOAuthToken(t *testing.T) {
	p := &TwitchPlugin{}
	_, err := p.Connect(context.Background(), "tw-1", map[string]any{
		"nick":     "bot",
		"channels": []interface{}{"#ch"},
	}, nil)
	if err == nil {
		t.Fatal("expected error when oauth_token is missing")
	}
}

func TestConnect_MissingNick(t *testing.T) {
	p := &TwitchPlugin{}
	_, err := p.Connect(context.Background(), "tw-1", map[string]any{
		"oauth_token": "tok",
		"channels":    []interface{}{"#ch"},
	}, nil)
	if err == nil {
		t.Fatal("expected error when nick is missing")
	}
}

func TestConnect_EmptyChannels(t *testing.T) {
	p := &TwitchPlugin{}
	_, err := p.Connect(context.Background(), "tw-1", map[string]any{
		"oauth_token": "tok",
		"nick":        "bot",
		"channels":    []interface{}{},
	}, nil)
	if err == nil {
		t.Fatal("expected error when channels is empty")
	}
}

func TestConnect_NormalizesChannelsAndAllowedSenders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &TwitchPlugin{}
	h, err := p.Connect(ctx, "tw-1", map[string]any{
		"oauth_token":     "oauth:tok",
		"nick":            "Bot",
		"channels":        []interface{}{"General", "#MixedCase"},
		"allowed_senders": []interface{}{"VIP", "Mod"},
		"require_mention": true,
	}, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer h.Close()

	bot, ok := h.(*twitchBot)
	if !ok {
		t.Fatalf("expected *twitchBot, got %T", h)
	}
	if bot.nick != "bot" {
		t.Fatalf("expected normalized nick, got %q", bot.nick)
	}
	if got, want := strings.Join(bot.joinChannels, ","), "#general,#mixedcase"; got != want {
		t.Fatalf("unexpected channels: got %q want %q", got, want)
	}
	if !bot.allowedSenders["vip"] || !bot.allowedSenders["mod"] {
		t.Fatalf("expected normalized allowed senders, got %#v", bot.allowedSenders)
	}
	if !bot.requireMention {
		t.Fatal("expected requireMention=true")
	}
}

func TestConnect_ValidConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &TwitchPlugin{}
	h, err := p.Connect(ctx, "tw-1", map[string]any{
		"oauth_token": "oauth:tok",
		"nick":        "bot",
		"channels":    []interface{}{"#general"},
	}, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.ID() != "tw-1" {
		t.Fatalf("expected tw-1, got %s", h.ID())
	}
	h.Close()
}

func TestConnect_UsesDialError(t *testing.T) {
	oldDial := twitchDialTLS
	twitchDialTLS = func(addr string, cfg *tls.Config) (net.Conn, error) {
		return nil, errors.New("boom")
	}
	defer func() { twitchDialTLS = oldDial }()

	b := &twitchBot{
		channelID:    "tw-1",
		oauthToken:   "oauth:tok",
		nick:         "bot",
		joinChannels: []string{"#general"},
		onMessage:    func(sdk.InboundChannelMessage) {},
		ctx:          context.Background(),
		sendCh:       make(chan string, 1),
	}

	err := b.connect()
	if err == nil || !strings.Contains(err.Error(), "dial: boom") {
		t.Fatalf("expected wrapped dial error, got %v", err)
	}
}

func TestConnect_HandshakeJoinPingSendAndReceive(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	oldDial := twitchDialTLS
	twitchDialTLS = func(addr string, cfg *tls.Config) (net.Conn, error) {
		if addr != twitchIRCAddr {
			t.Fatalf("unexpected addr %q", addr)
		}
		if cfg == nil || cfg.ServerName != "irc-ws.chat.twitch.tv" {
			t.Fatalf("unexpected tls config: %#v", cfg)
		}
		return clientConn, nil
	}
	defer func() { twitchDialTLS = oldDial }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgCh := make(chan sdk.InboundChannelMessage, 1)
	b := &twitchBot{
		channelID:    "tw-1",
		oauthToken:   "oauth:tok",
		nick:         "bot",
		joinChannels: []string{"#general", "#random"},
		onMessage:    func(msg sdk.InboundChannelMessage) { msgCh <- msg },
		ctx:          ctx,
		sendCh:       make(chan string, 4),
	}

	errCh := make(chan error, 1)
	go func() { errCh <- b.connect() }()

	reader := bufio.NewReader(serverConn)
	readLine := func(want string) {
		t.Helper()
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read line: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line != want {
			t.Fatalf("unexpected line: got %q want %q", line, want)
		}
	}

	readLine("CAP REQ :twitch.tv/tags twitch.tv/commands")
	readLine("PASS oauth:tok")
	readLine("NICK bot")

	if _, err := io.WriteString(serverConn, ":tmi.twitch.tv 001 bot :Welcome, GLHF!\r\n"); err != nil {
		t.Fatalf("write welcome: %v", err)
	}
	readLine("JOIN #general")
	readLine("JOIN #random")

	if _, err := io.WriteString(serverConn, "PING :tmi.twitch.tv\r\n"); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	readLine("PONG :tmi.twitch.tv")

	if err := b.Send(context.Background(), "hello from bot"); err != nil {
		t.Fatalf("send: %v", err)
	}
	readLine("PRIVMSG #general :hello from bot")

	if _, err := io.WriteString(serverConn, ":viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #general :hello back\r\n"); err != nil {
		t.Fatalf("write privmsg: %v", err)
	}

	select {
	case msg := <-msgCh:
		if msg.ChannelID != "tw-1" || msg.SenderID != "viewer" || msg.ThreadID != "#general" || msg.Text != "hello back" {
			t.Fatalf("unexpected inbound message: %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound message")
	}

	cancel()
	_ = serverConn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	_ = serverConn.Close()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("unexpected connect error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect to exit")
	}
}

func TestClose_Idempotent(t *testing.T) {
	b := &twitchBot{channelID: "tw-x"}
	b.Close()
	b.Close()
}

func TestSend_PushesToSendCh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &twitchBot{
		channelID:    "tw-1",
		joinChannels: []string{"#general"},
		sendCh:       make(chan string, 64),
	}
	if err := b.Send(ctx, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	select {
	case msg := <-b.sendCh:
		if msg != "PRIVMSG #general :hello" {
			t.Fatalf("unexpected message: %q", msg)
		}
	default:
		t.Fatal("expected message in sendCh")
	}
}

func TestSend_UsesReplyTarget(t *testing.T) {
	b := &twitchBot{
		channelID:    "tw-1",
		joinChannels: []string{"#general"},
		sendCh:       make(chan string, 1),
	}
	ctx := sdk.WithChannelReplyTarget(context.Background(), "#support")
	if err := b.Send(ctx, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := <-b.sendCh; got != "PRIVMSG #support :hello" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestSend_TruncatesLongMessages(t *testing.T) {
	b := &twitchBot{
		channelID:    "tw-1",
		joinChannels: []string{"#general"},
		sendCh:       make(chan string, 1),
	}
	longText := strings.Repeat("a", 501)
	if err := b.Send(context.Background(), longText); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := <-b.sendCh
	prefix := "PRIVMSG #general :"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("unexpected message: %q", got)
	}
	body := strings.TrimPrefix(got, prefix)
	if len(body) != 500 {
		t.Fatalf("expected truncated body length 500, got %d", len(body))
	}
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", body[len(body)-4:])
	}
}

func TestSend_NoChannels(t *testing.T) {
	b := &twitchBot{channelID: "tw-1", sendCh: make(chan string, 1)}
	if err := b.Send(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "no channels joined") {
		t.Fatalf("expected no channels error, got %v", err)
	}
}

func TestSend_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &twitchBot{channelID: "tw-1", joinChannels: []string{"#general"}, sendCh: make(chan string)}
	if err := b.Send(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestHandleLine_EmptyText(t *testing.T) {
	called := false
	b := &twitchBot{
		channelID: "ch-1",
		nick:      "mybot",
		onMessage: func(msg sdk.InboundChannelMessage) { called = true },
	}

	b.handleLine(":user!user@user.tmi.twitch.tv PRIVMSG #ch :")
	if called {
		t.Error("should skip empty text")
	}
}
