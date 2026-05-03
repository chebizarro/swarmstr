package realtimestt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type sttInvoker struct {
	result any
	err    error
	calls  []map[string]any
}

func (s *sttInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	if p, ok := params.(map[string]any); ok {
		s.calls = append(s.calls, p)
	}
	return s.result, s.err
}

func TestRealtimeSTTRegistryAndPluginProvider(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Default(); err == nil {
		t.Fatal("expected empty default error")
	}
	if err := reg.Register(nil); err == nil {
		t.Fatal("expected nil provider error")
	}
	host := &sttInvoker{result: map[string]any{"sessionId": "s1"}}
	p := NewPluginProvider(" DemoSTT ", map[string]any{"name": "Demo STT"}, host)
	if p.ID() != "demostt" || p.Name() != "Demo STT" || !p.Configured() {
		t.Fatalf("unexpected provider metadata")
	}
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got, ok := reg.Get(" DEMOSTT "); !ok || got.ID() != "demostt" || len(reg.List()) != 1 {
		t.Fatalf("lookup/list failed")
	}
	def, err := reg.Default()
	if err != nil || def.ID() != "demostt" {
		t.Fatalf("default=%v err=%v", def, err)
	}
	sess, err := p.CreateSession(context.Background(), SessionConfig{Language: "en", Model: "nova", SampleRate: 16000, Encoding: "linear16", Channels: 1})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(host.calls) != 1 || host.calls[0]["sample_rate"] != 16000 {
		t.Fatalf("unexpected call params: %+v", host.calls)
	}
	_ = sess.Close()
	<-sess.Done()
}

func TestRealtimeSTTProviderFallbacksAndParsers(t *testing.T) {
	if NewPluginProvider("x", nil, nil).Configured() {
		t.Fatal("nil host should not configure")
	}
	p := NewPluginProvider("x", nil, &sttInvoker{result: "false"})
	if p.Configured() {
		t.Fatal("configured false should be false")
	}
	p = NewPluginProvider("x", nil, &sttInvoker{result: map[string]any{}})
	if _, err := p.CreateSession(context.Background(), SessionConfig{}); err == nil {
		t.Fatal("expected missing session id error")
	}
	if sessionID("abc") != "abc" || sessionID(struct {
		ID string `json:"id"`
	}{ID: "json-id"}) != "json-id" {
		t.Fatal("sessionID parsing failed")
	}
	if !isMissingProviderMethod(sttErr("unknown provider method")) || !boolDefault("1", false) || firstNonEmpty(" ", " y ") != "y" {
		t.Fatal("helper mismatch")
	}
	if got := asMap(struct {
		SessionID string `json:"session_id"`
	}{SessionID: "s"}); got["session_id"] != "s" {
		t.Fatalf("asMap JSON conversion failed: %#v", got)
	}
	deepgram, final := parseTranscriptEvent([]byte(`{"channel":{"alternatives":[{"transcript":"hello"}]},"is_final":true}`))
	if deepgram != "hello" || !final {
		t.Fatalf("deepgram parse = %q %v", deepgram, final)
	}
	text, final := parseTranscriptEvent([]byte(`{"text":"partial","final":false}`))
	if text != "partial" || final {
		t.Fatalf("flat parse = %q %v", text, final)
	}
}

func TestRealtimeSTTWebSocketProvider(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.Header.Get("Authorization") != "Token key" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		conn, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"text": "hi", "is_final": true})
		_, msg, _ := conn.ReadMessage()
		if string(msg) != "audio" {
			t.Fatalf("audio payload = %q", msg)
		}
	}))
	defer server.Close()

	p := NewDeepgramProvider()
	p.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	t.Setenv("DEEPGRAM_API_KEY", "key")
	transcripts := make(chan string, 1)
	sess, err := p.CreateSession(context.Background(), SessionConfig{Model: "nova", Language: "en", SampleRate: 8000, Encoding: "linear16", Channels: 1, OnTranscript: func(text string, final bool) {
		if final {
			transcripts <- text
		}
	}})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !strings.Contains(gotQuery, "model=nova") || !strings.Contains(gotQuery, "interim_results=true") {
		t.Fatalf("unexpected query: %s", gotQuery)
	}
	if err := sess.SendAudio([]byte("audio")); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}
	select {
	case got := <-transcripts:
		if got != "hi" {
			t.Fatalf("transcript = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript")
	}
	_ = sess.Close()
	<-sess.Done()
}

func TestRealtimeSTTWebSocketProviderUnconfigured(t *testing.T) {
	p := NewAssemblyAIProvider()
	t.Setenv("ASSEMBLYAI_API_KEY", "")
	if p.Configured() {
		t.Fatal("expected unconfigured")
	}
	if _, err := p.CreateSession(context.Background(), SessionConfig{}); err == nil {
		t.Fatal("expected unconfigured error")
	}
	// Exercise the AssemblyAI query/header closures without dialing.
	q := p.query(SessionConfig{SampleRate: 44100, Encoding: "pcm_s16le"})
	if q.Get("sample_rate") != "44100" || p.headers("k").Get("Authorization") != "k" {
		t.Fatal("assemblyai closures mismatch")
	}
	_, _ = json.Marshal(map[string]string{"keep": "encoding/json imported"})
}

type sttErr string

func (e sttErr) Error() string { return string(e) }
