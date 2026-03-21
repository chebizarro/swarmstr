package signal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if !caps.Reactions || !caps.MultiAccount {
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
		allowedSenders: map[string]bool{},
		httpClient:     srv.Client(),
		done:           make(chan struct{}),
	}
	return srv, bot
}

// ── receive / polling ─────────────────────────────────────────────────────────

func TestReceive_JSONArray(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	envelopes := []signalEnvelope{
		{Envelope: struct {
			Source      string `json:"source"`
			Timestamp   int64  `json:"timestamp"`
			DataMessage *struct {
				Message   string `json:"message"`
				Timestamp int64  `json:"timestamp"`
			} `json:"dataMessage"`
		}{
			Source:    "+15559876543",
			Timestamp: 1000,
			DataMessage: &struct {
				Message   string `json:"message"`
				Timestamp int64  `json:"timestamp"`
			}{Message: "hello signal", Timestamp: 1000},
		}},
	}

	srv, bot := newTestSignalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/v1/receive/") {
			raw, _ := json.Marshal(envelopes)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
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
