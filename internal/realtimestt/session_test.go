package realtimestt

import (
	"context"
	"encoding/base64"
	"testing"
)

type sttHost struct {
	calls   []string
	params  []any
	results map[string]any
}

func (h *sttHost) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	h.calls = append(h.calls, method)
	h.params = append(h.params, params)
	return h.results[method], nil
}
func TestPluginSessionLifecycle(t *testing.T) {
	h := &sttHost{results: map[string]any{"create_session": map[string]any{"session_id": "s1"}, "send_audio": map[string]any{"transcript": "hi", "is_final": false}}}
	p := NewPluginProvider("deepgram", nil, h)
	var got string
	sess, err := p.CreateSession(context.Background(), SessionConfig{SampleRate: 16000, OnTranscript: func(text string, final bool) {
		got = text
		if final {
			t.Fatal("unexpected final")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SendAudio([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got != "hi" {
		t.Fatalf("callback=%q", got)
	}
	payload := h.params[1].(map[string]any)
	if payload["audio"] != base64.StdEncoding.EncodeToString([]byte("abc")) {
		t.Fatalf("bad audio payload %#v", payload)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.Done():
	default:
		t.Fatal("done not closed")
	}
	if err := sess.SendAudio([]byte("x")); err == nil {
		t.Fatal("expected closed error")
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
}
