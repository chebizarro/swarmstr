package talk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"metiq/internal/autoreply"
	"metiq/internal/realtimestt"
	"metiq/internal/realtimevoice"
)

// EventEmitter delivers a talk event to exactly one owning WS connection.
// Session audio/transcript/state events are never broadcast.
type EventEmitter interface {
	EmitTo(connID, event string, payload any) bool
}

// Talk session lifecycle event names emitted to the owning connection.
const (
	EventTalkSessionState      = "talk.session.state"
	EventTalkSessionTranscript = "talk.session.transcript"
	EventTalkSessionAudio      = "talk.session.audio"
	EventTalkSessionClosed     = "talk.session.closed"
)

// Talk session modes.
const (
	ModeRealtime      = "realtime"
	ModeTranscription = "transcription"
	ModeSTTTTS        = "stt-tts"
)

// Talk session transports.
const (
	TransportGatewayRelay = "gateway-relay"
	TransportManagedRoom  = "managed-room"
)

type sessionState string

const (
	stateIdle       sessionState = "idle"
	stateListening  sessionState = "listening"
	stateProcessing sessionState = "processing"
	stateClosed     sessionState = "closed"
)

type session struct {
	id        string
	connID    string
	mode      string
	transport string
	provider  string
	state     sessionState
	turnSeq   int
	marks     map[string]bool
	bridge    realtimevoice.Bridge
	sttSess   realtimestt.Session
}

// SessionManager owns server-side (gateway-relay) voice sessions keyed by the
// owning WS connection, mirroring the terminal-manager ownership pattern. Turn
// state is tracked in the gateway; audio transport binds to the realtimevoice /
// realtimestt provider registries and fails honestly when none is registered.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*session
	byConn   map[string]map[string]struct{}
	seq      int

	voice    *realtimevoice.Registry
	stt      *realtimestt.Registry
	steering *autoreply.SteeringMailboxRegistry
	emitter  EventEmitter
}

// NewSessionManager constructs a session manager. Any dependency may be nil; a
// nil voice/stt registry makes gateway-relay sessions honestly unavailable.
func NewSessionManager(voice *realtimevoice.Registry, stt *realtimestt.Registry, steering *autoreply.SteeringMailboxRegistry, emitter EventEmitter) *SessionManager {
	return &SessionManager{
		sessions: map[string]*session{},
		byConn:   map[string]map[string]struct{}{},
		voice:    voice,
		stt:      stt,
		steering: steering,
		emitter:  emitter,
	}
}

// CreateInput carries the resolved talk.session.create parameters.
type CreateInput struct {
	ConnID       string
	SessionID    string
	Mode         string
	Transport    string
	Provider     string
	Voice        string
	Language     string
	SystemPrompt string
}

// Create opens a new server-owned voice session. managed-room transport is an
// accepted deviation (ErrUnsupported); gateway-relay resolves an audio provider
// and returns ErrUnavailable when none is registered.
func (m *SessionManager) Create(ctx context.Context, in CreateInput) (map[string]any, error) {
	if in.ConnID == "" {
		return nil, fmt.Errorf("talk.session.create requires an owning connection")
	}
	switch in.Transport {
	case TransportGatewayRelay:
	case TransportManagedRoom:
		return nil, fmt.Errorf("%w: managed-room transport requires LiveKit infra not available in metiq", ErrUnsupported)
	default:
		return nil, fmt.Errorf("unsupported talk transport %q", in.Transport)
	}

	sess := &session{
		connID:    in.ConnID,
		mode:      in.Mode,
		transport: in.Transport,
		state:     stateIdle,
		marks:     map[string]bool{},
	}

	switch in.Mode {
	case ModeRealtime:
		provider, err := m.resolveVoice(in.Provider)
		if err != nil {
			return nil, err
		}
		sess.provider = provider.ID()
	case ModeTranscription, ModeSTTTTS:
		provider, err := m.resolveSTT(in.Provider)
		if err != nil {
			return nil, err
		}
		sess.provider = provider.ID()
	default:
		return nil, fmt.Errorf("unsupported talk mode %q", in.Mode)
	}

	m.mu.Lock()
	m.seq++
	id := in.SessionID
	if id == "" {
		id = fmt.Sprintf("talk-%d", m.seq)
	}
	if existing, ok := m.sessions[id]; ok && existing.connID != in.ConnID {
		m.mu.Unlock()
		return nil, fmt.Errorf("talk session %q is owned by another connection", id)
	}
	sess.id = id
	m.sessions[id] = sess
	if m.byConn[in.ConnID] == nil {
		m.byConn[in.ConnID] = map[string]struct{}{}
	}
	m.byConn[in.ConnID][id] = struct{}{}
	m.mu.Unlock()

	// Bind the audio transport now that the record exists so the OnAudio /
	// OnTranscript callbacks can stream events to the owning connection. A
	// binding failure tears the record down and surfaces honestly.
	if err := m.bind(ctx, sess, in); err != nil {
		m.removeSession(id)
		return nil, err
	}

	return map[string]any{
		"sessionId": id,
		"mode":      sess.mode,
		"transport": sess.transport,
		"provider":  sess.provider,
		"state":     string(sess.state),
	}, nil
}

