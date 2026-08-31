package signal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

// ── Plugin metadata ───────────────────────────────────────────────────────────

func TestSignalPlugin_ID(t *testing.T) {
	p := &SignalPlugin{}
	if p.ID() != "signal" {
		t.Fatalf("expected id='signal', got %q", p.ID())
	}
}

func TestSignalPlugin_Capabilities(t *testing.T) {
	p := &SignalPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions || !caps.Media || !caps.DirectTextMedia || !caps.MultiAccount {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestSignalPlugin_ConfigSchema(t *testing.T) {
	p := &SignalPlugin{}
	schema := p.ConfigSchema()
	required, _ := schema["required"].([]string)
	set := map[string]bool{}
	for _, r := range required {
		set[r] = true
	}
	for _, f := range []string{"api_url", "account"} {
		if !set[f] {
			t.Fatalf("expected %q in required fields", f)
		}
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["allow_polling"]; !ok {
		t.Fatal("expected allow_polling opt-in property")
	}
}

func TestSignalPlugin_Connect_MissingConfig(t *testing.T) {
	p := &SignalPlugin{}
	tests := []struct {
		name string
		cfg  map[string]any
	}{
		{"missing api_url", map[string]any{"account": "+1555"}},
		{"missing account", map[string]any{"api_url": "http://localhost:8080"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Connect(context.Background(), "c1", tc.cfg, func(sdk.InboundChannelMessage) {})
			if err == nil {
				t.Fatal("expected error for missing config")
			}
		})
	}
}

// ── test bot helper ───────────────────────────────────────────────────────────

func newTestSignalServer(handler http.Handler) (*httptest.Server, *signalBot) {
	srv := httptest.NewServer(handler)
	bot := &signalBot{
		channelID:      "+15559876543",
		apiURL:         srv.URL,
		account:        "+15551234567",
		defaultTo:      "+15559876543",
		reactionLevel:  "minimal",
		allowedSenders: map[string]bool{},
		httpClient:     srv.Client(),
		done:           make(chan struct{}),
		routesByID:     map[string]signalReactionRoute{},
		routesByTarget: map[string]string{},
		activeTyping:   map[string]bool{},
	}
	return srv, bot
}

// ── receive / polling ─────────────────────────────────────────────────────────

func TestReceive_JSONArray(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	rawEnvelopes := `[{"envelope":{"source":"+15559876543","timestamp":1000,"dataMessage":{"message":"hello signal","timestamp":1000}}}]`

	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/v1/receive/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(rawEnvelopes))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.receive(context.Background())

	if len(delivered) != 1 {
		t.Fatalf("expected 1 message, got %d", len(delivered))
	}
	if delivered[0].Text != "hello signal" {
		t.Fatalf("unexpected text: %q", delivered[0].Text)
	}
	if delivered[0].SenderID != "+15559876543" {
		t.Fatalf("unexpected sender: %q", delivered[0].SenderID)
	}
}

func TestReceive_NDJSON(t *testing.T) {
	var delivered []sdk.InboundChannelMessage

	line1 := `{"envelope":{"source":"+111","timestamp":1,"dataMessage":{"message":"msg1","timestamp":1}}}`
	line2 := `{"envelope":{"source":"+222","timestamp":2,"dataMessage":{"message":"msg2","timestamp":2}}}`

	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(line1 + "\n" + line2 + "\n"))
	}))
	defer srv.Close()

	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.receive(context.Background())

	if len(delivered) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(delivered))
	}
}

func TestReceive_SkipsEnvelopesWithoutDataMessage(t *testing.T) {
	var delivered []sdk.InboundChannelMessage

	// An envelope with no dataMessage (e.g. receipt).
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := `[{"envelope":{"source":"+111","timestamp":1}}]`
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(raw))
	}))
	defer srv.Close()

	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.receive(context.Background())

	if len(delivered) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(delivered))
	}
}

func TestReceive_AllowedSendersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage

	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := `[
			{"envelope":{"source":"+allowed","timestamp":1,"dataMessage":{"message":"ok","timestamp":1}}},
			{"envelope":{"source":"+blocked","timestamp":2,"dataMessage":{"message":"no","timestamp":2}}}
		]`
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(raw))
	}))
	defer srv.Close()

	bot.allowedSenders = map[string]bool{"+allowed": true}
	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.receive(context.Background())

	if len(delivered) != 1 || delivered[0].SenderID != "+allowed" {
		t.Fatalf("expected only +allowed, got %+v", delivered)
	}
}

