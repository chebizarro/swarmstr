package talk

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
)

type fakeManagedRoomTransport struct {
	createReq         ManagedRoomCreateRequest
	joinReq           ManagedRoomJoinRequest
	commands          []ManagedRoomTurnCommand
	closed            []string
	publishErr        error
	emitClosedOnClose bool
}

func (f *fakeManagedRoomTransport) CreateRoom(_ context.Context, req ManagedRoomCreateRequest) (ManagedRoom, error) {
	f.createReq = req
	return ManagedRoom{RoomID: "room-1", RoomURL: "wss://livekit.example", Provider: "livekit"}, nil
}

func (f *fakeManagedRoomTransport) JoinRoom(_ context.Context, req ManagedRoomJoinRequest) (ManagedRoomJoin, error) {
	f.joinReq = req
	return ManagedRoomJoin{Token: "participant-token", ExpiresAt: 12345}, nil
}

func (f *fakeManagedRoomTransport) PublishTurn(command ManagedRoomTurnCommand) error {
	if f.publishErr != nil {
		err := f.publishErr
		f.publishErr = nil
		return err
	}
	f.commands = append(f.commands, command)
	return nil
}

func (f *fakeManagedRoomTransport) CloseRoom(roomID string) error {
	f.closed = append(f.closed, roomID)
	if f.emitClosedOnClose {
		f.emit(ManagedRoomEvent{
			Protocol: ManagedRoomProtocolVersion,
			EventID:  "close-event",
			Type:     ManagedRoomEventClosed,
		})
	}
	return nil
}

func (f *fakeManagedRoomTransport) emit(event ManagedRoomEvent) {
	if f.createReq.OnEvent != nil {
		f.createReq.OnEvent(event)
	}
}

func newManagedRoomSession(t *testing.T) (*SessionManager, *fakeManagedRoomTransport, *fakeEmitter, string) {
	t.Helper()
	transport := &fakeManagedRoomTransport{}
	emitter := &fakeEmitter{}
	mgr := NewSessionManager(nil, nil, newSteering(), emitter)
	mgr.SetManagedRoomTransport(transport)
	out, err := mgr.Create(context.Background(), CreateInput{
		ConnID:       "owner",
		SessionID:    "managed-1",
		Mode:         ModeSTTTTS,
		Transport:    TransportManagedRoom,
		Voice:        "marin",
		Language:     "en",
		SystemPrompt: "be concise",
	})
	if err != nil {
		t.Fatalf("create managed room: %v", err)
	}
	if got := out["roomId"]; got != "room-1" {
		t.Fatalf("roomId = %v", got)
	}
	if got := out["roomUrl"]; got != "wss://livekit.example" {
		t.Fatalf("roomUrl = %v", got)
	}
	if got := out["provider"]; got != "livekit" {
		t.Fatalf("provider = %v", got)
	}
	return mgr, transport, emitter, out["sessionId"].(string)
}

func TestManagedRoomCreateJoinAndTurnProtocol(t *testing.T) {
	mgr, transport, _, sessionID := newManagedRoomSession(t)
	if transport.createReq.Protocol != ManagedRoomProtocolVersion || transport.createReq.SessionID != sessionID {
		t.Fatalf("create request = %#v", transport.createReq)
	}
	if transport.createReq.OwnerConnID != "owner" || transport.createReq.Mode != ModeSTTTTS {
		t.Fatalf("create request routing = %#v", transport.createReq)
	}

	if _, err := mgr.Join("intruder", sessionID); err == nil {
		t.Fatal("non-owner join should fail")
	}
	joined, err := mgr.Join("owner", sessionID)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if joined["token"] != "participant-token" || joined["roomUrl"] != "wss://livekit.example" {
		t.Fatalf("join result = %#v", joined)
	}
	if transport.joinReq.Protocol != ManagedRoomProtocolVersion || transport.joinReq.RoomID != "room-1" {
		t.Fatalf("join request = %#v", transport.joinReq)
	}

	started, err := mgr.StartTurn("owner", sessionID)
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if started["turn"] != 1 || started["state"] != string(stateListening) {
		t.Fatalf("start result = %#v", started)
	}
	ended, err := mgr.EndTurn("owner", sessionID)
	if err != nil {
		t.Fatalf("end turn: %v", err)
	}
	if ended["turn"] != 1 || ended["state"] != string(stateProcessing) {
		t.Fatalf("end result = %#v", ended)
	}
	wantTypes := []string{ManagedRoomCommandTurnStart, ManagedRoomCommandTurnEnd}
	gotTypes := []string{transport.commands[0].Type, transport.commands[1].Type}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("command types = %v, want %v", gotTypes, wantTypes)
	}
	for _, command := range transport.commands {
		if command.Protocol != ManagedRoomProtocolVersion || command.SessionID != sessionID || command.RoomID != "room-1" || command.Turn != 1 {
			t.Fatalf("non-conforming command = %#v", command)
		}
	}

	chunk := base64.StdEncoding.EncodeToString([]byte("pcm"))
	if _, err := mgr.AppendAudio("owner", sessionID, chunk); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("managed-room appendAudio = %v, want ErrUnsupported", err)
	}
	if _, err := mgr.CancelOutput("owner", sessionID); err != nil {
		t.Fatalf("cancel output: %v", err)
	}
	if _, err := mgr.AcknowledgeMark("owner", sessionID, "m1"); err != nil {
		t.Fatalf("acknowledge mark: %v", err)
	}
	if _, err := mgr.SubmitToolResult("owner", sessionID, "call-1", []byte(`{"ok":true}`), ""); err != nil {
		t.Fatalf("submit tool result: %v", err)
	}
	if got := transport.commands[len(transport.commands)-1]; got.Type != ManagedRoomCommandToolResultSubmit || got.Payload["toolCallId"] != "call-1" {
		t.Fatalf("tool command = %#v", got)
	}
}

