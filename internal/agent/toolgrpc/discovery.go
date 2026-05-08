package toolgrpc

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"metiq/internal/config"

	"google.golang.org/grpc"
	reflectionv1pb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alphapb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// MethodSpec is the discovery-normalized representation of a gRPC method.
// Reflection and static descriptor loading both normalize into this shape so
// downstream registration/invocation code does not need to know the source.
type MethodSpec struct {
	ProfileID          string
	ToolBaseName       string
	FullMethod         string // /package.Service/Method
	ServiceName        string
	MethodName         string
	ClientStreaming    bool
	ServerStreaming    bool
	RequestType        string
	ResponseType       string
	RequestDescriptor  protoreflect.MessageDescriptor
	ResponseDescriptor protoreflect.MessageDescriptor
	Resolver           *protoregistry.Types
	RequestSchema      map[string]any
	ResponseSchema     map[string]any
	Description        string
}

// Discover loads methods for a configured endpoint. Reflection is preferred for
// reflection-mode profiles; if reflection fails and a descriptor_set is present,
// the descriptor set is used as a static fallback.
func Discover(ctx context.Context, profile config.GRPCEndpointConfig, conn grpc.ClientConnInterface) ([]MethodSpec, error) {
	mode := profile.Discovery.EffectiveMode()
	switch mode {
	case config.GRPCDiscoveryModeReflection:
		reflectionCtx, cancel := reflectionContext(ctx, profile)
		defer cancel()
		methods, err := DiscoverFromReflection(reflectionCtx, profile, conn)
		if err == nil {
			return methods, nil
		}
		if strings.TrimSpace(profile.Discovery.DescriptorSet) == "" {
			return nil, err
		}
		fallback, fallbackErr := DiscoverFromDescriptorSet(profile)
		if fallbackErr != nil {
			return nil, fmt.Errorf("reflection discovery failed: %w; static descriptor fallback failed: %v", err, fallbackErr)
		}
		return fallback, nil
	case config.GRPCDiscoveryModeDescriptorSet:
		return DiscoverFromDescriptorSet(profile)
	case config.GRPCDiscoveryModeProtoFiles:
		return nil, fmt.Errorf("gRPC proto_files discovery is not implemented yet; provide a descriptor_set or enable reflection")
	default:
		return nil, fmt.Errorf("unsupported gRPC discovery mode %q", profile.Discovery.Mode)
	}
}

// DiscoverFromReflection queries gRPC server reflection and normalizes all
// returned service descriptors into MethodSpec values.
func DiscoverFromReflection(ctx context.Context, profile config.GRPCEndpointConfig, conn grpc.ClientConnInterface) ([]MethodSpec, error) {
	if conn == nil {
		return nil, fmt.Errorf("gRPC reflection discovery requires a connection")
	}
	fds, err := LoadDescriptorSetFromReflection(ctx, conn)
	if err != nil {
		return nil, err
	}
	return DiscoverFromFileDescriptorSet(profile, fds)
}

// LoadDescriptorSetFromReflection returns the transitive descriptors advertised
// by server reflection.
func LoadDescriptorSetFromReflection(ctx context.Context, conn grpc.ClientConnInterface) (*descriptorpb.FileDescriptorSet, error) {
	fds, err := loadDescriptorSetFromReflectionV1(ctx, conn)
	if err == nil {
		return fds, nil
	}
	alphaFDS, alphaErr := loadDescriptorSetFromReflectionV1Alpha(ctx, conn)
	if alphaErr == nil {
		return alphaFDS, nil
	}
	return nil, fmt.Errorf("gRPC reflection v1 failed: %w; v1alpha failed: %v", err, alphaErr)
}