func TestReceive_AttachmentMetadata(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/v1/receive/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"envelope":{"source":"+111","timestamp":3,"dataMessage":{"message":"photo","timestamp":3,"attachments":[{"id":"att-1","contentType":"image/png"}]}}}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	if err := bot.receive(context.Background()); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if len(delivered) != 1 || delivered[0].MediaURL != "signal://attachment/att-1" || delivered[0].MediaMIME != "image/png" {
		t.Fatalf("expected media metadata, got %+v", delivered)
	}
}

func TestResolveMedia_DownloadsAttachment(t *testing.T) {
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/attachments/att-1" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-data"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	blob, err := bot.ResolveMedia(context.Background(), "signal://attachment/att-1")
	if err != nil {
		t.Fatalf("ResolveMedia: %v", err)
	}
	if blob.MIME != "image/png" || string(blob.Data) != "png-data" {
		t.Fatalf("unexpected blob: %+v", blob)
	}
}

func TestReceive_SidecarUnreachable(t *testing.T) {
	bot := &signalBot{
		channelID:  "c",
		apiURL:     "http://127.0.0.1:1", // nothing listening
		account:    "+1",
		httpClient: &http.Client{},
		done:       make(chan struct{}),
		onMessage:  func(sdk.InboundChannelMessage) {},
	}
	// Should not panic.
	bot.receive(context.Background())
}

// ── Send ──────────────────────────────────────────────────────────────────────

func TestSend_Success(t *testing.T) {
	var received map[string]any
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v2/send" {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"timestamp":9999}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["message"] != "hello" {
		t.Fatalf("expected message='hello', got %v", received["message"])
	}
	recipients, _ := received["recipients"].([]interface{})
	if len(recipients) == 0 {
		t.Fatal("expected at least one recipient")
	}
}

func TestSendMedia_Base64Attachment(t *testing.T) {
	var received signalSendRequest
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v2/send" {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"timestamp":9999}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To: "+15550001111", Text: "Photo", Media: []sdk.MediaPayloadInput{{Path: path, ContentType: "image/jpeg"}},
	}); err != nil {
		t.Fatal(err)
	}
	if received.Message != "Photo" || len(received.Recipients) != 1 || received.Recipients[0] != "+15550001111" {
		t.Fatalf("unexpected send payload: %+v", received)
	}
	want := "data:image/jpeg;filename=photo.jpg;base64,aW1hZ2U="
	if len(received.Base64Attachments) != 1 || received.Base64Attachments[0] != want {
		t.Fatalf("attachments=%q want %q", received.Base64Attachments, want)
	}
	if _, ok := any(bot).(sdk.MediaHandle); !ok {
		t.Fatal("signal handle does not implement sdk.MediaHandle")
	}
	if err := sdk.ValidateChannelCapabilityContract((&SignalPlugin{}).Capabilities(), bot); err != nil {
		t.Fatalf("capability contract: %v", err)
	}
}

func TestSendMedia_RejectsRemoteURL(t *testing.T) {
	bot := &signalBot{channelID: "signal-test"}
	err := bot.SendMedia(context.Background(), sdk.DirectTextMediaPayload{
		To: "+15550001111", Media: []sdk.MediaPayloadInput{{Path: "https://example.test/photo.jpg", ContentType: "image/jpeg"}},
	})
	if err == nil || !strings.Contains(err.Error(), "stage the file locally") {
		t.Fatalf("expected remote media rejection, got %v", err)
	}
}