func (m *SessionManager) bind(ctx context.Context, sess *session, in CreateInput) error {
	switch sess.mode {
	case ModeRealtime:
		provider, err := m.resolveVoice(in.Provider)
		if err != nil {
			return err
		}
		bridge, err := provider.CreateBridge(ctx, realtimevoice.BridgeConfig{
			Voice:        in.Voice,
			Language:     in.Language,
			SystemPrompt: in.SystemPrompt,
			OnAudio: func(audio []byte, format string) {
				m.emit(sess.connID, EventTalkSessionAudio, map[string]any{
					"sessionId":   sess.id,
					"audioBase64": base64.StdEncoding.EncodeToString(audio),
					"format":      format,
				})
			},
			OnTranscript: func(text, role string) {
				m.emit(sess.connID, EventTalkSessionTranscript, map[string]any{
					"sessionId": sess.id, "text": text, "role": role,
				})
			},
		})
		if err != nil {
			return fmt.Errorf("%w: create voice bridge: %v", ErrUnavailable, err)
		}
		sess.bridge = bridge
	case ModeTranscription, ModeSTTTTS:
		provider, err := m.resolveSTT(in.Provider)
		if err != nil {
			return err
		}
		s, err := provider.CreateSession(ctx, realtimestt.SessionConfig{
			Language: in.Language,
			OnTranscript: func(text string, isFinal bool) {
				m.emit(sess.connID, EventTalkSessionTranscript, map[string]any{
					"sessionId": sess.id, "text": text, "final": isFinal, "role": "user",
				})
			},
		})
		if err != nil {
			return fmt.Errorf("%w: create transcription session: %v", ErrUnavailable, err)
		}
		sess.sttSess = s
	}
	return nil
}

func (m *SessionManager) resolveVoice(id string) (realtimevoice.Provider, error) {
	if m.voice == nil {
		return nil, fmt.Errorf("%w: no realtimevoice registry wired", ErrUnavailable)
	}
	if id != "" {
		p, ok := m.voice.Get(id)
		if !ok {
			return nil, fmt.Errorf("%w: unknown realtime voice provider %q", ErrUnavailable, id)
		}
		return p, nil
	}
	p, err := m.voice.Default()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return p, nil
}

func (m *SessionManager) resolveSTT(id string) (realtimestt.Provider, error) {
	if m.stt == nil {
		return nil, fmt.Errorf("%w: no realtimestt registry wired", ErrUnavailable)
	}
	if id != "" {
		p, ok := m.stt.Get(id)
		if !ok {
			return nil, fmt.Errorf("%w: unknown transcription provider %q", ErrUnavailable, id)
		}
		return p, nil
	}
	p, err := m.stt.Default()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return p, nil
}

// Join is managed-room only, an accepted deviation.
func (m *SessionManager) Join(connID, sessionID string) (map[string]any, error) {
	return nil, fmt.Errorf("%w: talk.session.join is managed-room only", ErrUnsupported)
}

// StartTurn opens an explicit-commit turn on a gateway-relay session (relay
// substitute for the managed-room turn protocol).
func (m *SessionManager) StartTurn(connID, sessionID string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if sess.state == stateListening {
		m.mu.Unlock()
		return nil, fmt.Errorf("talk session %q already has an open turn", sessionID)
	}
	sess.turnSeq++
	sess.state = stateListening
	turn := sess.turnSeq
	m.mu.Unlock()
	m.emitState(sess)
	return map[string]any{"sessionId": sessionID, "turn": turn, "state": string(stateListening)}, nil
}

// EndTurn commits an open turn and moves the session into processing.
func (m *SessionManager) EndTurn(connID, sessionID string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if sess.state != stateListening {
		m.mu.Unlock()
		return nil, fmt.Errorf("talk session %q has no open turn", sessionID)
	}
	sess.state = stateProcessing
	turn := sess.turnSeq
	m.mu.Unlock()
	m.emitState(sess)
	return map[string]any{"sessionId": sessionID, "turn": turn, "state": string(stateProcessing)}, nil
}

// AppendAudio forwards a decoded audio chunk to the bound provider transport.
func (m *SessionManager) AppendAudio(connID, sessionID, audioBase64 string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(audioBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid audioBase64")
	}
	switch {
	case sess.bridge != nil:
		if err := sess.bridge.SendAudio(data); err != nil {
			return nil, err
		}
	case sess.sttSess != nil:
		if err := sess.sttSess.SendAudio(data); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: talk session %q has no audio transport", ErrUnavailable, sessionID)
	}
	return map[string]any{"sessionId": sessionID, "bytes": len(data)}, nil
}

