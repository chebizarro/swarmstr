package bluebubbles

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &BlueBubblesPlugin{}
	if id := p.ID(); id == "" {
		t.Fatal("ID must not be empty")
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &BlueBubblesPlugin{}
	if typ := p.Type(); typ == "" {
		t.Fatal("Type must not be empty")
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &BlueBubblesPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"server_url", "password", "chat_guid"} {
		if _, ok := props[key]; !ok {
			t.Errorf("ConfigSchema missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &BlueBubblesPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions {
		t.Error("expected Reactions capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &BlueBubblesPlugin{}
	methods := p.GatewayMethods()
	if methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*BlueBubblesPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &bbBot{channelID: "test-ch"}
	if b.ID() != "test-ch" {
		t.Errorf("expected test-ch, got %s", b.ID())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func errorResponse(code int) *http.Response {
	return &http.Response{
		StatusCode: code,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestConnect_MissingRequiredConfig(t *testing.T) {
	p := &BlueBubblesPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []map[string]any{
		{"password": "pw", "chat_guid": "guid"},
		{"server_url": "http://x", "chat_guid": "guid"},
		{"server_url": "http://x", "password": "pw"},
		{},
	}
	for _, cfg := range cases {
		_, err := p.Connect(ctx, "ch", cfg, func(sdk.InboundChannelMessage) {})
		if err == nil {
			t.Errorf("expected error for config %v", cfg)
		}
	}
}

func TestConnect_ValidConfig(t *testing.T) {
	p := &BlueBubblesPlugin{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := map[string]any{
		"server_url": "http://localhost:1234",
		"password":   "secret",
		"chat_guid":  "iMessage;-;+1234",
	}
	handle, err := p.Connect(ctx, "test-ch", cfg, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handle.Close()
}

func TestPoll_DeliversNewMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &bbBot{
		channelID:      "bb-ch",
		serverURL:      "http://bb.test",
		password:       "pw",
		chatGUID:       "chat-guid",
		allowedSenders: map[string]bool{},
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done:      make(chan struct{}),
		seenGUIDs: map[string]struct{}{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"status":200,"data":[
				{"guid":"msg-2","text":"second","isFromMe":false,"handle":{"address":"+1111"},"dateCreated":2000000},
				{"guid":"msg-1","text":"first","isFromMe":false,"handle":{"address":"+1111"},"dateCreated":1000000}
			]}`), nil
		})},
	}

	err := bot.poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(delivered) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(delivered))
	}
	if delivered[0].Text != "first" || delivered[1].Text != "second" {
		t.Fatalf("messages not in chronological order: %+v", delivered)
	}
	if delivered[0].EventID != "msg-1" {
		t.Fatalf("expected eventID msg-1, got %s", delivered[0].EventID)
	}
}

func TestPoll_SkipsBotMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &bbBot{
		channelID:      "bb-ch",
		serverURL:      "http://bb.test",
		password:       "pw",
		chatGUID:       "chat-guid",
		allowedSenders: map[string]bool{},
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done:      make(chan struct{}),
		seenGUIDs: map[string]struct{}{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"status":200,"data":[
				{"guid":"msg-1","text":"from me","isFromMe":true,"handle":{"address":"+1111"},"dateCreated":1000000},
				{"guid":"msg-2","text":"from them","isFromMe":false,"handle":{"address":"+2222"},"dateCreated":2000000}
			]}`), nil
		})},
	}

	bot.poll(context.Background())
	if len(delivered) != 1 || delivered[0].Text != "from them" {
		t.Fatalf("expected only non-bot message, got %+v", delivered)
	}
}

func TestPoll_SkipsAlreadySeenMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &bbBot{
		channelID:      "bb-ch",
		serverURL:      "http://bb.test",
		password:       "pw",
		chatGUID:       "chat-guid",
		allowedSenders: map[string]bool{},
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done:      make(chan struct{}),
		seenGUIDs: map[string]struct{}{"msg-1": {}},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"status":200,"data":[
				{"guid":"msg-1","text":"old","isFromMe":false,"handle":{"address":"+1111"},"dateCreated":1000000}
			]}`), nil
		})},
	}

	bot.poll(context.Background())
	if len(delivered) != 0 {
		t.Fatalf("expected 0 messages (already seen), got %d", len(delivered))
	}
}

func TestPoll_AllowedSendersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &bbBot{
		channelID:      "bb-ch",
		serverURL:      "http://bb.test",
		password:       "pw",
		chatGUID:       "chat-guid",
		allowedSenders: map[string]bool{"+1111": true},
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done:      make(chan struct{}),
		seenGUIDs: map[string]struct{}{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"status":200,"data":[
				{"guid":"msg-1","text":"allowed","isFromMe":false,"handle":{"address":"+1111"},"dateCreated":1000000},
				{"guid":"msg-2","text":"blocked","isFromMe":false,"handle":{"address":"+9999"},"dateCreated":2000000}
			]}`), nil
		})},
	}

	bot.poll(context.Background())
	if len(delivered) != 1 || delivered[0].Text != "allowed" {
		t.Fatalf("expected only allowed sender, got %+v", delivered)
	}
}

