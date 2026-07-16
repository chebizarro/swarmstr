package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/config"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/paymentstate"
	"metiq/internal/secrets"
	"metiq/internal/store/state"
)

type lightningRuntimeDeps struct {
	Config          config.LightningConfig
	Secrets         *secrets.Store
	Tokens          paymentstate.L402TokenRepository
	PaymentAttempts paymentstate.PaymentAttemptRepository
	ResolveNWCURI   func(context.Context, string) (string, error)
	HubFunc         func() *nostruntime.NostrHub
	Keyer           nostr.Keyer
}

type lightningRuntime interface {
	Registrations() []agent.ToolRegistration
	// Close must stop new work, cancel pending network operations, drain active
	// calls, flush caches, and close wallet connections.
	Close(context.Context) error
}

type lightningRuntimeFactory func(context.Context, lightningRuntimeDeps) (lightningRuntime, error)

// INTEGRATION(wi1/wi2): the L402 client and payment coordinator install their
// concrete composition factory here. A nil factory intentionally fails closed.
var newLightningRuntime lightningRuntimeFactory = buildLightningRuntime

type lightningController struct {
	reconcileMu     sync.Mutex
	mu              sync.Mutex
	secrets         *secrets.Store
	factory         lightningRuntimeFactory
	config          config.LightningConfig
	runtime         lightningRuntime
	owned           map[string]agent.ToolDescriptor
	legacyNWCWarned bool
	closed          bool
	baseDeps        lightningRuntimeDeps
}

func newLightningController(store *secrets.Store, factory lightningRuntimeFactory, base ...lightningRuntimeDeps) *lightningController {
	controller := &lightningController{secrets: store, factory: factory, owned: map[string]agent.ToolDescriptor{}}
	if len(base) > 0 {
		controller.baseDeps = base[0]
	}
	return controller
}

func (c *lightningController) reconcile(ctx context.Context, registry *agent.ToolRegistry, doc state.ConfigDoc, logContext string) error {
	if c == nil {
		return nil
	}
	if c.secrets == nil {
		return fmt.Errorf("lightning secrets store is unavailable")
	}
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	if _, warnings := c.secrets.Reload(); len(warnings) > 0 {
		for _, warning := range warnings {
			log.Printf("[lightning] %s secret reload warning: %s", logContext, warning)
		}
	}
	candidate, err := config.ParseLightningConfigExtra(doc.Extra)
	if err != nil {
		return c.reject(logContext, err)
	}
	if validation := config.ValidateLightningConfigExtra(candidate, doc.Extra); len(validation) > 0 {
		return c.reject(logContext, errors.Join(validation...))
	}
	if !candidate.L402.Enabled {
		return c.disable(ctx, registry, candidate, logContext)
	}
	if err := c.preflightCredentials(ctx, doc.Extra, candidate); err != nil {
		return c.reject(logContext, err)
	}

	tokens, attempts, err := c.openRepositories(ctx, candidate)
	if err != nil {
		return c.reject(logContext, err)
	}
	if _, err := tokens.Load(ctx); err != nil {
		return c.reject(logContext, fmt.Errorf("load L402 token cache: %w", err))
	}
	if _, err := attempts.Load(ctx); err != nil {
		return c.reject(logContext, fmt.Errorf("load pending payments: %w", err))
	}
	if c.factory == nil {
		return c.reject(logContext, fmt.Errorf("L402 runtime implementation is unavailable"))
	}
	deps := c.baseDeps
	deps.Config, deps.Secrets, deps.Tokens, deps.PaymentAttempts = candidate, c.secrets, tokens, attempts
	deps.ResolveNWCURI = func(resolveCtx context.Context, walletID string) (string, error) {
		return c.resolveNWCURI(resolveCtx, doc.Extra, candidate, walletID)
	}
	next, err := c.factory(ctx, deps)
	if err != nil {
		return c.reject(logContext, fmt.Errorf("build runtime: %w", err))
	}
	if next == nil {
		return c.reject(logContext, fmt.Errorf("build runtime returned nil"))
	}
	registrations, descriptors, err := validateLightningRegistrations(next.Registrations())
	if err != nil {
		_ = next.Close(ctx)
		return c.reject(logContext, err)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = next.Close(ctx)
		return fmt.Errorf("lightning controller is closed")
	}
	for name := range descriptors {
		if existing, ok := registry.Descriptor(name); ok {
			previous, owned := c.owned[name]
			if !owned || !reflect.DeepEqual(existing, previous) {
				c.mu.Unlock()
				_ = next.Close(ctx)
				return c.reject(logContext, fmt.Errorf("tool %s is owned by another runtime", name))
			}
		}
	}
	previousRuntime := c.runtime
	previousOwned := c.owned
	installed := make(map[string]agent.ToolDescriptor, len(registrations))
	for name, registration := range registrations {
		registry.RegisterTool(name, registration)
		if descriptor, ok := registry.Descriptor(name); ok {
			installed[name] = descriptor
		}
	}
	for name, previous := range previousOwned {
		if _, retained := descriptors[name]; retained {
			continue
		}
		if current, ok := registry.Descriptor(name); ok && reflect.DeepEqual(current, previous) {
			registry.Remove(name)
		}
	}
	c.config, c.runtime, c.owned = candidate, next, installed
	c.mu.Unlock()
	if previousRuntime != nil {
		if err := previousRuntime.Close(ctx); err != nil {
			log.Printf("[lightning] %s close previous runtime warning: %v", logContext, err)
		}
	}
	log.Printf("[lightning] %s runtime applied tools=%d", logContext, len(descriptors))
	return nil
}

