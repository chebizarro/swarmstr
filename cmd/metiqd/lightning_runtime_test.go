package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/config"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/paymentstate"
)

type runtimeTestCloser struct {
	calls atomic.Int32
}

func (c *runtimeTestCloser) Close() error {
	c.calls.Add(1)
	return nil
}

func TestBuildLightningRuntimeRegistersDedicatedL402Tool(t *testing.T) {
	const pubkey = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	uri := "nostr+walletconnect://" + pubkey + "?relay=wss%3A%2F%2Frelay.example&secret=" + strings.Repeat("0", 63) + "1"
	cfg := config.LightningConfig{
		Wallets: map[string]config.LightningWalletConfig{"default": {
			Type: config.LightningWalletTypeNWC, Network: config.LightningNetworkRegtest,
			TimeoutMS: 1000, TrustWalletFeeLimit: true,
		}},
		L402: config.L402Config{
			Enabled: true, Payer: "default", AllowedOrigins: []string{"https://paid.example"},
			MaxInvoiceMSat: 1000, MaxFeeMSat: 10, MaxSpendMSatPerHour: 2000,
			PaymentTimeoutMS: 1000, Cache: config.L402CacheConfig{Persistence: config.L402CachePersistenceMemory},
		},
	}
	runtime, err := buildLightningRuntime(context.Background(), lightningRuntimeDeps{
		Config: cfg, Tokens: paymentstate.NewMemoryL402TokenRepository(),
		PaymentAttempts: paymentstate.NewMemoryPaymentAttemptRepository(),
		ResolveNWCURI:   func(context.Context, string) (string, error) { return uri, nil },
		HubFunc:         func() *nostruntime.NostrHub { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	registrations := runtime.Registrations()
	if len(registrations) != 1 || registrations[0].Descriptor.Name != "l402_fetch" || !registrations[0].Descriptor.Traits.Destructive {
		t.Fatalf("registrations = %#v", registrations)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagedLightningRuntimeCancelsAndDrainsActiveCalls(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	closer := &runtimeTestCloser{}
	runCtx, cancel := context.WithCancel(context.Background())
	runtime := &managedLightningRuntime{
		closer: closer, runCtx: runCtx, cancel: cancel, closeDone: make(chan struct{}),
	}
	base := func(ctx context.Context, _ map[string]any) (string, error) {
		close(started)
		<-ctx.Done()
		close(released)
		return "", ctx.Err()
	}
	runtime.registration = agent.ToolRegistration{
		Descriptor: agent.ToolDescriptor{Name: "l402_fetch"},
		Func:       runtime.execute(base),
	}

	callDone := make(chan error, 1)
	go func() {
		_, err := runtime.registration.Func(context.Background(), nil)
		callDone <- err
	}()
	<-started
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-released
	if err := <-callDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("active call error = %v", err)
	}
	if closer.calls.Load() != 1 {
		t.Fatalf("closer calls = %d", closer.calls.Load())
	}
	if _, err := runtime.registration.Func(context.Background(), nil); err == nil {
		t.Fatal("closed runtime accepted new work")
	}
	if err := runtime.Close(context.Background()); err != nil || closer.calls.Load() != 1 {
		t.Fatalf("second close = %v, calls=%d", err, closer.calls.Load())
	}
}

func TestManagedLightningRuntimeCloseHonorsCallerDeadline(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	runCtx, cancel := context.WithCancel(context.Background())
	runtime := &managedLightningRuntime{runCtx: runCtx, cancel: cancel, closeDone: make(chan struct{})}
	runtime.registration = agent.ToolRegistration{
		Descriptor: agent.ToolDescriptor{Name: "l402_fetch"},
		Func: runtime.execute(func(context.Context, map[string]any) (string, error) {
			close(started)
			<-block
			return "", nil
		}),
	}
	go runtime.registration.Func(context.Background(), nil)
	<-started
	ctx, stop := context.WithTimeout(context.Background(), time.Millisecond)
	defer stop()
	if err := runtime.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error = %v", err)
	}
	close(block)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
