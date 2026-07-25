package talk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"metiq/internal/autoreply"
)

func newSteering() *autoreply.SteeringMailboxRegistry {
	return autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
}

func TestSessionRealtimeLifecycle(t *testing.T) {
	voice := &fakeVoiceProvider{id: "vp", configured: true}
	em := &fakeEmitter{}
	mgr := NewSessionManager(voiceRegistryWith(voice), nil, newSteering(), em)

	out, err := mgr.Create(context.Background(), CreateInput{ConnID: "conn1", Mode: ModeRealtime, Transport: TransportGatewayRelay})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sid, _ := out["sessionId"].(string)
	if sid == "" {
		t.Fatal("missing sessionId")
	}
	if voice.bridge == nil {
		t.Fatal("bridge should have been created")
	}

	// Turn lifecycle: start -> append -> end.
	if _, err := mgr.StartTurn("conn1", sid); err != nil {
		t.Fatalf("startTurn: %v", err)
	}
	// Double start rejected.
	if _, err := mgr.StartTurn("conn1", sid); err == nil {
		t.Fatal("double startTurn should error")
	}
	chunk := base64.StdEncoding.EncodeToString([]byte("pcm"))
	if _, err := mgr.AppendAudio("conn1", sid, chunk); err != nil {
		t.Fatalf("appendAudio: %v", err)
	}
	if len(voice.bridge.audio) != 1 {
		t.Fatalf("want 1 audio chunk forwarded, got %d", len(voice.bridge.audio))
	}
	if _, err := mgr.EndTurn("conn1", sid); err != nil {
		t.Fatalf("endTurn: %v", err)
	}

	// Cancel interrupts the bridge.
	if _, err := mgr.CancelTurn("conn1", sid); err != nil {
		t.Fatalf("cancelTurn: %v", err)
	}
	if _, err := mgr.CancelOutput("conn1", sid); err != nil {
		t.Fatalf("cancelOutput: %v", err)
	}
	if voice.bridge.interrupted != 2 {
		t.Fatalf("want 2 interrupts, got %d", voice.bridge.interrupted)
	}

	// Marks + tool result + steer.
	if _, err := mgr.AcknowledgeMark("conn1", sid, "m1"); err != nil {
		t.Fatalf("acknowledgeMark: %v", err)
	}
	if _, err := mgr.SubmitToolResult("conn1", sid, "call1", json.RawMessage(`{"ok":true}`), ""); err != nil {
		t.Fatalf("submitToolResult: %v", err)
	}
	if len(voice.bridge.text) != 1 {
		t.Fatalf("want tool result forwarded as text, got %d", len(voice.bridge.text))
	}
	if _, err := mgr.Steer("conn1", sid, "focus on X"); err != nil {
		t.Fatalf("steer: %v", err)
	}

	// Close tears down the bridge and emits closed.
	if _, err := mgr.Close("conn1", sid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !voice.bridge.closed {
		t.Fatal("bridge should be closed")
	}
	if em.count(EventTalkSessionClosed) != 1 {
		t.Fatalf("want 1 closed event, got %d", em.count(EventTalkSessionClosed))
	}
	if _, err := mgr.StartTurn("conn1", sid); err == nil {
		t.Fatal("operations on a closed session should error")
	}
}

func TestSessionCloseConnectionReclaims(t *testing.T) {
	voice := &fakeVoiceProvider{id: "vp", configured: true}
	mgr := NewSessionManager(voiceRegistryWith(voice), nil, newSteering(), &fakeEmitter{})
	out, _ := mgr.Create(context.Background(), CreateInput{ConnID: "conn1", Mode: ModeRealtime, Transport: TransportGatewayRelay})
	sid := out["sessionId"].(string)
	if len(mgr.ListForConnection("conn1")) != 1 {
		t.Fatal("session should be owned by conn1")
	}
	mgr.CloseConnection("conn1")
	if len(mgr.ListForConnection("conn1")) != 0 {
		t.Fatal("CloseConnection should drop all owned sessions")
	}
	if !voice.bridge.closed {
		t.Fatal("bridge should be closed on connection drop")
	}
	if _, err := mgr.StartTurn("conn1", sid); err == nil {
		t.Fatal("session should be gone after CloseConnection")
	}
}

func TestSessionOwnershipIsolation(t *testing.T) {
	voice := &fakeVoiceProvider{id: "vp", configured: true}
	mgr := NewSessionManager(voiceRegistryWith(voice), nil, newSteering(), &fakeEmitter{})
	out, _ := mgr.Create(context.Background(), CreateInput{ConnID: "owner", Mode: ModeRealtime, Transport: TransportGatewayRelay})
	sid := out["sessionId"].(string)
	if _, err := mgr.StartTurn("intruder", sid); err == nil {
		t.Fatal("another connection must not drive the session")
	}
}

func TestSessionTranscriptionMode(t *testing.T) {
	stt := &fakeSTTProvider{id: "sp", configured: true}
	mgr := NewSessionManager(nil, sttRegistryWith(stt), newSteering(), &fakeEmitter{})
	out, err := mgr.Create(context.Background(), CreateInput{ConnID: "c", Mode: ModeTranscription, Transport: TransportGatewayRelay})
	if err != nil {
		t.Fatalf("create transcription: %v", err)
	}
	sid := out["sessionId"].(string)
	chunk := base64.StdEncoding.EncodeToString([]byte("pcm"))
	if _, err := mgr.AppendAudio("c", sid, chunk); err != nil {
		t.Fatalf("appendAudio: %v", err)
	}
	if stt.session == nil || len(stt.session.audio) != 1 {
		t.Fatal("audio should reach the stt session")
	}
}

func TestSessionUnavailableAndUnsupported(t *testing.T) {
	// No registries wired -> honest ErrUnavailable.
	mgr := NewSessionManager(nil, nil, newSteering(), &fakeEmitter{})
	_, err := mgr.Create(context.Background(), CreateInput{ConnID: "c", Mode: ModeRealtime, Transport: TransportGatewayRelay})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}

	// managed-room transport -> accepted deviation ErrUnsupported.
	voice := &fakeVoiceProvider{id: "vp", configured: true}
	mgr2 := NewSessionManager(voiceRegistryWith(voice), nil, newSteering(), &fakeEmitter{})
	_, err = mgr2.Create(context.Background(), CreateInput{ConnID: "c", Mode: ModeRealtime, Transport: TransportManagedRoom})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported for managed-room, got %v", err)
	}

	// join is managed-room only -> ErrUnsupported.
	if _, err := mgr2.Join("c", "whatever"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported for join, got %v", err)
	}
}