func TestPoll_SkipsEmptyText(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &bbBot{
		channelID:      "bb-ch",
		serverURL:      "http://bb.test",
		password:       "pw",
		chatGUID:       "chat-guid",
		allowedSenders: map[string]bool{},
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done:      make(chan struct{}),
		seenGUIDs: map[string]struct{}{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"status":200,"data":[
				{"guid":"msg-1","text":"","isFromMe":false,"handle":{"address":"+1111"},"dateCreated":1000000},
				{"guid":"msg-2","text":"  ","isFromMe":false,"handle":{"address":"+1111"},"dateCreated":2000000}
			]}`), nil
		})},
	}

	bot.poll(context.Background())
	if len(delivered) != 0 {
		t.Fatalf("expected 0 messages (empty text), got %d", len(delivered))
	}
}

func TestSend_PostsToAPI(t *testing.T) {
	var capturedURL string
	var capturedBody []byte
	bot := &bbBot{
		serverURL: "http://bb.test",
		password:  "pw",
		chatGUID:  "chat-guid",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			capturedBody, _ = io.ReadAll(req.Body)
			return jsonResponse(`{"status":200}`), nil
		})},
	}

	err := bot.Send(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(capturedURL, "/api/v1/message/text") {
		t.Fatalf("unexpected URL: %s", capturedURL)
	}
	if !strings.Contains(string(capturedBody), `"message":"hello"`) {
		t.Fatalf("unexpected body: %s", capturedBody)
	}
}

func TestSend_ReturnsErrorOnHTTPFailure(t *testing.T) {
	bot := &bbBot{
		serverURL: "http://bb.test",
		password:  "pw",
		chatGUID:  "chat-guid",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return errorResponse(500), nil
		})},
	}
	err := bot.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestAddReaction_PostsToAPI(t *testing.T) {
	var capturedURL string
	bot := &bbBot{
		serverURL: "http://bb.test",
		password:  "pw",
		chatGUID:  "chat-guid",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return jsonResponse(`{"status":200}`), nil
		})},
	}

	err := bot.AddReaction(context.Background(), "msg-guid-1", "love")
	if err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if !strings.Contains(capturedURL, "/api/v1/message/react") {
		t.Fatalf("unexpected URL: %s", capturedURL)
	}
}

func TestBotClose_Idempotent(t *testing.T) {
	bot := &bbBot{done: make(chan struct{})}
	bot.Close()
	bot.Close() // should not panic
}

func TestFetchMessages_HandlesHTTPError(t *testing.T) {
	bot := &bbBot{
		serverURL: "http://bb.test",
		password:  "pw",
		chatGUID:  "chat-guid",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return errorResponse(500), nil
		})},
	}
	_, err := bot.fetchMessages(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}
