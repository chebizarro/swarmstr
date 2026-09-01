package talk

import (
	"context"

	browserpkg "metiq/internal/browser"
	"metiq/internal/realtimestt"
	"metiq/internal/realtimevoice"
	"metiq/internal/tts"
)

// ── fake tts provider ───────────────────────────────────────────────────────

type fakeTTSProvider struct {
	id         string
	configured bool
	format     string
	fail       error
	empty      bool
}

func (f *fakeTTSProvider) ID() string       { return f.id }
func (f *fakeTTSProvider) Name() string     { return f.id }
func (f *fakeTTSProvider) Voices() []string { return []string{"alloy"} }
func (f *fakeTTSProvider) Configured() bool { return f.configured }
func (f *fakeTTSProvider) Convert(ctx context.Context, text, voice string) ([]byte, string, error) {
	if f.fail != nil {
		return nil, "", f.fail
	}
	if f.empty {
		return []byte{}, f.format, nil
	}
	return []byte("audio-bytes"), f.format, nil
}

func ttsManagerWith(providers ...tts.Provider) *tts.Manager {
	m := tts.NewManager()
	for _, p := range providers {
		m.Register(p)
	}
	return m
}

// ── fake realtimevoice provider / bridge ────────────────────────────────────

type fakeVoiceProvider struct {
	id          string
	configured  bool
	browser     bool
	browserFail error
	bridge      *fakeBridge
}

func (f *fakeVoiceProvider) ID() string       { return f.id }
func (f *fakeVoiceProvider) Name() string     { return f.id }
func (f *fakeVoiceProvider) Configured() bool { return f.configured }
func (f *fakeVoiceProvider) CreateBridge(ctx context.Context, cfg realtimevoice.BridgeConfig) (realtimevoice.Bridge, error) {
	f.bridge = &fakeBridge{done: make(chan struct{}), cfg: cfg}
	return f.bridge, nil
}
func (f *fakeVoiceProvider) ListVoices(ctx context.Context) ([]realtimevoice.VoiceInfo, error) {
	return nil, nil
}

// CreateBrowserSession is only present when browser==true is desired; a
// separate type is used to avoid always advertising the capability.
type fakeBrowserVoiceProvider struct {
	fakeVoiceProvider
}

func (f *fakeBrowserVoiceProvider) CreateBrowserSession(ctx context.Context, cfg BrowserSessionConfig) (browserpkg.Session, error) {
	if f.browserFail != nil {
		return nil, f.browserFail
	}
	return browserpkg.Session{"offer": "sdp-offer", "voice": cfg.Voice}, nil
}

type mismatchedBrowserVoiceProvider struct {
	fakeVoiceProvider
}

func (f *mismatchedBrowserVoiceProvider) CreateBrowserSession(context.Context, BrowserSessionConfig) (browserpkg.Session, error) {
	return browserpkg.Session{"transport": browserpkg.TransportProviderWebSocket}, nil
}

type fakeBridge struct {
	audio       [][]byte
	text        []string
	interrupted int
	closed      bool
	done        chan struct{}
	cfg         realtimevoice.BridgeConfig
}

func (b *fakeBridge) SendAudio(d []byte) error { b.audio = append(b.audio, d); return nil }
func (b *fakeBridge) SendText(t string) error  { b.text = append(b.text, t); return nil }
func (b *fakeBridge) Interrupt() error         { b.interrupted++; return nil }
func (b *fakeBridge) Close() error {
	if !b.closed {
		b.closed = true
		close(b.done)
	}
	return nil
}
func (b *fakeBridge) Done() <-chan struct{} { return b.done }

// ── fake realtimestt provider / session ─────────────────────────────────────

type fakeSTTProvider struct {
	id         string
	configured bool
	session    *fakeSTTSession
}

func (f *fakeSTTProvider) ID() string       { return f.id }
func (f *fakeSTTProvider) Name() string     { return f.id }
func (f *fakeSTTProvider) Configured() bool { return f.configured }
func (f *fakeSTTProvider) CreateSession(ctx context.Context, cfg realtimestt.SessionConfig) (realtimestt.Session, error) {
	f.session = &fakeSTTSession{done: make(chan struct{})}
	return f.session, nil
}

type fakeSTTSession struct {
	audio  [][]byte
	closed bool
	done   chan struct{}
}

func (s *fakeSTTSession) SendAudio(d []byte) error { s.audio = append(s.audio, d); return nil }
func (s *fakeSTTSession) Close() error {
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	return nil
}
func (s *fakeSTTSession) Done() <-chan struct{} { return s.done }

// ── fake emitter ────────────────────────────────────────────────────────────

type emittedEvent struct {
	connID  string
	event   string
	payload any
}

type fakeEmitter struct {
	events []emittedEvent
}

func (e *fakeEmitter) EmitTo(connID, event string, payload any) bool {
	e.events = append(e.events, emittedEvent{connID: connID, event: event, payload: payload})
	return true
}

func (e *fakeEmitter) count(event string) int {
	n := 0
	for _, ev := range e.events {
		if ev.event == event {
			n++
		}
	}
	return n
}

func voiceRegistryWith(p realtimevoice.Provider) *realtimevoice.Registry {
	r := realtimevoice.NewRegistry()
	if p != nil {
		_ = r.Register(p)
	}
	return r
}

func sttRegistryWith(p realtimestt.Provider) *realtimestt.Registry {
	r := realtimestt.NewRegistry()
	if p != nil {
		_ = r.Register(p)
	}
	return r
}
