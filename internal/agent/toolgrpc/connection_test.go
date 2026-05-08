package toolgrpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const testFullMethod = "/toolgrpc.test.TestService/Unary"

type testServiceServer interface {
	Unary(context.Context, *emptypb.Empty) (proto.Message, error)
}

type testServer struct {
	onUnary  func(context.Context) error
	response proto.Message
}

func (s *testServer) Unary(ctx context.Context, _ *emptypb.Empty) (proto.Message, error) {
	if s.onUnary != nil {
		if err := s.onUnary(ctx); err != nil {
			return nil, err
		}
	}
	if s.response != nil {
		return s.response, nil
	}
	return &emptypb.Empty{}, nil
}

var testServiceDesc = grpc.ServiceDesc{
	ServiceName: "toolgrpc.test.TestService",
	HandlerType: (*testServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Unary",
		Handler:    testUnaryHandler,
	}},
}

func testUnaryHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(testServiceServer).Unary(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: testFullMethod}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(testServiceServer).Unary(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func startTestServer(t *testing.T, srv *testServer, opts ...grpc.ServerOption) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(opts...)
	server.RegisterService(&testServiceDesc, srv)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String()
}

func TestConnectionManagerPoolsOneConnPerProfile(t *testing.T) {
	target := startTestServer(t, &testServer{})
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:     "local",
		Target: target,
		Transport: config.GRPCTransportConfig{
			TLSMode: config.GRPCTransportTLSModeInsecure,
		},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	ctx := context.Background()
	first, err := manager.Conn(ctx, "local")
	if err != nil {
		t.Fatalf("first Conn: %v", err)
	}
	second, err := manager.Conn(ctx, "local")
	if err != nil {
		t.Fatalf("second Conn: %v", err)
	}
	if first != second {
		t.Fatalf("Conn returned different pointers for same profile")
	}
}

func TestConnectionManagerAppliesAuthMetadataAndDefaultDeadline(t *testing.T) {
	target := startTestServer(t, &testServer{onUnary: func(ctx context.Context) error {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			t.Fatalf("missing incoming metadata")
		}
		if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer test-token" {
			t.Fatalf("authorization metadata = %v", got)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatalf("server context missing deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > 600*time.Millisecond {
			t.Fatalf("deadline remaining = %s, want <= 600ms and > 0", remaining)
		}
		return nil
	}})
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:     "auth",
		Target: target,
		Transport: config.GRPCTransportConfig{
			TLSMode: config.GRPCTransportTLSModeInsecure,
		},
		Auth: config.GRPCAuthConfig{Metadata: map[string]string{
			"authorization": "Bearer test-token",
		}},
		Defaults: config.GRPCDefaultsConfig{DeadlineMS: 500, MaxDeadlineMS: 1000},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	var out emptypb.Empty
	if err := manager.InvokeUnary(context.Background(), "auth", testFullMethod, &emptypb.Empty{}, &out, CallOptions{}); err != nil {
		t.Fatalf("InvokeUnary: %v", err)
	}
}

func TestConnectionManagerMetadataOverrides(t *testing.T) {
	target := startTestServer(t, &testServer{onUnary: func(ctx context.Context) error {
		md, _ := metadata.FromIncomingContext(ctx)
		if got := md.Get("x-request-id"); len(got) != 1 || got[0] != "req-123" {
			t.Fatalf("x-request-id metadata = %v", got)
		}
		return nil
	}})
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:     "override",
		Target: target,
		Transport: config.GRPCTransportConfig{
			TLSMode: grpcTransportTLSModeInsecurePlaintext,
		},
		Auth: config.GRPCAuthConfig{AllowOverrideKeys: []string{"x-request-id"}},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	var out emptypb.Empty
	if err := manager.InvokeUnary(context.Background(), "override", testFullMethod, &emptypb.Empty{}, &out, CallOptions{Metadata: map[string]string{"x-request-id": "req-123"}}); err != nil {
		t.Fatalf("InvokeUnary with allowed metadata: %v", err)
	}
	if _, cancel, err := manager.CallContext(context.Background(), "override", CallOptions{Metadata: map[string]string{"x-not-allowed": "nope"}}); err == nil {
		cancel()
		t.Fatalf("CallContext accepted disallowed metadata")
	}
}

