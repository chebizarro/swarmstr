package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/agent/toolgrpc"
	"metiq/internal/config"
	"metiq/internal/l402"
	"metiq/internal/lightning"
	"metiq/internal/secrets"
)

type runtimeCloser interface {
	Close() error
}

type managedLightningRuntime struct {
	registration agent.ToolRegistration
	closer       runtimeCloser
	runCtx       context.Context
	cancel       context.CancelFunc

	mu        sync.Mutex
	closed    bool
	active    sync.WaitGroup
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

func buildLightningRuntime(ctx context.Context, deps lightningRuntimeDeps) (lightningRuntime, error) {
	cfg := deps.Config
	payerID := strings.TrimSpace(cfg.L402.Payer)
	wallet, ok := findLightningWallet(cfg.Wallets, payerID)
	if !ok {
		return nil, fmt.Errorf("configured L402 payer is unavailable")
	}

	var payer lightning.InvoicePayer
	var err error
	switch strings.ToLower(strings.TrimSpace(wallet.Type)) {
	case config.LightningWalletTypeNWC:
		if deps.ResolveNWCURI == nil || deps.HubFunc == nil {
			return nil, fmt.Errorf("NWC runtime dependencies are unavailable")
		}
		uri, resolveErr := deps.ResolveNWCURI(ctx, payerID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		payer, err = lightning.NewNWCClient(lightning.NWCClientConfig{
			ID: payerID, URI: uri, Keyer: deps.Keyer,
			Timeout:   time.Duration(wallet.EffectiveTimeoutMS()) * time.Millisecond,
			Transport: lightning.NewHubNWCTransport(deps.HubFunc),
		})
	case config.LightningWalletTypeLND:
		profile, found := findLNDProfile(cfg.LND.Profiles, wallet.LNDProfile)
		if !found || deps.Secrets == nil {
			return nil, fmt.Errorf("LND payer profile or credential resolver is unavailable")
		}
		resolver := toolgrpc.ValueResolverFunc(func(resolveCtx context.Context, source toolgrpc.CredentialSource) ([]byte, error) {
			return deps.Secrets.ResolveBytes(resolveCtx, secrets.CredentialSource{
				Ref: source.Ref, Encoding: secrets.CredentialEncoding(source.Encoding),
			})
		})
		payer, err = lightning.NewLNDPayer(profile, resolver)
	default:
		return nil, fmt.Errorf("unsupported L402 payer type")
	}
	if err != nil {
		return nil, fmt.Errorf("initialize L402 payer: %w", err)
	}

	coordinator, err := lightning.NewCoordinator(ctx, lightning.CoordinatorConfig{
		Policy: lightning.CoordinatorPolicy{
			Network: wallet.Network, PayerID: payerID,
			MaxInvoiceMSat: cfg.L402.MaxInvoiceMSat, MaxFeeMSat: cfg.L402.MaxFeeMSat,
			MaxSpendMSatPerHour: cfg.L402.MaxSpendMSatPerHour,
			PaymentTimeout:      time.Duration(cfg.L402.PaymentTimeoutMS) * time.Millisecond,
		},
		Payers:   map[string]lightning.InvoicePayer{payerID: payer},
		Attempts: deps.PaymentAttempts,
	})
	if err != nil {
		_ = payer.Close()
		return nil, fmt.Errorf("initialize Lightning payment coordinator: %w", err)
	}

	cache, err := l402.NewCache(ctx, deps.Tokens, l402.CacheOptions{
		TTL: cfg.L402.Cache.EffectiveTTL(), MaxEntries: cfg.L402.Cache.EffectiveMaxEntries(),
	})
	if err != nil {
		_ = coordinator.Close()
		return nil, fmt.Errorf("initialize L402 token cache: %w", err)
	}
	client, err := l402.NewClient(l402.ClientOptions{
		Browser: toolbuiltin.NewFetchBrowserClient(nil, false),
		Cache:   cache, Coordinator: coordinator, PayerID: payerID,
		AllowedOrigins: cfg.L402.AllowedOrigins,
		Warn: func(warning error) {
			log.Printf("[lightning] HIGH: protected L402 token-cache persistence warning: %v", warning)
		},
	})
	if err != nil {
		_ = coordinator.Close()
		return nil, fmt.Errorf("initialize L402 HTTP client: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runtime := &managedLightningRuntime{
		closer: coordinator, runCtx: runCtx, cancel: cancel, closeDone: make(chan struct{}),
	}
	registration := toolbuiltin.L402FetchRegistration(toolbuiltin.L402FetchOpts{
		Client: client, MaxPaymentTimeout: time.Duration(cfg.L402.PaymentTimeoutMS) * time.Millisecond,
	})
	baseFunc := registration.Func
	registration.Func = runtime.execute(baseFunc)
	runtime.registration = registration
	return runtime, nil
}

func (r *managedLightningRuntime) Registrations() []agent.ToolRegistration {
	return []agent.ToolRegistration{r.registration}
}

func (r *managedLightningRuntime) execute(next agent.ToolFunc) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return "", errors.New("L402 runtime is closed")
		}
		r.active.Add(1)
		r.mu.Unlock()
		defer r.active.Done()

		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(r.runCtx, cancel)
		defer func() {
			stop()
			cancel()
		}()
		return next(callCtx, args)
	}
}

func (r *managedLightningRuntime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		r.cancel()
		go func() {
			r.active.Wait()
			if r.closer != nil {
				r.closeErr = r.closer.Close()
			}
			close(r.closeDone)
		}()
	})
	select {
	case <-r.closeDone:
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func findLightningWallet(wallets map[string]config.LightningWalletConfig, id string) (config.LightningWalletConfig, bool) {
	for candidateID, wallet := range wallets {
		if strings.EqualFold(strings.TrimSpace(candidateID), id) {
			return wallet, true
		}
	}
	return config.LightningWalletConfig{}, false
}

func findLNDProfile(profiles []config.LightningGRPCProfile, id string) (config.LightningGRPCProfile, bool) {
	for _, profile := range profiles {
		if strings.EqualFold(strings.TrimSpace(profile.ID), strings.TrimSpace(id)) {
			return profile, true
		}
	}
	return config.LightningGRPCProfile{}, false
}
