package lightning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent/toolgrpc"
	"metiq/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	lndSendPaymentV2  = "/routerrpc.Router/SendPaymentV2"
	lndTrackPaymentV2 = "/routerrpc.Router/TrackPaymentV2"
)

// LNDPayer uses pinned descriptors and Router server-streaming RPCs without
// generated LND clients.
type LNDPayer struct {
	id        string
	profileID string
	manager   *toolgrpc.ConnectionManager
	conn      grpc.ClientConnInterface
	files     *protoregistry.Files

	closeOnce sync.Once
	closeErr  error
}

// NewLNDPayer builds an independently-owned connection manager. It deliberately
// does not share the dynamic tool provider's lifecycle.
func NewLNDPayer(profile config.LightningGRPCProfile, resolver toolgrpc.ValueResolver) (*LNDPayer, error) {
	if !profile.PayerEnabled {
		return nil, fmt.Errorf("LND profile %q is not payer enabled", profile.ID)
	}
	sources, err := BuildGRPCEndpointSources(config.LightningConfig{
		LND: config.LNDProfilesConfig{Profiles: []config.LightningGRPCProfile{profile}},
	})
	if err != nil {
		return nil, err
	}
	if len(sources) != 1 {
		return nil, errors.New("LND payer profile did not produce one endpoint")
	}
	source := sources[0]
	manager, err := toolgrpc.NewConnectionManagerWithOptions(
		[]config.GRPCEndpointConfig{source.Profile},
		toolgrpc.WithValueResolver(resolver),
		toolgrpc.WithMetadataSources(map[string]map[string]toolgrpc.CredentialSource{
			source.Profile.ID: source.MetadataSources,
		}),
	)
	if err != nil {
		return nil, err
	}
	payer, err := newLNDPayer(strings.TrimSpace(profile.ID), source.Profile.ID, manager, nil, source.DescriptorSet)
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	return payer, nil
}

// NewLNDPayerWithConnection constructs a payer over an injected connection.
// It is intended for descriptor-backed bufconn tests and embedded runtimes.
func NewLNDPayerWithConnection(id string, conn grpc.ClientConnInterface) (*LNDPayer, error) {
	set, err := BundledDescriptorSet(FamilyLND)
	if err != nil {
		return nil, err
	}
	return newLNDPayer(id, "", nil, conn, set)
}

func newLNDPayer(id, profileID string, manager *toolgrpc.ConnectionManager, conn grpc.ClientConnInterface, set *descriptorpb.FileDescriptorSet) (*LNDPayer, error) {
	if set == nil {
		return nil, errors.New("LND descriptor set is unavailable")
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("build LND descriptor registry: %w", err)
	}
	for _, method := range []string{lndSendPaymentV2, lndTrackPaymentV2} {
		if _, err := findLNDMethod(files, method); err != nil {
			return nil, err
		}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = "lnd"
	}
	if manager == nil && conn == nil {
		return nil, errors.New("LND payer connection is unavailable")
	}
	return &LNDPayer{id: id, profileID: profileID, manager: manager, conn: conn, files: files}, nil
}

func (p *LNDPayer) ID() string { return p.id }

func (p *LNDPayer) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.manager != nil {
			p.closeErr = p.manager.Close()
		}
	})
	return p.closeErr
}