func TestConnectionManagerResolvesSecretAuthMetadata(t *testing.T) {
	t.Setenv("GRPC_TEST_BEARER", "Bearer secret-token")
	target := startTestServer(t, &testServer{onUnary: func(ctx context.Context) error {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			t.Fatalf("missing incoming metadata")
		}
		if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer secret-token" {
			t.Fatalf("authorization metadata = %v", got)
		}
		return nil
	}})
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:     "secret-auth",
		Target: target,
		Transport: config.GRPCTransportConfig{
			TLSMode: config.GRPCTransportTLSModeInsecure,
		},
		Auth: config.GRPCAuthConfig{Metadata: map[string]string{
			"authorization": "secret:GRPC_TEST_BEARER",
		}},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	var out emptypb.Empty
	if err := manager.InvokeUnary(context.Background(), "secret-auth", testFullMethod, &emptypb.Empty{}, &out, CallOptions{}); err != nil {
		t.Fatalf("InvokeUnary: %v", err)
	}
}

func TestConnectionManagerRejectsMissingSecretAuthMetadata(t *testing.T) {
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:     "secret-missing",
		Target: "127.0.0.1:1",
		Auth: config.GRPCAuthConfig{Metadata: map[string]string{
			"authorization": "secret:GRPC_TEST_MISSING_TOKEN",
		}},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	if _, cancel, err := manager.CallContext(context.Background(), "secret-missing", CallOptions{}); err == nil {
		cancel()
		t.Fatalf("CallContext accepted missing secret metadata")
	}
}

func TestConnectionManagerRejectsDeadlineAboveMax(t *testing.T) {
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:     "deadline",
		Target: "127.0.0.1:1",
		Defaults: config.GRPCDefaultsConfig{
			DeadlineMS:    25,
			MaxDeadlineMS: 50,
		},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	if _, cancel, err := manager.CallContext(context.Background(), "deadline", CallOptions{DeadlineMS: 51}); err == nil {
		cancel()
		t.Fatalf("CallContext accepted deadline above max")
	}
}

