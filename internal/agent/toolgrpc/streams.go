package toolgrpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	streamToolStart   = "start"
	streamToolSend    = "send"
	streamToolReceive = "receive"
	streamToolFinish  = "finish"

	streamStateOpen   = "open"
	streamStateClosed = "closed"
)

// StreamManager owns turn-scoped gRPC client streams. A manager is intended to
// be created for one agent turn and discarded after that turn. Each stream uses
// the parent tool-call context, so turn cancellation propagates to all active
// sessions and removes them from the manager.
type StreamManager struct {
	connManager *ConnectionManager
	sink        agent.ToolLifecycleSink
	sessionID   string
	turnID      string
	trace       agent.TraceContext
	redactError func(string) string
	onIdle      func(*StreamManager)

	mu        sync.Mutex
	sessions  map[string]*StreamSession
	closed    map[string]streamClosedSummary
	closedAll bool
}

// StreamManagerOption customizes a StreamManager for the enclosing turn.
type StreamManagerOption func(*StreamManager)

func WithStreamToolEventSink(sink agent.ToolLifecycleSink) StreamManagerOption {
	return func(m *StreamManager) { m.sink = sink }
}

func WithStreamEventContext(sessionID, turnID string, trace agent.TraceContext) StreamManagerOption {
	return func(m *StreamManager) {
		m.sessionID = sessionID
		m.turnID = turnID
		m.trace = trace
	}
}

func WithStreamErrorRedactor(redact func(string) string) StreamManagerOption {
	return func(m *StreamManager) { m.redactError = redact }
}

func WithStreamIdleCallback(onIdle func(*StreamManager)) StreamManagerOption {
	return func(m *StreamManager) { m.onIdle = onIdle }
}

