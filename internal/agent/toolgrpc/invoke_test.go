package toolgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
)

const invoiceGetFullMethod = "/acme.billing.InvoiceService/GetInvoice"

type unaryInvoiceServiceServer interface {
	GetInvoice(context.Context, *dynamicpb.Message) (proto.Message, error)
}

type unaryInvoiceServer struct {
	requestDesc  protoreflect.MessageDescriptor
	responseDesc protoreflect.MessageDescriptor
	onCall       func(context.Context, *dynamicpb.Message, *dynamicpb.Message) error
}

func (s *unaryInvoiceServer) GetInvoice(ctx context.Context, req *dynamicpb.Message) (proto.Message, error) {
	resp := dynamicpb.NewMessage(s.responseDesc)
	if s.onCall != nil {
		if err := s.onCall(ctx, req, resp); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

var unaryInvoiceServiceDesc = grpc.ServiceDesc{
	ServiceName: "acme.billing.InvoiceService",
	HandlerType: (*unaryInvoiceServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "GetInvoice",
		Handler:    unaryInvoiceHandler,
	}},
}

func unaryInvoiceHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	server := srv.(*unaryInvoiceServer)
	in := dynamicpb.NewMessage(server.requestDesc)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.GetInvoice(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: invoiceGetFullMethod}
	handler := func(ctx context.Context, req any) (any, error) {
		return server.GetInvoice(ctx, req.(*dynamicpb.Message))
	}
	return interceptor(ctx, in, info, handler)
}

