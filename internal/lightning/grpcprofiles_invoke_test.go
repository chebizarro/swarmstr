package lightning

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/agent/toolgrpc"
	"metiq/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type bundledGetInfoService interface {
	getInfo(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)
}

type bundledGetInfoServer struct {
	method protoreflect.MethodDescriptor
}

func (s *bundledGetInfoServer) getInfo(ctx context.Context, _ *dynamicpb.Message) (*dynamicpb.Message, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get("macaroon")) != 1 || md.Get("macaroon")[0] != "00abff" {
		return nil, fmt.Errorf("macaroon metadata mismatch")
	}
	response := dynamicpb.NewMessage(s.method.Output())
	alias := response.Descriptor().Fields().ByName("alias")
	if alias == nil {
		return nil, fmt.Errorf("pinned GetInfoResponse is missing alias")
	}
	response.Set(alias, protoreflect.ValueOfString("dynamic-lnd"))
	return response, nil
}

func TestBundledLNDDescriptorDynamicInvocationAndMacaroonEncoding(t *testing.T) {
	cfg := config.LightningConfig{LND: config.LNDProfilesConfig{Profiles: []config.LightningGRPCProfile{{
		ID:          "regtest",
		Target:      "unused:10009",
		Network:     config.LightningNetworkRegtest,
		TLSCertFile: "/tmp/unused.cert",
		Macaroon: config.CredentialSourceConfig{
			Ref:      "file:/tmp/replaced.macaroon",
			Encoding: config.CredentialEncodingHex,
		},
		Exposure: config.GRPCExposureConfig{Mode: config.GRPCExposureModeInline},
	}}}}
	sources, err := BuildGRPCEndpointSources(cfg)
	if err != nil {
		t.Fatalf("BuildGRPCEndpointSources: %v", err)
	}
	source := sources[0]
	fullMethod := "/lnrpc.Lightning/GetInfo"
	source.ToolNames = map[string]string{fullMethod: source.ToolNames[fullMethod]}
	source.ToolTraits = map[string]agent.ToolTraits{fullMethod: source.ToolTraits[fullMethod]}
	source.ToolDescriptions = map[string]string{fullMethod: source.ToolDescriptions[fullMethod]}

	files, err := protodesc.NewFiles(source.DescriptorSet)
	if err != nil {
		t.Fatalf("build descriptor registry: %v", err)
	}
	desc, err := files.FindDescriptorByName("lnrpc.Lightning")
	if err != nil {
		t.Fatalf("find Lightning service: %v", err)
	}
	service, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		t.Fatalf("lnrpc.Lightning descriptor type = %T", desc)
	}
	getInfo := service.Methods().ByName("GetInfo")
	if getInfo == nil {
		t.Fatal("GetInfo method missing from bundled LND descriptor")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	implementation := &bundledGetInfoServer{method: getInfo}
	serviceDesc := grpc.ServiceDesc{
		ServiceName: string(getInfo.Parent().FullName()),
		HandlerType: (*bundledGetInfoService)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: string(getInfo.Name()),
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				request := dynamicpb.NewMessage(getInfo.Input())
				if err := dec(request); err != nil {
					return nil, err
				}
				call := func(ctx context.Context, req any) (any, error) {
					return srv.(bundledGetInfoService).getInfo(ctx, req.(*dynamicpb.Message))
				}
				if interceptor == nil {
					return call(ctx, request)
				}
				return interceptor(ctx, request, &grpc.UnaryServerInfo{Server: srv, FullMethod: fullMethod}, call)
			},
		}},
	}
	server.RegisterService(&serviceDesc, implementation)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	macaroonPath := filepath.Join(t.TempDir(), "readonly.macaroon")
	if err := os.WriteFile(macaroonPath, []byte{0x00, 0xab, 0xff}, 0o600); err != nil {
		t.Fatalf("write macaroon: %v", err)
	}
	source.Profile.Target = listener.Addr().String()
	source.Profile.Transport = config.GRPCTransportConfig{TLSMode: config.GRPCTransportTLSModeInsecure}
	source.MetadataSources["macaroon"] = toolgrpc.CredentialSource{Ref: "file:" + macaroonPath, Encoding: "hex"}

	provider, err := toolgrpc.NewProviderFromSources(context.Background(), []toolgrpc.EndpointSource{source})
	if err != nil {
		t.Fatalf("NewProviderFromSources: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	registry := agent.NewToolRegistry()
	provider.RegisterInto(registry)

	result, err := registry.Execute(context.Background(), agent.ToolCall{Name: "lnd_get_info", Args: map[string]any{"request": map[string]any{}}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "dynamic-lnd") {
		t.Fatalf("dynamic LND response = %s", result)
	}
}