func NewStreamManager(connManager *ConnectionManager, opts ...StreamManagerOption) *StreamManager {
	m := &StreamManager{
		connManager: connManager,
		sessions:    map[string]*StreamSession{},
		closed:      map[string]streamClosedSummary{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// StreamSession is the externally visible state for an active gRPC stream.
type StreamSession struct {
	ID           string     `json:"id"`
	Method       MethodSpec `json:"-"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	State        string     `json:"state"`
	SendCount    int        `json:"send_count"`
	ReceiveCount int        `json:"receive_count"`
	StartedAt    time.Time  `json:"started_at"`
	ClosedAt     time.Time  `json:"closed_at,omitempty"`

	stream grpc.ClientStream
	cancel context.CancelFunc
	mu     sync.Mutex
}

type streamClosedSummary struct {
	ID            string `json:"stream_id"`
	Profile       string `json:"profile"`
	Method        string `json:"method"`
	State         string `json:"state"`
	SendCount     int    `json:"send_count"`
	ReceiveCount  int    `json:"receive_count"`
	AlreadyClosed bool   `json:"already_closed,omitempty"`
}

type streamProgressEvent struct {
	Kind         string `json:"kind"`
	Action       string `json:"action"`
	StreamID     string `json:"stream_id,omitempty"`
	Profile      string `json:"profile,omitempty"`
	Method       string `json:"method,omitempty"`
	State        string `json:"state,omitempty"`
	Messages     int    `json:"messages,omitempty"`
	SendCount    int    `json:"send_count,omitempty"`
	ReceiveCount int    `json:"receive_count,omitempty"`
	Terminal     bool   `json:"terminal,omitempty"`
	Error        string `json:"error,omitempty"`
}

type streamStartResult struct {
	OK       bool   `json:"ok"`
	StreamID string `json:"stream_id"`
	Profile  string `json:"profile"`
	Method   string `json:"method"`
	Type     string `json:"type"`
	State    string `json:"state"`
}

type streamSendResult struct {
	OK        bool   `json:"ok"`
	StreamID  string `json:"stream_id"`
	Profile   string `json:"profile"`
	Method    string `json:"method"`
	Sent      int    `json:"sent"`
	SendCount int    `json:"send_count"`
	State     string `json:"state"`
}

type streamReceiveResult struct {
	OK           bool             `json:"ok"`
	StreamID     string           `json:"stream_id"`
	Profile      string           `json:"profile"`
	Method       string           `json:"method"`
	Messages     []map[string]any `json:"messages"`
	Received     int              `json:"received"`
	ReceiveCount int              `json:"receive_count"`
	State        string           `json:"state"`
	Terminal     bool             `json:"terminal,omitempty"`
	Status       streamStatus     `json:"status,omitempty"`
}

type streamFinishResult struct {
	OK            bool             `json:"ok"`
	StreamID      string           `json:"stream_id"`
	Profile       string           `json:"profile"`
	Method        string           `json:"method"`
	State         string           `json:"state"`
	SendCount     int              `json:"send_count"`
	ReceiveCount  int              `json:"receive_count"`
	AlreadyClosed bool             `json:"already_closed,omitempty"`
	Response      map[string]any   `json:"response,omitempty"`
	Messages      []map[string]any `json:"messages,omitempty"`
	Drained       int              `json:"drained,omitempty"`
	Status        streamStatus     `json:"status,omitempty"`
}

type streamStatus struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// BuildStreamToolRegistrations creates start/send/receive/finish tool
// registrations for a streaming MethodSpec. Unary methods intentionally return
// no registrations; unary invocation is implemented separately in invoke.go.
func BuildStreamToolRegistrations(method MethodSpec, manager *StreamManager) []agent.ToolRegistration {
	if manager == nil || (!method.ClientStreaming && !method.ServerStreaming) {
		return nil
	}
	base := strings.TrimSpace(method.ToolBaseName)
	if base == "" {
		base = snakeIdentifier(method.ProfileID + "_" + method.ServiceName + "_" + method.MethodName)
	}

	regs := []agent.ToolRegistration{
		manager.registration(method, base+"_start", streamToolStart, startSchema(method), "Start a gRPC streaming session for "+method.FullMethod+"."),
		manager.registration(method, base+"_finish", streamToolFinish, finishSchema(method), "Finish and close a gRPC streaming session for "+method.FullMethod+"."),
	}
	if method.ClientStreaming {
		regs = append(regs, manager.registration(method, base+"_send", streamToolSend, sendSchema(method), "Send one message on a gRPC stream for "+method.FullMethod+"."))
	}
	if method.ServerStreaming {
		regs = append(regs, manager.registration(method, base+"_receive", streamToolReceive, receiveSchema(), "Receive messages from a gRPC stream for "+method.FullMethod+"."))
	}
	return regs
}

func (m *StreamManager) registration(method MethodSpec, name, action string, schema map[string]any, description string) agent.ToolRegistration {
	toolName := name
	return agent.ToolRegistration{
		Func: func(ctx context.Context, args map[string]any) (string, error) {
			switch action {
			case streamToolStart:
				return m.Start(ctx, method, args, toolName)
			case streamToolSend:
				return m.Send(ctx, args, toolName)
			case streamToolReceive:
				return m.Receive(ctx, args, toolName)
			case streamToolFinish:
				return m.Finish(ctx, args, toolName)
			default:
				return "", fmt.Errorf("unknown stream tool action %q", action)
			}
		},
		ProviderVisible: true,
		Descriptor: agent.ToolDescriptor{
			Name:            toolName,
			Description:     description,
			InputJSONSchema: schema,
			ParamAliases: map[string]string{
				"headers":    "metadata",
				"timeout_ms": "deadline_ms",
				"body":       "request",
				"input":      "request",
				"message":    "message",
			},
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC, ServerName: method.ProfileID, CanonicalName: method.FullMethod},
			Traits: agent.ToolTraits{ConcurrencySafe: false, InterruptBehavior: agent.ToolInterruptBehaviorCancel},
		},
	}
}

func (m *StreamManager) Start(ctx context.Context, method MethodSpec, args map[string]any, toolName string) (string, error) {
	if m == nil || m.connManager == nil {
		return "", errors.New("grpc stream manager is not configured")
	}
	if m.isClosed() {
		return "", errors.New("grpc stream manager is closed")
	}
	if !method.ClientStreaming && !method.ServerStreaming {
		return "", fmt.Errorf("gRPC method %s is unary; streaming tools are not available", method.FullMethod)
	}
	if err := ensureStreamDescriptors(method); err != nil {
		return "", err
	}
	conn, err := m.connManager.Conn(ctx, method.ProfileID)
	if err != nil {
		return "", err
	}
	metadataArgs, err := metadataFromArgs(args)
	if err != nil {
		return "", err
	}
	callCtx, cancel, err := m.connManager.CallContext(ctx, method.ProfileID, CallOptions{
		Metadata:   metadataArgs,
		DeadlineMS: intFromArgs(args, "deadline_ms", 0),
	})
	if err != nil {
		return "", err
	}

	desc := &grpc.StreamDesc{StreamName: method.MethodName, ClientStreams: method.ClientStreaming, ServerStreams: method.ServerStreaming}
	stream, err := conn.NewStream(callCtx, desc, method.FullMethod)
	if err != nil {
		cancel()
		return "", err
	}

	toolCallID := toolCallIDFromContext(ctx)
	session := &StreamSession{
		ID:         newStreamID(),
		Method:     method,
		ToolCallID: toolCallID,
		State:      streamStateOpen,
		StartedAt:  time.Now(),
		stream:     stream,
		cancel:     cancel,
	}

	if method.ServerStreaming && !method.ClientStreaming {
		req, err := decodeDynamicMessage(method.RequestDescriptor, args["request"])
		if err != nil {
			cancel()
			return "", fmt.Errorf("decode stream request: %w", err)
		}
		if err := stream.SendMsg(req); err != nil {
			cancel()
			return "", err
		}
		session.SendCount = 1
		if err := stream.CloseSend(); err != nil {
			cancel()
			return "", err
		}
	}

	if !m.addSession(session) {
		cancel()
		return "", errors.New("grpc stream manager is closed")
	}
	go m.cleanupOnContextDone(callCtx, session.ID)
	m.emit(toolName, streamToolStart, toolCallID, session, 0, false, "")
	return encodeJSON(streamStartResult{OK: true, StreamID: session.ID, Profile: method.ProfileID, Method: method.FullMethod, Type: streamType(method), State: session.State})
}

func (m *StreamManager) Send(ctx context.Context, args map[string]any, toolName string) (string, error) {
	if m.isClosed() {
		return "", errors.New("grpc stream manager is closed")
	}
	session, err := m.activeSession(argString(args, "stream_id"))
	if err != nil {
		return "", err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.State != streamStateOpen {
		return "", fmt.Errorf("grpc stream %q is closed", session.ID)
	}
	if !session.Method.ClientStreaming {
		return "", fmt.Errorf("grpc stream %q for %s does not accept client messages", session.ID, session.Method.FullMethod)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	req, err := decodeDynamicMessage(session.Method.RequestDescriptor, args["message"])
	if err != nil {
		return "", fmt.Errorf("decode stream message: %w", err)
	}
	toolCallID := toolCallIDFromContext(ctx)
	if err := session.stream.SendMsg(req); err != nil {
		m.closeSessionLocked(toolCallID, session, err.Error())
		return "", err
	}
	session.SendCount++
	m.emit(toolName, streamToolSend, toolCallID, session, 1, false, "")
	return encodeJSON(streamSendResult{OK: true, StreamID: session.ID, Profile: session.Method.ProfileID, Method: session.Method.FullMethod, Sent: 1, SendCount: session.SendCount, State: session.State})
}

func (m *StreamManager) Receive(ctx context.Context, args map[string]any, toolName string) (string, error) {
	if m.isClosed() {
		return "", errors.New("grpc stream manager is closed")
	}
	session, err := m.activeSession(argString(args, "stream_id"))
	if err != nil {
		return "", err
	}
	if !session.Method.ServerStreaming {
		return "", fmt.Errorf("grpc stream %q for %s does not produce server message batches", session.ID, session.Method.FullMethod)
	}
	maxMessages := intFromArgs(args, "max_messages", 1)
	if maxMessages <= 0 {
		maxMessages = 1
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.State != streamStateOpen {
		return "", fmt.Errorf("grpc stream %q is closed", session.ID)
	}

	messages := make([]map[string]any, 0, maxMessages)
	terminal := false
	status := streamStatus{}
	toolCallID := toolCallIDFromContext(ctx)
	for len(messages) < maxMessages {
		select {
		case <-ctx.Done():
			if len(messages) > 0 {
				status = streamStatus{Code: "CANCELLED", Message: ctx.Err().Error()}
				terminal = true
				goto done
			}
			return "", ctx.Err()
		default:
		}
		msg := dynamicpb.NewMessage(session.Method.ResponseDescriptor)
		err := session.stream.RecvMsg(msg)
		if err == nil {
			decoded, decodeErr := encodeProtoMessageMap(msg)
			if decodeErr != nil {
				return "", decodeErr
			}
			messages = append(messages, decoded)
			session.ReceiveCount++
			continue
		}
		terminal = true
		if errors.Is(err, io.EOF) {
			status = streamStatus{Code: "OK"}
			m.closeSessionLocked(toolCallID, session, "")
			break
		}
		status = streamStatus{Code: "ERROR", Message: err.Error()}
		m.closeSessionLocked(toolCallID, session, err.Error())
		break
	}

done:
	m.emit(toolName, streamToolReceive, toolCallID, session, len(messages), terminal, status.Message)
	return encodeJSON(streamReceiveResult{OK: status.Code != "ERROR", StreamID: session.ID, Profile: session.Method.ProfileID, Method: session.Method.FullMethod, Messages: messages, Received: len(messages), ReceiveCount: session.ReceiveCount, State: session.State, Terminal: terminal, Status: status})
}

func (m *StreamManager) Finish(ctx context.Context, args map[string]any, toolName string) (string, error) {
	if m.isClosed() {
		return "", errors.New("grpc stream manager is closed")
	}
	streamID := argString(args, "stream_id")
	if streamID == "" {
		return "", errors.New("stream_id is required")
	}
	if summary, ok := m.closedSummary(streamID); ok {
		summary.AlreadyClosed = true
		return encodeJSON(streamFinishResult{OK: true, StreamID: summary.ID, Profile: summary.Profile, Method: summary.Method, State: summary.State, SendCount: summary.SendCount, ReceiveCount: summary.ReceiveCount, AlreadyClosed: true, Status: streamStatus{Code: "OK", Message: "stream was already closed"}})
	}
	session, err := m.activeSession(streamID)
	if err != nil {
		return "", err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	toolCallID := toolCallIDFromContext(ctx)
	var response map[string]any
	status := streamStatus{Code: "OK"}
	if session.Method.ClientStreaming && !session.Method.ServerStreaming {
		if err := session.stream.CloseSend(); err != nil {
			status = streamStatus{Code: "ERROR", Message: err.Error()}
			m.closeSessionLocked(toolCallID, session, err.Error())
			return encodeJSON(streamFinishResult{OK: false, StreamID: session.ID, Profile: session.Method.ProfileID, Method: session.Method.FullMethod, State: session.State, SendCount: session.SendCount, ReceiveCount: session.ReceiveCount, Status: status})
		}
		msg := dynamicpb.NewMessage(session.Method.ResponseDescriptor)
		if err := session.stream.RecvMsg(msg); err != nil {
			status = streamStatus{Code: "ERROR", Message: err.Error()}
			m.closeSessionLocked(toolCallID, session, err.Error())
			return encodeJSON(streamFinishResult{OK: false, StreamID: session.ID, Profile: session.Method.ProfileID, Method: session.Method.FullMethod, State: session.State, SendCount: session.SendCount, ReceiveCount: session.ReceiveCount, Status: status})
		}
		var err error
		response, err = encodeProtoMessageMap(msg)
		if err != nil {
			return "", err
		}
		session.ReceiveCount++
	} else {
		if session.Method.ClientStreaming {
			_ = session.stream.CloseSend()
		}
	}

	var messages []map[string]any
	if session.Method.ServerStreaming && boolFromArgs(args, "drain_remaining", false) {
		maxMessages := intFromArgs(args, "max_messages", 64)
		var drainErr error
		messages, drainErr = m.drainMessagesLocked(session, maxMessages)
		if drainErr != nil && !errors.Is(drainErr, io.EOF) {
			status = streamStatus{Code: "ERROR", Message: drainErr.Error()}
		}
		if errors.Is(drainErr, io.EOF) && status.Code == "OK" {
			status = streamStatus{Code: "OK"}
		}
	}

	m.closeSessionLocked(toolCallID, session, status.Message)
	m.emit(toolName, streamToolFinish, toolCallID, session, len(messages), true, status.Message)
	return encodeJSON(streamFinishResult{OK: status.Code != "ERROR", StreamID: session.ID, Profile: session.Method.ProfileID, Method: session.Method.FullMethod, State: session.State, SendCount: session.SendCount, ReceiveCount: session.ReceiveCount, Response: response, Messages: messages, Drained: len(messages), Status: status})
}

func (m *StreamManager) drainMessagesLocked(session *StreamSession, maxMessages int) ([]map[string]any, error) {
	if maxMessages <= 0 {
		maxMessages = 64
	}
	messages := make([]map[string]any, 0, maxMessages)
	for len(messages) < maxMessages {
		msg := dynamicpb.NewMessage(session.Method.ResponseDescriptor)
		err := session.stream.RecvMsg(msg)
		if err != nil {
			return messages, err
		}
		decoded, decodeErr := encodeProtoMessageMap(msg)
		if decodeErr != nil {
			return messages, decodeErr
		}
		messages = append(messages, decoded)
		session.ReceiveCount++
	}
	return messages, nil
}

func (m *StreamManager) addSession(session *StreamSession) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closedAll {
		return false
	}
	m.sessions[session.ID] = session
	delete(m.closed, session.ID)
	return true
}

func (m *StreamManager) activeSession(id string) (*StreamSession, error) {
	if m == nil {
		return nil, errors.New("grpc stream manager is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("stream_id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if summary, ok := m.closed[id]; ok {
		return nil, fmt.Errorf("grpc stream %q is closed for %s", summary.ID, summary.Method)
	}
	session := m.sessions[id]
	if session == nil {
		return nil, fmt.Errorf("unknown grpc stream_id %q", id)
	}
	return session, nil
}

func (m *StreamManager) closedSummary(id string) (streamClosedSummary, bool) {
	if m == nil {
		return streamClosedSummary{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	summary, ok := m.closed[strings.TrimSpace(id)]
	return summary, ok
}

func (m *StreamManager) closeSessionLocked(toolCallID string, session *StreamSession, errMessage string) {
	if session.State == streamStateClosed {
		return
	}
	session.State = streamStateClosed
	session.ClosedAt = time.Now()
	if session.cancel != nil {
		session.cancel()
	}
	becameIdle := false
	m.mu.Lock()
	delete(m.sessions, session.ID)
	m.closed[session.ID] = streamClosedSummary{ID: session.ID, Profile: session.Method.ProfileID, Method: session.Method.FullMethod, State: session.State, SendCount: session.SendCount, ReceiveCount: session.ReceiveCount}
	becameIdle = len(m.sessions) == 0
	m.mu.Unlock()
	if errMessage != "" {
		m.emit("", "closed", coalesceToolCallID(toolCallID, session.ToolCallID), session, 0, true, errMessage)
	}
	if becameIdle {
		m.notifyIdle()
	}
}

func (m *StreamManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closedAll {
		m.mu.Unlock()
		return nil
	}
	m.closedAll = true
	sessions := make([]*StreamSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		session.mu.Lock()
		m.closeSessionLocked(session.ToolCallID, session, "grpc stream manager closed")
		session.mu.Unlock()
	}
	m.notifyIdle()
	return nil
}

func (m *StreamManager) isClosed() bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closedAll
}

func (m *StreamManager) closeIfIdle() bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closedAll {
		return true
	}
	if len(m.sessions) != 0 {
		return false
	}
	m.closedAll = true
	return true
}

func (m *StreamManager) notifyIdle() {
	if m != nil && m.onIdle != nil {
		m.onIdle(m)
	}
}

func (m *StreamManager) cleanupOnContextDone(ctx context.Context, streamID string) {
	<-ctx.Done()
	m.mu.Lock()
	session := m.sessions[streamID]
	m.mu.Unlock()
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.State == streamStateOpen {
		m.closeSessionLocked(session.ToolCallID, session, ctx.Err().Error())
		m.emit("", "cancelled", session.ToolCallID, session, 0, true, ctx.Err().Error())
	}
}

func (m *StreamManager) emit(toolName, action, toolCallID string, session *StreamSession, messages int, terminal bool, errMessage string) {
	if m == nil || m.sink == nil || session == nil {
		return
	}
	if errMessage != "" && m.redactError != nil {
		errMessage = m.redactError(errMessage)
	}
	evtType := agent.ToolLifecycleEventProgress
	if errMessage != "" {
		evtType = agent.ToolLifecycleEventError
	}
	m.sink(agent.ToolLifecycleEvent{
		Type:       evtType,
		TS:         time.Now().UnixMilli(),
		SessionID:  m.sessionID,
		TurnID:     m.turnID,
		ToolCallID: coalesceToolCallID(toolCallID, session.ToolCallID),
		ToolName:   toolName,
		Error:      errMessage,
		Trace:      m.trace,
		Data: streamProgressEvent{
			Kind:         "grpc_stream",
			Action:       action,
			StreamID:     session.ID,
			Profile:      session.Method.ProfileID,
			Method:       session.Method.FullMethod,
			State:        session.State,
			Messages:     messages,
			SendCount:    session.SendCount,
			ReceiveCount: session.ReceiveCount,
			Terminal:     terminal,
			Error:        errMessage,
		},
	})
}

func toolCallIDFromContext(ctx context.Context) string {
	lifecycle, ok := agent.ToolLifecycleFromContext(ctx)
	if !ok {
		return ""
	}
	return strings.TrimSpace(lifecycle.ToolCallID)
}

func coalesceToolCallID(ids ...string) string {
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ensureStreamDescriptors(method MethodSpec) error {
	if method.RequestDescriptor == nil {
		return fmt.Errorf("gRPC method %s is missing request descriptor", method.FullMethod)
	}
	if method.ResponseDescriptor == nil {
		return fmt.Errorf("gRPC method %s is missing response descriptor", method.FullMethod)
	}
	return nil
}

func decodeDynamicMessage(md protoreflect.MessageDescriptor, raw any) (proto.Message, error) {
	if md == nil {
		return nil, errors.New("protobuf message descriptor is required")
	}
	msg := dynamicpb.NewMessage(md)
	if raw == nil {
		raw = map[string]any{}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(encoded, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func encodeProtoMessageMap(msg proto.Message) (map[string]any, error) {
	encoded, err := (protojson.MarshalOptions{UseProtoNames: false, EmitUnpopulated: false}).Marshal(msg)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func metadataFromArgs(args map[string]any) (map[string]string, error) {
	raw, ok := args["metadata"]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("metadata must be an object with string values")
	}
	out := make(map[string]string, len(m))
	for key, value := range m {
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("metadata %q must be a string", key)
		}
		out[key] = s
	}
	return out, nil
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func boolFromArgs(args map[string]any, key string, def bool) bool {
	if args == nil {
		return def
	}
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "y":
			return true
		case "false", "0", "no", "n":
			return false
		}
	}
	return def
}

func intFromArgs(args map[string]any, key string, def int) int {
	if args == nil {
		return def
	}
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
	case string:
		var i int
		if _, err := fmt.Sscanf(t, "%d", &i); err == nil {
			return i
		}
	}
	return def
}

func encodeJSON(v any) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func newStreamID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "grpc_stream_" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("grpc_stream_%d", time.Now().UnixNano())
}

func streamType(method MethodSpec) string {
	switch {
	case method.ClientStreaming && method.ServerStreaming:
		return "bidirectional"
	case method.ClientStreaming:
		return "client_streaming"
	case method.ServerStreaming:
		return "server_streaming"
	default:
		return "unary"
	}
}

func startSchema(method MethodSpec) map[string]any {
	props := map[string]any{
		"metadata":    metadataSchema(),
		"deadline_ms": map[string]any{"type": "integer", "minimum": 0},
	}
	required := []string{}
	if method.ServerStreaming && !method.ClientStreaming {
		props["request"] = schemaOrObject(method.RequestSchema)
		required = append(required, "request")
	}
	return objectSchema(props, required...)
}

func sendSchema(method MethodSpec) map[string]any {
	return objectSchema(map[string]any{
		"stream_id": map[string]any{"type": "string"},
		"message":   schemaOrObject(method.RequestSchema),
	}, "stream_id", "message")
}

func receiveSchema() map[string]any {
	return objectSchema(map[string]any{
		"stream_id":    map[string]any{"type": "string"},
		"max_messages": map[string]any{"type": "integer", "minimum": 1, "default": 1},
	}, "stream_id")
}

func finishSchema(method MethodSpec) map[string]any {
	props := map[string]any{"stream_id": map[string]any{"type": "string"}}
	if method.ServerStreaming {
		props["drain_remaining"] = map[string]any{"type": "boolean", "default": false, "description": "If true, receive remaining server messages after client half-close before closing."}
		props["max_messages"] = map[string]any{"type": "integer", "minimum": 1, "default": 64}
	}
	return objectSchema(props, "stream_id")
}

func metadataSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func schemaOrObject(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	return schema
}
