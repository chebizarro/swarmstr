package toolgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type streamTestServiceServer interface{}

type streamingTestServer struct {
	reqDesc  protoreflect.MessageDescriptor
	respDesc protoreflect.MessageDescriptor
}

func TestStreamManagerServerStreamingReceiveAndDuplicateFinish(t *testing.T) {
	manager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	method := methodByName(t, methods, "ServerStream")

	startRaw, err := manager.Start(context.Background(), method, map[string]any{"request": map[string]any{"text": "hello", "count": 2}}, "server_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	if streamID == "" {
		t.Fatalf("missing stream_id in %s", startRaw)
	}

	recvRaw, err := manager.Receive(context.Background(), map[string]any{"stream_id": streamID, "max_messages": 2}, "server_receive")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	var recv streamReceiveResult
	decodeJSON(t, recvRaw, &recv)
	if recv.Received != 2 || recv.ReceiveCount != 2 || recv.Terminal {
		t.Fatalf("unexpected receive result: %+v raw=%s", recv, recvRaw)
	}
	if got := recv.Messages[0]["text"]; got != "hello-1" {
		t.Fatalf("first message text = %v", got)
	}

	recvRaw, err = manager.Receive(context.Background(), map[string]any{"stream_id": streamID, "max_messages": 1}, "server_receive")
	if err != nil {
		t.Fatalf("Receive EOF: %v", err)
	}
	decodeJSON(t, recvRaw, &recv)
	if !recv.Terminal || recv.Status.Code != "OK" || recv.State != streamStateClosed {
		t.Fatalf("expected terminal OK close, got %+v raw=%s", recv, recvRaw)
	}

	finishRaw, err := manager.Finish(context.Background(), map[string]any{"stream_id": streamID}, "server_finish")
	if err != nil {
		t.Fatalf("duplicate Finish: %v", err)
	}
	var finish streamFinishResult
	decodeJSON(t, finishRaw, &finish)
	if !finish.OK || !finish.AlreadyClosed {
		t.Fatalf("duplicate finish not idempotent: %+v raw=%s", finish, finishRaw)
	}
}

func TestStreamManagerClientStreamingSendFinish(t *testing.T) {
	manager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	method := methodByName(t, methods, "ClientStream")

	startRaw, err := manager.Start(context.Background(), method, nil, "client_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	for _, text := range []string{"alpha", "beta"} {
		if _, err := manager.Send(context.Background(), map[string]any{"stream_id": streamID, "message": map[string]any{"text": text}}, "client_send"); err != nil {
			t.Fatalf("Send(%s): %v", text, err)
		}
	}
	finishRaw, err := manager.Finish(context.Background(), map[string]any{"stream_id": streamID}, "client_finish")
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	var finish streamFinishResult
	decodeJSON(t, finishRaw, &finish)
	if !finish.OK || finish.Response["text"] != "alpha,beta" || finish.Response["index"] != float64(2) {
		t.Fatalf("unexpected finish response: %+v raw=%s", finish, finishRaw)
	}
	if _, err := manager.Send(context.Background(), map[string]any{"stream_id": streamID, "message": map[string]any{"text": "late"}}, "client_send"); err == nil {
		t.Fatalf("send after finish succeeded")
	}
}

