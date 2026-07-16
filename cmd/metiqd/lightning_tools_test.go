package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/config"
	"metiq/internal/secrets"
	"metiq/internal/store/state"
)

type controllerProtectedBackend struct {
	mu    sync.Mutex
	items map[string]string
	fail  bool
}

func (b *controllerProtectedBackend) Name() string          { return "test-protected" }
func (b *controllerProtectedBackend) ProtectedAtRest() bool { return true }
func (b *controllerProtectedBackend) Get(key string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return "", false, errors.New("backend down")
	}
	value, ok := b.items[key]
	return value, ok, nil
}
func (b *controllerProtectedBackend) Set(key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return errors.New("backend down")
	}
	if b.items == nil {
		b.items = map[string]string{}
	}
	b.items[key] = value
	return nil
}
func (b *controllerProtectedBackend) Delete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.items, key)
	return nil
}

type fakeLightningRuntime struct {
	registration agent.ToolRegistration
	closeStarted chan struct{}
	closeRelease chan struct{}
	once         sync.Once
}

func (r *fakeLightningRuntime) Registrations() []agent.ToolRegistration {
	return []agent.ToolRegistration{r.registration}
}
func (r *fakeLightningRuntime) Close(ctx context.Context) error {
	r.once.Do(func() {
		if r.closeStarted != nil {
			close(r.closeStarted)
		}
	})
	if r.closeRelease != nil {
		select {
		case <-r.closeRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func controllerDoc(memory bool) state.ConfigDoc {
	persistence := "secret_store"
	if memory {
		persistence = "memory"
	}
	return state.ConfigDoc{Extra: map[string]any{"lightning": map[string]any{
		"wallets": map[string]any{"default": map[string]any{"type": "nwc", "network": "mainnet", "uri": "secret:TEST_NWC_URI", "trust_wallet_fee_limit": true}},
		"l402":    map[string]any{"enabled": true, "payer": "default", "allowed_origins": []any{"https://api.example.com"}, "max_invoice_msat": float64(1000), "max_fee_msat": float64(10), "max_spend_msat_per_hour": float64(2000), "payment_timeout_ms": float64(30000), "cache": map[string]any{"persistence": persistence}},
	}}}
}

func runtimeRegistration(result string) agent.ToolRegistration {
	return agent.ToolRegistration{Descriptor: agent.ToolDescriptor{Name: "l402_fetch", Description: result}, Func: func(context.Context, map[string]any) (string, error) { return result, nil }, ProviderVisible: true}
}

func TestLightningControllerAbsentConfigIsNoOp(t *testing.T) {
	store := secrets.NewStore(nil)
	factoryCalled := false
	controller := newLightningController(store, func(context.Context, lightningRuntimeDeps) (lightningRuntime, error) {
		factoryCalled = true
		return nil, errors.New("factory must not run")
	})
	registry := agent.NewToolRegistry()
	if err := controller.reconcile(context.Background(), registry, state.ConfigDoc{}, "startup"); err != nil {
		t.Fatalf("absent extra.lightning must be a no-op: %v", err)
	}
	if factoryCalled {
		t.Fatal("Lightning runtime factory ran without extra.lightning")
	}
	if _, ok := registry.Descriptor("l402_fetch"); ok {
		t.Fatal("l402_fetch registered without extra.lightning")
	}
}

func TestLightningControllerNilFactoryFailsClosed(t *testing.T) {
	t.Setenv("TEST_NWC_URI", "nostr+walletconnect://wallet")
	store := secrets.NewStore(nil)
	store.SetBackend(&controllerProtectedBackend{items: map[string]string{}})
	controller := newLightningController(store, nil)
	registry := agent.NewToolRegistry()
	if err := controller.reconcile(context.Background(), registry, controllerDoc(false), "test"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected unavailable runtime, got %v", err)
	}
	if _, ok := registry.Descriptor("l402_fetch"); ok {
		t.Fatal("tool registered without runtime")
	}
}

func TestLightningControllerInvalidReloadKeepsPreviousRuntime(t *testing.T) {
	t.Setenv("TEST_NWC_URI", "nostr+walletconnect://wallet")
	store := secrets.NewStore(nil)
	store.SetBackend(&controllerProtectedBackend{items: map[string]string{}})
	factory := func(context.Context, lightningRuntimeDeps) (lightningRuntime, error) {
		return &fakeLightningRuntime{registration: runtimeRegistration("working")}, nil
	}
	controller := newLightningController(store, factory)
	registry := agent.NewToolRegistry()
	ctx := context.Background()
	if err := controller.reconcile(ctx, registry, controllerDoc(false), "initial"); err != nil {
		t.Fatal(err)
	}
	invalid := controllerDoc(false)
	invalid.Extra["lightning"].(map[string]any)["l402"].(map[string]any)["allowed_origins"] = []any{"http://unsafe.example.com"}
	if err := controller.reconcile(ctx, registry, invalid, "reload"); err == nil {
		t.Fatal("expected invalid reload")
	}
	got, err := registry.Execute(ctx, agent.ToolCall{Name: "l402_fetch"})
	if err != nil || got != "working" {
		t.Fatalf("previous runtime not retained: %q, %v", got, err)
	}
}

func TestLightningControllerSwapInstallsNewBeforeDrainingOld(t *testing.T) {
	t.Setenv("TEST_NWC_URI", "nostr+walletconnect://wallet")
	store := secrets.NewStore(nil)
	store.SetBackend(&controllerProtectedBackend{items: map[string]string{}})
	old := &fakeLightningRuntime{registration: runtimeRegistration("old"), closeStarted: make(chan struct{}), closeRelease: make(chan struct{})}
	next := &fakeLightningRuntime{registration: runtimeRegistration("new")}
	count := 0
	factory := func(context.Context, lightningRuntimeDeps) (lightningRuntime, error) {
		count++
		if count == 1 {
			return old, nil
		}
		return next, nil
	}
	controller := newLightningController(store, factory)
	registry := agent.NewToolRegistry()
	ctx := context.Background()
	if err := controller.reconcile(ctx, registry, controllerDoc(false), "initial"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- controller.reconcile(ctx, registry, controllerDoc(false), "reload") }()
	<-old.closeStarted
	got, err := registry.Execute(ctx, agent.ToolCall{Name: "l402_fetch"})
	if err != nil || got != "new" {
		t.Fatalf("new runtime not active during drain: %q, %v", got, err)
	}
	close(old.closeRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLightningControllerProtectedBackendFailureAndDoubleClose(t *testing.T) {
	t.Setenv("TEST_NWC_URI", "nostr+walletconnect://wallet")
	backend := &controllerProtectedBackend{items: map[string]string{}, fail: true}
	store := secrets.NewStore(nil)
	store.SetBackend(backend)
	controller := newLightningController(store, func(context.Context, lightningRuntimeDeps) (lightningRuntime, error) {
		t.Fatal("factory called with failed backend")
		return nil, nil
	})
	if err := controller.reconcile(context.Background(), agent.NewToolRegistry(), controllerDoc(false), "test"); err == nil {
		t.Fatal("expected backend failure")
	}
	if err := controller.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := controller.close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLightningControllerMemoryModeSuppliesRepositories(t *testing.T) {
	t.Setenv("TEST_NWC_URI", "nostr+walletconnect://wallet")
	store := secrets.NewStore(nil)
	store.SetBackend(nil)
	called := false
	factory := func(_ context.Context, deps lightningRuntimeDeps) (lightningRuntime, error) {
		called = true
		if deps.Config.L402.Cache.EffectivePersistence() != config.L402CachePersistenceMemory {
			t.Fatal("wrong persistence")
		}
		if _, err := deps.Tokens.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		return &fakeLightningRuntime{registration: runtimeRegistration("memory")}, nil
	}
	controller := newLightningController(store, factory)
	if err := controller.reconcile(context.Background(), agent.NewToolRegistry(), controllerDoc(true), "test"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("factory not called")
	}
}

func TestLightningControllerLegacyNWCSourceIsResolvedWithoutConfigRewrite(t *testing.T) {
	doc := controllerDoc(true)
	wallet := doc.Extra["lightning"].(map[string]any)["wallets"].(map[string]any)["default"].(map[string]any)
	delete(wallet, "uri")
	doc.Extra["nwc"] = map[string]any{"connection_string": "nostr+walletconnect://legacy"}
	store := secrets.NewStore(nil)
	var gotURI string
	factory := func(ctx context.Context, deps lightningRuntimeDeps) (lightningRuntime, error) {
		var err error
		gotURI, err = deps.ResolveNWCURI(ctx, "default")
		if err != nil {
			return nil, err
		}
		return &fakeLightningRuntime{registration: runtimeRegistration("legacy")}, nil
	}
	controller := newLightningController(store, factory)
	if err := controller.reconcile(context.Background(), agent.NewToolRegistry(), doc, "test"); err != nil {
		t.Fatal(err)
	}
	if gotURI != "nostr+walletconnect://legacy" {
		t.Fatalf("resolved URI = %q", gotURI)
	}
	if _, exists := wallet["uri"]; exists {
		t.Fatal("legacy resolution rewrote config")
	}
	if !controller.legacyNWCWarned {
		t.Fatal("legacy source warning was not recorded")
	}
}
