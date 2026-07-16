package lightning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type dynamicRouterService interface {
	handleRouter(string, *dynamicpb.Message, grpc.ServerStream) error
}

type testRouterService struct {
	t           *testing.T
	preimage    []byte
	hash        [32]byte
	sendStatus  string
	trackStatus string
	requestSeen chan struct{}
}

func (s *testRouterService) handleRouter(method string, request *dynamicpb.Message, stream grpc.ServerStream) error {
	switch method {
	case lndSendPaymentV2:
		if stringField(request, "payment_request") != "lnbc-router-test" {
			s.t.Errorf("payment_request = %q", stringField(request, "payment_request"))
		}
		if int64Field(request, "fee_limit_msat") != 50 {
			s.t.Errorf("fee_limit_msat = %d", int64Field(request, "fee_limit_msat"))
		}
		if descriptor := field(request, "no_inflight_updates"); descriptor == nil || !request.Get(descriptor).Bool() {
			s.t.Error("no_inflight_updates was not set")
		}
		if int64Field(request, "timeout_seconds") <= 0 {
			s.t.Errorf("timeout_seconds = %d", int64Field(request, "timeout_seconds"))
		}
		if s.requestSeen != nil {
			close(s.requestSeen)
		}
		if err := stream.SendMsg(s.paymentMessage(stream, "IN_FLIGHT")); err != nil {
			return err
		}
		return stream.SendMsg(s.paymentMessage(stream, s.sendStatus))
	case lndTrackPaymentV2:
		descriptor := field(request, "payment_hash")
		if descriptor == nil || string(request.Get(descriptor).Bytes()) != string(s.hash[:]) {
			s.t.Errorf("track payment_hash = %x", request.Get(descriptor).Bytes())
		}
		if s.trackStatus == "NOT_FOUND" {
			return status.Error(codes.NotFound, "payment not found")
		}
		return stream.SendMsg(s.paymentMessage(stream, s.trackStatus))
	default:
		return status.Error(codes.Unimplemented, method)
	}
}

func (s *testRouterService) paymentMessage(stream grpc.ServerStream, state string) *dynamicpb.Message {
	// The stream's concrete type does not expose descriptors, so use the pinned
	// registry just as the production payer does.
	set, err := BundledDescriptorSet(FamilyLND)
	if err != nil {
		s.t.Fatalf("BundledDescriptorSet: %v", err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		s.t.Fatalf("protodesc.NewFiles: %v", err)
	}
	method, err := findLNDMethod(files, lndSendPaymentV2)
	if err != nil {
		s.t.Fatalf("find method: %v", err)
	}
	message := dynamicpb.NewMessage(method.Output())
	setStringField(message, "payment_hash", hex.EncodeToString(s.hash[:]))
	setEnumField(s.t, message, "status", state)
	switch state {
	case "SUCCEEDED":
		setStringField(message, "payment_preimage", hex.EncodeToString(s.preimage))
		setInt64Field(message, "value_msat", 2_000)
		setInt64Field(message, "fee_msat", 7)
	case "FAILED":
		setEnumField(s.t, message, "failure_reason", "FAILURE_REASON_NO_ROUTE")
	}
	return message
}

func newBufconnLNDPayer(t *testing.T, service *testRouterService) *LNDPayer {
	t.Helper()
	set, err := BundledDescriptorSet(FamilyLND)
	if err != nil {
		t.Fatalf("BundledDescriptorSet: %v", err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatalf("protodesc.NewFiles: %v", err)
	}
	send, err := findLNDMethod(files, lndSendPaymentV2)
	if err != nil {
		t.Fatalf("find SendPaymentV2: %v", err)
	}
	track, err := findLNDMethod(files, lndTrackPaymentV2)
	if err != nil {
		t.Fatalf("find TrackPaymentV2: %v", err)
	}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "routerrpc.Router",
		HandlerType: (*dynamicRouterService)(nil),
		Streams: []grpc.StreamDesc{
			routerStreamDesc(send, lndSendPaymentV2),
			routerStreamDesc(track, lndTrackPaymentV2),
		},
	}, service)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	payer, err := NewLNDPayerWithConnection("bufconn-lnd", connection)
	if err != nil {
		t.Fatalf("NewLNDPayerWithConnection: %v", err)
	}
	return payer
}