func TestStreamManagerBidirectionalSendReceiveFinish(t *testing.T) {
	manager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	method := methodByName(t, methods, "Bidi")

	startRaw, err := manager.Start(context.Background(), method, nil, "bidi_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	if _, err := manager.Send(context.Background(), map[string]any{"stream_id": streamID, "message": map[string]any{"text": "ping", "count": 7}}, "bidi_send"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	recvRaw, err := manager.Receive(context.Background(), map[string]any{"stream_id": streamID}, "bidi_receive")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	var recv streamReceiveResult
	decodeJSON(t, recvRaw, &recv)
	if recv.Received != 1 || recv.Messages[0]["text"] != "echo:ping" || recv.Messages[0]["index"] != float64(7) {
		t.Fatalf("unexpected bidi receive: %+v raw=%s", recv, recvRaw)
	}
	finishRaw, err := manager.Finish(context.Background(), map[string]any{"stream_id": streamID}, "bidi_finish")
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	var finish streamFinishResult
	decodeJSON(t, finishRaw, &finish)
	if !finish.OK || finish.State != streamStateClosed {
		t.Fatalf("unexpected finish: %+v raw=%s", finish, finishRaw)
	}
}

func TestStreamManagerBidiFinishCanDrainAfterHalfClose(t *testing.T) {
	manager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	method := methodByName(t, methods, "Bidi")

	startRaw, err := manager.Start(context.Background(), method, nil, "bidi_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	if _, err := manager.Send(context.Background(), map[string]any{"stream_id": streamID, "message": map[string]any{"text": "final", "count": 9}}, "bidi_send"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	finishRaw, err := manager.Finish(context.Background(), map[string]any{"stream_id": streamID, "drain_remaining": true, "max_messages": 4}, "bidi_finish")
	if err != nil {
		t.Fatalf("Finish drain: %v", err)
	}
	var finish streamFinishResult
	decodeJSON(t, finishRaw, &finish)
	if !finish.OK || finish.Drained != 1 || finish.Messages[0]["text"] != "final:final" || finish.Messages[0]["index"] != float64(9) {
		t.Fatalf("unexpected drained finish: %+v raw=%s", finish, finishRaw)
	}
}

func TestStreamToolRegistrationAliasesUseInputJSONSchema(t *testing.T) {
	manager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	method := methodByName(t, methods, "ServerStream")
	registry := agent.NewToolRegistry()
	var startName string
	for _, reg := range BuildStreamToolRegistrations(method, manager) {
		registry.RegisterTool(reg.Descriptor.Name, reg)
		if strings.HasSuffix(reg.Descriptor.Name, "_start") {
			startName = reg.Descriptor.Name
		}
	}
	if startName == "" {
		t.Fatalf("missing start registration")
	}
	raw, err := registry.Execute(context.Background(), agent.ToolCall{Name: startName, Args: map[string]any{"body": map[string]any{"text": "alias", "count": 1}}})
	if err != nil {
		t.Fatalf("Execute with body alias: %v", err)
	}
	if streamID := decodeField[string](t, raw, "stream_id"); streamID == "" {
		t.Fatalf("missing stream id from alias start: %s", raw)
	}
}

func TestStreamManagerOutOfOrderAndWrongDirectionErrors(t *testing.T) {
	manager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	serverMethod := methodByName(t, methods, "ServerStream")
	clientMethod := methodByName(t, methods, "ClientStream")

	if _, err := manager.Send(context.Background(), map[string]any{"stream_id": "missing", "message": map[string]any{}}, "send"); err == nil || !strings.Contains(err.Error(), "unknown grpc stream_id") {
		t.Fatalf("expected unknown stream error, got %v", err)
	}
	startRaw, err := manager.Start(context.Background(), serverMethod, map[string]any{"request": map[string]any{"text": "one", "count": 1}}, "server_start")
	if err != nil {
		t.Fatalf("Start server stream: %v", err)
	}
	serverID := decodeField[string](t, startRaw, "stream_id")
	if _, err := manager.Send(context.Background(), map[string]any{"stream_id": serverID, "message": map[string]any{}}, "send"); err == nil || !strings.Contains(err.Error(), "does not accept") {
		t.Fatalf("expected wrong-direction send error, got %v", err)
	}

	startRaw, err = manager.Start(context.Background(), clientMethod, nil, "client_start")
	if err != nil {
		t.Fatalf("Start client stream: %v", err)
	}
	clientID := decodeField[string](t, startRaw, "stream_id")
	if _, err := manager.Receive(context.Background(), map[string]any{"stream_id": clientID}, "receive"); err == nil || !strings.Contains(err.Error(), "does not produce") {
		t.Fatalf("expected wrong-direction receive error, got %v", err)
	}
}

func TestStreamManagerTurnCancellationClosesStreamAndEmitsProgress(t *testing.T) {
	eventCh := make(chan agent.ToolLifecycleEvent, 8)
	sink := func(evt agent.ToolLifecycleEvent) { eventCh <- evt }
	manager, methods, cleanup := startStreamManagerTest(t, sink)
	defer cleanup()
	method := methodByName(t, methods, "Bidi")
	ctx, cancel := context.WithCancel(streamLifecycleContext(context.Background(), "tc-start"))

	startRaw, err := manager.Start(ctx, method, nil, "bidi_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	cancel()

	var sawCancel bool
	deadline := time.After(2 * time.Second)
	for !sawCancel {
		select {
		case evt := <-eventCh:
			if data, ok := evt.Data.(streamProgressEvent); ok && data.StreamID == streamID && data.Action == "cancelled" {
				if evt.ToolCallID != "tc-start" {
					t.Fatalf("cancelled event tool_call_id = %q, want tc-start", evt.ToolCallID)
				}
				sawCancel = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for cancellation event")
		}
	}
	finishRaw, err := manager.Finish(context.Background(), map[string]any{"stream_id": streamID}, "bidi_finish")
	if err != nil {
		t.Fatalf("Finish after cancel: %v", err)
	}
	var finish streamFinishResult
	decodeJSON(t, finishRaw, &finish)
	if !finish.AlreadyClosed {
		t.Fatalf("cancelled stream should be tombstoned as closed: %+v raw=%s", finish, finishRaw)
	}
}

func TestStreamManagerEventsIncludeToolCallIDCorrelation(t *testing.T) {
	events := make([]agent.ToolLifecycleEvent, 0, 8)
	sink := func(evt agent.ToolLifecycleEvent) { events = append(events, evt) }
	manager, methods, cleanup := startStreamManagerTest(t, sink)
	defer cleanup()
	method := methodByName(t, methods, "Bidi")

	startCtx := streamLifecycleContext(context.Background(), "tc-start")
	startRaw, err := manager.Start(startCtx, method, nil, "bidi_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")

	sendCtx := streamLifecycleContext(context.Background(), "tc-send")
	if _, err := manager.Send(sendCtx, map[string]any{"stream_id": streamID, "message": map[string]any{"text": "ping", "count": 1}}, "bidi_send"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recvCtx := streamLifecycleContext(context.Background(), "tc-receive")
	if _, err := manager.Receive(recvCtx, map[string]any{"stream_id": streamID, "max_messages": 1}, "bidi_receive"); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	finishCtx := streamLifecycleContext(context.Background(), "tc-finish")
	if _, err := manager.Finish(finishCtx, map[string]any{"stream_id": streamID}, "bidi_finish"); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	assertEventWithToolCallID(t, events, "start", "tc-start")
	assertEventWithToolCallID(t, events, "send", "tc-send")
	assertEventWithToolCallID(t, events, "receive", "tc-receive")
	assertEventWithToolCallID(t, events, "finish", "tc-finish")
}

func TestBuildStreamToolRegistrationsShapes(t *testing.T) {
	manager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	bidi := methodByName(t, methods, "Bidi")
	regs := BuildStreamToolRegistrations(bidi, manager)
	if len(regs) != 4 {
		t.Fatalf("bidi registrations = %d, want 4", len(regs))
	}
	for _, reg := range regs {
		if reg.Descriptor.Origin.Kind != agent.ToolOriginKindGRPC || reg.Descriptor.Traits.InterruptBehavior != agent.ToolInterruptBehaviorCancel || reg.Descriptor.Traits.ConcurrencySafe {
			t.Fatalf("bad descriptor for %s: %+v", reg.Descriptor.Name, reg.Descriptor)
		}
	}
}

func startStreamManagerTest(t *testing.T, sink agent.ToolLifecycleSink) (*StreamManager, []MethodSpec, func()) {
	t.Helper()
	fds := streamDescriptorSet()
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		t.Fatalf("NewFiles: %v", err)
	}
	reqDesc := messageDescriptor(t, files, "toolgrpc.streamtest.StreamRequest")
	respDesc := messageDescriptor(t, files, "toolgrpc.streamtest.StreamResponse")
	server := &streamingTestServer{reqDesc: reqDesc, respDesc: respDesc}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(streamServiceDesc(), server)
	go func() { _ = grpcServer.Serve(listener) }()

	profile := config.GRPCEndpointConfig{
		ID:        "streams",
		Target:    listener.Addr().String(),
		Transport: config.GRPCTransportConfig{TLSMode: config.GRPCTransportTLSModeInsecure},
		Defaults:  config.GRPCDefaultsConfig{DeadlineMS: 2000, MaxDeadlineMS: 5000},
	}
	methods, err := DiscoverFromFileDescriptorSet(profile, fds)
	if err != nil {
		t.Fatalf("DiscoverFromFileDescriptorSet: %v", err)
	}
	connManager, err := NewConnectionManager([]config.GRPCEndpointConfig{profile})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	manager := NewStreamManager(connManager, WithStreamToolEventSink(sink), WithStreamEventContext("sess", "turn", agent.TraceContext{TaskID: "task"}))
	cleanup := func() {
		_ = connManager.Close()
		grpcServer.Stop()
		_ = listener.Close()
	}
	return manager, methods, cleanup
}

func streamServiceDesc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: "toolgrpc.streamtest.StreamService",
		HandlerType: (*streamTestServiceServer)(nil),
		Streams: []grpc.StreamDesc{
			{StreamName: "ServerStream", Handler: serverStreamHandler, ServerStreams: true},
			{StreamName: "ClientStream", Handler: clientStreamHandler, ClientStreams: true},
			{StreamName: "Bidi", Handler: bidiStreamHandler, ClientStreams: true, ServerStreams: true},
		},
	}
}

func serverStreamHandler(srv any, stream grpc.ServerStream) error {
	s := srv.(*streamingTestServer)
	req := dynamicpb.NewMessage(s.reqDesc)
	if err := stream.RecvMsg(req); err != nil {
		return err
	}
	text := stringField(req, "text")
	count := intField(req, "count")
	if count <= 0 {
		count = 1
	}
	for i := 1; i <= count; i++ {
		resp := newResponse(s.respDesc, fmt.Sprintf("%s-%d", text, i), int32(i))
		if err := stream.SendMsg(resp); err != nil {
			return err
		}
	}
	return nil
}

func clientStreamHandler(srv any, stream grpc.ServerStream) error {
	s := srv.(*streamingTestServer)
	var texts []string
	for {
		req := dynamicpb.NewMessage(s.reqDesc)
		err := stream.RecvMsg(req)
		if errorsIsEOF(err) {
			break
		}
		if err != nil {
			return err
		}
		texts = append(texts, stringField(req, "text"))
	}
	return stream.SendMsg(newResponse(s.respDesc, strings.Join(texts, ","), int32(len(texts))))
}

func bidiStreamHandler(srv any, stream grpc.ServerStream) error {
	s := srv.(*streamingTestServer)
	var deferred []proto.Message
	for {
		req := dynamicpb.NewMessage(s.reqDesc)
		err := stream.RecvMsg(req)
		if errorsIsEOF(err) {
			for _, msg := range deferred {
				if err := stream.SendMsg(msg); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		text := stringField(req, "text")
		respText := "echo:" + text
		if text == "final" {
			respText = "final:" + text
			deferred = append(deferred, newResponse(s.respDesc, respText, int32(intField(req, "count"))))
			continue
		}
		resp := newResponse(s.respDesc, respText, int32(intField(req, "count")))
		if err := stream.SendMsg(resp); err != nil {
			return err
		}
	}
}

func errorsIsEOF(err error) bool { return err == io.EOF }

func newResponse(md protoreflect.MessageDescriptor, text string, index int32) proto.Message {
	msg := dynamicpb.NewMessage(md)
	fields := md.Fields()
	msg.Set(fields.ByName("text"), protoreflect.ValueOfString(text))
	msg.Set(fields.ByName("index"), protoreflect.ValueOfInt32(index))
	return msg
}

func stringField(msg protoreflect.Message, name protoreflect.Name) string {
	fd := msg.Descriptor().Fields().ByName(name)
	if fd == nil {
		return ""
	}
	return msg.Get(fd).String()
}

func intField(msg protoreflect.Message, name protoreflect.Name) int {
	fd := msg.Descriptor().Fields().ByName(name)
	if fd == nil {
		return 0
	}
	return int(msg.Get(fd).Int())
}

func methodByName(t *testing.T, methods []MethodSpec, name string) MethodSpec {
	t.Helper()
	for _, method := range methods {
		if method.MethodName == name {
			return method
		}
	}
	t.Fatalf("method %s not found in %+v", name, methods)
	return MethodSpec{}
}

func messageDescriptor(t *testing.T, files *protoregistry.Files, fullName protoreflect.FullName) protoreflect.MessageDescriptor {
	t.Helper()
	desc, err := files.FindDescriptorByName(fullName)
	if err != nil {
		t.Fatalf("FindDescriptorByName(%s): %v", fullName, err)
	}
	md, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		t.Fatalf("%s is %T, not message descriptor", fullName, desc)
	}
	return md
}

func streamDescriptorSet() *descriptorpb.FileDescriptorSet {
	pkg := "toolgrpc.streamtest"
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("streamtest.proto"),
		Package: proto.String(pkg),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("StreamRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					fieldDescriptor("text", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					fieldDescriptor("count", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
				},
			},
			{
				Name: proto.String("StreamResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					fieldDescriptor("text", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					fieldDescriptor("index", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("StreamService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{
					Name:            proto.String("ServerStream"),
					InputType:       proto.String("." + pkg + ".StreamRequest"),
					OutputType:      proto.String("." + pkg + ".StreamResponse"),
					ServerStreaming: proto.Bool(true),
				},
				{
					Name:            proto.String("ClientStream"),
					InputType:       proto.String("." + pkg + ".StreamRequest"),
					OutputType:      proto.String("." + pkg + ".StreamResponse"),
					ClientStreaming: proto.Bool(true),
				},
				{
					Name:            proto.String("Bidi"),
					InputType:       proto.String("." + pkg + ".StreamRequest"),
					OutputType:      proto.String("." + pkg + ".StreamResponse"),
					ClientStreaming: proto.Bool(true),
					ServerStreaming: proto.Bool(true),
				},
			},
		}},
	}}}
}

func fieldDescriptor(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		JsonName: proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     typ.Enum(),
	}
}

func streamLifecycleContext(ctx context.Context, toolCallID string) context.Context {
	return agent.ContextWithToolLifecycle(ctx, agent.ToolLifecycleContext{ToolCallID: toolCallID})
}

func assertEventWithToolCallID(t *testing.T, events []agent.ToolLifecycleEvent, action, toolCallID string) {
	t.Helper()
	for _, evt := range events {
		data, ok := evt.Data.(streamProgressEvent)
		if !ok || data.Action != action {
			continue
		}
		if evt.ToolCallID != toolCallID {
			t.Fatalf("event action %s tool_call_id = %q, want %q", action, evt.ToolCallID, toolCallID)
		}
		return
	}
	t.Fatalf("missing event with action %s", action)
}

func decodeField[T any](t *testing.T, raw string, key string) T {
	t.Helper()
	var values map[string]any
	decodeJSON(t, raw, &values)
	value, _ := values[key].(T)
	return value
}

func decodeJSON(t *testing.T, raw string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("decode JSON %s: %v", raw, err)
	}
}
