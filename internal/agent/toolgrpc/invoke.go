package toolgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/config"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/dynamicpb"
)

// UnaryExecutor exposes discovered unary gRPC methods through the standard
// agent.ToolExecutor interface. It delegates validation and phase wrapping to
// ToolRegistry so gRPC tools behave like built-in, plugin, and MCP tools.
type UnaryExecutor struct {
	registry *agent.ToolRegistry
	manager  *ConnectionManager
}

var _ agent.ToolExecutor = (*UnaryExecutor)(nil)

// NewUnaryExecutor registers every unary MethodSpec as an executable gRPC tool.
// Streaming methods are intentionally ignored; they are owned by the streaming
// session implementation.
func NewUnaryExecutor(manager *ConnectionManager, methods []MethodSpec) (*UnaryExecutor, error) {
	if manager == nil {
		return nil, errors.New("grpc connection manager is nil")
	}
	registry := agent.NewToolRegistry()
	exec := &UnaryExecutor{registry: registry, manager: manager}
	for _, method := range methods {
		if method.ClientStreaming || method.ServerStreaming {
			continue
		}
		if strings.TrimSpace(method.ToolBaseName) == "" {
			return nil, fmt.Errorf("grpc unary method %q has no tool name", method.FullMethod)
		}
		if strings.TrimSpace(method.FullMethod) == "" {
			return nil, fmt.Errorf("grpc unary tool %q has no full method", method.ToolBaseName)
		}
		if method.RequestDescriptor == nil || method.ResponseDescriptor == nil {
			return nil, fmt.Errorf("grpc unary method %q is missing protobuf descriptors", method.FullMethod)
		}
		method := method
		registry.RegisterTool(method.ToolBaseName, agent.ToolRegistration{
			Func: func(ctx context.Context, args map[string]any) (string, error) {
				return exec.invoke(ctx, method, args)
			},
			Descriptor:      unaryDescriptor(method),
			ProviderVisible: true,
			Validate: func(ctx context.Context, call agent.ToolCall, desc agent.ToolDescriptor) error {
				return exec.validate(ctx, method, call.Args)
			},
		})
	}
	return exec, nil
}

func (e *UnaryExecutor) Execute(ctx context.Context, call agent.ToolCall) (string, error) {
	if e == nil || e.registry == nil {
		return "", errors.New("grpc unary executor is nil")
	}
	return e.registry.Execute(ctx, call)
}

func (e *UnaryExecutor) ProviderDescriptors() []agent.ToolDescriptor {
	if e == nil || e.registry == nil {
		return nil
	}
	return e.registry.ProviderDescriptors()
}

func (e *UnaryExecutor) Definitions() []agent.ToolDefinition {
	if e == nil || e.registry == nil {
		return nil
	}
	return e.registry.Definitions()
}

func (e *UnaryExecutor) Descriptor(name string) (agent.ToolDescriptor, bool) {
	if e == nil || e.registry == nil {
		return agent.ToolDescriptor{}, false
	}
	return e.registry.Descriptor(name)
}

func (e *UnaryExecutor) SnapshotToolExecutor() agent.ToolExecutor {
	if e == nil || e.registry == nil {
		return e
	}
	return &UnaryExecutor{registry: e.registry.Snapshot(), manager: e.manager}
}

func unaryDescriptor(method MethodSpec) agent.ToolDescriptor {
	requestSchema := cloneSchemaOrDefault(method.RequestSchema)
	return agent.ToolDescriptor{
		Name:        method.ToolBaseName,
		Description: unaryDescription(method),
		Parameters: agent.ToolParameters{
			Type: "object",
			Properties: map[string]agent.ToolParamProp{
				"request":     {Type: "object", Description: "gRPC request message encoded with protobuf JSON mapping."},
				"metadata":    {Type: "object", Description: "Per-call metadata overrides. Keys must be allowed by the endpoint profile."},
				"deadline_ms": {Type: "integer", Description: "Per-call deadline in milliseconds, clamped to max_deadline_ms."},
			},
			Required: []string{"request"},
		},
		InputJSONSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"request": requestSchema,
				"metadata": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"deadline_ms": map[string]any{"type": "integer", "minimum": 0},
			},
			"required": []string{"request"},
		},
		ParamAliases: map[string]string{
			"args":       "request",
			"body":       "request",
			"input":      "request",
			"headers":    "metadata",
			"timeout_ms": "deadline_ms",
		},
		Origin: agent.ToolOrigin{
			Kind:          agent.ToolOriginKindGRPC,
			ServerName:    method.ProfileID,
			CanonicalName: method.FullMethod,
		},
		Traits: agent.ToolTraits{
			ConcurrencySafe:   true,
			InterruptBehavior: agent.ToolInterruptBehaviorCancel,
		},
	}
}

func unaryDescription(method MethodSpec) string {
	if strings.TrimSpace(method.Description) != "" {
		return method.Description
	}
	return fmt.Sprintf("Call gRPC unary method %s", method.FullMethod)
}

func cloneSchemaOrDefault(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	return out
}