// CancelTurn interrupts the in-flight turn and returns the session to idle.
func (m *SessionManager) CancelTurn(connID, sessionID string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.bridge != nil {
		_ = sess.bridge.Interrupt()
	}
	m.mu.Lock()
	sess.state = stateIdle
	m.mu.Unlock()
	m.emitState(sess)
	return map[string]any{"sessionId": sessionID, "state": string(stateIdle)}, nil
}

// CancelOutput interrupts model audio output without ending the turn.
func (m *SessionManager) CancelOutput(connID, sessionID string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.bridge != nil {
		_ = sess.bridge.Interrupt()
	}
	return map[string]any{"sessionId": sessionID, "cancelled": true}, nil
}

// AcknowledgeMark records a client-side playback-mark acknowledgement.
func (m *SessionManager) AcknowledgeMark(connID, sessionID, mark string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	sess.marks[mark] = true
	pending := 0
	for _, acked := range sess.marks {
		if !acked {
			pending++
		}
	}
	m.mu.Unlock()
	return map[string]any{"sessionId": sessionID, "mark": mark, "acknowledged": true, "pending": pending}, nil
}

// SubmitToolResult forwards a realtime tool-call result to the voice bridge.
func (m *SessionManager) SubmitToolResult(connID, sessionID, toolCallID string, result json.RawMessage, callErr string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.bridge == nil {
		return nil, fmt.Errorf("%w: talk session %q has no realtime tool bridge", ErrUnsupported, sessionID)
	}
	envelope := map[string]any{"type": "tool_result", "toolCallId": toolCallID}
	if callErr != "" {
		envelope["error"] = callErr
	} else if len(result) > 0 {
		envelope["result"] = json.RawMessage(result)
	}
	payload, _ := json.Marshal(envelope)
	if err := sess.bridge.SendText(string(payload)); err != nil {
		return nil, err
	}
	return map[string]any{"sessionId": sessionID, "toolCallId": toolCallID, "submitted": true}, nil
}

// Steer enqueues steering text for the session's active agent run.
func (m *SessionManager) Steer(connID, sessionID, text string) (map[string]any, error) {
	if _, err := m.owned(connID, sessionID); err != nil {
		return nil, err
	}
	if m.steering == nil {
		return nil, fmt.Errorf("%w: steering mailboxes not configured", ErrUnavailable)
	}
	accepted := m.steering.Get(sessionID).Enqueue(autoreply.SteeringMessage{
		Text:   text,
		Source: "talk",
	})
	return map[string]any{"sessionId": sessionID, "accepted": accepted}, nil
}

// Close tears down a session and its provider transport.
func (m *SessionManager) Close(connID, sessionID string) (map[string]any, error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.bridge != nil {
		_ = sess.bridge.Close()
	}
	if sess.sttSess != nil {
		_ = sess.sttSess.Close()
	}
	m.removeSession(sessionID)
	if m.steering != nil {
		m.steering.Delete(sessionID)
	}
	m.emit(connID, EventTalkSessionClosed, map[string]any{"sessionId": sessionID})
	return map[string]any{"sessionId": sessionID, "closed": true}, nil
}

// CloseConnection tears down every session owned by a disconnecting connection.
func (m *SessionManager) CloseConnection(connID string) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.byConn[connID]))
	for id := range m.byConn[connID] {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_, _ = m.Close(connID, id)
	}
}

// ListForConnection returns the session ids owned by a connection (sorted).
func (m *SessionManager) ListForConnection(connID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.byConn[connID]))
	for id := range m.byConn[connID] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *SessionManager) owned(connID, sessionID string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("unknown talk session %q", sessionID)
	}
	if sess.connID != connID {
		return nil, fmt.Errorf("talk session %q is not owned by this connection", sessionID)
	}
	return sess, nil
}

func (m *SessionManager) removeSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return
	}
	sess.state = stateClosed
	delete(m.sessions, sessionID)
	if set, ok := m.byConn[sess.connID]; ok {
		delete(set, sessionID)
		if len(set) == 0 {
			delete(m.byConn, sess.connID)
		}
	}
}

func (m *SessionManager) emitState(sess *session) {
	m.mu.Lock()
	state := string(sess.state)
	turn := sess.turnSeq
	m.mu.Unlock()
	m.emit(sess.connID, EventTalkSessionState, map[string]any{
		"sessionId": sess.id, "state": state, "turn": turn,
	})
}

func (m *SessionManager) emit(connID, event string, payload any) {
	if m.emitter == nil || connID == "" {
		return
	}
	m.emitter.EmitTo(connID, event, payload)
}