func loadDescriptorSetFromReflectionV1(ctx context.Context, conn grpc.ClientConnInterface) (*descriptorpb.FileDescriptorSet, error) {
	client := reflectionv1pb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("open gRPC reflection v1 stream: %w", err)
	}

	if err := stream.Send(&reflectionv1pb.ServerReflectionRequest{
		MessageRequest: &reflectionv1pb.ServerReflectionRequest_ListServices{ListServices: "*"},
	}); err != nil {
		return nil, fmt.Errorf("send reflection v1 ListServices request: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("receive reflection v1 ListServices response: %w", err)
	}
	servicesResp := resp.GetListServicesResponse()
	if servicesResp == nil {
		return nil, fmt.Errorf("reflection v1 ListServices returned %T", resp.GetMessageResponse())
	}

	filesByName := map[string]*descriptorpb.FileDescriptorProto{}
	for _, service := range servicesResp.Service {
		name := strings.TrimSpace(service.Name)
		if name == "" || strings.HasPrefix(name, "grpc.reflection.") {
			continue
		}
		if err := stream.Send(&reflectionv1pb.ServerReflectionRequest{
			MessageRequest: &reflectionv1pb.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: name},
		}); err != nil {
			return nil, fmt.Errorf("send reflection v1 FileContainingSymbol %q request: %w", name, err)
		}
		fileResp, err := stream.Recv()
		if err != nil {
			return nil, fmt.Errorf("receive reflection v1 FileContainingSymbol %q response: %w", name, err)
		}
		if e := fileResp.GetErrorResponse(); e != nil {
			return nil, fmt.Errorf("reflection v1 FileContainingSymbol %q failed: code=%d message=%s", name, e.ErrorCode, e.ErrorMessage)
		}
		fdr := fileResp.GetFileDescriptorResponse()
		if fdr == nil {
			return nil, fmt.Errorf("reflection v1 FileContainingSymbol %q returned %T", name, fileResp.GetMessageResponse())
		}
		if err := addReflectedDescriptorBytes(filesByName, name, fdr.FileDescriptorProto); err != nil {
			return nil, err
		}
	}
	if err := stream.CloseSend(); err != nil {
		return nil, fmt.Errorf("close reflection v1 send stream: %w", err)
	}
	return descriptorSetFromFileMap(filesByName)
}

func loadDescriptorSetFromReflectionV1Alpha(ctx context.Context, conn grpc.ClientConnInterface) (*descriptorpb.FileDescriptorSet, error) {
	client := reflectionv1alphapb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("open gRPC reflection v1alpha stream: %w", err)
	}

	if err := stream.Send(&reflectionv1alphapb.ServerReflectionRequest{
		MessageRequest: &reflectionv1alphapb.ServerReflectionRequest_ListServices{ListServices: "*"},
	}); err != nil {
		return nil, fmt.Errorf("send reflection v1alpha ListServices request: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("receive reflection v1alpha ListServices response: %w", err)
	}
	servicesResp := resp.GetListServicesResponse()
	if servicesResp == nil {
		return nil, fmt.Errorf("reflection v1alpha ListServices returned %T", resp.GetMessageResponse())
	}

	filesByName := map[string]*descriptorpb.FileDescriptorProto{}
	for _, service := range servicesResp.Service {
		name := strings.TrimSpace(service.Name)
		if name == "" || strings.HasPrefix(name, "grpc.reflection.") {
			continue
		}
		if err := stream.Send(&reflectionv1alphapb.ServerReflectionRequest{
			MessageRequest: &reflectionv1alphapb.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: name},
		}); err != nil {
			return nil, fmt.Errorf("send reflection v1alpha FileContainingSymbol %q request: %w", name, err)
		}
		fileResp, err := stream.Recv()
		if err != nil {
			return nil, fmt.Errorf("receive reflection v1alpha FileContainingSymbol %q response: %w", name, err)
		}
		if e := fileResp.GetErrorResponse(); e != nil {
			return nil, fmt.Errorf("reflection v1alpha FileContainingSymbol %q failed: code=%d message=%s", name, e.ErrorCode, e.ErrorMessage)
		}
		fdr := fileResp.GetFileDescriptorResponse()
		if fdr == nil {
			return nil, fmt.Errorf("reflection v1alpha FileContainingSymbol %q returned %T", name, fileResp.GetMessageResponse())
		}
		if err := addReflectedDescriptorBytes(filesByName, name, fdr.FileDescriptorProto); err != nil {
			return nil, err
		}
	}
	if err := stream.CloseSend(); err != nil {
		return nil, fmt.Errorf("close reflection v1alpha send stream: %w", err)
	}
	return descriptorSetFromFileMap(filesByName)
}

func addReflectedDescriptorBytes(filesByName map[string]*descriptorpb.FileDescriptorProto, serviceName string, rawFiles [][]byte) error {
	for _, raw := range rawFiles {
		var fd descriptorpb.FileDescriptorProto
		if err := proto.Unmarshal(raw, &fd); err != nil {
			return fmt.Errorf("decode reflected descriptor for %q: %w", serviceName, err)
		}
		if fd.GetName() != "" {
			filesByName[fd.GetName()] = &fd
		}
	}
	return nil
}