func (c *lightningController) openRepositories(ctx context.Context, cfg config.LightningConfig) (paymentstate.L402TokenRepository, paymentstate.PaymentAttemptRepository, error) {
	if cfg.L402.Cache.EffectivePersistence() == config.L402CachePersistenceMemory {
		log.Printf("[lightning] L402 cache uses memory persistence; tokens and pending-payment protection do not survive restart")
		return paymentstate.NewMemoryL402TokenRepository(), paymentstate.NewMemoryPaymentAttemptRepository(), nil
	}
	tokenNS, err := c.secrets.OpenProtectedJSONNamespace(paymentstate.L402TokenNamespace)
	if err != nil {
		return nil, nil, fmt.Errorf("open protected L402 token cache: %w", err)
	}
	attemptNS, err := c.secrets.OpenProtectedJSONNamespace(paymentstate.PaymentAttemptNamespace)
	if err != nil {
		return nil, nil, fmt.Errorf("open protected pending-payment store: %w", err)
	}
	return paymentstate.NewSecretL402TokenRepository(tokenNS), paymentstate.NewSecretPaymentAttemptRepository(attemptNS), ctx.Err()
}

func (c *lightningController) preflightCredentials(ctx context.Context, extra map[string]any, cfg config.LightningConfig) error {
	for _, profiles := range [][]config.LightningGRPCProfile{cfg.LND.Profiles, cfg.Tapd.Profiles} {
		for _, profile := range profiles {
			_, err := c.secrets.ResolveBytes(ctx, secrets.CredentialSource{Ref: profile.Macaroon.Ref, Encoding: secrets.CredentialEncoding(profile.Macaroon.EffectiveEncoding())})
			if err != nil {
				return fmt.Errorf("credential for profile %s is unavailable: %w", profile.ID, err)
			}
		}
	}
	for id, wallet := range cfg.Wallets {
		if !strings.EqualFold(wallet.Type, config.LightningWalletTypeNWC) {
			continue
		}
		_, source, sourceFound := config.ResolveNWCURI(extra, wallet)
		if sourceFound && source != "extra.lightning.wallets.uri" {
			c.mu.Lock()
			warn := !c.legacyNWCWarned
			c.legacyNWCWarned = true
			c.mu.Unlock()
			if warn {
				log.Printf("[lightning] NWC wallet %s uses deprecated credential source %s", id, source)
			}
		}
		if _, err := c.resolveNWCURI(ctx, extra, cfg, id); err != nil {
			return err
		}
	}
	return nil
}

