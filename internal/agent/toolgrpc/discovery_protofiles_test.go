package toolgrpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/config"
)

const echoProtoSource = `syntax = "proto3";
package acme.echo;

message EchoRequest {
  string message = 1;
}

message EchoResponse {
  string message = 1;
  int32 length = 2;
}

service EchoService {
  rpc Echo(EchoRequest) returns (EchoResponse);
  rpc ServerStream(EchoRequest) returns (stream EchoResponse);
}
`

// TestDiscoverFromProtoFilesCompilesRealDescriptors proves the proto_files
// discovery mode actually compiles .proto sources into descriptors and
// normalizes them into MethodSpec values (rather than hard-stopping with a
// "not implemented" error). It drives the public Discover entrypoint that
// previously rejected the mode.
func TestDiscoverFromProtoFilesCompilesRealDescriptors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "echo.proto"), []byte(echoProtoSource), 0o600); err != nil {
		t.Fatalf("write proto: %v", err)
	}

	profile := config.GRPCEndpointConfig{
		ID: "echo",
		Discovery: config.GRPCDiscoveryConfig{
			Mode:        config.GRPCDiscoveryModeProtoFiles,
			ProtoFiles:  []string{"echo.proto"},
			ImportPaths: []string{dir},
		},
		Exposure: config.GRPCExposureConfig{Namespace: "grpc_echo"},
	}

	methods, err := Discover(context.Background(), profile, nil)
	if err != nil {
		t.Fatalf("Discover(proto_files): %v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("expected 2 methods, got %d: %#v", len(methods), methods)
	}

	// Methods are sorted by FullMethod: Echo < ServerStream.
	unary := methods[0]
	if unary.FullMethod != "/acme.echo.EchoService/Echo" {
		t.Fatalf("unexpected first method: %#v", unary)
	}
	if unary.RequestType != "acme.echo.EchoRequest" || unary.ResponseType != "acme.echo.EchoResponse" {
		t.Fatalf("unexpected types: request=%q response=%q", unary.RequestType, unary.ResponseType)
	}
	if unary.ClientStreaming || unary.ServerStreaming {
		t.Fatalf("Echo should be unary, got client=%v server=%v", unary.ClientStreaming, unary.ServerStreaming)
	}
	if unary.ToolBaseName != "grpc_echo_acme_echo_echo_service_echo" {
		t.Fatalf("unexpected tool base name: %q", unary.ToolBaseName)
	}
	// A real compiled descriptor yields a populated request schema, not a stub.
	if unary.RequestDescriptor == nil || unary.RequestSchema == nil {
		t.Fatalf("expected populated descriptor/schema, got descriptor=%v schema=%v", unary.RequestDescriptor, unary.RequestSchema)
	}
	if _, ok := unary.RequestSchema["properties"]; !ok {
		t.Fatalf("expected request schema properties, got %#v", unary.RequestSchema)
	}

	stream := methods[1]
	if stream.FullMethod != "/acme.echo.EchoService/ServerStream" {
		t.Fatalf("unexpected second method: %#v", stream)
	}
	if !stream.ServerStreaming || stream.ClientStreaming {
		t.Fatalf("ServerStream streaming flags wrong: client=%v server=%v", stream.ClientStreaming, stream.ServerStreaming)
	}
}

// TestDiscoverFromProtoFilesRequiresFiles ensures the mode fails fast with a
// clear error when no proto files are configured, instead of silently
// appearing supported.
func TestDiscoverFromProtoFilesRequiresFiles(t *testing.T) {
	profile := config.GRPCEndpointConfig{
		ID:        "echo",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeProtoFiles},
	}
	_, err := Discover(context.Background(), profile, nil)
	if err == nil {
		t.Fatal("expected error for proto_files mode with no files")
	}
	if !strings.Contains(err.Error(), "at least one proto file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDiscoverFromProtoFilesSurfacesCompileErrors proves a malformed proto is
// reported as a real compile error rather than a placeholder success.
func TestDiscoverFromProtoFilesSurfacesCompileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.proto"), []byte("this is not valid proto"), 0o600); err != nil {
		t.Fatalf("write proto: %v", err)
	}
	profile := config.GRPCEndpointConfig{
		ID: "broken",
		Discovery: config.GRPCDiscoveryConfig{
			Mode:        config.GRPCDiscoveryModeProtoFiles,
			ProtoFiles:  []string{"broken.proto"},
			ImportPaths: []string{dir},
		},
	}
	_, err := Discover(context.Background(), profile, nil)
	if err == nil {
		t.Fatal("expected compile error for malformed proto")
	}
	if strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("mode should be implemented, got: %v", err)
	}
}