func descriptorSetFromFileMap(filesByName map[string]*descriptorpb.FileDescriptorProto) (*descriptorpb.FileDescriptorSet, error) {
	if len(filesByName) == 0 {
		return nil, fmt.Errorf("gRPC reflection returned no service descriptors")
	}
	names := make([]string, 0, len(filesByName))
	for name := range filesByName {
		names = append(names, name)
	}
	sort.Strings(names)
	fds := &descriptorpb.FileDescriptorSet{File: make([]*descriptorpb.FileDescriptorProto, 0, len(names))}
	for _, name := range names {
		fds.File = append(fds.File, filesByName[name])
	}
	return fds, nil
}

// DiscoverFromDescriptorSet loads a static FileDescriptorSet path from the
// endpoint profile and normalizes it into MethodSpec values.
func DiscoverFromDescriptorSet(profile config.GRPCEndpointConfig) ([]MethodSpec, error) {
	path := strings.TrimSpace(profile.Discovery.DescriptorSet)
	if path == "" {
		return nil, fmt.Errorf("gRPC descriptor_set path is required")
	}
	fds, err := LoadDescriptorSetFile(path)
	if err != nil {
		return nil, err
	}
	return DiscoverFromFileDescriptorSet(profile, fds)
}

// LoadDescriptorSetFile reads a protoc --descriptor_set_out style file.
func LoadDescriptorSetFile(path string) (*descriptorpb.FileDescriptorSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gRPC descriptor set %q: %w", path, err)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &fds); err != nil {
		return nil, fmt.Errorf("decode gRPC descriptor set %q: %w", path, err)
	}
	if len(fds.File) == 0 {
		return nil, fmt.Errorf("gRPC descriptor set %q contains no files", path)
	}
	return &fds, nil
}

// DiscoverFromFileDescriptorSet normalizes a parsed FileDescriptorSet into
// MethodSpec values.
func DiscoverFromFileDescriptorSet(profile config.GRPCEndpointConfig, fds *descriptorpb.FileDescriptorSet) ([]MethodSpec, error) {
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return nil, fmt.Errorf("build gRPC descriptor registry: %w", err)
	}
	return NormalizeMethodSpecs(profile, files)
}

// NormalizeMethodSpecs walks a descriptor registry and returns stable, filtered
// MethodSpec values with protobuf-derived request/response JSON schemas.
func NormalizeMethodSpecs(profile config.GRPCEndpointConfig, files *protoregistry.Files) ([]MethodSpec, error) {
	if files == nil {
		return nil, fmt.Errorf("descriptor registry is nil")
	}
	profileID := strings.TrimSpace(profile.ID)
	if profileID == "" {
		profileID = "grpc"
	}

	converter := NewSchemaConverter()
	resolver, err := TypeResolverFromFiles(files)
	if err != nil {
		return nil, err
	}
	var methods []MethodSpec
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			svc := services.Get(i)
			serviceName := string(svc.FullName())
			if !serviceIncluded(serviceName, profile.Exposure.IncludeServices) {
				continue
			}
			svcMethods := svc.Methods()
			for j := 0; j < svcMethods.Len(); j++ {
				m := svcMethods.Get(j)
				fullMethod := "/" + serviceName + "/" + string(m.Name())
				if methodExcluded(fullMethod, serviceName, string(m.Name()), profile.Exposure.ExcludeMethods) {
					continue
				}
				methods = append(methods, MethodSpec{
					ProfileID:          profileID,
					FullMethod:         fullMethod,
					ServiceName:        serviceName,
					MethodName:         string(m.Name()),
					ClientStreaming:    m.IsStreamingClient(),
					ServerStreaming:    m.IsStreamingServer(),
					RequestType:        string(m.Input().FullName()),
					ResponseType:       string(m.Output().FullName()),
					RequestDescriptor:  m.Input(),
					ResponseDescriptor: m.Output(),
					Resolver:           resolver,
					RequestSchema:      converter.MessageToJSONSchema(m.Input()),
					ResponseSchema:     converter.MessageToJSONSchema(m.Output()),
					Description:        fmt.Sprintf("Call gRPC method %s", fullMethod),
				})
			}
		}
		return true
	})

	sort.Slice(methods, func(i, j int) bool { return methods[i].FullMethod < methods[j].FullMethod })
	assignToolBaseNames(methods, profile)
	return methods, nil
}

