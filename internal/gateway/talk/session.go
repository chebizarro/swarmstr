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
	id             string
	connID         string
	mode           string
	transport      string
	provider       string
	state          sessionState
	turnSeq        int
	marks          map[string]bool
	roomEvents     map[string]bool
	roomEventOrder []string
	room           *ManagedRoom
	opMu           sync.Mutex
	bridge         realtimevoice.Bridge
	sttSess        realtimestt.Session
}

// SessionManager owns talk sessions keyed by the owning WS connection. Gateway
// relay sessions bind local realtimevoice/realtimestt providers; managed rooms
// delegate provisioning and their event-driven turn protocol to an optional
// ManagedRoomTransport.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*session
	byConn   map[string]map[string]struct{}
	seq      int

	voice       *realtimevoice.Registry
	stt         *realtimestt.Registry
	steering    *autoreply.SteeringMailboxRegistry
	emitter     EventEmitter
	managedRoom ManagedRoomTransport
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

// Create opens a new server-owned voice session. Gateway-relay resolves a local
// audio provider; managed-room provisions through the configured room transport.
func (m *SessionManager) Create(ctx context.Context, in CreateInput) (map[string]any, error) {
	if in.ConnID == "" {
		return nil, fmt.Errorf("talk.session.create requires an owning connection")
	}
	switch in.Transport {
	case TransportGatewayRelay:
	case TransportManagedRoom:
		if m.managedRoomTransport() == nil {
			return nil, fmt.Errorf("%w: managed-room transport is not configured", ErrUnavailable)
		}
	default:
		return nil, fmt.Errorf("unsupported talk transport %q", in.Transport)
	}

	sess := &session{
		connID:     in.ConnID,
		mode:       in.Mode,
		transport:  in.Transport,
		state:      stateIdle,
		marks:      map[string]bool{},
		roomEvents: map[string]bool{},
	}

	switch in.Mode {
	case ModeRealtime, ModeTranscription, ModeSTTTTS:
	default:
		return nil, fmt.Errorf("unsupported talk mode %q", in.Mode)
	}
	if in.Transport == TransportManagedRoom {
		sess.provider = in.Provider
	} else {
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
		}
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

	// Bind after the record exists so provider callbacks can target the owner.
	// A binding failure tears the record down and surfaces honestly.
	var bindErr error
	if sess.transport == TransportManagedRoom {
		bindErr = m.bindManagedRoom(ctx, sess, in)
	} else {
		bindErr = m.bind(ctx, sess, in)
	}
	if bindErr != nil {
		m.removeSession(id)
		return nil, bindErr
	}

	out := map[string]any{
		"sessionId": id,
		"mode":      sess.mode,
		"transport": sess.transport,
		"provider":  sess.provider,
		"state":     string(sess.state),
	}
	if sess.room != nil {
		out["roomId"] = sess.room.RoomID
		out["roomUrl"] = sess.room.RoomURL
	}
	return out, nil
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

// Join returns a participant token for a managed-room session.
func (m *SessionManager) Join(connID, sessionID string) (map[string]any, error) {
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if sess.transport != TransportManagedRoom {
		return nil, fmt.Errorf("%w: talk.session.join is managed-room only", ErrUnsupported)
	}
	return m.joinManagedRoom(context.Background(), sess)
}

// StartTurn opens an explicit-commit turn. Managed-room sessions publish a
// versioned turn.start command and wait for room events for media/results.
func (m *SessionManager) StartTurn(connID, sessionID string) (map[string]any, error) {
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m.mu.Lock()
	if sess.state == stateListening {
		m.mu.Unlock()
		return nil, fmt.Errorf("talk session %q already has an open turn", sessionID)
	}
	previousState := sess.state
	sess.turnSeq++
	sess.state = stateListening
	turn := sess.turnSeq
	m.mu.Unlock()
	if sess.transport == TransportManagedRoom {
		if err := m.publishManagedRoomTurn(sess, ManagedRoomCommandTurnStart, turn, nil); err != nil {
			m.mu.Lock()
			if sess.turnSeq == turn && sess.state == stateListening {
				sess.turnSeq--
				sess.state = previousState
			}
			m.mu.Unlock()
			return nil, err
		}
	}
	m.emitState(sess)
	return map[string]any{"sessionId": sessionID, "turn": turn, "state": string(stateListening)}, nil
}

// EndTurn commits an open turn and moves the session into processing.
func (m *SessionManager) EndTurn(connID, sessionID string) (map[string]any, error) {
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m.mu.Lock()
	if sess.state != stateListening {
		m.mu.Unlock()
		return nil, fmt.Errorf("talk session %q has no open turn", sessionID)
	}
	sess.state = stateProcessing
	turn := sess.turnSeq
	m.mu.Unlock()
	if sess.transport == TransportManagedRoom {
		if err := m.publishManagedRoomTurn(sess, ManagedRoomCommandTurnEnd, turn, nil); err != nil {
			m.mu.Lock()
			if sess.turnSeq == turn && sess.state == stateProcessing {
				sess.state = stateListening
			}
			m.mu.Unlock()
			return nil, err
		}
	}
	m.emitState(sess)
	return map[string]any{"sessionId": sessionID, "turn": turn, "state": string(stateProcessing)}, nil
}

// AppendAudio forwards a decoded audio chunk to the bound provider transport.
func (m *SessionManager) AppendAudio(connID, sessionID, audioBase64 string) (map[string]any, error) {
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if sess.transport == TransportManagedRoom {
		return nil, fmt.Errorf("%w: managed-room media travels over the room transport", ErrUnsupported)
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
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m.mu.Lock()
	turn := sess.turnSeq
	m.mu.Unlock()
	if sess.transport == TransportManagedRoom {
		if err := m.publishManagedRoomTurn(sess, ManagedRoomCommandTurnCancel, turn, nil); err != nil {
			return nil, err
		}
	} else if sess.bridge != nil {
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
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m.mu.Lock()
	turn := sess.turnSeq
	m.mu.Unlock()
	if sess.transport == TransportManagedRoom {
		if err := m.publishManagedRoomTurn(sess, ManagedRoomCommandOutputCancel, turn, nil); err != nil {
			return nil, err
		}
	} else if sess.bridge != nil {
		_ = sess.bridge.Interrupt()
	}
	return map[string]any{"sessionId": sessionID, "cancelled": true}, nil
}

// AcknowledgeMark records a client-side playback-mark acknowledgement.
func (m *SessionManager) AcknowledgeMark(connID, sessionID, mark string) (map[string]any, error) {
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if sess.transport == TransportManagedRoom {
		m.mu.Lock()
		turn := sess.turnSeq
		m.mu.Unlock()
		if err := m.publishManagedRoomTurn(sess, ManagedRoomCommandMarkAcknowledge, turn, map[string]any{"mark": mark}); err != nil {
			return nil, err
		}
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
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	envelope := map[string]any{"type": "tool_result", "toolCallId": toolCallID}
	if callErr != "" {
		envelope["error"] = callErr
	} else if len(result) > 0 {
		envelope["result"] = json.RawMessage(result)
	}
	if sess.transport == TransportManagedRoom {
		m.mu.Lock()
		turn := sess.turnSeq
		m.mu.Unlock()
		if err := m.publishManagedRoomTurn(sess, ManagedRoomCommandToolResultSubmit, turn, envelope); err != nil {
			return nil, err
		}
	} else {
		if sess.bridge == nil {
			return nil, fmt.Errorf("%w: talk session %q has no realtime tool bridge", ErrUnsupported, sessionID)
		}
		payload, _ := json.Marshal(envelope)
		if err := sess.bridge.SendText(string(payload)); err != nil {
			return nil, err
		}
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
	sess, unlock, err := m.lockOwned(connID, sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if sess.bridge != nil {
		_ = sess.bridge.Close()
	}
	if sess.sttSess != nil {
		_ = sess.sttSess.Close()
	}
	if sess.transport == TransportManagedRoom {
		transport := m.managedRoomTransport()
		m.mu.Lock()
		roomID := ""
		if sess.room != nil {
			roomID = sess.room.RoomID
		}
		m.mu.Unlock()
		if transport == nil || roomID == "" {
			return nil, fmt.Errorf("%w: managed-room transport is not configured", ErrUnavailable)
		}
		if err := transport.CloseRoom(roomID); err != nil {
			return nil, fmt.Errorf("%w: close managed room: %v", ErrUnavailable, err)
		}
	}
	removed := m.removeSession(sessionID)
	if m.steering != nil {
		m.steering.Delete(sessionID)
	}
	if removed {
		m.emit(connID, EventTalkSessionClosed, map[string]any{"sessionId": sessionID})
	}
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

func (m *SessionManager) lockOwned(connID, sessionID string) (*session, func(), error) {
	sess, err := m.owned(connID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	sess.opMu.Lock()
	m.mu.Lock()
	current, ok := m.sessions[sessionID]
	valid := ok && current == sess && sess.connID == connID && sess.state != stateClosed
	m.mu.Unlock()
	if !valid {
		sess.opMu.Unlock()
		return nil, nil, fmt.Errorf("unknown talk session %q", sessionID)
	}
	return sess, sess.opMu.Unlock, nil
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

func (m *SessionManager) removeSession(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return false
	}
	sess.state = stateClosed
	delete(m.sessions, sessionID)
	if set, ok := m.byConn[sess.connID]; ok {
		delete(set, sessionID)
		if len(set) == 0 {
			delete(m.byConn, sess.connID)
		}
	}
	return true
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