func (p *LNDPayer) PayInvoice(ctx context.Context, request PaymentRequest) (PaymentResult, error) {
	method, err := findLNDMethod(p.files, lndSendPaymentV2)
	if err != nil {
		return PaymentResult{}, err
	}
	message := dynamicpb.NewMessage(method.Input())
	setStringField(message, "payment_request", request.Invoice)
	setInt64Field(message, "fee_limit_msat", request.MaxFeeMSat)
	setBoolField(message, "no_inflight_updates", true)
	setBoolField(message, "cancelable", true)
	timeout := time.Until(request.Deadline)
	if timeout <= 0 {
		return PaymentResult{}, context.DeadlineExceeded
	}
	seconds := int64(math.Ceil(timeout.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	if seconds > math.MaxInt32 {
		seconds = math.MaxInt32
	}
	setInt32Field(message, "timeout_seconds", int32(seconds))

	callCtx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	result, submitted, err := p.consumePaymentStream(callCtx, method, message, request.PaymentHash)
	if err != nil {
		if submitted {
			result := PaymentResult{Status: PaymentStatusInFlight, PaymentHash: request.PaymentHash}
			if errors.Is(err, ErrPaymentResultInvalid) {
				return result, err
			}
			return result, nil
		}
		return PaymentResult{}, err
	}
	if result.Status == PaymentStatusSucceeded {
		if err := ValidateSucceededResult(request, result); err != nil {
			return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: request.PaymentHash}, err
		}
	}
	return result, nil
}

func (p *LNDPayer) LookupPayment(ctx context.Context, lookup PaymentLookup) (PaymentResult, error) {
	method, err := findLNDMethod(p.files, lndTrackPaymentV2)
	if err != nil {
		return PaymentResult{}, err
	}
	message := dynamicpb.NewMessage(method.Input())
	setBytesField(message, "payment_hash", lookup.PaymentHash[:])
	setBoolField(message, "no_inflight_updates", true)
	result, _, err := p.consumePaymentStream(ctx, method, message, lookup.PaymentHash)
	if status.Code(err) == codes.NotFound {
		return PaymentResult{Status: PaymentStatusNotFound, PaymentHash: lookup.PaymentHash}, nil
	}
	return result, err
}

func (p *LNDPayer) consumePaymentStream(ctx context.Context, method protoreflect.MethodDescriptor, request *dynamicpb.Message, expectedHash [32]byte) (PaymentResult, bool, error) {
	conn, callCtx, cancel, err := p.connectionAndContext(ctx)
	if err != nil {
		return PaymentResult{}, false, err
	}
	defer cancel()
	fullMethod := "/" + string(method.Parent().FullName()) + "/" + string(method.Name())
	stream, err := conn.NewStream(callCtx, &grpc.StreamDesc{ServerStreams: true}, fullMethod)
	if err != nil {
		return PaymentResult{}, false, err
	}
	if err := stream.SendMsg(request); err != nil {
		return PaymentResult{}, true, err
	}
	if err := stream.CloseSend(); err != nil {
		return PaymentResult{}, true, err
	}
	current := PaymentResult{Status: PaymentStatusInFlight, PaymentHash: expectedHash}
	for {
		update := dynamicpb.NewMessage(method.Output())
		err := stream.RecvMsg(update)
		if errors.Is(err, io.EOF) {
			return current, true, nil
		}
		if err != nil {
			return PaymentResult{}, true, err
		}
		parsed, terminal, err := parseLNDPayment(update, expectedHash)
		if err != nil {
			return PaymentResult{}, true, err
		}
		current = parsed
		if terminal {
			return parsed, true, nil
		}
	}
}

func (p *LNDPayer) connectionAndContext(ctx context.Context) (grpc.ClientConnInterface, context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p.manager == nil {
		return p.conn, ctx, func() {}, nil
	}
	conn, err := p.manager.Conn(ctx, p.profileID)
	if err != nil {
		return nil, nil, nil, err
	}
	callCtx, cancel, err := p.manager.CallContext(ctx, p.profileID, toolgrpc.CallOptions{})
	if err != nil {
		return nil, nil, nil, err
	}
	return conn, callCtx, cancel, nil
}

func parseLNDPayment(message *dynamicpb.Message, expectedHash [32]byte) (PaymentResult, bool, error) {
	statusName, err := enumFieldName(message, "status")
	if err != nil {
		return PaymentResult{}, false, err
	}
	if hashText := strings.TrimSpace(stringField(message, "payment_hash")); hashText != "" {
		hash, err := decodeHash(hashText)
		if err != nil || hash != expectedHash {
			return PaymentResult{}, false, fmt.Errorf("%w: LND returned a different payment hash", ErrPaymentResultInvalid)
		}
	}
	switch statusName {
	case "SUCCEEDED":
		preimage, err := hex.DecodeString(strings.TrimSpace(stringField(message, "payment_preimage")))
		if err != nil || len(preimage) != 32 {
			return PaymentResult{}, false, fmt.Errorf("%w: LND returned an invalid preimage", ErrPaymentResultInvalid)
		}
		return PaymentResult{
			Status: PaymentStatusSucceeded, PaymentHash: expectedHash, Preimage: preimage,
			AmountMSat: int64Field(message, "value_msat"), FeeMSat: int64Field(message, "fee_msat"),
		}, true, nil
	case "FAILED":
		failure, _ := enumFieldName(message, "failure_reason")
		if failure == "" {
			failure = "FAILURE_REASON_ERROR"
		}
		return PaymentResult{
			Status: PaymentStatusFailed, PaymentHash: expectedHash,
			FailureCode:    strings.ToLower(strings.TrimPrefix(failure, "FAILURE_REASON_")),
			FailureMessage: "LND reports terminal payment failure",
		}, true, nil
	case "IN_FLIGHT", "INITIATED", "UNKNOWN":
		return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: expectedHash}, false, nil
	default:
		return PaymentResult{}, false, fmt.Errorf("%w: unknown LND payment status %q", ErrPaymentResultInvalid, statusName)
	}
}

