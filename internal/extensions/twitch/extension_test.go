package twitch

import (
	"context"
	"sync"
	"testing"

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
	_ = p.Capabilities() // just ensure no panic
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

	// Without mention — should be skipped.
	b.handleLine(":user!user@user.tmi.twitch.tv PRIVMSG #ch :no mention here")
	if called {
		t.Error("should skip message without mention")
	}

	// With mention — should pass.
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

func TestConnect_ValidConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately to prevent TCP connection
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

func TestClose_Idempotent(t *testing.T) {
	b := &twitchBot{channelID: "tw-x"}
	b.Close()
	b.Close() // second call should not panic
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