func TestUnaryExecutorSuccessAliasesMetadataDeadlineClampEnvelope(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	serverSawCall := false
	target := startUnaryInvoiceServer(t, method, func(ctx context.Context, req, resp *dynamicpb.Message) error {
		serverSawCall = true
		if got := req.Get(req.Descriptor().Fields().ByName("id")).String(); got != "inv-123" {
			t.Fatalf("request id = %q", got)
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			t.Fatalf("missing incoming metadata")
		}
		if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer default" {
			t.Fatalf("authorization metadata = %v", got)
		}
		if got := md.Get("x-request-id"); len(got) != 1 || got[0] != "req-1" {
			t.Fatalf("x-request-id metadata = %v", got)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatalf("server context missing deadline")
		}
		if remaining := time.Until(deadline); remaining <= 0 || remaining > 650*time.Millisecond {
			t.Fatalf("deadline remaining = %s, want clamped to <= 650ms and > 0", remaining)
		}
		resp.Set(resp.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString("inv-123"))
		return nil
	})
	exec := newUnaryExecutorForTarget(t, target, method, config.GRPCAuthConfig{
		Metadata:          map[string]string{"authorization": "Bearer default"},
		AllowOverrideKeys: []string{"x-request-id"},
	}, config.GRPCDefaultsConfig{DeadlineMS: 1000, MaxDeadlineMS: 500})

	result, err := exec.Execute(context.Background(), agent.ToolCall{
		Name: method.ToolBaseName,
		Args: map[string]any{
			"input":      map[string]any{"id": "inv-123"},
			"headers":    map[string]any{"x-request-id": "req-1"},
			"timeout_ms": 5000,
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !serverSawCall {
		t.Fatalf("server was not called")
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		t.Fatalf("result is not JSON: %v; %s", err, result)
	}
	if envelope["ok"] != true || envelope["profile"] != "billing" || envelope["method"] != invoiceGetFullMethod {
		t.Fatalf("unexpected envelope metadata: %#v", envelope)
	}
	statusMap, _ := envelope["status"].(map[string]any)
	if statusMap["code"] != "OK" {
		t.Fatalf("status = %#v", statusMap)
	}
	responseMap, _ := envelope["response"].(map[string]any)
	if responseMap["id"] != "inv-123" {
		t.Fatalf("response = %#v", responseMap)
	}
	if _, ok := envelope["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms missing or not numeric: %#v", envelope["duration_ms"])
	}
}

func TestUnaryExecutorUsesDescriptorResolverForAny(t *testing.T) {
	method := anyUnaryMethodSpec(t)
	target := startDynamicUnaryServer(t, method, func(ctx context.Context, req, resp *dynamicpb.Message) error {
		payload := req.Descriptor().Fields().ByName("payload")
		resp.Set(resp.Descriptor().Fields().ByName("payload"), req.Get(payload))
		return nil
	})
	exec := newUnaryExecutorForTarget(t, target, method, config.GRPCAuthConfig{}, config.GRPCDefaultsConfig{DeadlineMS: 500, MaxDeadlineMS: 500})
	result, err := exec.Execute(context.Background(), agent.ToolCall{
		Name: method.ToolBaseName,
		Args: map[string]any{"request": map[string]any{"payload": map[string]any{
			"@type": "type.googleapis.com/invoke.any.Payload",
			"name":  "zelda",
		}}},
	})
	if err != nil {
		t.Fatalf("Execute with Any payload: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		t.Fatalf("result is not JSON: %v; %s", err, result)
	}
	responseMap, _ := envelope["response"].(map[string]any)
	payloadMap, _ := responseMap["payload"].(map[string]any)
	if payloadMap["@type"] != "type.googleapis.com/invoke.any.Payload" || payloadMap["name"] != "zelda" {
		t.Fatalf("Any response payload = %#v", payloadMap)
	}
}

func TestUnaryExecutorSchemaValidationFailure(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	exec := newUnaryExecutorForTarget(t, "127.0.0.1:1", method, config.GRPCAuthConfig{}, config.GRPCDefaultsConfig{})
	_, err := exec.Execute(context.Background(), agent.ToolCall{Name: method.ToolBaseName, Args: map[string]any{}})
	if err == nil {
		t.Fatalf("expected schema validation error")
	}
	var execErr *agent.ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != agent.ToolExecutionPhaseSchemaValidation {
		t.Fatalf("expected schema validation phase, got %T %v", err, err)
	}
}

func TestUnaryExecutorSemanticValidationRejectsMetadataOverride(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	exec := newUnaryExecutorForTarget(t, "127.0.0.1:1", method, config.GRPCAuthConfig{AllowOverrideKeys: []string{"x-request-id"}}, config.GRPCDefaultsConfig{})
	_, err := exec.Execute(context.Background(), agent.ToolCall{
		Name: method.ToolBaseName,
		Args: map[string]any{
			"request":  map[string]any{"id": "inv-123"},
			"metadata": map[string]any{"x-not-allowed": "nope"},
		},
	})
	if err == nil {
		t.Fatalf("expected semantic validation error")
	}
	var execErr *agent.ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != agent.ToolExecutionPhaseSemanticValidation {
		t.Fatalf("expected semantic validation phase, got %T %v", err, err)
	}
}

func TestUnaryExecutorRPCFailureIsExecutePhase(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	target := startUnaryInvoiceServer(t, method, func(ctx context.Context, req, resp *dynamicpb.Message) error {
		return status.Error(codes.PermissionDenied, "no invoice for you")
	})
	exec := newUnaryExecutorForTarget(t, target, method, config.GRPCAuthConfig{}, config.GRPCDefaultsConfig{DeadlineMS: 500, MaxDeadlineMS: 500})
	_, err := exec.Execute(context.Background(), agent.ToolCall{Name: method.ToolBaseName, Args: map[string]any{"request": map[string]any{"id": "inv-123"}}})
	if err == nil {
		t.Fatalf("expected execute error")
	}
	var execErr *agent.ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != agent.ToolExecutionPhaseExecute {
		t.Fatalf("expected execute phase, got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "PermissionDenied") {
		t.Fatalf("error = %v, want PermissionDenied status", err)
	}
}

func TestUnaryExecutorTimeoutIsExecutePhase(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	target := startUnaryInvoiceServer(t, method, func(ctx context.Context, req, resp *dynamicpb.Message) error {
		<-ctx.Done()
		return ctx.Err()
	})
	exec := newUnaryExecutorForTarget(t, target, method, config.GRPCAuthConfig{}, config.GRPCDefaultsConfig{DeadlineMS: 30, MaxDeadlineMS: 30})
	_, err := exec.Execute(context.Background(), agent.ToolCall{Name: method.ToolBaseName, Args: map[string]any{"request": map[string]any{"id": "inv-123"}}})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	var execErr *agent.ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != agent.ToolExecutionPhaseExecute {
		t.Fatalf("expected execute phase, got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "DeadlineExceeded") && !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func invoiceUnaryMethodSpec(t *testing.T) MethodSpec {
	t.Helper()
	methods, err := DiscoverFromFileDescriptorSet(config.GRPCEndpointConfig{
		ID:        "billing",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet},
		Exposure:  config.GRPCExposureConfig{Namespace: "grpc_billing"},
	}, testDescriptorSet())
	if err != nil {
		t.Fatalf("DiscoverFromFileDescriptorSet: %v", err)
	}
	for _, method := range methods {
		if method.FullMethod == invoiceGetFullMethod {
			return method
		}
	}
	t.Fatalf("did not find %s in methods %#v", invoiceGetFullMethod, methods)
	return MethodSpec{}
}

type genericDynamicUnaryServiceServer interface {
	Call(context.Context, *dynamicpb.Message) (proto.Message, error)
}

type genericDynamicUnaryServer struct {
	requestDesc  protoreflect.MessageDescriptor
	responseDesc protoreflect.MessageDescriptor
	onCall       func(context.Context, *dynamicpb.Message, *dynamicpb.Message) error
}

func (s *genericDynamicUnaryServer) Call(ctx context.Context, req *dynamicpb.Message) (proto.Message, error) {
	resp := dynamicpb.NewMessage(s.responseDesc)
	if s.onCall != nil {
		if err := s.onCall(ctx, req, resp); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func startDynamicUnaryServer(t *testing.T, method MethodSpec, onCall func(context.Context, *dynamicpb.Message, *dynamicpb.Message) error) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	svc := &genericDynamicUnaryServer{
		requestDesc:  method.RequestDescriptor,
		responseDesc: method.ResponseDescriptor,
		onCall:       onCall,
	}
	handler := func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		server := srv.(*genericDynamicUnaryServer)
		in := dynamicpb.NewMessage(server.requestDesc)
		if err := dec(in); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return server.Call(ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv, FullMethod: method.FullMethod}
		next := func(ctx context.Context, req any) (any, error) {
			return server.Call(ctx, req.(*dynamicpb.Message))
		}
		return interceptor(ctx, in, info, next)
	}
	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: method.ServiceName,
		HandlerType: (*genericDynamicUnaryServiceServer)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: method.MethodName,
			Handler:    handler,
		}},
	}, svc)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String()
}