func findLNDMethod(files *protoregistry.Files, fullMethod string) (protoreflect.MethodDescriptor, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(fullMethod), "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash <= 0 || slash == len(trimmed)-1 {
		return nil, fmt.Errorf("invalid LND RPC path %q", fullMethod)
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(trimmed[:slash]))
	if err != nil {
		return nil, fmt.Errorf("find LND service %s: %w", trimmed[:slash], err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("LND descriptor %s is not a service", trimmed[:slash])
	}
	method := service.Methods().ByName(protoreflect.Name(trimmed[slash+1:]))
	if method == nil {
		return nil, fmt.Errorf("LND method %s is absent from pinned descriptors", fullMethod)
	}
	return method, nil
}

func field(message *dynamicpb.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(name)
}

func setStringField(message *dynamicpb.Message, name string, value string) {
	if descriptor := field(message, protoreflect.Name(name)); descriptor != nil {
		message.Set(descriptor, protoreflect.ValueOfString(value))
	}
}

func setBytesField(message *dynamicpb.Message, name string, value []byte) {
	if descriptor := field(message, protoreflect.Name(name)); descriptor != nil {
		message.Set(descriptor, protoreflect.ValueOfBytes(append([]byte(nil), value...)))
	}
}

func setInt64Field(message *dynamicpb.Message, name string, value int64) {
	if descriptor := field(message, protoreflect.Name(name)); descriptor != nil {
		message.Set(descriptor, protoreflect.ValueOfInt64(value))
	}
}

func setInt32Field(message *dynamicpb.Message, name string, value int32) {
	if descriptor := field(message, protoreflect.Name(name)); descriptor != nil {
		message.Set(descriptor, protoreflect.ValueOfInt32(value))
	}
}

func setBoolField(message *dynamicpb.Message, name string, value bool) {
	if descriptor := field(message, protoreflect.Name(name)); descriptor != nil {
		message.Set(descriptor, protoreflect.ValueOfBool(value))
	}
}

func stringField(message *dynamicpb.Message, name string) string {
	if descriptor := field(message, protoreflect.Name(name)); descriptor != nil {
		return message.Get(descriptor).String()
	}
	return ""
}

func int64Field(message *dynamicpb.Message, name string) int64 {
	if descriptor := field(message, protoreflect.Name(name)); descriptor != nil {
		return message.Get(descriptor).Int()
	}
	return 0
}

func enumFieldName(message *dynamicpb.Message, name string) (string, error) {
	descriptor := field(message, protoreflect.Name(name))
	if descriptor == nil || descriptor.Enum() == nil {
		return "", fmt.Errorf("LND response field %s is missing", name)
	}
	value := descriptor.Enum().Values().ByNumber(message.Get(descriptor).Enum())
	if value == nil {
		return "", fmt.Errorf("LND response field %s has an unknown enum value", name)
	}
	return string(value.Name()), nil
}