func (c *lightningController) resolveNWCURI(ctx context.Context, extra map[string]any, cfg config.LightningConfig, walletID string) (string, error) {
	var wallet config.LightningWalletConfig
	foundWallet := false
	for id, candidate := range cfg.Wallets {
		if strings.EqualFold(strings.TrimSpace(id), strings.TrimSpace(walletID)) {
			wallet, foundWallet = candidate, true
			break
		}
	}
	if !foundWallet || !strings.EqualFold(strings.TrimSpace(wallet.Type), config.LightningWalletTypeNWC) {
		return "", fmt.Errorf("NWC wallet %s is unavailable", walletID)
	}
	uri, source, found := config.ResolveNWCURI(extra, wallet)
	if !found {
		return "", fmt.Errorf("NWC credential for wallet %s is unavailable", walletID)
	}
	if source == "NWC_CONNECTION_STRING" || !isExplicitCredentialReference(uri) {
		return uri, nil
	}
	resolved, err := c.secrets.ResolveBytes(ctx, secrets.CredentialSource{Ref: uri, Encoding: secrets.CredentialEncodingText})
	if err != nil {
		return "", fmt.Errorf("NWC credential for wallet %s is unavailable: %w", walletID, err)
	}
	return string(resolved), nil
}

func isExplicitCredentialReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "secret:") || strings.HasPrefix(value, "env:") || strings.HasPrefix(value, "$") || strings.HasPrefix(value, "file:")
}

func (c *lightningController) disable(ctx context.Context, registry *agent.ToolRegistry, cfg config.LightningConfig, logContext string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	previousRuntime, previousOwned := c.runtime, c.owned
	for name, previous := range previousOwned {
		if current, ok := registry.Descriptor(name); ok && reflect.DeepEqual(current, previous) {
			registry.Remove(name)
		}
	}
	c.config, c.runtime, c.owned = cfg, nil, map[string]agent.ToolDescriptor{}
	c.mu.Unlock()
	if previousRuntime != nil {
		if err := previousRuntime.Close(ctx); err != nil {
			log.Printf("[lightning] %s close disabled runtime warning: %v", logContext, err)
		}
	}
	return nil
}

func (c *lightningController) close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	runtime := c.runtime
	c.runtime = nil
	c.owned = map[string]agent.ToolDescriptor{}
	c.mu.Unlock()
	if runtime != nil {
		return runtime.Close(ctx)
	}
	return nil
}

func (c *lightningController) reject(logContext string, err error) error {
	log.Printf("[lightning] %s candidate rejected; keeping previous runtime: %v", logContext, err)
	return err
}

func validateLightningRegistrations(items []agent.ToolRegistration) (map[string]agent.ToolRegistration, map[string]agent.ToolDescriptor, error) {
	registrations := map[string]agent.ToolRegistration{}
	descriptors := map[string]agent.ToolDescriptor{}
	for _, item := range items {
		name := strings.TrimSpace(item.Descriptor.Name)
		if name != "l402_fetch" {
			return nil, nil, fmt.Errorf("unexpected Lightning tool registration %q", name)
		}
		if item.Func == nil {
			return nil, nil, fmt.Errorf("Lightning tool %s has no implementation", name)
		}
		if _, duplicate := registrations[name]; duplicate {
			return nil, nil, fmt.Errorf("duplicate Lightning tool registration %s", name)
		}
		registrations[name], descriptors[name] = item, item.Descriptor
	}
	return registrations, descriptors, nil
}
