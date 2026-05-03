package realtimestt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type WebSocketProvider struct {
	id, name, apiKeyEnv, endpoint string
	query                         func(SessionConfig) url.Values
	headers                       func(string) http.Header
}

func NewDeepgramProvider() *WebSocketProvider {
	return &WebSocketProvider{id: "deepgram", name: "Deepgram Realtime", apiKeyEnv: "DEEPGRAM_API_KEY", endpoint: "wss://api.deepgram.com/v1/listen", query: func(c SessionConfig) url.Values {
		q := url.Values{}
		if c.Model != "" {
			q.Set("model", c.Model)
		}
		if c.Language != "" {
			q.Set("language", c.Language)
		}
		if c.Encoding != "" {
			q.Set("encoding", c.Encoding)
		}
		if c.SampleRate > 0 {
			q.Set("sample_rate", strconv.Itoa(c.SampleRate))
		}
		if c.Channels > 0 {
			q.Set("channels", strconv.Itoa(c.Channels))
		}
		q.Set("interim_results", "true")
		return q
	}, headers: func(k string) http.Header { h := http.Header{}; h.Set("Authorization", "Token "+k); return h }}
}
func NewAssemblyAIProvider() *WebSocketProvider {
	return &WebSocketProvider{id: "assemblyai", name: "AssemblyAI Realtime", apiKeyEnv: "ASSEMBLYAI_API_KEY", endpoint: "wss://streaming.assemblyai.com/v3/ws", query: func(c SessionConfig) url.Values {
		q := url.Values{}
		if c.SampleRate > 0 {
			q.Set("sample_rate", strconv.Itoa(c.SampleRate))
		}
		if c.Encoding != "" {
			q.Set("encoding", c.Encoding)
		}
		return q
	}, headers: func(k string) http.Header { h := http.Header{}; h.Set("Authorization", k); return h }}
}
func (p *WebSocketProvider) ID() string       { return p.id }
func (p *WebSocketProvider) Name() string     { return p.name }
func (p *WebSocketProvider) Configured() bool { return strings.TrimSpace(os.Getenv(p.apiKeyEnv)) != "" }
func (p *WebSocketProvider) CreateSession(ctx context.Context, cfg SessionConfig) (Session, error) {
	key := strings.TrimSpace(os.Getenv(p.apiKeyEnv))
	if key == "" {
		return nil, fmt.Errorf("%s realtime transcription provider is not configured (%s)", p.ID(), p.apiKeyEnv)
	}
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, err
	}
	if p.query != nil {
		u.RawQuery = p.query(cfg).Encode()
	}
	h := http.Header{}
	if p.headers != nil {
		h = p.headers(key)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), h)
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithCancel(ctx)
	s := &wsSession{conn: conn, ctx: sctx, cancel: cancel, onTranscript: cfg.OnTranscript, done: make(chan struct{})}
	go s.readLoop()
	return s, nil
}

type wsSession struct {
	conn         *websocket.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	onTranscript TranscriptCallback
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	closed       bool
}

func (s *wsSession) SendAudio(data []byte) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("session closed")
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}
func (s *wsSession) Close() error {
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
	err := s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = s.conn.Close()
	s.once.Do(func() { close(s.done) })
	return err
}
func (s *wsSession) Done() <-chan struct{} { return s.done }
func (s *wsSession) readLoop() {
	defer s.once.Do(func() { close(s.done) })
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		txt, final := parseTranscriptEvent(data)
		if txt != "" && s.onTranscript != nil {
			s.onTranscript(txt, final)
		}
	}
}
func parseTranscriptEvent(data []byte) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return "", false
	}
	if ch, ok := m["channel"].(map[string]any); ok {
		if alts, ok := ch["alternatives"].([]any); ok && len(alts) > 0 {
			am := asMap(alts[0])
			return stringValue(am["transcript"]), boolDefault(firstPresent(m, "is_final", "speech_final"), false)
		}
	}
	txt := firstNonEmpty(stringValue(firstPresent(m, "transcript", "text", "partial", "final")))
	return txt, boolDefault(firstPresent(m, "is_final", "isFinal", "final"), false)
}
