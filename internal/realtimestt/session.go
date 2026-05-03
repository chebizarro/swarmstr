package realtimestt

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

type pluginSession struct {
	id           string
	host         ProviderInvoker
	providerID   string
	ctx          context.Context
	cancel       context.CancelFunc
	onTranscript TranscriptCallback
	done         chan struct{}
	mu           sync.Mutex
	closed       bool
	once         sync.Once
}

func (s *pluginSession) SendAudio(data []byte) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("session closed")
	}
	res, err := s.host.InvokeProvider(s.ctx, s.providerID, "send_audio", map[string]any{"session_id": s.id, "audio": base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return err
	}
	m := asMap(res)
	txt := firstNonEmpty(stringValue(m["transcript"]), stringValue(m["text"]), stringValue(m["partial"]), stringValue(m["final"]))
	if txt != "" && s.onTranscript != nil {
		s.onTranscript(txt, boolDefault(firstPresent(m, "is_final", "isFinal", "final"), false))
	}
	return nil
}
func (s *pluginSession) Close() error {
	var err error
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.once.Do(func() { close(s.done) })
	if s.host != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = s.host.InvokeProvider(ctx, s.providerID, "close_session", map[string]any{"session_id": s.id})
	}
	return err
}
func (s *pluginSession) Done() <-chan struct{} { return s.done }
