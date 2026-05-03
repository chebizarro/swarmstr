package realtimevoice

import (
	"context"
	"encoding/base64"
	"testing"
)

type voiceHost struct {
	calls   []string
	params  []any
	results map[string]any
}

func (h *voiceHost) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	h.calls = append(h.calls, method)
	h.params = append(h.params, params)
	return h.results[method], nil
}
func TestPluginBridgeLifecycle(t *testing.T) {
	h := &voiceHost{results: map[string]any{"create_bridge": map[string]any{"session_id": "b1"}, "bridge_send_audio": map[string]any{"audio": base64.StdEncoding.EncodeToString([]byte("out")), "format": "pcm16", "transcript": "hello", "role": "assistant"}, "list_voices": map[string]any{"voices": []any{map[string]any{"id": "v", "name": "Voice"}}}}}
	p := NewPluginProvider("openai-realtime", nil, h)
	voices, err := p.ListVoices(context.Background())
	if err != nil || len(voices) != 1 {
		t.Fatalf("voices=%#v err=%v", voices, err)
	}
	var audio []byte
	var transcript string
	b, err := p.CreateBridge(context.Background(), BridgeConfig{OnAudio: func(a []byte, f string) {
		audio = a
		if f != "pcm16" {
			t.Fatal(f)
		}
	}, OnTranscript: func(t, role string) { transcript = t + ":" + role }})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SendAudio([]byte("in")); err != nil {
		t.Fatal(err)
	}
	if string(audio) != "out" || transcript != "hello:assistant" {
		t.Fatalf("callbacks audio=%q transcript=%q", audio, transcript)
	}
	payload := h.params[2].(map[string]any)
	if payload["audio"] != base64.StdEncoding.EncodeToString([]byte("in")) {
		t.Fatalf("bad payload %#v", payload)
	}
	if err := b.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Done():
	default:
		t.Fatal("done not closed")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}