func startUnaryInvoiceServer(t *testing.T, method MethodSpec, onCall func(context.Context, *dynamicpb.Message, *dynamicpb.Message) error) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	server.RegisterService(&unaryInvoiceServiceDesc, &unaryInvoiceServer{
		requestDesc:  method.RequestDescriptor,
		responseDesc: method.ResponseDescriptor,
		onCall:       onCall,
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String()
}

func anyUnaryMethodSpec(t *testing.T) MethodSpec {
	t.Helper()
	methods, err := DiscoverFromFileDescriptorSet(config.GRPCEndpointConfig{
		ID:        "anysvc",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet},
		Exposure:  config.GRPCExposureConfig{Namespace: "grpc_anysvc"},
	}, anyDescriptorSet())
	if err != nil {
		t.Fatalf("DiscoverFromFileDescriptorSet(any): %v", err)
	}
	for _, method := range methods {
		if method.FullMethod == "/invoke.any.AnyService/Echo" {
			return method
		}
	}
	t.Fatalf("did not find AnyService/Echo in methods %#v", methods)
	return MethodSpec{}
}

func anyDescriptorSet() *descriptorpb.FileDescriptorSet {
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto),
		{
			Name:       proto.String("invoke/any.proto"),
			Package:    proto.String("invoke.any"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"google/protobuf/any.proto"},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("Payload"), Field: []*descriptorpb.FieldDescriptorProto{field("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING)}},
				{Name: proto.String("AnyEnvelope"), Field: []*descriptorpb.FieldDescriptorProto{messageField("payload", 1, ".google.protobuf.Any")}},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("AnyService"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:       proto.String("Echo"),
					InputType:  proto.String(".invoke.any.AnyEnvelope"),
					OutputType: proto.String(".invoke.any.AnyEnvelope"),
				}},
			}},
		},
	}}
}

func newUnaryExecutorForTarget(t *testing.T, target string, method MethodSpec, auth config.GRPCAuthConfig, defaults config.GRPCDefaultsConfig) *UnaryExecutor {
	t.Helper()
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:       method.ProfileID,
		Target:   target,
		Auth:     auth,
		Defaults: defaults,
		Transport: config.GRPCTransportConfig{
			TLSMode: config.GRPCTransportTLSModeInsecure,
		},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	exec, err := NewUnaryExecutor(manager, []MethodSpec{method})
	if err != nil {
		t.Fatalf("NewUnaryExecutor: %v", err)
	}
	return exec
}