func routerStreamDesc(method protoreflect.MethodDescriptor, fullMethod string) grpc.StreamDesc {
	return grpc.StreamDesc{
		StreamName:    string(method.Name()),
		ServerStreams: true,
		Handler: func(server any, stream grpc.ServerStream) error {
			request := dynamicpb.NewMessage(method.Input())
			if err := stream.RecvMsg(request); err != nil {
				return err
			}
			return server.(dynamicRouterService).handleRouter(fullMethod, request, stream)
		},
	}
}

func setEnumField(t *testing.T, message *dynamicpb.Message, name, value string) {
	t.Helper()
	descriptor := field(message, protoreflect.Name(name))
	if descriptor == nil || descriptor.Enum() == nil {
		t.Fatalf("enum field %s is missing", name)
	}
	enumValue := descriptor.Enum().Values().ByName(protoreflect.Name(value))
	if enumValue == nil {
		t.Fatalf("enum %s has no value %s", name, value)
	}
	message.Set(descriptor, protoreflect.ValueOfEnum(enumValue.Number()))
}

func TestLNDSendPaymentV2MapsInFlightToSucceeded(t *testing.T) {
	preimage := []byte("0123456789abcdef0123456789abcdef")
	hash := sha256.Sum256(preimage)
	service := &testRouterService{
		t: t, preimage: preimage, hash: hash, sendStatus: "SUCCEEDED",
	}
	payer := newBufconnLNDPayer(t, service)
	result, err := payer.PayInvoice(context.Background(), PaymentRequest{
		Invoice: "lnbc-router-test", PaymentHash: hash, AmountMSat: 2_000,
		MaxFeeMSat: 50, Deadline: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("PayInvoice: %v", err)
	}
	if result.Status != PaymentStatusSucceeded || result.PaymentHash != hash ||
		result.AmountMSat != 2_000 || result.FeeMSat != 7 ||
		string(result.Preimage) != string(preimage) {
		t.Fatalf("result = %#v", result)
	}
}

func TestLNDSendPaymentV2MapsTerminalFailure(t *testing.T) {
	preimage := []byte("0123456789abcdef0123456789abcdef")
	hash := sha256.Sum256(preimage)
	service := &testRouterService{
		t: t, preimage: preimage, hash: hash, sendStatus: "FAILED",
	}
	payer := newBufconnLNDPayer(t, service)
	result, err := payer.PayInvoice(context.Background(), PaymentRequest{
		Invoice: "lnbc-router-test", PaymentHash: hash, AmountMSat: 2_000,
		MaxFeeMSat: 50, Deadline: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("PayInvoice: %v", err)
	}
	if result.Status != PaymentStatusFailed || result.FailureCode != "no_route" {
		t.Fatalf("result = %#v", result)
	}
}

func TestLNDTrackPaymentV2MapsSuccessAndNotFound(t *testing.T) {
	preimage := []byte("0123456789abcdef0123456789abcdef")
	hash := sha256.Sum256(preimage)
	service := &testRouterService{
		t: t, preimage: preimage, hash: hash, sendStatus: "SUCCEEDED", trackStatus: "SUCCEEDED",
	}
	payer := newBufconnLNDPayer(t, service)
	result, err := payer.LookupPayment(context.Background(), PaymentLookup{PaymentHash: hash})
	if err != nil || result.Status != PaymentStatusSucceeded {
		t.Fatalf("successful lookup = %#v, %v", result, err)
	}
	service.trackStatus = "NOT_FOUND"
	result, err = payer.LookupPayment(context.Background(), PaymentLookup{PaymentHash: hash})
	if err != nil || result.Status != PaymentStatusNotFound {
		t.Fatalf("not-found lookup = %#v, %v", result, err)
	}
}

func TestLNDRejectsCryptographicallyInvalidSucceededUpdate(t *testing.T) {
	preimage := []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	hash := sha256.Sum256([]byte("0123456789abcdef0123456789abcdef"))
	service := &testRouterService{
		t: t, preimage: preimage, hash: hash, sendStatus: "SUCCEEDED",
	}
	payer := newBufconnLNDPayer(t, service)
	result, err := payer.PayInvoice(context.Background(), PaymentRequest{
		Invoice: "lnbc-router-test", PaymentHash: hash, AmountMSat: 2_000,
		MaxFeeMSat: 50, Deadline: time.Now().Add(time.Minute),
	})
	if !errors.Is(err, ErrPaymentResultInvalid) || result.Status != PaymentStatusInFlight {
		t.Fatalf("invalid preimage result = %#v, err=%v", result, err)
	}
}