func TestSend_Error(t *testing.T) {
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"not registered"}`))
	}))
	defer srv.Close()

	err := bot.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error from 500 response")
	}
}

// ── AddReaction ───────────────────────────────────────────────────────────────

func TestAddReaction_Success(t *testing.T) {
	var received map[string]any
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/v1/react/") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.AddReaction(context.Background(), "signal-+sender-1000", "👍"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["reaction"] != "👍" {
		t.Fatalf("expected reaction=👍, got %v", received["reaction"])
	}
	if received["remove"] != nil && received["remove"] == true {
		t.Fatal("expected remove to be absent or false for AddReaction")
	}
}

func TestAddReaction_InvalidEventID(t *testing.T) {
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := bot.AddReaction(context.Background(), "bad-format", "👍")
	if err == nil {
		t.Fatal("expected error for invalid eventID")
	}
}

func TestRemoveReaction_Success(t *testing.T) {
	var received map[string]any
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/v1/react/") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.RemoveReaction(context.Background(), "signal-+sender-2000", "👎"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["remove"] != true {
		t.Fatalf("expected remove=true, got %v", received["remove"])
	}
}

// ── EventID encoding ──────────────────────────────────────────────────────────

func TestEventIDFormat(t *testing.T) {
	// Verify that receive produces event IDs parseable by AddReaction.
	var delivered []sdk.InboundChannelMessage

	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := `[{"envelope":{"source":"+111","timestamp":42000,"dataMessage":{"message":"hi","timestamp":42000}}}]`
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(raw))
	}))
	defer srv.Close()

	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.receive(context.Background())

	if len(delivered) != 1 {
		t.Fatalf("expected 1 message, got %d", len(delivered))
	}
	eventID := delivered[0].EventID
	if !strings.HasPrefix(eventID, "signal-") {
		t.Fatalf("unexpected event ID format: %q", eventID)
	}
	// AddReaction must be able to parse it without error (we just check no panic/error).
	var reactCalled bool
	reactionSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reactCalled = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer reactionSrv.Close()
	bot.apiURL = reactionSrv.URL
	bot.httpClient = reactionSrv.Client()
	if err := bot.AddReaction(context.Background(), eventID, "❤️"); err != nil {
		t.Fatalf("AddReaction failed for eventID %q: %v", eventID, err)
	}
	if !reactCalled {
		t.Fatal("expected react endpoint to be called")
	}
}

func TestSignalAdvancedActionSurface(t *testing.T) {
	p := &SignalPlugin{}
	props, _ := p.ConfigSchema()["properties"].(map[string]any)
	for _, key := range []string{"default_to", "reaction_level"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("config schema missing %q", key)
		}
	}
	want := []string{"signal.send", "signal.send_question", "signal.send_approval", "signal.remove_reaction_route"}
	methods := p.GatewayMethods()
	if len(methods) != len(want) {
		t.Fatalf("expected %d methods, got %d", len(want), len(methods))
	}
	for i := range want {
		if methods[i].Method != want[i] {
			t.Fatalf("method %d: want %q, got %q", i, want[i], methods[i].Method)
		}
	}
}

func TestSignalRouteChoices(t *testing.T) {
	approval, err := signalRouteChoices("approval", map[string]any{})
	if err != nil || approval["✅"] != "approve" || approval["❌"] != "deny" {
		t.Fatalf("unexpected default approval route: %#v, %v", approval, err)
	}
	question, err := signalRouteChoices("question", map[string]any{"choices": []any{
		map[string]any{"emoji": "1️⃣", "value": "one"},
		map[string]any{"emoji": "2️⃣", "value": "two"},
	}})
	if err != nil || question["2️⃣"] != "two" {
		t.Fatalf("unexpected question route: %#v, %v", question, err)
	}
	if _, err := signalRouteChoices("question", map[string]any{"choices": []any{
		map[string]any{"emoji": "1️⃣", "value": "one"},
		map[string]any{"emoji": "1️⃣", "value": "again"},
	}}); err == nil {
		t.Fatal("expected duplicate question emoji error")
	}
}

func TestSignalRoutesApprovalReactionOnce(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &signalBot{
		channelID: "signal-personal",
		onMessage: func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) },
	}
	targetID := signalEventID("+15551234567", 1000)
	bot.registerReactionRoute(signalReactionRoute{
		ID: "approval-1", Kind: "approval", TargetID: targetID,
		Choices: map[string]string{"✅": "approve", "❌": "deny"},
	})

	var env signalEnvelope
	env.Envelope.Source = "+15557654321"
	env.Envelope.Timestamp = 2000
	env.Envelope.DataMessage = &struct {
		Message     string             `json:"message"`
		Timestamp   int64              `json:"timestamp"`
		Attachments []signalAttachment `json:"attachments"`
		Reaction    *signalReaction    `json:"reaction"`
	}{Reaction: &signalReaction{Emoji: "✅", TargetAuthor: "+15551234567", TargetSentTimestamp: 1000}}
	bot.deliverEnvelope(env)
	bot.deliverEnvelope(env)

	if len(delivered) != 1 {
		t.Fatalf("expected one-shot routed reaction, got %d deliveries", len(delivered))
	}
	if !strings.Contains(delivered[0].Text, "route_id=approval-1 value=approve") {
		t.Fatalf("unexpected routed reaction: %q", delivered[0].Text)
	}
}

func TestSignalTypingClearsAfterSend(t *testing.T) {
	var calls []string
	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/v2/send" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"timestamp":9999}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := bot.SendTyping(context.Background(), 0); err != nil {
		t.Fatalf("send typing: %v", err)
	}
	if err := bot.Send(context.Background(), "done"); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := []string{
		"PUT /v1/typing-indicator/+15551234567",
		"POST /v2/send",
		"DELETE /v1/typing-indicator/+15551234567",
	}
	if len(calls) != len(want) {
		t.Fatalf("unexpected typing lifecycle calls: %#v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d: want %q, got %q", i, want[i], calls[i])
		}
	}
}