// TypeResolverFromFiles builds a protobuf JSON resolver for dynamically
// discovered message and enum types. protojson needs this for Any payloads whose
// concrete types came from reflection or a static descriptor set instead of the
// process-wide generated registry.
func TypeResolverFromFiles(files *protoregistry.Files) (*protoregistry.Types, error) {
	if files == nil {
		return nil, fmt.Errorf("descriptor registry is nil")
	}
	types := new(protoregistry.Types)
	var registerErr error
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		registerErr = registerFileTypes(types, fd)
		return registerErr == nil
	})
	if registerErr != nil {
		return nil, registerErr
	}
	return types, nil
}

func registerFileTypes(types *protoregistry.Types, fd protoreflect.FileDescriptor) error {
	for i := 0; i < fd.Enums().Len(); i++ {
		if err := types.RegisterEnum(dynamicpb.NewEnumType(fd.Enums().Get(i))); err != nil {
			return fmt.Errorf("register protobuf enum %s: %w", fd.Enums().Get(i).FullName(), err)
		}
	}
	for i := 0; i < fd.Messages().Len(); i++ {
		if err := registerMessageTypes(types, fd.Messages().Get(i)); err != nil {
			return err
		}
	}
	return nil
}

func registerMessageTypes(types *protoregistry.Types, md protoreflect.MessageDescriptor) error {
	if err := types.RegisterMessage(dynamicpb.NewMessageType(md)); err != nil {
		return fmt.Errorf("register protobuf message %s: %w", md.FullName(), err)
	}
	for i := 0; i < md.Enums().Len(); i++ {
		if err := types.RegisterEnum(dynamicpb.NewEnumType(md.Enums().Get(i))); err != nil {
			return fmt.Errorf("register protobuf enum %s: %w", md.Enums().Get(i).FullName(), err)
		}
	}
	for i := 0; i < md.Messages().Len(); i++ {
		if err := registerMessageTypes(types, md.Messages().Get(i)); err != nil {
			return err
		}
	}
	return nil
}

func reflectionContext(ctx context.Context, profile config.GRPCEndpointConfig) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	ms := profile.Defaults.EffectiveReflectionTimeoutMS()
	if ms <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
}

func serviceIncluded(serviceName string, includes []string) bool {
	if len(includes) == 0 {
		return true
	}
	for _, include := range includes {
		if strings.TrimSpace(include) == serviceName {
			return true
		}
	}
	return false
}

func methodExcluded(fullMethod, serviceName, methodName string, excludes []string) bool {
	serviceMethod := serviceName + "/" + methodName
	for _, exclude := range excludes {
		exclude = strings.TrimSpace(exclude)
		if exclude == "" {
			continue
		}
		switch {
		case strings.HasPrefix(exclude, "/"):
			if exclude == fullMethod {
				return true
			}
		case strings.Contains(exclude, "/"):
			if exclude == serviceMethod {
				return true
			}
		case exclude == methodName:
			return true
		}
	}
	return false
}

func assignToolBaseNames(methods []MethodSpec, profile config.GRPCEndpointConfig) {
	counts := map[string]int{}
	for i := range methods {
		base := toolBaseName(profile, methods[i].ServiceName, methods[i].MethodName)
		methods[i].ToolBaseName = base
		counts[base]++
	}
	for i := range methods {
		if counts[methods[i].ToolBaseName] > 1 {
			methods[i].ToolBaseName += "_" + shortHash(methods[i].FullMethod)
		}
	}
}

func toolBaseName(profile config.GRPCEndpointConfig, serviceName, methodName string) string {
	namespace := strings.TrimSpace(profile.Exposure.Namespace)
	if namespace == "" {
		namespace = "grpc_" + snakeIdentifier(profile.ID)
	}
	return trimRepeatedUnderscores(namespace + "_" + snakeIdentifier(serviceName) + "_" + snakeIdentifier(methodName))
}

var nonIdentifierRunes = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func snakeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "grpc"
	}
	value = nonIdentifierRunes.ReplaceAllString(value, "_")
	var out []rune
	for i, r := range value {
		if i > 0 && unicode.IsUpper(r) {
			prev := rune(value[i-1])
			if prev != '_' && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				out = append(out, '_')
			}
		}
		out = append(out, unicode.ToLower(r))
	}
	return trimRepeatedUnderscores(string(out))
}

func trimRepeatedUnderscores(value string) string {
	value = strings.Trim(value, "_")
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if r == '_' {
			if !lastUnderscore {
				b.WriteRune(r)
			}
			lastUnderscore = true
			continue
		}
		b.WriteRune(r)
		lastUnderscore = false
	}
	out := b.String()
	if out == "" {
		return "grpc"
	}
	return out
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}
