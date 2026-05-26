//go:build experimental_fips

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const fipsErrorTestPubkey = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
const fipsErrorTestPeer = "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"

func newFIPSErrorTestTransport() *FIPSTransport {
	return &FIPSTransport{
		pubkeyHex: fipsErrorTestPubkey,
		agentPort: FIPSDefaultAgentPort,
		dialTTL:   time.Nanosecond,
		conns:     make(map[string]*fipsConn),
		idCache:   make(map[string]string),
		ctx:       context.Background(),
	}
}

func TestFIPSTransport_SendDMClassifiesInvalidPubkeyPermanent(t *testing.T) {
	ft := newFIPSErrorTestTransport()
	defer ft.Close()

	err := ft.SendDM(context.Background(), "not-a-pubkey", "hello")
	assertFIPSErrorKind(t, err, FIPSErrorKindPermanent, false)
}

func TestFIPSTransport_SendDMClassifiesPayloadTooLargePermanent(t *testing.T) {
	ft := newFIPSErrorTestTransport()
	defer ft.Close()

	err := ft.SendDM(context.Background(), fipsErrorTestPeer, strings.Repeat("x", fipsMaxPayloadBytes))
	assertFIPSErrorKind(t, err, FIPSErrorKindPermanent, false)
}

func TestFIPSTransport_SendDMPropagatesContextCancellation(t *testing.T) {
	ft := newFIPSErrorTestTransport()
	defer ft.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ft.SendDM(ctx, fipsErrorTestPeer, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	var fipsErr *FIPSError
	if errors.As(err, &fipsErr) {
		t.Fatalf("context cancellation should propagate directly, got FIPSError: %v", err)
	}
}

func TestFIPSTransport_SendDMPropagatesContextDeadline(t *testing.T) {
	ft := newFIPSErrorTestTransport()
	defer ft.Close()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := ft.SendDM(ctx, fipsErrorTestPeer, "hello")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	var fipsErr *FIPSError
	if errors.As(err, &fipsErr) {
		t.Fatalf("context deadline should propagate directly, got FIPSError: %v", err)
	}
}

func TestFIPSTransport_SendDMClassifiesDialFailureTransport(t *testing.T) {
	ft := newFIPSErrorTestTransport()
	defer ft.Close()

	err := ft.SendDM(context.Background(), fipsErrorTestPeer, "hello")
	assertFIPSErrorKind(t, err, FIPSErrorKindTransport, true)
}

func assertFIPSErrorKind(t *testing.T, err error, want FIPSErrorKind, wantFallback bool) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	var fipsErr *FIPSError
	if !errors.As(err, &fipsErr) {
		t.Fatalf("expected FIPSError, got %T: %v", err, err)
	}
	if fipsErr.Kind != want {
		t.Fatalf("expected kind %q, got %q: %v", want, fipsErr.Kind, err)
	}
	if fipsErr.AllowFallback() != wantFallback {
		t.Fatalf("expected AllowFallback=%v, got %v", wantFallback, fipsErr.AllowFallback())
	}
}
