package toolgrpc

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"metiq/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	reflectionv1pb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestDiscoverFromFileDescriptorSetStatic(t *testing.T) {
	profile := config.GRPCEndpointConfig{
		ID:        "billing",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet},
		Exposure: config.GRPCExposureConfig{
			Namespace:       "grpc_billing",
			IncludeServices: []string{"acme.billing.InvoiceService"},
			ExcludeMethods:  []string{"acme.billing.InvoiceService/DeleteInvoice"},
		},
	}

	methods, err := DiscoverFromFileDescriptorSet(profile, testDescriptorSet())
	if err != nil {
		t.Fatalf("DiscoverFromFileDescriptorSet: %v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("expected 2 filtered methods, got %d: %#v", len(methods), methods)
	}
	unary := methods[0]
	if unary.FullMethod != "/acme.billing.InvoiceService/GetInvoice" {
		t.Fatalf("unexpected first method: %#v", unary)
	}
	if unary.ToolBaseName != "grpc_billing_acme_billing_invoice_service_get_invoice" {
		t.Fatalf("unexpected tool base name: %q", unary.ToolBaseName)
	}
	if unary.RequestType != "acme.billing.GetInvoiceRequest" || unary.ResponseType != "acme.billing.Invoice" {
		t.Fatalf("unexpected types: request=%q response=%q", unary.RequestType, unary.ResponseType)
	}
	stream := methods[1]
	if !stream.ServerStreaming || stream.ClientStreaming {
		t.Fatalf("unexpected streaming flags: %#v", stream)
	}
}

func TestDiscoverFromDescriptorSetFile(t *testing.T) {
	path := writeDescriptorSet(t, testDescriptorSet())
	profile := config.GRPCEndpointConfig{
		ID:        "billing",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet, DescriptorSet: path},
	}
	methods, err := DiscoverFromDescriptorSet(profile)
	if err != nil {
		t.Fatalf("DiscoverFromDescriptorSet: %v", err)
	}
	if len(methods) != 3 {
		t.Fatalf("expected 3 methods, got %d", len(methods))
	}
}

func TestDiscoverReflectionHealthService(t *testing.T) {
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()
	go func() {
		_ = server.Serve(lis)
	}()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	methods, err := Discover(ctx, config.GRPCEndpointConfig{ID: "health"}, conn)
	if err != nil {
		t.Fatalf("Discover reflection: %v", err)
	}
	var found bool
	for _, method := range methods {
		if method.FullMethod == "/grpc.health.v1.Health/Check" {
			found = true
			if method.ToolBaseName != "grpc_health_grpc_health_v1_health_check" {
				t.Fatalf("unexpected health tool name: %q", method.ToolBaseName)
			}
			if method.RequestSchema["type"] != "object" || method.ResponseSchema["type"] != "object" {
				t.Fatalf("expected object schemas: %#v %#v", method.RequestSchema, method.ResponseSchema)
			}
		}
	}
	if !found {
		t.Fatalf("reflection methods did not include health Check: %#v", methods)
	}
}

func TestDiscoverReflectionV1OnlyHealthService(t *testing.T) {
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.RegisterV1(server)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()
	go func() {
		_ = server.Serve(lis)
	}()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	fds, err := loadDescriptorSetFromReflectionV1(ctx, conn)
	if err != nil {
		t.Fatalf("loadDescriptorSetFromReflectionV1: %v", err)
	}
	files := fds.GetFile()
	if len(files) == 0 {
		t.Fatal("expected reflected v1 descriptors")
	}
	if _, err := reflectionv1pb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx); err != nil {
		t.Fatalf("expected v1 reflection client to connect: %v", err)
	}
}

func TestDiscoverReflectionFallsBackToStaticDescriptorSet(t *testing.T) {
	path := writeDescriptorSet(t, testDescriptorSet())
	profile := config.GRPCEndpointConfig{
		ID:        "billing",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeReflection, DescriptorSet: path},
	}
	methods, err := Discover(context.Background(), profile, nil)
	if err != nil {
		t.Fatalf("Discover fallback: %v", err)
	}
	if len(methods) != 3 {
		t.Fatalf("expected 3 fallback methods, got %d", len(methods))
	}
}

func writeDescriptorSet(t *testing.T, fds *descriptorpb.FileDescriptorSet) string {
	t.Helper()
	raw, err := proto.Marshal(fds)
	if err != nil {
		t.Fatalf("marshal descriptor set: %v", err)
	}
	file, err := os.CreateTemp(t.TempDir(), "api-*.pb")
	if err != nil {
		t.Fatalf("create descriptor file: %v", err)
	}
	if _, err := file.Write(raw); err != nil {
		t.Fatalf("write descriptor file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close descriptor file: %v", err)
	}
	return file.Name()
}

func testDescriptorSet() *descriptorpb.FileDescriptorSet {
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("acme/billing/invoice.proto"),
		Package: proto.String("acme.billing"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("GetInvoiceRequest"), Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("id"),
				JsonName: proto.String("id"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}}},
			{Name: proto.String("Invoice"), Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("id"),
				JsonName: proto.String("id"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}}},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("InvoiceService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{
					Name:       proto.String("GetInvoice"),
					InputType:  proto.String(".acme.billing.GetInvoiceRequest"),
					OutputType: proto.String(".acme.billing.Invoice"),
				},
				{
					Name:            proto.String("WatchInvoices"),
					InputType:       proto.String(".acme.billing.GetInvoiceRequest"),
					OutputType:      proto.String(".acme.billing.Invoice"),
					ServerStreaming: proto.Bool(true),
				},
				{
					Name:       proto.String("DeleteInvoice"),
					InputType:  proto.String(".acme.billing.GetInvoiceRequest"),
					OutputType: proto.String(".acme.billing.Invoice"),
				},
			},
		}},
	}}}
}
