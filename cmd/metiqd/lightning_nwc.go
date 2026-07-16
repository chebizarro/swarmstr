package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/config"
	"metiq/internal/lightning"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/secrets"
	"metiq/internal/store/state"
)

type resolvedStandaloneNWC struct {
	WalletID string
	URI      string
	Source   string
	Timeout  time.Duration
}

type nwcOperationClient interface {
	toolbuiltin.NWCToolClient
	PayInvoiceTool(context.Context, string, int64) (map[string]any, error)
	Close() error
}

type nwcClientFactory func(lightning.NWCClientConfig) (nwcOperationClient, error)

// liveNWCToolClient deliberately retains config and secret resolvers rather than
// a resolved URI. Each invocation receives a coherent config snapshot and a
// short-lived NWC client, so credential rotation and removal take effect without
// leaving old wallet secrets usable.
type liveNWCToolClient struct {
	snapshot  func() state.ConfigDoc
	secrets   *secrets.Store
	hubFunc   func() *nostruntime.NostrHub
	keyer     nostr.Keyer
	relays    []string
	newClient nwcClientFactory
}

func newLiveNWCToolClient(
	snapshot func() state.ConfigDoc,
	store *secrets.Store,
	hubFunc func() *nostruntime.NostrHub,
	keyer nostr.Keyer,
	relays []string,
) *liveNWCToolClient {
	return &liveNWCToolClient{
		snapshot: snapshot,
		secrets:  store,
		hubFunc:  hubFunc,
		keyer:    keyer,
		relays:   append([]string(nil), relays...),
		newClient: func(cfg lightning.NWCClientConfig) (nwcOperationClient, error) {
			return lightning.NewNWCClient(cfg)
		},
	}
}

func (c *liveNWCToolClient) Request(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	client, _, found, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("NWC is not configured")
	}
	defer client.Close()
	return client.Request(ctx, method, params)
}

func (c *liveNWCToolClient) PayInvoiceTool(ctx context.Context, invoice string, amountMSat int64) (map[string]any, error) {
	client, _, found, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("NWC is not configured")
	}
	defer client.Close()
	return client.PayInvoiceTool(ctx, invoice, amountMSat)
}

func (c *liveNWCToolClient) client(ctx context.Context) (nwcOperationClient, string, bool, error) {
	if c == nil || c.snapshot == nil {
		return nil, "", false, fmt.Errorf("NWC config snapshot is unavailable")
	}
	resolved, found, err := resolveStandaloneNWC(ctx, c.secrets, c.snapshot())
	if err != nil || !found {
		return nil, resolved.Source, found, err
	}
	factory := c.newClient
	if factory == nil {
		return nil, resolved.Source, true, fmt.Errorf("NWC client factory is unavailable")
	}
	client, err := factory(lightning.NWCClientConfig{
		ID:        resolved.WalletID,
		URI:       resolved.URI,
		Relays:    append([]string(nil), c.relays...),
		Keyer:     c.keyer,
		Timeout:   resolved.Timeout,
		Transport: lightning.NewHubNWCTransport(c.hubFunc),
	})
	if err != nil {
		return nil, resolved.Source, true, fmt.Errorf("initialize NWC client from %s: %w", resolved.Source, err)
	}
	if client == nil {
		return nil, resolved.Source, true, fmt.Errorf("initialize NWC client from %s: client is unavailable", resolved.Source)
	}
	return client, resolved.Source, true, nil
}

// registerConfiguredNWCTools exposes the compatibility tools only when the
// initial config has a resolvable, valid NWC client. The live client still
// re-resolves every invocation, so later removal fails closed.
func registerConfiguredNWCTools(ctx context.Context, registry *agent.ToolRegistry, client *liveNWCToolClient) (bool, string, error) {
	if registry == nil || client == nil {
		return false, "", fmt.Errorf("NWC tool assembly is unavailable")
	}
	initial, source, found, err := client.client(ctx)
	if err != nil || !found {
		return false, source, err
	}
	if err := initial.Close(); err != nil {
		return false, source, fmt.Errorf("close NWC client validation instance: %w", err)
	}
	toolbuiltin.RegisterNWCTools(registry, toolbuiltin.NWCToolOpts{Client: client})
	return true, source, nil
}