func TestManagedRoomEventsAreOwnedDedupedAndMonotonic(t *testing.T) {
	mgr, transport, emitter, sessionID := newManagedRoomSession(t)
	if _, err := mgr.StartTurn("owner", sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.EndTurn("owner", sessionID); err != nil {
		t.Fatal(err)
	}

	if mgr.HandleManagedRoomEvent(sessionID, ManagedRoomEvent{
		Protocol: ManagedRoomProtocolVersion,
		EventID:  "late-start",
		Type:     ManagedRoomEventTurnStarted,
		Turn:     1,
	}) {
		t.Fatal("turn.started must not regress a processing turn")
	}
	if mgr.HandleManagedRoomEvent(sessionID, ManagedRoomEvent{
		Protocol: ManagedRoomProtocolVersion,
		Type:     ManagedRoomEventTranscript,
		Turn:     1,
		Text:     "missing id",
	}) {
		t.Fatal("events without an id must be rejected")
	}

	transcript := ManagedRoomEvent{
		Protocol: ManagedRoomProtocolVersion,
		EventID:  "event-1",
		Type:     ManagedRoomEventTranscript,
		Turn:     1,
		Text:     "hello",
		Role:     "assistant",
		Final:    true,
	}
	transport.emit(transcript)
	transport.emit(transcript)
	if got := emitter.count(EventTalkSessionTranscript); got != 1 {
		t.Fatalf("transcript count = %d, want 1", got)
	}
	last := emitter.events[len(emitter.events)-1]
	if last.connID != "owner" {
		t.Fatalf("event delivered to %q", last.connID)
	}

	transport.emit(ManagedRoomEvent{
		Protocol: ManagedRoomProtocolVersion,
		EventID:  "event-2",
		Type:     ManagedRoomEventTurnCompleted,
		Turn:     1,
	})
	if _, err := mgr.StartTurn("owner", sessionID); err != nil {
		t.Fatalf("start second turn: %v", err)
	}
	before := emitter.count(EventTalkSessionTranscript)
	transport.emit(ManagedRoomEvent{
		Protocol: ManagedRoomProtocolVersion,
		EventID:  "stale-event",
		Type:     ManagedRoomEventTranscript,
		Turn:     1,
		Text:     "stale",
	})
	if got := emitter.count(EventTalkSessionTranscript); got != before {
		t.Fatalf("stale transcript was emitted: count %d -> %d", before, got)
	}
	if mgr.HandleManagedRoomEvent(sessionID, ManagedRoomEvent{Protocol: "wrong", EventID: "wrong-version", Type: ManagedRoomEventAudio, Turn: 2, Audio: []byte("audio")}) {
		t.Fatal("wrong protocol version should be rejected")
	}

	transport.emit(ManagedRoomEvent{
		Protocol: ManagedRoomProtocolVersion,
		EventID:  "audio-1",
		Type:     ManagedRoomEventAudio,
		Turn:     2,
		Audio:    []byte("audio"),
		Format:   "pcm16",
	})
	if got := emitter.count(EventTalkSessionAudio); got != 1 {
		t.Fatalf("audio count = %d, want 1", got)
	}
}

func TestManagedRoomPublishFailureRollsBackTurn(t *testing.T) {
	mgr, transport, _, sessionID := newManagedRoomSession(t)
	transport.publishErr = errors.New("relay unavailable")
	if _, err := mgr.StartTurn("owner", sessionID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("start error = %v, want ErrUnavailable", err)
	}
	started, err := mgr.StartTurn("owner", sessionID)
	if err != nil {
		t.Fatalf("retry start: %v", err)
	}
	if started["turn"] != 1 {
		t.Fatalf("retry turn = %v, want 1", started["turn"])
	}
	transport.publishErr = errors.New("relay unavailable")
	if _, err := mgr.EndTurn("owner", sessionID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("end error = %v, want ErrUnavailable", err)
	}
	if _, err := mgr.EndTurn("owner", sessionID); err != nil {
		t.Fatalf("retry end: %v", err)
	}
}

func TestManagedRoomCatalogReadiness(t *testing.T) {
	catalog := BuildCatalog(CatalogInput{ManagedRoom: true})
	transports := catalog["transports"].([]transportDescriptor)
	for _, transport := range transports {
		if transport.ID == TransportManagedRoom {
			if !transport.Ready || transport.Note != "" {
				t.Fatalf("managed-room descriptor = %#v", transport)
			}
			return
		}
	}
	t.Fatal("managed-room descriptor missing")
}

func TestManagedRoomCloseUsesTransport(t *testing.T) {
	mgr, transport, emitter, sessionID := newManagedRoomSession(t)
	transport.emitClosedOnClose = true
	if _, err := mgr.Close("owner", sessionID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !reflect.DeepEqual(transport.closed, []string{"room-1"}) {
		t.Fatalf("closed rooms = %v", transport.closed)
	}
	if got := emitter.count(EventTalkSessionClosed); got != 1 {
		t.Fatalf("closed event count = %d", got)
	}
	if got := mgr.ListForConnection("owner"); len(got) != 0 {
		t.Fatalf("sessions after close = %v", got)
	}
}