func TestConnectionManagerAppliesMaxReceiveMessageBytes(t *testing.T) {
	target := startTestServer(t, &testServer{response: wrapperspb.String(strings.Repeat("x", 2048))})
	manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
		ID:     "small-msg",
		Target: target,
		Transport: config.GRPCTransportConfig{
			TLSMode: config.GRPCTransportTLSModeInsecure,
		},
		Defaults: config.GRPCDefaultsConfig{MaxRecvMessageBytes: 64},
	}})
	if err != nil {
		t.Fatalf("NewConnectionManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	var out wrapperspb.StringValue
	err = manager.InvokeUnary(context.Background(), "small-msg", testFullMethod, &emptypb.Empty{}, &out, CallOptions{})
	if err == nil {
		t.Fatalf("InvokeUnary succeeded despite response exceeding max_recv_message_bytes")
	}
	if !strings.Contains(err.Error(), "larger than max") && !strings.Contains(err.Error(), "received message larger") {
		t.Fatalf("InvokeUnary error = %v, want max message size failure", err)
	}
}

func TestBuildTransportCredentialsSupportsSystemTLS(t *testing.T) {
	creds, err := buildTransportCredentials(config.GRPCTransportConfig{TLSMode: config.GRPCTransportTLSModeSystem, ServerName: "example.com"})
	if err != nil {
		t.Fatalf("buildTransportCredentials(system): %v", err)
	}
	if creds.Info().SecurityProtocol == "" {
		t.Fatalf("system TLS credentials missing security protocol info")
	}
}

func TestConnectionManagerCustomCAAndMTLS(t *testing.T) {
	caCert, caKey := newCertificateAuthority(t)
	serverCert, serverKey := newSignedCertificate(t, caCert, caKey, certificateTemplate{
		CommonName: "localhost",
		DNSNames:   []string{"localhost"},
		KeyUsage:   x509.KeyUsageDigitalSignature,
		ExtUsage:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	clientCert, clientKey := newSignedCertificate(t, caCert, caKey, certificateTemplate{
		CommonName: "client",
		KeyUsage:   x509.KeyUsageDigitalSignature,
		ExtUsage:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	caFile := writePEMFile(t, "ca.pem", "CERTIFICATE", caCert.Raw)
	clientCertFile := writePEMFile(t, "client.pem", "CERTIFICATE", clientCert.Raw)
	clientKeyFile := writePrivateKeyFile(t, "client-key.pem", clientKey)

	t.Run("custom_ca", func(t *testing.T) {
		serverTLS := tlsConfigForServer(t, serverCert, serverKey, nil)
		target := startTestServer(t, &testServer{}, grpc.Creds(credentials.NewTLS(serverTLS)))
		manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
			ID:     "custom-ca",
			Target: target,
			Transport: config.GRPCTransportConfig{
				TLSMode:    config.GRPCTransportTLSModeCustomCA,
				CAFile:     caFile,
				ServerName: "localhost",
			},
		}})
		if err != nil {
			t.Fatalf("NewConnectionManager: %v", err)
		}
		t.Cleanup(func() { _ = manager.Close() })
		var out emptypb.Empty
		if err := manager.InvokeUnary(context.Background(), "custom-ca", testFullMethod, &emptypb.Empty{}, &out, CallOptions{}); err != nil {
			t.Fatalf("InvokeUnary custom_ca: %v", err)
		}
	})

	t.Run("mtls", func(t *testing.T) {
		caPool := x509.NewCertPool()
		caPool.AddCert(caCert)
		serverTLS := tlsConfigForServer(t, serverCert, serverKey, caPool)
		target := startTestServer(t, &testServer{}, grpc.Creds(credentials.NewTLS(serverTLS)))
		manager, err := NewConnectionManager([]config.GRPCEndpointConfig{{
			ID:     "mtls",
			Target: target,
			Transport: config.GRPCTransportConfig{
				TLSMode:    config.GRPCTransportTLSModeMTLS,
				CAFile:     caFile,
				CertFile:   clientCertFile,
				KeyFile:    clientKeyFile,
				ServerName: "localhost",
			},
		}})
		if err != nil {
			t.Fatalf("NewConnectionManager: %v", err)
		}
		t.Cleanup(func() { _ = manager.Close() })
		var out emptypb.Empty
		if err := manager.InvokeUnary(context.Background(), "mtls", testFullMethod, &emptypb.Empty{}, &out, CallOptions{}); err != nil {
			t.Fatalf("InvokeUnary mtls: %v", err)
		}
	})
}

type certificateTemplate struct {
	CommonName string
	DNSNames   []string
	KeyUsage   x509.KeyUsage
	ExtUsage   []x509.ExtKeyUsage
}

func newCertificateAuthority(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key := newRSAKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "toolgrpc-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return cert, key
}

func newSignedCertificate(t *testing.T, caCert *x509.Certificate, caKey *rsa.PrivateKey, spec certificateTemplate) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key := newRSAKey(t)
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: spec.CommonName},
		DNSNames:     spec.DNSNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     spec.KeyUsage,
		ExtKeyUsage:  spec.ExtUsage,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create signed certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse signed certificate: %v", err)
	}
	return cert, key
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func tlsConfigForServer(t *testing.T, cert *x509.Certificate, key *rsa.PrivateKey, clientCAs *x509.CertPool) *tls.Config {
	t.Helper()
	serverCert := tls.Certificate{
		Certificate: [][]byte{cert.Raw},
		PrivateKey:  key,
		Leaf:        cert,
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{serverCert}}
	if clientCAs != nil {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = clientCAs
	}
	return cfg
}

func writePEMFile(t *testing.T, name, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if err := pem.Encode(file, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", name, err)
	}
	return path
}

func writePrivateKeyFile(t *testing.T, name string, key *rsa.PrivateKey) string {
	t.Helper()
	return writePEMFile(t, name, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
}