func TestClientStoreLifecycle(t *testing.T) {
	provider := &fakeBrowserVoiceProvider{fakeVoiceProvider: fakeVoiceProvider{id: "vp", configured: true}}
	steering := newSteering()
	store := NewClientStore(voiceRegistryWith(provider), steering)

	out, err := store.Create(context.Background(), ClientCreateInput{Transport: "webrtc", Voice: "alloy"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sid := out["sessionId"].(string)
	if out["browserSession"] == nil {
		t.Fatal("want browserSession payload")
	}

	// Resume returns the same record.
	resumed, err := store.Create(context.Background(), ClientCreateInput{SessionID: sid, Transport: "webrtc"})
	if err != nil || resumed["resumed"] != true {
		t.Fatalf("resume: %v (%v)", err, resumed)
	}

	// Transcript appends.
	tr, err := store.Transcript(sid, TranscriptEntry{Role: "user", Text: "hi", Final: true})
	if err != nil || tr["entries"].(int) != 1 {
		t.Fatalf("transcript: %v (%v)", err, tr)
	}

	// ToolCall bridges into a run via the injected bridge.
	called := false
	bridge := func(ctx context.Context, in ToolCallInput) (string, error) {
		called = true
		if in.SessionID != sid || in.Tool != "consult" {
			t.Fatalf("bridge got unexpected input: %+v", in)
		}
		return "run-123", nil
	}
	tc, err := store.ToolCall(context.Background(), ToolCallInput{SessionID: sid, Tool: "consult", ToolCallID: "tc1"}, bridge)
	if err != nil || !called || tc["runId"] != "run-123" {
		t.Fatalf("toolCall: %v (%v)", err, tc)
	}

	// Steer targets the active run id and enqueues.
	st, err := store.Steer(sid, "refocus")
	if err != nil || st["accepted"] != true {
		t.Fatalf("steer: %v (%v)", err, st)
	}
	if st["runId"] != "run-123" {
		t.Fatalf("steer should target active run, got %v", st["runId"])
	}

	// Close removes the record.
	if _, err := store.Close(sid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := store.Transcript(sid, TranscriptEntry{Text: "x"}); err == nil {
		t.Fatal("transcript on closed session should error")
	}
}

func TestClientCreateUnavailable(t *testing.T) {
	// No provider registered -> honest ErrUnavailable.
	store := NewClientStore(nil, newSteering())
	if _, err := store.Create(context.Background(), ClientCreateInput{Transport: "webrtc"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable (no registry), got %v", err)
	}

	// Provider present but without browser-session support -> ErrUnavailable.
	plain := &fakeVoiceProvider{id: "vp", configured: true}
	store2 := NewClientStore(voiceRegistryWith(plain), newSteering())
	if _, err := store2.Create(context.Background(), ClientCreateInput{Transport: "webrtc"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable (no browser support), got %v", err)
	}

	// ToolCall without a bridge -> ErrUnavailable.
	browser := &fakeBrowserVoiceProvider{fakeVoiceProvider: fakeVoiceProvider{id: "vp", configured: true}}
	store3 := NewClientStore(voiceRegistryWith(browser), newSteering())
	out, _ := store3.Create(context.Background(), ClientCreateInput{Transport: "webrtc"})
	sid := out["sessionId"].(string)
	if _, err := store3.ToolCall(context.Background(), ToolCallInput{SessionID: sid, Tool: "x"}, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable (no bridge), got %v", err)
	}
}