func (e *UnaryExecutor) validate(_ context.Context, method MethodSpec, args map[string]any) error {
	profile, ok := e.manager.Profile(method.ProfileID)
	if !ok {
		return fmt.Errorf("grpc profile %q is not configured", method.ProfileID)
	}
	metadataArgs, err := unaryMetadataFromArgs(args)
	if err != nil {
		return err
	}
	allowed := allowedOverrideKeys(profile.Auth.AllowOverrideKeys)
	for key := range metadataArgs {
		canonical, err := normalizeMetadataKey(key)
		if err != nil {
			return fmt.Errorf("metadata %q: %w", key, err)
		}
		if !allowed[canonical] {
			return fmt.Errorf("metadata override %q is not allowed for grpc profile %q", canonical, profile.ID)
		}
	}
	return nil
}

func (e *UnaryExecutor) invoke(ctx context.Context, method MethodSpec, args map[string]any) (string, error) {
	request := dynamicpb.NewMessage(method.RequestDescriptor)
	requestJSON, err := json.Marshal(args["request"])
	if err != nil {
		return "", fmt.Errorf("marshal grpc request for %s: %w", method.FullMethod, err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false, Resolver: method.Resolver}).Unmarshal(requestJSON, request); err != nil {
		return "", fmt.Errorf("decode grpc request for %s: %w", method.FullMethod, err)
	}

	metadataArgs, err := unaryMetadataFromArgs(args)
	if err != nil {
		return "", err
	}
	deadlineMS, err := clampedDeadlineFromArgs(e.manager, method.ProfileID, args)
	if err != nil {
		return "", err
	}

	response := dynamicpb.NewMessage(method.ResponseDescriptor)
	started := time.Now()
	err = e.manager.InvokeUnary(ctx, method.ProfileID, method.FullMethod, request, response, CallOptions{
		Metadata:   metadataArgs,
		DeadlineMS: deadlineMS,
	})
	durationMS := time.Since(started).Milliseconds()
	if err != nil {
		return "", grpcStatusError(method.FullMethod, err)
	}
	return marshalUnaryEnvelope(method, response, durationMS)
}

func unaryMetadataFromArgs(args map[string]any) (map[string]string, error) {
	raw, ok := args["metadata"]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("metadata must be an object")
	}
	out := make(map[string]string, len(m))
	for key, value := range m {
		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("metadata %q must be a string", key)
		}
		out[key] = str
	}
	return out, nil
}

func clampedDeadlineFromArgs(manager *ConnectionManager, profileID string, args map[string]any) (int, error) {
	raw, ok := args["deadline_ms"]
	if !ok || raw == nil {
		return 0, nil
	}
	requested, err := intFromJSONValue(raw)
	if err != nil {
		return 0, fmt.Errorf("deadline_ms: %w", err)
	}
	if requested <= 0 {
		return requested, nil
	}
	profile, ok := manager.Profile(profileID)
	if !ok {
		return 0, fmt.Errorf("grpc profile %q is not configured", profileID)
	}
	maxMS := profile.Defaults.EffectiveMaxDeadlineMS()
	if maxMS <= 0 {
		maxMS = config.DefaultGRPCMaxDeadlineMS
	}
	if requested > maxMS {
		return maxMS, nil
	}
	return requested, nil
}

func intFromJSONValue(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		minInt, maxInt := intBounds()
		if v > maxInt || v < minInt {
			return 0, fmt.Errorf("value %d overflows int", v)
		}
		return int(v), nil
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("must be an integer")
		}
		minInt, maxInt := intBounds()
		if v > float64(maxInt) || v < float64(minInt) {
			return 0, fmt.Errorf("value %.0f overflows int", v)
		}
		return int(v), nil
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, err
		}
		return intFromJSONValue(parsed)
	default:
		return 0, fmt.Errorf("must be an integer")
	}
}

func intBounds() (int64, int64) {
	maxInt := int64(^uint(0) >> 1)
	return -maxInt - 1, maxInt
}

func marshalUnaryEnvelope(method MethodSpec, response *dynamicpb.Message, durationMS int64) (string, error) {
	responseJSON, err := (protojson.MarshalOptions{UseProtoNames: false, Resolver: method.Resolver}).Marshal(response)
	if err != nil {
		return "", fmt.Errorf("encode grpc response for %s: %w", method.FullMethod, err)
	}
	var responseValue any
	if len(responseJSON) == 0 {
		responseValue = map[string]any{}
	} else if err := json.Unmarshal(responseJSON, &responseValue); err != nil {
		return "", fmt.Errorf("decode grpc response JSON for %s: %w", method.FullMethod, err)
	}
	envelope := map[string]any{
		"ok":          true,
		"profile":     method.ProfileID,
		"method":      method.FullMethod,
		"response":    responseValue,
		"status":      map[string]any{"code": codes.OK.String()},
		"duration_ms": durationMS,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode grpc result envelope for %s: %w", method.FullMethod, err)
	}
	return string(encoded), nil
}

func grpcStatusError(method string, err error) error {
	if st, ok := status.FromError(err); ok {
		if st.Code() == codes.OK {
			return nil
		}
		if msg := strings.TrimSpace(st.Message()); msg != "" {
			return fmt.Errorf("grpc unary %s failed: code=%s message=%s", method, st.Code(), msg)
		}
		return fmt.Errorf("grpc unary %s failed: code=%s", method, st.Code())
	}
	return fmt.Errorf("grpc unary %s failed: %w", method, err)
}
