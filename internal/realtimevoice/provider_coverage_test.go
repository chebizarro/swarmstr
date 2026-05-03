package realtimevoice

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type voiceInvoker struct {
	result any
	err    error
	calls  []voiceCall
}

type voiceCall struct {
	method string
	params map[string]any
}

func (v *voiceInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	m, _ := params.(map[string]any)
	v.calls = append(v.calls, voiceCall{method: method, params: m})
	return v.result, v.err
}

func TestRealtimeVoiceRegistryAndPluginProvider(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Default(); err == nil {
		t.Fatal("expected empty default error")
	}
	if err := reg.Register(nil); err == nil {
		t.Fatal("expected nil provider error")
	}
	host := &voiceInvoker{result: map[string]any{"session_id": "v1"}}
	p := NewPluginProvider(" Voice ", map[string]any{"name": "Voice Plugin"}, host)
	if p.ID() != "voice" || p.Name() != "Voice Plugin" || !p.Configured() {
		t.Fatalf("unexpected provider metadata")
	}
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got, ok := reg.Get("VOICE"); !ok || got.ID() != "voice" || len(reg.List()) != 1 {
		t.Fatal("lookup/list failed")
	}
	def, err := reg.Default()
	if err != nil || def.ID() != "voice" {
		t.Fatalf("default=%v err=%v", def, err)
	}
	bridge, err := p.CreateBridge(context.Background(), BridgeConfig{Model: "m", Voice: "alloy", Language: "en", InputFormat: AudioFormat{Encoding: "pcm16", SampleRate: 16000, Channels: 1}})
	if err != nil {
		t.Fatalf("CreateBridge: %v", err)
	}
	last := host.calls[len(host.calls)-1]
	if last.method != "create_bridge" || last.params["voice"] != "alloy" {
		t.Fatalf("unexpected bridge params: %+v", host.calls)
	}
	_ = bridge.Close()
	<-bridge.Done()

	host.result = []VoiceInfo{{ID: "a", Name: "Alloy"}}
	voices, err := p.ListVoices(context.Background())
	if err != nil || len(voices) != 1 || voices[0].ID != "a" {
		t.Fatalf("voices=%+v err=%v", voices, err)
	}
	host.result = map[string]any{"voices": []map[string]any{{"id": "b", "name": "B"}}}
	voices, err = p.ListVoices(context.Background())
	if err != nil || len(voices) != 1 || voices[0].ID != "b" {
		t.Fatalf("wrapped voices=%+v err=%v", voices, err)
	}
}

func TestRealtimeVoiceProviderFallbacksAndHelpers(t *testing.T) {
	if NewPluginProvider("x", nil, nil).Configured() {
		t.Fatal("nil host should not configure")
	}
	p := NewPluginProvider("x", nil, &voiceInvoker{result: ""})
	if _, err := p.CreateBridge(context.Background(), BridgeConfig{}); err == nil {
		t.Fatal("expected missing session id error")
	}
	if !isMissingProviderMethod(voiceErr("not a function")) || !boolDefault("true", false) || firstNonEmpty(" ", " z ") != "z" {
		t.Fatal("helper mismatch")
	}
	if sessionID(struct {
		SessionID string `json:"sessionId"`
	}{SessionID: "sid"}) != "sid" {
		t.Fatal("sessionID JSON conversion failed")
	}
}

func TestRealtimeVoiceWebSocketProviderOpenAI(t *testing.T) {
	messages := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" || r.Header.Get("OpenAI-Beta") != "realtime=v1" {
			t.Fatalf("unexpected headers: %v", r.Header)
		}
		if r.URL.Query().Get("model") != "test-model" {
			t.Fatalf("model query = %q", r.URL.Query().Get("model"))
		}
		conn, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"type": "response.audio.delta", "delta": base64.StdEncoding.EncodeToString([]byte("pcm"))})
		_ = conn.WriteJSON(map[string]any{"transcript": "hello", "role": "assistant"})
		for i := 0; i < 4; i++ {
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			messages <- msg
		}
	}))
	defer server.Close()

	p := NewOpenAIRealtimeProvider()
	p.defaultEndpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	t.Setenv("OPENAI_API_KEY", "key")
	audioCh := make(chan string, 1)
	textCh := make(chan string, 1)
	bridge, err := p.CreateBridge(context.Background(), BridgeConfig{Model: "test-model", OnAudio: func(audio []byte, format string) { audioCh <- string(audio) + ":" + format }, OnTranscript: func(text, role string) { textCh <- role + ":" + text }})
	if err != nil {
		t.Fatalf("CreateBridge: %v", err)
	}
	if err := bridge.SendAudio([]byte("audio")); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}
	if err := bridge.SendText("hi"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := bridge.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case got := <-audioCh:
		if got != "pcm:pcm16" {
			t.Fatalf("audio callback = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio")
	}
	select {
	case got := <-textCh:
		if got != "assistant:hello" {
			t.Fatalf("text callback = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript")
	}
	if m := <-messages; m["type"] != "input_audio_buffer.append" {
		t.Fatalf("first message = %+v", m)
	}
	_ = bridge.Close()
	<-bridge.Done()
	if err := bridge.SendAudio([]byte("after-close")); err == nil {
		t.Fatal("expected closed send error")
	}
}

func TestRealtimeVoiceWebSocketProviderElevenLabsAndUnconfigured(t *testing.T) {
	p := NewElevenLabsRealtimeProvider()
	t.Setenv("ELEVENLABS_API_KEY", "")
	if p.Configured() {
		t.Fatal("expected unconfigured")
	}
	if _, err := p.CreateBridge(context.Background(), BridgeConfig{}); err == nil {
		t.Fatal("expected unconfigured error")
	}
	if voices, err := p.ListVoices(context.Background()); err != nil || voices != nil {
		t.Fatalf("ListVoices default = %+v %v", voices, err)
	}
}

type voiceErr string

func (e voiceErr) Error() string { return string(e) }
