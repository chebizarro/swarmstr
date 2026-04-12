package discord

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestPlugin_ID(t *testing.T) {
	p := &DiscordPlugin{}
	if id := p.ID(); id == "" {
		t.Fatal("ID must not be empty")
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &DiscordPlugin{}
	if typ := p.Type(); typ == "" {
		t.Fatal("Type must not be empty")
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &DiscordPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
}

func TestPlugin_ConfigSchemaNoUnusedFields(t *testing.T) {
	p := &DiscordPlugin{}
	schema := p.ConfigSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map in schema")
	}
	allowed := map[string]bool{"bot_token": true, "channel_id": true}
	for key := range props {
		if !allowed[key] {
			t.Errorf("ConfigSchema exposes %q which is not used by Connect/poll/send — remove it or implement support", key)
		}
	}
}

func TestPlugin_ConnectIgnoresUnknownConfigKeys(t *testing.T) {
	p := &DiscordPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := map[string]any{
		"bot_token":  "Bot test-token",
		"channel_id": "123",
		"guild_id":   "should-be-ignored",
	}
	handle, err := p.Connect(ctx, "test-ch", cfg, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("Connect with extra config keys should not fail: %v", err)
	}
	handle.Close()
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &DiscordPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions {
		t.Error("expected Reactions capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &DiscordPlugin{}
	methods := p.GatewayMethods()
	if methods == nil {
		t.Fatal("GatewayMethods must not be nil")
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*DiscordPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &discordBot{channelID: "disc-1"}
	if b.ID() != "disc-1" {
		t.Errorf("expected disc-1, got %s", b.ID())
	}
}

func TestIsDiscordThreadType(t *testing.T) {
	for _, kind := range []int{10, 11, 12} {
		if !isDiscordThreadType(kind) {
			t.Errorf("expected kind %d to be thread type", kind)
		}
	}
	for _, kind := range []int{0, 1, 5, 13, 100} {
		if isDiscordThreadType(kind) {
			t.Errorf("expected kind %d to NOT be thread type", kind)
		}
	}
}

func TestBotClient_Default(t *testing.T) {
	b := &discordBot{}
	c := b.client(0)
	if c == nil {
		t.Fatal("client should not be nil")
	}
}

func TestBotClient_Custom(t *testing.T) {
	custom := &http.Client{}
	b := &discordBot{httpClient: custom}
	if b.client(0) != custom {
		t.Error("expected custom client")
	}
}

func TestDiscordFetchMessages_PopulatesReplyAndThreadMetadata(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &discordBot{
		channelID:        "discord-main",
		token:            "Bot test-token",
		discordChannelID: "thread-123",
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done: make(chan struct{}),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.URL.Path == "/api/v10/channels/thread-123":
				return jsonResponse(req, `{"id":"thread-123","type":11}`), nil
			case strings.HasPrefix(req.URL.Path, "/api/v10/channels/thread-123/messages"):
				return jsonResponse(req, `[
					{
						"id":"msg-2",
						"content":"thread reply",
						"timestamp":"2026-04-05T09:00:00Z",
						"message_reference":{"message_id":"msg-1"},
						"author":{"id":"user-1","username":"alice","bot":false}
					}
				]`), nil
			default:
				t.Fatalf("unexpected path: %s", req.URL.Path)
				return nil, nil
			}
		})},
	}

	bot.fetchMessages(context.Background())

	if len(delivered) != 1 {
		t.Fatalf("expected 1 delivered message, got %d", len(delivered))
	}
	if delivered[0].ThreadID != "thread-123" {
		t.Fatalf("expected thread channel id as ThreadID, got %+v", delivered[0])
	}
	if delivered[0].ReplyToEventID != "discord-msg-1" {
		t.Fatalf("expected reply metadata, got %+v", delivered[0])
	}
	if delivered[0].SenderID != "alice#user-1" {
		t.Fatalf("unexpected sender id: %+v", delivered[0])
	}
}
