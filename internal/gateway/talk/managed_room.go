package talk

import (
	"context"
	"encoding/base64"
	"fmt"
)

const ManagedRoomProtocolVersion = "metiq.talk.managed-room.v1"

const managedRoomEventWindow = 4096

const (
	ManagedRoomCommandTurnStart        = "turn.start"
	ManagedRoomCommandTurnEnd          = "turn.end"
	ManagedRoomCommandTurnCancel       = "turn.cancel"
	ManagedRoomCommandOutputCancel     = "output.cancel"
	ManagedRoomCommandMarkAcknowledge  = "mark.acknowledge"
	ManagedRoomCommandToolResultSubmit = "tool-result.submit"
)

const (
	ManagedRoomEventTurnStarted    = "turn.started"
	ManagedRoomEventInputCommitted = "turn.input-committed"
	ManagedRoomEventTurnCompleted  = "turn.completed"
	ManagedRoomEventTurnCancelled  = "turn.cancelled"
	ManagedRoomEventTranscript     = "transcript"
	ManagedRoomEventAudio          = "audio"
	ManagedRoomEventClosed         = "room.closed"
	ManagedRoomEventError          = "room.error"
)

// ManagedRoomTransport is the daemon-side control-plane seam for a LiveKit-like
// room service. Create/Join may perform provisioning; PublishTurn must publish a
// command to the room's event stream rather than poll for completion.
type ManagedRoomTransport interface {
	CreateRoom(ctx context.Context, req ManagedRoomCreateRequest) (ManagedRoom, error)
	JoinRoom(ctx context.Context, req ManagedRoomJoinRequest) (ManagedRoomJoin, error)
	PublishTurn(command ManagedRoomTurnCommand) error
	CloseRoom(roomID string) error
}

type ManagedRoomCreateRequest struct {
	Protocol     string
	SessionID    string
	OwnerConnID  string
	Mode         string
	Provider     string
	Voice        string
	Language     string
	SystemPrompt string
	OnEvent      func(ManagedRoomEvent)
}

type ManagedRoom struct {
	RoomID   string
	RoomURL  string
	Provider string
}

type ManagedRoomJoinRequest struct {
	Protocol      string
	SessionID     string
	RoomID        string
	ParticipantID string
}

type ManagedRoomJoin struct {
	RoomID    string
	RoomURL   string
	Token     string
	ExpiresAt int64
}

type ManagedRoomTurnCommand struct {
	Protocol  string
	Type      string
	SessionID string
	RoomID    string
	Turn      int
	Payload   map[string]any
}

type ManagedRoomEvent struct {
	Protocol string
	EventID  string
	Type     string
	Turn     int
	State    string
	Text     string
	Role     string
	Final    bool
	Audio    []byte
	Format   string
	Error    string
	Payload  map[string]any
}

// SetManagedRoomTransport installs the optional managed-room control plane.
// Daemons without LiveKit infrastructure leave it nil and fail honestly.
func (m *SessionManager) SetManagedRoomTransport(transport ManagedRoomTransport) {
	m.mu.Lock()
	m.managedRoom = transport
	m.mu.Unlock()
}

func (m *SessionManager) managedRoomTransport() ManagedRoomTransport {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.managedRoom
}