func resolveStandaloneNWC(ctx context.Context, store *secrets.Store, doc state.ConfigDoc) (resolvedStandaloneNWC, bool, error) {
	cfg, err := config.ParseLightningConfigExtra(doc.Extra)
	if err != nil {
		return resolvedStandaloneNWC{}, false, fmt.Errorf("parse NWC configuration: %w", err)
	}
	walletID, wallet, selected := selectStandaloneNWCWallet(cfg)
	uri, source, found, err := resolveConfiguredNWCURI(ctx, store, doc.Extra, wallet)
	timeout := time.Duration(config.DefaultLightningWalletTimeoutMS) * time.Millisecond
	if selected {
		timeout = time.Duration(wallet.EffectiveTimeoutMS()) * time.Millisecond
	}
	if walletID == "" {
		walletID = "nwc"
	}
	resolved := resolvedStandaloneNWC{
		WalletID: walletID,
		URI:      uri,
		Source:   source,
		Timeout:  timeout,
	}
	if err != nil {
		return resolved, found, err
	}
	return resolved, found, nil
}

// selectStandaloneNWCWallet chooses a canonical wallet without depending on Go
// map iteration order. An enabled L402 NWC payer wins, followed by a wallet
// named "default", followed by the sole configured NWC wallet.
func selectStandaloneNWCWallet(cfg config.LightningConfig) (string, config.LightningWalletConfig, bool) {
	if cfg.L402.Enabled {
		if wallet, ok := findLightningWallet(cfg.Wallets, strings.TrimSpace(cfg.L402.Payer)); ok &&
			strings.EqualFold(strings.TrimSpace(wallet.Type), config.LightningWalletTypeNWC) {
			return strings.TrimSpace(cfg.L402.Payer), wallet, true
		}
	}
	for id, wallet := range cfg.Wallets {
		if strings.EqualFold(strings.TrimSpace(id), "default") &&
			strings.EqualFold(strings.TrimSpace(wallet.Type), config.LightningWalletTypeNWC) {
			return strings.TrimSpace(id), wallet, true
		}
	}
	var selectedID string
	var selected config.LightningWalletConfig
	count := 0
	for id, wallet := range cfg.Wallets {
		if !strings.EqualFold(strings.TrimSpace(wallet.Type), config.LightningWalletTypeNWC) {
			continue
		}
		selectedID, selected = strings.TrimSpace(id), wallet
		count++
	}
	if count == 1 {
		return selectedID, selected, true
	}
	return "", config.LightningWalletConfig{}, false
}

// resolveConfiguredNWCURI is the shared secret-aware layer used by both L402
// and the standalone nwc_* compatibility tools. A broken higher-priority
// reference fails closed rather than falling through to another wallet.
func resolveConfiguredNWCURI(
	ctx context.Context,
	store *secrets.Store,
	extra map[string]any,
	wallet config.LightningWalletConfig,
) (string, string, bool, error) {
	uri, source, found := config.ResolveNWCURI(extra, wallet)
	if !found {
		return "", "", false, nil
	}
	if source == "NWC_CONNECTION_STRING" || !isExplicitCredentialReference(uri) {
		return uri, source, true, nil
	}
	if store == nil {
		return "", source, true, fmt.Errorf("NWC credential from %s is unavailable: secret store is unavailable", source)
	}
	resolved, err := store.ResolveBytes(ctx, secrets.CredentialSource{
		Ref: uri, Encoding: secrets.CredentialEncodingText,
	})
	if err != nil {
		return "", source, true, fmt.Errorf("NWC credential from %s is unavailable: %w", source, err)
	}
	return string(resolved), source, true, nil
}
