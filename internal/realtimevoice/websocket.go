package realtimevoice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

type WebSocketProvider struct {
	id, name, apiKeyEnv, endpointEnv, defaultEndpoint string
	openAI                                            bool
}

func NewOpenAIRealtimeProvider() *WebSocketProvider {
	return &WebSocketProvider{id: "openai-realtime", name: "OpenAI Realtime", apiKeyEnv: "OPENAI_API_KEY", endpointEnv: "OPENAI_REALTIME_URL", defaultEndpoint: "wss://api.openai.com/v1/realtime", openAI: true}
}
func NewElevenLabsRealtimeProvider() *WebSocketProvider {
	return &WebSocketProvider{id: "elevenlabs", name: "ElevenLabs Realtime", apiKeyEnv: "ELEVENLABS_API_KEY", endpointEnv: "ELEVENLABS_REALTIME_URL", defaultEndpoint: "wss://api.elevenlabs.io/v1/convai/conversation"}
}
func (p *WebSocketProvider) ID() string                                          { return p.id }
func (p *WebSocketProvider) Name() string                                        { return p.name }
func (p *WebSocketProvider) Configured() bool                                    { return strings.TrimSpace(os.Getenv(p.apiKeyEnv)) != "" }
// openAIRealtimeVoices is the fixed, documented voice set exposed by the OpenAI
// Realtime API. The realtime WebSocket transport has no voices-listing method,
// so we return this known set rather than silently returning an empty list.
var openAIRealtimeVoices = []VoiceInfo{
	{ID: "alloy", Name: "Alloy", Language: "en"},
	{ID: "ash", Name: "Ash", Language: "en"},
	{ID: "ballad", Name: "Ballad", Language: "en"},
	{ID: "coral", Name: "Coral", Language: "en"},
	{ID: "echo", Name: "Echo", Language: "en"},
	{ID: "sage", Name: "Sage", Language: "en"},
	{ID: "shimmer", Name: "Shimmer", Language: "en"},
	{ID: "verse", Name: "Verse", Language: "en"},
}

// ListVoices returns the provider's available voices. OpenAI Realtime exposes a
// fixed documented voice set. Other realtime providers (e.g. ElevenLabs) publish
// voices via a separate REST catalog that is not reachable over this WebSocket
// transport, so we return an explicit unsupported error instead of a misleading
// empty (nil, nil) result.
func (p *WebSocketProvider) ListVoices(_ context.Context) ([]VoiceInfo, error) {
	if p.openAI {
		out := make([]VoiceInfo, len(openAIRealtimeVoices))
		copy(out, openAIRealtimeVoices)
		return out, nil
	}
	return nil, fmt.Errorf("%s: listing voices is not supported over the realtime transport; query the provider's voices REST API instead", p.id)
}
func (p *WebSocketProvider) CreateBridge(ctx context.Context, cfg BridgeConfig) (Bridge, error) {
	key := strings.TrimSpace(os.Getenv(p.apiKeyEnv))
	if key == "" {
		return nil, fmt.Errorf("%s realtime voice provider is not configured (%s)", p.ID(), p.apiKeyEnv)
	}
	endpoint := firstNonEmpty(os.Getenv(p.endpointEnv), p.defaultEndpoint)
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if p.openAI {
		if cfg.Model != "" {
			q.Set("model", cfg.Model)
		}
		if q.Get("model") == "" {
			q.Set("model", "gpt-4o-realtime-preview")
		}
	} else if cfg.Model != "" {
		q.Set("agent_id", cfg.Model)
	}
	u.RawQuery = q.Encode()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+key)
	if p.openAI {
		h.Set("OpenAI-Beta", "realtime=v1")
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), h)
	if err != nil {
		return nil, err
	}
	bctx, cancel := context.WithCancel(ctx)
	b := &wsBridge{conn: conn, ctx: bctx, cancel: cancel, onAudio: cfg.OnAudio, onTranscript: cfg.OnTranscript, done: make(chan struct{}), openAI: p.openAI}
	go b.readLoop()
	if cfg.SystemPrompt != "" {
		_ = b.SendText(cfg.SystemPrompt)
	}
	return b, nil
}

type wsBridge struct {
	conn         *websocket.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	onAudio      AudioCallback
	onTranscript func(string, string)
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	closed       bool
	openAI       bool
}

func (b *wsBridge) SendAudio(data []byte) error {
	if b.isClosed() {
		return fmt.Errorf("bridge closed")
	}
	if b.openAI {
		return b.writeJSON(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(data)})
	}
	return b.writeJSON(map[string]any{"user_audio_chunk": base64.StdEncoding.EncodeToString(data)})
}
func (b *wsBridge) SendText(text string) error {
	if b.isClosed() {
		return fmt.Errorf("bridge closed")
	}
	if b.openAI {
		if err := b.writeJSON(map[string]any{"type": "conversation.item.create", "item": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": text}}}}); err != nil {
			return err
		}
		return b.writeJSON(map[string]any{"type": "response.create"})
	}
	return b.writeJSON(map[string]any{"text": text})
}
func (b *wsBridge) Interrupt() error {
	if b.isClosed() {
		return fmt.Errorf("bridge closed")
	}
	if b.openAI {
		return b.writeJSON(map[string]any{"type": "response.cancel"})
	}
	return b.writeJSON(map[string]any{"type": "interrupt"})
}
func (b *wsBridge) Close() error {
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
	err := b.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = b.conn.Close()
	b.once.Do(func() { close(b.done) })
	return err
}
func (b *wsBridge) Done() <-chan struct{} { return b.done }
func (b *wsBridge) isClosed() bool        { b.mu.Lock(); defer b.mu.Unlock(); return b.closed }
func (b *wsBridge) writeJSON(v any) error { return b.conn.WriteJSON(v) }
func (b *wsBridge) readLoop() {
	defer b.once.Do(func() { close(b.done) })
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}
		_, data, err := b.conn.ReadMessage()
		if err != nil {
			return
		}
		b.handleWSMessage(data)
	}
}
func (b *wsBridge) handleWSMessage(data []byte) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	if audioB64 := firstNonEmpty(stringValue(m["audio"]), stringValue(m["audio_base64"]), stringValue(m["audioBase64"]), stringValue(m["delta"])); audioB64 != "" && (strings.Contains(stringValue(m["type"]), "audio") || m["audio"] != nil || m["audio_base64"] != nil) {
		audio, _ := base64.StdEncoding.DecodeString(audioB64)
		if b.onAudio != nil {
			b.onAudio(audio, firstNonEmpty(stringValue(m["format"]), "pcm16"))
		}
	}
	if txt := firstNonEmpty(stringValue(m["transcript"]), stringValue(m["text"])); txt != "" && b.onTranscript != nil {
		role := firstNonEmpty(stringValue(m["role"]), "assistant")
		b.onTranscript(txt, role)
	}
}
