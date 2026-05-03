package realtimevoice

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

type pluginBridge struct {
	sessionID    string
	host         ProviderInvoker
	providerID   string
	ctx          context.Context
	cancel       context.CancelFunc
	onAudio      AudioCallback
	onTranscript func(string, string)
	done         chan struct{}
	mu           sync.Mutex
	closed       bool
	once         sync.Once
}

func (b *pluginBridge) SendAudio(data []byte) error {
	return b.invoke("bridge_send_audio", map[string]any{"session_id": b.sessionID, "audio": base64.StdEncoding.EncodeToString(data)})
}
func (b *pluginBridge) SendText(text string) error {
	return b.invoke("bridge_send_text", map[string]any{"session_id": b.sessionID, "text": text})
}
func (b *pluginBridge) Interrupt() error {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return fmt.Errorf("bridge closed")
	}
	_, err := b.host.InvokeProvider(b.ctx, b.providerID, "bridge_interrupt", map[string]any{"session_id": b.sessionID})
	return err
}
func (b *pluginBridge) invoke(method string, payload map[string]any) error {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return fmt.Errorf("bridge closed")
	}
	res, err := b.host.InvokeProvider(b.ctx, b.providerID, method, payload)
	if err != nil {
		return err
	}
	b.handleResponse(res)
	return nil
}
func (b *pluginBridge) handleResponse(res any) {
	m := asMap(res)
	if len(m) == 0 {
		return
	}
	if audioB64 := firstNonEmpty(stringValue(m["audio"]), stringValue(m["audio_base64"]), stringValue(m["audioBase64"])); audioB64 != "" {
		audio, _ := base64.StdEncoding.DecodeString(audioB64)
		if b.onAudio != nil {
			b.onAudio(audio, firstNonEmpty(stringValue(m["format"]), stringValue(m["audio_format"]), stringValue(m["audioFormat"])))
		}
	}
	if txt := firstNonEmpty(stringValue(m["transcript"]), stringValue(m["text"])); txt != "" && b.onTranscript != nil {
		b.onTranscript(txt, stringValue(m["role"]))
	}
}
func (b *pluginBridge) Close() error {
	var err error
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
	b.once.Do(func() { close(b.done) })
	if b.host != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = b.host.InvokeProvider(ctx, b.providerID, "bridge_close", map[string]any{"session_id": b.sessionID})
	}
	return err
}
func (b *pluginBridge) Done() <-chan struct{} { return b.done }