func (m *SessionManager) bindManagedRoom(ctx context.Context, sess *session, in CreateInput) error {
	transport := m.managedRoomTransport()
	if transport == nil {
		return fmt.Errorf("%w: managed-room transport is not configured", ErrUnavailable)
	}
	room, err := transport.CreateRoom(ctx, ManagedRoomCreateRequest{
		Protocol:     ManagedRoomProtocolVersion,
		SessionID:    sess.id,
		OwnerConnID:  sess.connID,
		Mode:         sess.mode,
		Provider:     in.Provider,
		Voice:        in.Voice,
		Language:     in.Language,
		SystemPrompt: in.SystemPrompt,
		OnEvent: func(event ManagedRoomEvent) {
			m.HandleManagedRoomEvent(sess.id, event)
		},
	})
	if err != nil {
		return fmt.Errorf("%w: create managed room: %v", ErrUnavailable, err)
	}
	if room.RoomID == "" || room.RoomURL == "" {
		if room.RoomID != "" {
			_ = transport.CloseRoom(room.RoomID)
		}
		return fmt.Errorf("%w: managed-room transport returned incomplete room metadata", ErrUnavailable)
	}
	m.mu.Lock()
	if current := m.sessions[sess.id]; current == sess {
		roomCopy := room
		sess.room = &roomCopy
		if sess.provider == "" {
			sess.provider = room.Provider
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *SessionManager) joinManagedRoom(ctx context.Context, sess *session) (map[string]any, error) {
	transport := m.managedRoomTransport()
	m.mu.Lock()
	var room ManagedRoom
	if sess.room != nil {
		room = *sess.room
	}
	state := sess.state
	m.mu.Unlock()
	if transport == nil || room.RoomID == "" {
		return nil, fmt.Errorf("%w: managed-room transport is not configured", ErrUnavailable)
	}
	joined, err := transport.JoinRoom(ctx, ManagedRoomJoinRequest{
		Protocol:      ManagedRoomProtocolVersion,
		SessionID:     sess.id,
		RoomID:        room.RoomID,
		ParticipantID: sess.connID,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: join managed room: %v", ErrUnavailable, err)
	}
	if joined.RoomID == "" {
		joined.RoomID = room.RoomID
	}
	if joined.RoomURL == "" {
		joined.RoomURL = room.RoomURL
	}
	out := map[string]any{
		"sessionId": sess.id,
		"transport": TransportManagedRoom,
		"roomId":    joined.RoomID,
		"roomUrl":   joined.RoomURL,
		"state":     string(state),
	}
	if joined.Token != "" {
		out["token"] = joined.Token
	}
	if joined.ExpiresAt > 0 {
		out["expiresAt"] = joined.ExpiresAt
	}
	return out, nil
}

func (m *SessionManager) publishManagedRoomTurn(sess *session, commandType string, turn int, payload map[string]any) error {
	transport := m.managedRoomTransport()
	m.mu.Lock()
	roomID := ""
	if sess.room != nil {
		roomID = sess.room.RoomID
	}
	m.mu.Unlock()
	if transport == nil || roomID == "" {
		return fmt.Errorf("%w: managed-room transport is not configured", ErrUnavailable)
	}
	if err := transport.PublishTurn(ManagedRoomTurnCommand{
		Protocol:  ManagedRoomProtocolVersion,
		Type:      commandType,
		SessionID: sess.id,
		RoomID:    roomID,
		Turn:      turn,
		Payload:   payload,
	}); err != nil {
		return fmt.Errorf("%w: publish managed-room %s: %v", ErrUnavailable, commandType, err)
	}
	return nil
}

// HandleManagedRoomEvent applies one event-stream update. Event IDs are deduped,
// stale turn events are ignored, and only the owning connection receives output.
func (m *SessionManager) HandleManagedRoomEvent(sessionID string, event ManagedRoomEvent) bool {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok || sess.transport != TransportManagedRoom || sess.state == stateClosed {
		m.mu.Unlock()
		return false
	}
	if event.Protocol != ManagedRoomProtocolVersion || event.EventID == "" || !validManagedRoomEventType(event.Type) {
		m.mu.Unlock()
		return false
	}
	if sess.roomEvents[event.EventID] {
		m.mu.Unlock()
		return false
	}
	if event.Turn > 0 && event.Turn < sess.turnSeq {
		m.mu.Unlock()
		return false
	}
	if eventRequiresTurn(event.Type) && event.Turn <= 0 {
		m.mu.Unlock()
		return false
	}
	previousTurn := sess.turnSeq
	previousState := sess.state
	if !validManagedRoomTransition(event, previousTurn, previousState) {
		m.mu.Unlock()
		return false
	}
	sess.roomEvents[event.EventID] = true
	sess.roomEventOrder = append(sess.roomEventOrder, event.EventID)
	if len(sess.roomEventOrder) > managedRoomEventWindow {
		delete(sess.roomEvents, sess.roomEventOrder[0])
		sess.roomEventOrder = sess.roomEventOrder[1:]
	}
	if event.Turn > sess.turnSeq {
		sess.turnSeq = event.Turn
	}
	connID := sess.connID
	turn := sess.turnSeq
	roomID := ""
	if sess.room != nil {
		roomID = sess.room.RoomID
	}
	stateChanged := false
	switch event.Type {
	case ManagedRoomEventTurnStarted:
		sess.state = stateListening
		stateChanged = true
	case ManagedRoomEventInputCommitted:
		sess.state = stateProcessing
		stateChanged = true
	case ManagedRoomEventTurnCompleted, ManagedRoomEventTurnCancelled:
		sess.state = stateIdle
		stateChanged = true
	}
	state := string(sess.state)
	m.mu.Unlock()

	base := map[string]any{"sessionId": sessionID, "roomId": roomID, "turn": turn}
	for key, value := range event.Payload {
		base[key] = value
	}
	switch event.Type {
	case ManagedRoomEventTranscript:
		base["text"] = event.Text
		base["role"] = event.Role
		base["final"] = event.Final
		m.emit(connID, EventTalkSessionTranscript, base)
	case ManagedRoomEventAudio:
		base["audioBase64"] = base64.StdEncoding.EncodeToString(event.Audio)
		base["format"] = event.Format
		m.emit(connID, EventTalkSessionAudio, base)
	case ManagedRoomEventClosed:
		if m.removeSession(sessionID) {
			m.emit(connID, EventTalkSessionClosed, base)
		}
	case ManagedRoomEventError:
		base["state"] = state
		base["error"] = event.Error
		m.emit(connID, EventTalkSessionState, base)
	default:
		if stateChanged {
			base["state"] = state
			m.emit(connID, EventTalkSessionState, base)
		}
	}
	return true
}

func validManagedRoomEventType(eventType string) bool {
	switch eventType {
	case ManagedRoomEventTurnStarted,
		ManagedRoomEventInputCommitted,
		ManagedRoomEventTurnCompleted,
		ManagedRoomEventTurnCancelled,
		ManagedRoomEventTranscript,
		ManagedRoomEventAudio,
		ManagedRoomEventClosed,
		ManagedRoomEventError:
		return true
	default:
		return false
	}
}

func eventRequiresTurn(eventType string) bool {
	switch eventType {
	case ManagedRoomEventClosed, ManagedRoomEventError:
		return false
	default:
		return true
	}
}

func validManagedRoomTransition(event ManagedRoomEvent, previousTurn int, previousState sessionState) bool {
	if event.Turn > previousTurn {
		return true
	}
	switch event.Type {
	case ManagedRoomEventTurnStarted:
		return previousState == stateListening
	case ManagedRoomEventInputCommitted:
		return previousState == stateListening || previousState == stateProcessing
	case ManagedRoomEventTurnCompleted, ManagedRoomEventTurnCancelled:
		return previousState == stateListening || previousState == stateProcessing
	default:
		return true
	}
}
